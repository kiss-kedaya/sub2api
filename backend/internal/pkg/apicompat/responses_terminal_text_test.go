package apicompat

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesTerminalTextFallback(t *testing.T) {
	for _, terminalType := range []string{"response.completed", "response.done", "response.incomplete"} {
		for _, prelude := range []string{"none", "created", "reasoning", "text"} {
			t.Run(terminalType+"/"+prelude, func(t *testing.T) {
				chat := NewResponsesEventToChatState()
				chat.IncludeUsage = true
				anthropic := NewResponsesEventToAnthropicState()
				var chunks []ChatCompletionsChunk
				var events []AnthropicStreamEvent
				convert := func(event ResponsesStreamEvent) {
					chunks = append(chunks, ResponsesEventToChatChunks(&event, chat)...)
					events = append(events, ResponsesEventToAnthropicEvents(&event, anthropic)...)
				}
				if prelude != "none" {
					convert(ResponsesStreamEvent{Type: "response.created", Response: &ResponsesResponse{ID: "resp_text"}})
				}
				if prelude == "reasoning" {
					convert(ResponsesStreamEvent{Type: "response.reasoning_text.delta", Delta: "thinking"})
				}
				if prelude == "text" {
					convert(ResponsesStreamEvent{Type: "response.output_text.delta", Delta: "hello"})
					convert(ResponsesStreamEvent{Type: "response.output_text.delta", Delta: "\nworld"})
				}
				convert(ResponsesStreamEvent{Type: terminalType, Response: &ResponsesResponse{
					ID: "resp_text", Status: "completed",
					Output: []ResponsesOutput{{Type: "message", Content: []ResponsesContentPart{
						{Type: "output_text", Text: "hello"}, {Type: "output_text", Text: "\nworld"},
					}}},
					Usage: &ResponsesUsage{InputTokens: 12, OutputTokens: 4, InputTokensDetails: &ResponsesInputTokensDetails{CachedTokens: 3}},
				}})
				var chatText, anthropicText strings.Builder
				chatStopped := false
				for _, chunk := range chunks {
					for _, choice := range chunk.Choices {
						if choice.Delta.Content != nil && *choice.Delta.Content != "" {
							require.False(t, chatStopped, "text must precede finish_reason")
							chatText.WriteString(*choice.Delta.Content)
						}
						if choice.FinishReason != nil {
							chatStopped = true
						}
					}
				}
				anthropicStarted, anthropicStopped := false, false
				for _, event := range events {
					if event.Type == "message_start" {
						anthropicStarted = true
					}
					if event.Delta != nil && event.Delta.Type == "text_delta" {
						require.True(t, anthropicStarted)
						require.False(t, anthropicStopped)
						anthropicText.WriteString(event.Delta.Text)
					}
					if event.Type == "message_stop" {
						anthropicStopped = true
					}
				}
				require.Equal(t, "hello\nworld", chatText.String())
				require.Equal(t, "hello\nworld", anthropicText.String())
				require.True(t, chatStopped)
				require.True(t, anthropicStopped)
				require.Equal(t, 12, chat.Usage.PromptTokens)
				require.Equal(t, 4, chat.Usage.CompletionTokens)
				require.Equal(t, 3, chat.Usage.PromptTokensDetails.CachedTokens)
				require.Equal(t, 9, anthropic.InputTokens)
				require.Equal(t, 4, anthropic.OutputTokens)
				require.Equal(t, 3, anthropic.CacheReadInputTokens)
				require.Empty(t, FinalizeResponsesChatStream(chat))
				require.Empty(t, FinalizeResponsesAnthropicStream(anthropic))
			})
		}
	}
}
