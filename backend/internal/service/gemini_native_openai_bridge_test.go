package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const geminiBridgeRequest = `{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"generationConfig":{"maxOutputTokens":64}}`
const geminiBridgeChatUsage = `{"prompt_tokens":1000,"completion_tokens":50,"total_tokens":1050,"prompt_tokens_details":{"cached_tokens":600,"cache_write_tokens":100}}`
const geminiBridgeResponsesUsage = `{"input_tokens":1000,"output_tokens":50,"total_tokens":1050,"input_tokens_details":{"cached_tokens":600,"cache_write_tokens":100}}`

type geminiBridgeUpstream struct {
	HTTPUpstream
	do func(*http.Request) (*http.Response, error)
}

func (u *geminiBridgeUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.do(req)
}

func geminiBridgeAccount(protocol string) *Account {
	return &Account{ID: 9, Platform: PlatformGemini, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "local-fake-key", "base_url": "https://upstream.invalid/v1", "api_protocol": protocol,
	}}
}

func geminiBridgeContext() (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-flash:generateContent", nil)
	return c, rec
}

func geminiBridgeResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Request-Id": {"bridge-upstream-id"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func requireGeminiBridgeBilling(t *testing.T, result *ForwardResult, account *Account) {
	t.Helper()
	require.Equal(t, ClaudeUsage{InputTokens: 300, OutputTokens: 50, CacheReadInputTokens: 600, CacheCreationInputTokens: 100}, result.Usage)
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	repo := &openAIRecordUsageBillingRepoStub{}
	billing := NewBillingService(&config.Config{}, &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"gemini-2.5-flash": {InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6, CacheReadInputTokenCost: 0.1e-6, CacheCreationInputTokenCost: 1.25e-6},
	}})
	gateway := &GatewayService{usageLogRepo: logs, usageBillingRepo: repo, billingService: billing,
		billingCacheService: &BillingCacheService{}, deferredService: &DeferredService{}}
	require.NoError(t, gateway.RecordUsage(context.Background(), &RecordUsageInput{
		Result: result, APIKey: &APIKey{ID: 7}, User: &User{ID: 42}, Account: account,
	}))
	expected, err := billing.CalculateCost("gemini-2.5-flash", UsageTokens{InputTokens: 300, OutputTokens: 50, CacheReadTokens: 600, CacheCreationTokens: 100}, 1)
	require.NoError(t, err)
	require.NotNil(t, repo.lastCmd)
	require.Positive(t, repo.lastCmd.BalanceCost)
	require.InDelta(t, expected.ActualCost, repo.lastCmd.BalanceCost, 1e-12)
	require.InDelta(t, expected.ActualCost, logs.lastLog.ActualCost, 1e-12)
}

