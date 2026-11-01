//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Paid probes are explicit opt-in. No application listeners, scheduler, Redis,
// database, production configuration, credentials or raw bodies are written.
// Replay the same genuine provider bytes through Forward to distinguish gateway
// rewriting from nondeterministic differences between two generated answers.
func TestDeepSeekProtocolParityLive(t *testing.T) {
	if os.Getenv("SUB2API_DEEPSEEK_PARITY_LIVE") != "1" {
		t.Skip("explicit paid protocol parity opt-in required")
	}
	key := os.Getenv("SUB2API_DEEPSEEK_PARITY_KEY")
	base := strings.TrimRight(os.Getenv("SUB2API_DEEPSEEK_PARITY_BASE"), "/")
	anthropicBase := strings.TrimRight(os.Getenv("SUB2API_DEEPSEEK_PARITY_ANTHROPIC_BASE"), "/")
	model := os.Getenv("SUB2API_DEEPSEEK_PARITY_MODEL")
	if key == "" || base == "" || anthropicBase == "" || model == "" {
		t.Fatal("required parity environment is unset")
	}
	gin.SetMode(gin.TestMode)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 95 * time.Second}
	schema := map[string]any{"type": "object", "properties": map[string]any{"x": map[string]string{"type": "integer"}}, "required": []string{"x"}, "additionalProperties": false}
	for _, protocol := range []string{"responses", "chat", "anthropic"} {
		for _, stream := range []bool{false, true} {
			for _, tools := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%t/tools=%t", protocol, stream, tools), func(t *testing.T) {
					body := map[string]any{"model": model, "stream": stream}
					prompt := "Return exactly PARITY_OK."
					if tools {
						prompt = "Call parity_lookup with x=7 exactly once. Do not invent its result."
					}
					path, url := "/v1/responses", base+"/responses"
					switch protocol {
					case "responses":
						body["input"] = prompt
						body["max_output_tokens"] = 768
						body["reasoning"] = map[string]string{"effort": "none"}
						body["store"] = false
						if tools {
							body["tools"] = []any{map[string]any{"type": "function", "name": "parity_lookup", "parameters": schema}}
							body["tool_choice"] = map[string]string{"type": "function", "name": "parity_lookup"}
						}
					case "chat":
						path, url = "/v1/chat/completions", base+"/chat/completions"
						body["messages"] = []any{map[string]string{"role": "user", "content": prompt}}
						body["max_tokens"] = 768
						body["thinking"] = map[string]string{"type": "disabled"}
						if tools {
							body["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{"name": "parity_lookup", "parameters": schema}}}
							body["tool_choice"] = map[string]any{"type": "function", "function": map[string]string{"name": "parity_lookup"}}
						}
					case "anthropic":
						path, url = "/v1/messages", anthropicBase+"/v1/messages"
						body["messages"] = []any{map[string]string{"role": "user", "content": prompt}}
						body["max_tokens"] = 768
						body["thinking"] = map[string]string{"type": "disabled"}
						if tools {
							body["tools"] = []any{map[string]any{"name": "parity_lookup", "input_schema": schema}}
							body["tool_choice"] = map[string]string{"type": "tool", "name": "parity_lookup"}
						}
					}
					encoded, _ := json.Marshal(body)
					req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, url, bytes.NewReader(encoded))
					req.Header.Set("Authorization", "Bearer "+key)
					req.Header.Set("Content-Type", "application/json")
					if protocol == "anthropic" {
						req.Header.Set("x-api-key", key)
						req.Header.Set("anthropic-version", "2023-06-01")
					}
					response, err := client.Do(req)
					if err != nil {
						t.Fatal("direct protocol transport failed; private diagnostic withheld")
					}
					raw, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20))
					response.Body.Close()
					if readErr != nil {
						t.Fatal("direct response read failed")
					}
					// Assertions below emit only codes and field names, never provider bodies.
					if response.StatusCode != 200 {
						t.Fatalf("direct HTTP status=%d", response.StatusCode)
					}
					upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: response.StatusCode, Header: response.Header.Clone(), Body: io.NopCloser(bytes.NewReader(raw))}}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), cache: &reasoningRecordingCache{}, httpUpstream: upstream}
					account := deepSeekNativeReplayAccount()
					account.Credentials = map[string]any{"api_key": key, "base_url": base, "api_protocol": APIProtocolAdaptive, "api_base_urls": map[string]any{APIProtocolResponses: base, APIProtocolChatCompletions: base, APIProtocolAnthropic: anthropicBase}}
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
					c.Request.Header = req.Header.Clone()
					c.Set("api_key", &APIKey{ID: 21})
					var forwardErr error
					switch protocol {
					case "responses":
						_, forwardErr = svc.Forward(context.Background(), c, account, encoded)
					case "chat":
						_, forwardErr = svc.ForwardAsChatCompletions(context.Background(), c, account, encoded, "", "")
					case "anthropic":
						_, forwardErr = svc.ForwardAsAnthropic(context.Background(), c, account, encoded, "", "")
					}
					if forwardErr != nil {
						t.Fatalf("candidate forwarding failed, status=%d", rec.Code)
					}
					if rec.Code != 200 {
						t.Fatalf("candidate HTTP status=%d", rec.Code)
					}
					if upstream.lastReq == nil || upstream.lastReq.URL.String() != url {
						t.Fatal("native endpoint unexpectedly converted")
					}
					var before, after any
					json.Unmarshal(encoded, &before)
					json.Unmarshal(upstream.lastBody, &after)
					if !reflect.DeepEqual(before, after) {
						t.Fatal("native request fields changed; body withheld")
					}
					if stream {
						a, b := deepSeekParityWireFrames(raw), deepSeekParityWireFrames(rec.Body.Bytes())
						if !reflect.DeepEqual(a, b) {
							t.Fatalf("native SSE payload changed: provider=%d candidate=%d frames", len(a), len(b))
						}
					} else {
						json.Unmarshal(raw, &before)
						json.Unmarshal(rec.Body.Bytes(), &after)
						if !reflect.DeepEqual(before, after) {
							t.Fatal("native JSON payload changed; body withheld")
						}
					}
					t.Logf("native parity status=200 request_preserved=true response_preserved=true stream=%t", stream)
				})
			}
		}
	}
}

func deepSeekParityWireFrames(raw []byte) []any {
	var out []any
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			out = append(out, data)
			continue
		}
		// Ignore transport keepalive comments; compare every actual JSON event.
		if !gjson.Valid(data) {
			out = append(out, "INVALID_JSON")
			continue
		}
		var obj any
		json.Unmarshal([]byte(data), &obj)
		out = append(out, obj)
	}
	return out
}
