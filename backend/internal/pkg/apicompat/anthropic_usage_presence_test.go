package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnthropicUsagePresenceCumulativeMerge(t *testing.T) {
	var usage AnthropicUsage
	require.NoError(t, json.Unmarshal([]byte(`{"input_tokens":11,"output_tokens":4,"cache_creation_input_tokens":600,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":600}}`), &usage))
	var delta AnthropicUsage
	require.NoError(t, json.Unmarshal([]byte(`{"input_tokens":0,"output_tokens":8,"cache_creation_input_tokens":0,"cache_read_input_tokens":600}`), &delta))
	usage.Merge(delta)
	require.Zero(t, usage.InputTokens)
	require.Zero(t, usage.CacheCreationInputTokens)
	require.Nil(t, usage.CacheCreation)
	require.Equal(t, 600, usage.CacheReadInputTokens)
	require.Equal(t, 8, usage.OutputTokens)

	require.NoError(t, json.Unmarshal([]byte(`{"output_tokens":9}`), &delta))
	usage.Merge(delta)
	require.Equal(t, 600, usage.CacheReadInputTokens, "omitted fields must retain the last cumulative value")
	require.Zero(t, usage.CacheCreationInputTokens)
	require.Equal(t, 9, usage.OutputTokens)

	require.NoError(t, json.Unmarshal([]byte(`{"input_tokens":null,"cache_read_input_tokens":null}`), &delta))
	usage.Merge(delta)
	require.Equal(t, 600, usage.CacheReadInputTokens, "null is not an explicit numeric zero")
}

func TestAnthropicUsagePresenceStreamReplacesCacheWriteWithRead(t *testing.T) {
	state := NewAnthropicEventToResponsesState()
	for _, raw := range []string{
		`{"type":"message_start","message":{"id":"msg_cache","model":"claude-test","usage":{"input_tokens":11,"output_tokens":4,"cache_creation_input_tokens":600,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":600}}}}`,
		`{"type":"message_delta","usage":{"input_tokens":0,"output_tokens":8,"cache_creation_input_tokens":0,"cache_read_input_tokens":600},"delta":{"stop_reason":"end_turn"}}`,
		`{"type":"message_delta","usage":{"output_tokens":9}}`,
	} {
		var event AnthropicStreamEvent
		require.NoError(t, json.Unmarshal([]byte(raw), &event))
		AnthropicEventToResponsesEvents(&event, state)
	}
	events := AnthropicEventToResponsesEvents(&AnthropicStreamEvent{Type: "message_stop"}, state)
	require.Len(t, events, 1)
	usage := events[0].Response.Usage
	require.Equal(t, 600, usage.InputTokens)
	require.Zero(t, usage.CacheCreationInputTokens, "a final explicit zero must clear the initial cache write")
	require.Nil(t, usage.CacheCreation)
	require.Equal(t, 600, usage.InputTokensDetails.CachedTokens)
	require.Equal(t, 9, usage.OutputTokens)
	chat := chatUsageFromResponsesUsage(usage)
	require.Equal(t, 600, chat.PromptTokens)
	require.Zero(t, chat.PromptTokensDetails.CacheCreationTokens)
	require.Equal(t, 600, chat.PromptTokensDetails.CachedTokens)
}

func TestAnthropicUsagePresenceTTLDetailsRoundTrip(t *testing.T) {
	var usage AnthropicUsage
	require.NoError(t, json.Unmarshal([]byte(`{"input_tokens":11,"output_tokens":4,"cache_creation_input_tokens":600,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":500}}`), &usage))
	response := AnthropicToResponsesResponse(&AnthropicResponse{Usage: usage})
	chat := chatUsageFromResponsesUsage(response.Usage)
	raw, err := json.Marshal(chat)
	require.NoError(t, err)
	var decoded ChatUsage
	require.NoError(t, json.Unmarshal(raw, &decoded))
	result := anthropicUsageFromResponsesUsage(ChatUsageToResponsesUsage(&decoded))
	require.Equal(t, 11, result.InputTokens)
	require.Equal(t, 600, result.CacheCreationInputTokens)
	require.Equal(t, 100, *result.CacheCreation.Ephemeral5mInputTokens)
	require.Equal(t, 500, *result.CacheCreation.Ephemeral1hInputTokens)
}

func TestAnthropicUsagePresencePartialTTLAndSplitOnly(t *testing.T) {
	var usage, delta AnthropicUsage
	require.NoError(t, json.Unmarshal([]byte(`{"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":500}}`), &delta))
	usage.Merge(delta)
	require.Equal(t, 600, usage.CacheCreationInputTokens)
	require.NoError(t, json.Unmarshal([]byte(`{"cache_creation":{"ephemeral_5m_input_tokens":0}}`), &delta))
	usage.Merge(delta)
	require.Equal(t, 500, usage.CacheCreationInputTokens)
	require.Equal(t, 0, *usage.CacheCreation.Ephemeral5mInputTokens)
	require.Equal(t, 500, *usage.CacheCreation.Ephemeral1hInputTokens)
}
