package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageCacheSurvivesProtocolRoundTrips(t *testing.T) {
	tests := []struct {
		name   string
		fields string
		read   int
		write  int
	}{
		{"read input alias", `"cache_read_input_tokens":128`, 128, 0},
		{"read alias", `"cache_read_tokens":128`, 128, 0},
		{"cached alias", `"cached_tokens":128`, 128, 0},
		{"write input alias", `"cache_write_input_tokens":32`, 0, 32},
		{"creation input alias", `"cache_creation_input_tokens":32`, 0, 32},
		{"write alias priority", `"cache_write_tokens":32,"cache_creation_input_tokens":64`, 0, 32},
		{"split details", `"input_tokens_details":{"audio_tokens":4},"prompt_tokens_details":{"cached_tokens":128,"cache_write_tokens":32}`, 128, 32},
		{"split write priority", `"input_tokens_details":{"cache_creation_tokens":64},"prompt_tokens_details":{"cache_write_tokens":32}`, 0, 32},
		{"write zero", `"input_tokens_details":{"cache_write_tokens":0,"cache_creation_tokens":64}`, 0, 0},
		{"write null", `"input_tokens_details":{"cache_write_tokens":null},"cache_creation_input_tokens":64`, 0, 0},
		{"write negative", `"input_tokens_details":{"cache_write_tokens":-1,"cache_creation_tokens":64}`, 0, 0},
		{"read zero", `"input_tokens_details":{"cached_tokens":0},"prompt_tokens_details":{"cached_tokens":128},"cached_tokens":64`, 0, 0},
		{"prompt write zero", `"prompt_tokens_details":{"cache_write_tokens":0,"cache_creation_tokens":64}`, 0, 0},
	}
	for _, tt := range tests {
		for _, source := range []string{"responses", "chat"} {
			t.Run(tt.name+"/"+source, func(t *testing.T) {
				var usage *ResponsesUsage
				if source == "responses" {
					require.NoError(t, json.Unmarshal([]byte(`{"input_tokens":200,"output_tokens":5,`+tt.fields+`}`), &usage))
				} else {
					var chat ChatUsage
					require.NoError(t, json.Unmarshal([]byte(`{"prompt_tokens":200,"completion_tokens":5,`+tt.fields+`}`), &chat))
					anthropic := chatUsageToAnthropicUsage(&chat)
					require.Equal(t, tt.read, anthropic.CacheReadInputTokens, "direct Chat-to-Messages read")
					require.Equal(t, tt.write, anthropic.CacheCreationInputTokens, "direct Chat-to-Messages write")
					usage = ChatUsageToResponsesUsage(&chat)
				}
				for hop := 0; hop < 3; hop++ {
					anthropic := anthropicUsageFromResponsesUsage(usage)
					require.Equal(t, tt.read, anthropic.CacheReadInputTokens, "hop %d read", hop)
					require.Equal(t, tt.write, anthropic.CacheCreationInputTokens, "hop %d write", hop)
					require.Equal(t, 200-tt.read-tt.write, anthropic.InputTokens, "hop %d uncached input", hop)
					wire, err := json.Marshal(chatUsageFromResponsesUsage(usage))
					require.NoError(t, err)
					var chat ChatUsage
					require.NoError(t, json.Unmarshal(wire, &chat))
					usage = ChatUsageToResponsesUsage(&chat)
					wire, err = json.Marshal(usage)
					require.NoError(t, err)
					require.NoError(t, json.Unmarshal(wire, &usage))
				}
			})
		}
	}
}

func TestResponsesCanceledTerminalPreservesCacheUsage(t *testing.T) {
	for _, terminal := range []string{"response.completed", "response.cancelled", "response.canceled"} {
		for _, location := range []string{"response", "event"} {
			t.Run(terminal+"/"+location, func(t *testing.T) {
				usage := `"usage":{"input_tokens":200,"output_tokens":5,"cache_read_input_tokens":128,"cache_write_tokens":32}`
				payload := `{"type":"` + terminal + `","response":{"id":"resp_cache",` + usage + `}}`
				if location == "event" {
					payload = `{"type":"` + terminal + `","response":{"id":"resp_cache"},` + usage + `}`
				}
				var event ResponsesStreamEvent
				require.NoError(t, json.Unmarshal([]byte(payload), &event))
				chatState := NewResponsesEventToChatState()
				chatState.IncludeUsage = true
				chunks := ResponsesEventToChatChunks(&event, chatState)
				chunks = append(chunks, FinalizeResponsesChatStream(chatState)...)
				var chatUsage *ChatUsage
				for _, chunk := range chunks {
					if chunk.Usage != nil {
						chatUsage = chunk.Usage
					}
				}
				require.NotNil(t, chatUsage, "terminal usage must reach Chat clients")
				require.Equal(t, 128, chatUsage.PromptTokensDetails.CachedTokens)
				require.Equal(t, 32, chatUsageToAnthropicUsage(chatUsage).CacheCreationInputTokens)
				anthState := NewResponsesEventToAnthropicState()
				anthState.MessageStartSent = true
				events := ResponsesEventToAnthropicEvents(&event, anthState)
				events = append(events, FinalizeResponsesAnthropicStream(anthState)...)
				var anthUsage *AnthropicUsage
				for _, output := range events {
					if output.Type == "message_delta" {
						anthUsage = output.Usage
					}
				}
				require.NotNil(t, anthUsage)
				require.Equal(t, 128, anthUsage.CacheReadInputTokens)
				require.Equal(t, 32, anthUsage.CacheCreationInputTokens)
				require.Equal(t, 40, anthUsage.InputTokens)
			})
		}
	}
}
