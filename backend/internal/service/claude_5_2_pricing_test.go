//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Fable/Mythos 5.2 的官方价是 $8/$40 per MTok。若不精确拦截，模型名会命中
// "fable-5"/"mythos-5" 前缀回退到 5.0/5.1 的 $10/$50，用户每百万输入多付 $2。
func TestFallbackPricingForClaude52Models(t *testing.T) {
	svc := &BillingService{fallbackPrices: make(map[string]*ModelPricing)}
	svc.initFallbackPricing()

	for _, model := range []string{
		"claude-fable-5-2",
		"claude-mythos-5-2",
		"anthropic/claude-fable-5-2",
		"claude-mythos-5-2-thinking",
		"us.anthropic.claude-fable-5-2",
	} {
		p := svc.getFallbackPricing(model)
		require.NotNil(t, p, model)
		require.InDelta(t, 8e-6, p.InputPricePerToken, 1e-12, model)
		require.InDelta(t, 40e-6, p.OutputPricePerToken, 1e-12, model)
		require.InDelta(t, 10e-6, p.CacheCreation5mPrice, 1e-12, model)
		require.InDelta(t, 16e-6, p.CacheCreation1hPrice, 1e-12, model)
		require.InDelta(t, 0.2e-6, p.CacheReadPricePerToken, 1e-12, model)
	}
}

// 5.0 / 5.1 不能被 5.2 分支带走：价格和缓存读价必须保持原值。
func TestFallbackPricingKeepsOlderFableVersions(t *testing.T) {
	svc := &BillingService{fallbackPrices: make(map[string]*ModelPricing)}
	svc.initFallbackPricing()

	base := svc.getFallbackPricing("claude-fable-5")
	require.NotNil(t, base)
	require.InDelta(t, 10e-6, base.InputPricePerToken, 1e-12)
	require.InDelta(t, 1e-6, base.CacheReadPricePerToken, 1e-12)

	v51 := svc.getFallbackPricing("claude-fable-5-1")
	require.NotNil(t, v51)
	require.InDelta(t, 0.25e-6, v51.CacheReadPricePerToken, 1e-12)
}
