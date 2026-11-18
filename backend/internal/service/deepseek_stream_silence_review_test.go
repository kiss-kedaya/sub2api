//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const silenceReviewCompleted = silenceResponsesPrelude + "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_synthetic\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1000,\"output_tokens\":2}}}\n\n"

func TestDeepSeekStreamSilenceReviewBufferedCompleted(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 60*time.Millisecond)
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprint(passthrough), func(t *testing.T) {
			svc, c, rec, account, resp := streamSilenceFixture(t, "/v1/responses", silenceReviewCompleted)
			var usage *OpenAIUsage
			if passthrough {
				result, err := svc.handleNonStreamingResponsePassthrough(c.Request.Context(), resp, c, account, "synthetic", "synthetic")
				require.NoError(t, err)
				require.NotNil(t, result)
				require.False(t, result.nonBillableUpstreamError)
				usage = result.usage
			} else {
				result, err := svc.handleNonStreamingResponse(c.Request.Context(), resp, c, account, "synthetic", "synthetic")
				require.NoError(t, err)
				require.NotNil(t, result)
				require.False(t, result.nonBillableUpstreamError)
				usage = result.usage
			}
			require.Equal(t, http.StatusOK, rec.Code)
			require.True(t, gjson.Valid(rec.Body.String()))
			require.Contains(t, rec.Body.String(), "partial answer")
			require.Equal(t, 1000, usage.InputTokens)
			require.Equal(t, 2, usage.OutputTokens)
			_, event := c.Get(OpsUpstreamErrorsKey)
			require.False(t, event)
			rows := &openAIRecordUsageLogRepoStub{inserted: true}
			ledger := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
			billing := newOpenAIRecordUsageServiceWithBillingRepoForTest(rows, ledger, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			require.NoError(t, billing.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{Usage: *usage, Model: "gpt-5.1"},
				APIKey: &APIKey{ID: 1000, Group: &Group{RateMultiplier: 1}},
				User:   &User{ID: 2000}, Account: &Account{ID: 3000, Type: AccountTypeAPIKey},
			}))
			require.Equal(t, 1, rows.calls)
			require.Equal(t, 1, ledger.calls)
			require.Positive(t, ledger.lastCmd.BalanceCost, "a completed generation must retain normal billing")
		})
	}
}

func TestDeepSeekStreamSilenceReviewPassthroughFailedOnce(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 60*time.Millisecond)
	wire := silenceResponsesPrelude + "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"invalid_request_error\",\"message\":\"synthetic rejected\"}}}\n\n"
	svc, c, rec, account, resp := streamSilenceFixture(t, "/v1/responses", wire)
	result, err := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "synthetic", "synthetic")
	require.Error(t, err)
	require.NotNil(t, result)
	require.True(t, result.nonBillableUpstreamError)
	require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed"))
	require.NotContains(t, rec.Body.String(), "Upstream did not respond in time")
	raw, exists := c.Get(OpsUpstreamErrorsKey)
	if exists {
		events, ok := raw.([]*OpsUpstreamErrorEvent)
		require.True(t, ok)
		for _, event := range events {
			require.NotEqual(t, "stream_silence_limit", event.Kind)
		}
	}
}

type silenceReviewHTTPUpstream struct{ calls int }

func (u *silenceReviewHTTPUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{"Content-Type": {"application/json"}}, Body: &streamSilenceBlockingBody{prefix: strings.NewReader(`{"usage":{"input_tokens":1000,"output_tokens":2},"error":{"message":"synthetic`), closed: make(chan struct{})}}, nil
}

