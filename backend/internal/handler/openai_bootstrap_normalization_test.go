package handler

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const delegationBootstrapEnvelope = `<codex_delegation><source_thread_id>thread-1</source_thread_id><input>do the work</input></codex_delegation>`

func mustJSONString(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}

func TestNormalizeCodexDelegationBootstrapRequiresCalllessSafeShape(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","output":` + mustJSONString(t, delegationBootstrapEnvelope) + `}]}`)
	got, changed := normalizeCodexDelegationBootstrap(body)
	require.True(t, changed)
	require.Equal(t, "message", gjson.GetBytes(got, "input.0.type").String())
	require.Equal(t, "user", gjson.GetBytes(got, "input.0.role").String())
	require.Equal(t, delegationBootstrapEnvelope, gjson.GetBytes(got, "input.0.content.0.text").String())
	require.False(t, gjson.GetBytes(got, "input.0.call_id").Exists())

	again, changedAgain := normalizeCodexDelegationBootstrap(got)
	require.False(t, changedAgain)
	require.Equal(t, got, again)
}

func TestNormalizeCodexDelegationBootstrapRejectsAmbiguousInputs(t *testing.T) {
	cases := []string{
		`{"model":"gpt-5","previous_response_id":"resp-1","input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","output":` + mustJSONString(t, delegationBootstrapEnvelope) + `}]}`,
		`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","call_id":"call-1","output":` + mustJSONString(t, delegationBootstrapEnvelope) + `}]}`,
		`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","output":` + mustJSONString(t, delegationBootstrapEnvelope) + `},{"type":"function_call","call_id":"call-1"}]}`,
		`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","output":` + mustJSONString(t, delegationBootstrapEnvelope) + `},{"type":"item_reference","id":"call-1"}]}`,
		`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","output":"not an envelope"}]}`,
		`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","output":` + mustJSONString(t, delegationBootstrapEnvelope) + `},{"type":"computer_call_output","output":"done"}]}`,
		`{"model":"gpt-5","previous_response_id":"","previous_response_id":"resp-1","input":[]}`,
	}
	for _, body := range cases {
		got, changed := normalizeCodexDelegationBootstrap([]byte(body))
		require.False(t, changed, body)
		require.Equal(t, []byte(body), got)
	}
}

func TestNormalizeCodexBootstrapPreservesExactNumbersAndDuplicateRejection(t *testing.T) {
	body := []byte(`{"model":"gpt-5","metadata":{"integer":9007199254740993},"input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","output":` + mustJSONString(t, delegationBootstrapEnvelope) + `}]}`)
	got, changed := normalizeCodexDelegationBootstrap(body)
	require.True(t, changed)
	require.Equal(t, "9007199254740993", gjson.GetBytes(got, "metadata.integer").Raw)

	duplicate := []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","output":` + mustJSONString(t, delegationBootstrapEnvelope) + `,"output":` + mustJSONString(t, delegationBootstrapEnvelope) + `}]}`)
	got, changed = normalizeCodexDelegationBootstrap(duplicate)
	require.False(t, changed)
	require.Equal(t, duplicate, got)
}
