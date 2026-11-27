//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// legacyIncompleteInputCacheReadPercent and its wrappers stay in unit tests
// (golangci-lint runs without -tags=unit) for historical 95%-split comparisons.
const legacyIncompleteInputCacheReadPercent = 95

func splitIncompleteInput(total int) incompleteInputEstimate {
	return splitIncompleteInputWithPercent(total, legacyIncompleteInputCacheReadPercent)
}

func incompleteCacheReadTokens(total int) int {
	return splitIncompleteInput(total).CacheRead
}

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

// withOpenAIIncompleteInputCacheReadPercent sets the OpenAI-family estimate policy
// for one test and restores it afterwards.
func withOpenAIIncompleteInputCacheReadPercent(t *testing.T, percent int) {
	t.Helper()
	previous := OpenAIIncompleteInputCacheReadPercent()
	SetOpenAIIncompleteInputCacheReadPercent(percent)
	t.Cleanup(func() { SetOpenAIIncompleteInputCacheReadPercent(previous) })
}

// withAnthropicIncompleteInputCacheReadPercent sets the Anthropic-family estimate policy
// for one test and restores it afterwards.
func withAnthropicIncompleteInputCacheReadPercent(t *testing.T, percent int) {
	t.Helper()
	previous := AnthropicIncompleteInputCacheReadPercent()
	SetAnthropicIncompleteInputCacheReadPercent(percent)
	t.Cleanup(func() { SetAnthropicIncompleteInputCacheReadPercent(previous) })
}

// withGeminiIncompleteInputCacheReadPercent sets the Gemini/Antigravity-family estimate policy
// for one test and restores it afterwards.
func withGeminiIncompleteInputCacheReadPercent(t *testing.T, percent int) {
	t.Helper()
	previous := GeminiIncompleteInputCacheReadPercent()
	SetGeminiIncompleteInputCacheReadPercent(percent)
	t.Cleanup(func() { SetGeminiIncompleteInputCacheReadPercent(previous) })
}

// An OpenAI-family estimate bills the whole input at the plain input price by
// default, matching new-api's abnormal-end fallback (issue #456); the share is
// configurable and clamped.
func TestOpenAIIncompleteInputCacheReadPolicy(t *testing.T) {
	require.Zero(t, OpenAIIncompleteInputCacheReadPercent(), "no cache discount is invented by default")
	require.Equal(t, incompleteInputEstimate{Total: 100, CacheRead: 0, Fresh: 100}, splitOpenAIIncompleteInput(100))
	require.Zero(t, openAIIncompleteCacheReadTokens(100))

	withOpenAIIncompleteInputCacheReadPercent(t, 95)
	require.Equal(t, 95, OpenAIIncompleteInputCacheReadPercent())
	require.Equal(t, incompleteInputEstimate{Total: 100, CacheRead: 95, Fresh: 5}, splitOpenAIIncompleteInput(100))
	require.Equal(t, 950, openAIIncompleteCacheReadTokens(1000))

	SetOpenAIIncompleteInputCacheReadPercent(-7)
	require.Equal(t, 0, OpenAIIncompleteInputCacheReadPercent(), "out-of-range values are clamped")
	SetOpenAIIncompleteInputCacheReadPercent(300)
	require.Equal(t, 100, OpenAIIncompleteInputCacheReadPercent(), "out-of-range values are clamped")
	SetOpenAIIncompleteInputCacheReadPercent(openAIIncompleteInputCacheReadPercentDefault)

	// Anthropic and Gemini families also default to 0% (plain input price) and are independently configurable.
	require.Zero(t, AnthropicIncompleteInputCacheReadPercent())
	require.Zero(t, GeminiIncompleteInputCacheReadPercent())
	require.Equal(t, incompleteInputEstimate{Total: 100, CacheRead: 0, Fresh: 100}, splitAnthropicIncompleteInput(100))
	require.Equal(t, incompleteInputEstimate{Total: 100, CacheRead: 0, Fresh: 100}, splitGeminiIncompleteInput(100))

	withAnthropicIncompleteInputCacheReadPercent(t, 80)
	require.Equal(t, incompleteInputEstimate{Total: 100, CacheRead: 80, Fresh: 20}, splitAnthropicIncompleteInput(100))
	require.Zero(t, splitGeminiIncompleteInput(100).CacheRead)

	withGeminiIncompleteInputCacheReadPercent(t, 95)
	require.Equal(t, incompleteInputEstimate{Total: 100, CacheRead: 95, Fresh: 5}, splitGeminiIncompleteInput(100))
	require.Equal(t, 950, geminiIncompleteCacheReadTokens(1000))

	SetAnthropicIncompleteInputCacheReadPercent(-1)
	require.Equal(t, 0, AnthropicIncompleteInputCacheReadPercent())
	SetAnthropicIncompleteInputCacheReadPercent(120)
	require.Equal(t, 100, AnthropicIncompleteInputCacheReadPercent())
	SetGeminiIncompleteInputCacheReadPercent(-5)
	require.Equal(t, 0, GeminiIncompleteInputCacheReadPercent())
	SetGeminiIncompleteInputCacheReadPercent(200)
	require.Equal(t, 100, GeminiIncompleteInputCacheReadPercent())

	// Legacy helper remains fixed at 95% for historical comparison tests.
	require.Equal(t, 95, splitIncompleteInput(100).CacheRead)
}
