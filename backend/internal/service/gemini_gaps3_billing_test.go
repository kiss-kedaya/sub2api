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

const geminiGaps3Error = `{"error":{"code":503,"status":"UNAVAILABLE","message":"synthetic failure"}}`
const geminiGaps3EmptyStop = `{"candidates":[{"finishReason":"STOP"}],` + geminiB2Usage + `}`

func geminiGaps3Forward(t *testing.T, native bool, mode, body string, readErr error) (*ForwardResult, error, *gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	account := geminiSignalTestAccount()
	account.Credentials["base_url"] = "https://example.invalid"
	if mode == "oauth-collected" {
		account.Type = AccountTypeOAuth
		account.Credentials = map[string]any{"access_token": "synthetic-token", "project_id": "synthetic-project"}
	}
	svc := &GeminiMessagesCompatService{cfg: &config.Config{}, tokenProvider: &GeminiTokenProvider{}, httpUpstream: &geminiBridgeUpstream{
		do: func(*http.Request) (*http.Response, error) {
			response := geminiBridgeResponse(body)
			if readErr != nil {
				response.Body = &geminiBridgeEndingReader{Reader: strings.NewReader(body), err: readErr}
			}
			return response, nil
		},
	}}
	c, rec := geminiBridgeContext()
	stream := mode == "stream"
	if native {
		action := "generateContent"
		if stream {
			action = "streamGenerateContent"
		}
		result, err := svc.ForwardNative(context.Background(), c, account, "gemini-3.7-flash", action, stream, geminiSignalTestRequest())
		return result, err, c, rec
	}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	request := []byte(fmt.Sprintf(`{"model":"gemini-3.7-flash","max_tokens":64,"stream":%t,"messages":[{"role":"user","content":"hello"}]}`, stream))
	result, err := svc.Forward(context.Background(), c, account, request)
	return result, err, c, rec
}

func TestGeminiGaps3NamedErrorBilling(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, mode := range []string{"stream", "oauth-collected"} {
			for _, crlf := range []bool{false, true} {
				t.Run(fmt.Sprintf("native=%t/%s/crlf=%t", native, mode, crlf), func(t *testing.T) {
					body := geminiB2SSE(mode == "oauth-collected", geminiB2Partial) +
						"event: error\n: provider comment\ndata: {\"code\":503,\"status\":\"UNAVAILABLE\",\"message\":\"synthetic failure\"}\n\n"
					if crlf {
						body = strings.ReplaceAll(body, "\n", "\r\n")
					}
					result, err, c, rec := geminiGaps3Forward(t, native, mode, body, nil)
					require.NotNil(t, result)
					if native && mode == "stream" {
						require.NoError(t, err)
						require.Equal(t, 1, strings.Count(strings.ReplaceAll(rec.Body.String(), "\r\n", "\n"), "event: error\n"))
					} else {
						require.ErrorContains(t, err, "upstream response failed")
						require.True(t, IsResponseCommitted(c))
						require.Contains(t, rec.Body.String(), `"error"`)
					}
					require.NotContains(t, rec.Body.String(), "event: message_stop\n")
					require.Equal(t, ClaudeUsage{InputTokens: 10, OutputTokens: 2}, result.Usage)
					requireGeminiSignalBilling(t, result, true)
				})
			}
		}
	}
}

func TestGeminiGaps3EmptyStopAndInvisibleContentStayFree(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, mode := range []string{"stream", "oauth-collected"} {
			t.Run(fmt.Sprintf("native=%t/%s", native, mode), func(t *testing.T) {
				body := geminiB2SSE(mode == "oauth-collected", geminiGaps3Error, geminiGaps3EmptyStop)
				result, err, _, rec := geminiGaps3Forward(t, native, mode, body, nil)
				require.NotNil(t, result)
				if !native || mode == "oauth-collected" {
					require.Error(t, err)
					require.Contains(t, rec.Body.String(), `"error"`)
				}
				requireGeminiSignalBilling(t, result, true)
			})
		}
	}
	for _, frame := range []string{
		`{"candidates":[{"content":{"parts":[{"executableCode":{"code":"print(1)"}}]},"finishReason":"STOP"}],` + geminiB2Usage + `}`,
		`{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"c3ludGhldGlj"}}]},"finishReason":"STOP"}],` + geminiB2Usage + `}`,
		`{"candidates":[{"finishReason":"STOP"},{"index":1,"content":{"parts":[{"text":"invisible second candidate"}]}}],` + geminiB2Usage + `}`,
	} {
		t.Run(frame, func(t *testing.T) {
			result, err, _, rec := geminiGaps3Forward(t, false, "oauth-collected", geminiB2SSE(true, geminiGaps3Error, frame), nil)
			require.Error(t, err)
			require.Contains(t, rec.Body.String(), `"error"`)
			requireGeminiSignalBilling(t, result, true)
		})
	}
}

