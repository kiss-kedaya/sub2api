package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesToAnthropicRequest_NoneDisablesThinking(t *testing.T) {
	req := &ResponsesRequest{
		Model:     "deepseek-v4-pro",
		Reasoning: &ResponsesReasoning{Effort: "none"},
		Input:     json.RawMessage(`"hi"`),
	}
	out, err := ResponsesToAnthropicRequest(req)
	require.NoError(t, err)
	require.NotNil(t, out.Thinking)
	require.Equal(t, "disabled", out.Thinking.Type)
	require.Zero(t, out.Thinking.BudgetTokens)
	require.Nil(t, out.OutputConfig, "effort=none must not be sent as output_config.effort")
}

func TestResponsesToAnthropicRequest_DefaultDropsReasoningText(t *testing.T) {
	req := &ResponsesRequest{
		Model: "deepseek-v4-pro",
		Input: json.RawMessage(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"run"}]},
			{"type":"reasoning","id":"rs_1","summary":[],"content":[{"type":"reasoning_text","text":"provider cot"}]}
		]`),
	}
	out, err := ResponsesToAnthropicRequest(req)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	require.NotContains(t, string(out.Messages[0].Content), "provider cot")
}

func TestResponsesToAnthropicRequest_PreservesReasoningText(t *testing.T) {
	req := &ResponsesRequest{
		Model: "deepseek-v4-pro",
		Input: json.RawMessage(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"run"}]},
			{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"short summary"}],"content":[{"type":"reasoning_text","text":"real chain of thought"}]}
		]`),
	}
	out, err := ResponsesToAnthropicRequestWithOptions(req, &ResponsesToAnthropicOptions{PreserveReasoningText: true})
	require.NoError(t, err)
	require.Len(t, out.Messages, 2)
	blocks := parseContentBlocks(out.Messages[1].Content)
	require.Len(t, blocks, 1)
	require.Equal(t, "thinking", blocks[0].Type)
	require.Equal(t, "real chain of thought", blocks[0].Thinking)
	require.NotEqual(t, "short summary", blocks[0].Thinking)
}

func TestResponsesToAnthropic_PrefersReasoningTextOverSummary(t *testing.T) {
	resp := &ResponsesResponse{
		Output: []ResponsesOutput{{
			Type:    "reasoning",
			Content: []ResponsesContentPart{{Type: "reasoning_text", Text: "real chain of thought"}},
			Summary: []ResponsesSummary{{Type: "summary_text", Text: "short summary"}},
		}},
	}
	anth := ResponsesToAnthropic(resp, "deepseek-v4-pro")
	require.Len(t, anth.Content, 1)
	require.Equal(t, "thinking", anth.Content[0].Type)
	require.Equal(t, "real chain of thought", anth.Content[0].Thinking)
}

func TestResponsesToAnthropic_StreamFallsBackToReasoningText(t *testing.T) {
	state := NewResponsesEventToAnthropicState()
	ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{
		Type: "response.output_item.added",
		Item: &ResponsesOutput{Type: "reasoning"},
	}, state)
	events := ResponsesEventToAnthropicEvents(&ResponsesStreamEvent{
		Type: "response.output_item.done",
		Item: &ResponsesOutput{
			Type:    "reasoning",
			Status:  "completed",
			Content: []ResponsesContentPart{{Type: "reasoning_text", Text: "real chain of thought"}},
		},
	}, state)

	var thinking string
	for _, event := range events {
		if event.Type == "content_block_delta" && event.Delta != nil && event.Delta.Type == "thinking_delta" {
			thinking += event.Delta.Thinking
		}
	}
	require.Equal(t, "real chain of thought", thinking)
}

