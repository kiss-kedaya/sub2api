package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

const (
	// BalancePreauthorizationDirectPGHoldPrefix identifies preauthorization rows
	// paired with a direct PG users.balance/frozen_balance hold (batch image,
	// async video). Because the PG hold transaction atomically advances the row
	// from Authorized -> FinalizationPending, any recovery observing Authorized
	// with this prefix knows the PG hold never committed and refunds Redis.
	BalancePreauthorizationDirectPGHoldPrefix = "pg-hold:"

	// BalancePreauthorizationDirectPGHoldFingerprintPrefix marks
	// billing_balance_settlements rows whose PG users.balance deduction was
	// already applied in the caller's hold transaction.
	// CompleteBalancePreauthorizationSettlement transitions matching rows
	// directly to Applied (never Pending) so FlushPendingBalanceSettlements
	// never double-debits PG.
	BalancePreauthorizationDirectPGHoldFingerprintPrefix = "pg-direct-hold:"
)

// BalancePreauthorizationDirectPGHoldRequestID normalizes a direct-PG-hold
// request identifier with the required pg-hold: prefix.
func BalancePreauthorizationDirectPGHoldRequestID(rawID string) string {
	trimmed := strings.TrimSpace(rawID)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, BalancePreauthorizationDirectPGHoldPrefix) {
		return trimmed
	}
	return BalancePreauthorizationDirectPGHoldPrefix + trimmed
}

// PreauthorizeDirectPGHold atomically reserves holdAmount against Redis
// live_wallet before a direct-PG balance freeze (batch image / async video).
// Returns (nil, nil) when preauthorization is disabled or holdAmount <= 0.
func (s *BalancePreauthorizationService) PreauthorizeDirectPGHold(
	ctx context.Context,
	rawRequestID string,
	apiKeyID int64,
	userID int64,
	holdAmount float64,
) (*BalancePreauthorizationGuard, error) {
	if s == nil {
		return nil, nil
	}
	ctx = nonNilContext(ctx)
	if !s.balanceHoldRuntimeEnabled(ctx) {
		return nil, nil
	}
	if invalidNonnegativeMoney(holdAmount) {
		return nil, balancePreauthorizationUnavailable(ErrInvalidBillingPreauthorizationEstimate)
	}
	quantized := QuantizeUsageBillingAmount(holdAmount)
	if quantized <= 0 {
		return nil, nil
	}
	requestID := BalancePreauthorizationDirectPGHoldRequestID(rawRequestID)
	if requestID == "" || apiKeyID <= 0 || userID <= 0 {
		return nil, balancePreauthorizationUnavailable(ErrInvalidBillingPreauthorizationEstimate)
	}
	authFingerprint := BalancePreauthorizationDirectPGHoldFingerprintPrefix + requestID
	return s.authorizeFixedHold(ctx, requestID, apiKeyID, userID, authFingerprint, quantized)
}

// PreauthorizeFixedAmount reserves an explicit USD hold against Redis
// live_wallet for endpoints whose admission price is computed outside
// CalculateCostUnified (Gemini CachedContent storage, DeepSeek tools, Grok
// Voice/Realtime, Grok WebSearch). Returns (nil, nil) when preauthorization is
// disabled.
func (s *BalancePreauthorizationService) PreauthorizeFixedAmount(
	ctx context.Context,
	requestID string,
	apiKeyID int64,
	userID int64,
	authorizationFingerprint string,
	holdAmount float64,
) (*BalancePreauthorizationGuard, error) {
	if s == nil {
		return nil, balancePreauthorizationUnavailable(errors.New("balance preauthorization service is nil"))
	}
	ctx = nonNilContext(ctx)
	if !s.balanceHoldRuntimeEnabled(ctx) {
		return nil, nil
	}
	requestID = strings.TrimSpace(requestID)
	authorizationFingerprint = strings.TrimSpace(authorizationFingerprint)
	if requestID == "" || authorizationFingerprint == "" || apiKeyID <= 0 || userID <= 0 || invalidNonnegativeMoney(holdAmount) {
		return nil, balancePreauthorizationUnavailable(ErrInvalidBillingPreauthorizationEstimate)
	}
	quantized := quantizeBillingHoldUpFromFloat(holdAmount)
	return s.authorizeFixedHold(ctx, requestID, apiKeyID, userID, authorizationFingerprint, quantized)
}

