package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// balancePreauthorizationGuardForRequest resolves the active preauthorization
// guard from ctx first, then falls back to the Gin request context. Returning
// the context that carried the guard ensures downstream ObserveStreamingOutput
// calls inherit the right cancellation/value lineage.
func balancePreauthorizationGuardForRequest(ctx context.Context, c *gin.Context) (context.Context, *BalancePreauthorizationGuard) {
	if guard, ok := BalancePreauthorizationGuardFromContext(ctx); ok {
		return nonNilContext(ctx), guard
	}
	if c != nil && c.Request != nil {
		reqCtx := c.Request.Context()
		if guard, ok := BalancePreauthorizationGuardFromContext(reqCtx); ok {
			return reqCtx, guard
		}
	}
	return nonNilContext(ctx), nil
}

// IsBalanceHoldTopUpError reports whether err came from a mid-stream balance
// preauthorization hold top-up failure (insufficient balance, wallet outage, or
// stale guard ownership). Callers must preserve partial billable usage and must
// NOT fail over, penalize upstream account affinity/health, or refund delivered output.
func IsBalanceHoldTopUpError(err error) bool {
	return isBalanceHoldTopUpError(err)
}

// isBalanceHoldTopUpError reports whether err came from a mid-stream balance
// preauthorization hold top-up failure (insufficient balance, wallet outage, or
// stale guard ownership). Callers must preserve partial billable usage and must
// NOT fail over or refund 100% of already-delivered output.
func isBalanceHoldTopUpError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrBalanceWithholdingFailed) ||
		errors.Is(err, ErrBillingServiceUnavailable) ||
		errors.Is(err, ErrBalancePreauthorizationOwnershipTransferred)
}

// balanceHoldAbortClientError maps a hold top-up abort to the HTTP status,
// error type, and client-safe message used when no response has been written yet.
// Aligns with handler billingErrorDetails (429 insufficient_quota / 503 billing_service_error).
func balanceHoldAbortClientError(err error) (status int, errType string, message string) {
	if errors.Is(err, ErrBalanceWithholdingFailed) {
		return http.StatusTooManyRequests, "insufficient_quota", ErrBalanceWithholdingFailed.Message
	}
	return http.StatusServiceUnavailable, "billing_service_error", ErrBillingServiceUnavailable.Message
}

// anthropicEventOutputBytes returns the billable output byte delta carried by
// a decoded Anthropic stream event (text, thinking, tool name / partial JSON).
func anthropicEventOutputBytes(event *apicompat.AnthropicStreamEvent) int {
	if event == nil {
		return 0
	}
	switch event.Type {
	case "content_block_start":
		if block := event.ContentBlock; block != nil {
			switch block.Type {
			case "text":
				return len(block.Text)
			case "thinking":
				return len(block.Thinking)
			case "tool_use", "server_tool_use":
				return len(block.Name)
			}
		}
	case "content_block_delta":
		if delta := event.Delta; delta != nil {
			switch delta.Type {
			case "text_delta":
				return len(delta.Text)
			case "thinking_delta":
				return len(delta.Thinking)
			case "input_json_delta":
				return len(delta.PartialJSON)
			}
		}
	}
	return 0
}

// responsesEventOutputBytes returns the billable output byte delta carried by
// a decoded OpenAI Responses stream event.
func responsesEventOutputBytes(event *apicompat.ResponsesStreamEvent) int {
	if event == nil {
		return 0
	}
	switch strings.TrimSpace(event.Type) {
	case "response.output_text.delta",
		"response.refusal.delta",
		"response.function_call_arguments.delta",
		"response.custom_tool_call_input.delta",
		"response.reasoning_summary_text.delta",
		"response.reasoning_text.delta",
		"response.code_interpreter_call_code.delta",
		"response.mcp_call_arguments.delta":
		return len(event.Delta)
	case "response.content_part.added":
		if event.Part != nil {
			return len(event.Part.Text) + len(event.Part.Refusal)
		}
	case "response.output_item.added":
		if event.Item != nil {
			return len(event.Item.Name)
		}
	}
	return 0
}

// chatChunkOutputBytes returns the billable output byte delta carried by a
// Chat Completions streaming chunk (content, refusal, reasoning, tool calls).
func chatChunkOutputBytes(chunk *apicompat.ChatCompletionsChunk) int {
	if chunk == nil {
		return 0
	}
	total := 0
	for _, choice := range chunk.Choices {
		delta := choice.Delta
		if delta.Content != nil {
			total += len(*delta.Content)
		}
		if delta.Refusal != nil {
			total += len(*delta.Refusal)
		}
		switch {
		case delta.ReasoningContent != nil:
			total += len(*delta.ReasoningContent)
		case delta.Reasoning != nil:
			total += len(*delta.Reasoning)
		}
		for _, tc := range delta.ToolCalls {
			total += len(tc.Function.Name) + len(tc.Function.Arguments)
		}
	}
	return total
}

