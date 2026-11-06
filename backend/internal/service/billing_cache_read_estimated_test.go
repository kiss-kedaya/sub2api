//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A cache read share that was estimated (UsageTokens.CacheReadEstimated) bills at the
// plain input price when the model has no cache read price. A reported cache read
// never takes this path.
func TestComputeTokenBreakdown_EstimatedCacheReadFallsBackToInputPrice(t *testing.T) {
	svc := newTestBillingService()
	noCachePrice := &ModelPricing{InputPricePerToken: 2e-6, OutputPricePerToken: 10e-6}

	t.Run("estimated cache read without a cache price bills as input", func(t *testing.T) {
		cost := svc.computeTokenBreakdown(noCachePrice,
			UsageTokens{InputTokens: 50, CacheReadTokens: 950, CacheReadEstimated: true}, 1.0, "", false)
		require.InDelta(t, 50*2e-6, cost.InputCost, 1e-15)
		require.InDelta(t, 950*2e-6, cost.CacheReadCost, 1e-15, "the cache share bills at the input price")
		require.InDelta(t, 1000*2e-6, cost.TotalCost, 1e-15, "the whole estimated input bills as plain input")
	})
	t.Run("a reported cache read without a cache price stays unchanged", func(t *testing.T) {
		cost := svc.computeTokenBreakdown(noCachePrice,
			UsageTokens{InputTokens: 50, CacheReadTokens: 950}, 1.0, "", false)
		require.InDelta(t, 50*2e-6, cost.TotalCost, 1e-15)
		require.Zero(t, cost.CacheReadCost)
	})
	t.Run("a real cache price is used as before", func(t *testing.T) {
		priced := &ModelPricing{InputPricePerToken: 2e-6, OutputPricePerToken: 10e-6, CacheReadPricePerToken: 0.2e-6}
		cost := svc.computeTokenBreakdown(priced,
			UsageTokens{InputTokens: 50, CacheReadTokens: 950, CacheReadEstimated: true}, 1.0, "", false)
		require.InDelta(t, 950*0.2e-6, cost.CacheReadCost, 1e-15)
		require.InDelta(t, 50*2e-6+950*0.2e-6, cost.TotalCost, 1e-15)
	})
	t.Run("the estimated mark with no cache tokens changes nothing", func(t *testing.T) {
		cost := svc.computeTokenBreakdown(noCachePrice,
			UsageTokens{InputTokens: 100, OutputTokens: 10, CacheReadEstimated: true}, 1.0, "", false)
		require.InDelta(t, 100*2e-6+10*10e-6, cost.TotalCost, 1e-15)
		require.Zero(t, cost.CacheReadCost)
	})
	t.Run("the priority tier input price is the fallback", func(t *testing.T) {
		priority := &ModelPricing{
			InputPricePerToken: 2e-6, InputPricePerTokenPriority: 4e-6,
			OutputPricePerToken: 10e-6, OutputPricePerTokenPriority: 20e-6,
		}
		cost := svc.computeTokenBreakdown(priority,
			UsageTokens{InputTokens: 50, CacheReadTokens: 950, CacheReadEstimated: true}, 1.0, "priority", false)
		require.InDelta(t, 50*4e-6, cost.InputCost, 1e-15)
		require.InDelta(t, 950*4e-6, cost.CacheReadCost, 1e-15)
	})
	t.Run("the long context multiplier scales both prices the same way", func(t *testing.T) {
		long := &ModelPricing{
			InputPricePerToken: 2e-6, OutputPricePerToken: 10e-6,
			LongContextInputThreshold: 1000, LongContextInputMultiplier: 2, LongContextOutputMultiplier: 1.5,
		}
		cost := svc.computeTokenBreakdown(long,
			UsageTokens{InputTokens: 100, CacheReadTokens: 1900, CacheReadEstimated: true}, 1.0, "", true)
		require.InDelta(t, 100*2e-6*2, cost.InputCost, 1e-15)
		require.InDelta(t, 1900*2e-6*2, cost.CacheReadCost, 1e-15)
	})
	t.Run("the rate multiplier applies to the fallback cost", func(t *testing.T) {
		cost := svc.computeTokenBreakdown(noCachePrice,
			UsageTokens{InputTokens: 50, CacheReadTokens: 950, CacheReadEstimated: true}, 0.5, "", false)
		require.InDelta(t, 1000*2e-6, cost.TotalCost, 1e-15)
		require.InDelta(t, 1000*2e-6*0.5, cost.ActualCost, 1e-15)
	})
}

// The mark travels from the forward result through RecordUsage into the cost.
func TestOpenAIGatewayServiceRecordUsage_EstimatedCacheReadWithoutCachePriceBillsAsInput(t *testing.T) {
	for _, tc := range []struct {
		name      string
		estimated bool
		wantTotal float64
	}{
		{"estimated cache read bills as input", true, 1000 * 2e-6},
		{"reported cache read without a price stays free", false, 50 * 2e-6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(
				usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil,
			)
			svc.billingService.fallbackPrices["gpt-5.4"] = &ModelPricing{InputPricePerToken: 2e-6, OutputPricePerToken: 10e-6}

			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{
					RequestID: "resp_estimated_cache",
					Model:     "gpt-5.4",
					Duration:  time.Second,
					Usage: OpenAIUsage{
						InputTokens: 1000, CacheReadInputTokens: 950, CacheReadEstimated: tc.estimated,
					},
				},
				APIKey:  &APIKey{ID: 1000, Group: &Group{RateMultiplier: 1}},
				User:    &User{ID: 2000},
				Account: &Account{ID: 3000, Type: AccountTypeAPIKey},
			})

			require.NoError(t, err)
			require.NotNil(t, usageRepo.lastLog)
			require.Equal(t, 950, usageRepo.lastLog.CacheReadTokens)
			require.InDelta(t, tc.wantTotal, usageRepo.lastLog.TotalCost, 1e-12)
		})
	}
}