func TestChatCompletionsToResponses_ReasoningContentUsesReasoningTextField(t *testing.T) {
	req := &ChatCompletionsRequest{
		Model: "deepseek-v4-pro",
		Messages: []ChatMessage{{
			Role:             "assistant",
			Content:          json.RawMessage(`"answer"`),
			ReasoningContent: "real chain of thought",
		}},
	}
	out, err := ChatCompletionsToResponsesWithOptions(req, &ChatCompletionsToResponsesOptions{PreserveReasoningContent: true})
	require.NoError(t, err)

	var items []ResponsesInputItem
	require.NoError(t, json.Unmarshal(out.Input, &items))
	var reasoning *ResponsesInputItem
	for i := range items {
		if items[i].Type == "reasoning" {
			reasoning = &items[i]
			break
		}
	}
	require.NotNil(t, reasoning)
	var parts []ResponsesContentPart
	require.NoError(t, json.Unmarshal(reasoning.Content, &parts))
	require.Len(t, parts, 1)
	require.Equal(t, "reasoning_text", parts[0].Type)
	require.Equal(t, "real chain of thought", parts[0].Text)
}

func TestChatCompletionsResponseToResponses_ReasoningContentUsesReasoningTextField(t *testing.T) {
	resp := &ChatCompletionsResponse{Choices: []ChatChoice{{
		Index: 0,
		Message: ChatMessage{
			Role:             "assistant",
			Content:          json.RawMessage(`"answer"`),
			ReasoningContent: "real chain of thought",
		},
	}}}
	out := ChatCompletionsResponseToResponses(resp, "deepseek-v4-pro", nil, nil, false, nil)
	require.Len(t, out.Output, 2)
	require.Equal(t, "reasoning", out.Output[0].Type)
	require.Len(t, out.Output[0].Content, 1)
	require.Equal(t, "reasoning_text", out.Output[0].Content[0].Type)
	require.Equal(t, "real chain of thought", out.Output[0].Content[0].Text)
}

func TestChatCompletionsStream_ReasoningTerminalCarriesReasoningText(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"reasoning_content":"real chain of thought"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	})
	var completed *ResponsesResponse
	for _, event := range events {
		if event.Type == "response.completed" {
			completed = event.Response
		}
	}
	require.NotNil(t, completed)
	require.NotEmpty(t, completed.Output)
	require.Equal(t, "reasoning", completed.Output[0].Type)
	require.Len(t, completed.Output[0].Content, 1)
	require.Equal(t, "reasoning_text", completed.Output[0].Content[0].Type)
	require.Equal(t, "real chain of thought", completed.Output[0].Content[0].Text)
}

func TestResponsesToChatCompletions_PrefersReasoningTextOverSummary(t *testing.T) {
	resp := &ResponsesResponse{
		Output: []ResponsesOutput{{
			Type:    "reasoning",
			Content: []ResponsesContentPart{{Type: "reasoning_text", Text: "real chain of thought"}},
			Summary: []ResponsesSummary{{Type: "summary_text", Text: "short summary"}},
		}},
	}
	out := ResponsesToChatCompletions(resp, "deepseek-v4-pro")
	require.Equal(t, "real chain of thought", out.Choices[0].Message.ReasoningContent)
}

func TestResponsesToChatCompletionsRequest_PrefersReasoningTextOverSummary(t *testing.T) {
	req := &ResponsesRequest{
		Model: "deepseek-v4-pro",
		Input: json.RawMessage(`[
			{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"short summary"}],"content":[{"type":"reasoning_text","text":"real chain of thought"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}
		]`),
	}
	out, err := ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	require.Equal(t, "assistant", out.Messages[0].Role)
	require.Equal(t, "real chain of thought", out.Messages[0].ReasoningContent)
}

func TestAnthropicToResponses_ThinkingCarriesReasoningTextField(t *testing.T) {
	resp := &AnthropicResponse{
		Content: []AnthropicContentBlock{{Type: "thinking", Thinking: "real chain of thought"}},
	}
	out := AnthropicToResponsesResponse(resp)
	require.Len(t, out.Output, 1)
	require.Equal(t, "reasoning", out.Output[0].Type)
	require.Len(t, out.Output[0].Content, 1)
	require.Equal(t, "reasoning_text", out.Output[0].Content[0].Type)
	require.Equal(t, "real chain of thought", out.Output[0].Content[0].Text)
}