func TestGeminiBridgeProtocolUsageAndBilling(t *testing.T) {
	for _, protocol := range []string{APIProtocolChatCompletions, APIProtocolResponses, APIProtocolAdaptive} {
		for _, stream := range []bool{false, true} {
			name := protocol + "/buffered"
			if stream {
				name = protocol + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				account := geminiBridgeAccount(protocol)
				if protocol == APIProtocolAdaptive {
					account.Credentials["api_base_urls"] = map[string]any{APIProtocolResponses: "https://responses.invalid/v1"}
				}
				calls := 0
				upstream := &geminiBridgeUpstream{do: func(req *http.Request) (*http.Response, error) {
					calls++
					body, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					require.Equal(t, stream, gjson.GetBytes(body, "stream").Bool())
					if protocol == APIProtocolChatCompletions {
						require.Equal(t, "/v1/chat/completions", req.URL.Path)
						if stream {
							require.True(t, gjson.GetBytes(body, "stream_options.include_usage").Bool())
							return geminiBridgeResponse("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}],\"usage\":null}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\ndata: {\"choices\":[],\"usage\":" + geminiBridgeChatUsage + "}\n\ndata: [DONE]\n\n"), nil
						}
						return geminiBridgeResponse(`{"choices":[{"message":{"content":"hello"},"finish_reason":"length"}],"usage":` + geminiBridgeChatUsage + `}`), nil
					}
					require.Equal(t, "/v1/responses", req.URL.Path)
					if protocol == APIProtocolAdaptive {
						require.Equal(t, "responses.invalid", req.URL.Host)
					}
					require.True(t, gjson.GetBytes(body, "input").IsArray())
					require.Equal(t, int64(64), gjson.GetBytes(body, "max_output_tokens").Int())
					response := `{"id":"resp-local","model":"gemini-2.5-flash","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":` + geminiBridgeResponsesUsage + `}`
					if stream {
						return geminiBridgeResponse("event: response.incomplete\ndata: {\"response\":" + response + "}\n\n"), nil
					}
					return geminiBridgeResponse(response), nil
				}}
				svc := &GeminiMessagesCompatService{httpUpstream: upstream}
				c, rec := geminiBridgeContext()
				action := "generateContent"
				if stream {
					action = "streamGenerateContent"
				}
				result, err := svc.ForwardNative(context.Background(), c, account, "gemini-2.5-flash", action, stream, []byte(geminiBridgeRequest))
				require.NoError(t, err)
				require.Equal(t, 1, calls)
				require.Equal(t, http.StatusOK, rec.Code)
				require.Contains(t, rec.Body.String(), "hello")
				require.Contains(t, rec.Body.String(), `"finishReason":"MAX_TOKENS"`)
				require.Equal(t, stream, result.Stream)
				require.Equal(t, "bridge-upstream-id", result.RequestID)
				var final []byte
				if stream {
					forEachOpenAISSEDataPayload(rec.Body.String(), func(data []byte) { final = data })
				} else {
					final = rec.Body.Bytes()
				}
				require.Equal(t, int64(1000), gjson.GetBytes(final, "usageMetadata.promptTokenCount").Int())
				require.Equal(t, int64(600), gjson.GetBytes(final, "usageMetadata.cachedContentTokenCount").Int())
				require.Equal(t, int64(1050), gjson.GetBytes(final, "usageMetadata.totalTokenCount").Int())
				requireGeminiBridgeBilling(t, result, account)
			})
		}
	}
}

func TestGeminiBridgeCacheAliasesAndReasoningUsage(t *testing.T) {
	for _, cache := range []string{
		`"prompt_tokens_details":{"cached_tokens":600,"cache_write_tokens":100}`,
		`"input_tokens_details":{"cached_tokens":600,"cache_creation_tokens":100}`,
		`"cache_read_input_tokens":600,"cache_creation_input_tokens":100`,
	} {
		raw := []byte(`{"choices":[{"message":{"content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1000,"completion_tokens":50,"total_tokens":1050,"completion_tokens_details":{"reasoning_tokens":20,"image_tokens":5},` + cache + `}}`)
		body, usage := chatCompletionsToGeminiNative(raw, "gemini-2.5-flash")
		require.Equal(t, ClaudeUsage{InputTokens: 300, OutputTokens: 50, CacheReadInputTokens: 600, CacheCreationInputTokens: 100, ImageOutputTokens: 5}, usage)
		require.Equal(t, int64(600), gjson.GetBytes(body, "usageMetadata.cachedContentTokenCount").Int())
		require.Equal(t, int64(30), gjson.GetBytes(body, "usageMetadata.candidatesTokenCount").Int())
		require.Equal(t, int64(20), gjson.GetBytes(body, "usageMetadata.thoughtsTokenCount").Int())
		require.Equal(t, int64(1050), gjson.GetBytes(body, "usageMetadata.totalTokenCount").Int())
	}
}

func TestGeminiBridgeResponsesStreamTerminalAliases(t *testing.T) {
	for _, event := range []string{"response.completed", "response.done", "response.incomplete"} {
		for _, nested := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/nested=%v", event, nested), func(t *testing.T) {
				usage := `,"usage":` + geminiBridgeResponsesUsage
				response := `{"status":"completed"`
				if nested {
					response += usage
				}
				response += `}`
				terminal := `{"response":` + response
				if !nested {
					terminal += usage
				}
				terminal += `}`
				body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\nevent: " + event + "\ndata: " + terminal + "\n\n"
				svc := &GeminiMessagesCompatService{httpUpstream: &geminiBridgeUpstream{do: func(*http.Request) (*http.Response, error) { return geminiBridgeResponse(body), nil }}}
				c, rec := geminiBridgeContext()
				account := geminiBridgeAccount(APIProtocolResponses)
				result, err := svc.ForwardNative(context.Background(), c, account, "gemini-2.5-flash", "streamGenerateContent", true, []byte(geminiBridgeRequest))
				require.NoError(t, err)
				require.Equal(t, 1, strings.Count(rec.Body.String(), `"text":"hello"`))
				require.Equal(t, 1, strings.Count(rec.Body.String(), `"finishReason":"STOP"`))
				requireGeminiBridgeBilling(t, result, account)
			})
		}
	}
}

