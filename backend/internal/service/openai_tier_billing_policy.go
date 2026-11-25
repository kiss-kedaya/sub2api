package service

import (
	"context"
	"math"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// OpenAITierBillingPolicy is a group's service-tier billing policy as one request
// saw it. It is captured while the request is still in flight, from the same
// group object that decides forced tiers (the authenticated request group), and
// carried to usage recording. Reading it later from the key's routed group is
// not equivalent: that copy comes from a process-local scheduler cache which can
// lag a group edit, so a request could be forced to Fast by the new setting and
// still be billed by the old one.
//
// The policy only changes what the customer is charged. TotalCost keeps the real
// upstream tier price and service_tier keeps the tier that was actually used.
type OpenAITierBillingPolicy struct {
	GroupID int64
	// FreeFast is the legacy "Fast requests bill at the Standard price" switch.
	// It is a Fast multiplier of exactly 1 and wins over FastMultiplier.
	FreeFast bool
	// FastMultiplier / UltrafastMultiplier are multipliers applied on top of the
	// group rate multiplier. nil keeps the official, model-owned tier pricing.
	FastMultiplier      *float64
	UltrafastMultiplier *float64
}

func openAITierBillingPolicyFromGroup(group *Group) *OpenAITierBillingPolicy {
	if group == nil || !groupSupportsOpenAIFast(group.Platform) {
		return nil
	}
	return &OpenAITierBillingPolicy{
		GroupID:             group.ID,
		FreeFast:            group.FreeOpenAIFast,
		FastMultiplier:      cloneGroupValuePointer(group.OpenAIFastMultiplier),
		UltrafastMultiplier: cloneGroupValuePointer(group.OpenAIUltrafastMultiplier),
	}
}

// OpenAITierBillingPolicyFromRequest captures the policy of the group serving this
// request. The request context group is the one forcing reads, so it is preferred;
// the key's own group is the fallback for callers without one.
func OpenAITierBillingPolicyFromRequest(ctx context.Context, apiKey *APIKey) *OpenAITierBillingPolicy {
	if apiKey == nil {
		return nil
	}
	if ctx != nil && apiKey.GroupID != nil {
		if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(group) && group.ID == *apiKey.GroupID {
			return openAITierBillingPolicyFromGroup(group)
		}
	}
	return openAITierBillingPolicyFromGroup(apiKey.Group)
}

// openAIPremiumTier maps a billed service tier to the policy tier it belongs to:
// "priority" (Fast), "ultrafast", or "" for everything the policy does not touch.
func openAIPremiumTier(serviceTier string) string {
	switch normalizeBillingServiceTier(serviceTier) {
	case "priority", "fast":
		return "priority"
	case OpenAIFastTierUltrafast:
		return OpenAIFastTierUltrafast
	default:
		return ""
	}
}

// multiplierFor returns the customer-facing tier multiplier the group overrides the
// official tier price with, and whether it overrides at all. Only OpenAI accounts
// serve these tiers; any other account keeps the price it was computed with.
func (p *OpenAITierBillingPolicy) multiplierFor(account *Account, tier string) (float64, bool) {
	if p == nil || account == nil || !account.IsOpenAI() {
		return 0, false
	}
	switch openAIPremiumTier(tier) {
	case "priority":
		if p.FreeFast {
			return 1, true
		}
		if p.FastMultiplier != nil && *p.FastMultiplier > 0 {
			return *p.FastMultiplier, true
		}
	case OpenAIFastTierUltrafast:
		if p.UltrafastMultiplier != nil && *p.UltrafastMultiplier > 0 {
			return *p.UltrafastMultiplier, true
		}
	}
	return 0, false
}

func roundTierMultiplier(v float64) float64 {
	return math.Round(v*10000) / 10000
}

// tokenCostPortion is the part of a cost breakdown that depends on the service
// tier: everything priced per token. Per-call add-ons (hosted search) are not.
func tokenCostPortion(c *CostBreakdown) float64 {
	if c == nil {
		return 0
	}
	return c.InputCost + c.ImageInputCost + c.OutputCost + c.ImageOutputCost + c.CacheCreationCost + c.CacheReadCost
}

// applyOpenAITierBilling re-prices the customer charge of a Fast / UltraFast
// request under the group's tier multiplier and returns the multiplier that
// applies to the usage row: the group's override, else the official tier ratio.
// cost is updated in place only when the group overrides the tier price.
func (s *OpenAIGatewayService) applyOpenAITierBilling(
	ctx context.Context,
	result *OpenAIForwardResult,
	apiKey *APIKey,
	billingModels []string,
	multiplier float64,
	imageMultiplier float64,
	videoMultiplier float64,
	baseMultiplier float64,
	tokens UsageTokens,
	serviceTier string,
	longContextBillingGate *bool,
	pricingAt time.Time,
	cost *CostBreakdown,
	override float64,
	hasOverride bool,
) (*float64, error) {
	// Hosted search is a per-call add-on and is not priced by tier: take it out of
	// both sides so only the token charge is re-priced.
	tokenResult := result
	tierTokens := cost
	if result.SearchCount > 0 {
		noSearch := *result
		noSearch.SearchCount = 0
		tokenResult = &noSearch
		var err error
		tierTokens, err = s.calculateOpenAIRecordUsageCost(ctx, tokenResult, apiKey, billingModels,
			multiplier, imageMultiplier, videoMultiplier, baseMultiplier, tokens, serviceTier, longContextBillingGate, pricingAt)
		if err != nil {
			return nil, err
		}
	}
	standard, err := s.calculateOpenAIRecordUsageCost(ctx, tokenResult, apiKey, billingModels,
		multiplier, imageMultiplier, videoMultiplier, baseMultiplier, tokens, "", longContextBillingGate, pricingAt)
	if err != nil {
		return nil, err
	}
	if standard == nil || tierTokens == nil {
		return nil, nil
	}
	if hasOverride {
		perCallAddOns := cost.ActualCost - tierTokens.ActualCost
		cost.ActualCost = standard.ActualCost*override + perCallAddOns
		applied := roundTierMultiplier(override)
		return &applied, nil
	}
	if standardPortion := tokenCostPortion(standard); standardPortion > 0 {
		applied := roundTierMultiplier(tokenCostPortion(tierTokens) / standardPortion)
		return &applied, nil
	}
	return nil, nil
}
