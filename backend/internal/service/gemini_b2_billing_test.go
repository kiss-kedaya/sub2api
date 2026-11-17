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

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const geminiB2Usage = `"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2}`
const geminiB2Partial = `{"candidates":[{"content":{"parts":[{"text":"partial answer"}]}}],` + geminiB2Usage + `}`
const geminiB2Success = `{"candidates":[{"content":{"parts":[{"text":"complete answer"}]},"finishReason":"STOP"}],` + geminiB2Usage + `}`

func geminiB2SSE(wrapped bool, frames ...string) string {
	var body strings.Builder
	for _, frame := range frames {
		if wrapped {
			frame = `{"response":` + frame + `}`
		}
		fmt.Fprintf(&body, "data: %s\n\n", frame)
	}
	return body.String()
}

func TestGeminiB2MessagesExplicitErrorBilling(t *testing.T) {
	for _, mode := range []string{"stream", "buffered", "oauth-collected"} {
		for _, status := range []int{http.StatusBadRequest, http.StatusServiceUnavailable} {
			t.Run(fmt.Sprintf("%s/%d", mode, status), func(t *testing.T) {
				account := geminiSignalTestAccount()
				if mode == "oauth-collected" {
					account.Type = AccountTypeOAuth
					account.Credentials = map[string]any{"access_token": "synthetic-token", "project_id": "synthetic-project"}
				}
				errorFrame := fmt.Sprintf(`{"error":{"code":%d,"status":"UNAVAILABLE","message":"synthetic failure"}}`, status)
				if mode == "buffered" {
					errorFrame = strings.TrimSuffix(errorFrame, "}") + "," + geminiB2Usage + "}"
				} else {
					errorFrame = geminiB2SSE(mode == "oauth-collected", geminiB2Partial, errorFrame)
				}
				body := errorFrame
				svc := &GeminiMessagesCompatService{cfg: &config.Config{}, tokenProvider: &GeminiTokenProvider{}, httpUpstream: &geminiBridgeUpstream{
					do: func(*http.Request) (*http.Response, error) {
						return geminiBridgeResponse(body), nil
					},
				}}
				request := []byte(fmt.Sprintf(`{"model":"gemini-3.7-flash","max_tokens":64,"stream":%t,"messages":[{"role":"user","content":"hello"}]}`, mode == "stream"))
				for _, failed := range []bool{true, false} {
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
					result, err := svc.Forward(context.Background(), c, account, request)
					require.NotNil(t, result)
					require.Equal(t, ClaudeUsage{InputTokens: 10, OutputTokens: 2}, result.Usage)
					if failed {
						require.Error(t, err)
						if mode == "stream" {
							require.Contains(t, recorder.Body.String(), "event: error\n")
							require.NotContains(t, recorder.Body.String(), "event: message_stop\n")
						} else {
							require.GreaterOrEqual(t, recorder.Code, http.StatusBadRequest)
							require.Equal(t, "error", gjson.GetBytes(recorder.Body.Bytes(), "type").String())
						}
					} else {
						require.NoError(t, err)
						require.Contains(t, recorder.Body.String(), "complete answer")
					}
					requireGeminiSignalBilling(t, result, failed)
					body = geminiB2Success
					if mode != "buffered" {
						body = geminiB2SSE(mode == "oauth-collected", body)
					}
				}
			})
		}
	}
}

func TestGeminiB2MessagesErrorPreludeSuccess(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			account := geminiSignalTestAccount()
			if !stream {
				account.Type = AccountTypeOAuth
				account.Credentials = map[string]any{"access_token": "synthetic-token", "project_id": "synthetic-project"}
			}
			svc := newGeminiSignalService("text/event-stream", geminiB2SSE(!stream,
				`{"error":{"code":503,"message":"synthetic prelude"}}`, geminiB2Success))
			svc.tokenProvider = &GeminiTokenProvider{}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			request := []byte(fmt.Sprintf(`{"model":"gemini-3.7-flash","max_tokens":64,"stream":%t,"messages":[{"role":"user","content":"hello"}]}`, stream))
			result, err := svc.Forward(context.Background(), c, account, request)
			require.NoError(t, err)
			require.NotContains(t, recorder.Body.String(), `"error"`)
			require.Equal(t, 1, strings.Count(recorder.Body.String(), "complete answer"))
			requireGeminiSignalBilling(t, result, false)
		})
	}
}

func TestGeminiB2MessagesPolicyKeepsBilling(t *testing.T) {
	for _, stream := range []bool{true, false} {
		for _, frame := range []string{
			`{"promptFeedback":{"blockReason":"SAFETY"},` + geminiB2Usage + `}`,
			`{"candidates":[{"finishReason":"PROHIBITED_CONTENT"}],` + geminiB2Usage + `}`,
		} {
			t.Run(fmt.Sprintf("%t/%s", stream, frame), func(t *testing.T) {
				body := frame
				if stream {
					body = geminiB2SSE(false, frame)
				}
				svc := newGeminiSignalService("application/json", body)
				c, _ := geminiBridgeContext()
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				request := []byte(fmt.Sprintf(`{"model":"gemini-3.7-flash","max_tokens":64,"stream":%t,"messages":[{"role":"user","content":"hello"}]}`, stream))
				result, err := svc.Forward(context.Background(), c, geminiSignalTestAccount(), request)
				require.NoError(t, err)
				requireGeminiSignalBilling(t, result, false)
			})
		}
	}
}

