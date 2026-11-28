package handler

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

type balancePreauthorizer interface {
	Preauthorize(context.Context, service.BalancePreauthorizationRequest) (*service.BalancePreauthorizationGuard, error)
}

type balanceFixedAmountPreauthorizer interface {
	PreauthorizeFixedAmount(ctx context.Context, requestID string, apiKeyID, userID int64, authorizationFingerprint string, holdAmount float64) (*service.BalancePreauthorizationGuard, error)
}

type balancePreauthorizationPricingProvider interface {
	BalancePreauthorizationCostInput(context.Context, *service.APIKey, string, time.Time, string) service.CostInput
}

type balancePreauthorizationRequirement interface {
	RequiresPreauthorization(billingType int8) bool
}

func preauthorizeTextGatewayRequest(
	ctx context.Context,
	preauthorizer balancePreauthorizer,
	pricing balancePreauthorizationPricingProvider,
	apiKey *service.APIKey,
	subscription *service.UserSubscription,
	body []byte,
	billingModel string,
	pricingAt time.Time,
	serviceTier string,
) (*service.BalancePreauthorizationGuard, error) {
	return preauthorizeTextGatewayRequestWithID(
		ctx, "", preauthorizer, pricing, apiKey, subscription, body, billingModel, pricingAt, serviceTier,
	)
}

func preauthorizeTextGatewayRequestWithID(
	ctx context.Context,
	requestID string,
	preauthorizer balancePreauthorizer,
	pricing balancePreauthorizationPricingProvider,
	apiKey *service.APIKey,
	subscription *service.UserSubscription,
	body []byte,
	billingModel string,
	pricingAt time.Time,
	serviceTier string,
) (*service.BalancePreauthorizationGuard, error) {
	if preauthorizer == nil || pricing == nil || apiKey == nil {
		return nil, nil
	}
	if deferKeyRoutePreauthorization(ctx, apiKey) {
		return nil, nil
	}
	billingType := service.BalancePreauthorizationBillingType(apiKey, subscription)
	if requirement, ok := preauthorizer.(balancePreauthorizationRequirement); ok &&
		!requirement.RequiresPreauthorization(billingType) {
		return nil, nil
	}
	userID := apiKey.UserID
	if userID <= 0 && apiKey.User != nil {
		userID = apiKey.User.ID
	}
	reqID := strings.TrimSpace(requestID)
	if reqID == "" {
		reqID = service.ResolveBalancePreauthorizationRequestID(ctx)
	}
	payloadHash := service.HashUsageRequestPayload(body)
	if payloadHash == "" {
		payloadHash = service.HashUsageRequestPayload([]byte(reqID))
	}
	tokenEstimate := service.EstimateBalancePreauthorizationTokensForContext(ctx, body)
	return preauthorizer.Preauthorize(ctx, service.BalancePreauthorizationRequest{
		RequestID:                 reqID,
		APIKeyID:                  apiKey.ID,
		UserID:                    userID,
		AuthorizationFingerprint:  payloadHash,
		BillingType:               billingType,
		BillableInputBytes:        len(body),
		EstimatedInputTokens:      tokenEstimate.InputTokens,
		InitialOutputWindowTokens: tokenEstimate.OutputTokens,
		CostInput:                 pricing.BalancePreauthorizationCostInput(ctx, apiKey, billingModel, pricingAt, serviceTier),
	})
}

