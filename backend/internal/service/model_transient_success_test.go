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

	"github.com/Wei-Shaw/sub2api/internal/pkg/typesafe"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestModelTransientForwardSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, kind := range []anthropicDisconnectKind{
		anthropicKindNative, anthropicKindNativeJSON,
		anthropicKindPassthrough, anthropicKindPassthroughJSON,
		anthropicKindChatStream, anthropicKindChatBuffered,
		anthropicKindResponsesStream, anthropicKindResponsesBuffered,
	} {
		for _, outcome := range []string{"success", "incomplete", "http400", "http502", "stream error", "client gone"} {
			t.Run(string(kind)+"/"+outcome, func(t *testing.T) {
				account := newAnthropicDisconnectAccount(kind)
				account.Credentials["model_mapping"] = map[string]any{"claude-sonnet-4": "mapped-model"}
				account.Credentials["pool_mode"] = true
				body := kind.requestBody()
				responseBody := anthropicDisconnectRealStartFrame() + anthropicDisconnectBlockStartFrame() + anthropicDisconnectDeltaFrame("ok")
				// Compatibility endpoints always request SSE, even for buffered clients.
				jsonResponse := kind == anthropicKindNativeJSON || kind == anthropicKindPassthroughJSON
				contentType := "text/event-stream"
				if jsonResponse {
					contentType = "application/json"
					responseBody = `{"id":"msg_success","type":"message","model":"mapped-model","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":1}}`
				}
				if outcome == "incomplete" {
					if jsonResponse {
						responseBody = `{"type":"message"`
					}
				} else if !jsonResponse {
					responseBody += anthropicDisconnectFinalFrames(1)
				}
				if outcome == "stream error" {
					failure := `{"type":"error","error":{"type":"api_error","message":"provider unavailable"}}`
					if jsonResponse {
						responseBody = failure
					} else {
						responseBody = "event: error\ndata: " + failure + "\n\n" + responseBody
					}
				}
				status := http.StatusOK
				if outcome == "http400" || outcome == "http502" {
					status = http.StatusBadRequest
					if outcome == "http502" {
						status = http.StatusBadGateway
					}
					responseBody = `{"type":"error","error":{"type":"api_error","message":"provider unavailable"}}`
				}
				upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
					StatusCode: status, Header: http.Header{"Content-Type": {contentType}},
					Body: io.NopCloser(strings.NewReader(responseBody)),
				}}
				svc := newAnthropicDisconnectService(upstream, 0)
				svc.rateLimitService = NewRateLimitService(nil, nil, svc.cfg, nil, nil)
				cooldown := svc.rateLimitService.modelTransient
				now := time.Now()
				cooldown.now = func() time.Time { return now }
				for range 3 {
					svc.rateLimitService.HandleUpstreamError(context.Background(), account, http.StatusBadGateway, nil, nil, "mapped-model")
				}
				cooldown.ObserveFailure(account.ID, "other-model")
				cooldown.ObserveFailure(account.ID, "other-model")
				cooldown.ObserveFailure(account.ID+1, "mapped-model")
				cooldown.ObserveFailure(account.ID+1, "mapped-model")
				require.False(t, cooldown.Allow(account.ID, "mapped-model"))
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				c.Set("api_key", &APIKey{Group: &Group{CrossProtocolConversionEnabled: true}})
				if outcome == "client gone" {
					c.Writer = &failWriteResponseWriter{ResponseWriter: c.Writer}
				}
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
				require.NoError(t, err)
				var result *ForwardResult
				switch kind {
				case anthropicKindChatStream, anthropicKindChatBuffered:
					result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, parsed)
				case anthropicKindResponsesStream, anthropicKindResponsesBuffered:
					result, err = svc.ForwardAsResponses(context.Background(), c, account, body, parsed)
				default:
					result, err = svc.Forward(context.Background(), c, account, parsed)
				}
				if outcome == "success" {
					require.NoError(t, err)
					require.NotNil(t, result)
					require.False(t, result.ClientDisconnect)
					require.Contains(t, recorder.Body.String(), "ok")
					require.True(t, cooldown.Allow(account.ID, "mapped-model"), "completed responses must clear the failure's mapped model key")
				} else {
					require.False(t, cooldown.Allow(account.ID, "mapped-model"), "failed or interrupted responses must retain cooldown")
				}
				require.False(t, cooldown.Allow(account.ID, "other-model"))
				require.False(t, cooldown.Allow(account.ID+1, "mapped-model"))
			})
		}
	}
}

