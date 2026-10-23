package ipnotice

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const testNotice = "Operator notice: check credential ownership."

func TestGatewayIPNoticeValidation(t *testing.T) {
	settings, err := (Settings{Enabled: true, IPs: []string{"203.0.113.1", "::ffff:203.0.113.1", "2001:db8::1"}, Message: testNotice, RPM: 100}).Normalize()
	require.NoError(t, err)
	require.Equal(t, []string{"203.0.113.1", "2001:db8::1"}, settings.IPs)
	_, match := settings.Match("::ffff:203.0.113.1")
	require.True(t, match)
	_, match = settings.Match("203.0.113.2")
	require.False(t, match)
	for _, value := range []string{"10.0.0.1", "127.0.0.1", "0.0.0.0/0", "203.0.113.1:443", "fe80::1%eth0", "not-an-ip"} {
		t.Run(value, func(t *testing.T) {
			_, err := (Settings{IPs: []string{value}, RPM: 100}).Normalize()
			require.Error(t, err)
		})
	}
}

func TestGatewayIPNoticeCurrentInputPreservesToolsAndHistory(t *testing.T) {
	for _, protocol := range []Protocol{Chat, Anthropic, Responses, Gemini} {
		t.Run(string(protocol), func(t *testing.T) {
			field, parts := "messages", "content"
			if protocol == Responses {
				field = "input"
			}
			if protocol == Gemini {
				field, parts = "contents", "parts"
			}
			tool := map[string]any{"type": "tool_result", "tool_use_id": "call_1", "content": `{"command":"unchanged"}`}
			if protocol == Gemini {
				tool = map[string]any{"functionResponse": map[string]any{"name": "exec", "response": map[string]any{"result": "unchanged"}}}
			}
			body, err := json.Marshal(map[string]any{field: []any{
				map[string]any{"role": "user", parts: "historical input"},
				map[string]any{"role": "assistant", parts: "historical reply"},
				map[string]any{"role": "user", parts: []any{tool}},
			}, "model": "fixture", "sequence": json.Number("9007199254740993")})
			require.NoError(t, err)
			beforeTool := gjson.GetBytes(body, field+".2."+parts+".0").Raw
			out, err := InjectRequest(body, protocol, testNotice)
			require.NoError(t, err)
			require.Equal(t, beforeTool, gjson.GetBytes(out, field+".2."+parts+".0").Raw)
			require.Equal(t, "historical input", gjson.GetBytes(out, field+".0."+parts).String())
			require.Equal(t, testNotice, gjson.GetBytes(out, field+".2."+parts+".1.text").String())
			require.Equal(t, "9007199254740993", gjson.GetBytes(out, "sequence").Raw)
		})
	}
	input := []byte(`{"input":[{"type":"function_call_output","call_id":"x","output":"result"}]}`)
	out, err := InjectRequest(input, Responses, testNotice)
	require.NoError(t, err)
	require.Equal(t, gjson.GetBytes(input, "input.0").Raw, gjson.GetBytes(out, "input.0").Raw)
	require.Equal(t, testNotice, gjson.GetBytes(out, "input.1.content.0.text").String())
	replayed := []byte(`{"input":[{"type":"item_reference","id":"msg_gateway_notice_resp1"},{"type":"function_call_output","call_id":"x","output":"result"}],"previous_response_id":"resp1"}`)
	out, err = InjectRequest(replayed, Responses, testNotice)
	require.NoError(t, err)
	require.Equal(t, "function_call_output", gjson.GetBytes(out, "input.0.type").String())
	require.Equal(t, "resp1", gjson.GetBytes(out, "previous_response_id").String())
}

