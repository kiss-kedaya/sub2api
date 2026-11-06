package service

import (
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

// Estimation helpers for the OpenAI passthrough path on client disconnect (owner
// decision 2026-11-06). The shared client-gone helpers live in
// openai_client_gone.go.

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