func (s *BalancePreauthorizationService) authorizeFixedHold(
	ctx context.Context,
	requestID string,
	apiKeyID int64,
	userID int64,
	authorizationFingerprint string,
	holdAmount float64,
) (*BalancePreauthorizationGuard, error) {
	if s.snapshotReader == nil || s.wallet == nil || s.watermarkWallet == nil || s.repo == nil {
		return nil, balancePreauthorizationUnavailable(errors.New("balance preauthorization dependency is unavailable"))
	}
	record, err := s.repo.PrepareBalancePreauthorization(ctx, &BalancePreauthorizationCommand{
		RequestID:                requestID,
		APIKeyID:                 apiKeyID,
		UserID:                   userID,
		AuthorizationFingerprint: authorizationFingerprint,
		HoldAmount:               holdAmount,
	})
	if err != nil {
		return nil, balancePreauthorizationUnavailable(err)
	}
	if record == nil || record.RequestID != requestID || record.APIKeyID != apiKeyID ||
		record.UserID != userID || record.HoldAmount != holdAmount ||
		(record.Status != BalanceSettlementPrepared && record.Status != BalanceSettlementAuthorized) {
		return nil, balancePreauthorizationUnavailable(fmt.Errorf("unexpected balance preauthorization state: %v", balancePreauthorizationRecordStatus(record)))
	}

	attemptID := BalancePreauthorizationAttemptID(requestID, apiKeyID)
	authorized, err := s.watermarkWallet.AuthorizeExistingLiveBalance(
		ctx,
		userID,
		attemptID,
		record.HoldAmount,
	)
	if err == nil && authorized.Outcome == LiveBalanceOutcomeNotFound {
		var snapshot LiveBalanceInitializationSnapshot
		snapshot, err = s.snapshotReader.LoadLiveBalanceInitializationSnapshot(ctx, userID)
		if err == nil {
			authorized, err = s.watermarkWallet.AuthorizeLiveBalanceAtWatermarkIfSafe(
				ctx,
				userID,
				attemptID,
				snapshot.Balance,
				snapshot.Watermark,
				record.HoldAmount,
				!snapshot.HasUnsettled,
			)
		}
	}
	if err != nil {
		s.compensateAuthorizationFailure(requestID, apiKeyID, userID)
		return nil, balancePreauthorizationUnavailable(fmt.Errorf("authorize live balance: %w", err))
	}
	if authorized.Outcome == LiveBalanceOutcomeInsufficient {
		s.compensateAuthorizationFailure(requestID, apiKeyID, userID)
		return nil, ErrBalanceWithholdingFailed
	}
	if !liveBalanceAuthorizationSucceeded(authorized, record.HoldAmount) {
		s.compensateAuthorizationFailure(requestID, apiKeyID, userID)
		return nil, balancePreauthorizationUnavailable(fmt.Errorf(
			"authorize live balance returned outcome=%d state=%d",
			authorized.Outcome,
			authorized.State,
		))
	}
	if err := s.repo.MarkBalancePreauthorizationAuthorized(ctx, requestID, apiKeyID); err != nil {
		s.compensateAuthorizationFailure(requestID, apiKeyID, userID)
		return nil, balancePreauthorizationUnavailable(err)
	}

	core := &balancePreauthorizationGuardCore{
		service:       s,
		requestID:     requestID,
		apiKeyID:      apiKeyID,
		userID:        userID,
		attemptID:     attemptID,
		holdAmount:    record.HoldAmount,
		outputWindow:  0,
		ownerToken:    1,
		terminalState: balancePreauthorizationGuardActive,
	}
	return &BalancePreauthorizationGuard{core: core, ownerToken: 1}, nil
}

// EstimateDeepSeekToolPreauthorizationCost computes the exact customer charge
// (USD, post-multiplier) for a DeepSeek standalone tool request using the same
// pricing chain and tool multiplier as OpenAIGatewayService.RecordUsage.
// Explicit 0-priced tools return 0 cleanly.
func (s *OpenAIGatewayService) EstimateDeepSeekToolPreauthorizationCost(
	ctx context.Context,
	apiKey *APIKey,
	modelLabel string,
	units float64,
) float64 {
	if s == nil || apiKey == nil || units <= 0 || math.IsNaN(units) || math.IsInf(units, 0) {
		return 0
	}
	baseMultiplier := s.openAIBaseRateMultiplier(ctx, apiKey)
	toolMultiplier := resolveToolRateMultiplier(apiKey, baseMultiplier)
	if toolMultiplier <= 0 {
		return 0
	}
	breakdown := CalculateDeepSeekToolCost(ctx, s.resolver, apiKeyGroup(apiKey), modelLabel, units, toolMultiplier)
	if breakdown == nil || invalidNonnegativeMoney(breakdown.ActualCost) {
		return 0
	}
	return breakdown.ActualCost
}