func TestModelTransientSystemOneSuccess(t *testing.T) {
	account := newAnthropicAPIKeyAccountForTest()
	account.Platform = PlatformTypeSafe
	svc := newSystemOneTestService(&systemOneHTTPUpstream{do: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"answers":{},"usage":{"input_tokens":3}}`))}, nil
	}})
	svc.rateLimitService = NewRateLimitService(nil, nil, svc.cfg, nil, nil)
	for range 2 {
		svc.rateLimitService.HandleUpstreamError(context.Background(), account, http.StatusBadGateway, nil, nil, typesafe.JevLatestModel)
	}
	result, err := svc.ForwardSystemOne(context.Background(), newSystemOneTestContext(), account, []byte(`{}`))
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, svc.rateLimitService.ModelTransientAllowed(account.ID, typesafe.JevLatestModel))
}

func TestModelTransientOpenAINativeAnthropicSuccess(t *testing.T) {
	for _, platform := range []string{PlatformKimi, PlatformGemini} {
		for _, stream := range []bool{false, true} {
			for _, outcome := range []string{"success", "incomplete", "stream error", "client gone"} {
				t.Run(fmt.Sprintf("%s/stream=%t/%s", platform, stream, outcome), func(t *testing.T) {
					account := newAnthropicAPIKeyAccountForTest()
					account.Platform = platform
					account.Credentials["api_protocol"] = APIProtocolAnthropic
					account.Credentials["model_mapping"] = map[string]any{"public-model": "mapped-model"}
					response := `{"id":"msg_success","type":"message","model":"mapped-model","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":3,"output_tokens":1}}`
					if stream {
						response = anthropicDisconnectRealStartFrame() + anthropicDisconnectBlockStartFrame() + anthropicDisconnectDeltaFrame("ok")
						if outcome != "incomplete" {
							response += anthropicDisconnectFinalFrames(1)
						}
					} else if outcome == "incomplete" {
						response = `{"type":"message"`
					}
					if outcome == "stream error" {
						failure := `{"type":"error","error":{"type":"api_error","message":"provider unavailable"}}`
						if stream {
							response = "event: error\ndata: " + failure + "\n\n" + response
						} else {
							response = failure
						}
					}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: &anthropicHTTPUpstreamRecorder{resp: &http.Response{
						StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response)),
					}}}
					svc.rateLimitService = NewRateLimitService(nil, nil, svc.cfg, nil, nil)
					for range 2 {
						svc.rateLimitService.modelTransient.ObserveFailure(account.ID, "mapped-model")
					}
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
					if outcome == "client gone" {
						c.Writer = &failWriteResponseWriter{ResponseWriter: c.Writer}
					}
					body := []byte(fmt.Sprintf(`{"model":"public-model","max_tokens":8,"stream":%t,"messages":[{"role":"user","content":"hello"}]}`, stream))
					result, err := svc.forwardAnthropicViaNativeAnthropicEndpoint(context.Background(), c, account, body, "mapped-model")
					if outcome == "success" {
						require.NoError(t, err)
						require.NotNil(t, result)
						require.False(t, result.ClientDisconnect)
					}
					require.Equal(t, outcome == "success", svc.rateLimitService.ModelTransientAllowed(account.ID, "mapped-model"))
				})
			}
		}
	}
}
