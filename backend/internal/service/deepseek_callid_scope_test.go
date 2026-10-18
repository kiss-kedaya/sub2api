package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// A neutral platform + neutral model alias can still be routed to a
// DeepSeek-backed upstream. Passing duplicate call ids through in that case is
// what produced the upstream "Duplicate 'call_id'" rejection, so the rewrite
// must trigger on the shape of the body rather than only on platform/model.
func TestRewriteInboundDeepSeekCallIDs_NeutralScopeStillDedupes(t *testing.T) {
	callID := "call_00_NGDdtohYPFUNxkT8wU2X4174"
	body := []byte(`{"model":"some-alias","input":[` +
		`{"type":"function_call","id":"` + callID + `","call_id":"` + callID + `","name":"shell","arguments":"{\"cmd\":\"pwd\"}"},` +
		`{"type":"function_call","id":"` + callID + `","call_id":"` + callID + `","name":"shell","arguments":""},` +
		`{"type":"function_call_output","call_id":"` + callID + `","output":"/tmp"}]}`)

	out := RewriteInboundDeepSeekCallIDs(PlatformOpenAI, "some-alias", body)
	items := gjson.GetBytes(out, "input").Array()
	require.Len(t, items, 2, "duplicate function_call must be dropped")
	require.Equal(t, "{\"cmd\":\"pwd\"}", items[0].Get("arguments").String())
	require.NotEqual(t, callID, items[0].Get("id").String(), "id must not equal call_id")
}

// Bodies without tool calls must not be touched, so the wider trigger does not
// rewrite unrelated traffic.
func TestRewriteInboundDeepSeekCallIDs_NoToolCallsUntouched(t *testing.T) {
	body := []byte(`{"model":"some-alias","input":[{"type":"message","role":"user","content":"hi"}]}`)
	out := RewriteInboundDeepSeekCallIDs(PlatformOpenAI, "some-alias", body)
	require.Equal(t, string(body), string(out))
}

func TestBodyHasToolCallIDs(t *testing.T) {
	require.True(t, bodyHasToolCallIDs([]byte(`{"input":[{"call_id":"x"}]}`)))
	require.True(t, bodyHasToolCallIDs([]byte(`{"messages":[{"tool_calls":[]}]}`)))
	require.False(t, bodyHasToolCallIDs([]byte(`{"messages":[{"role":"user"}]}`)))
	require.False(t, bodyHasToolCallIDs(nil))
}
