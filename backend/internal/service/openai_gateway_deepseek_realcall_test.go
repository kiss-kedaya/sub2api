package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestDeepSeekRealCall_DuplicateCallID(t *testing.T) {
	callID := "call_00_gEGoDdh7olwRXAaeGk7k6101"
	body := []byte(`{"model":"deepseek-v4.1-flash","input":[` +
		`{"type":"function_call","id":"` + callID + `","call_id":"` + callID + `","name":"shell","arguments":"{}"},` +
		`{"type":"function_call","id":"` + callID + `","call_id":"` + callID + `","name":"shell","arguments":"{\"cmd\":\"pwd\"}"},` +
		`{"type":"function_call_output","call_id":"` + callID + `","output":"/tmp"},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}]}`)

	inbound := RewriteInboundDeepSeekCallIDs(PlatformDeepseek, "deepseek-v4.1-flash", body)
	outbound := normalizeDeepSeekResponsesRequestBody(&Account{Platform: PlatformDeepseek}, body)

	t.Logf("INBOUND_COUNT=%d", len(gjson.GetBytes(inbound, "input").Array()))
	t.Logf("INBOUND_ID=%s", gjson.GetBytes(inbound, "input.0.id").String())
	t.Logf("INBOUND_CALL=%s", gjson.GetBytes(inbound, "input.0.call_id").String())
	t.Logf("INBOUND_ARGS=%s", gjson.GetBytes(inbound, "input.0.arguments").String())
	t.Logf("INBOUND_OUTPUT=%s", gjson.GetBytes(inbound, "input.1.output").String())
	t.Logf("INBOUND_JSON=%s", string(inbound))
	t.Logf("OUTBOUND_JSON=%s", string(outbound))

	require.Equal(t, 3, len(gjson.GetBytes(inbound, "input").Array()))
	require.Equal(t, "fc_00_gEGoDdh7olwRXAaeGk7k6101", gjson.GetBytes(inbound, "input.0.id").String())
	require.Equal(t, callID, gjson.GetBytes(inbound, "input.0.call_id").String())
	require.Contains(t, gjson.GetBytes(inbound, "input.0.arguments").String(), "pwd")
	require.Equal(t, "/tmp", gjson.GetBytes(inbound, "input.1.output").String())
	require.Equal(t, "next", gjson.GetBytes(inbound, "input.2.content.0.text").String())
	require.Equal(t, string(inbound), string(outbound))

	var st deepSeekPassthrough
	added := []byte(`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"` + callID + `","call_id":"` + callID + `","name":"shell","arguments":""}}`)
	_, _, next, drop := st.rewrite(nil, "response.output_item.added", added)
	require.False(t, drop)
	require.Nil(t, next)

	delta := []byte(`{"type":"response.function_call_arguments.delta","output_index":1,"item_id":"` + callID + `","delta":"{\"cmd\":\"pwd\"}"}`)
	_, _, next, drop = st.rewrite(nil, "response.function_call_arguments.delta", delta)
	require.False(t, drop)
	require.Nil(t, next)
	require.Equal(t, callID, gjson.GetBytes(delta, "item_id").String())

	completed := []byte(`{"type":"response.completed","response":{"output":[` +
		`{"type":"function_call","id":"` + callID + `","call_id":"` + callID + `","name":"shell","arguments":"{}"},` +
		`{"type":"function_call","id":"` + callID + `","call_id":"` + callID + `","name":"shell","arguments":"{\"cmd\":\"pwd\"}"}` +
		`]}}`)
	_, _, next, drop = st.rewrite(nil, "response.completed", completed)
	require.False(t, drop)
	require.NotNil(t, next)
	t.Logf("COMPLETED_JSON=%s", string(next))
	require.Equal(t, 1, len(gjson.GetBytes(next, "response.output").Array()))
	require.Equal(t, callID, gjson.GetBytes(next, "response.output.0.id").String())
	require.Equal(t, callID, gjson.GetBytes(next, "response.output.0.call_id").String())
	require.Contains(t, gjson.GetBytes(next, "response.output.0.arguments").String(), "pwd")
}
