//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const gaps3ResponsesStart = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_synthetic\",\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n"
const gaps3ResponsesAnswer = "data: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_synthetic\",\"delta\":\"synthetic answer\"}\n\n"
const gaps3ResponsesError = "data: {\"error\":{\"type\":\"invalid_request_error\",\"message\":\"synthetic rejection\"},\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}\n\n"
const gaps3ResponsesStop = "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_synthetic\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n"

func gaps3Context() (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	return c, rec
}

func TestOpenAIGaps3ResponsesUntypedStreamError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, route := range []string{"responses", "passthrough", "messages", "chat"} {
		t.Run(route, func(t *testing.T) {
			c, rec := gaps3Context()
			resp := explicitErrorResponse(gaps3ResponsesStart+gaps3ResponsesAnswer+gaps3ResponsesError+"data: [DONE]\n\n", "text/event-stream")
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			var result *OpenAIForwardResult
			var err error
			switch route {
			case "responses":
				var v *openaiStreamingResult
				v, err = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
				require.NotNil(t, v)
				result = &OpenAIForwardResult{Usage: *v.usage, NonBillableUpstreamError: v.nonBillableUpstreamError}
			case "passthrough":
				var v *openaiStreamingResultPassthrough
				v, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
				require.NotNil(t, v)
				result = &OpenAIForwardResult{Usage: *v.usage, NonBillableUpstreamError: v.nonBillableUpstreamError}
			case "messages":
				result, err = svc.handleAnthropicStreamingResponse(resp, c, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", time.Now())
			case "chat":
				result, err = svc.handleChatStreamingResponse(resp, c, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", time.Now(), 0)
			}
			require.Error(t, err)
			assertExplicitUpstreamErrorFree(t, result)
			require.Contains(t, rec.Body.String(), "synthetic rejection")
			require.NotContains(t, rec.Body.String(), "response.completed")
			require.NotContains(t, rec.Body.String(), "message_stop")
			require.Equal(t, 1, strings.Count(rec.Body.String(), "synthetic rejection"))
		})
	}
}

func TestOpenAIGaps3ResponsesSuccessRequiresAnswer(t *testing.T) {
	for _, route := range []string{"responses", "passthrough", "messages", "chat"} {
		for _, answer := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/answer=%t", route, answer), func(t *testing.T) {
				c, rec := gaps3Context()
				wire := gaps3ResponsesStart + gaps3ResponsesError
				if answer {
					wire += gaps3ResponsesAnswer
				}
				wire += gaps3ResponsesStop + "data: [DONE]\n\n"
				resp := explicitErrorResponse(wire, "text/event-stream")
				svc := &OpenAIGatewayService{cfg: &config.Config{}}
				account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
				var result *OpenAIForwardResult
				var err error
				if route == "responses" {
					var v *openaiStreamingResult
					v, err = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
					require.NotNil(t, v)
					result = &OpenAIForwardResult{Usage: *v.usage, NonBillableUpstreamError: v.nonBillableUpstreamError}
				} else if route == "passthrough" {
					var v *openaiStreamingResultPassthrough
					v, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
					require.NotNil(t, v)
					result = &OpenAIForwardResult{Usage: *v.usage, NonBillableUpstreamError: v.nonBillableUpstreamError}
				} else if route == "messages" {
					result, err = svc.handleAnthropicStreamingResponse(resp, c, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", time.Now())
				} else {
					result, err = svc.handleChatStreamingResponse(resp, c, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", time.Now(), 0)
				}
				if answer {
					require.NoError(t, err)
					assertTransientFallbackBilled(t, result)
					require.Contains(t, rec.Body.String(), "synthetic answer")
					if route == "responses" || route == "passthrough" {
						require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"response.output_text.delta"`))
					}
				} else {
					require.Error(t, err)
					assertExplicitUpstreamErrorFree(t, result)
					require.NotContains(t, rec.Body.String(), "response.completed")
				}
			})
		}
	}
}

func TestOpenAIGaps3ResponsesToolSuccess(t *testing.T) {
	const tool = "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_synthetic\",\"name\":\"synthetic_tool\",\"arguments\":\"\"}}\n\n"
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%t", passthrough), func(t *testing.T) {
			c, rec := gaps3Context()
			resp := explicitErrorResponse(gaps3ResponsesStart+gaps3ResponsesError+tool+gaps3ResponsesStop, "text/event-stream")
			svc := &OpenAIGatewayService{}
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			var result *OpenAIForwardResult
			if passthrough {
				v, err := svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
				require.NoError(t, err)
				require.NotNil(t, v)
				result = &OpenAIForwardResult{Usage: *v.usage, NonBillableUpstreamError: v.nonBillableUpstreamError}
			} else {
				v, err := svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
				require.NoError(t, err)
				require.NotNil(t, v)
				result = &OpenAIForwardResult{Usage: *v.usage, NonBillableUpstreamError: v.nonBillableUpstreamError}
			}
			assertTransientFallbackBilled(t, result)
			require.Contains(t, rec.Body.String(), "synthetic_tool")
		})
	}
}

func TestOpenAIGaps3ChatFallbackOutcome(t *testing.T) {
	const start = "data: {\"id\":\"chat_synthetic\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":2}}\n\n"
	const failure = "data: {\"error\":{\"message\":\"synthetic rejection\"}}\n\n"
	const content = "data: {\"choices\":[{\"delta\":{\"content\":\"synthetic answer\"}}]}\n\n"
	for _, route := range []string{"responses", "messages"} {
		for _, mode := range []string{"error_done", "empty_success", "error_finish", "answer_success", "answer_error_finish", "disconnect"} {
			t.Run(route+"/"+mode, func(t *testing.T) {
				wire := start
				if mode != "disconnect" {
					wire += failure
				}
				if strings.HasPrefix(mode, "answer") || mode == "disconnect" {
					wire += content
				}
				if mode == "empty_success" || mode == "answer_success" {
					wire += "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"
				} else if strings.Contains(mode, "finish") {
					wire += "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"error\"}]}\n\n"
				}
				if mode != "disconnect" {
					wire += "data: [DONE]\n\n"
				}
				c, rec := gaps3Context()
				svc := &OpenAIGatewayService{}
				resp := explicitErrorResponse(wire, "text/event-stream")
				var result *OpenAIForwardResult
				var err error
				if route == "responses" {
					result, err = svc.streamChatCompletionsAsResponses(c, resp, "gpt-5.1", nil, nil, false, nil, nil, "gpt-5.1", "gpt-5.1", nil, nil, time.Now())
				} else {
					result, err = svc.streamChatCompletionsAsAnthropic(c, resp, "gpt-5.1", "gpt-5.1", "gpt-5.1", nil, nil, time.Now())
				}
				if mode == "answer_success" || mode == "disconnect" {
					require.NoError(t, err)
					assertTransientFallbackBilled(t, result)
					require.Contains(t, rec.Body.String(), "synthetic answer")
				} else {
					require.ErrorContains(t, err, "upstream response failed:")
					assertExplicitUpstreamErrorFree(t, result)
					require.NotContains(t, rec.Body.String(), "response.completed")
					require.NotContains(t, rec.Body.String(), "message_stop")
					require.Equal(t, 1, strings.Count(rec.Body.String(), "synthetic rejection"))
					require.True(t, IsResponseCommitted(c))
				}
			})
		}
	}
}

const gaps3AnthropicStart = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_synthetic\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"gpt-5.1\",\"content\":[],\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n"
const gaps3AnthropicError = "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"synthetic rejection\"}}\n\n"
const gaps3AnthropicAnswer = "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"synthetic answer\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
const gaps3AnthropicStop = "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"

func TestOpenAIGaps3NativeAnthropicOutcome(t *testing.T) {
	for _, route := range []string{"messages", "responses_stream", "responses_buffered", "chat_stream", "chat_buffered"} {
		for _, mode := range []string{"error", "empty_success", "answer_success", "disconnect", "tool_success", "role_success", "error_header_stop"} {
			t.Run(route+"/"+mode, func(t *testing.T) {
				wire := gaps3AnthropicStart
				if mode != "disconnect" {
					wire += gaps3AnthropicError
				}
				if mode == "answer_success" || mode == "disconnect" {
					wire += gaps3AnthropicAnswer
				}
				if mode == "tool_success" {
					wire += "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool_synthetic\",\"name\":\"synthetic_tool\",\"input\":{}}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
				}
				if mode == "role_success" {
					wire += "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"\"}}\n\n"
				}
				if mode == "error_header_stop" {
					wire += "event: message_stop\n\n"
				}
				if mode == "empty_success" || mode == "answer_success" || mode == "tool_success" || mode == "role_success" {
					wire += gaps3AnthropicStop
				}
				c, rec := gaps3Context()
				svc := &OpenAIGatewayService{}
				resp := explicitErrorResponse(wire, "text/event-stream")
				var result *OpenAIForwardResult
				var err error
				switch route {
				case "messages":
					result, err = svc.handleNativeAnthropicStreamingResponse(context.Background(), resp, c, &Account{ID: 42, Platform: PlatformOpenAI}, "gpt-5.1", "gpt-5.1", "gpt-5.1", nil, time.Now())
				case "responses_stream":
					result, err = svc.handleResponsesStreamingFromNativeAnthropic(resp, c, "gpt-5.1", "gpt-5.1", "gpt-5.1", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
				case "responses_buffered":
					result, err = svc.handleResponsesBufferedFromNativeAnthropic(resp, c, "gpt-5.1", "gpt-5.1", "gpt-5.1", nil, time.Now(), apicompat.ResponsesClientToolMapping{})
				case "chat_stream":
					result, err = svc.handleCCStreamingFromNativeAnthropic(resp, c, "gpt-5.1", "gpt-5.1", "gpt-5.1", nil, time.Now(), true)
				case "chat_buffered":
					result, err = svc.handleCCBufferedFromNativeAnthropic(resp, c, "gpt-5.1", "gpt-5.1", "gpt-5.1", nil, time.Now())
				}
				if mode == "answer_success" || mode == "disconnect" || mode == "tool_success" {
					if route != "messages" || mode != "disconnect" {
						require.NoError(t, err)
					}
					assertTransientFallbackBilled(t, result)
					if mode == "tool_success" {
						require.Contains(t, rec.Body.String(), "synthetic_tool")
					} else {
						require.Contains(t, rec.Body.String(), "synthetic answer")
					}
				} else {
					assertExplicitUpstreamErrorFree(t, result)
					if route != "messages" {
						require.ErrorContains(t, err, "upstream response failed:")
						require.NotContains(t, rec.Body.String(), "response.completed")
						require.NotContains(t, rec.Body.String(), `"finish_reason":"stop"`)
						require.True(t, IsResponseCommitted(c))
					}
					require.Equal(t, 1, strings.Count(rec.Body.String(), "synthetic rejection"))
				}
			})
		}
	}
}
