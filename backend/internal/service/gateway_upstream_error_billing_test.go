//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const syntheticAnthropicUsagePrefix = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_synthetic_error\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-3-7-sonnet-20250219\",\"content\":[],\"usage\":{\"input_tokens\":11,\"cache_creation_input_tokens\":300000,\"output_tokens\":3}}}\n\n" +
	"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"synthetic partial output\"}}\n\n"

func requireAnthropicErrorSettlesFree(t *testing.T, result *ForwardResult) {
	t.Helper()
	require.NotNil(t, result)
	require.True(t, result.NonBillableUpstreamError)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	require.NoError(t, svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: result,
		APIKey: &APIKey{ID: 1000, Group: &Group{RateMultiplier: 1}},
		User:   &User{ID: 2000}, Account: &Account{ID: 3000, Type: AccountTypeAPIKey},
	}))
	require.Equal(t, 1, usageRepo.calls, "keep the diagnostic usage row")
	require.Equal(t, result.Usage.CacheCreationInputTokens, usageRepo.lastLog.CacheCreationTokens)
	require.Zero(t, usageRepo.lastLog.TotalCost)
	require.Zero(t, usageRepo.lastLog.ActualCost)
	require.Equal(t, 1, billingRepo.calls, "keep settlement and preauthorization finalization")
	require.NotNil(t, billingRepo.lastCmd)
	require.Zero(t, billingRepo.lastCmd.BalanceCost)
}

func TestAnthropicNativeQueuedErrorAfterClientDisconnectSettlesFree(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, named := range []bool{false, true} {
		t.Run(fmt.Sprintf("named=%t", named), func(t *testing.T) {
			header := ""
			if named {
				header = "event: error\n"
			}
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(syntheticAnthropicUsagePrefix + header + "data: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"synthetic rejection after client write failure\"}}\n\n")),
			}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			c.Writer = &failWriteResponseWriter{ResponseWriter: c.Writer}
			result, err := newForwardPartialUsageServiceForTest(upstream).Forward(context.Background(), c, newAnthropicOAuthAccountForPartialUsageTest(), &ParsedRequest{
				Body:  NewRequestBodyRef([]byte(`{"model":"claude-3-7-sonnet-20250219","stream":true,"messages":[{"role":"user","content":"synthetic check"}]}`)),
				Model: "claude-3-7-sonnet-20250219", Stream: true,
			})
			require.NoError(t, err, "keep the original client-disconnected short circuit")
			require.True(t, result.ClientDisconnect)
			require.Equal(t, 300000, result.Usage.CacheCreationInputTokens)
			requireAnthropicErrorSettlesFree(t, result)
		})
	}
}

func TestAnthropicPassthroughExplicitErrorRefundsPreauthorization(t *testing.T) {
	fixture := newPreauthorizationFixture()
	handlerGuard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.NoError(t, err)
	workerGuard, ok := handlerGuard.TransferToWorker()
	require.True(t, ok)
	ctx := ContextWithBalancePreauthorizationGuard(context.Background(), workerGuard)
	result := partialStreamUsageResult(newPartialStreamUsageContext(t), newPartialStreamResp(), &streamingResult{
		usage: &ClaudeUsage{InputTokens: 11, CacheCreationInputTokens: 300000},
	}, "claude-3-7-sonnet-20250219", "claude-3-7-sonnet-20250219", time.Now(), &AnthropicPassthroughStreamError{StatusCode: http.StatusBadGateway})
	require.True(t, result.NonBillableUpstreamError)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	require.NoError(t, svc.RecordUsage(ctx, &RecordUsageInput{
		Result: result, APIKey: &APIKey{ID: 1000, Group: &Group{RateMultiplier: 1}},
		User: &User{ID: 2000}, Account: &Account{ID: 3000, Type: AccountTypeAPIKey},
	}))
	require.Equal(t, 1, usageRepo.calls)
	require.Equal(t, 300000, usageRepo.lastLog.CacheCreationTokens)
	require.Zero(t, usageRepo.lastLog.ActualCost)
	require.Zero(t, billingRepo.lastCmd.BalanceCost)
	require.True(t, billingRepo.lastCmd.BalancePreauthorized)
	require.Equal(t, 1, fixture.wallet.refundCalls)
	require.Zero(t, fixture.wallet.finalizeCalls)
	require.False(t, workerGuard.IsCurrentOwner())
}

