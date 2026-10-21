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

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGrokHTTPProtocolRefusalPreservesPayloadAndSettlementSignal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, eventName := range []string{"response.failed", "error", "ip_blacklist"} {
		eventType := eventName
		payload := `{"type":"response.failed","response":{"id":"resp_refused","model":"grok-upstream","status":"failed","error":{"status_code":403,"type":"upstream_error","message":"I'm sorry, I can't help with that request."},"metadata":{"provider":"fixture"},"usage":{"input_tokens":8,"output_tokens":1}}}`
		if eventType == "error" {
			payload = `{"type":"error","error":{"status_code":403,"type":"upstream_error","message":"I'm sorry, I can't help with that request."},"metadata":{"provider":"fixture"}}`
		}
		if eventName == "ip_blacklist" {
			eventType = "error"
			payload = `{"type":"error","error":{"status_code":403,"type":"upstream_error","message":"blocked by global ip blacklist"},"metadata":{"provider":"fixture"}}`
		}
		for _, adapter := range []string{"responses", "passthrough", "chat", "messages", "raw_chat"} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", eventName, adapter, stream), func(t *testing.T) {
					svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					body := "event: " + eventType + "\ndata: " + payload + "\n\n"
					resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
					account := &Account{ID: 12, Platform: PlatformGrok, Type: AccountTypeOAuth}
					err := runGrokHTTPRefusalAdapter(svc, c, resp, account, adapter, stream)
					require.Error(t, err)
					require.True(t, IsGrokModerationRefusalFailure(account.Platform, nil, err), "the HTTP handler must recognize the fixed-price settlement signal")
					var failover *UpstreamFailoverError
					require.NotErrorAs(t, err, &failover, "a content refusal must never retry or switch accounts")
					if stream && (adapter == "responses" || adapter == "passthrough" || adapter == "raw_chat") {
						require.Equal(t, http.StatusOK, rec.Code)
						require.Equal(t, body, rec.Body.String(), "preserve the complete upstream error frame, including usage and provider metadata")
					} else {
						require.Equal(t, http.StatusForbidden, rec.Code)
						require.Equal(t, payload, rec.Body.String(), "preserve the original terminal JSON rather than returning a generic 502")
					}
				})
			}
		}
	}
}

func TestGrokHTTPProtocolRefusalRequiresActual403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{PlatformGrok, PlatformOpenAI} {
		for _, status := range []int{0, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
			if platform == PlatformGrok && status == http.StatusForbidden {
				continue // covered by the positive refusal matrix above
			}
			for _, adapter := range []string{"responses", "passthrough", "chat", "messages", "raw_chat"} {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/status=%d/%s/stream=%t", platform, status, adapter, stream), func(t *testing.T) {
						payload := fmt.Sprintf(`{"type":"response.failed","response":{"id":"resp_boundary","status":"failed","error":{"status_code":%d,"type":"upstream_error","message":"I'm sorry, I can't help with that request."}}}`, status)
						svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
						rec := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(rec)
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
						resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("event: response.failed\ndata: " + payload + "\n\n"))}
						account := &Account{ID: 13, Platform: platform, Type: AccountTypeOAuth}
						err := runGrokHTTPRefusalAdapter(svc, c, resp, account, adapter, stream)
						if adapter != "raw_chat" {
							require.Error(t, err)
						}
						require.False(t, IsGrokPlatformContentPolicyRejection(platform, err), "a refusal sentence alone must not create a moderation fee or permanent block")
					})
				}
			}
		}
	}
}

func TestGrokHTTPProtocolRefusalAfterOutputPreservesSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const payload = `{"type":"response.failed","response":{"id":"resp_partial","status":"failed","error":{"status_code":403,"type":"upstream_error","message":"I'm sorry, I can't help with that request."},"usage":{"input_tokens":8,"output_tokens":1}}}`
	for _, adapter := range []string{"chat", "messages", "raw_chat"} {
		t.Run(adapter, func(t *testing.T) {
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			body := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\",\"status\":\"in_progress\"}}\n\n" +
				"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
				"event: response.failed\ndata: " + payload + "\n\n"
			if adapter == "raw_chat" {
				body = "data: {\"id\":\"chatcmpl_partial\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":null}]}\n\n" +
					"event: response.failed\ndata: " + payload + "\n\n"
			}
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
			err := runGrokHTTPRefusalAdapter(svc, c, resp, &Account{ID: 14, Platform: PlatformGrok, Type: AccountTypeOAuth}, adapter, true)
			require.True(t, IsGrokPlatformContentPolicyRejection(PlatformGrok, err))
			require.Equal(t, http.StatusOK, rec.Code, "already committed output must remain one SSE response")
			require.Contains(t, rec.Body.String(), "hello")
			require.True(t, strings.HasSuffix(rec.Body.String(), "event: response.failed\ndata: "+payload+"\n\n"), rec.Body.String())
			require.Equal(t, 1, strings.Count(rec.Body.String(), payload))
		})
	}
}

