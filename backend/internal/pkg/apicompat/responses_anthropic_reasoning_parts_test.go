package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func anthropicReasoningText(events []AnthropicStreamEvent) string {
	var out string
	for _, event := range events {
		if event.Delta != nil && event.Delta.Type == "thinking_delta" {
			out += event.Delta.Thinking
		}
	}
	return out
}

func TestDeepSeekAnthropicOutputSummaryCannotBecomeReplayThinking(t *testing.T) {
	response := &ResponsesResponse{Output: []ResponsesOutput{{Type: "reasoning", Summary: []ResponsesSummary{{Type: "summary_text", Text: "display only"}}}}}
	strict := ResponsesToAnthropicWithOptions(response, "deepseek-flash", true)
	for _, block := range strict.Content {
		require.NotEqual(t, "thinking", block.Type)
	}
	legacy := ResponsesToAnthropic(response, "gpt")
	require.Equal(t, "display only", legacy.Content[0].Thinking)
	raw, err := json.Marshal(strict.Content)
	require.NoError(t, err)
	replay, err := AnthropicToResponsesWithOptions(&AnthropicRequest{Model: "deepseek-flash", Messages: []AnthropicMessage{{Role: "assistant", Content: raw}}}, &AnthropicToResponsesOptions{PreserveThinkingContent: true})
	require.NoError(t, err)
	require.NotContains(t, string(replay.Input), "display only")
}

func TestDeepSeekAnthropicSummaryDoneDoesNotSwallowRealReasoning(t *testing.T) {
	state := NewResponsesEventToAnthropicState()
	state.RequireReasoningContent = true
	var events []AnthropicStreamEvent
	for _, event := range []*ResponsesStreamEvent{
		{Type: "response.output_item.added", OutputIndex: 0, Item: &ResponsesOutput{Type: "reasoning"}},
		{Type: "response.reasoning_summary_text.done", OutputIndex: 0, Text: "display only"},
		{Type: "response.output_item.done", OutputIndex: 0, Item: &ResponsesOutput{Type: "reasoning", Content: []ResponsesContentPart{{Type: "reasoning_text", Text: "first"}, {Type: "reasoning_text", Text: "second"}}}},
	} {
		events = append(events, ResponsesEventToAnthropicEvents(event, state)...)
	}
	require.Equal(t, "firstsecond", anthropicReasoningText(events))
}

func TestDeepSeekAnthropicDoneOnlyPartsAndDeltaDeduplicatePerContentIndex(t *testing.T) {
	state := NewResponsesEventToAnthropicState()
	state.RequireReasoningContent = true
	var events []AnthropicStreamEvent
	for _, event := range []*ResponsesStreamEvent{
		{Type: "response.output_item.added", OutputIndex: 0, Item: &ResponsesOutput{Type: "reasoning"}},
		{Type: "response.reasoning_text.delta", OutputIndex: 0, ContentIndex: 0, Delta: "first"},
		{Type: "response.reasoning_text.done", OutputIndex: 0, ContentIndex: 0, Text: "first"},
		{Type: "response.reasoning_text.done", OutputIndex: 0, ContentIndex: 1, Text: "second"},
		{Type: "response.output_item.done", OutputIndex: 0, Item: &ResponsesOutput{Type: "reasoning", Content: []ResponsesContentPart{{Type: "reasoning_text", Text: "first"}, {Type: "reasoning_text", Text: "second"}}}},
	} {
		events = append(events, ResponsesEventToAnthropicEvents(event, state)...)
	}
	require.Equal(t, "firstsecond", anthropicReasoningText(events))
}
