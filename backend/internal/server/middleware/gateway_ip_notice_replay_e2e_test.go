package middleware

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ipnotice"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// This is a middleware HTTP end-to-end test, not a complete gateway/billing
// handler test. Both the client-to-gateway and gateway-to-fixture hops use real
// local TCP sockets. The fixture rejects changed historical request prefixes,
// and the client echoes the actual JSON/SSE response content without invoking
// the replay helpers directly.
func TestGatewayIPNoticeHTTPThreeTurnThinkingReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, streaming := range []bool{false, true} {
		for _, disableRule := range []bool{false, true} {
			name := fmt.Sprintf("stream=%t/disable_rule=%t", streaming, disableRule)
			t.Run(name, func(t *testing.T) {
				const notice = "Fixture operator notice."
				policy := ipnotice.Settings{Enabled: true, IPs: []string{"203.0.113.1"}, Message: notice, RPM: 100}
				settings := service.NewSettingService(&panelRateLimitStubRepo{}, nil)
				require.NoError(t, settings.SetGatewayIPNotice(context.Background(), policy))
				redisServer := miniredis.RunT(t)
				redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
				t.Cleanup(func() { require.NoError(t, redisClient.Close()) })

				traces := make(chan noticeReplayHTTPTrace, 3)
				var fixtureMu sync.Mutex
				var previousInput []gjson.Result
				var previousAssistant json.RawMessage
				var turn int
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						http.Error(w, "read fixture input", http.StatusBadRequest)
						return
					}
					fixtureMu.Lock()
					defer fixtureMu.Unlock()
					turn++
					trace := noticeReplayHTTPTrace{turn: turn, request: body}
					messages := gjson.GetBytes(body, "messages").Array()
					if turn > 1 {
						if len(messages) != len(previousInput)+2 {
							trace.err = fmt.Errorf("turn %d: got %d messages, want previous %d plus assistant and user", turn, len(messages), len(previousInput))
						} else {
							for i, prior := range previousInput {
								if !noticeReplayJSONEqual([]byte(prior.Raw), []byte(messages[i].Raw)) {
									trace.err = fmt.Errorf("turn %d: upstream historical message %d differs from its actual prior input", turn, i)
									break
								}
							}
							if trace.err == nil && !noticeReplayJSONEqual(previousAssistant, []byte(messages[len(previousInput)].Raw)) {
								trace.err = fmt.Errorf("turn %d: echoed thinking/tool/text content differs from the original upstream assistant", turn)
							}
						}
					}
					if trace.err != nil {
						traces <- trace
						http.Error(w, trace.err.Error(), http.StatusBadRequest)
						return
					}
					trace.reply = noticeReplayHTTPReply(turn, body)
					previousInput = messages
					previousAssistant = noticeReplayAssistant(trace.reply)
					traces <- trace
					if streaming {
						if err := noticeReplayWriteSSE(w, trace.reply); err != nil {
							return
						}
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = w.Write(trace.reply)
					}
				}))
				defer upstream.Close()

				router := gin.New()
				require.NoError(t, router.SetTrustedProxies([]string{"127.0.0.1", "::1"}))
				router.Use(GatewayIPNotice(settings, redisClient))
				router.POST("/v1/messages", func(c *gin.Context) {
					// Use the same shared reader as the real message handlers. Its
					// normalization must run even after the policy/IP stops matching.
					body, err := pkghttputil.ReadLenientJSONRequestBodyWithPrealloc(c.Request, 1<<20)
					if err != nil {
						c.String(http.StatusBadRequest, "read gateway input: %v", err)
						return
					}
					req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstream.URL+"/v1/messages", bytes.NewReader(body))
					if err != nil {
						c.String(http.StatusInternalServerError, "create fixture request: %v", err)
						return
					}
					req.Header.Set("Content-Type", "application/json")
					resp, err := upstream.Client().Do(req)
					if err != nil {
						c.String(http.StatusBadGateway, "call fixture: %v", err)
						return
					}
					defer func() { _ = resp.Body.Close() }()
					c.Header("Content-Type", resp.Header.Get("Content-Type"))
					c.Status(resp.StatusCode)
					// Deliberately fragment successful JSON and SSE. The production
					// middleware must assemble complete events before editing them.
					buf := make([]byte, 47)
					for {
						n, readErr := resp.Body.Read(buf)
						if n > 0 {
							if _, writeErr := c.Writer.Write(buf[:n]); writeErr != nil {
								return
							}
							if streaming {
								c.Writer.Flush()
							}
						}
						if readErr != nil {
							return
						}
					}
				})
				gateway := httptest.NewServer(router)
				defer gateway.Close()
				client := gateway.Client()
				client.Timeout = 10 * time.Second
				history := []json.RawMessage{json.RawMessage(`{"role":"user","content":"first question"}`)}
				for round := 1; round <= 3; round++ {
					clientIP := "203.0.113.1"
					if round == 3 {
						if disableRule {
							policy.Enabled = false
							require.NoError(t, settings.SetGatewayIPNotice(context.Background(), policy))
						} else {
							clientIP = "203.0.113.9"
						}
					}
					payload, err := json.Marshal(map[string]any{
						"model": "fixture-thinking", "max_tokens": 2048, "stream": streaming,
						"system":   "Fixture system prompt.",
						"thinking": map[string]any{"type": "enabled", "budget_tokens": 1024}, "messages": history,
						"tools": []any{map[string]any{"name": "fixture_tool", "description": "Inspect fixture data.", "input_schema": map[string]any{"type": "object"}}},
					})
					require.NoError(t, err)
					req, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/messages", bytes.NewReader(payload))
					require.NoError(t, err)
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("X-Forwarded-For", clientIP)
					resp, err := client.Do(req)
					require.NoError(t, err, "round %d", round)
					visible, readErr := noticeReplayReadReply(resp)
					require.NoError(t, resp.Body.Close())
					require.NoError(t, readErr, "round %d", round)
					require.Equal(t, http.StatusOK, resp.StatusCode, "round %d: %s", round, visible)
					var trace noticeReplayHTTPTrace
					select {
					case trace = <-traces:
					case <-time.After(3 * time.Second):
						t.Fatal("gateway did not reach the local upstream")
					}
					require.NoError(t, trace.err)
					require.Equal(t, round, trace.turn)
					require.Equal(t, "thinking", gjson.GetBytes(visible, "content.0.type").String())
					require.Equal(t, gjson.GetBytes(trace.reply, "content.0.signature").String(), gjson.GetBytes(visible, "content.0.signature").String())
					require.NotContains(t, string(trace.request), "\u2063")
					if round < 3 {
						var visibleText strings.Builder
						for _, block := range gjson.GetBytes(visible, "content").Array() {
							_, _ = visibleText.WriteString(block.Get("text").String())
						}
						require.Equal(t, 1, strings.Count(visibleText.String(), ipnotice.RenderNotice(notice)))
					} else {
						require.NotContains(t, string(visible), notice)
						require.JSONEq(t, string(trace.reply), string(visible))
						require.Equal(t, "third question", gjson.GetBytes(trace.request, "messages.4.content").String())
					}
					switch round {
					case 1:
						require.Equal(t, notice+"\n\nfirst question", gjson.GetBytes(trace.request, "messages.0.content").String())
						require.Equal(t, "redacted_thinking", gjson.GetBytes(visible, "content.1.type").String())
						require.Equal(t, gjson.GetBytes(trace.reply, "content.2.input").Raw, gjson.GetBytes(visible, "content.2.input").Raw)
					case 2:
						require.Equal(t, "unaltered tool output", gjson.GetBytes(trace.request, "messages.2.content.0.content").String())
						require.Equal(t, notice, gjson.GetBytes(trace.request, "messages.2.content.1.text").String())
					}
					history = append(history, noticeReplayAssistant(visible))
					switch round {
					case 1:
						history = append(history, json.RawMessage(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-fixture","content":"unaltered tool output"}]}`))
					case 2:
						history = append(history, json.RawMessage(`{"role":"user","content":"third question"}`))
					}
				}
			})
		}
	}
}

