package service

import (
	"context"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
)

// Client disconnect on the OpenAI passthrough and image paths (owner decision
// 2026-11-06): the upstream connection is closed the moment the client leaves,
// stream or not. What the upstream already reported stays authoritative; what it
// produced without reporting is billed from an estimate where the protocol allows
// one (see incomplete_request_usage.go and openai_stream_incomplete_usage.go), and
// never invented where it does not (images).
//
// These requests run on a context detached from the client so that token refresh,
// billing and failover outlive it. That also means nothing cancels a quiet upstream
// when the client leaves, so the helpers here and openai_gateway_header_cancel.go
// watch the client's own context.

// openAIRequestContext returns the client's request context, or nil when the
// gin context carries no request.
func openAIRequestContext(c *gin.Context) context.Context {
	if c == nil || c.Request == nil {
		return nil
	}
	return c.Request.Context()
}

// openAIClientContext returns the context whose end means the client left: the
// request context when there is one, otherwise ctx.
func openAIClientContext(ctx context.Context, c *gin.Context) context.Context {
	if requestCtx := openAIRequestContext(c); requestCtx != nil {
		return requestCtx
	}
	return ctx
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
