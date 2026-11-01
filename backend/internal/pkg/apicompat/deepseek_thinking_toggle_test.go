package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeepSeekResponsesToChatExplicitNoneUsesThinkingToggle(t *testing.T) {
	req := &ResponsesRequest{Model: "deepseek-flash", Input: json.RawMessage(`"hi"`), Reasoning: &ResponsesReasoning{Effort: "none"}}
	out, err := ResponsesToChatCompletionsRequestWithOptions(req, &ResponsesToChatOptions{PreserveThinkingSwitch: true})
	require.NoError(t, err)
	require.NotNil(t, out.Thinking)
	require.Equal(t, "disabled", out.Thinking.Type)
	require.Empty(t, out.ReasoningEffort, "none is not a DeepSeek Chat effort level")
	legacy, err := ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Nil(t, legacy.Thinking)
	require.Equal(t, "none", legacy.ReasoningEffort)
}

func TestDeepSeekChatToResponsesThinkingTogglePrecedesEffort(t *testing.T) {
	req := &ChatCompletionsRequest{Model: "deepseek-flash", Messages: []ChatMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}}, Thinking: &AnthropicThinking{Type: "disabled"}, ReasoningEffort: "max"}
	out, err := ChatCompletionsToResponsesWithOptions(req, &ChatCompletionsToResponsesOptions{PreserveReasoningContent: true})
	require.NoError(t, err)
	require.Equal(t, "none", out.Reasoning.Effort)
	legacy, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)
	require.Equal(t, "max", legacy.Reasoning.Effort)
}

func TestDeepSeekAnthropicToResponsesKeepsMaximumEffort(t *testing.T) {
	req := &AnthropicRequest{Model: "deepseek-flash", MaxTokens: 4096, OutputConfig: &AnthropicOutputConfig{Effort: "max"}, Messages: []AnthropicMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}}}
	out, err := AnthropicToResponsesWithOptions(req, &AnthropicToResponsesOptions{PreserveThinkingContent: true})
	require.NoError(t, err)
	require.Equal(t, "max", out.Reasoning.Effort)
	legacy, err := AnthropicToResponses(req)
	require.NoError(t, err)
	require.Equal(t, "xhigh", legacy.Reasoning.Effort)
}
