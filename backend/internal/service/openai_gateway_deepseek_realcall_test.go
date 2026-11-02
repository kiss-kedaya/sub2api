package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestDeepSeekRealCall_PreservesNativeRequestAndLegacyResponseBehavior(t *testing.T) {
	callID := "call_00_gEGoDdh7olwRXAaeGk7k6101"
	body := []byte(`{"model":"deepseek-v4.1-flash","input":[` +
		`{"type":"function_call","id":"` + callID + `","call_id":"` + callID + `","name":"shell","arguments":"{}"},` +
		`{"type":"function_call","id":"` + callID + `","call_id":"` + callID + `","name":"shell","arguments":"{\"cmd\":\"pwd\"}"},` +
		`{"type":"function_call_output","call_id":"` + callID + `","output":"/tmp"},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}]}`)

	for _, protocol := range []string{APIProtocolResponses, APIProtocolAdaptive} {
		t.Run(protocol, func(t *testing.T) {
			account := &Account{
				Platform: PlatformDeepseek, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_protocol": protocol},
			}
			inbound := RewriteInboundDeepSeekCallIDs(PlatformDeepseek, "deepseek-v4.1-flash", body)
			outbound := normalizeDeepSeekResponsesRequestBody(account, inbound)
			require.Equal(t, string(body), string(inbound), "native ingress must preserve duplicate IDs for upstream validation")
			require.Equal(t, string(body), string(outbound), "native egress must preserve the same request")
			require.Len(t, gjson.GetBytes(outbound, "input").Array(), 4)
			require.Equal(t, callID, gjson.GetBytes(outbound, "input.0.id").String())
			require.Equal(t, callID, gjson.GetBytes(outbound, "input.0.call_id").String())
			require.Equal(t, callID, gjson.GetBytes(outbound, "input.1.id").String())
			require.Equal(t, callID, gjson.GetBytes(outbound, "input.1.call_id").String())
			require.Equal(t, "{}", gjson.GetBytes(outbound, "input.0.arguments").String())
			require.Equal(t, `{"cmd":"pwd"}`, gjson.GetBytes(outbound, "input.1.arguments").String())
			require.Equal(t, "/tmp", gjson.GetBytes(outbound, "input.2.output").String())
			require.Equal(t, "next", gjson.GetBytes(outbound, "input.3.content.0.text").String())
		})
	}

	// The opt-in legacy response helper retains its existing assertions.
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
