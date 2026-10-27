package service

import (
	"bytes"
	"context"
	"errors"
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

func TestClientErrorPrivacyPreservesSuccessfulDocuments(t *testing.T) {
	for _, body := range []string{
		`{"error":null,"output":[{"text":"https://public.example/article"}],"usage":{"input_tokens":9}}`,
		`{"error":false,"content":"https://public.example/article"}`,
		`{"error":"","content":"https://public.example/article"}`,
		`{"error":0,"content":"https://public.example/article"}`,
		`{"response":{"error":null,"output":[{"text":"192.0.2.10"}]}}`,
		`{"results":[{"url":"https://public.example/article","content":"contact author@public.example"}]}`,
		`{"content":"api_key=synthetic-example in an article","url":"https://public.example/article"}`,
	} {
		require.Equal(t, body, string(sanitizeClientUpstreamErrorBody(http.StatusOK, []byte(body))))
	}
}

func TestClientErrorPrivacyDeepSeekErrorsRetainStatusWithoutBillingOrRetry(t *testing.T) {
	for _, path := range []string{DeepSeekToolsPathSearch, DeepSeekToolsPathFetch} {
		for _, status := range []int{400, 401, 403, 429, 502, 503} {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, path, nil)
			response := `{"error":{"code":"invalid_parameter","type":"invalid_request_error","param":"tools","message":"https://private-provider.example failed; Bearer synthetic-private-secret","account_name":"private-account","api_key":"opaque-private-key"}}`
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: status, Header: http.Header{"Location": {"https://private-provider.example/login"}}, Body: io.NopCloser(strings.NewReader(response)),
			}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			account := &Account{ID: 7, Platform: PlatformDeepseek, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "synthetic-model-key", "base_url": "https://model.example"}}
			key := "synthetic-search-key"
			result, err := svc.ForwardDeepSeekTools(context.Background(), c, account, &Group{SearchProviderAPIKey: &key}, path, "", []byte(`{"query":"test","url":"https://public.example"}`))
			require.NoError(t, err)
			require.Nil(t, result, "a provider error must not become a billable result")
			require.Equal(t, status, rec.Code)
			require.Len(t, upstream.requests, 1, "redaction must not replay the request")
			require.Empty(t, rec.Header().Get("Location"))
			for _, secret := range []string{"private-provider", "synthetic-private-secret", "private-account", "opaque-private-key"} {
				require.NotContains(t, rec.Body.String(), secret)
			}
			require.Equal(t, "invalid_parameter", gjson.Get(rec.Body.String(), "error.code").String())
			require.Equal(t, "tools", gjson.Get(rec.Body.String(), "error.param").String())
		}
	}
}

func TestClientErrorPrivacyAnthropicTerminalRetainsOriginalEvidenceAndUsage(t *testing.T) {
	const payload = `{"type":"error","error":{"type":"relay_stream_error","status_code":403,"message":"https://private-provider.example failed","api_key":"synthetic-opaque-value"}}`
	const prefix = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":11,\"cache_read_input_tokens\":7}}}\n\n"
	for _, frame := range []string{
		"event: error\ndata: " + payload + "\n\n",
		"data: " + payload + "\n\n",
		payload + "\n\n",
		"event: error\ndata: {\ndata: \"type\":\"error\",\n" + `data: "error":{"type":"relay_stream_error","status_code":403,"message":"https://private-provider.example failed","api_key":"synthetic-opaque-value"}}` + "\n\n",
	} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
			Body: &streamReadCloser{payload: []byte(prefix + frame), err: io.ErrUnexpectedEOF},
		}}
		svc := newForwardPartialUsageServiceForTest(upstream)
		body := []byte(`{"model":"claude-3-7-sonnet-20250219","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
		result, err := svc.Forward(context.Background(), c, newAnthropicAPIKeyAccountForTest(), &ParsedRequest{
			Body: NewRequestBodyRef(body), Model: "claude-3-7-sonnet-20250219", Stream: true,
		})
		var failure *AnthropicPassthroughStreamError
		require.ErrorAs(t, err, &failure)
		require.Equal(t, 403, failure.StatusCode)
		require.True(t, failure.ResponseWritten)
		require.Contains(t, failure.RawData, "private-provider.example", "restricted original evidence must be preserved")
		var failover *UpstreamFailoverError
		require.False(t, errors.As(err, &failover))
		require.NotNil(t, result)
		require.Equal(t, 11, result.Usage.InputTokens)
		require.Equal(t, 7, result.Usage.CacheReadInputTokens)
		require.True(t, strings.HasPrefix(rec.Body.String(), prefix), "normal frames must keep their exact bytes")
		require.NotContains(t, rec.Body.String(), "private-provider")
		require.NotContains(t, rec.Body.String(), "synthetic-opaque-value")
		require.Contains(t, rec.Body.String(), `"status_code":403`)
	}
}

func TestClientErrorPrivacyOpenAITerminalAndSyntheticEOF(t *testing.T) {
	for _, eventType := range []string{"error", "response.failed"} {
		body := []byte(`{"type":"` + eventType + `","error":{"code":"upstream_error","message":"dial tcp 192.0.2.42:443 failed; account_name=private-account"}}`)
		original := bytes.Clone(body)
		cleaned, changed := sanitizeOpenAIResponseFailedEventForClient(body, eventType, true)
		require.True(t, changed)
		require.Equal(t, original, body)
		require.Equal(t, eventType, gjson.GetBytes(cleaned, "type").String())
		require.NotContains(t, string(cleaned), "192.0.2.42")
		require.NotContains(t, string(cleaned), "private-account")
	}
	wire := buildOpenAIResponseFailedSSE("resp_safe", "safe-model", nil, "Get https://private-provider.example: unexpected EOF")
	require.Contains(t, wire, "event: response.failed\n")
	require.Contains(t, wire, "unexpected EOF")
	require.NotContains(t, wire, "private-provider")
	tool := []byte(`{"type":"response.function_call_arguments.delta","delta":"https://public.example"}`)
	cleaned, changed := sanitizeOpenAIResponseFailedEventForClient(tool, "response.function_call_arguments.delta", true)
	require.False(t, changed)
	require.Equal(t, tool, cleaned)
}
