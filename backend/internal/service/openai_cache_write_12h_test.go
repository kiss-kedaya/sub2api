//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// OpenAI 原生 30m/12h 缓存写入：30m 桶按通用写入价，12h 桶按 12h 单价。
func TestOpenAICacheWrite12hBilledAtConfiguredPrice(t *testing.T) {
	svc := &BillingService{}
	pricing := &ModelPricing{
		InputPricePerToken:            1e-6,
		CacheCreationPricePerToken:    1.25e-6,
		CacheCreation12hPricePerToken: testPtrFloat64(2.5e-6),
	}
	tokens := UsageTokens{CacheCreationTokens: 2400, CacheCreation30mTokens: 10, CacheCreation12hTokens: 2390}

	cost := svc.computeCacheCreationCostWithCacheTTL(pricing, tokens, pricing.CacheCreationPricePerToken, 1, "")
	require.InDelta(t, 10*1.25e-6+2390*2.5e-6, cost, 1e-12)

	// 走完整 token breakdown 也必须是同一口径，不能把聚合值再算一遍。
	breakdown := svc.computeTokenBreakdown(pricing, tokens, 1, "", false)
	require.InDelta(t, 10*1.25e-6+2390*2.5e-6, breakdown.CacheCreationCost, 1e-12)
}

// 12h 价未配置时沿用通用写入价（`ok=false` 回退路径）。
func TestOpenAICacheWrite12hUnconfiguredFallsBackToGenericPrice(t *testing.T) {
	svc := &BillingService{}
	pricing := &ModelPricing{
		InputPricePerToken:         1e-6,
		CacheCreationPricePerToken: 1.25e-6,
	}
	tokens := UsageTokens{CacheCreationTokens: 2400, CacheCreation30mTokens: 10, CacheCreation12hTokens: 2390}

	cost := svc.computeCacheCreationCostWithCacheTTL(pricing, tokens, pricing.CacheCreationPricePerToken, 1, "")
	require.InDelta(t, 2400*1.25e-6, cost, 1e-12)
}

// GPT-5.6 / GPT-6 系列目录价兜底：未配置 12h 价时默认输入价 2.5 倍。
func TestOpenAICacheWrite12hCatalogFallback(t *testing.T) {
	svc := NewBillingService(nil, nil)
	for _, model := range []string{"gpt-6-luna", "gpt-6-sol", "gpt-6.1-sol", "gpt-5.6-luna", "gpt-6-astra"} {
		t.Run(model, func(t *testing.T) {
			pricing, err := svc.GetModelPricing(model)
			require.NoError(t, err)
			require.NotNil(t, pricing.CacheCreation12hPricePerToken, "GPT-5.6/GPT-6 目录价应带 12h 默认价")
			require.InDelta(t, pricing.InputPricePerToken*2.5, *pricing.CacheCreation12hPricePerToken, 1e-15)
		})
	}
}

// 非 GPT-5.6/GPT-6 模型未配置 12h 价时保持通用写入价，不引入 2.5 倍溢价。
func TestOpenAICacheWrite12hNonPremiumCatalogKeepsGenericPrice(t *testing.T) {
	svc := NewBillingService(nil, nil)
	pricing := svc.applyModelSpecificPricingPolicy("gpt-5.4-mini", &ModelPricing{
		InputPricePerToken:         7.5e-7,
		CacheCreationPricePerToken: 1e-6,
	})
	require.Nil(t, pricing.CacheCreation12hPricePerToken)

	tokens := UsageTokens{CacheCreationTokens: 100, CacheCreation30mTokens: 10, CacheCreation12hTokens: 90}
	cost := svc.computeCacheCreationCostWithCacheTTL(pricing, tokens, pricing.CacheCreationPricePerToken, 1, "")
	require.InDelta(t, 100*1e-6, cost, 1e-12)
}

// 上游明细缺失/矛盾时不猜 12h，回退聚合值的既有单价。
func TestOpenAICacheWrite12hInvalidBreakdownKeepsAggregate(t *testing.T) {
	svc := &BillingService{}
	pricing := &ModelPricing{
		CacheCreationPricePerToken:    1e-6,
		CacheCreation12hPricePerToken: testPtrFloat64(3e-6),
	}
	for _, tokens := range []UsageTokens{
		{CacheCreationTokens: 100},                                        // 无明细
		{CacheCreationTokens: 100, CacheCreation30mTokens: 10},            // 两桶之和不等于聚合
		{CacheCreationTokens: 100, CacheCreation12hTokens: 50},            // 同上（另一桶缺失）
		{CacheCreationTokens: 100, CacheCreation30mTokens: 10, CacheCreation12hTokens: 80}, // 和多于/少于
	} {
		cost := svc.computeCacheCreationCostWithCacheTTL(pricing, tokens, pricing.CacheCreationPricePerToken, 1, "")
		require.InDelta(t, float64(tokens.CacheCreationTokens)*1e-6, cost, 1e-12)
	}
}

