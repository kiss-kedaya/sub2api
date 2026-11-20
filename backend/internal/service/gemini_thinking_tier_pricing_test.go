package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGemini4ThinkingTierPricingForCatalogAndBilling(t *testing.T) {
	argon := &LiteLLMModelPricing{InputCostPerToken: 2e-6, OutputCostPerToken: 10e-6}
	carbon := &LiteLLMModelPricing{InputCostPerToken: 4e-6, OutputCostPerToken: 12e-6}
	hydrogen := &LiteLLMModelPricing{InputCostPerToken: 1.5e-6, OutputCostPerToken: 7.5e-6}
	exact := &LiteLLMModelPricing{InputCostPerToken: 3e-6, OutputCostPerToken: 20e-6}
	googleExact := &LiteLLMModelPricing{InputCostPerToken: 5e-6, OutputCostPerToken: 21e-6}
	vertexCarbon := &LiteLLMModelPricing{InputCostPerToken: 6e-6, OutputCostPerToken: 22e-6}
	svc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"gemini-4-argon": argon, "gemini-4-carbon": carbon, "gemini-4-hydrogen": hydrogen,
		"gemini-4-argon-high":   exact,
		"google/gemini-4-argon": argon, "google/gemini-4-argon-high": googleExact,
		"vertex_ai/gemini-4-carbon": vertexCarbon,
	}}
	billing := NewBillingService(&config.Config{}, svc)
	for name, want := range map[string]*LiteLLMModelPricing{
		"gemini-4-argon-high": exact, "gemini-4-argon-medium": argon, "gemini-4-argon-low": argon,
		"gemini-4-carbon-high": carbon, "gemini-4-carbon-medium": carbon, "gemini-4-carbon-low": carbon,
		"gemini-4-hydrogen-high": hydrogen, "gemini-4-hydrogen-medium": hydrogen, "gemini-4-hydrogen-low": hydrogen,
		"gemini-4-hydrogen-xhigh": hydrogen, "gemini-4-hydrogen-sonar": hydrogen,
		"google/gemini-4-argon-high":    googleExact,
		"models/gemini-4-hydrogen-high": hydrogen, "vertex_ai/gemini-4-carbon-low": vertexCarbon,
	} {
		t.Run(name, func(t *testing.T) {
			require.Same(t, want, svc.GetModelPricing(name))
			require.Same(t, want, svc.GetIdentifiedModelPricing(name))
			cost, err := billing.GetModelPricing(name)
			require.NoError(t, err)
			require.Equal(t, want.InputCostPerToken, cost.InputPricePerToken)
			require.Equal(t, want.OutputCostPerToken, cost.OutputPricePerToken)
			models := []SupportedModel{{Name: name}}
			fillGlobalPricingFallback(svc, models)
			require.NotNil(t, models[0].Pricing)
			require.NotNil(t, models[0].Pricing.InputPrice)
			require.Equal(t, cost.InputPricePerToken, *models[0].Pricing.InputPrice)
		})
	}
}

func TestGemini4ThinkingTierPricingDoesNotCrossUnknownNamespace(t *testing.T) {
	base := &LiteLLMModelPricing{InputCostPerToken: 2e-6}
	tier := &LiteLLMModelPricing{InputCostPerToken: 3e-6}
	svc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"gemini-4-carbon":        base,
		"gemini-4-carbon-medium": tier,
	}}
	billing := NewBillingService(&config.Config{}, svc)
	for _, name := range []string{"other/gemini-4-carbon-medium", "other/gemini-4-carbon-low"} {
		require.Nil(t, svc.GetIdentifiedModelPricing(name), name)
		require.Nil(t, svc.GetModelPricing(name), name)
		_, err := billing.GetModelPricing(name)
		require.ErrorIs(t, err, ErrModelPricingUnavailable, name)
	}

	scopedBase := &LiteLLMModelPricing{InputCostPerToken: 2e-6}
	scopedTier := &LiteLLMModelPricing{InputCostPerToken: 3e-6}
	scoped := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"other/gemini-4-carbon":      scopedBase,
		"other/gemini-4-carbon-high": scopedTier,
	}}
	require.Same(t, scopedTier, scoped.GetModelPricing("other/gemini-4-carbon-high"))
	require.Same(t, scopedBase, scoped.GetModelPricing("other/gemini-4-carbon-low"))
}

func TestGemini4ThinkingTierPricingKeepsQualifiedQuotePreference(t *testing.T) {
	base := &LiteLLMModelPricing{InputCostPerToken: 1e-6}
	qualifiedBase := &LiteLLMModelPricing{InputCostPerToken: 2e-6}
	tier := &LiteLLMModelPricing{InputCostPerToken: 3e-6}
	googleTier := &LiteLLMModelPricing{InputCostPerToken: 4e-6}
	svc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"gemini-4-argon": base, "google/gemini-4-argon": qualifiedBase,
		"gemini-4-argon-high": tier, "google/gemini-4-argon-high": googleTier,
		"google/gemini-4-carbon": qualifiedBase,
	}}
	require.Same(t, tier, svc.GetModelPricing("gemini-4-argon-high"))
	require.Same(t, googleTier, svc.GetModelPricing("google/gemini-4-argon-high"))
	require.Same(t, base, svc.GetModelPricing("gemini-4-argon-low"))
	require.Same(t, qualifiedBase, svc.GetModelPricing("google/gemini-4-argon-low"))
	require.Same(t, qualifiedBase, svc.GetModelPricing("google/gemini-4-carbon-medium"))
	require.Nil(t, svc.GetIdentifiedModelPricing("other/gemini-4-carbon-medium"))
	require.Nil(t, svc.GetModelPricing("other/gemini-4-carbon-medium"))
	require.Nil(t, svc.GetModelPricing("other/gemini-4-carbon-low"))
}

func TestGemini4ThinkingTierPricingDoesNotGuessUnknownModels(t *testing.T) {
	svc := &PricingService{pricingData: map[string]*LiteLLMModelPricing{
		"gemini-4-argon": {InputCostPerToken: 2e-6},
	}}
	billing := NewBillingService(&config.Config{}, svc)
	for _, name := range []string{"gemini-4-carbon-high", "gemini-4-hydrogen-sonar", "gemini-4-argon-image", "gemini-4-argon-xhigh", "qwen4-max-high"} {
		require.Nil(t, svc.GetIdentifiedModelPricing(name), name)
		models := []SupportedModel{{Name: name}}
		fillGlobalPricingFallback(svc, models)
		require.Nil(t, models[0].Pricing, name)
		_, err := billing.GetModelPricing(name)
		require.ErrorIs(t, err, ErrModelPricingUnavailable, name)
	}
}
