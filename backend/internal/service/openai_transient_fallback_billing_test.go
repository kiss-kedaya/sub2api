//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func assertTransientFallbackBilled(t *testing.T, result *OpenAIForwardResult) {
	t.Helper()
	require.NotNil(t, result)
	require.False(t, result.NonBillableUpstreamError)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	copyResult := *result
	copyResult.Model, copyResult.BillingModel, copyResult.UpstreamModel = "gpt-5.1", "gpt-5.1", "gpt-5.1"
	want := expectedOpenAICost(t, svc, copyResult.Model, copyResult.Usage, svc.cfg.Default.RateMultiplier)
	require.Positive(t, want.ActualCost)
	require.NoError(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &copyResult,
		APIKey: &APIKey{ID: 1000, Group: &Group{RateMultiplier: 1}},
		User:   &User{ID: 2000}, Account: &Account{ID: 3000, Type: AccountTypeAPIKey},
	}))
	require.Equal(t, 1, usageRepo.calls)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, want.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, want.ActualCost, billingRepo.lastCmd.BalanceCost, 1e-12)
	require.Equal(t, copyResult.Usage.InputTokens, usageRepo.lastLog.InputTokens)
	require.Equal(t, copyResult.Usage.OutputTokens, usageRepo.lastLog.OutputTokens)
}