// 12h 价存在时，Anthropic 的 5m/1h 分档与 TTL 兜底逻辑保持不变。
func TestOpenAICacheWrite12hDoesNotAffectAnthropicTTLBuckets(t *testing.T) {
	svc := &BillingService{}
	pricing := &ModelPricing{
		SupportsCacheBreakdown:        true,
		CacheCreation5mPrice:          2e-6,
		CacheCreation1hPrice:          4e-6,
		CacheCreation12hPricePerToken: testPtrFloat64(8e-6),
	}

	split := UsageTokens{CacheCreationTokens: 100, CacheCreation5mTokens: 40, CacheCreation1hTokens: 60}
	require.InDelta(t, 40*2e-6+60*4e-6,
		svc.computeCacheCreationCostWithCacheTTL(pricing, split, 0, 1, ""), 1e-12)

	// 上游漏报 ephemeral 明细时仍按声明的 1h TTL 收（已有兜底修复）。
	hintOnly := UsageTokens{CacheCreationTokens: 100}
	require.InDelta(t, 100*4e-6,
		svc.computeCacheCreationCostWithCacheTTL(pricing, hintOnly, 0, 1, cacheTTLTarget1h), 1e-12)
	// 未声明 1h 时仍按 5m 兜底。
	require.InDelta(t, 100*2e-6,
		svc.computeCacheCreationCostWithCacheTTL(pricing, hintOnly, 0, 1, ""), 1e-12)
}

// priority/fast 档：12h 单价跟随通用写入价同比例缩放。
func TestOpenAICacheWrite12hScalesWithPriorityTier(t *testing.T) {
	svc := &BillingService{}
	pricing := &ModelPricing{
		InputPricePerToken:                 1e-6,
		InputPricePerTokenPriority:         2e-6,
		CacheCreationPricePerToken:         1.25e-6,
		CacheCreationPricePerTokenPriority: 2.5e-6,
		CacheCreation12hPricePerToken:      testPtrFloat64(2.5e-6),
	}
	tokens := UsageTokens{CacheCreationTokens: 2400, CacheCreation30mTokens: 10, CacheCreation12hTokens: 2390}

	breakdown := svc.computeTokenBreakdown(pricing, tokens, 1, "fast", false)
	require.InDelta(t, 10*2.5e-6+2390*5e-6, breakdown.CacheCreationCost, 1e-12)
	// 不改动共享定价对象。
	require.InDelta(t, 2.5e-6, *pricing.CacheCreation12hPricePerToken, 1e-15)
}

// 渠道价卡：配了 12h 用 12h；只配通用写入价时 12h 保持平价。
func TestOpenAICacheWrite12hChannelPriceOverrides(t *testing.T) {
	svc := &BillingService{}
	tokens := UsageTokens{CacheCreationTokens: 2400, CacheCreation30mTokens: 10, CacheCreation12hTokens: 2390}

	with12h := &ModelPricing{InputPricePerToken: 1e-6, CacheCreationPricePerToken: 1.25e-6}
	applyChannelTokenPriceOverrides(with12h, &ChannelModelPricing{
		CacheWritePrice:    testPtrFloat64(1e-6),
		CacheWrite12hPrice: testPtrFloat64(2e-6),
	})
	require.True(t, with12h.CacheCreation12hExplicit)
	require.InDelta(t, 10*1e-6+2390*2e-6,
		svc.computeCacheCreationCostWithCacheTTL(with12h, tokens, with12h.CacheCreationPricePerToken, 1, ""), 1e-12)

	flat := &ModelPricing{InputPricePerToken: 1e-6, CacheCreationPricePerToken: 1.25e-6,
		CacheCreation12hPricePerToken: testPtrFloat64(2.5e-6)}
	applyChannelTokenPriceOverrides(flat, &ChannelModelPricing{CacheWritePrice: testPtrFloat64(1e-6)})
	require.Nil(t, flat.CacheCreation12hPricePerToken, "只配通用写入价时不应继承目录 12h 价")
	require.True(t, flat.CacheCreationWritePriceOperatorExplicit)
	require.InDelta(t, 2400*1e-6,
		svc.computeCacheCreationCostWithCacheTTL(flat, tokens, flat.CacheCreationPricePerToken, 1, ""), 1e-12)

	// 显式 12h 为 0（免费）也要保留显式语义。
	free := &ModelPricing{InputPricePerToken: 1e-6, CacheCreationPricePerToken: 1.25e-6}
	applyChannelTokenPriceOverrides(free, &ChannelModelPricing{
		CacheWritePrice:    testPtrFloat64(1e-6),
		CacheWrite12hPrice: testPtrFloat64(0),
	})
	require.NotNil(t, free.CacheCreation12hPricePerToken)
	require.InDelta(t, 10*1e-6,
		svc.computeCacheCreationCostWithCacheTTL(free, tokens, free.CacheCreationPricePerToken, 1, ""), 1e-12)
}

