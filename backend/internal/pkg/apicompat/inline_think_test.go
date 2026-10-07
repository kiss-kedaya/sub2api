package apicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runInlineThink(deltas ...string) (string, string) {
	var splitter InlineThinkSplitter
	var thinking, text strings.Builder
	collect := func(th, tx string) {
		_, _ = thinking.WriteString(th)
		_, _ = text.WriteString(tx)
	}
	for _, delta := range deltas {
		collect(splitter.Push(delta))
	}
	collect(splitter.Flush())
	return thinking.String(), text.String()
}

func TestInlineThink_SplitsBothTagsAndMismatch(t *testing.T) {
	thinking, text := runInlineThink("<think>a</think>b")
	assert.Equal(t, "a", thinking)
	assert.Equal(t, "b", text)

	thinking, text = runInlineThink("<thinking>a</thinking>b")
	assert.Equal(t, "a", thinking)
	assert.Equal(t, "b", text)

	thinking, text = runInlineThink("<think>a</thinking>b")
	assert.Equal(t, "a", thinking)
	assert.Equal(t, "b", text)
}

func TestInlineThink_EmitsReasoningBeforeCloseTag(t *testing.T) {
	var splitter InlineThinkSplitter
	thinking, text := splitter.Push("<think>")
	assert.Equal(t, "", thinking)
	assert.Equal(t, "", text)
	thinking, text = splitter.Push("step one")
	assert.Equal(t, "step one", thinking)
	assert.Equal(t, "", text)
	thinking, text = splitter.Push(" two</th")
	assert.Equal(t, " two", thinking)
	assert.Equal(t, "", text)
	thinking, text = splitter.Push("ink>done")
	assert.Equal(t, "", thinking)
	assert.Equal(t, "done", text)
}

func TestInlineThink_ChunkIndependent(t *testing.T) {
	response := "  \n<think>\r\nrea\nson\n</think>\r\n\n    return 42\n"
	wantThink, wantText := "rea\nson", "    return 42\n"
	thinking, text := runInlineThink(response)
	assert.Equal(t, wantThink, thinking)
	assert.Equal(t, wantText, text)

	runes := []rune(response)
	for chunkLen := 1; chunkLen < len(runes); chunkLen++ {
		var parts []string
		for i := 0; i < len(runes); i += chunkLen {
			end := i + chunkLen
			if end > len(runes) {
				end = len(runes)
			}
			parts = append(parts, string(runes[i:end]))
		}
		thinking, text = runInlineThink(parts...)
		if thinking != wantThink || text != wantText {
			t.Fatalf("chunk_len %d got (%q,%q)", chunkLen, thinking, text)
		}
	}
}

func TestInlineThink_FlushReleasesHoldback(t *testing.T) {
	thinking, text := runInlineThink("<thi")
	assert.Equal(t, "", thinking)
	assert.Equal(t, "<thi", text)

	thinking, text = runInlineThink("<think>partial</th")
	assert.Equal(t, "partial</th", thinking)
	assert.Equal(t, "", text)

	thinking, text = runInlineThink("<think>a </th", "ree> b</think>c")
	assert.Equal(t, "a </three> b", thinking)
	assert.Equal(t, "c", text)

	thinking, text = runInlineThink("<think>a\n", "\n</th", "ree>b\n", "</think>c")
	assert.Equal(t, "a\n\n</three>b", thinking)
	assert.Equal(t, "c", text)

	thinking, text = runInlineThink("<", "div>hi <think>x</think>")
	assert.Equal(t, "", thinking)
	assert.Equal(t, "<div>hi <think>x</think>", text)

	thinking, text = runInlineThink("\n\n", "hi")
	assert.Equal(t, "", thinking)
	assert.Equal(t, "\n\nhi", text)
}

func TestSplitLeadingThinkBlock(t *testing.T) {
	thinking, answer, ok := splitLeadingThinkBlock(" <thinking>\nrea\nson\n</thinking>\n\n    answer")
	require.True(t, ok)
	assert.Equal(t, "rea\nson", thinking)
	assert.Equal(t, "    answer", answer)

	thinking, answer, ok = splitLeadingThinkBlock("<think>a</thinking>b")
	require.True(t, ok)
	assert.Equal(t, "a", thinking)
	assert.Equal(t, "b", answer)

	_, _, ok = splitLeadingThinkBlock("<think>unclosed")
	assert.False(t, ok)
	_, _, ok = splitLeadingThinkBlock("hi <think>a</think>")
	assert.False(t, ok)
}

