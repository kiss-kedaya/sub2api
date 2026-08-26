package handler

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

type balancePreauthorizer interface {
	Preauthorize(context.Context, service.BalancePreauthorizationRequest) (*service.BalancePreauthorizationGuard, error)
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
	if preauthorizer == nil || pricing == nil || apiKey == nil {
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
	tokenEstimate := service.EstimateBalancePreauthorizationTokens(body)
	return preauthorizer.Preauthorize(ctx, service.BalancePreauthorizationRequest{
		RequestID:                 service.ResolveBalancePreauthorizationRequestID(ctx),
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
