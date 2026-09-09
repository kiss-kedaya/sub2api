package handler

import (
	"encoding/json"
	"strings"
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

func TestCodexBootstrapInputLooksRelevant(t *testing.T) {
	require.False(t, codexBootstrapInputLooksRelevant([]byte(`{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)))
	require.False(t, codexBootstrapInputLooksRelevant([]byte(`{"model":"gpt-5"}`)))
	require.True(t, codexBootstrapInputLooksRelevant([]byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":"x"}]}`)))
	require.True(t, codexBootstrapInputLooksRelevant([]byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_tui","name":"create_thread","output":"x"}]}`)))
}

func TestApplyCodexBootstrapNormalizationsSkipsOrdinaryResponsesBodies(t *testing.T) {
	body := []byte(`{"model":"gpt-5","previous_response_id":"","previous_response_id":"resp-1","input":[{"type":"message","role":"user"}]}`)
	got, automationChanged, delegationChanged := applyCodexBootstrapNormalizations(body)
	require.False(t, automationChanged)
	require.False(t, delegationChanged)
	require.Equal(t, body, got)
}

func TestApplyCodexBootstrapNormalizationsRewritesAutomationAndDelegation(t *testing.T) {
	output := automationBootstrapOutput("wiki-maintenance", "never", "Review the project and report changes.")
	autoBody := automationBootstrapBody(t, output)
	got, automationChanged, delegationChanged := applyCodexBootstrapNormalizations(autoBody)
	require.True(t, automationChanged)
	require.False(t, delegationChanged)
	require.Equal(t, "message", gjson.GetBytes(got, "input.0.type").String())

	delegation := []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","output":` + mustJSONString(t, delegationBootstrapEnvelope) + `}]}`)
	got, automationChanged, delegationChanged = applyCodexBootstrapNormalizations(delegation)
	require.False(t, automationChanged)
	require.True(t, delegationChanged)
	require.Equal(t, "message", gjson.GetBytes(got, "input.0.type").String())
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
		`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","call_id":"call-1","output":` + mustJSONString(t, delegationBootstrapEnvelope) + `}]}`,
		`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"create_thread","output":` + mustJSONString(t, delegationBootstrapEnvelope) + `},{"type":"function_call"}]}`,
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

func TestNormalizeCodexDelegationBootstrapWithHistoricalContext(t *testing.T) {
	body := []byte(`{"model":"gpt-5","previous_response_id":"resp-1","input":[{"type":"function_call_output","namespace":"codex_app","name":"send_message_to_thread","output":` + mustJSONString(t, delegationBootstrapEnvelope) + `},{"type":"function_call","call_id":"call-1"},{"type":"function_call_output","call_id":"call-1","output":"done"},{"type":"item_reference","id":"item-1"},{"type":"computer_call","call_id":"call-2"}]}`)

	got, changed := normalizeCodexDelegationBootstrap(body)
	require.True(t, changed)
	require.Equal(t, "message", gjson.GetBytes(got, "input.0.type").String())
	require.Equal(t, "user", gjson.GetBytes(got, "input.0.role").String())
	require.Equal(t, "resp-1", gjson.GetBytes(got, "previous_response_id").String())
	require.Equal(t, "call-1", gjson.GetBytes(got, "input.2.call_id").String())
	require.Equal(t, "item-1", gjson.GetBytes(got, "input.3.id").String())
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

func automationBootstrapOutput(id, lastRun, prompt string) string {
	return "Automation: Scheduled project review\n" +
		"Automation ID: " + id + "\n" +
		"Automation memory: $CODEX_HOME/automations/" + id + "/memory.md\n" +
		"Last run: " + lastRun + "\n\n" + prompt
}

func automationBootstrapBody(t *testing.T, output string) []byte {
	return []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":` + mustJSONString(t, output) + `}]}`)
}

func TestNormalizeCodexAutomationBootstrapValidatesSafeEnvelope(t *testing.T) {
	output := automationBootstrapOutput("wiki-maintenance", "never", "Review the project and report changes.")
	got, changed := normalizeCodexAutomationBootstrap(automationBootstrapBody(t, output))
	require.True(t, changed)
	require.Equal(t, "message", gjson.GetBytes(got, "input.0.type").String())
	require.Equal(t, output, gjson.GetBytes(got, "input.0.content.0.text").String())

	crlf := strings.ReplaceAll(output, "\n", "\r\n")
	got, changed = normalizeCodexAutomationBootstrap(automationBootstrapBody(t, crlf))
	require.True(t, changed)
	require.Equal(t, crlf, gjson.GetBytes(got, "input.0.content.0.text").String())
}

func TestNormalizeCodexAutomationBootstrapRejectsUnsafeEnvelope(t *testing.T) {
	valid := automationBootstrapOutput("wiki", "never", "Review the project.")
	cases := []string{
		strings.Replace(valid, "/wiki/memory.md", "/other/memory.md", 1),
		automationBootstrapOutput("../wiki", "never", "Review the project."),
		automationBootstrapOutput("wiki", "yesterday", "Review the project."),
		strings.Replace(valid, "\n\nReview", "\nReview", 1),
		strings.Replace(valid, "Review the project.", " ", 1),
	}
	for _, output := range cases {
		body := automationBootstrapBody(t, output)
		got, changed := normalizeCodexAutomationBootstrap(body)
		require.False(t, changed, output)
		require.Equal(t, body, got)
	}

	withCallID := []byte(`{"model":"gpt-5","input":[{"type":"function_call_output","namespace":"codex_app","name":"automation_update","call_id":"call-1","output":` + mustJSONString(t, valid) + `}]}`)
	got, changed := normalizeCodexAutomationBootstrap(withCallID)
	require.False(t, changed)
	require.Equal(t, withCallID, got)
}

func TestNormalizeCodexAutomationBootstrapHeartbeat(t *testing.T) {
	output := `<heartbeat><automation_id>wiki</automation_id></heartbeat>`
	got, changed := normalizeCodexAutomationBootstrap(automationBootstrapBody(t, output))
	require.True(t, changed)
	require.Equal(t, "message", gjson.GetBytes(got, "input.0.type").String())
	require.Equal(t, "user", gjson.GetBytes(got, "input.0.role").String())
	require.Equal(t, output, gjson.GetBytes(got, "input.0.content.0.text").String())
	require.False(t, gjson.GetBytes(got, "input.0.call_id").Exists())

	again, changedAgain := normalizeCodexAutomationBootstrap(got)
	require.False(t, changedAgain)
	require.Equal(t, got, again)
}

func TestNormalizeCodexAutomationBootstrapRejectsUnsafeHeartbeatShapes(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{name: "arbitrary tool output", output: `<result><automation_id>wiki</automation_id></result>`},
		{name: "root attribute", output: `<heartbeat status="ok"><automation_id>wiki</automation_id></heartbeat>`},
		{name: "namespaced root", output: `<heartbeat xmlns="urn:codex"><automation_id>wiki</automation_id></heartbeat>`},
		{name: "extra child", output: `<heartbeat><automation_id>wiki</automation_id><status>ok</status></heartbeat>`},
		{name: "nested id content", output: `<heartbeat><automation_id><value>wiki</value></automation_id></heartbeat>`},
		{name: "padded id", output: `<heartbeat><automation_id> wiki </automation_id></heartbeat>`},
		{name: "unsafe id", output: `<heartbeat><automation_id>../wiki</automation_id></heartbeat>`},
		{name: "comment", output: `<heartbeat><!-- ok --><automation_id>wiki</automation_id></heartbeat>`},
		{name: "trailing content", output: `<heartbeat><automation_id>wiki</automation_id></heartbeat>extra`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := automationBootstrapBody(t, tt.output)
			got, changed := normalizeCodexAutomationBootstrap(body)
			require.False(t, changed)
			require.Equal(t, body, got)
		})
	}
}
