//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitIncompleteInput(t *testing.T) {
	for _, tc := range []struct{ total, cacheRead, fresh int }{
		{-5, 0, 0}, {0, 0, 0}, {1, 0, 1}, {19, 18, 1}, {20, 19, 1}, {100, 95, 5}, {1000, 950, 50}, {123456, 117283, 6173},
	} {
		got := splitIncompleteInput(tc.total)
		if tc.total <= 0 {
			require.Equal(t, incompleteInputEstimate{}, got, "total=%d", tc.total)
			continue
		}
		require.Equal(t, tc.total, got.Total, "total=%d", tc.total)
		require.Equal(t, tc.cacheRead, got.CacheRead, "total=%d", tc.total)
		require.Equal(t, tc.fresh, got.Fresh, "total=%d", tc.total)
		require.Equal(t, got.Total, got.CacheRead+got.Fresh, "the split must add up")
		require.Equal(t, tc.cacheRead, incompleteCacheReadTokens(tc.total))
	}
}

func TestEstimateRequestInputTokens_PicksTheEstimatorByRequestShape(t *testing.T) {
	text := "Write a long story about a fox who learns to code, with a twist at the end."
	responses := []byte(`{"model":"gpt-5.4","input":"` + text + `","stream":true}`)
	anthropic := []byte(`{"model":"claude-sonnet-4","messages":[{"role":"user","content":"` + text + `"}],"max_tokens":64}`)
	gemini := []byte(`{"contents":[{"role":"user","parts":[{"text":"` + text + `"}]}]}`)

	require.Greater(t, estimateRequestInputTokens(responses), 0)
	require.Greater(t, estimateRequestInputTokens(anthropic), 0)
	require.Equal(t, estimateTokensForText(text), estimateRequestInputTokens(gemini))

	for name, body := range map[string][]byte{
		"empty":         nil,
		"not json":      []byte("not json"),
		"unknown shape": []byte(`{"model":"x","prompt":"` + text + `"}`),
		"empty input":   []byte(`{"model":"x","input":""}`),
	} {
		require.Zero(t, estimateRequestInputTokens(body), name)
	}
}

func TestSplitIncompleteInput_OfARequestEstimate(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","input":"Summarise this paragraph about gateways and billing in two sentences."}`)
	got := splitIncompleteInput(estimateRequestInputTokens(body))
	require.Greater(t, got.Total, 0)
	require.Equal(t, got.Total*95/100, got.CacheRead)
	require.Equal(t, got.Total, got.CacheRead+got.Fresh)
	require.Equal(t, incompleteInputEstimate{}, splitIncompleteInput(estimateRequestInputTokens([]byte(`{"x":1}`))))
}

// The preauthorization reserve keeps its byte-length floor for shapes the shared
// estimator cannot read.
func TestEstimateBalancePreauthorizationInputTokens_KeepsTheByteFallback(t *testing.T) {
	unknown := []byte(`{"model":"x","prompt":"hello there"}`)
	require.Equal(t, len(unknown), estimateBalancePreauthorizationInputTokens(unknown))
	require.Zero(t, estimateBalancePreauthorizationInputTokens(nil))
	known := []byte(`{"model":"gpt-5.4","input":"hello there"}`)
	require.Equal(t, estimateRequestInputTokens(known), estimateBalancePreauthorizationInputTokens(known))
}

func TestAddOpenAIUsage_KeepsTheEstimatedCacheReadMark(t *testing.T) {
	dst := OpenAIUsage{InputTokens: 10, CacheReadInputTokens: 2}
	addOpenAIUsage(&dst, OpenAIUsage{InputTokens: 100, CacheReadInputTokens: 95, CacheReadEstimated: true})
	require.Equal(t, 110, dst.InputTokens)
	require.Equal(t, 97, dst.CacheReadInputTokens)
	require.True(t, dst.CacheReadEstimated, "a later estimated round must keep the mark")

	clean := OpenAIUsage{}
	addOpenAIUsage(&clean, OpenAIUsage{InputTokens: 5})
	require.False(t, clean.CacheReadEstimated)
}