// preauthorizeFixedAmountGatewayRequest reserves an explicit USD hold against
// live_wallet for endpoints whose admission price is calculated directly from
// tool, audio, search, or cache-storage tariffs rather than CalculateCostUnified.
func preauthorizeFixedAmountGatewayRequest(
	ctx context.Context,
	preauthorizer balancePreauthorizer,
	apiKey *service.APIKey,
	subscription *service.UserSubscription,
	requestID string,
	body []byte,
	holdAmount float64,
) (*service.BalancePreauthorizationGuard, error) {
	if preauthorizer == nil || apiKey == nil {
		return nil, nil
	}
	if deferKeyRoutePreauthorization(ctx, apiKey) {
		return nil, nil
	}
	billingType := service.BalancePreauthorizationBillingType(apiKey, subscription)
	if billingType != service.BillingTypeBalance {
		return nil, nil
	}
	if requirement, ok := preauthorizer.(balancePreauthorizationRequirement); ok &&
		!requirement.RequiresPreauthorization(billingType) {
		return nil, nil
	}
	fixed, ok := preauthorizer.(balanceFixedAmountPreauthorizer)
	if !ok {
		return nil, service.ErrBillingServiceUnavailable
	}
	userID := apiKey.UserID
	if userID <= 0 && apiKey.User != nil {
		userID = apiKey.User.ID
	}
	reqID := strings.TrimSpace(requestID)
	if reqID == "" {
		reqID = service.ResolveBalancePreauthorizationRequestID(ctx)
	}
	payloadHash := service.HashUsageRequestPayload(body)
	if payloadHash == "" {
		payloadHash = service.HashUsageRequestPayload([]byte(reqID))
	}
	return fixed.PreauthorizeFixedAmount(ctx, reqID, apiKey.ID, userID, payloadHash, holdAmount)
}

func withFixedKeyRouteForPreauthorization(ctx context.Context, apiKey *service.APIKey) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if apiKey == nil || apiKey.GroupID == nil {
		return ctx
	}
	return context.WithValue(ctx, selectedKeyRouteContextKey{}, *apiKey.GroupID)
}

// preauthorizePerRequestGatewayRequest reserves balance for count/size/duration
// -metered endpoints (images, video, standalone search) before an upstream
// account is selected. Unlike the text path it prices the request once from the
// explicit billing units in estimate, holding the exact request price; usage
// settlement later refunds any positive difference through the shared guard.
func preauthorizePerRequestGatewayRequest(
	ctx context.Context,
	preauthorizer balancePreauthorizer,
	pricing balancePreauthorizationPricingProvider,
	apiKey *service.APIKey,
	subscription *service.UserSubscription,
	body []byte,
	billingModel string,
	pricingAt time.Time,
	estimate service.PerRequestPreauthorizationEstimate,
) (*service.BalancePreauthorizationGuard, error) {
	if preauthorizer == nil || pricing == nil || apiKey == nil {
		return nil, nil
	}
	if deferKeyRoutePreauthorization(ctx, apiKey) {
		return nil, nil
	}
	billingType := service.BalancePreauthorizationBillingType(apiKey, subscription)
	if requirement, ok := preauthorizer.(balancePreauthorizationRequirement); ok &&
		!requirement.RequiresPreauthorization(billingType) {
		return nil, nil
	}
	userID := apiKey.UserID
	if userID <= 0 && apiKey.User != nil {
		userID = apiKey.User.ID
	}
	payloadHash := service.HashUsageRequestPayload(body)
	return preauthorizer.Preauthorize(ctx, service.BalancePreauthorizationRequest{
		RequestID:                service.ResolveBalancePreauthorizationRequestID(ctx),
		APIKeyID:                 apiKey.ID,
		UserID:                   userID,
		AuthorizationFingerprint: payloadHash,
		BillingType:              billingType,
		EstimateKind:             service.PreauthorizationEstimatePerRequest,
		PerRequestEstimate:       estimate,
		CostInput:                pricing.BalancePreauthorizationCostInput(ctx, apiKey, billingModel, pricingAt, ""),
	})
}

func deferBalancePreauthorizationRefund(reqLog *zap.Logger, guard *service.BalancePreauthorizationGuard) {
	if guard == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := guard.Refund(ctx); err != nil &&
		err != service.ErrBalancePreauthorizationAlreadyFinalized {
		if reqLog != nil {
			reqLog.Error("billing.balance_preauthorization_refund_failed", zap.Error(err))
		}
	}
}

func transferBalancePreauthorizationUsageTask(
	parent context.Context,
	task service.UsageRecordTask,
) (service.UsageRecordTask, bool) {
	guard, ok := service.BalancePreauthorizationGuardFromContext(parent)
	if !ok {
		return task, task != nil
	}
	return service.TransferBalancePreauthorizationToUsageTask(guard, task)
}

func shouldRecordStandaloneCyberUsage(forwardErr error, hasUsageResult bool) bool {
	return forwardErr != nil && !hasUsageResult
}

var errDuplicateBalancePreauthorizationUsageTask = errors.New("duplicate balance preauthorization usage task")
