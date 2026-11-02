package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Neutral platforms and model aliases retain their existing body-shape dedupe.
// The native DeepSeek exemption must not change that compatibility behavior.
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

// Explicit DeepSeek routing preserves duplicate IDs and invalid tool arguments
// so native upstream validation reports the original error to the client.
func TestRewriteInboundDeepSeekCallIDs_ExplicitDeepSeekPreservesNativeHistory(t *testing.T) {
	t.Parallel()
	for _, scope := range []struct {
		name     string
		platform string
		model    string
	}{
		{"platform-with-alias", PlatformDeepseek, "some-alias"},
		{"model-on-openai-platform", PlatformOpenAI, "deepseek-flash"},
		{"model-without-platform", "", "deepseek-v4-pro"},
		{"platform-and-model", PlatformDeepseek, "deepseek-v4.1-flash"},
	} {
		t.Run(scope.name, func(t *testing.T) {
			for _, tc := range []struct {
				name string
				body string
			}{
				{"responses-duplicate-pairs", `{"model":"some-alias","input":[{"type":"function_call","id":"call_same","call_id":"call_same","name":"lookup","arguments":"{}"},{"type":"function_call","id":"call_same","call_id":"call_same","name":"lookup","arguments":"{\"q\":7}"},{"type":"function_call_output","call_id":"call_same","output":"first"},{"type":"function_call_output","call_id":"call_same","output":"second"}],"opaque":9007199254740993}`},
				{"chat-duplicate-pairs", `{"model":"some-alias","messages":[{"role":"assistant","tool_calls":[{"type":"function","id":"call_same","function":{"name":"lookup","arguments":"{}"}},{"type":"function","id":"call_same","function":{"name":"lookup","arguments":"{\"q\":7}"}}]},{"role":"tool","tool_call_id":"call_same","content":"first"},{"role":"tool","tool_call_id":"call_same","content":"second"}]}`},
				{"invalid-tool-arguments", `{"model":"some-alias","input":[{"type":"function_call","id":"call_same","call_id":"call_same","name":"lookup","arguments":"{"},{"type":"function_call","id":"call_same","call_id":"call_same","name":"lookup","arguments":"{\"q\":7}"}]}`},
				{"invalid-json", `{"input":[{"call_id":"call_same"}`},
			} {
				t.Run(tc.name, func(t *testing.T) {
					body := []byte(tc.body)
					out := RewriteInboundDeepSeekCallIDs(scope.platform, scope.model, body)
					require.Equal(t, tc.body, string(out), "native history must remain byte-for-byte intact, including invalid data")
					require.Equal(t, tc.body, string(body), "the caller's input buffer must not be mutated")
				})
			}
		})
	}
}

func TestBodyHasToolCallIDs(t *testing.T) {
	require.True(t, bodyHasToolCallIDs([]byte(`{"input":[{"call_id":"x"}]}`)))
	require.True(t, bodyHasToolCallIDs([]byte(`{"messages":[{"tool_calls":[]}]}`)))
	require.False(t, bodyHasToolCallIDs([]byte(`{"messages":[{"role":"user"}]}`)))
	require.False(t, bodyHasToolCallIDs(nil))
}
