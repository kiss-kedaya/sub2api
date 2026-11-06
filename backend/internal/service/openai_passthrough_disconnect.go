package service

import (
	"encoding/json"
	"errors"
	"fmt"

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

// passthroughResponseOptions selects what the shared passthrough response handlers
// do when the client leaves.
//
// The zero value is the historic behaviour: keep reading the upstream until it
// answers so its usage can be billed. The Excel relay still depends on that and
// is migrated by its own change. forwardOpenAIPassthrough uses
// closeUpstreamOnClientGone, the 2026-11-06 owner decision: close the upstream the
// moment the client leaves and bill an estimate for what is missing.
type passthroughResponseOptions struct {
	closeUpstreamOnClientGone bool
}

// passthroughClosesUpstreamOnClientGone is what forwardOpenAIPassthrough passes.
var passthroughClosesUpstreamOnClientGone = passthroughResponseOptions{closeUpstreamOnClientGone: true}

// errOpenAIBufferedResponseAbandoned marks a non-stream response whose client left
// while the body was still arriving, so the upstream was closed before the usage
// it would have reported. The buffered handler still returns a nil result with it,
// like every other read failure, so callers that rely on that contract (the Excel
// relay keeps its own usage tap for it) are unaffected.
var errOpenAIBufferedResponseAbandoned = errors.New("upstream response abandoned after client disconnect")

// newOpenAIBufferedResponseAbandonedError wraps the read failure that the closed
// upstream caused.
func newOpenAIBufferedResponseAbandonedError(readErr error) error {
	return fmt.Errorf("%w: %w", errOpenAIBufferedResponseAbandoned, readErr)
}

// abandonedPassthroughBufferedUsage bills the input of a non-stream request whose
// client left while the response body was still arriving. The upstream had accepted
// the request, but no usage was reported and no output was observed, so only the
// input side is estimated. It returns the usage to bill, empty when the estimate
// has nothing to bill.
func abandonedPassthroughBufferedUsage(account *Account, requestBody []byte, model string, err error) *OpenAIUsage {
	usage := &OpenAIUsage{}
	applyIncompleteOpenAIStreamUsage(account, &openaiStreamingResult{
		usage:            usage,
		clientDisconnect: true,
		incompleteStream: true,
	}, requestBody, model, err)
	return usage
}
