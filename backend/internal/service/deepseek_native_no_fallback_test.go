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

func deepSeekParityOKResponse(stream bool) *http.Response {
	response := hostedSearchResponse("resp_parity_ok", 8, 2,
		map[string]any{"type": "message", "id": "msg_parity", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": "ok"}}})
	if stream {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(hostedSearchSSE(response))),
		}
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(response)),
	}
}

func deepSeekParityReasoningSSE() string {
	events := []struct {
		typ  string
		data string
	}{
		{"response.created", `{"type":"response.created","response":{"id":"resp_parity_stream","object":"response","status":"in_progress","model":"deepseek-v4.1-flash","output":[]}}`},
		{"response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_live","summary":[]}}`},
		{"response.reasoning_text.delta", `{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"item_id":"rs_live","delta":"think"}`},
		{"response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_live","summary":[],"content":[{"type":"reasoning_text","text":"think"}]}}`},
		{"response.output_text.delta", `{"type":"response.output_text.delta","output_index":1,"content_index":0,"item_id":"msg_live","delta":"answer"}`},
		{"response.output_item.done", `{"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"msg_live","role":"assistant","status":"completed","content":[{"type":"output_text","text":"answer"}]}}`},
		{"response.completed", `{"type":"response.completed","response":{"id":"resp_parity_stream","object":"response","status":"completed","model":"deepseek-v4.1-flash","output":[{"type":"reasoning","id":"rs_live","summary":[],"content":[{"type":"reasoning_text","text":"think"}]},{"type":"message","id":"msg_live","role":"assistant","status":"completed","content":[{"type":"output_text","text":"answer"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}`},
	}
	var out strings.Builder
	for _, event := range events {
		fmt.Fprintf(&out, "event: %s\ndata: %s\n\n", event.typ, event.data)
	}
	return out.String()
}

// A cache miss must not delete the reasoning item or rewrite the assistant
// message that followed it. This is the transport-level contract: the upstream
// sees exactly the items the client sent; a real upstream rejection is surfaced
// instead of being disguised by a role rewrite.
func TestDeepSeekNativeForwardPreservesMissingReasoningHistory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		for _, variant := range []struct {
			name string
			text string
		}{
			{"plain", "answer without reasoning"},
			{"previous-context", "[Previous web search]\nStatus: completed"},
		} {
			name := fmt.Sprintf("%s/%s", map[bool]string{false: "json", true: "stream"}[stream], variant.name)
			t.Run(name, func(t *testing.T) {
				requestBody, err := json.Marshal(map[string]any{
					"model":  "deepseek-v4.1-flash",
					"stream": stream,
					"input": []any{
						map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "first"}}},
						map[string]any{"type": "reasoning", "id": "rs_missing", "summary": []any{}, "encrypted_content": "opaque-other"},
						map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": variant.text}}},
						map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "next"}}},
					},
				})
				require.NoError(t, err)

				upstream := &httpUpstreamRecorder{resp: deepSeekParityOKResponse(stream)}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), cache: &reasoningRecordingCache{}, httpUpstream: upstream}
				c, rec := deepSeekNativeReplayContext(21, "parity-missing-"+variant.name)
				c.Request.Body = io.NopCloser(bytes.NewReader(requestBody))

				_, err = svc.Forward(context.Background(), c, deepSeekNativeReplayAccount(), requestBody)
				require.NoError(t, err, rec.Body.String())
				require.Equal(t, http.StatusOK, rec.Code)

				forwarded := upstream.lastBody
				require.Len(t, gjson.GetBytes(forwarded, "input").Array(), 4, "no history item may be deleted")
				reasoning := gjson.GetBytes(forwarded, `input.#(type=="reasoning")`)
				require.True(t, reasoning.Exists(), "reasoning item must survive a cache miss")
				require.Equal(t, "rs_missing", reasoning.Get("id").String())
				require.Equal(t, "opaque-other", reasoning.Get("encrypted_content").String())
				require.False(t, reasoning.Get("content").Exists(), "missing reasoning must not be fabricated")
				require.Equal(t, "assistant", gjson.GetBytes(forwarded, "input.2.role").String(), "assistant role must not be rewritten")
				require.Equal(t, "output_text", gjson.GetBytes(forwarded, "input.2.content.0.type").String(), "content type must not be rewritten")
				require.Equal(t, variant.text, gjson.GetBytes(forwarded, "input.2.content.0.text").String())
			})
		}
	}
}