// 账户统计口径（account stats）也要按 12h 价计。
func TestOpenAICacheWrite12hAccountStatsCost(t *testing.T) {
	tokens := UsageTokens{CacheCreationTokens: 2400, CacheCreation30mTokens: 10, CacheCreation12hTokens: 2390}
	cost := calculateTokenStatsCost(&ChannelModelPricing{
		BillingMode:        BillingModeToken,
		CacheWritePrice:    testPtrFloat64(1e-6),
		CacheWrite12hPrice: testPtrFloat64(2e-6),
	}, tokens)
	require.NotNil(t, cost)
	require.InDelta(t, 10*1e-6+2390*2e-6, *cost, 1e-12)
}

// 上游 usage 解析：cache_write_tokens_details 的 30m/12h 明细。
func TestOpenAIUsageParsesCacheWriteTTLDetails(t *testing.T) {
	usage, ok := openAIUsageFromGJSON(gjson.Parse(`{"input_tokens":10,"input_tokens_details":{"cache_write_tokens":10,"cache_write_tokens_details":{"ttl_30m_tokens":3,"ttl_12h_tokens":7}}}`))
	require.True(t, ok)
	require.Equal(t, 10, usage.CacheCreationInputTokens)
	require.Equal(t, 3, usage.CacheCreation30mInputTokens)
	require.Equal(t, 7, usage.CacheCreation12hInputTokens)
	require.True(t, usage.cacheCreationTTLDetailsPresent)

	// prompt_tokens_details 兼容路径（chat completions 风格）。
	usage, ok = openAIUsageFromGJSON(gjson.Parse(`{"prompt_tokens":20,"prompt_tokens_details":{"cache_write_tokens":20,"cache_write_tokens_details":{"ttl_30m_tokens":20,"ttl_12h_tokens":0}}}`))
	require.True(t, ok)
	require.Equal(t, 20, usage.CacheCreation30mInputTokens)
	require.Zero(t, usage.CacheCreation12hInputTokens)
}

// 明细缺失、部分或矛盾时保留聚合值，不伪造 12h 写入。
func TestOpenAIUsageInvalidCacheWriteTTLDetailsKeepAggregate(t *testing.T) {
	for _, details := range []string{
		`null`,
		`{}`,
		`{"ttl_12h_tokens":10}`,
		`{"ttl_30m_tokens":0,"ttl_12h_tokens":9}`,
		`{"ttl_30m_tokens":9,"ttl_12h_tokens":9}`,
		`{"ttl_30m_tokens":-1,"ttl_12h_tokens":11}`,
		`{"ttl_30m_tokens":0.5,"ttl_12h_tokens":9.5}`,
		`{"ttl_30m_tokens":"0","ttl_12h_tokens":"10"}`,
	} {
		t.Run(details, func(t *testing.T) {
			usage, ok := openAIUsageFromGJSON(gjson.Parse(`{"input_tokens":10,"input_tokens_details":{"cache_write_tokens":10,"cache_write_tokens_details":` + details + `}}`))
			require.True(t, ok)
			require.Equal(t, 10, usage.CacheCreationInputTokens)
			require.Zero(t, usage.CacheCreation30mInputTokens)
			require.Zero(t, usage.CacheCreation12hInputTokens)
			require.False(t, usage.cacheCreationTTLDetailsPresent)
		})
	}
}
