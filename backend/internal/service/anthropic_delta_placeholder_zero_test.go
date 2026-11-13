//go:build unit

package service

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

// issue #320: a relay re-serialized message_delta as
// {"input_tokens":0,"output_tokens":N}, i.e. a placeholder zero without any
// cache counters. The real input had already been reported by message_start
// together with the cache split, so the bare zero must not erase it: doing so
// drops the whole uncached-input bucket from usage logs and billing.
func TestAnthropicMessageDeltaPlaceholderZeroInputKeepsMessageStartInput(t *testing.T) {
	start := `{"type":"message_start","message":{"usage":{"input_tokens":47168,"output_tokens":1,"cache_creation_input_tokens":787,"cache_read_input_tokens":186900}}}`
	delta := `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":0,"output_tokens":2876}}`

	t.Run("passthrough", func(t *testing.T) {
		usage := &ClaudeUsage{}
		parseSSEUsagePassthrough(start, usage)
		parseSSEUsagePassthrough(delta, usage)

		require.Equal(t, 47168, usage.InputTokens, "a placeholder zero must not replace the reported input")
		require.Equal(t, 2876, usage.OutputTokens)
		require.Equal(t, 787, usage.CacheCreationInputTokens)
		require.Equal(t, 186900, usage.CacheReadInputTokens)
	})

	t.Run("general", func(t *testing.T) {
		svc := newMinimalGatewayService()
		usage := &ClaudeUsage{}
		svc.parseSSEUsage(start, usage)
		svc.parseSSEUsage(delta, usage)

		require.Equal(t, 47168, usage.InputTokens, "a placeholder zero must not replace the reported input")
		require.Equal(t, 2876, usage.OutputTokens)
		require.Equal(t, 787, usage.CacheCreationInputTokens)
		require.Equal(t, 186900, usage.CacheReadInputTokens)
	})
}

// Upstream issue #8407: when message_delta carries the final usage split, an
// explicit input_tokens=0 next to positive cache tokens is authoritative and
// must be kept, otherwise input and cache reads are billed twice.
func TestAnthropicMessageDeltaExplicitZeroInputWithCacheSplitIsAuthoritative(t *testing.T) {
	start := `{"type":"message_start","message":{"usage":{"input_tokens":8535,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}}`
	delta := `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":0,"output_tokens":33,"cache_creation_input_tokens":0,"cache_read_input_tokens":4004}}`

	t.Run("passthrough", func(t *testing.T) {
		usage := &ClaudeUsage{}
		parseSSEUsagePassthrough(start, usage)
		parseSSEUsagePassthrough(delta, usage)

		require.Zero(t, usage.InputTokens)
		require.Equal(t, 33, usage.OutputTokens)
		require.Equal(t, 4004, usage.CacheReadInputTokens)
	})

	t.Run("general", func(t *testing.T) {
		svc := newMinimalGatewayService()
		usage := &ClaudeUsage{}
		svc.parseSSEUsage(start, usage)
		svc.parseSSEUsage(delta, usage)

		require.Zero(t, usage.InputTokens)
		require.Equal(t, 33, usage.OutputTokens)
		require.Equal(t, 4004, usage.CacheReadInputTokens)
	})
}

// The converted chat/responses paths merge through mergeAnthropicUsage; they
// must apply the same rule so a stream bills identically whichever endpoint a
// client used.
func TestMergeAnthropicUsagePlaceholderZeroInputKeepsEarlierInput(t *testing.T) {
	var placeholder apicompat.AnthropicUsage
	require.NoError(t, json.Unmarshal([]byte(`{"input_tokens":0,"output_tokens":2876}`), &placeholder))

	usage := ClaudeUsage{InputTokens: 47168, OutputTokens: 1, CacheCreationInputTokens: 787, CacheReadInputTokens: 186900}
	mergeAnthropicUsage(&usage, placeholder)

	require.Equal(t, 47168, usage.InputTokens)
	require.Equal(t, 2876, usage.OutputTokens)
	require.Equal(t, 787, usage.CacheCreationInputTokens)
	require.Equal(t, 186900, usage.CacheReadInputTokens)

	var cumulative apicompat.AnthropicUsage
	require.NoError(t, json.Unmarshal([]byte(`{"input_tokens":0,"output_tokens":33,"cache_creation_input_tokens":0,"cache_read_input_tokens":4004}`), &cumulative))
	mergeAnthropicUsage(&usage, cumulative)

	require.Zero(t, usage.InputTokens, "a delta that restates the cache split stays authoritative")
	require.Equal(t, 4004, usage.CacheReadInputTokens)
}