type noticeReplayHTTPTrace struct {
	turn    int
	request []byte
	reply   []byte
	err     error
}

func noticeReplayAssistant(reply []byte) json.RawMessage {
	body, _ := json.Marshal(struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}{Role: "assistant", Content: json.RawMessage(gjson.GetBytes(reply, "content").Raw)})
	return body
}

func noticeReplayJSONEqual(left, right []byte) bool {
	decode := func(raw []byte) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		err := decoder.Decode(&value)
		return value, err
	}
	a, errA := decode(left)
	b, errB := decode(right)
	return errA == nil && errB == nil && reflect.DeepEqual(a, b)
}

func noticeReplayHTTPReply(round int, input []byte) []byte {
	digest := sha256.Sum256(input)
	content := []any{map[string]any{"type": "thinking", "thinking": fmt.Sprintf("reasoning round %d", round), "signature": fmt.Sprintf("fixture-prefix-%x", digest)}}
	stopReason := "end_turn"
	if round == 1 {
		content = append(content, map[string]any{"type": "redacted_thinking", "data": "redacted-fixture"}, map[string]any{
			"type": "tool_use", "id": "call-fixture", "name": "fixture_tool",
			"input": map[string]any{"operation": "inspect", "amount": json.Number("9007199254740993"), "literal": "do not alter"},
		})
		stopReason = "tool_use"
	} else {
		content = append(content, map[string]any{"type": "text", "text": fmt.Sprintf("upstream answer %d", round)})
	}
	body, _ := json.Marshal(map[string]any{"id": fmt.Sprintf("msg_fixture_%d", round), "type": "message", "role": "assistant", "model": "fixture-thinking",
		"content": content, "stop_reason": stopReason, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 10, "output_tokens": 3}})
	return body
}

