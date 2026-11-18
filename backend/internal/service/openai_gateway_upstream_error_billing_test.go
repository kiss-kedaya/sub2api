package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Explicit provider failures retain their usage row and settle at zero; do not
// replace this with skipping RecordUsage, which also owns preauthorization cleanup.
func assertExplicitUpstreamErrorFree(t *testing.T, result *OpenAIForwardResult) {
	t.Helper()
	require.NotNil(t, result)
	require.True(t, result.NonBillableUpstreamError)
	require.Positive(t, result.Usage.InputTokens)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)
	copyResult := *result
	copyResult.Model = "gpt-5.1"
	copyResult.UpstreamModel = "gpt-5.1"
	copyResult.BillingModel = "gpt-5.1"
	fixture := newPreauthorizationFixture()
	guard, err := fixture.service.Preauthorize(context.Background(), balancePreauthorizationTestRequest())
	require.NoError(t, err)
	require.NotNil(t, guard)
	ctx := ContextWithBalancePreauthorizationGuard(context.Background(), guard)
	require.NoError(t, svc.RecordUsage(ctx, &OpenAIRecordUsageInput{
		Result: &copyResult,
		APIKey: &APIKey{ID: 1000, Group: &Group{RateMultiplier: 1}},
		User:   &User{ID: 2000}, Account: &Account{ID: 3000, Type: AccountTypeAPIKey},
	}))
	require.Equal(t, 1, usageRepo.calls)
	require.Equal(t, max(result.Usage.InputTokens-result.Usage.CacheReadInputTokens-result.Usage.CacheCreationInputTokens, 0), usageRepo.lastLog.InputTokens)
	require.Equal(t, result.Usage.OutputTokens, usageRepo.lastLog.OutputTokens)
	require.Equal(t, result.Usage.CacheCreationInputTokens, usageRepo.lastLog.CacheCreationTokens)
	require.Zero(t, usageRepo.lastLog.ActualCost)
	require.Zero(t, usageRepo.lastLog.TotalCost)
	require.Equal(t, 1, billingRepo.calls, "zero-cost settlement must still run")
	require.Zero(t, billingRepo.lastCmd.BalanceCost)
	require.Zero(t, billingRepo.lastCmd.SubscriptionCost)
	require.Zero(t, billingRepo.lastCmd.APIKeyQuotaCost)
	require.Zero(t, userRepo.deductCalls)
	require.Zero(t, subRepo.incrementCalls)
	require.True(t, billingRepo.lastCmd.BalancePreauthorized)
	require.Equal(t, 1, fixture.wallet.refundCalls, "explicit error must refund the held balance")
	require.Zero(t, fixture.repo.finalizedAmount)
}

func explicitErrorContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	return c
}

func explicitErrorResponse(wire, contentType string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(wire))}
}