func (u *silenceReviewHTTPUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestDeepSeekStreamSilenceReviewHTTPErrorBody(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 80*time.Millisecond)
	for _, route := range []string{"responses", "passthrough", "raw_chat", "responses_chat", "messages_chat", "chat_responses", "messages_responses", "embeddings", "alpha", "ws_bridge", "direct_errors", "compat_errors"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", route, stream), func(t *testing.T) {
				large := strings.Repeat("x", int(deepSeekLargeRequestBytes)+1)
				input, message, query := large, "hello", "hello"
				if route == "raw_chat" || route == "chat_responses" || route == "messages_responses" || route == "messages_chat" {
					input, message = "hello", large
				}
				if route == "alpha" {
					input, query = "hello", large
				}
				body := []byte(fmt.Sprintf(`{"model":"deepseek-synthetic","input":"%s","messages":[{"role":"user","content":"%s"}],"commands":[{"query":"%s"}],"stream":%t}`, input, message, query, stream))
				c, rec := newOpenAIPartialUsageContext(t, body)
				account := newOpenAIPartialUsageAccount(false)
				account.Platform, account.Schedulable = PlatformDeepseek, true
				account.Credentials["base_url"], account.Credentials["api_protocol"] = "https://example.invalid", APIProtocolResponses
				u := &silenceReviewHTTPUpstream{}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: u}
				ctx := WithOpsGinContext(c.Request.Context(), c)
				var result *OpenAIForwardResult
				var err error
				switch route {
				case "responses":
					result, err = svc.Forward(ctx, c, account, body)
				case "passthrough":
					result, err = svc.forwardOpenAIPassthrough(ctx, c, account, body, body, "deepseek-synthetic", false, nil, stream, time.Now())
				case "ws_bridge":
					result, err = svc.proxyOpenAIWSHTTPBridgeTurn(ctx, c, account, "synthetic", body, len(body), "deepseek-synthetic", "", "", "", "", 1, func(p []byte) error { _, writeErr := rec.Body.Write(p); return writeErr })
				case "direct_errors", "compat_errors":
					req := waitLimitRequest(t, ctx, HTTPUpstreamProfileOpenAI)
					req.ContentLength = deepSeekLargeRequestBytes + 1
					var resp *http.Response
					resp, err = svc.doOpenAIUpstream(req, "", account)
					require.NoError(t, err)
					defer func() { require.NoError(t, resp.Body.Close()) }()
					if route == "direct_errors" {
						result, err = svc.handleErrorResponse(ctx, resp, c, account, body)
					} else {
						result, err = svc.handleChatCompletionsErrorResponse(resp, c, account, "deepseek-synthetic")
					}
				case "raw_chat":
					c.Request.URL.Path = "/v1/chat/completions"
					result, err = svc.forwardAsRawChatCompletions(ctx, c, account, body, "")
				case "responses_chat":
					result, err = svc.forwardResponsesViaRawChatCompletions(ctx, c, account, body)
				case "messages_chat":
					c.Request.URL.Path = "/v1/messages"
					result, err = svc.forwardAnthropicViaRawChatCompletions(ctx, c, account, body, "")
				case "chat_responses":
					c.Request.URL.Path = "/v1/chat/completions"
					result, err = svc.ForwardAsChatCompletions(ctx, c, account, body, "", "")
				case "messages_responses":
					c.Request.URL.Path = "/v1/messages"
					result, err = svc.ForwardAsAnthropic(ctx, c, account, body, "", "")
				case "embeddings":
					c.Request.URL.Path = "/v1/embeddings"
					result, err = svc.ForwardEmbeddings(ctx, c, account, body, "")
				case "alpha":
					c.Request.URL.Path = "/v1/alpha/search"
					result, err = svc.ForwardAlphaSearch(ctx, c, account, body)
				}
				requireStreamSilenceError(t, err, account)
				require.Equal(t, 1, u.calls)
				require.NotNil(t, result)
				require.True(t, result.NonBillableUpstreamError)
				requireOneStreamSilenceEvent(t, c)
				// The partial error document may not contain parseable usage, but it
				// must still finalize at zero and retain a usage row.
				settlementResult := *result
				settlementResult.Usage = OpenAIUsage{InputTokens: 1000, OutputTokens: 2}
				assertExplicitUpstreamErrorFree(t, &settlementResult)
				require.Equal(t, 1, strings.Count(rec.Body.String(), "Upstream did not respond in time"))
				if route != "ws_bridge" && (!stream || route == "alpha" || route == "embeddings" || route == "compat_errors") {
					require.Equal(t, http.StatusGatewayTimeout, rec.Code)
					require.True(t, gjson.Valid(rec.Body.String()))
				}
			})
		}
	}
}

func TestDeepSeekStreamSilenceReviewHistoryUsesOriginalContext(t *testing.T) {
	shortenDeepSeekStreamSilence(t, 60*time.Millisecond)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			u := &hostedSearchUpstream{}
			u.modelBody = func(_ int, _ string) io.ReadCloser {
				return &streamSilenceBlockingBody{prefix: strings.NewReader(silenceResponsesPrelude), closed: make(chan struct{})}
			}
			u.modelResponses = [][]byte{[]byte(silenceResponsesPrelude)}
			svc, c, rec, account, body := hostedSearchFixture(t, u, stream, "none", false)
			account.Credentials["base_url"] = "https://example.invalid"
			svc.cfg = rawChatCompletionsTestConfig()
			var err error
			body, err = sjson.SetRawBytes(body, "input", []byte(fmt.Sprintf(`[{"type":"message","role":"user","content":"%s"},{"type":"web_search_call","id":"ws_synthetic","status":"completed","action":{"type":"search","query":"synthetic"}}]`, strings.Repeat("x", int(deepSeekLargeRequestBytes)+1))))
			require.NoError(t, err)
			originalRequest := c.Request
			result, err := svc.Forward(WithOpsGinContext(c.Request.Context(), c), c, account, body)
			require.Same(t, originalRequest, c.Request, "internal recursion must restore the client request context")
			requireStreamSilenceError(t, err, account)
			require.NotNil(t, result)
			require.True(t, IsResponseCommitted(c))
			require.Equal(t, 1, strings.Count(rec.Body.String(), "Upstream did not respond in time"))
			if !stream {
				require.True(t, gjson.ValidBytes(rec.Body.Bytes()))
			}
			requireOneStreamSilenceEvent(t, c)
		})
	}
}
