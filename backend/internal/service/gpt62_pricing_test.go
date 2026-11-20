//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// GPT-6.2 Astra 官方价 $8/$40（priority 2x），缓存写 $10/$20、读 $0.4/$0.8；
// 6.2 Sol 与 6.1 Sol 同价（$2/$10）。
func TestFallbackPricingForGPT62Family(t *testing.T) {
	svc := &BillingService{fallbackPrices: make(map[string]*ModelPricing)}
	svc.initFallbackPricing()

	for _, model := range []string{"gpt-6.2-astra", "gpt-6.2-astra-high", "gpt-6.2-astra-openai-compact"} {
		p := svc.getFallbackPricing(model)
		require.NotNil(t, p, model)
		require.InDelta(t, 8e-6, p.InputPricePerToken, 1e-12, model)
		require.InDelta(t, 16e-6, p.InputPricePerTokenPriority, 1e-12, model)
		require.InDelta(t, 40e-6, p.OutputPricePerToken, 1e-12, model)
		require.InDelta(t, 80e-6, p.OutputPricePerTokenPriority, 1e-12, model)
		require.InDelta(t, 10e-6, p.CacheCreationPricePerToken, 1e-12, model)
		require.InDelta(t, 0.4e-6, p.CacheReadPricePerToken, 1e-12, model)
		require.Equal(t, 272_000, p.LongContextInputThreshold, model)
		require.True(t, p.CacheCreationPriceExplicit, model)
	}

	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-sol-medium", "gpt-6.2-sol-openai-compact"} {
		p := svc.getFallbackPricing(model)
		require.NotNil(t, p, model)
		require.InDelta(t, 2e-6, p.InputPricePerToken, 1e-12, model)
		require.InDelta(t, 10e-6, p.OutputPricePerToken, 1e-12, model)
		require.InDelta(t, 2.5e-6, p.CacheCreationPricePerToken, 1e-12, model)
	}
}

// 6.1 / 6.0 不能被 6.2 分支带走。
func TestFallbackPricingKeepsGPT61Family(t *testing.T) {
	svc := &BillingService{fallbackPrices: make(map[string]*ModelPricing)}
	svc.initFallbackPricing()

	p61 := svc.getFallbackPricing("gpt-6.1-sol")
	require.NotNil(t, p61)
	require.InDelta(t, 2e-6, p61.InputPricePerToken, 1e-12)

	pTerra := svc.getFallbackPricing("gpt-6.1-terra")
	require.NotNil(t, pTerra)
	require.InDelta(t, 1e-6, pTerra.InputPricePerToken, 1e-12)
}
