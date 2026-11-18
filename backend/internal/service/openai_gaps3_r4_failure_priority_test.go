//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIR4FailedStatusCannotClearPrelude(t *testing.T) {
	wire := gaps3ResponsesStart + gaps3ResponsesError +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_synthetic\",\"status\":\"failed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"synthetic answer\"}]}],\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n"
	for _, route := range []string{"responses", "passthrough", "chat", "messages"} {
		t.Run(route, func(t *testing.T) {
			c, _ := gaps3Context()
			svc := newNonStreamingFailoverService()
			resp, account := explicitErrorResponse(wire, "text/event-stream"), newNonStreamingFailoverAccount()
			var result *OpenAIForwardResult
			switch route {
			case "responses":
				v, _ := svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "model", "model")
				require.NotNil(t, v)
				result = &OpenAIForwardResult{Usage: *v.usage, NonBillableUpstreamError: v.nonBillableUpstreamError}
			case "passthrough":
				v, _ := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "model", "model")
				require.NotNil(t, v)
				result = &OpenAIForwardResult{Usage: *v.usage, NonBillableUpstreamError: v.nonBillableUpstreamError}
			case "chat":
				result, _ = svc.handleChatStreamingResponse(resp, c, account, "model", "model", "model", time.Now(), 0)
			case "messages":
				result, _ = svc.handleAnthropicStreamingResponse(resp, c, account, "model", "model", "model", time.Now())
			}
			require.NotNil(t, result)
			require.True(t, result.NonBillableUpstreamError)
		})
	}
}
