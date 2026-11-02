//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// A literal code example must not silently become provider reasoning. Exercise
// the real non-passthrough transport path; helper-only tests missed this gate.
func TestDeepSeekNativeReasoningReplayResponseCompatOptIn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("stream=%t/legacyCompat=%t", stream, enabled), func(t *testing.T) {
				literal := "<think>literal source code, not private reasoning</think>PARITY_OK"
				response := hostedSearchResponse("resp_transparency", 8, 4,
					map[string]any{"type": "message", "id": "msg_literal", "role": "assistant", "status": "completed",
						"content": []any{map[string]any{"type": "output_text", "text": literal, "annotations": []any{}}}})
				wire, contentType := string(response), "application/json"
				if stream {
					wire, contentType = hostedSearchSSE(response), "text/event-stream"
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(wire))}}
				cfg := rawChatCompletionsTestConfig()
				cfg.Gateway.DeepSeekResponseCompatEnabled = enabled
				svc := &OpenAIGatewayService{cfg: cfg, cache: &reasoningRecordingCache{}, httpUpstream: upstream}
				c, rec := deepSeekNativeReplayContext(21, "literal-code")
				body := []byte(fmt.Sprintf("{\"model\":\"deepseek-flash\",\"input\":\"Return this literal code\",\"stream\":%t}", stream))
				c.Request.Body = io.NopCloser(bytes.NewReader(body))
				_, err := svc.Forward(context.Background(), c, deepSeekNativeReplayAccount(), body)
				require.NoError(t, err, rec.Body.String())
				require.Equal(t, 200, rec.Code)
				output := gjson.Get(rec.Body.String(), "output")
				if stream {
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						if !strings.HasPrefix(line, "data:") {
							continue
						}
						frame := gjson.Parse(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
						if frame.Get("type").String() == "response.completed" {
							output = frame.Get("response.output")
						}
					}
				}
				require.True(t, output.IsArray(), rec.Body.String())
				if !enabled {
					require.JSONEq(t, gjson.GetBytes(response, "output").Raw, output.Raw, "native output must bypass all legacy inline rewriting")
				} else {
					require.NotContains(t, output.Raw, "<think>", "legacy rewriting still requires an explicit operator opt-in")
				}
			})
		}
	}
}
