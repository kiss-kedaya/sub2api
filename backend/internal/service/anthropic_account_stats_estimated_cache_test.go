//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// The user's bill and the account cost statistic must price an estimated cache read
// the same way: a model without a cache read price bills that share at the input
// price (UsageTokens.CacheReadEstimated). The statistic used to drop the mark and
// count the estimated share as free.
func TestGatewayServiceRecordUsage_AccountStatsKeepsTheEstimatedCacheReadMark(t *testing.T) {
	for _, tc := range []struct {
		name      string
		estimated bool
		wantTotal float64
	}{
		{"estimated cache read is priced as input", true, 1000 * 2e-6},
		{"reported cache read without a price stays free", false, 50 * 2e-6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
			svc.billingService.fallbackPrices["claude-sonnet-4"] = &ModelPricing{InputPricePerToken: 2e-6, OutputPricePerToken: 10e-6}
			groupID := int64(906)
			svc.channelService = newTestChannelServiceForStats(t, &Channel{ID: 1, Status: StatusActive}, groupID, PlatformAnthropic)

			err := svc.RecordUsage(context.Background(), &RecordUsageInput{
				Result: &ForwardResult{
					RequestID: "gateway_account_stats_estimated_cache",
					Model:     "claude-sonnet-4",
					Usage:     ClaudeUsage{InputTokens: 50, CacheReadInputTokens: 950, CacheReadEstimated: tc.estimated},
				},
				APIKey:  &APIKey{ID: 806, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformAnthropic, RateMultiplier: 1}},
				User:    &User{ID: 606},
				Account: &Account{ID: 706, Platform: PlatformAnthropic},
			})

			require.NoError(t, err)
			require.NotNil(t, usageRepo.lastLog)
			require.InDelta(t, tc.wantTotal, usageRepo.lastLog.TotalCost, 1e-12, "the user's bill")
			require.NotNil(t, usageRepo.lastLog.AccountStatsCost)
			require.InDelta(t, tc.wantTotal, *usageRepo.lastLog.AccountStatsCost, 1e-12, "the account statistic prices it the same way")
		})
	}
}