func TestGeminiGaps3DeliveredAnswerRecoversBilling(t *testing.T) {
	for _, native := range []bool{false, true} {
		parts := []string{`{"text":"complete answer"}`, `{"functionCall":{"name":"synthetic_tool","args":{}}}`}
		if native {
			parts = append(parts, `{"inlineData":{"mimeType":"image/png","data":"c3ludGhldGlj"}}`)
		}
		for _, mode := range []string{"stream", "oauth-collected"} {
			for _, part := range parts {
				t.Run(fmt.Sprintf("native=%t/%s/%s", native, mode, part), func(t *testing.T) {
					frame := `{"candidates":[{"content":{"parts":[` + part + `]}}],` + geminiB2Usage + `}`
					body := "event: error\ndata: {\"code\":503,\"message\":\"synthetic prelude\"}\n\n" + geminiB2SSE(mode == "oauth-collected", frame, geminiGaps3EmptyStop)
					result, err, _, rec := geminiGaps3Forward(t, native, mode, body, nil)
					require.NoError(t, err)
					require.NotNil(t, result)
					if !native {
						require.NotContains(t, rec.Body.String(), `"error"`)
					}
					for _, marker := range []string{"complete answer", "synthetic_tool", "c3ludGhldGlj"} {
						if strings.Contains(part, marker) {
							require.Contains(t, rec.Body.String(), marker)
						}
					}
					requireGeminiSignalBilling(t, result, false)
				})
			}
		}
	}
}

