package service

import (
	"context"
	"strings"
	"time"
)

// ResolveBalancePreauthorizationRequestID returns the same request key that
// usage billing will choose for normal HTTP gateway traffic. The request
// logger middleware always supplies a local request ID in production, so an
// upstream response ID cannot split the hold and settlement into two ledgers.
func ResolveBalancePreauthorizationRequestID(ctx context.Context) string {
	return resolveUsageBillingRequestID(ctx, "")
}

// BalancePreauthorizationBillingType mirrors the billing-mode decision in both
// gateway usage recorders.
func BalancePreauthorizationBillingType(apiKey *APIKey, subscription *UserSubscription) int8 {
	if subscription != nil && apiKey != nil && apiKey.Group != nil && apiKey.Group.IsSubscriptionType() {
		return BillingTypeSubscription
	}
	return BillingTypeBalance
}

// BalancePreauthorizationBillingModel freezes the best model known before an
// upstream account is selected, using the same requested/channel mapping
// policy as post-usage billing. Upstream/response-model policies necessarily
// settle their exact difference after the provider response.
func BalancePreauthorizationBillingModel(requestedModel string, mapping ChannelMappingResult) string {
	requestedModel = strings.TrimSpace(requestedModel)
	switch mapping.BillingModelSource {
	case BillingModelSourceRequested:
		return requestedModel
	case BillingModelSourceChannelMapped:
		if mapped := strings.TrimSpace(mapping.MappedModel); mapped != "" {
			return mapped
		}
	}
	if mapping.Mapped {
		if mapped := strings.TrimSpace(mapping.MappedModel); mapped != "" {
			return mapped
		}
	}
	return requestedModel
}

// BalancePreauthorizationCostInput returns the same pricing resolver,
// user/group multiplier, peak multiplier, and frozen price instant used by
// GatewayService.RecordUsage.
func (s *GatewayService) BalancePreauthorizationCostInput(
	ctx context.Context,
	apiKey *APIKey,
	model string,
	pricingAt time.Time,
	serviceTier string,
) CostInput {
	multiplier := 1.0
	if s != nil && s.cfg != nil {
		multiplier = s.cfg.Default.RateMultiplier
	}
	if s != nil && apiKey != nil && apiKey.GroupID != nil && apiKey.Group != nil {
		multiplier = s.ResolveUserGroupRateMultiplier(ctx, balancePreauthorizationAPIKeyUserID(apiKey), *apiKey.GroupID, apiKey.Group.RateMultiplier)
	}
	multiplier, _ = computePeakAwareMultipliers(apiKey, multiplier, pricingAt)
	return CostInput{
		Ctx:            ctx,
		Model:          strings.TrimSpace(model),
		GroupID:        apiKeyGroupID(apiKey),
		Group:          balancePreauthorizationAPIKeyGroup(apiKey),
		RateMultiplier: multiplier,
		PricingAt:      pricingAt,
		ServiceTier:    strings.TrimSpace(serviceTier),
		Resolver:       s.gatewayPricingResolver(),
	}
}

// BalancePreauthorizationCostInput returns the same pricing resolver,
// user/group multiplier, peak multiplier, and frozen price instant used by
// OpenAIGatewayService.RecordUsage.
func (s *OpenAIGatewayService) BalancePreauthorizationCostInput(
	ctx context.Context,
	apiKey *APIKey,
	model string,
	pricingAt time.Time,
	serviceTier string,
) CostInput {
	multiplier := 1.0
	if s != nil && s.cfg != nil {
		multiplier = s.cfg.Default.RateMultiplier
	}
	if s != nil && apiKey != nil && apiKey.GroupID != nil && apiKey.Group != nil {
		multiplier = s.ResolveUserGroupRateMultiplier(ctx, balancePreauthorizationAPIKeyUserID(apiKey), *apiKey.GroupID, apiKey.Group.RateMultiplier)
	}
	multiplier, _ = computePeakAwareMultipliers(apiKey, multiplier, pricingAt)
	return CostInput{
		Ctx:            ctx,
		Model:          strings.TrimSpace(model),
		GroupID:        apiKeyGroupID(apiKey),
		Group:          balancePreauthorizationAPIKeyGroup(apiKey),
		RateMultiplier: multiplier,
		PricingAt:      pricingAt,
		ServiceTier:    strings.TrimSpace(serviceTier),
		Resolver:       s.openAIGatewayPricingResolver(),
	}
}

func (s *GatewayService) gatewayPricingResolver() *ModelPricingResolver {
	if s == nil {
		return nil
	}
	return s.resolver
}

func (s *OpenAIGatewayService) openAIGatewayPricingResolver() *ModelPricingResolver {
	if s == nil {
		return nil
	}
	return s.resolver
}

func apiKeyGroupID(apiKey *APIKey) *int64 {
	if apiKey == nil {
		return nil
	}
	return apiKey.GroupID
}

func balancePreauthorizationAPIKeyGroup(apiKey *APIKey) *Group {
	if apiKey == nil {
		return nil
	}
	return apiKey.Group
}

func balancePreauthorizationAPIKeyUserID(apiKey *APIKey) int64 {
	if apiKey == nil {
		return 0
	}
	if apiKey.UserID > 0 {
		return apiKey.UserID
	}
	if apiKey.User != nil {
		return apiKey.User.ID
	}
	return 0
}

// TransferBalancePreauthorizationToUsageTask moves the sole active guard owner
// into the worker context. The old handler handle becomes a harmless stale
// owner, so its deferred Refund cannot undo worker-owned settlement.
func TransferBalancePreauthorizationToUsageTask(
	guard *BalancePreauthorizationGuard,
	task UsageRecordTask,
) (UsageRecordTask, bool) {
	if task == nil {
		return nil, false
	}
	if guard == nil {
		return task, true
	}
	workerGuard, ok := guard.TransferToWorker()
	if !ok {
		// A duplicate submission is an invariant violation, but dropping the
		// opaque task would also discard its non-money side effects. Keep the
		// stale guard attached: applyUsageBilling rejects it before repo.Apply,
		// while the rest of the task remains observable and runnable.
		return func(ctx context.Context) {
			task(ContextWithBalancePreauthorizationGuard(ctx, guard))
		}, false
	}
	return func(ctx context.Context) {
		task(ContextWithBalancePreauthorizationGuard(ctx, workerGuard))
	}, true
}
