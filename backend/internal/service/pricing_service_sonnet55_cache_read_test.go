package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSonnet55CatalogCacheReadPrice(t *testing.T) {
	for _, model := range []string{"claude-sonnet-5-5", "anthropic/claude-sonnet-5.5", "us.anthropic.claude-sonnet-5-5"} {
		t.Run(model, func(t *testing.T) {
			catalog := []byte(fmt.Sprintf(`{
				%q: {"input_cost_per_token":2e-6,"output_cost_per_token":10e-6,
					"cache_read_input_token_cost":2e-7,"cache_creation_input_token_cost":2.5e-6,
					"cache_creation_input_token_cost_above_1hr":4e-6},
				"claude-opus-5-5":{"input_cost_per_token":4e-6,"output_cost_per_token":20e-6,"cache_read_input_token_cost":2e-7}
			}`, model))
			for _, override := range []bool{false, true} {
				t.Run(fmt.Sprintf("local_override=%t", override), func(t *testing.T) {
					svc := &PricingService{}
					expected := 0.1e-6
					if override {
						svc = newPricingServiceWithOverride(t, fmt.Sprintf(`{%q:{"cache_read_input_token_cost":3.7e-7}}`, model))
						expected = 3.7e-7
					}
					data, err := svc.parsePricingData(catalog)
					require.NoError(t, err)
					svc.pricingData = data
					pricing := svc.GetModelPricing(model)
					require.NotNil(t, pricing)
					require.InDelta(t, expected, pricing.CacheReadInputTokenCost, 1e-14)
					require.InDelta(t, 2.5e-6, pricing.CacheCreationInputTokenCost, 1e-14)
					require.InDelta(t, 4e-6, pricing.CacheCreationInputTokenCostAbove1hr, 1e-14)
					require.InDelta(t, 2e-7, data["claude-opus-5-5"].CacheReadInputTokenCost, 1e-14)

					billing := NewBillingService(&config.Config{}, svc)
					cost, err := billing.CalculateCostUnified(CostInput{
						Ctx: context.Background(), Model: model,
						Tokens: UsageTokens{CacheReadTokens: 1_000_000}, RateMultiplier: 0.75,
						Resolver: NewModelPricingResolver(nil, billing),
					})
					require.NoError(t, err)
					require.InDelta(t, 1_000_000*expected*0.75, cost.ActualCost, 1e-10)
					custom := 4.5e-7
					channelPricing, err := billing.GetModelPricingWithChannel(model, &ChannelModelPricing{CacheReadPrice: &custom})
					require.NoError(t, err)
					require.InDelta(t, custom, channelPricing.CacheReadPricePerToken, 1e-14)
				})
			}
		})
	}
}
