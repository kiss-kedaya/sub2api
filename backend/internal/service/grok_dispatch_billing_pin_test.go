//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

// A claude-named /v1/messages request on a Grok group is not served by Claude. The group's messages dispatch maps
// the name to the group's Grok model before an account is chosen, the account runs that model, and the response
// only echoes the name the client wrote. The bill therefore has to follow the model that did the work: its price
// and the group's rate. These tests pin that, so the Claude-pricing correction added for non-Anthropic groups
// (claude_pricing_anchor.go) can never start billing Grok-served work at a Claude price.

func TestGrokDispatchGroupServesClaudeNamesWithItsOwnModel(t *testing.T) {
	original := xai.RuntimeModelMappingOptions()
	t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(original) })
	xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{DefaultText: xai.DefaultTextModel, EnableCrossClientMap: true})
	group := &Group{Platform: PlatformGrok}

	for _, name := range []string{"claude-opus-4-8", "claude-sonnet-4-5", "claude-haiku-4-5", "claude-opus-zzz-9999"} {
		require.Equal(t, xai.DefaultTextModel, group.ResolveMessagesDispatchModel(name), name)
	}
	// A claude-looking name outside the opus/sonnet/haiku families is not dispatched at all.
	require.Empty(t, group.ResolveMessagesDispatchModel("claude-doesnotexist-9"))
	// And nothing here is a Claude upstream model: the mapped name is not one.
	require.False(t, isClaudeModel(group.ResolveMessagesDispatchModel("claude-opus-4-8")))
}

func TestOpenAIGatewayServiceRecordUsage_GrokDispatchBillsTheServedModel(t *testing.T) {
	const groupRate = 0.12
	usage := OpenAIUsage{InputTokens: 1230, OutputTokens: 45, CacheReadInputTokens: 1152}
	claudeCard := ChannelModelPricing{
		Platform: PlatformAnthropic, Models: []string{"claude-*"}, BillingMode: BillingModeToken,
		InputPrice: testPtrFloat64(5e-6), OutputPrice: testPtrFloat64(25e-6), CacheReadPrice: testPtrFloat64(5e-7),
	}

	for _, tc := range []struct {
		name      string
		modelCard []ChannelModelPricing
	}{
		{name: "plain Grok group"},
		{name: "Grok group that also carries a Claude price card", modelCard: []ChannelModelPricing{claudeCard}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			group := &Group{
				ID: 31, Platform: PlatformGrok, RateMultiplier: groupRate,
				CrossProtocolConversionEnabled: true, ModelPricing: tc.modelCard,
			}

			// What ForwardAsAnthropic reports for a claude-named request served by the group's Grok model.
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &OpenAIForwardResult{
					RequestID: "resp_grok_dispatch_billing", Usage: usage, Duration: time.Second,
					Model: "claude-opus-4-8", BillingModel: xai.DefaultTextModel, UpstreamModel: xai.DefaultTextModel,
				},
				APIKey:  &APIKey{ID: 1040, GroupID: i64p(group.ID), Group: group},
				User:    &User{ID: 2040},
				Account: &Account{ID: 3040, Platform: PlatformGrok, Type: AccountTypeAPIKey},
			})
			require.NoError(t, err)
			log := usageRepo.lastLog
			require.NotNil(t, log)

			served := expectedOpenAICost(t, svc, xai.DefaultTextModel, usage, groupRate)
			require.Greater(t, served.ActualCost, 0.0)
			require.InDelta(t, served.TotalCost, log.TotalCost, 1e-12, "list price of the served model")
			require.InDelta(t, served.ActualCost, log.ActualCost, 1e-12, "served model's price at the group's own rate")
			require.InDelta(t, groupRate, log.RateMultiplier, 1e-12)

			// The same tokens at the Claude card's price (78 fresh input, 45 output, 1152 cache read) cost about twice
			// as much, so the equality above cannot hold by accident.
			claudeListPrice := 78*5e-6 + 45*25e-6 + 1152*5e-7
			require.Greater(t, claudeListPrice, 1.5*log.TotalCost)

			// The log keeps both names, so an operator can always see what really served the request.
			require.Equal(t, "claude-opus-4-8", log.Model)
			require.NotNil(t, log.UpstreamModel)
			require.Equal(t, xai.DefaultTextModel, *log.UpstreamModel)
		})
	}
}

// The correction targets a different mistake (a non-Anthropic group billing a Claude model from its provider
// catalog). It must still leave work served by that group's own model alone, however the name arrives.
func TestClaudeCatalogMispricingGroupIgnoresGrokServedModels(t *testing.T) {
	group := &Group{
		ID: 31, Platform: PlatformGrok,
		ModelPricing: []ChannelModelPricing{{
			Platform: PlatformAnthropic, Models: []string{"claude-*"}, BillingMode: BillingModeToken,
			InputPrice: testPtrFloat64(5e-6),
		}},
	}

	require.Nil(t, claudeCatalogMispricingGroup(group, xai.DefaultTextModel), "billed as the Grok model it ran on")
	// A catch-all card matches every model, including the Grok one: that is the Grok model's own price.
	catchAll := &Group{ID: 32, Platform: PlatformGrok, ModelPricing: []ChannelModelPricing{{
		Platform: PlatformGrok, Models: []string{"*"}, BillingMode: BillingModeToken, InputPrice: testPtrFloat64(2e-6),
	}}}
	require.Nil(t, claudeCatalogMispricingGroup(catchAll, xai.DefaultTextModel))
	require.Nil(t, claudeCatalogMispricingGroup(group, "grok-4.5"))
	require.NotNil(t, claudeCatalogMispricingGroup(group, "claude-opus-4-8"), "the case the correction exists for")
	require.Nil(t, claudeCatalogMispricingGroup(&Group{ID: 7, Platform: PlatformAnthropic, ModelPricing: group.ModelPricing}, "claude-opus-4-8"))
}
