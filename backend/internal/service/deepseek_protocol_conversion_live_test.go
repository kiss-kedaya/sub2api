//go:build unit

package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// Each fixed upstream protocol exercises an intentionally required conversion.
// It sends synthetic paid requests only; no listeners or production DB access.
func TestDeepSeekProtocolConversionLive(t *testing.T) {
	if os.Getenv("SUB2API_DEEPSEEK_PARITY_LIVE") != "1" {
		t.Skip("explicit paid protocol parity opt-in required")
	}
	key, base, anthBase := os.Getenv("SUB2API_DEEPSEEK_PARITY_KEY"), strings.TrimRight(os.Getenv("SUB2API_DEEPSEEK_PARITY_BASE"), "/"), strings.TrimRight(os.Getenv("SUB2API_DEEPSEEK_PARITY_ANTHROPIC_BASE"), "/")
	model := os.Getenv("SUB2API_DEEPSEEK_PARITY_MODEL")
	if key == "" || base == "" || anthBase == "" || model == "" {
		t.Fatal("required parity environment is unset")
	}
	gin.SetMode(gin.TestMode)
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 95 * time.Second}
	schema := map[string]any{"type": "object", "properties": map[string]any{"x": map[string]string{"type": "integer"}}, "required": []string{"x"}, "additionalProperties": false}
	protocols := []string{APIProtocolResponses, APIProtocolChatCompletions, APIProtocolAnthropic}
	for _, target := range protocols {
		for _, input := range protocols {
			if target == input {
				continue
			}
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s_to_%s/stream=%t", input, target, stream), func(t *testing.T) {
					prompt := "Call parity_lookup with x=7 exactly once. Do not invent its result."
					body := map[string]any{"model": model, "stream": stream}
					path := "/v1/responses"
					switch input {
					case APIProtocolResponses:
						body["input"] = prompt
						body["max_output_tokens"] = 1024
						body["reasoning"] = map[string]string{"effort": "none"}
						body["tools"] = []any{map[string]any{"type": "function", "name": "parity_lookup", "parameters": schema}}
						body["tool_choice"] = map[string]string{"type": "function", "name": "parity_lookup"}
					case APIProtocolChatCompletions:
						path = "/v1/chat/completions"
						body["messages"] = []any{map[string]string{"role": "user", "content": prompt}}
						body["max_tokens"] = 1024
						body["reasoning_effort"] = "none"
						body["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{"name": "parity_lookup", "parameters": schema}}}
						body["tool_choice"] = map[string]any{"type": "function", "function": map[string]string{"name": "parity_lookup"}}
					case APIProtocolAnthropic:
						path = "/v1/messages"
						body["messages"] = []any{map[string]string{"role": "user", "content": prompt}}
						body["max_tokens"] = 1024
						body["thinking"] = map[string]string{"type": "disabled"}
						body["tools"] = []any{map[string]any{"name": "parity_lookup", "input_schema": schema}}
						body["tool_choice"] = map[string]string{"type": "tool", "name": "parity_lookup"}
					}
					encoded, _ := json.Marshal(body)
					cfg := rawChatCompletionsTestConfig()
					cfg.Gateway.MaxLineSize = 16 << 20
					cfg.Gateway.UpstreamResponseReadMaxBytes = 16 << 20
					upstream := &deepSeekHostedLiveHTTP{client: client}
					svc := &OpenAIGatewayService{cfg: cfg, cache: &reasoningRecordingCache{}, httpUpstream: upstream}
					account := deepSeekNativeReplayAccount()
					targetBase := base
					if target == APIProtocolAnthropic {
						targetBase = anthBase
					}
					account.Credentials = map[string]any{"api_key": key, "base_url": targetBase, "api_protocol": target}
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
					c.Request.Header.Set("Content-Type", "application/json")
					c.Set("api_key", &APIKey{ID: 21})
					var err error
					switch input {
					case APIProtocolResponses:
						_, err = svc.Forward(t.Context(), c, account, encoded)
					case APIProtocolChatCompletions:
						_, err = svc.ForwardAsChatCompletions(t.Context(), c, account, encoded, "", "")
					case APIProtocolAnthropic:
						_, err = svc.ForwardAsAnthropic(t.Context(), c, account, encoded, "", "")
					}
					if err != nil {
						obs, _ := json.Marshal(upstream.protocolObservations())
						t.Fatalf("conversion failed status=%d protocol=%s; bodies withheld", rec.Code, obs)
					}
					if rec.Code != 200 {
						t.Fatalf("conversion status=%d", rec.Code)
					}
					var payloads []gjson.Result
					if stream {
						for _, frame := range deepSeekParityWireFrames(rec.Body.Bytes()) {
							if token, ok := frame.(string); ok {
								if token == "INVALID_JSON" {
									t.Fatal("invalid SSE JSON")
								}
								continue
							}
							b, _ := json.Marshal(frame)
							payloads = append(payloads, gjson.ParseBytes(b))
						}
					} else {
						if !gjson.ValidBytes(rec.Body.Bytes()) {
							t.Fatal("nonstream response is not JSON")
						}
						payloads = []gjson.Result{gjson.ParseBytes(rec.Body.Bytes())}
					}
					found := false
					for _, p := range payloads {
						raw := p.Raw
						if strings.Contains(raw, "parity_lookup") {
							found = true
						}
					}
					if !found {
						t.Fatal("converted response lost declared tool identity")
					}
					t.Logf("conversion status=200 stream=%t tool_identity_preserved=true", stream)
				})
			}
		}
	}
}
