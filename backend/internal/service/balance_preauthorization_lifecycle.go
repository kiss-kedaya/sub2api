package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/shopspring/decimal"
)

const (
	DefaultBalancePreauthorizationOutputWindow = 256
	balancePreauthorizationCompensationTimeout = 3 * time.Second
)

var (
	ErrBalanceWithholdingFailed = infraerrors.Forbidden(
		"BALANCE_WITHHOLDING_FAILED",
		"Insufficient balance, withholding failed",
	)
)

type balancePreauthorizationCostCalculator interface {
	CalculateCostUnified(input CostInput) (*CostBreakdown, error)
}

type balancePreauthorizationSnapshotReader interface {
	LoadLiveBalanceInitializationSnapshot(ctx context.Context, userID int64) (LiveBalanceInitializationSnapshot, error)
}

type balancePreauthorizationWallet interface {
	AuthorizeLiveBalance(ctx context.Context, userID int64, attemptID string, fallbackBalance, holdAmount float64) (LiveBalanceResult, error)
	FinalizeLiveBalance(ctx context.Context, userID int64, attemptID string, actualAmount float64) (LiveBalanceResult, error)
	RefundLiveBalance(ctx context.Context, userID int64, attemptID string) (LiveBalanceResult, error)
}

type balancePreauthorizationWatermarkedWallet interface {
	AuthorizeExistingLiveBalance(
		ctx context.Context,
		userID int64,
		attemptID string,
		holdAmount float64,
	) (LiveBalanceResult, error)
	AuthorizeLiveBalanceAtWatermark(
		ctx context.Context,
		userID int64,
		attemptID string,
		fallbackBalance float64,
		fallbackWatermark int64,
		holdAmount float64,
	) (LiveBalanceResult, error)
	AuthorizeLiveBalanceAtWatermarkIfSafe(
		ctx context.Context,
		userID int64,
		attemptID string,
		fallbackBalance float64,
		fallbackWatermark int64,
		holdAmount float64,
		allowInitialize bool,
	) (LiveBalanceResult, error)
}

type balancePreauthorizationRepository interface {
	PrepareBalancePreauthorization(ctx context.Context, cmd *BalancePreauthorizationCommand) (*BalancePreauthorizationRecord, error)
	MarkBalancePreauthorizationAuthorized(ctx context.Context, requestID string, apiKeyID int64) error
	BeginBalancePreauthorizationFinalization(ctx context.Context, requestID string, apiKeyID int64, amount float64, requestFingerprint string) error
	CompleteBalancePreauthorizationSettlement(ctx context.Context, requestID string, apiKeyID int64) error
	BeginBalancePreauthorizationRefund(ctx context.Context, requestID string, apiKeyID int64) error
	CompleteBalancePreauthorizationRefund(ctx context.Context, requestID string, apiKeyID int64) error
}

// BalancePreauthorizationService owns the durable request hold lifecycle.
// Pricing and validation happen before any mutation. Once prepare succeeds,
// every uncertain dependency result is fail-closed and left recoverable in PG.
type BalancePreauthorizationService struct {
	cfg             *config.Config
	costCalculator  balancePreauthorizationCostCalculator
	snapshotReader  balancePreauthorizationSnapshotReader
	wallet          balancePreauthorizationWallet
	watermarkWallet balancePreauthorizationWatermarkedWallet
	repo            balancePreauthorizationRepository
}

func NewBalancePreauthorizationService(
	cfg *config.Config,
	billingService *BillingService,
	billingCacheService *BillingCacheService,
	repo UsageBillingRepository,
) *BalancePreauthorizationService {
	service := &BalancePreauthorizationService{
		cfg:            cfg,
		costCalculator: billingService,
		repo:           repo,
	}
	service.snapshotReader, _ = repo.(balancePreauthorizationSnapshotReader)
	if billingCacheService != nil {
		service.wallet = billingCacheService.cache
		service.watermarkWallet, _ = billingCacheService.cache.(balancePreauthorizationWatermarkedWallet)
	}
	return service
}

// BalancePreauthorizationRequest carries the exact pricing context frozen for
// the request. Tokens in CostInput are ignored: the service prices the same
// conservative byte upper bound as input, cache-read, and cache-creation and
// holds the largest result.
type BalancePreauthorizationRequest struct {
	RequestID                 string
	APIKeyID                  int64
	UserID                    int64
	AuthorizationFingerprint  string
	BillingType               int8
	BillableInputBytes        int
	InitialOutputWindowTokens int
	CostInput                 CostInput
}

// RequiresPreauthorization lets handlers avoid pricing and hashing work for
// modes that Preauthorize would immediately skip.
func (s *BalancePreauthorizationService) RequiresPreauthorization(billingType int8) bool {
	if s == nil || billingType != BillingTypeBalance {
		return false
	}
	return s.cfg == nil || s.cfg.RunMode != config.RunModeSimple
}

