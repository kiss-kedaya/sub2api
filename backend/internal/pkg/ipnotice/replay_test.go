package ipnotice

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func replayWithItems(t *testing.T, body []byte, field string, additional ...[]byte) []byte {
	t.Helper()
	var items []json.RawMessage
	for _, item := range gjson.GetBytes(body, field).Array() {
		items = append(items, json.RawMessage(item.Raw))
	}
	for _, item := range additional {
		items = append(items, json.RawMessage(item))
	}
	return set(body, field, items)
}

func TestNoticeReplayRestoresAnthropicThinkingPrefixAcrossThreeTurns(t *testing.T) {
	first := []byte(`{"model":"fixture","messages":[{"role":"user","content":"first question"}]}`)
	firstSent, err := InjectRequest(first, Anthropic, testNotice)
	require.NoError(t, err)
	answer := []byte(`{"type":"message","role":"assistant","content":[{"type":"thinking","thinking":"reasoning","signature":"signed-prefix-1"},{"type":"tool_use","id":"call-1","name":"exec","input":{"command":"unaltered"}}]}`)
	visible := TransformJSON(answer, Anthropic, testNotice)
	require.Equal(t, "thinking", gjson.GetBytes(visible, "content.0.type").String())
	require.Equal(t, WrapOutputText("", testNotice), gjson.GetBytes(visible, "content.2.text").String())
	toolResult := []byte(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","content":"tool output"}]}`)
	second := replayWithItems(t, first, "messages", visible, toolResult)
	secondSent, err := InjectRequest(second, Anthropic, testNotice)
	require.NoError(t, err)
	require.Equal(t, gjson.GetBytes(firstSent, "messages.0").Raw, gjson.GetBytes(secondSent, "messages.0").Raw)
	require.JSONEq(t, string(answer), gjson.GetBytes(secondSent, "messages.1").Raw)
	require.Equal(t, gjson.GetBytes(toolResult, "content.0").Raw, gjson.GetBytes(secondSent, "messages.2.content.0").Raw)
	require.Equal(t, testNotice, gjson.GetBytes(secondSent, "messages.2.content.1.text").String())
	secondAnswer := []byte(`{"type":"message","role":"assistant","content":[{"type":"thinking","thinking":"more reasoning","signature":"signed-prefix-2"},{"type":"text","text":"answer"}]}`)
	secondVisible := TransformJSON(secondAnswer, Anthropic, testNotice)
	third := replayWithItems(t, second, "messages", secondVisible, []byte(`{"role":"user","content":"third question"}`))
	// Disabling the rule or changing IP must still restore signed old prefixes.
	normalized := NormalizeReplay(third, Anthropic)
	for i, item := range gjson.GetBytes(secondSent, "messages").Array() {
		require.JSONEq(t, item.Raw, gjson.GetBytes(normalized, "messages."+index(i)).Raw)
	}
	require.JSONEq(t, string(secondAnswer), gjson.GetBytes(normalized, "messages.3").Raw)
	require.Equal(t, "third question", gjson.GetBytes(normalized, "messages.4.content").String())
	require.Equal(t, normalized, NormalizeReplay(normalized, Anthropic))
	// A changed policy only applies its new text to the current turn.
	thirdSent, err := InjectRequest(third, Anthropic, "new notice")
	require.NoError(t, err)
	require.Equal(t, gjson.GetBytes(normalized, "messages.0").Raw, gjson.GetBytes(thirdSent, "messages.0").Raw)
	require.Equal(t, "new notice\n\nthird question", gjson.GetBytes(thirdSent, "messages.4.content").String())
}

func TestNoticeReplayRestoresResponsesToolOutputBoundary(t *testing.T) {
	first := []byte(`{"model":"fixture","previous_response_id":"resp0","input":[{"type":"function_call_output","call_id":"call-1","output":"unaltered result"}]}`)
	sent, err := InjectRequest(first, Responses, testNotice)
	require.NoError(t, err)
	answer := []byte(`{"id":"resp1","output":[{"type":"function_call","call_id":"call-2","name":"exec","arguments":"{\"x\":1}"}],"usage":{"output_tokens":3}}`)
	visible := TransformJSON(answer, Responses, testNotice)
	replayed := first
	for _, item := range gjson.GetBytes(visible, "output").Array() {
		replayed = replayWithItems(t, replayed, "input", []byte(item.Raw))
	}
	replayed = replayWithItems(t, replayed, "input", []byte(`{"type":"function_call_output","call_id":"call-2","output":"second result"}`))
	clean := NormalizeReplay(replayed, Responses)
	require.Equal(t, "resp0", gjson.GetBytes(clean, "previous_response_id").String())
	for i, item := range gjson.GetBytes(sent, "input").Array() {
		require.JSONEq(t, item.Raw, gjson.GetBytes(clean, "input."+index(i)).Raw)
	}
	require.JSONEq(t, gjson.GetBytes(answer, "output.0").Raw, gjson.GetBytes(clean, "input.2").Raw)
	require.Equal(t, "call-2", gjson.GetBytes(clean, "input.3.call_id").String())
	require.NotContains(t, string(clean), "msg_gateway_notice_")
	next, err := InjectRequest(replayed, Responses, "changed notice")
	require.NoError(t, err)
	require.Len(t, gjson.GetBytes(next, "input").Array(), 5)
	require.Equal(t, "changed notice", gjson.GetBytes(next, "input.4.content.0.text").String())
}

func TestNoticeReplayChatAndGeminiPreserveOriginalHistory(t *testing.T) {
	for _, protocol := range []Protocol{Chat, Gemini} {
		t.Run(string(protocol), func(t *testing.T) {
			field, parts, role := "messages", "content", "assistant"
			if protocol == Gemini {
				field, parts, role = "contents", "parts", "model"
			}
			input, err := json.Marshal(map[string]any{field: []any{map[string]any{"role": "user", parts: []any{map[string]any{"type": "text", "text": "first question"}}}}})
			require.NoError(t, err)
			sent, err := InjectRequest(input, protocol, testNotice)
			require.NoError(t, err)
			answer, err := json.Marshal(map[string]any{"role": role, parts: []any{map[string]any{"type": "text", "text": "answer"}}})
			require.NoError(t, err)
			visible := prependArray(answer, parts, map[string]any{"type": "text", "text": RenderNotice(testNotice)})
			replayed := replayWithItems(t, input, field, visible)
			clean := NormalizeReplay(replayed, protocol)
			require.JSONEq(t, gjson.GetBytes(sent, field+".0").Raw, gjson.GetBytes(clean, field+".0").Raw)
			require.JSONEq(t, string(answer), gjson.GetBytes(clean, field+".1").Raw)
		})
	}
}

func TestNoticeReplayPreservesUserToolThinkingAndMalformedMarkers(t *testing.T) {
	malformed := noticeFence + "incomplete"
	userNotice := RenderNotice("user supplied literal")
	input, err := json.Marshal(map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": userNotice},
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "thinking", "thinking": userNotice, "signature": userNotice},
			map[string]any{"type": "tool_use", "id": "call", "input": map[string]any{"literal": userNotice}},
			map[string]any{"type": "text", "text": malformed},
		}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "call", "content": userNotice}}},
	}})
	require.NoError(t, err)
	require.JSONEq(t, string(input), string(NormalizeReplay(input, Anthropic)))
	_, err = (Settings{IPs: []string{"203.0.113.1"}, Message: "invalid" + noticeFence, RPM: 100}).Normalize()
	require.Error(t, err)
}