func TestGeminiBridgeFailureAndCancellationKeepUsage(t *testing.T) {
	for _, status := range []string{"failed", "cancelled", "canceled"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", status, stream), func(t *testing.T) {
				payload := `{"status":"` + status + `","usage":` + geminiBridgeResponsesUsage + `}`
				if stream {
					payload = "event: response." + status + "\ndata: {\"response\":" + payload + "}\n\n"
				}
				response := geminiBridgeResponse(payload)
				svc := &GeminiMessagesCompatService{httpUpstream: &geminiBridgeUpstream{do: func(*http.Request) (*http.Response, error) { return response, nil }}}
				c, rec := geminiBridgeContext()
				account := geminiBridgeAccount(APIProtocolResponses)
				result, err := svc.ForwardNative(context.Background(), c, account, "gemini-2.5-flash", "generateContent", stream, []byte(geminiBridgeRequest))
				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover))
				require.NotContains(t, rec.Body.String(), `"finishReason"`)
				require.Contains(t, rec.Body.String(), `"error"`)
				require.NotNil(t, result)
				requireGeminiBridgeBilling(t, result, account)
			})
		}
	}
}

func TestGeminiBridgeResponsesErrorPreludeKeepsTerminalUsage(t *testing.T) {
	body := "event: error\ndata: {\"error\":{\"type\":\"server_error\",\"message\":\"generation failed\"}}\n\n" +
		"event: response.failed\ndata: {\"response\":{\"status\":\"failed\",\"usage\":" + geminiBridgeResponsesUsage + "}}\n\n"
	svc := &GeminiMessagesCompatService{httpUpstream: &geminiBridgeUpstream{do: func(*http.Request) (*http.Response, error) { return geminiBridgeResponse(body), nil }}}
	c, rec := geminiBridgeContext()
	account := geminiBridgeAccount(APIProtocolResponses)
	result, err := svc.ForwardNative(context.Background(), c, account, "gemini-2.5-flash", "streamGenerateContent", true, []byte(geminiBridgeRequest))
	require.Error(t, err)
	require.NotContains(t, rec.Body.String(), `"finishReason"`)
	require.Equal(t, 1, strings.Count(rec.Body.String(), `"error"`))
	requireGeminiBridgeBilling(t, result, account)
}

func TestGeminiBridgeHTTPClientErrorsDoNotRetry(t *testing.T) {
	for _, status := range []int{400, 404, 422} {
		for _, protocol := range []string{APIProtocolChatCompletions, APIProtocolResponses} {
			t.Run(fmt.Sprintf("%s/%d", protocol, status), func(t *testing.T) {
				calls := 0
				svc := &GeminiMessagesCompatService{httpUpstream: &geminiBridgeUpstream{do: func(*http.Request) (*http.Response, error) {
					calls++
					response := geminiBridgeResponse(`{"error":{"type":"invalid_request_error","message":"unsupported parameter"}}`)
					response.StatusCode = status
					return response, nil
				}}}
				c, rec := geminiBridgeContext()
				result, err := svc.ForwardNative(context.Background(), c, geminiBridgeAccount(protocol), "gemini-2.5-flash", "generateContent", false, []byte(geminiBridgeRequest))
				require.Error(t, err)
				require.Nil(t, result)
				require.Equal(t, status, rec.Code)
				require.Equal(t, 1, calls)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover))
			})
		}
	}
}

