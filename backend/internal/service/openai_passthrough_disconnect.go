package service

import (
	"context"
	"encoding/json"
	"io"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

// Client disconnect on the OpenAI passthrough and image paths (owner decision
// 2026-11-06): the upstream connection is closed the moment the client leaves,
// stream or not. What the upstream already reported stays authoritative; what it
// produced without reporting is billed from an estimate (see
// incomplete_request_usage.go and openai_stream_incomplete_usage.go).
//
// These requests run on a context detached from the client so that billing and
// failover outlive it. That also means nothing cancels a quiet upstream when the
// client leaves, so the response body is closed explicitly.

// closeOpenAIBodyOnClientGone closes body as soon as any of the contexts ends and
// returns a function that stops the watch. The close unblocks a Read that waits on
// a quiet upstream; frames a scanner already buffered are still delivered first.
// Closing is idempotent, so overlapping watches (and the caller's own close) are
// harmless.
func closeOpenAIBodyOnClientGone(body io.Closer, contexts ...context.Context) (stop func()) {
	if body == nil {
		return func() {}
	}
	stops := make([]func() bool, 0, len(contexts))
	for _, ctx := range contexts {
		if ctx == nil || ctx.Done() == nil {
			continue
		}
		stops = append(stops, context.AfterFunc(ctx, func() { _ = body.Close() }))
	}
	return func() {
		for _, stopWatch := range stops {
			stopWatch()
		}
	}
}

// openAIClientGone reports whether any of the contexts has ended.
func openAIClientGone(contexts ...context.Context) bool {
	for _, ctx := range contexts {
		if ctx != nil && ctx.Err() != nil {
			return true
		}
	}
	return false
}

// passthroughStreamEventContributesOutput reports whether a Responses stream event
// carries content the upstream bills as output. It is a superset of the events the
// non-passthrough stream accumulates: refusal text, raw reasoning text, custom tool
// input and the "done" events that complete a function call are billed output too.
func passthroughStreamEventContributesOutput(eventType string) bool {
	switch eventType {
	case "response.output_text.delta",
		"response.refusal.delta",
		"response.output_item.added",
		"response.output_item.done",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.custom_tool_call_input.delta",
		"response.custom_tool_call_input.done",
		"response.reasoning_summary_text.delta",
		"response.reasoning_text.delta":
		return true
	default:
		return false
	}
}

// observePassthroughStreamOutput feeds one stream frame to the output accumulator
// that an incomplete stream is estimated from. Frames that carry no output are
// skipped without being parsed, so the passthrough hot path stays cheap.
func observePassthroughStreamOutput(acc *apicompat.BufferedResponseAccumulator, eventType string, data []byte) {
	if acc == nil || !passthroughStreamEventContributesOutput(eventType) {
		return
	}
	var event apicompat.ResponsesStreamEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return
	}
	if event.Type == "" {
		event.Type = eventType
	}
	acc.ProcessEvent(&event)
}

// applyIncompletePassthroughStreamUsage fills an incomplete passthrough stream's
// usage from the estimates, with the same rules as the non-passthrough stream.
func applyIncompletePassthroughStreamUsage(account *Account, result *openaiStreamingResultPassthrough, requestBody []byte, model string, streamErr error) {
	if result == nil {
		return
	}
	applyIncompleteOpenAIStreamUsage(account, &openaiStreamingResult{
		usage:                    result.usage,
		responseID:               result.responseID,
		nonBillableUpstreamError: result.nonBillableUpstreamError,
		clientDisconnect:         result.clientDisconnect,
		incompleteStream:         result.incompleteStream,
		incompleteOutputTokens:   result.incompleteOutputTokens,
	}, requestBody, model, streamErr)
}

// applyIncompletePassthroughBufferedUsage bills the input of a non-stream request
// whose client left while the response body was still arriving. The upstream had
// accepted the request, but no usage was reported and no output was observed, so
// only the input side is estimated.
func applyIncompletePassthroughBufferedUsage(account *Account, result *openaiNonStreamingResultPassthrough, requestBody []byte, model string, err error) {
	if result == nil {
		return
	}
	applyIncompleteOpenAIStreamUsage(account, &openaiStreamingResult{
		usage:            result.usage,
		responseID:       result.responseID,
		clientDisconnect: result.clientDisconnect,
		incompleteStream: true,
	}, requestBody, model, err)
}