func TestNoticeReplayControlPathsRemoveReferencesWithoutPolicy(t *testing.T) {
	for _, path := range []string{"/responses", "/v1/responses", "/v1/responses/compact", "/responses/input_tokens"} {
		protocol := ReplayProtocolForPath(path)
		require.Equal(t, Responses, protocol)
		body := []byte(`{"input":[{"type":"item_reference","id":"msg_gateway_notice_resp1"},{"type":"item_reference","id":"real-message"}],"previous_response_id":"resp1"}`)
		clean := NormalizeReplay(body, protocol)
		require.Len(t, gjson.GetBytes(clean, "input").Array(), 1)
		require.Equal(t, "real-message", gjson.GetBytes(clean, "input.0.id").String())
		require.Equal(t, "resp1", gjson.GetBytes(clean, "previous_response_id").String())
	}
	require.Equal(t, Anthropic, ReplayProtocolForPath("/v1/messages/count_tokens"))
}

func TestNoticeReplayPreservesGeminiSignedThoughtParts(t *testing.T) {
	input, err := json.Marshal(map[string]any{"contents": []any{
		map[string]any{"role": "user", "parts": []any{map[string]any{"text": "original"}}},
		map[string]any{"role": "model", "parts": []any{
			map[string]any{"thought": true, "text": RenderNotice(testNotice), "thoughtSignature": "signature-1"},
			map[string]any{"text": RenderNotice(testNotice), "thoughtSignature": "signature-2"},
			map[string]any{"text": RenderNotice(testNotice)},
			map[string]any{"text": "answer"},
		}},
	}})
	require.NoError(t, err)
	clean := NormalizeReplay(input, Gemini)
	require.Len(t, gjson.GetBytes(clean, "contents.1.parts").Array(), 3)
	for i := range 2 {
		path := "contents.1.parts." + index(i)
		require.Equal(t, gjson.GetBytes(input, path).Raw, gjson.GetBytes(clean, path).Raw)
	}
}

func TestNoticeAnthropicThinkingOnlySuccessAndError(t *testing.T) {
	answer := []byte(`{"type":"message","role":"assistant","content":[{"type":"thinking","thinking":"reasoning","signature":"original"}]}`)
	visible := TransformJSON(answer, Anthropic, testNotice)
	require.Equal(t, gjson.GetBytes(answer, "content.0").Raw, gjson.GetBytes(visible, "content.0").Raw)
	require.Equal(t, WrapOutputText("", testNotice), gjson.GetBytes(visible, "content.1.text").String())
	for _, fail := range []bool{false, true} {
		transform := NewEventTransformer(Anthropic, testNotice)
		transform.Transform([]byte(`{"type":"message_start","message":{"id":"m"}}`))
		transform.Transform([]byte(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`))
		transform.Transform([]byte(`{"type":"content_block_stop","index":0}`))
		if fail {
			errFrame := []byte(`{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`)
			require.Equal(t, [][]byte{errFrame}, transform.Transform(errFrame))
			continue
		}
		frames := transform.Transform([]byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`))
		require.Len(t, frames, 4)
		require.Equal(t, int64(1), gjson.GetBytes(frames[0], "index").Int())
		require.Equal(t, WrapOutputText("", testNotice), gjson.GetBytes(frames[1], "delta.text").String())
		require.Len(t, transform.Transform([]byte(`{"type":"message_stop"}`)), 1)
	}
}