func noticeReplayWriteSSE(w http.ResponseWriter, reply []byte) error {
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(event any) error {
		encoded, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", gjson.GetBytes(encoded, "type").String(), encoded); err != nil {
			return err
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			return fmt.Errorf("SSE fixture writer does not support flushing")
		}
		flusher.Flush()
		return nil
	}
	message := map[string]any{"id": gjson.GetBytes(reply, "id").String(), "type": "message", "role": "assistant", "model": "fixture-thinking",
		"content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 10, "output_tokens": 0}}
	if err := emit(map[string]any{"type": "message_start", "message": message}); err != nil {
		return err
	}
	for i, block := range gjson.GetBytes(reply, "content").Array() {
		kind := block.Get("type").String()
		start := map[string]any{"type": kind}
		var deltas []any
		switch kind {
		case "thinking":
			start["thinking"] = ""
			deltas = append(deltas, map[string]any{"type": "thinking_delta", "thinking": block.Get("thinking").String()}, map[string]any{"type": "signature_delta", "signature": block.Get("signature").String()})
		case "redacted_thinking":
			start["data"] = block.Get("data").String()
		case "tool_use":
			start["id"], start["name"], start["input"] = block.Get("id").String(), block.Get("name").String(), map[string]any{}
			deltas = append(deltas, map[string]any{"type": "input_json_delta", "partial_json": block.Get("input").Raw})
		case "text":
			start["text"] = ""
			deltas = append(deltas, map[string]any{"type": "text_delta", "text": block.Get("text").String()})
		}
		if err := emit(map[string]any{"type": "content_block_start", "index": i, "content_block": start}); err != nil {
			return err
		}
		for _, delta := range deltas {
			if err := emit(map[string]any{"type": "content_block_delta", "index": i, "delta": delta}); err != nil {
				return err
			}
		}
		if err := emit(map[string]any{"type": "content_block_stop", "index": i}); err != nil {
			return err
		}
	}
	if err := emit(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": gjson.GetBytes(reply, "stop_reason").String(), "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 3}}); err != nil {
		return err
	}
	return emit(map[string]any{"type": "message_stop"})
}

// Reconstruct streamed content as an SDK does, preserving block order,
// signatures and JSON tool arguments before echoing them in the next request.
func noticeReplayReadReply(resp *http.Response) ([]byte, error) {
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return io.ReadAll(resp.Body)
	}
	var message map[string]any
	var blocks []map[string]any
	toolInput := map[int]string{}
	stopped := false
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		idx := int(gjson.GetBytes(data, "index").Int())
		switch gjson.GetBytes(data, "type").String() {
		case "message_start":
			if err := json.Unmarshal([]byte(gjson.GetBytes(data, "message").Raw), &message); err != nil {
				return nil, err
			}
		case "content_block_start":
			if idx != len(blocks) {
				return nil, fmt.Errorf("noncontiguous content block index %d, want %d", idx, len(blocks))
			}
			var block map[string]any
			if err := json.Unmarshal([]byte(gjson.GetBytes(data, "content_block").Raw), &block); err != nil {
				return nil, err
			}
			blocks = append(blocks, block)
		case "content_block_delta":
			if idx < 0 || idx >= len(blocks) {
				return nil, fmt.Errorf("delta refers to missing block %d", idx)
			}
			field := ""
			switch gjson.GetBytes(data, "delta.type").String() {
			case "text_delta":
				field = "text"
			case "thinking_delta":
				field = "thinking"
			case "signature_delta":
				field = "signature"
			case "input_json_delta":
				toolInput[idx] += gjson.GetBytes(data, "delta.partial_json").String()
			}
			if field != "" {
				previous, _ := blocks[idx][field].(string)
				blocks[idx][field] = previous + gjson.GetBytes(data, "delta."+field).String()
			}
		case "content_block_stop":
			if raw, ok := toolInput[idx]; ok {
				decoder := json.NewDecoder(strings.NewReader(raw))
				decoder.UseNumber()
				var input any
				if err := decoder.Decode(&input); err != nil {
					return nil, err
				}
				blocks[idx]["input"] = input
			}
		case "message_delta":
			if message == nil {
				return nil, fmt.Errorf("message delta arrived before message start")
			}
			message["stop_reason"] = gjson.GetBytes(data, "delta.stop_reason").String()
			message["stop_sequence"] = nil
			usage, ok := message["usage"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("SSE fixture message has no usage object")
			}
			usage["output_tokens"] = gjson.GetBytes(data, "usage.output_tokens").Int()
		case "message_stop":
			stopped = true
		case "error":
			return nil, fmt.Errorf("SSE upstream error: %s", data)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if message == nil || !stopped {
		return nil, fmt.Errorf("incomplete SSE message")
	}
	message["content"] = blocks
	return json.Marshal(message)
}