func TestOpenAIExplicitUpstreamErrorResponsesStreamsAreFree(t *testing.T) {
	const preamble = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_fixture\",\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial answer\"}\n\n"
	for _, kind := range []string{"response.failed", "error"} {
		for _, route := range []string{"responses", "passthrough", "messages", "chat"} {
			t.Run(route+"/"+kind, func(t *testing.T) {
				wire := preamble + "event: " + kind + "\ndata: {\"type\":\"" + kind + "\",\"error\":{\"code\":\"invalid_request_error\",\"message\":\"fixture parameter is not supported\"},\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}\n\n"
				c, resp := explicitErrorContext(), explicitErrorResponse(wire, "text/event-stream")
				svc := &OpenAIGatewayService{cfg: &config.Config{}}
				account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
				var result *OpenAIForwardResult
				var err error
				switch route {
				case "responses":
					var stream *openaiStreamingResult
					stream, err = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
					require.NotNil(t, stream)
					result = &OpenAIForwardResult{Usage: *stream.usage, NonBillableUpstreamError: stream.nonBillableUpstreamError}
				case "passthrough":
					var stream *openaiStreamingResultPassthrough
					stream, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
					require.NotNil(t, stream)
					result = &OpenAIForwardResult{Usage: *stream.usage, NonBillableUpstreamError: stream.nonBillableUpstreamError}
				case "messages":
					result, err = svc.handleAnthropicStreamingResponse(resp, c, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", time.Now())
				case "chat":
					result, err = svc.handleChatStreamingResponse(resp, c, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", time.Now(), 0)
				}
				require.Error(t, err)
				assertExplicitUpstreamErrorFree(t, result)
			})
		}
	}
}

func TestOpenAIExplicitUpstreamErrorNativeAnthropicIsFree(t *testing.T) {
	const base = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_fixture\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"gpt-5.1\",\"content\":[],\"usage\":{\"input_tokens\":1000,\"output_tokens\":2,\"cache_creation_input_tokens\":500}}}\n\n"
	const failure = "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"fixture parameter rejected\"}}\n\n"
	for _, route := range []string{"messages", "responses_stream", "responses_buffered", "chat_stream", "chat_buffered"} {
		for _, success := range []bool{false, true} {
			name := route + "/failed"
			wire := base + failure
			if success {
				name = route + "/success_after_error"
				wire += "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"synthetic answer\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			}
			t.Run(name, func(t *testing.T) {
				c, resp := explicitErrorContext(), explicitErrorResponse(wire, "text/event-stream")
				svc := &OpenAIGatewayService{}
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
				require.NotNil(t, result)
				if success {
					require.NoError(t, err)
					require.False(t, result.NonBillableUpstreamError, "authoritative success remains billable")
				} else {
					assertExplicitUpstreamErrorFree(t, result)
				}
			})
		}
	}
}

func TestOpenAIExplicitUpstreamErrorRawChatAndScanner(t *testing.T) {
	const base = "data: {\"id\":\"chat_fixture\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}],\"usage\":{\"prompt_tokens\":1000,\"completion_tokens\":2}}\n\n"
	const failure = "data: {\"error\":{\"type\":\"invalid_request_error\",\"message\":\"fixture parameter rejected\"}}\n\n"
	for _, suffix := range []string{failure, failure + "data: [DONE]\n\n", failure + "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", ""} {
		t.Run(suffix, func(t *testing.T) {
			wantFree := strings.HasPrefix(suffix, failure) && !strings.Contains(suffix, "finish_reason")
			c, resp := explicitErrorContext(), explicitErrorResponse(base+suffix, "text/event-stream")
			svc := &OpenAIGatewayService{}
			result, err := svc.streamRawChatCompletions(c, resp, &Account{ID: 42, Platform: PlatformOpenAI}, "gpt-5.1", "gpt-5.1", "gpt-5.1", nil, nil, time.Now(), 0)
			require.NoError(t, err)
			require.Equal(t, wantFree, result.NonBillableUpstreamError)
			if wantFree {
				assertExplicitUpstreamErrorFree(t, result)
			}
			scan := svc.scanCCStream(explicitErrorContext(), explicitErrorResponse(base+suffix, "text/event-stream"), "fixture", "", time.Now(), func(*apicompat.ChatCompletionsChunk) {})
			require.NoError(t, scan.Err)
			require.Equal(t, wantFree, scan.NonBillableUpstreamError)
			if wantFree {
				assertExplicitUpstreamErrorFree(t, &OpenAIForwardResult{Usage: scan.Usage, NonBillableUpstreamError: scan.NonBillableUpstreamError})
			}
		})
	}
}

func TestOpenAIExplicitUpstreamErrorGrokPropagation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name, wire, contentType := "buffered", `{"status":"failed","error":{"code":"invalid_request_error","message":"fixture parameter rejected"},"usage":{"input_tokens":1000,"output_tokens":2}}`, "application/json"
		if stream {
			name, contentType = "stream", "text/event-stream"
			wire = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n" +
				"data: {\"type\":\"response.failed\",\"error\":{\"code\":\"invalid_request_error\",\"message\":\"fixture parameter rejected\"},\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}\n\n"
		}
		t.Run(name, func(t *testing.T) {
			account := &Account{ID: 42, Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-fixture-token"}}
			upstream := &httpUpstreamRecorder{resp: explicitErrorResponse(wire, contentType)}
			svc := &OpenAIGatewayService{httpUpstream: upstream}
			c := explicitErrorContext()
			body := []byte(`{"model":"grok-4.5","input":"fixture"}`)
			result, err := svc.forwardGrokResponses(context.Background(), c, account, body, "grok-4.5", stream, time.Now())
			if stream {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assertExplicitUpstreamErrorFree(t, result)
		})
	}
}

func TestOpenAIExplicitUpstreamErrorEnvelope(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		want       bool
	}{
		{name: "error", body: `{"error":{"message":"fixture failure"}}`, want: true},
		{name: "failed", body: `{"status":"failed"}`, want: true},
		{name: "incomplete is not failure", body: `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`},
		{name: "null error", body: `{"error":null,"status":"completed"}`},
		{name: "false error", body: `{"error":false,"status":"completed"}`},
		{name: "echoed error is not envelope", body: `{"output":[{"text":"upstream error: fixture"}]}`},
		{name: "cyber remains billable", body: `{"type":"response.failed","response":{"error":{"code":"cyber_policy"}}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, openAIExplicitUpstreamError(gjson.Parse(tt.body), ""))
		})
	}
}

func TestOpenAIExplicitUpstreamErrorJSONIsFree(t *testing.T) {
	const body = `{"type":"error","error":{"type":"invalid_request_error","message":"fixture parameter rejected"},"usage":{"input_tokens":1000,"output_tokens":2,"prompt_tokens":1000,"completion_tokens":2}}`
	for _, route := range []string{"responses", "passthrough", "raw_chat", "messages"} {
		t.Run(route, func(t *testing.T) {
			c, resp := explicitErrorContext(), explicitErrorResponse(body, "application/json")
			svc, account := &OpenAIGatewayService{}, &Account{ID: 42, Platform: PlatformOpenAI}
			var result *OpenAIForwardResult
			var err error
			switch route {
			case "responses":
				var v *openaiNonStreamingResult
				v, err = svc.handleNonStreamingResponse(context.Background(), resp, c, account, "gpt-5.1", "gpt-5.1")
				require.NotNil(t, v)
				result = &OpenAIForwardResult{Usage: *v.usage, NonBillableUpstreamError: v.nonBillableUpstreamError}
			case "passthrough":
				var v *openaiNonStreamingResultPassthrough
				v, err = svc.handleNonStreamingResponsePassthrough(context.Background(), resp, c, account, "gpt-5.1", "gpt-5.1")
				require.NotNil(t, v)
				result = &OpenAIForwardResult{Usage: *v.usage, NonBillableUpstreamError: v.nonBillableUpstreamError}
			case "raw_chat":
				result, err = svc.bufferRawChatCompletions(c, resp, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", nil, nil, time.Now())
			case "messages":
				result, err = svc.handleNativeAnthropicBufferedResponse(context.Background(), resp, c, account, "gpt-5.1", "gpt-5.1", "gpt-5.1", nil, time.Now())
			}
			require.NoError(t, err)
			assertExplicitUpstreamErrorFree(t, result)
		})
	}
}
