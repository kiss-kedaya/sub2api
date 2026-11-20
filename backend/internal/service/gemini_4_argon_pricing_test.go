//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Gemini 4 Argon 优惠价 $2/$10/$0.1；Cyber 变体走 Vertex 价 $4/$20/$0.2。
// Antigravity 的 -high/-low/-medium 只是推理档位，必须归一到基础价。
func TestFallbackPricingForGemini4Argon(t *testing.T) {
	svc := &BillingService{fallbackPrices: make(map[string]*ModelPricing)}
	svc.initFallbackPricing()

	for _, model := range []string{
		"gemini-4-argon",
		"gemini-4-argon-high",
		"gemini-4-argon-low",
		"gemini-4-argon-medium",
		"google/gemini-4-argon",
	} {
		p := svc.getFallbackPricing(model)
		require.NotNil(t, p, model)
		require.InDelta(t, 2e-6, p.InputPricePerToken, 1e-12, model)
		require.InDelta(t, 10e-6, p.OutputPricePerToken, 1e-12, model)
		require.InDelta(t, 0.1e-6, p.CacheReadPricePerToken, 1e-12, model)
	}

	// Cyber 变体不享受优惠价，且必须先于基础型号判。
	for _, model := range []string{
		"gemini-4-argon-cyber-permissive",
		"vertex_ai/gemini-4-argon-cyber-permissive",
	} {
		p := svc.getFallbackPricing(model)
		require.NotNil(t, p, model)
		require.InDelta(t, 4e-6, p.InputPricePerToken, 1e-12, model)
		require.InDelta(t, 20e-6, p.OutputPricePerToken, 1e-12, model)
		require.InDelta(t, 0.2e-6, p.CacheReadPricePerToken, 1e-12, model)
	}
}

// 3.x Flash 系列不能被 4-argon 分支带走（价格差 2.7 倍）。
func TestFallbackPricingKeepsGemini3FlashFamily(t *testing.T) {
	svc := &BillingService{fallbackPrices: make(map[string]*ModelPricing)}
	svc.initFallbackPricing()

	p36 := svc.getFallbackPricing("gemini-3.6-flash")
	require.NotNil(t, p36)
	require.InDelta(t, 1.5e-6, p36.InputPricePerToken, 1e-12)

	p38 := svc.getFallbackPricing("gemini-3.8-flash-high")
	require.NotNil(t, p38)
	require.InDelta(t, 0.75e-6, p38.InputPricePerToken, 1e-12)
}