// DeepSeek must not receive a synthetic function_call_output that the client
// never produced. The same helper is still allowed for OpenAI accounts.
func TestDeepSeekNativeForwardDoesNotFabricateMissingToolOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			requestBody, err := json.Marshal(map[string]any{
				"model":  "deepseek-v4.1-flash",
				"stream": stream,
				"input": []any{
					map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "run"}}},
					map[string]any{"type": "function_call", "call_id": "call_missing", "name": "lookup", "arguments": "{}"},
					map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "continue"}}},
				},
			})
			require.NoError(t, err)

			upstream := &httpUpstreamRecorder{resp: deepSeekParityOKResponse(stream)}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), cache: &reasoningRecordingCache{}, httpUpstream: upstream}
			c, rec := deepSeekNativeReplayContext(21, "parity-missing-output")
			c.Request.Body = io.NopCloser(bytes.NewReader(requestBody))

			_, err = svc.Forward(context.Background(), c, deepSeekNativeReplayAccount(), requestBody)
			require.NoError(t, err, rec.Body.String())
			require.Equal(t, http.StatusOK, rec.Code)

			forwarded := upstream.lastBody
			require.Len(t, gjson.GetBytes(forwarded, "input").Array(), 3, "no synthetic output item may be inserted")
			require.False(t, gjson.GetBytes(forwarded, `input.#(type=="function_call_output")`).Exists())
			require.NotContains(t, string(forwarded), "tool_output_missing")
		})
	}
}

// A real upstream 400 must keep its status, type, code, param and message. It
// must not be converted into a 502, retried, or hidden by mutating history.
func TestDeepSeekNativeForwardPreservesUpstreamReasoningText400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestBody, err := json.Marshal(map[string]any{
		"model":  "deepseek-v4.1-flash",
		"stream": false,
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "first"}}},
			map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "answer without reasoning"}}},
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "next"}}},
		},
	})
	require.NoError(t, err)

	upstreamBody := `{"error":{"type":"invalid_request_error","code":"invalid_request_error","param":"input","message":"The reasoning_text in the thinking mode must be passed back to the API. (request_id: parity-rid)"}}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"parity-rid"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), cache: &reasoningRecordingCache{}, httpUpstream: upstream}
	c, rec := deepSeekNativeReplayContext(21, "parity-400")
	c.Request.Body = io.NopCloser(bytes.NewReader(requestBody))

	result, err := svc.Forward(context.Background(), c, deepSeekNativeReplayAccount(), requestBody)
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Len(t, upstream.requests, 1, "a deterministic 400 must not retry or fail over")

	payload := gjson.ParseBytes(rec.Body.Bytes())
	require.Equal(t, "invalid_request_error", payload.Get("error.type").String())
	require.Equal(t, "invalid_request_error", payload.Get("error.code").String())
	require.Equal(t, "input", payload.Get("error.param").String())
	require.Contains(t, payload.Get("error.message").String(), "reasoning_text")
	require.Contains(t, payload.Get("error.message").String(), "must be passed back")

	// The forwarded request still carries the client's assistant turn; the
	// gateway did not rewrite it to a user message to dodge the 400.
	require.Equal(t, "assistant", gjson.GetBytes(upstream.lastBody, "input.1.role").String())
	require.Equal(t, "output_text", gjson.GetBytes(upstream.lastBody, "input.1.content.0.type").String())
}

// Streaming reasoning_text events must reach the client unchanged.
func TestDeepSeekNativeForwardStreamsReasoningTextEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestBody := []byte(`{"model":"deepseek-v4.1-flash","stream":true,"input":"hi"}`)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(deepSeekParityReasoningSSE())),
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), cache: &reasoningRecordingCache{}, httpUpstream: upstream}
	c, rec := deepSeekNativeReplayContext(21, "parity-stream-reasoning")
	c.Request.Body = io.NopCloser(bytes.NewReader(requestBody))

	_, err := svc.Forward(context.Background(), c, deepSeekNativeReplayAccount(), requestBody)
	require.NoError(t, err, rec.Body.String())
	require.Equal(t, http.StatusOK, rec.Code)

	downstream := rec.Body.String()
	require.Contains(t, downstream, "response.reasoning_text.delta")
	require.Contains(t, downstream, `"delta":"think"`)
	require.Contains(t, downstream, "response.completed")
}

// Non-streaming Responses output must keep the reasoning item and its
// reasoning_text content; it must not be collapsed into a summary.
func TestDeepSeekNativeForwardNonStreamPreservesReasoningItem(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestBody := []byte(`{"model":"deepseek-v4.1-flash","stream":false,"input":"hi"}`)
	upstreamBody := hostedSearchResponse("resp_parity_json", 5, 3,
		map[string]any{"type": "reasoning", "id": "rs_out", "summary": []any{},
			"content": []any{map[string]any{"type": "reasoning_text", "text": "visible cot"}}},
		map[string]any{"type": "message", "id": "msg_out", "role": "assistant", "status": "completed",
			"content": []any{map[string]any{"type": "output_text", "text": "ok"}}})
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(upstreamBody)),
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), cache: &reasoningRecordingCache{}, httpUpstream: upstream}
	c, rec := deepSeekNativeReplayContext(21, "parity-json-reasoning")
	c.Request.Body = io.NopCloser(bytes.NewReader(requestBody))

	_, err := svc.Forward(context.Background(), c, deepSeekNativeReplayAccount(), requestBody)
	require.NoError(t, err, rec.Body.String())
	require.Equal(t, http.StatusOK, rec.Code)

	reasoning := gjson.GetBytes(rec.Body.Bytes(), `output.#(type=="reasoning")`)
	require.True(t, reasoning.Exists())
	require.Equal(t, "reasoning_text", reasoning.Get("content.0.type").String())
	require.Equal(t, "visible cot", reasoning.Get("content.0.text").String())
}