func TestGeminiBridgeStreamErrorsNeverSynthesizeStop(t *testing.T) {
	partial := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}],\"usage\":" + geminiBridgeChatUsage + "}\n\n"
	for _, tc := range []struct {
		name, tail string
		status     int
	}{
		{"truncated", "", 502},
		{"malformed", "data: {broken}\n\n", 502},
		{"bare-error", "data: {\"error\":{\"type\":\"invalid_request_error\",\"message\":\"unsupported parameter\"}}\n\n", 400},
		{"scanner-limit", "data: " + strings.Repeat("x", 1024*1024) + "\n\n", 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &GeminiMessagesCompatService{httpUpstream: &geminiBridgeUpstream{do: func(*http.Request) (*http.Response, error) { return geminiBridgeResponse(partial + tc.tail), nil }}}
			c, rec := geminiBridgeContext()
			account := geminiBridgeAccount(APIProtocolChatCompletions)
			result, err := svc.ForwardNative(context.Background(), c, account, "gemini-2.5-flash", "streamGenerateContent", true, []byte(geminiBridgeRequest))
			require.Error(t, err)
			require.NotContains(t, rec.Body.String(), `"finishReason"`)
			var final []byte
			forEachOpenAISSEDataPayload(rec.Body.String(), func(data []byte) { final = data })
			require.Equal(t, int64(tc.status), gjson.GetBytes(final, "error.code").Int())
			requireGeminiBridgeBilling(t, result, account)
		})
	}
}

type geminiBridgeEndingReader struct {
	io.Reader
	err    error
	onEnd  func()
	closed bool
}

func (r *geminiBridgeEndingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		if r.onEnd != nil {
			r.onEnd()
			r.onEnd = nil
		}
		if r.err != nil {
			return n, r.err
		}
	}
	return n, err
}

func (r *geminiBridgeEndingReader) Close() error { r.closed = true; return nil }

func TestGeminiBridgeCanceledReadKeepsPartialUsage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &geminiBridgeEndingReader{Reader: strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}],\"usage\":" + geminiBridgeChatUsage + "}\n\n"), err: context.Canceled, onEnd: cancel}
	svc := &GeminiMessagesCompatService{httpUpstream: &geminiBridgeUpstream{do: func(*http.Request) (*http.Response, error) {
		response := geminiBridgeResponse("")
		response.Body = reader
		return response, nil
	}}}
	c, rec := geminiBridgeContext()
	c.Request = c.Request.WithContext(ctx)
	account := geminiBridgeAccount(APIProtocolChatCompletions)
	result, err := svc.ForwardNative(ctx, c, account, "gemini-2.5-flash", "streamGenerateContent", true, []byte(geminiBridgeRequest))
	require.ErrorIs(t, err, context.Canceled)
	require.True(t, reader.closed)
	require.True(t, result.ClientDisconnect)
	require.NotContains(t, rec.Body.String(), `"finishReason"`)
	requireGeminiBridgeBilling(t, result, account)
}

func TestGeminiBridgeStreamMissingUsageIsNotSuccessfulFreeResponse(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}],\"usage\":null}\n\ndata: [DONE]\n\n"
	svc := &GeminiMessagesCompatService{httpUpstream: &geminiBridgeUpstream{do: func(*http.Request) (*http.Response, error) { return geminiBridgeResponse(body), nil }}}
	c, rec := geminiBridgeContext()
	result, err := svc.ForwardNative(context.Background(), c, geminiBridgeAccount(APIProtocolChatCompletions), "gemini-2.5-flash", "streamGenerateContent", true, []byte(geminiBridgeRequest))
	require.ErrorContains(t, err, "omitted usage")
	require.Zero(t, result.Usage)
	require.NotContains(t, rec.Body.String(), `"finishReason":"STOP"`)
}

func TestGeminiBridgeNonStreamingReadFailureDoesNotReturnEmptySuccess(t *testing.T) {
	reader := &geminiBridgeEndingReader{Reader: strings.NewReader(`{"choices":[{"message":{"content":"hello"}}]}`), err: io.ErrUnexpectedEOF}
	svc := &GeminiMessagesCompatService{httpUpstream: &geminiBridgeUpstream{do: func(*http.Request) (*http.Response, error) {
		response := geminiBridgeResponse("")
		response.Body = reader
		return response, nil
	}}}
	c, rec := geminiBridgeContext()
	result, err := svc.ForwardNative(context.Background(), c, geminiBridgeAccount(APIProtocolChatCompletions), "gemini-2.5-flash", "generateContent", false, []byte(geminiBridgeRequest))
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.True(t, reader.closed)
}