// Preauthorize returns nil without touching billing state in simple or
// subscription mode. Balance mode returns a request-owned guard on success.
func (s *BalancePreauthorizationService) Preauthorize(
	ctx context.Context,
	request BalancePreauthorizationRequest,
) (*BalancePreauthorizationGuard, error) {
	if s == nil {
		return nil, balancePreauthorizationUnavailable(errors.New("balance preauthorization service is nil"))
	}
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		return nil, nil
	}
	if request.BillingType == BillingTypeSubscription {
		return nil, nil
	}
	if err := validateBalancePreauthorizationRequest(&request); err != nil {
		return nil, balancePreauthorizationUnavailable(err)
	}
	if s.costCalculator == nil || s.snapshotReader == nil || s.wallet == nil || s.watermarkWallet == nil || s.repo == nil {
		return nil, balancePreauthorizationUnavailable(errors.New("balance preauthorization dependency is unavailable"))
	}
	ctx = nonNilContext(ctx)

	holdAmount, outputWindow, err := s.estimateHold(ctx, request)
	if err != nil {
		return nil, balancePreauthorizationUnavailable(err)
	}
	requestID := strings.TrimSpace(request.RequestID)
	fingerprint := strings.TrimSpace(request.AuthorizationFingerprint)
	record, err := s.repo.PrepareBalancePreauthorization(ctx, &BalancePreauthorizationCommand{
		RequestID:                requestID,
		APIKeyID:                 request.APIKeyID,
		UserID:                   request.UserID,
		AuthorizationFingerprint: fingerprint,
		HoldAmount:               holdAmount,
	})
	if err != nil {
		return nil, balancePreauthorizationUnavailable(err)
	}
	if record == nil || record.RequestID != requestID || record.APIKeyID != request.APIKeyID ||
		record.UserID != request.UserID || record.HoldAmount != holdAmount ||
		(record.Status != BalanceSettlementPrepared && record.Status != BalanceSettlementAuthorized) {
		return nil, balancePreauthorizationUnavailable(fmt.Errorf("unexpected balance preauthorization state: %v", balancePreauthorizationRecordStatus(record)))
	}

	attemptID := BalancePreauthorizationAttemptID(requestID, request.APIKeyID)
	authorized, err := s.watermarkWallet.AuthorizeExistingLiveBalance(
		ctx,
		request.UserID,
		attemptID,
		record.HoldAmount,
	)
	if err == nil && authorized.Outcome == LiveBalanceOutcomeNotFound {
		var snapshot LiveBalanceInitializationSnapshot
		snapshot, err = s.snapshotReader.LoadLiveBalanceInitializationSnapshot(ctx, request.UserID)
		if err == nil {
			authorized, err = s.watermarkWallet.AuthorizeLiveBalanceAtWatermarkIfSafe(
				ctx,
				request.UserID,
				attemptID,
				snapshot.Balance,
				snapshot.Watermark,
				record.HoldAmount,
				!snapshot.HasUnsettled,
			)
		}
	}
	if err != nil {
		s.compensateAuthorizationFailure(requestID, request.APIKeyID, request.UserID)
		return nil, balancePreauthorizationUnavailable(fmt.Errorf("authorize live balance: %w", err))
	}
	if authorized.Outcome == LiveBalanceOutcomeInsufficient {
		s.compensateAuthorizationFailure(requestID, request.APIKeyID, request.UserID)
		return nil, ErrBalanceWithholdingFailed
	}
	if !liveBalanceAuthorizationSucceeded(authorized, record.HoldAmount) {
		s.compensateAuthorizationFailure(requestID, request.APIKeyID, request.UserID)
		return nil, balancePreauthorizationUnavailable(fmt.Errorf(
			"authorize live balance returned outcome=%d state=%d",
			authorized.Outcome,
			authorized.State,
		))
	}
	if err := s.repo.MarkBalancePreauthorizationAuthorized(ctx, requestID, request.APIKeyID); err != nil {
		s.compensateAuthorizationFailure(requestID, request.APIKeyID, request.UserID)
		return nil, balancePreauthorizationUnavailable(err)
	}

	core := &balancePreauthorizationGuardCore{
		service:       s,
		requestID:     requestID,
		apiKeyID:      request.APIKeyID,
		userID:        request.UserID,
		attemptID:     attemptID,
		holdAmount:    record.HoldAmount,
		outputWindow:  outputWindow,
		ownerToken:    1,
		terminalState: balancePreauthorizationGuardActive,
	}
	return &BalancePreauthorizationGuard{core: core, ownerToken: 1}, nil
}

