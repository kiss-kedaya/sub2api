//go:build unit

package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"

	"github.com/stretchr/testify/require"
)

func TestOpenAIR4NativeBufferedThinkingRecovery(t *testing.T) {
	wire := gaps3AnthropicStart + gaps3AnthropicError +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"synthetic thinking\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		strings.ReplaceAll(gaps3AnthropicStop, "end_turn", "max_tokens")
	for _, route := range []string{"responses", "chat"} {
		t.Run(route, func(t *testing.T) {
			c, rec := gaps3Context()
			svc := newNonStreamingFailoverService()
			var result *OpenAIForwardResult
			var err error
			if route == "responses" {
				result, err = svc.handleResponsesBufferedFromNativeAnthropic(explicitErrorResponse(wire, "text/event-stream"), c, "model", "model", "model", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
			} else {
				result, err = svc.handleCCBufferedFromNativeAnthropic(explicitErrorResponse(wire, "text/event-stream"), c, "model", "model", "model", nil, time.Now(), true)
			}
			require.NoError(t, err)
			assertTransientFallbackBilled(t, result)
			require.Contains(t, rec.Body.String(), "synthetic thinking")
		})
	}
}

func TestOpenAIR4BufferedRecovery(t *testing.T) {
	start := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_synthetic\",\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n"
	frames := map[string]string{
		"completed_side_note": start + "data: {\"type\":\"response.completed\",\"error\":{\"message\":\"synthetic relay note\"},\"response\":{\"id\":\"resp_synthetic\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"synthetic real answer\"}]}],\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n",
	}
	for _, tool := range []string{`{"type":"function_call","call_id":"call_synthetic","name":"synthetic_tool","status":"completed"}`, `{"type":"custom_tool_call","call_id":"call_synthetic","name":"synthetic_tool","input":"","status":"completed"}`} {
		frames[tool] = start + "data: {\"error\":{\"message\":\"synthetic rejection\"}}\n\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":" + tool + "}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_synthetic\",\"status\":\"completed\",\"output\":[" + tool + "],\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n"
	}
	frames["reasoning_recovery"] = start + "data: {\"error\":{\"message\":\"synthetic rejection\"}}\n\ndata: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"synthetic thinking\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_synthetic\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n"
	for _, passthrough := range []bool{false, true} {
		for name, wire := range frames {
			t.Run(fmt.Sprintf("passthrough=%t/%s", passthrough, name), func(t *testing.T) {
				c, rec := newNonStreamingFailoverContext(t)
				svc := newNonStreamingFailoverService()
				var usage *OpenAIUsage
				var free bool
				var err error
				if passthrough {
					result, handleErr := svc.handleNonStreamingResponsePassthrough(c.Request.Context(), explicitErrorResponse(wire, "text/event-stream"), c, newNonStreamingFailoverAccount(), "model", "model")
					err = handleErr
					if result != nil {
						usage, free = result.usage, result.nonBillableUpstreamError
					}
				} else {
					result, handleErr := svc.handleNonStreamingResponse(c.Request.Context(), explicitErrorResponse(wire, "text/event-stream"), c, newNonStreamingFailoverAccount(), "model", "model")
					err = handleErr
					if result != nil {
						usage, free = result.usage, result.nonBillableUpstreamError
					}
				}
				require.NoError(t, err, rec.Body.String())
				require.NotNil(t, usage)
				require.Equal(t, OpenAIUsage{InputTokens: 1000, OutputTokens: 2}, *usage)
				require.False(t, free)
				t.Logf("R4 usage=%+v free=%t status=%d body=%s", *usage, free, rec.Code, rec.Body.String())
			})
		}
	}
}
