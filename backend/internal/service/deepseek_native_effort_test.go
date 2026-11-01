//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Reproduce the live direct-vs-gateway mismatch: explicit none was removed,
// enabling thinking and rejecting forced tools; minimal was rewritten to none.
// Assert the real Forward transport body, not just helper return values.
func TestDeepSeekNativeReasoningReplayPreservesNativeEffort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{APIProtocolAdaptive, APIProtocolResponses} {
		for _, stream := range []bool{false, true} {
			for _, effort := range []string{"", "none", "minimal", "high"} {
				for _, flat := range []bool{false, true} {
					if effort == "" && flat {
						continue
					}
					t.Run(fmt.Sprintf("%s/stream=%t/effort=%s/flat=%t", protocol, stream, effort, flat), func(t *testing.T) {
						account := deepSeekNativeReplayAccount()
						account.Credentials["api_protocol"] = protocol
						account.Credentials["model_mapping"] = map[string]any{"deepseek-v4.1-flash-1017": "deepseek-flash"}
						req := map[string]any{
							"model": "deepseek-v4.1-flash-1017", "stream": stream, "store": false,
							"input": []any{map[string]any{"role": "user", "content": "Call lookup with x=7."}},
							"tools": []any{map[string]any{"type": "function", "name": "lookup",
								"parameters": map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "integer"}}}}},
							"tool_choice": "auto", "max_output_tokens": 256,
						}
						path := "reasoning.effort"
						if effort != "" {
							if flat {
								req["reasoning_effort"] = effort
								path = "reasoning_effort"
							} else {
								req["reasoning"] = map[string]any{"effort": effort}
							}
						}
						if effort == "none" {
							req["tool_choice"] = map[string]string{"type": "function", "name": "lookup"}
						}
						body, err := json.Marshal(req)
						require.NoError(t, err)
						call := map[string]any{"type": "function_call", "id": "fc_native", "call_id": "call_native", "name": "lookup", "arguments": `{"x":7}`, "status": "completed"}
						items := []map[string]any{call}
						if effort != "none" {
							items = append([]map[string]any{{"type": "reasoning", "id": "rs_native", "summary": []any{},
								"encrypted_content": "opaque-native", "content": []any{map[string]string{"type": "reasoning_text", "text": "Provider-supplied reasoning."}}}}, call)
						}
						response := hostedSearchResponse("resp_native_effort", 8, 4, items...)
						wire, contentType := string(response), "application/json"
						if stream {
							wire, contentType = hostedSearchSSE(response), "text/event-stream"
						}
						upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(wire))}}
						svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), cache: &reasoningRecordingCache{}, httpUpstream: upstream}
						c, rec := deepSeekNativeReplayContext(21, "native-effort-thread")
						c.Request.Body = io.NopCloser(bytes.NewReader(body))
						_, err = svc.Forward(context.Background(), c, account, body)
						require.NoError(t, err, rec.Body.String())
						require.Equal(t, http.StatusOK, rec.Code)
						require.NotNil(t, upstream.lastReq)
						forwarded := upstream.lastBody
						if effort == "" {
							require.False(t, gjson.GetBytes(forwarded, "reasoning.effort").Exists())
							require.False(t, gjson.GetBytes(forwarded, "reasoning_effort").Exists())
						} else {
							require.Equal(t, effort, gjson.GetBytes(forwarded, path).String(), "preserve the client's explicit native effort")
						}
						for _, field := range []string{"input", "tools", "tool_choice"} {
							require.JSONEq(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(forwarded, field).Raw, field)
						}
						require.Equal(t, "deepseek-flash", gjson.GetBytes(forwarded, "model").String())
						if stream {
							require.Contains(t, rec.Body.String(), "response.completed")
							require.Contains(t, rec.Body.String(), "fc_native")
							require.Contains(t, rec.Body.String(), "call_native")
						} else {
							require.JSONEq(t, gjson.GetBytes(response, "output").Raw, gjson.Get(rec.Body.String(), "output").Raw)
						}
					})
				}
			}
		}
	}
}

func TestDeepSeekNativeReasoningReplayPreservesNoneInDecodedRequest(t *testing.T) {
	for _, protocol := range []string{APIProtocolAdaptive, APIProtocolResponses} {
		t.Run(protocol, func(t *testing.T) {
			account := deepSeekNativeReplayAccount()
			account.Credentials["api_protocol"] = protocol
			body := []byte(`{"reasoning":{"effort":"none","summary":"auto"},"reasoning_effort":"none","tool_choice":{"type":"function","name":"lookup"}}`)
			out, err := filterOpenAIResponsesNoneReasoningEffortForAccount(account, body)
			require.NoError(t, err)
			require.Equal(t, body, out)
			var decoded map[string]any
			require.NoError(t, json.Unmarshal(body, &decoded))
			deleteOpenAIResponsesNoneReasoningEffortFromObject(account, decoded)
			encoded, err := json.Marshal(decoded)
			require.NoError(t, err)
			require.JSONEq(t, string(body), string(encoded))
		})
	}
}

func TestDeepSeekChatCompletionsProtocolStillFiltersNonePlaceholder(t *testing.T) {
	account := deepSeekNativeReplayAccount()
	account.Credentials["api_protocol"] = APIProtocolChatCompletions
	body := []byte(`{"reasoning":{"effort":"none"},"reasoning_effort":"none"}`)
	out, err := filterOpenAIResponsesNoneReasoningEffortForAccount(account, body)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(out, "reasoning.effort").Exists())
	require.False(t, gjson.GetBytes(out, "reasoning_effort").Exists())
}