func TestAnthropicToResponsesStream_ThinkingTerminalCarriesReasoningTextField(t *testing.T) {
	state := NewAnthropicEventToResponsesState()
	idx := 0
	AnthropicEventToResponsesEvents(&AnthropicStreamEvent{
		Type:    "message_start",
		Message: &AnthropicResponse{},
	}, state)
	AnthropicEventToResponsesEvents(&AnthropicStreamEvent{
		Type:         "content_block_start",
		Index:        &idx,
		ContentBlock: &AnthropicContentBlock{Type: "thinking"},
	}, state)
	AnthropicEventToResponsesEvents(&AnthropicStreamEvent{
		Type:  "content_block_delta",
		Index: &idx,
		Delta: &AnthropicDelta{Type: "thinking_delta", Thinking: "real chain of thought"},
	}, state)
	events := AnthropicEventToResponsesEvents(&AnthropicStreamEvent{
		Type:  "content_block_stop",
		Index: &idx,
	}, state)

	var reasoning *ResponsesOutput
	for _, event := range events {
		if event.Type == "response.output_item.done" && event.Item != nil && event.Item.Type == "reasoning" {
			reasoning = event.Item
		}
	}
	require.NotNil(t, reasoning)
	require.Len(t, reasoning.Content, 1)
	require.Equal(t, "reasoning_text", reasoning.Content[0].Type)
	require.Equal(t, "real chain of thought", reasoning.Content[0].Text)
}

func TestChatCompletionsToResponses_DefaultKeepsSummaryOnly(t *testing.T) {
	req := &ChatCompletionsRequest{
		Model: "deepseek-v4-pro",
		Messages: []ChatMessage{{
			Role:             "assistant",
			Content:          json.RawMessage(`"answer"`),
			ReasoningContent: "provider cot",
		}},
	}
	out, err := ChatCompletionsToResponses(req)
	require.NoError(t, err)

	var items []ResponsesInputItem
	require.NoError(t, json.Unmarshal(out.Input, &items))
	var reasoning *ResponsesInputItem
	for i := range items {
		if items[i].Type == "reasoning" {
			reasoning = &items[i]
			break
		}
	}
	require.NotNil(t, reasoning)
	require.Nil(t, reasoning.Content)
	require.Len(t, reasoning.Summary, 1)
	require.Equal(t, "provider cot", reasoning.Summary[0].Text)
}

func TestResponsesToChatCompletionsRequest_SummaryOnlyDoesNotBecomeReasoningContent(t *testing.T) {
	req := &ResponsesRequest{
		Model: "gpt-5.5",
		Input: json.RawMessage(`[
			{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"display only"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}
		]`),
	}
	out, err := ResponsesToChatCompletionsRequest(req)
	require.NoError(t, err)
	require.Len(t, out.Messages, 1)
	require.Empty(t, out.Messages[0].ReasoningContent)
}

func TestResponsesToChatCompletionsStream_SeparateReasoningItemsNotSwallowed(t *testing.T) {
	state := NewResponsesEventToChatState()
	chunks := ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:        "response.output_item.done",
		OutputIndex: 0,
		Item: &ResponsesOutput{
			Type:    "reasoning",
			Content: []ResponsesContentPart{{Type: "reasoning_text", Text: "first"}},
		},
	}, state)
	chunks = append(chunks, ResponsesEventToChatChunks(&ResponsesStreamEvent{
		Type:        "response.output_item.done",
		OutputIndex: 1,
		Item: &ResponsesOutput{
			Type:    "reasoning",
			Content: []ResponsesContentPart{{Type: "reasoning_text", Text: "second"}},
		},
	}, state)...)

	var got []string
	for _, chunk := range chunks {
		for _, choice := range chunk.Choices {
			if choice.Delta.ReasoningContent != nil {
				got = append(got, *choice.Delta.ReasoningContent)
			}
		}
	}
	require.Equal(t, []string{"first", "second"}, got)
}
