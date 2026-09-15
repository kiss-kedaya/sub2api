package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesCacheReadAliasesSurviveProtocolConversion(t *testing.T) {
	tests := []struct {
		name   string
		fields string
		cached int
	}{
		{"cache_read_input_tokens", `"cache_read_input_tokens":128`, 128},
		{"cache_read_tokens", `"cache_read_tokens":128`, 128},
		{"cached_tokens", `"cached_tokens":128`, 128},
		{"alias precedence", `"cache_read_input_tokens":128,"cache_read_tokens":64,"cached_tokens":32`, 128},
		{"skip nonpositive alias", `"cache_read_input_tokens":-1,"cache_read_tokens":128`, 128},
		{"preserve other details", `"input_tokens_details":{"audio_tokens":4},"cached_tokens":128`, 128},
		{"prompt details with input details", `"input_tokens_details":{"audio_tokens":4},"prompt_tokens_details":{"cached_tokens":128}`, 128},
		{"canonical zero", `"input_tokens_details":{"cached_tokens":0},"cached_tokens":128`, 0},
		{"canonical null", `"input_tokens_details":{"cached_tokens":null},"cached_tokens":128`, 0},
		{"canonical negative", `"input_tokens_details":{"cached_tokens":-1},"cached_tokens":128`, 0},
		{"canonical precedence", `"input_tokens_details":{"cached_tokens":128},"prompt_tokens_details":{"cached_tokens":64},"cached_tokens":32`, 128},
		{"prompt canonical zero", `"prompt_tokens_details":{"cached_tokens":0},"cache_read_input_tokens":128`, 0},
		{"absent", `"input_tokens_details":{"audio_tokens":4}`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var event ResponsesStreamEvent
			payload := `{"type":"response.completed","response":{"id":"resp_cache","status":"completed","usage":{"input_tokens":200,"output_tokens":5,` + tt.fields + `}}}`
			require.NoError(t, json.Unmarshal([]byte(payload), &event))
			chat := chatUsageFromResponsesUsage(event.Response.Usage)
			cached := 0
			if chat.PromptTokensDetails != nil {
				cached = chat.PromptTokensDetails.CachedTokens
			}
			require.Equal(t, tt.cached, cached, "downstream Chat cache count")
			require.Equal(t, 200, chat.PromptTokens)
			anthropic := anthropicUsageFromResponsesUsage(event.Response.Usage)
			require.Equal(t, tt.cached, anthropic.CacheReadInputTokens)
			require.Equal(t, 200-tt.cached, anthropic.InputTokens)
			state := NewResponsesEventToAnthropicState()
			events := ResponsesEventToAnthropicEvents(&event, state)
			found := false
			for _, output := range events {
				if output.Type == "message_delta" {
					found = true
					require.Equal(t, tt.cached, output.Usage.CacheReadInputTokens)
				}
			}
			require.True(t, found, "streaming usage must reach downstream before message_stop")
		})
	}
}
