//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 密钥强制 1h 时请求体一定带 ttl=1h，但上游可能不给 ephemeral 明细。
// 这种情况下按 5m 收就是净亏，必须按我们声明的 1h 单价收。
func TestComputeCacheCreationCostHonours1hHintWithoutUpstreamBreakdown(t *testing.T) {
	svc := &BillingService{}
	pricing := &ModelPricing{
		SupportsCacheBreakdown: true,
		CacheCreation5mPrice:   3.75e-6,
		CacheCreation1hPrice:   6e-6,
	}
	tokens := UsageTokens{CacheCreationTokens: 1122}

	fiveMin := svc.computeCacheCreationCostWithCacheTTL(pricing, tokens, 0, 1.0, "")
	oneHour := svc.computeCacheCreationCostWithCacheTTL(pricing, tokens, 0, 1.0, cacheTTLTarget1h)

	require.InDelta(t, 1122*3.75e-6, fiveMin, 1e-12, "无提示时保持按 5m 兜底")
	require.InDelta(t, 1122*6e-6, oneHour, 1e-12, "声明 1h 时必须按 1h 单价收")
	require.Greater(t, oneHour, fiveMin, "1h 单价高于 5m，修前会少收")
}

// 声明 5m 或空值时不能顺手涨价。
func TestComputeCacheCreationCostKeeps5mWhenHintIsNotOneHour(t *testing.T) {
	svc := &BillingService{}
	pricing := &ModelPricing{
		SupportsCacheBreakdown: true,
		CacheCreation5mPrice:   3.75e-6,
		CacheCreation1hPrice:   6e-6,
	}
	tokens := UsageTokens{CacheCreationTokens: 500}

	for _, hint := range []string{"", cacheTTLTarget5m} {
		got := svc.computeCacheCreationCostWithCacheTTL(pricing, tokens, 0, 1.0, hint)
		require.InDelta(t, 500*3.75e-6, got, 1e-12, "hint=%q 应保持 5m", hint)
	}
}

// 上游给了 ephemeral 明细时，明细是权威，hint 不得覆盖它。
func TestComputeCacheCreationCostPrefersUpstreamBreakdownOverHint(t *testing.T) {
	svc := &BillingService{}
	pricing := &ModelPricing{
		SupportsCacheBreakdown: true,
		CacheCreation5mPrice:   3.75e-6,
		CacheCreation1hPrice:   6e-6,
	}
	tokens := UsageTokens{
		CacheCreationTokens:   600,
		CacheCreation5mTokens: 100,
		CacheCreation1hTokens: 500,
	}

	got := svc.computeCacheCreationCostWithCacheTTL(pricing, tokens, 0, 1.0, cacheTTLTarget1h)

	require.InDelta(t, 100*3.75e-6+500*6e-6, got, 1e-12)
}

// 没有 1h 单价时不能把 1h 提示变成 0 或 5m 以外的值。
func TestComputeCacheCreationCostFallsBackTo5mWhenNo1hPrice(t *testing.T) {
	svc := &BillingService{}
	pricing := &ModelPricing{
		SupportsCacheBreakdown: true,
		CacheCreation5mPrice:   3.75e-6,
	}
	tokens := UsageTokens{CacheCreationTokens: 200}

	got := svc.computeCacheCreationCostWithCacheTTL(pricing, tokens, 0, 1.0, cacheTTLTarget1h)

	require.InDelta(t, 200*3.75e-6, got, 1e-12)
}

// 密钥模式决定计费提示：只有强制 1h 才给提示。
func TestCacheCreationTTLHintForAPIKey(t *testing.T) {
	require.Equal(t, "", cacheCreationTTLHintForAPIKey(nil))
	require.Equal(t, "", cacheCreationTTLHintForAPIKey(&APIKey{AnthropicCacheTTLMode: AnthropicCacheTTLModeInherit}))
	require.Equal(t, "", cacheCreationTTLHintForAPIKey(&APIKey{AnthropicCacheTTLMode: AnthropicCacheTTLMode5m}))
	require.Equal(t, "", cacheCreationTTLHintForAPIKey(&APIKey{AnthropicCacheTTLMode: AnthropicCacheTTLModeOff}))
	require.Equal(t, cacheTTLTarget1h, cacheCreationTTLHintForAPIKey(&APIKey{AnthropicCacheTTLMode: AnthropicCacheTTLMode1h}))
}
