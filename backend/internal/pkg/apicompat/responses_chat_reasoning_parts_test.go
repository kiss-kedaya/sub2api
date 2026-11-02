package apicompat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func chatReasoningText(chunks []ChatCompletionsChunk) string {
	var out string
	for _, chunk := range chunks {
		for _, choice := range chunk.Choices {
			if choice.Delta.ReasoningContent != nil {
				out += *choice.Delta.ReasoningContent
			}
		}
	}
	return out
}

func TestDeepSeekChatOutputSummaryCannotBecomeReplayThinking(t *testing.T) {
	response := &ResponsesResponse{Output: []ResponsesOutput{{Type: "reasoning", Summary: []ResponsesSummary{{Type: "summary_text", Text: "display only"}}}}}
	strict := ResponsesToChatCompletionsWithOptions(response, "deepseek-flash", true)
	require.Empty(t, strict.Choices[0].Message.ReasoningContent)
	legacy := ResponsesToChatCompletions(response, "gpt")
	require.Equal(t, "display only", legacy.Choices[0].Message.ReasoningContent)
}

func TestDeepSeekChatSummaryDoneDoesNotSwallowRealReasoning(t *testing.T) {
	state := NewResponsesEventToChatState()
	state.RequireReasoningContent = true
	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.reasoning_summary_text.done", OutputIndex: 0, Text: "display only"}, state)
	chunks = append(chunks, ResponsesEventToChatChunks(&ResponsesStreamEvent{Type: "response.output_item.done", OutputIndex: 0, Item: &ResponsesOutput{Type: "reasoning", Content: []ResponsesContentPart{{Type: "reasoning_text", Text: "first"}, {Type: "reasoning_text", Text: "second"}}}}, state)...)
	require.Equal(t, "firstsecond", chatReasoningText(chunks))
}

func TestDeepSeekChatDoneOnlyPartsAndDeltaDeduplicatePerContentIndex(t *testing.T) {
	state := NewResponsesEventToChatState()
	state.RequireReasoningContent = true
	var chunks []ChatCompletionsChunk
	for _, event := range []*ResponsesStreamEvent{
		{Type: "response.reasoning_text.delta", OutputIndex: 0, ContentIndex: 0, Delta: "first"},
		{Type: "response.reasoning_text.done", OutputIndex: 0, ContentIndex: 0, Text: "first"},
		{Type: "response.reasoning_text.done", OutputIndex: 0, ContentIndex: 1, Text: "second"},
		{Type: "response.output_item.done", OutputIndex: 0, Item: &ResponsesOutput{Type: "reasoning", Content: []ResponsesContentPart{{Type: "reasoning_text", Text: "first"}, {Type: "reasoning_text", Text: "second"}}}},
	} {
		chunks = append(chunks, ResponsesEventToChatChunks(event, state)...)
	}
	require.Equal(t, "firstsecond", chatReasoningText(chunks))
}