func TestGrokHTTPProtocolRefusalRawChatJSONRequires403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, message := range []string{"I'm sorry, I can't help with that request.", "blocked by global ip blacklist"} {
		for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
			t.Run(fmt.Sprintf("%s/%d", message, status), func(t *testing.T) {
				payload := fmt.Sprintf(`{"error":{"status_code":%d,"type":"upstream_error","message":%q},"metadata":{"provider":"fixture"}}`, status, message)
				svc := &OpenAIGatewayService{cfg: &config.Config{}}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))}
				err := runGrokHTTPRefusalAdapter(svc, c, resp, &Account{ID: 15, Platform: PlatformGrok, Type: AccountTypeAPIKey}, "raw_chat", false)
				if status == http.StatusForbidden {
					require.True(t, IsGrokPlatformContentPolicyRejection(PlatformGrok, err))
					require.Equal(t, http.StatusForbidden, rec.Code)
					require.Equal(t, payload, rec.Body.String())
					var failover *UpstreamFailoverError
					require.NotErrorAs(t, err, &failover)
				} else {
					require.False(t, IsGrokPlatformContentPolicyRejection(PlatformGrok, err), "429/500 must not trigger a fixed refusal fee")
				}
			})
		}
	}
}

func TestGrokHTTPProtocolRefusalRawHTTPBlacklistRequires403(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: status,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"blocked by global ip blacklist"}}`)),
			}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream, accountRepo: &grokQuotaAccountRepo{}}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			account := &Account{ID: 16, Platform: PlatformGrok, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture", "base_url": "https://api.example.test"}}
			_, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, []byte(`{"model":"grok-4.7","stream":false,"messages":[{"role":"user","content":"hello"}]}`), "grok-4.7")
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.Equal(t, status, failover.StatusCode)
			require.False(t, IsGlobalIPBlacklistRejectionMarked(c), "429/500 text must not enter the permanent-session-block branch")
			require.False(t, IsGrokModerationRefusalFailure(PlatformGrok, nil, err), "429/500 text must not trigger the fixed refusal fee")
			require.False(t, c.Writer.Written(), "the normal account failover path must remain available")
		})
	}
}

func runGrokHTTPRefusalAdapter(svc *OpenAIGatewayService, c *gin.Context, resp *http.Response, account *Account, adapter string, stream bool) (err error) {
	switch {
	case adapter == "raw_chat" && stream:
		_, err = svc.streamRawChatCompletions(c, resp, account, "grok-client", "grok-client", "grok-upstream", nil, nil, time.Now(), 0)
	case adapter == "raw_chat":
		_, err = svc.bufferRawChatCompletions(c, resp, account, "grok-client", "grok-client", "grok-upstream", nil, nil, time.Now())
	case adapter == "chat" && stream:
		_, err = svc.handleChatStreamingResponse(resp, c, account, "grok-client", "grok-client", "grok-upstream", time.Now(), 0)
	case adapter == "chat":
		_, err = svc.handleChatBufferedStreamingResponse(resp, c, account, "grok-client", "grok-client", "grok-upstream", time.Now())
	case adapter == "messages" && stream:
		_, err = svc.handleAnthropicStreamingResponse(resp, c, account, "grok-client", "grok-client", "grok-upstream", time.Now())
	case adapter == "messages":
		_, err = svc.handleAnthropicBufferedStreamingResponse(resp, c, account, "grok-client", "grok-client", "grok-upstream", time.Now())
	case stream && adapter == "passthrough":
		_, err = svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "grok-client", "grok-upstream")
	case stream:
		_, err = svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "grok-client", "grok-upstream")
	case adapter == "passthrough":
		_, err = svc.handleNonStreamingResponsePassthrough(context.Background(), resp, c, account, "grok-client", "grok-upstream")
	default:
		_, err = svc.handleNonStreamingResponse(context.Background(), resp, c, account, "grok-client", "grok-upstream")
	}
	return err
}