func TestAnthropicNonStreamingErrorEnvelopeSettlesFreeAndDoesNotLeakToNextAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		for _, errorType := range []string{"invalid_request_error", "insufficient_quota"} {
			t.Run(fmt.Sprintf("passthrough=%t/%s", passthrough, errorType), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				account := newAnthropicAPIKeyAccountForTest()
				if !passthrough {
					account.Extra["anthropic_passthrough"] = false
				}
				const model = "claude-3-7-sonnet-20250219"
				parsed := &ParsedRequest{Body: NewRequestBodyRef([]byte(`{"model":"` + model + `","messages":[{"role":"user","content":"synthetic check"}]}`)), Model: model}
				upstream := &anthropicHTTPUpstreamRecorder{}
				svc := newForwardPartialUsageServiceForTest(upstream)
				for _, failed := range []bool{true, false} {
					payload := `{"id":"msg_synthetic_json","type":"message","content":[{"type":"text","text":"synthetic error text is ordinary output"}],"usage":{"input_tokens":11,"cache_creation_input_tokens":300000,"output_tokens":3}}`
					if failed {
						payload = `{"type":"error","error":{"type":"` + errorType + `","message":"synthetic rejection"},"usage":{"input_tokens":11,"cache_creation_input_tokens":300000,"output_tokens":3}}`
					}
					upstream.resp = &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))}
					result, err := svc.Forward(context.Background(), c, account, parsed)
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, 300000, result.Usage.CacheCreationInputTokens)
					if failed {
						requireAnthropicErrorSettlesFree(t, result)
					} else {
						require.False(t, result.NonBillableUpstreamError, "a successful attempt cannot inherit an earlier error")
						require.Positive(t, billAnthropicDisconnectResult(t, result).lastCmd.BalanceCost)
					}
				}
			})
		}
	}
}

func TestAnthropicNativeDataOnlyErrorIsTerminalAndNotBillable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, beforeOutput := range []bool{false, true} {
		t.Run(fmt.Sprintf("before_output=%t", beforeOutput), func(t *testing.T) {
			prefix := syntheticAnthropicUsagePrefix
			if beforeOutput {
				prefix = ""
			}
			payload := `{"type":"error","error":{"type":"invalid_request_error","message":"synthetic invalid parameter"}}`
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(prefix + "data: " + payload + "\n\n")),
			}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			result, err := newForwardPartialUsageServiceForTest(upstream).Forward(context.Background(), c, newAnthropicOAuthAccountForPartialUsageTest(), &ParsedRequest{
				Body:  NewRequestBodyRef([]byte(`{"model":"claude-3-7-sonnet-20250219","stream":true,"messages":[{"role":"user","content":"synthetic check"}]}`)),
				Model: "claude-3-7-sonnet-20250219", Stream: true,
			})
			var failure *UpstreamFailoverError
			require.ErrorAs(t, err, &failure)
			require.Nil(t, result, "native named and data-only errors share the existing no-usage-result path")
			require.JSONEq(t, payload, string(failure.ResponseBody))
			if beforeOutput {
				require.False(t, c.Writer.Written(), "pre-output failure remains replayable")
			}
		})
	}
}

func TestAnthropicPassthroughFinancialErrorPreservesUsageButSettlesFree(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, named := range []bool{false, true} {
		t.Run(fmt.Sprintf("named=%t", named), func(t *testing.T) {
			header := ""
			if named {
				header = "event: error\n"
			}
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(syntheticAnthropicUsagePrefix + header + "data: {\"type\":\"error\",\"error\":{\"type\":\"insufficient_quota\",\"message\":\"synthetic upstream quota exhausted\"}}\n\n")),
			}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			result, err := newForwardPartialUsageServiceForTest(upstream).Forward(context.Background(), c, newAnthropicAPIKeyAccountForTest(), &ParsedRequest{
				Body:  NewRequestBodyRef([]byte(`{"model":"claude-3-7-sonnet-20250219","stream":true,"messages":[{"role":"user","content":"synthetic check"}]}`)),
				Model: "claude-3-7-sonnet-20250219", Stream: true,
			})
			require.NoError(t, err, "preserve the existing financial-error response path")
			require.Equal(t, 300000, result.Usage.CacheCreationInputTokens)
			requireAnthropicErrorSettlesFree(t, result)
		})
	}
}

func TestAnthropicConvertedExplicitErrorBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"chat", "responses"} {
		for _, stream := range []bool{false, true} {
			for _, terminal := range []string{"error", "data_only_error", "financial", "financial_envelope", "success_after_error", "disconnect_after_error", "break_without_error"} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", protocol, stream, terminal), func(t *testing.T) {
					wire := syntheticAnthropicUsagePrefix
					if terminal != "break_without_error" {
						errorType := "invalid_request_error"
						if strings.HasPrefix(terminal, "financial") {
							errorType = "insufficient_quota"
						}
						header, envelope := "error", "error"
						if terminal == "financial_envelope" {
							header, envelope = "message", "message"
						}
						wire += "event: " + header + "\ndata: {\"type\":\"" + envelope + "\",\"error\":{\"type\":\"" + errorType + "\",\"message\":\"synthetic rejection\"}}\n\n"
					}
					if terminal == "data_only_error" {
						// A relay may send its own error as a bare data line, with no event name.
						wire = syntheticAnthropicUsagePrefix + "data: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"synthetic rejection\"}}\n\n"
					}
					if terminal == "success_after_error" {
						wire += "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
					}
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, nil)
					if terminal == "disconnect_after_error" {
						ctx, cancel := context.WithCancel(c.Request.Context())
						cancel()
						c.Request = c.Request.WithContext(ctx)
					}
					resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Request-Id": {"req_synthetic_conversion"}}, Body: io.NopCloser(strings.NewReader(wire))}
					svc := newMinimalGatewayService()
					var result *ForwardResult
					var err error
					const model = "claude-3-7-sonnet-20250219"
					if protocol == "chat" {
						if stream {
							result, err = svc.handleCCStreamingFromAnthropic(resp, c, model, model, nil, time.Now(), true, anthropicEstimateSource{})
						} else {
							result, err = svc.handleCCBufferedFromAnthropic(resp, c, model, model, nil, time.Now(), anthropicEstimateSource{})
						}
					} else {
						if stream {
							result, err = svc.handleResponsesStreamingResponse(resp, c, model, model, nil, time.Now(), apicompat.ResponsesClientToolMapping{}, anthropicEstimateSource{})
						} else {
							result, err = svc.handleResponsesBufferedStreamingResponse(resp, c, model, model, nil, time.Now(), apicompat.ResponsesClientToolMapping{}, anthropicEstimateSource{})
						}
					}
					if strings.HasPrefix(terminal, "financial") || terminal == "disconnect_after_error" {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
					}
					require.NotNil(t, result)
					require.Equal(t, 300000, result.Usage.CacheCreationInputTokens)
					if terminal == "success_after_error" || terminal == "break_without_error" {
						require.False(t, result.NonBillableUpstreamError, "normal completion and unclassified disconnect retain existing billing")
						require.Positive(t, billAnthropicDisconnectResult(t, result).lastCmd.BalanceCost)
					} else {
						requireAnthropicErrorSettlesFree(t, result)
					}
				})
			}
		}
	}
}

// A normal answer from a relay that adds an empty or null "error" field, or
// that mentions an error inside its content, must stay billable.
func TestAnthropicNonStreamingUpstreamError(t *testing.T) {
	for body, want := range map[string]bool{
		`{"type":"error","error":{"type":"invalid_request_error","message":"synthetic"}}`:                                 true,
		`{"error":{"message":"synthetic rejection"}}`:                                                                     true,
		`{"error":{"type":"upstream_error"}}`:                                                                             true,
		`{"error":{"code":"insufficient_quota"},"usage":{"input_tokens":11}}`:                                             true,
		`{"id":"msg_1","type":"message","content":[{"type":"text","text":"ok"}],"error":null}`:                            false,
		`{"id":"msg_1","type":"message","content":[],"error":{}}`:                                                         false,
		`{"id":"msg_1","type":"message","content":[{"type":"text","text":"ok"}],"error":{"message":"relay note"}}`:        false,
		`{"id":"msg_1","type":"message","error":""}`:                                                                      false,
		`{"id":"msg_1","type":"message","content":[{"type":"text","text":"the word error and \"type\":\"error\" here"}]}`: false,
	} {
		require.Equal(t, want, anthropicNonStreamingUpstreamError([]byte(body)), body)
	}
}