func (s *BalancePreauthorizationService) estimateHold(
	ctx context.Context,
	request BalancePreauthorizationRequest,
) (float64, int, error) {
	outputWindow := request.InitialOutputWindowTokens
	if outputWindow == 0 {
		outputWindow = DefaultBalancePreauthorizationOutputWindow
	}
	base := request.CostInput
	base.Ctx = ctx
	if base.Resolved == nil && base.Resolver != nil {
		base.Resolved = base.Resolver.Resolve(ctx, PricingInput{
			Model:   base.Model,
			GroupID: base.GroupID,
			Group:   base.Group,
		})
		if base.Resolved == nil {
			return 0, 0, ErrModelPricingUnavailable
		}
	}

	scenarios := [...]UsageTokens{
		{InputTokens: request.BillableInputBytes, OutputTokens: outputWindow},
		{CacheReadTokens: request.BillableInputBytes, OutputTokens: outputWindow},
		{CacheCreationTokens: request.BillableInputBytes, OutputTokens: outputWindow},
		{
			CacheCreationTokens:   request.BillableInputBytes,
			CacheCreation1hTokens: request.BillableInputBytes,
			OutputTokens:          outputWindow,
		},
	}
	maxCost := 0.0
	for _, tokens := range scenarios {
		input := base
		input.Tokens = tokens
		breakdown, err := s.costCalculator.CalculateCostUnified(input)
		if err != nil {
			return 0, 0, err
		}
		if breakdown == nil || invalidNonnegativeMoney(breakdown.ActualCost) {
			return 0, 0, ErrInvalidBillingPreauthorizationEstimate
		}
		maxCost = math.Max(maxCost, breakdown.ActualCost)
	}
	return quantizeBillingHoldUpFromFloat(maxCost), outputWindow, nil
}

func (s *BalancePreauthorizationService) compensateAuthorizationFailure(requestID string, apiKeyID, userID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), balancePreauthorizationCompensationTimeout)
	defer cancel()
	beginErr := s.repo.BeginBalancePreauthorizationRefund(ctx, requestID, apiKeyID)
	result, refundErr := s.wallet.RefundLiveBalance(ctx, userID, BalancePreauthorizationAttemptID(requestID, apiKeyID))
	if beginErr != nil || refundErr != nil {
		return
	}
	if result.Outcome == LiveBalanceOutcomeNotFound || liveBalanceRefundSucceeded(result) {
		_ = s.repo.CompleteBalancePreauthorizationRefund(ctx, requestID, apiKeyID)
	}
}

func validateBalancePreauthorizationRequest(request *BalancePreauthorizationRequest) error {
	if request == nil || strings.TrimSpace(request.RequestID) == "" ||
		strings.TrimSpace(request.AuthorizationFingerprint) == "" ||
		request.APIKeyID <= 0 || request.UserID <= 0 ||
		request.BillableInputBytes < 0 || request.InitialOutputWindowTokens < 0 {
		return ErrInvalidBillingPreauthorizationEstimate
	}
	if request.BillingType != BillingTypeBalance {
		return fmt.Errorf("unsupported billing type %d", request.BillingType)
	}
	return nil
}

func liveBalanceOperationSucceeded(result LiveBalanceResult, expected LiveBalanceAttemptState) bool {
	return (result.Outcome == LiveBalanceOutcomeApplied || result.Outcome == LiveBalanceOutcomeIdempotent) &&
		result.State == expected
}

func liveBalanceAuthorizationSucceeded(result LiveBalanceResult, holdAmount float64) bool {
	return liveBalanceOperationSucceeded(result, LiveBalanceAttemptAuthorized) &&
		!invalidNonnegativeMoney(result.ReservedAmount) &&
		QuantizeUsageBillingAmount(result.ReservedAmount) >= QuantizeUsageBillingAmount(holdAmount)
}

func liveBalanceFinalizationSucceeded(result LiveBalanceResult, actualAmount float64) bool {
	return liveBalanceOperationSucceeded(result, LiveBalanceAttemptFinalized) &&
		!invalidNonnegativeMoney(result.ActualAmount) &&
		QuantizeUsageBillingAmount(result.ActualAmount) == QuantizeUsageBillingAmount(actualAmount)
}

func liveBalanceRefundSucceeded(result LiveBalanceResult) bool {
	return liveBalanceOperationSucceeded(result, LiveBalanceAttemptRefunded) &&
		!invalidNonnegativeMoney(result.ActualAmount) &&
		QuantizeUsageBillingAmount(result.ActualAmount) == 0
}

func balancePreauthorizationRecordStatus(record *BalancePreauthorizationRecord) any {
	if record == nil {
		return nil
	}
	return record.Status
}

func balancePreauthorizationUnavailable(cause error) error {
	if cause == nil {
		cause = errors.New("unknown balance preauthorization failure")
	}
	return ErrBillingServiceUnavailable.WithCause(cause)
}

func quantizeBillingHoldUpFromFloat(value float64) float64 {
	return quantizeBillingHoldUp(decimal.NewFromFloat(value))
}

func BalancePreauthorizationAttemptID(requestID string, apiKeyID int64) string {
	return strings.TrimSpace(requestID) + ":" + strconv.FormatInt(apiKeyID, 10)
}
