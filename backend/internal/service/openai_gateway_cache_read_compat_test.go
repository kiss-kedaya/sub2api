//go:build unit

package service

import (
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

func TestGrokCacheReadAliasesReachDownstreamAndBilling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"chat", "messages"} {
		for _, stream := range []bool{false, true} {
			name := protocol + "/buffered"
			if stream {
				name = protocol + "/stream"
			}
			t.Run(name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, nil)
				payload := `{"type":"response.completed","response":{"id":"resp_cache","model":"grok-4.6","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":200,"output_tokens":5,"cache_read_input_tokens":128}}}`
				resp := &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": {"text/event-stream"}},
					Body:       io.NopCloser(strings.NewReader("data: " + payload + "\n\ndata: [DONE]\n\n")),
				}
				defer func() { require.NoError(t, resp.Body.Close()) }()
				svc := &OpenAIGatewayService{}
				account := &Account{ID: 1, Platform: PlatformGrok, Type: AccountTypeAPIKey}
				var result *OpenAIForwardResult
				var err error
				switch {
				case protocol == "chat" && stream:
					result, err = svc.handleChatStreamingResponse(resp, c, account, "grok-4.6", "grok-4.6", "grok-4.6", time.Now(), 0)
				case protocol == "chat":
					result, err = svc.handleChatBufferedStreamingResponse(resp, c, account, "grok-4.6", "grok-4.6", "grok-4.6", time.Now())
				case stream:
					result, err = svc.handleAnthropicStreamingResponse(resp, c, account, "grok-4.6", "grok-4.6", "grok-4.6", time.Now())
				default:
					result, err = svc.handleAnthropicBufferedStreamingResponse(resp, c, account, "grok-4.6", "grok-4.6", "grok-4.6", time.Now())
				}
				require.NoError(t, err)
				require.Equal(t, 128, result.Usage.CacheReadInputTokens)
				var wireUsage gjson.Result
				if stream {
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						if strings.HasPrefix(line, "data: ") {
							usage := gjson.Get(strings.TrimPrefix(line, "data: "), "usage")
							if usage.Exists() {
								wireUsage = usage
							}
						}
					}
				} else {
					wireUsage = gjson.Get(rec.Body.String(), "usage")
				}
				field := "prompt_tokens_details.cached_tokens"
				if protocol == "messages" {
					field = "cache_read_input_tokens"
					require.Equal(t, int64(72), wireUsage.Get("input_tokens").Int())
				}
				require.Equal(t, int64(128), wireUsage.Get(field).Int(), "actual serialized downstream cache count")
			})
		}
	}
}