// EstimateAudioPreauthorizationCost computes the exact customer charge (USD,
// post-multiplier) for Grok Voice HTTP (tts/stt) and Realtime WS audio units
// using the exact same pricing precedence as OpenAIGatewayService.RecordUsage.
// Explicit 0-priced audio modes return 0 cleanly.
func (s *OpenAIGatewayService) EstimateAudioPreauthorizationCost(
	ctx context.Context,
	apiKey *APIKey,
	model string,
	mode string,
	units float64,
) float64 {
	if s == nil || apiKey == nil || units <= 0 || math.IsNaN(units) || math.IsInf(units, 0) {
		return 0
	}
	baseMultiplier := s.openAIBaseRateMultiplier(ctx, apiKey)
	if baseMultiplier <= 0 {
		return 0
	}
	billingModel := strings.TrimSpace(model)
	if billingModel == "" {
		billingModel = strings.TrimSpace(mode)
	}
	if s.billingService != nil {
		if resolved := s.resolveOpenAIChannelPricing(ctx, billingModel, apiKey); resolved != nil &&
			resolved.Mode == BillingModePerRequest && apiKey.Group != nil {
			gid := apiKey.Group.ID
			cost, err := s.billingService.CalculateCostUnified(CostInput{
				Ctx:            ctx,
				Model:          billingModel,
				GroupID:        &gid,
				Group:          apiKey.Group,
				UsageUnits:     units,
				SizeTier:       mode,
				RateMultiplier: baseMultiplier,
				Resolver:       s.resolver,
				Resolved:       resolved,
			})
			if err == nil && cost != nil && !invalidNonnegativeMoney(cost.ActualCost) {
				return cost.ActualCost
			}
		}
		cfg := groupAudioPriceConfigFromAPIKey(apiKey)
		if cost := s.billingService.CalculateAudioCost(mode, units, cfg, baseMultiplier); cost != nil && !invalidNonnegativeMoney(cost.ActualCost) {
			return cost.ActualCost
		}
	}
	return 0
}

func (s *OpenAIGatewayService) openAIBaseRateMultiplier(ctx context.Context, apiKey *APIKey) float64 {
	multiplier := 1.0
	if s != nil && s.cfg != nil && s.cfg.Default.RateMultiplier > 0 {
		multiplier = s.cfg.Default.RateMultiplier
	}
	if s != nil && apiKey != nil && apiKey.GroupID != nil && apiKey.Group != nil {
		multiplier = s.ResolveUserGroupRateMultiplier(ctx, balancePreauthorizationAPIKeyUserID(apiKey), *apiKey.GroupID, apiKey.Group.RateMultiplier)
	}
	return multiplier
}

// EstimateSearchPreauthorizationCost computes the exact customer charge (USD,
// post-multiplier) for Grok standalone web_search / x_search calls using the
// exact same pricing and multiplier chain as GatewayService.RecordUsage.
// Explicit 0-priced search groups return 0 cleanly.
func (s *GatewayService) EstimateSearchPreauthorizationCost(
	ctx context.Context,
	apiKey *APIKey,
	searchCalls int,
	pricingAt time.Time,
) float64 {
	if s == nil || s.billingService == nil || apiKey == nil || searchCalls <= 0 {
		return 0
	}
	multiplier := 1.0
	if s.cfg != nil {
		multiplier = s.cfg.Default.RateMultiplier
	}
	if apiKey.GroupID != nil && apiKey.Group != nil {
		multiplier = s.ResolveUserGroupRateMultiplier(ctx, balancePreauthorizationAPIKeyUserID(apiKey), *apiKey.GroupID, apiKey.Group.RateMultiplier)
	}
	if pricingAt.IsZero() {
		pricingAt = timezone.Now()
	}
	multiplier, _ = computePeakAwareMultipliers(apiKey, multiplier, pricingAt)
	if multiplier <= 0 {
		return 0
	}
	cost := s.billingService.CalculateSearchCost(searchCalls, groupSearchPricePer1kFromAPIKey(apiKey), multiplier)
	if cost == nil || invalidNonnegativeMoney(cost.ActualCost) {
		return 0
	}
	return cost.ActualCost
}