func TestGatewayIPNoticeJSONOutputs(t *testing.T) {
	for _, tt := range []struct {
		protocol   Protocol
		body, path string
	}{
		{Chat, `{"choices":[{"message":{"role":"assistant","content":"answer","tool_calls":[{"id":"call","function":{"arguments":"{}"}}]}}],"usage":{"total_tokens":10}}`, "choices.0.message.content"},
		{Anthropic, `{"type":"message","content":[{"type":"tool_use","id":"call","input":{"x":1}}],"usage":{"output_tokens":7}}`, "content.0.text"},
		{Responses, `{"id":"resp1","output":[{"type":"function_call","call_id":"call","arguments":"{}"}],"usage":{"output_tokens":7}}`, "output.0.content.0.text"},
		{Gemini, `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"exec","args":{"x":1}}}]}}],"usageMetadata":{"totalTokenCount":7}}`, "candidates.0.content.parts.0.text"},
	} {
		t.Run(string(tt.protocol), func(t *testing.T) {
			out := TransformJSON([]byte(tt.body), tt.protocol, testNotice)
			require.Contains(t, gjson.GetBytes(out, tt.path).String(), testNotice)
			require.Equal(t, gjson.Get(tt.body, "usage").Raw, gjson.GetBytes(out, "usage").Raw)
		})
	}
	for _, protocol := range []Protocol{Chat, Anthropic, Responses, Gemini} {
		body := []byte(`{"error":{"message":"upstream refused","status_code":403}}`)
		require.Equal(t, body, TransformJSON(body, protocol, testNotice))
	}
}

func TestGatewayIPNoticeResponsesEventsAndSnapshotsAgree(t *testing.T) {
	transform := NewEventTransformer(Responses, testNotice)
	created := transform.Transform([]byte(`{"type":"response.created","sequence_number":0,"response":{"id":"resp1","output":[]}}`))
	require.Len(t, created, 7)
	require.Equal(t, RenderNotice(testNotice), gjson.GetBytes(created[3], "delta").String())
	for i, frame := range created {
		require.Equal(t, int64(i), gjson.GetBytes(frame, "sequence_number").Int())
	}
	tool := []byte(`{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"type":"function_call","arguments":"{\"x\":1}"}}`)
	modified := transform.Transform(tool)[0]
	require.Equal(t, int64(1), gjson.GetBytes(modified, "output_index").Int())
	require.Equal(t, int64(7), gjson.GetBytes(modified, "sequence_number").Int())
	require.Equal(t, gjson.GetBytes(tool, "item").Raw, gjson.GetBytes(modified, "item").Raw)
	final := transform.Transform([]byte(`{"type":"response.completed","sequence_number":2,"response":{"output":[{"type":"function_call","call_id":"x"}],"usage":{"output_tokens":5}}}`))[0]
	require.Equal(t, RenderNotice(testNotice), gjson.GetBytes(final, "response.output.0.content.0.text").String())
	require.Equal(t, "x", gjson.GetBytes(final, "response.output.1.call_id").String())
	require.Equal(t, int64(5), gjson.GetBytes(final, "response.usage.output_tokens").Int())
	errFrame := []byte(`{"type":"error","error":{"status_code":403,"message":"refused"}}`)
	require.Equal(t, errFrame, transform.Transform(errFrame)[0])
	second := transform.Transform([]byte(`{"type":"response.created","sequence_number":0,"response":{"id":"resp2"}}`))
	require.Contains(t, string(second[1]), "msg_gateway_notice_resp2")
}

func TestGatewayIPNoticeAnthropicThinkingToolIndices(t *testing.T) {
	transform := NewEventTransformer(Anthropic, testNotice)
	require.Len(t, transform.Transform([]byte(`{"type":"message_start","message":{"id":"m"}}`)), 1)
	start := []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`)
	require.Equal(t, [][]byte{start}, transform.Transform(start))
	body := []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"original"}}`)
	out := transform.Transform(body)[0]
	require.Equal(t, int64(0), gjson.GetBytes(out, "index").Int())
	require.Equal(t, gjson.GetBytes(body, "delta").Raw, gjson.GetBytes(out, "delta").Raw)
	tool := []byte(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call","input":{"x":1}}}`)
	frames := transform.Transform(tool)
	require.Len(t, frames, 4)
	require.Equal(t, int64(1), gjson.GetBytes(frames[0], "index").Int())
	require.Equal(t, RenderNotice(testNotice), gjson.GetBytes(frames[1], "delta.text").String())
	require.Equal(t, int64(2), gjson.GetBytes(frames[3], "index").Int())
	require.Equal(t, gjson.GetBytes(tool, "content_block").Raw, gjson.GetBytes(frames[3], "content_block").Raw)
}
