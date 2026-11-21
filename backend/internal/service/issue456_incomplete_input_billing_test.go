//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Issue #456: when an OpenAI-family stream ends without a usage report, the upstream
// new-api itself falls back to ResponseText2Usage, which bills the whole prompt
// (including cache hits) at the plain input price. The estimated input on our side
// must bill the same way, otherwise the operator absorbs the difference.
//
// Production sample (usage_logs id 407852431, 2026-11-22, gpt-6.2-astra):
//
//	input_tokens  = 7,039   at $8.00/M  -> $0.056312
//	cache_read    = 133,738 at $0.40/M  -> $0.0534952   (0.05x input)
//	output_tokens = 12      at $40.00/M -> $0.00048
//	sub2api billed total_cost = $0.1102872
//
// new-api's abnormal-end fallback bills 140,777 prompt tokens at the plain input
// price: 140,777 * 8e-6 = $1.126216 (+ output), i.e. about 10.2x more. The old 95%
// cache-read heuristic priced 95% of the estimate at 0.05x input:
// 0.95*0.05 + 0.05*1 = 0.0975x, which is exactly the observed ratio.
func TestIncompleteEstimateMatchesUpstreamAbnormalEndBilling(t *testing.T) {
	svc := newTestBillingService()
	inputPrice := 8e-6
	outputPrice := 40e-6
	cacheReadPrice := 0.4e-6
	pricing := &ModelPricing{
		InputPricePerToken:     inputPrice,
		OutputPricePerToken:    outputPrice,
		CacheReadPricePerToken: cacheReadPrice,
	}

	const (
		freshInput  = 7039
		cacheRead   = 133738
		outputToken = 12
	)
	totalInput := freshInput + cacheRead

	// Default policy: no invented cache discount, matching new-api's fallback.
	split := splitOpenAIIncompleteInput(totalInput)
	require.Zero(t, split.CacheRead)
	require.Equal(t, totalInput, split.Fresh)
	cost := svc.computeTokenBreakdown(pricing, UsageTokens{
		InputTokens:        split.Total,
		OutputTokens:       outputToken,
		CacheReadTokens:    split.CacheRead,
		CacheReadEstimated: true,
	}, 1.0, "", false)
	upstreamFallback := float64(totalInput)*inputPrice + float64(outputToken)*outputPrice
	require.InDelta(t, upstreamFallback, cost.TotalCost, 1e-12,
		"the estimate must bill the whole prompt at the plain input price")

	// The pre-#456 heuristic priced 95% as cache read and is what produced the gap.
	legacySplit := splitIncompleteInputWithPercent(totalInput, legacyIncompleteInputCacheReadPercent)
	legacyCost := svc.computeTokenBreakdown(pricing, UsageTokens{
		InputTokens:        legacySplit.Fresh,
		OutputTokens:       outputToken,
		CacheReadTokens:    legacySplit.CacheRead,
		CacheReadEstimated: true,
	}, 1.0, "", false)
	require.InDelta(t, 0.0975, legacyCost.TotalCost/upstreamFallback, 1e-3,
		"the legacy heuristic billed ~9.75% of the upstream fallback cost")
	require.InDelta(t, 10.25, upstreamFallback/legacyCost.TotalCost, 0.1,
		"which is the ~10x undercharge issue #456 reports")

	// The configured share restores the heuristic in a controlled way and the
	// estimated mark keeps a model without a cache price at the plain input price.
	withOpenAIIncompleteInputCacheReadPercent(t, 95)
	configured := splitOpenAIIncompleteInput(totalInput)
	require.Equal(t, legacySplit, configured)
	costWithMark := svc.computeTokenBreakdown(&ModelPricing{
		InputPricePerToken:  inputPrice,
		OutputPricePerToken: outputPrice,
	}, UsageTokens{
		InputTokens:        split.Total,
		OutputTokens:       outputToken,
		CacheReadTokens:    split.CacheRead,
		CacheReadEstimated: true,
	}, 1.0, "", false)
	require.InDelta(t, float64(totalInput)*inputPrice+float64(outputToken)*outputPrice, costWithMark.TotalCost, 1e-12,
		"a missing cache price still bills the estimated share at the input price")
}