func TestGeminiB2MessagesJSONErrorOnStream(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			account := geminiSignalTestAccount()
			if !stream {
				account.Type = AccountTypeOAuth
				account.Credentials = map[string]any{"access_token": "synthetic-token", "project_id": "synthetic-project"}
			}
			svc := newGeminiSignalService("application/json", `{"error":{"code":503,"message":"synthetic failure"},`+geminiB2Usage+`}`)
			svc.tokenProvider = &GeminiTokenProvider{}
			c, recorder := geminiBridgeContext()
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			request := []byte(fmt.Sprintf(`{"model":"gemini-3.7-flash","max_tokens":64,"stream":%t,"messages":[{"role":"user","content":"hello"}]}`, stream))
			result, err := svc.Forward(context.Background(), c, account, request)
			require.Error(t, err)
			require.NotNil(t, result)
			require.Contains(t, recorder.Body.String(), `"error"`)
			require.Equal(t, ClaudeUsage{InputTokens: 10, OutputTokens: 2}, result.Usage)
			requireGeminiSignalBilling(t, result, true)
		})
	}
}

func TestGeminiB2MessagesEmptyStopDoesNotRecoverBilling(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			account := geminiSignalTestAccount()
			if !stream {
				account.Type = AccountTypeOAuth
				account.Credentials = map[string]any{"access_token": "synthetic-token", "project_id": "synthetic-project"}
			}
			svc := newGeminiSignalService("text/event-stream", geminiB2SSE(!stream,
				`{"error":{"code":503,"message":"synthetic prelude"}}`,
				`{"candidates":[{"finishReason":"STOP"}],`+geminiB2Usage+`}`))
			svc.tokenProvider = &GeminiTokenProvider{}
			c, _ := geminiBridgeContext()
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			request := []byte(fmt.Sprintf(`{"model":"gemini-3.7-flash","max_tokens":64,"stream":%t,"messages":[{"role":"user","content":"hello"}]}`, stream))
			result, err := svc.Forward(context.Background(), c, account, request)
			require.Error(t, err)
			requireGeminiSignalBilling(t, result, true)
		})
	}
}

func TestGeminiB2BridgeEmptyCompletionDoesNotRecoverBilling(t *testing.T) {
	body := "event: error\ndata: {\"error\":{\"code\":\"server_error\",\"message\":\"synthetic prelude\"}}\n\n" +
		"event: response.completed\ndata: {\"response\":{\"status\":\"completed\",\"usage\":" + geminiBridgeResponsesUsage + "}}\n\n"
	svc := &GeminiMessagesCompatService{httpUpstream: &geminiBridgeUpstream{do: func(*http.Request) (*http.Response, error) {
		return geminiBridgeResponse(body), nil
	}}}
	c, recorder := geminiBridgeContext()
	account := geminiBridgeAccount(APIProtocolResponses)
	result, err := svc.ForwardNative(context.Background(), c, account, "gemini-2.5-flash", "streamGenerateContent", true, []byte(geminiBridgeRequest))
	require.Error(t, err)
	require.Contains(t, recorder.Body.String(), `"error"`)
	requireGeminiBridgeBilling(t, result, account, true)
}

func TestGeminiB2MessagesReadFailuresKeepExistingResult(t *testing.T) {
	for _, mode := range []string{"stream", "buffered", "oauth-collected"} {
		t.Run(mode, func(t *testing.T) {
			account := geminiSignalTestAccount()
			if mode == "oauth-collected" {
				account.Type = AccountTypeOAuth
				account.Credentials = map[string]any{"access_token": "synthetic-token", "project_id": "synthetic-project"}
			}
			body := geminiB2Partial
			if mode != "buffered" {
				body = geminiB2SSE(mode == "oauth-collected", body)
			}
			svc := &GeminiMessagesCompatService{cfg: &config.Config{}, tokenProvider: &GeminiTokenProvider{}, httpUpstream: &geminiBridgeUpstream{
				do: func(*http.Request) (*http.Response, error) {
					response := geminiBridgeResponse("")
					response.Body = &geminiBridgeEndingReader{Reader: strings.NewReader(body), err: io.ErrUnexpectedEOF}
					return response, nil
				},
			}}
			c, _ := geminiBridgeContext()
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			request := []byte(fmt.Sprintf(`{"model":"gemini-3.7-flash","max_tokens":64,"stream":%t,"messages":[{"role":"user","content":"hello"}]}`, mode == "stream"))
			result, err := svc.Forward(context.Background(), c, account, request)
			require.Error(t, err)
			require.Nil(t, result, "a read error must not become a new usage result or upstream-error waiver")
		})
	}
}

func TestGeminiB2BridgeErrorPreludeWithoutSuccessIsFree(t *testing.T) {
	body := "event: error\ndata: {\"error\":{\"code\":\"server_error\",\"message\":\"synthetic prelude\"}}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial answer\"}\n\n" +
		"data: {\"type\":\"response.in_progress\",\"response\":{\"usage\":" + geminiBridgeResponsesUsage + "}}\n\n"
	svc := &GeminiMessagesCompatService{httpUpstream: &geminiBridgeUpstream{do: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}}}
	c, recorder := geminiBridgeContext()
	account := geminiBridgeAccount(APIProtocolResponses)
	result, err := svc.ForwardNative(context.Background(), c, account, "gemini-2.5-flash", "streamGenerateContent", true, []byte(geminiBridgeRequest))
	require.Error(t, err)
	require.Contains(t, recorder.Body.String(), `"error"`)
	requireGeminiBridgeBilling(t, result, account, true)
}