// responsesSSEDataOutputBytes returns the billable output byte delta carried by
// a raw OpenAI Responses or Chat Completions SSE data payload.
// Non-delta Responses events (.done, content_part.done, output_item.done,
// response.completed, response.done, etc.) and [DONE] always return 0.
func responsesSSEDataOutputBytes(eventType string, data []byte) int {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("[DONE]")) {
		return 0
	}
	et := strings.TrimSpace(eventType)
	if et == "" {
		et = strings.TrimSpace(gjson.GetBytes(trimmed, "type").String())
	}
	switch et {
	case "response.output_text.delta",
		"response.refusal.delta",
		"response.function_call_arguments.delta",
		"response.custom_tool_call_input.delta",
		"response.reasoning_summary_text.delta",
		"response.reasoning_text.delta",
		"response.code_interpreter_call_code.delta",
		"response.mcp_call_arguments.delta":
		return len(gjson.GetBytes(trimmed, "delta").String())
	case "response.content_part.added":
		total := 0
		if v := gjson.GetBytes(trimmed, "part.text"); v.Type == gjson.String {
			total += len(v.String())
		}
		if v := gjson.GetBytes(trimmed, "part.refusal"); v.Type == gjson.String {
			total += len(v.String())
		}
		return total
	case "response.output_item.added":
		return len(gjson.GetBytes(trimmed, "item.name").String())
	}
	// Never count non-delta Responses frames (.done, content_part.done,
	// output_item.done, response.completed, response.done) or let them fall
	// through to the Chat Completions choices[].delta extractor.
	if strings.HasPrefix(et, "response.") {
		return 0
	}
	if bytes.Contains(trimmed, []byte(`"delta"`)) {
		total := 0
		gjson.GetBytes(trimmed, "choices").ForEach(func(_, choice gjson.Result) bool {
			delta := choice.Get("delta")
			if v := delta.Get("content"); v.Type == gjson.String {
				total += len(v.String())
			}
			if v := delta.Get("refusal"); v.Type == gjson.String {
				total += len(v.String())
			}
			if v := delta.Get("reasoning_content"); v.Type == gjson.String {
				total += len(v.String())
			} else if v := delta.Get("reasoning"); v.Type == gjson.String {
				total += len(v.String())
			}
			delta.Get("tool_calls").ForEach(func(_, call gjson.Result) bool {
				if v := call.Get("function.name"); v.Type == gjson.String {
					total += len(v.String())
				}
				if v := call.Get("function.arguments"); v.Type == gjson.String {
					total += len(v.String())
				}
				return true
			})
			if v := delta.Get("function_call.name"); v.Type == gjson.String {
				total += len(v.String())
			}
			if v := delta.Get("function_call.arguments"); v.Type == gjson.String {
				total += len(v.String())
			}
			return true
		})
		return total
	}
	return 0
}

// readUpstreamResponseBodyWithBalanceGuard reads a non-streaming upstream body
// while incrementally observing SSE output deltas against the active balance
// preauthorization guard when inspectSSE is true (e.g. buffered upstream SSE-to-JSON).
// When no guard is active or inspectSSE is false, it delegates directly to
// ReadUpstreamResponseBody with zero extra overhead.
func readUpstreamResponseBodyWithBalanceGuard(
	ctx context.Context,
	resp *http.Response,
	cfg *config.Config,
	c *gin.Context,
	onTooLarge TooLargeWriter,
	inspectSSE bool,
) ([]byte, *OpenAIUsage, int, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil, 0, errors.New("response body is nil")
	}
	guardCtx, guard := balancePreauthorizationGuardForRequest(ctx, c)
	if !inspectSSE || guard == nil {
		body, err := ReadUpstreamResponseBody(resp.Body, cfg, c, onTooLarge)
		return body, nil, 0, err
	}

	maxBytes := resolveUpstreamResponseReadLimit(cfg)
	limited := io.LimitReader(resp.Body, maxBytes+1)
	reader := bufio.NewReader(limited)

	var buf bytes.Buffer
	var usage OpenAIUsage
	acc := apicompat.NewBufferedResponseAccumulator()
	var chatTally compatChatTally
	pendingEventType := ""

	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			_, _ = buf.WriteString(line)
			if int64(buf.Len()) > maxBytes {
				setOpsUpstreamError(c, http.StatusBadGateway, "upstream response too large", "")
				if onTooLarge != nil {
					onTooLarge(c)
				}
				return nil, nil, 0, fmt.Errorf("%w: limit=%d", ErrUpstreamResponseBodyTooLarge, maxBytes)
			}

			trimmedLine := strings.TrimRight(line, "\r\n")
			if eventType, ok := extractOpenAISSEEventLine(trimmedLine); ok {
				pendingEventType = eventType
			} else if trimmedLine == "" {
				pendingEventType = ""
			} else if payload, ok := extractOpenAISSEDataLine(trimmedLine); ok {
				trimmedPayload := strings.TrimSpace(payload)
				if trimmedPayload != "" && trimmedPayload != "[DONE]" {
					dataBytes := []byte(trimmedPayload)
					eventType := effectiveOpenAISSEEventType(dataBytes, pendingEventType)
					observePassthroughStreamOutput(acc, eventType, dataBytes)
					if bytes.Contains(dataBytes, []byte(`"delta"`)) {
						chatTally.AddPayload(trimmedPayload)
					}
					if bytes.Contains(dataBytes, []byte(`"usage"`)) {
						if parsedUsage, parsed := extractOpenAIUsageFromJSONBytes(dataBytes); parsed {
							mergeOpenAIUsageNonZero(&usage, parsedUsage)
						}
					}
					if n := responsesSSEDataOutputBytes(eventType, dataBytes); n > 0 {
						if topUpErr := guard.ObserveStreamingOutput(guardCtx, n); topUpErr != nil {
							_ = resp.Body.Close()
							est := estimateIncompleteStreamOutputTokens(acc) + chatTally.EstimateTokens()
							return buf.Bytes(), &usage, est, wrapStreamOutputHoldTopUpFailure(topUpErr)
						}
					}
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			if errors.Is(readErr, errOpenAIUpstreamWaitLimit) {
				return buf.Bytes(), nil, 0, readErr
			}
			return nil, nil, 0, readErr
		}
	}

	return buf.Bytes(), nil, 0, nil
}
