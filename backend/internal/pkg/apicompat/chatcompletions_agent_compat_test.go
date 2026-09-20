package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChatCompletionsToResponses_AgentToolChoice(t *testing.T) {
	for _, tt := range []struct {
		name, choice, want string
	}{
		{"named function", `{"type":"function","function":{"name":"exec_command"}}`, `{"type":"function","name":"exec_command"}`},
		{"flat function", `{"type":"function","name":"exec_command"}`, `{"type":"function","name":"exec_command"}`},
		{"auto", `"auto"`, `"auto"`},
		{"required", `"required"`, `"required"`},
		{"none", `"none"`, `"none"`},
		{"allowed tools", `{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"exec_command"}}]}}`, `{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"exec_command"}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := &ChatCompletionsRequest{
				Model: "gpt-5.6-sol", ToolChoice: json.RawMessage(tt.choice),
				Tools: []ChatTool{{Type: "function", Function: &ChatFunction{Name: "exec_command"}}},
			}
			converted, err := ChatCompletionsToResponses(request)
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(converted.ToolChoice))
			require.JSONEq(t, tt.choice, string(request.ToolChoice), "conversion must not mutate the client request")
		})
	}
}

func TestChatCompletionsToResponses_AgentDeveloperAndToolHistory(t *testing.T) {
	request := &ChatCompletionsRequest{
		Model: "gpt-5.6-sol",
		Messages: []ChatMessage{
			{Role: "developer", Content: json.RawMessage(`"Use exec_command for terminal commands."`)},
			{Role: "user", Content: json.RawMessage(`"Print the working directory."`)},
			{Role: "assistant", ToolCalls: []ChatToolCall{{ID: "call_terminal", Type: "function", Function: ChatFunctionCall{Name: "exec_command", Arguments: `{"cmd":"pwd"}`}}}},
			{Role: "tool", ToolCallID: "call_terminal", Content: json.RawMessage(`"/workspace"`)},
		},
	}
	converted, err := ChatCompletionsToResponses(request)
	require.NoError(t, err)
	var input []ResponsesInputItem
	require.NoError(t, json.Unmarshal(converted.Input, &input))
	require.Len(t, input, 4)
	require.Equal(t, "developer", input[0].Role)
	require.Equal(t, "function_call", input[2].Type)
	require.Equal(t, "exec_command", input[2].Name)
	require.JSONEq(t, `{"cmd":"pwd"}`, input[2].Arguments)
	require.Equal(t, "call_terminal", input[2].CallID)
	require.Equal(t, "function_call_output", input[3].Type)
	require.Equal(t, input[2].CallID, input[3].CallID)
	require.Equal(t, "/workspace", input[3].Output)
}