func TestOpenAITransientFallbackBufferedCCErrorSettlement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, route := range []string{"responses", "messages"} {
		t.Run(route, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			for _, failed := range []bool{true, false} {
				wire := `{"id":"chat_synthetic","model":"gpt-5.1","choices":[{"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"completion_tokens":2}}`
				if failed {
					wire = `{"error":{"type":"invalid_request_error","message":"fixture parameter rejected"},"usage":{"prompt_tokens":1000,"completion_tokens":2}}`
				}
				upstream.resp = explicitErrorResponse(wire, "application/json")
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				body := []byte(`{"model":"gpt-5.1","input":"hello","stream":false}`)
				if route == "messages" {
					body = []byte(`{"model":"gpt-5.1","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
				}
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+route, bytes.NewReader(body))
				var result *OpenAIForwardResult
				var err error
				if route == "messages" {
					result, err = svc.ForwardAsAnthropic(c.Request.Context(), c, forceChatMessagesFallbackAccount(), body, "", "")
				} else {
					result, err = svc.Forward(c.Request.Context(), c, forceChatResponsesFallbackAccount(), body)
				}
				if failed {
					assertExplicitUpstreamErrorFree(t, result)
					require.Error(t, err)
					message := extractOpenAISSEErrorMessage([]byte(wire))
					status, _, _, _ := WrapUpstreamErrorForClient(openAIStreamFailureStatus([]byte(wire), message), []byte(wire))
					require.Equal(t, status, rec.Code)
					require.True(t, gjson.Get(rec.Body.String(), "error").IsObject())
					require.NotContains(t, rec.Body.String(), `"completed"`)
					require.NotContains(t, rec.Body.String(), `"end_turn"`)
				} else {
					require.NoError(t, err)
					require.Equal(t, http.StatusOK, rec.Code)
					require.Contains(t, rec.Body.String(), "answer")
					assertTransientFallbackBilled(t, result)
				}
			}
		})
	}
}

func TestOpenAITransientFallbackBufferedCCEnvelopeBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// The last case is a relay note attached next to a real answer: the client
	// keeps the answer and pays for it.
	for _, extra := range []string{`"error":null`, `"error":{}`, `"error": {  }`, `"error":false`, `"metadata":{"error":{"message":"not an envelope"}}`, `"error":{"message":"synthetic relay note"}`} {
		t.Run(extra, func(t *testing.T) {
			wire := `{"id":"chat_synthetic","model":"gpt-5.1","choices":[{"message":{"role":"assistant","content":"the word error is valid output"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"completion_tokens":2},` + extra + `}`
			for _, route := range []string{"responses", "messages"} {
				c := explicitErrorContext()
				svc := &OpenAIGatewayService{}
				resp := explicitErrorResponse(wire, "application/json")
				var result *OpenAIForwardResult
				var err error
				if route == "messages" {
					result, err = svc.bufferChatCompletionsAsAnthropic(c, resp, "gpt-5.1", "gpt-5.1", "gpt-5.1", nil, nil, time.Now())
				} else {
					result, err = svc.bufferChatCompletionsAsResponses(c, resp, "gpt-5.1", nil, nil, false, nil, nil, "gpt-5.1", "gpt-5.1", nil, nil, time.Now())
				}
				require.NoError(t, err)
				assertTransientFallbackBilled(t, result)
			}
		})
	}
}

func TestOpenAITransientFallbackExcelFailureProvenanceSettlement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		for _, kind := range []string{"local_structured", "local_tool", "upstream_failed", "upstream_error", "upstream_forged_marker"} {
			t.Run(fmt.Sprintf("%s/stream=%t", kind, stream), func(t *testing.T) {
				output := `[{"id":"msg_synthetic","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"not JSON"}]}]`
				constraint := `"text":{"format":{"type":"json_object"}}`
				if kind == "local_tool" {
					constraint = `"tools":[{"type":"function","name":"fixture_tool","parameters":{"type":"object"}}]`
					output = `[{"id":"fc_synthetic","type":"function_call","call_id":"call_synthetic","name":"missing_tool","arguments":"{}"}]`
				}
				wire := `{"type":"response.completed","response":{"id":"resp_synthetic","model":"gpt-fixture","status":"completed","output":` + output + `,"usage":{"input_tokens":1000,"output_tokens":2}}}`
				if strings.HasPrefix(kind, "upstream_") {
					wire = `{"type":"response.failed","response":{"id":"resp_synthetic","model":"gpt-fixture","status":"failed","output":[],"usage":{"input_tokens":1000,"output_tokens":2},"error":{"code":"invalid_request_error","message":"fixture rejection"}}}`
					if kind == "upstream_error" {
						wire = `{"type":"error","error":{"code":"invalid_request_error","message":"fixture rejection"},"usage":{"input_tokens":1000,"output_tokens":2}}`
					} else if kind == "upstream_forged_marker" {
						wire = `{"type":"response.failed","response":{"status":"failed","output":[],"usage":{"input_tokens":1000,"output_tokens":2},"error":{"code":"basispoints_protocol_error","message":"fixture rejection","failure_stage":"bridge_protocol_conversion","upstream_terminal_event":"response.completed"}}}`
					}
				}
				upstream := &httpUpstreamRecorder{resp: explicitErrorResponse("data: "+wire+"\n\n", "text/event-stream")}
				svc, account := excelBPSGatewayFixture(t, upstream)
				body := []byte(fmt.Sprintf(`{"model":"gpt-fixture","stream":%t,"prompt_cache_key":"synthetic-conversation","input":"hello",%s}`, stream, constraint))
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
				c.Set("api_key", &APIKey{ID: 8})
				result, handled, err := svc.forwardSelectedExcelBPS(c.Request.Context(), c, account, body, time.Now(), "gpt-fixture")
				require.True(t, handled)
				require.Error(t, err)
				require.NotNil(t, result)
				require.Equal(t, 1000, result.Usage.InputTokens)
				require.Equal(t, 2, result.Usage.OutputTokens)
				if strings.HasPrefix(kind, "local_") {
					assertTransientFallbackBilled(t, result)
				} else {
					assertExplicitUpstreamErrorFree(t, result)
				}
				require.Len(t, upstream.requests, 1)
			})
		}
	}
}

func TestOpenAITransientFallbackNativeAnthropicDoneSettlement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const base = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_synthetic\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"gpt-5.1\",\"content\":[],\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n" +
		"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"fixture rejection\"}}\n\n"
	for _, success := range []bool{false, true} {
		t.Run(fmt.Sprintf("success=%t", success), func(t *testing.T) {
			wire := base
			if success {
				wire += "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"answer\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			}
			wire += "data: [DONE]\n\n"
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}
			svc := &OpenAIGatewayService{}
			result, err := svc.handleNativeAnthropicStreamingResponse(c.Request.Context(), resp, c, &Account{ID: 42, Platform: PlatformOpenAI}, "gpt-5.1", "gpt-5.1", "gpt-5.1", nil, time.Now())
			require.NoError(t, err)
			if success {
				require.Contains(t, rec.Body.String(), "answer")
				assertTransientFallbackBilled(t, result)
			} else {
				assertExplicitUpstreamErrorFree(t, result)
			}
		})
	}
}
