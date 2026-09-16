//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGrokCustomToolIncrementalStreamFlushesBeforeTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const responseID = "resp_custom_incremental"
	const firstSequence = 40
	const prefix = "first line\nquoted \"input\" and backslash \\ "
	wantInput := prefix + strings.Repeat("x", 5717-len(prefix))
	argumentsJSON, err := json.Marshal(map[string]string{"input": wantInput})
	require.NoError(t, err)
	arguments := string(argumentsJSON)
	sequence := firstSequence
	frame := func(event map[string]any) string {
		event["sequence_number"] = sequence
		sequence++
		payload, marshalErr := json.Marshal(event)
		require.NoError(t, marshalErr)
		return fmt.Sprintf("event: %s\ndata: %s\n\n", event["type"], payload)
	}
	item := func(status, args string) map[string]any {
		return map[string]any{
			"type": "function_call", "id": "item_custom_incremental", "call_id": "call_custom_incremental",
			"name": "exec", "arguments": args, "status": status,
		}
	}
	first := frame(map[string]any{
		"type": "response.created", "response": map[string]any{"id": responseID, "model": "grok-4.6"},
	}) + frame(map[string]any{
		"type": "response.output_item.added", "output_index": 0, "item": item("in_progress", ""),
	})
	// The input string and its JSON wrapper remain open until arguments.done.
	for _, delta := range []string{arguments[:32], arguments[32:2200], arguments[2200 : len(arguments)-2]} {
		first += frame(map[string]any{
			"type": "response.function_call_arguments.delta", "output_index": 0,
			"item_id": "item_custom_incremental", "delta": delta,
		})
	}
	terminal := frame(map[string]any{
		"type": "response.function_call_arguments.done", "output_index": 0,
		"item_id": "item_custom_incremental", "call_id": "call_custom_incremental", "name": "exec", "arguments": arguments,
	}) + frame(map[string]any{
		"type": "response.output_item.done", "output_index": 0, "item": item("completed", arguments),
	}) + frame(map[string]any{
		"type": "response.completed", "response": map[string]any{
			"id": responseID, "object": "response", "model": "grok-4.6", "status": "completed",
			"output": []any{item("completed", arguments)},
			"usage": map[string]any{
				"input_tokens": 128, "output_tokens": 64, "total_tokens": 192,
				"input_tokens_details": map[string]any{"cached_tokens": 96},
			},
		},
	})
	resp, firstFlushed, releaseTerminal := delayedGrokSSEResponse(t, first, terminal)
	// Capture the original body before forwarding replaces resp.Body with wrappers.
	sourceBody := resp.Body
	requestBody := []byte(`{"model":"grok-4.6","stream":true,"input":"run the tool","tools":[{"type":"custom","name":"exec"}],"tool_choice":{"type":"custom","name":"exec"}}`)
	recorder := newOpenAIResponseFlushRecorder()
	c, _ := gin.CreateTestContext(recorder)
	ctx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(requestBody))).WithContext(ctx)
	upstream := &httpUpstreamRecorder{resp: resp}
	svc := &OpenAIGatewayService{
		cfg: &config.Config{}, httpUpstream: upstream, toolCorrector: NewCodexToolCorrector(),
	}
	account := grokProtocolAPIKeyAccount(7199)
	var result *OpenAIForwardResult
	var forwardErr error
	streamDone := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		assert.NoError(t, sourceBody.Close())
		releaseTerminal()
		select {
		case <-streamDone:
		case <-time.After(3 * time.Second):
			t.Error("Grok custom-tool forwarding goroutine did not stop after upstream close")
		}
	})
	go func() {
		defer close(streamDone)
		result, forwardErr = svc.forwardGrokResponses(ctx, c, account, requestBody, "grok-4.6", true, time.Now())
	}()
	select {
	case <-firstFlushed:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream did not flush the tool argument fragments")
	}

	var preTerminalFrames []grokProtocolSSEFrame
	preTerminalDeltas := 0
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for preTerminalDeltas < 2 {
		select {
		case <-recorder.flushEvents:
			_, flushes := recorder.snapshot()
			preTerminalFrames = parseGrokProtocolSSEFrames(t, flushes[len(flushes)-1])
			preTerminalDeltas = 0
			for _, event := range preTerminalFrames {
				if event.event == "response.custom_tool_call_input.delta" {
					require.NotEmpty(t, gjson.GetBytes(event.data, "delta").String())
					preTerminalDeltas++
				}
			}
		case <-streamDone:
			t.Fatalf("stream returned before terminal release: %v", forwardErr)
		case <-deadline.C:
			t.Fatalf("only %d custom tool input deltas were flushed while terminal was withheld; want at least two", preTerminalDeltas)
		}
	}
	require.GreaterOrEqual(t, preTerminalDeltas, 2, "at least two input deltas must be flushed before terminal release")
	for _, event := range preTerminalFrames {
		require.False(t, strings.HasSuffix(event.event, ".done"), "no done event may precede terminal release")
		require.NotEqual(t, "response.completed", event.event)
	}

	releaseTerminal()
	select {
	case <-streamDone:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not finish after terminal; upstream deliberately keeps EOF withheld")
	}
	require.NoError(t, forwardErr)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, responseID, result.ResponseID)
	require.Equal(t, 128, result.Usage.InputTokens)
	require.Equal(t, 64, result.Usage.OutputTokens)
	require.Equal(t, 96, result.Usage.CacheReadInputTokens)
	require.Equal(t, int64(1), gjson.GetBytes(upstream.lastBody, "tools.#").Int())
	require.Equal(t, "function", gjson.GetBytes(upstream.lastBody, "tools.0.type").String())

	body, _ := recorder.snapshot()
	frames := parseGrokProtocolSSEFrames(t, body)
	var input strings.Builder
	deltas, inputDone, itemDone, completed := 0, 0, 0, 0
	for index, event := range frames {
		require.Equal(t, event.event, gjson.GetBytes(event.data, "type").String())
		require.True(t, gjson.GetBytes(event.data, "sequence_number").Exists())
		require.Equal(t, firstSequence+index, int(gjson.GetBytes(event.data, "sequence_number").Int()))
		require.NotContains(t, event.event, "function_call_arguments", "proxy argument events must not leak")
		switch event.event {
		case "response.custom_tool_call_input.delta":
			require.Zero(t, inputDone, "input deltas must precede input.done")
			input.WriteString(gjson.GetBytes(event.data, "delta").String())
			deltas++
		case "response.custom_tool_call_input.done":
			inputDone++
			require.Equal(t, wantInput, gjson.GetBytes(event.data, "input").String())
		case "response.output_item.done":
			itemDone++
			require.Equal(t, "custom_tool_call", gjson.GetBytes(event.data, "item.type").String())
			require.Equal(t, wantInput, gjson.GetBytes(event.data, "item.input").String())
		case "response.completed":
			completed++
			require.Equal(t, "custom_tool_call", gjson.GetBytes(event.data, "response.output.0.type").String())
			require.Equal(t, wantInput, gjson.GetBytes(event.data, "response.output.0.input").String())
			require.Equal(t, int64(128), gjson.GetBytes(event.data, "response.usage.input_tokens").Int())
			require.Equal(t, int64(64), gjson.GetBytes(event.data, "response.usage.output_tokens").Int())
			require.Equal(t, int64(96), gjson.GetBytes(event.data, "response.usage.input_tokens_details.cached_tokens").Int())
		}
	}
	require.GreaterOrEqual(t, deltas, 2)
	require.Equal(t, wantInput, input.String(), "done and terminal snapshots must not replay input")
	require.Equal(t, 1, inputDone)
	require.Equal(t, 1, itemDone)
	require.Equal(t, 1, completed)
}