func TestGeminiGaps3ExplicitErrorThenReadFailureRetainsUsage(t *testing.T) {
	for _, mode := range []string{"stream", "oauth-collected"} {
		t.Run(mode, func(t *testing.T) {
			body := geminiB2SSE(mode == "oauth-collected", geminiB2Partial, geminiGaps3Error)
			result, err, c, rec := geminiGaps3Forward(t, false, mode, body, io.ErrUnexpectedEOF)
			require.ErrorContains(t, err, "upstream response failed")
			require.NotNil(t, result)
			require.True(t, IsResponseCommitted(c))
			require.Equal(t, ClaudeUsage{InputTokens: 10, OutputTokens: 2}, result.Usage)
			require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"error"`))
			require.NotContains(t, rec.Body.String(), "event: message_stop\n")
			requireGeminiSignalBilling(t, result, true)
		})
	}
}

func TestGeminiGaps3NativeCollectedFailureIsNotSuccess(t *testing.T) {
	for _, terminal := range []string{geminiGaps3Error, "named"} {
		t.Run(terminal, func(t *testing.T) {
			body := geminiB2SSE(true, geminiB2Partial)
			if terminal == "named" {
				body += "event: error\ndata: {\"code\":503,\"message\":\"synthetic failure\"}\n\n"
			} else {
				body += geminiB2SSE(true, terminal)
			}
			result, err, c, rec := geminiGaps3Forward(t, true, "oauth-collected", body, nil)
			require.ErrorContains(t, err, "upstream response failed")
			require.True(t, IsResponseCommitted(c))
			require.Equal(t, http.StatusServiceUnavailable, rec.Code)
			require.True(t, gjson.GetBytes(rec.Body.Bytes(), "error").IsObject())
			require.False(t, gjson.GetBytes(rec.Body.Bytes(), "candidates").Exists())
			requireGeminiSignalBilling(t, result, true)
		})
	}
}

func TestGeminiGaps3ChatStringErrorAndTruncation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/explicit=%t", stream, explicit), func(t *testing.T) {
				if !stream && !explicit {
					return
				}
				body := `{"error":"synthetic failure","usage":` + geminiBridgeChatUsage + `}`
				if stream {
					body = "data: {\"choices\":[],\"usage\":" + geminiBridgeChatUsage + "}\n\n"
					if explicit {
						body += "data: {\"error\":\"synthetic failure\"}\n\n"
					}
				}
				svc := &GeminiMessagesCompatService{httpUpstream: &geminiBridgeUpstream{do: func(*http.Request) (*http.Response, error) { return geminiBridgeResponse(body), nil }}}
				c, rec := geminiBridgeContext()
				account := geminiBridgeAccount(APIProtocolChatCompletions)
				account.Credentials["base_url"] = "https://example.invalid/v1"
				action := "generateContent"
				if stream {
					action = "streamGenerateContent"
				}
				result, err := svc.ForwardNative(context.Background(), c, account, "gemini-2.5-flash", action, stream, []byte(geminiBridgeRequest))
				require.Error(t, err)
				require.Contains(t, rec.Body.String(), `"error"`)
				require.NotNil(t, result)
				require.Equal(t, ClaudeUsage{InputTokens: 300, OutputTokens: 50, CacheReadInputTokens: 600, CacheCreationInputTokens: 100}, result.Usage)
				requireGeminiSignalBilling(t, result, explicit)
			})
		}
	}
}

func TestGeminiGaps3ChatAnswerWithErrorMetadataIsBillable(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			body := `{"choices":[{"message":{"content":"complete answer"},"finish_reason":"stop"}],"error":"synthetic relay note","usage":` + geminiBridgeChatUsage + `}`
			if stream {
				body = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"complete answer\"},\"finish_reason\":\"stop\"}],\"error\":\"synthetic relay note\",\"usage\":" + geminiBridgeChatUsage + "}\n\ndata: [DONE]\n\n"
			}
			svc := &GeminiMessagesCompatService{httpUpstream: &geminiBridgeUpstream{do: func(*http.Request) (*http.Response, error) { return geminiBridgeResponse(body), nil }}}
			c, rec := geminiBridgeContext()
			account := geminiBridgeAccount(APIProtocolChatCompletions)
			account.Credentials["base_url"] = "https://example.invalid/v1"
			action := "generateContent"
			if stream {
				action = "streamGenerateContent"
			}
			result, err := svc.ForwardNative(context.Background(), c, account, "gemini-2.5-flash", action, stream, []byte(geminiBridgeRequest))
			require.NoError(t, err)
			require.Contains(t, rec.Body.String(), "complete answer")
			require.Equal(t, ClaudeUsage{InputTokens: 300, OutputTokens: 50, CacheReadInputTokens: 600, CacheCreationInputTokens: 100}, result.Usage)
			requireGeminiSignalBilling(t, result, false)
		})
	}
}

func TestGeminiGaps3PolicyAndPlainStatusKeepBilling(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, mode := range []string{"stream", "buffered", "oauth-collected"} {
			for _, frame := range []string{
				`{"promptFeedback":{"blockReason":"SAFETY"},` + geminiB2Usage + `}`,
				`{"candidates":[{"finishReason":"PROHIBITED_CONTENT"}],` + geminiB2Usage + `}`,
				`{"code":503,"status":"UNAVAILABLE","message":"synthetic metadata",` + geminiB2Usage + `}`,
			} {
				t.Run(fmt.Sprintf("native=%t/%s/%s", native, mode, frame), func(t *testing.T) {
					body := frame
					if mode != "buffered" {
						body = geminiB2SSE(mode == "oauth-collected", frame)
					}
					result, err, _, rec := geminiGaps3Forward(t, native, mode, body, nil)
					require.NoError(t, err)
					require.Equal(t, http.StatusOK, rec.Code)
					require.NotContains(t, rec.Body.String(), `"error"`)
					require.NotNil(t, result)
					requireGeminiSignalBilling(t, result, false)
				})
			}
		}
	}
}

func TestGeminiGaps3LaterErrorOverridesRecoveredAnswer(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, mode := range []string{"stream", "oauth-collected"} {
			t.Run(fmt.Sprintf("native=%t/%s", native, mode), func(t *testing.T) {
				body := geminiB2SSE(mode == "oauth-collected", geminiGaps3Error, geminiB2Success, geminiGaps3Error)
				result, err, _, rec := geminiGaps3Forward(t, native, mode, body, nil)
				if !native || mode == "oauth-collected" {
					require.ErrorContains(t, err, "upstream response failed")
				}
				require.Contains(t, rec.Body.String(), `"error"`)
				require.NotNil(t, result)
				requireGeminiSignalBilling(t, result, true)
			})
		}
	}
}

func TestGeminiGaps3BufferedExplicitErrorKeepsUsage(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(fmt.Sprint(native), func(t *testing.T) {
			body := strings.TrimSuffix(geminiGaps3Error, "}") + "," + geminiB2Usage + "}"
			result, err, _, rec := geminiGaps3Forward(t, native, "buffered", body, nil)
			if !native {
				require.ErrorContains(t, err, "upstream response failed")
			}
			require.Contains(t, rec.Body.String(), `"error"`)
			require.NotNil(t, result)
			requireGeminiSignalBilling(t, result, true)
		})
	}
}