func TestStream_InlineThinkingTagNotInBody(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"index":0,"delta":{"content":"<thinking>tool call</thinking>\npong"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	})
	var thinking, text strings.Builder
	for _, event := range events {
		switch event.Type {
		case "response.reasoning_summary_text.delta":
			_, _ = thinking.WriteString(event.Delta)
			assert.NotContains(t, event.Delta, "thinking>")
		case "response.output_text.delta":
			_, _ = text.WriteString(event.Delta)
			assert.NotContains(t, event.Delta, "thinking>")
		}
	}
	assert.Equal(t, "tool call", thinking.String())
	assert.Equal(t, "pong", text.String())
}

func TestChatCompletionsResponseToResponses_InlineThinkingTag(t *testing.T) {
	resp := &ChatCompletionsResponse{
		Choices: []ChatChoice{{
			Message: ChatMessage{
				Role:    "assistant",
				Content: json.RawMessage("\"<thinking>tool call</thinking>\\npong\""),
			},
		}},
	}
	out := ChatCompletionsResponseToResponses(resp, "deepseek-v4", nil, nil, false, nil)
	require.GreaterOrEqual(t, len(out.Output), 2)
	assert.Equal(t, "reasoning", out.Output[0].Type)
	require.NotEmpty(t, out.Output[0].Summary)
	assert.Equal(t, "tool call", out.Output[0].Summary[0].Text)
	assert.Equal(t, "message", out.Output[1].Type)
	require.NotEmpty(t, out.Output[1].Content)
	assert.Equal(t, "pong", out.Output[1].Content[0].Text)
	assert.NotContains(t, out.Output[1].Content[0].Text, "thinking>")
}

func TestAnthropicStream_InlineThinkingTagNotInBody(t *testing.T) {
	state := NewChatCompletionsToAnthropicStreamState("deepseek-v4")
	payloads := []string{
		`{"choices":[{"index":0,"delta":{"content":"<thinking>tool call</thinking>\npong"}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	}
	var events []AnthropicStreamEvent
	for _, payload := range payloads {
		var chunk ChatCompletionsChunk
		require.NoError(t, json.Unmarshal([]byte(payload), &chunk))
		events = append(events, ChatCompletionsChunkToAnthropicEvents(&chunk, state)...)
	}
	events = append(events, FinalizeChatCompletionsAnthropicStream(state)...)
	var thinking, text strings.Builder
	for _, event := range events {
		if event.Delta == nil {
			continue
		}
		switch event.Delta.Type {
		case "thinking_delta":
			_, _ = thinking.WriteString(event.Delta.Thinking)
			assert.NotContains(t, event.Delta.Thinking, "thinking>")
		case "text_delta":
			_, _ = text.WriteString(event.Delta.Text)
			assert.NotContains(t, event.Delta.Text, "thinking>")
		}
	}
	assert.Equal(t, "tool call", thinking.String())
	assert.Equal(t, "pong", text.String())
}

func TestResponsesToAnthropic_LocalShellCall(t *testing.T) {
	resp := &ResponsesResponse{
		ID:     "resp_1",
		Status: "completed",
		Output: []ResponsesOutput{{
			Type:      "local_shell_call",
			CallID:    "call_sh",
			Name:      "local_shell",
			Arguments: `{"command":["pwd"]}`,
		}},
	}
	out := ResponsesToAnthropic(resp, "gpt-5.4")
	require.Len(t, out.Content, 1)
	assert.Equal(t, "tool_use", out.Content[0].Type)
	assert.Equal(t, "local_shell", out.Content[0].Name)
	assert.Equal(t, "call_sh", out.Content[0].ID)
	assert.JSONEq(t, `{"command":["pwd"]}`, string(out.Content[0].Input))
	require.NotNil(t, out.StopReason)
	assert.Equal(t, "tool_use", *out.StopReason)
}

func TestResponsesEventToAnthropic_LocalShellCallCarriesAction(t *testing.T) {
	state := NewResponsesEventToAnthropicState()
	incoming := []ResponsesStreamEvent{
		{Type: "response.output_item.added", OutputIndex: 0, Item: &ResponsesOutput{Type: "local_shell_call", CallID: "call_sh", Name: "local_shell"}},
		{Type: "response.output_item.done", OutputIndex: 0, Item: &ResponsesOutput{Type: "local_shell_call", CallID: "call_sh", Name: "local_shell", Arguments: `{"command":["pwd"]}`, Status: "completed"}},
	}
	var sawName, sawArgs bool
	for _, evt := range incoming {
		for _, out := range ResponsesEventToAnthropicEvents(&evt, state) {
			if out.ContentBlock != nil && out.ContentBlock.Type == "tool_use" {
				assert.Equal(t, "local_shell", out.ContentBlock.Name)
				sawName = true
			}
			if out.Delta != nil && out.Delta.Type == "input_json_delta" {
				assert.JSONEq(t, `{"command":["pwd"]}`, out.Delta.PartialJSON)
				sawArgs = true
			}
		}
	}
	assert.True(t, sawName)
	assert.True(t, sawArgs)
}
