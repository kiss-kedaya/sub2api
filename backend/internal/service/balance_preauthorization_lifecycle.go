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
	TopUpLiveBalance(ctx context.Context, userID int64, attemptID string, targetHoldAmount float64) (LiveBalanceResult, error)
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

// PreauthorizationEstimateKind selects how estimateHold prices a request.
// The token upper-bound path is the historical default for chat/text traffic;
// the per-request path prices count/size/duration-metered endpoints (images,
// video, standalone search) directly through the unified cost engine.
type PreauthorizationEstimateKind uint8

const (
	// PreauthorizationEstimateTokenUpperBound treats BillableInputBytes as a
	// conservative token upper bound and holds the largest of the input,
	// cache-read, and cache-creation pricing scenarios plus an output window.
	PreauthorizationEstimateTokenUpperBound PreauthorizationEstimateKind = iota
	// PreauthorizationEstimatePerRequest prices the request once from explicit
	// per-request billing units (image count, size tier, video seconds) using
	// the same BillingModeImage/Video/PerRequest path as post-usage settlement.
	PreauthorizationEstimatePerRequest
)

// PerRequestPreauthorizationEstimate carries the already-parsed billing units
// for a count/size/duration-metered endpoint. All fields mirror CostInput so
// the reserved hold is priced with the exact policy used at settlement.
type PerRequestPreauthorizationEstimate struct {
	// RequestCount is the number of billable units (e.g. images requested).
	RequestCount int
	// UsageUnits is a continuous billable quantity (e.g. total video seconds).
	// When positive it takes precedence over RequestCount in the cost engine.
	UsageUnits float64
	// SizeTier is the per-request size label ("1K"/"2K"/"4K"/"HD" ...).
	SizeTier string
}

// BalancePreauthorizationRequest carries the exact pricing context frozen for
// the request. For the token upper-bound estimate kind, Tokens in CostInput are
// ignored: the service prices the same conservative byte upper bound as input,
// cache-read, and cache-creation and holds the largest result. For the
// per-request estimate kind, PerRequestEstimate supplies the billing units and
// the output window is unused.
type BalancePreauthorizationRequest struct {
	RequestID                 string
	APIKeyID                  int64
	UserID                    int64
	AuthorizationFingerprint  string
	BillingType               int8
	BillableInputBytes        int
	InitialOutputWindowTokens int
	CostInput                 CostInput
	EstimateKind              PreauthorizationEstimateKind
	PerRequestEstimate        PerRequestPreauthorizationEstimate
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

	estimate, err := s.estimateHold(ctx, request)
	if err != nil {
		return nil, balancePreauthorizationUnavailable(err)
	}
	holdAmount := estimate.HoldAmount
	outputWindow := estimate.OutputWindow
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
	// Streaming top-up tracker: only for token-metered requests with a positive
	// output window and non-free output. NewBillingOutputHoldTracker returns nil
	// otherwise, so the hot path stays a no-op for per-request and free traffic.
	core.outputHoldTracker = NewBillingOutputHoldTracker(
		outputWindow,
		outputWindow,
		record.HoldAmount,
		estimate.OutputUnitPrice,
		1,
	)
	return &BalancePreauthorizationGuard{core: core, ownerToken: 1}, nil
}

// balancePreauthorizationEstimate is the frozen pricing result of a request.
// OutputUnitPrice is the effective per-output-token price (after all pricing
// policy), used to plan streaming top-ups; it is zero for per-request endpoints
// and for free output, in which case no streaming tracker is created.
type balancePreauthorizationEstimate struct {
	HoldAmount      float64
	OutputWindow    int
	OutputUnitPrice float64
}

// estimateHold dispatches to the pricing strategy named by the request. Both
// strategies resolve pricing before any billing-state mutation and fail closed
// (returning an error) rather than emitting a zero hold for an unknown model.
func (s *BalancePreauthorizationService) estimateHold(
	ctx context.Context,
	request BalancePreauthorizationRequest,
) (balancePreauthorizationEstimate, error) {
	switch request.EstimateKind {
	case PreauthorizationEstimatePerRequest:
		return s.estimatePerRequestHold(ctx, request)
	default:
		return s.estimateTokenUpperBoundHold(ctx, request)
	}
}

// estimateTokenUpperBoundHold prices chat/text traffic. A text token cannot
// encode fewer than one source byte, so transformed payload bytes are a safe
// token upper bound; the largest of the input/cache-read/cache-creation
// scenarios plus a bounded output window is reserved.
func (s *BalancePreauthorizationService) estimateTokenUpperBoundHold(
	ctx context.Context,
	request BalancePreauthorizationRequest,
) (balancePreauthorizationEstimate, error) {
	outputWindow := request.InitialOutputWindowTokens
	if outputWindow == 0 {
		outputWindow = DefaultBalancePreauthorizationOutputWindow
	}
	base, err := s.resolvedPricingCostInput(ctx, request.CostInput)
	if err != nil {
		return balancePreauthorizationEstimate{}, err
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
	firstScenarioCost := 0.0
	for i, tokens := range scenarios {
		input := base
		input.Tokens = tokens
		breakdown, err := s.costCalculator.CalculateCostUnified(input)
		if err != nil {
			return balancePreauthorizationEstimate{}, err
		}
		if breakdown == nil || invalidNonnegativeMoney(breakdown.ActualCost) {
			return balancePreauthorizationEstimate{}, ErrInvalidBillingPreauthorizationEstimate
		}
		if i == 0 {
			firstScenarioCost = breakdown.ActualCost
		}
		maxCost = math.Max(maxCost, breakdown.ActualCost)
	}

	// Effective per-output-token price via difference: the output window is the
	// only term that varies between the plain-input scenario already priced
	// above and an output-free baseline, so (windowed - baseline) / windowTokens
	// matches the exact policy (intervals, priority tier, long-context, time
	// multipliers) used at settlement without reaching into pricing internals.
	// Only one extra pricing call runs, once per request at preauthorization,
	// never on the hot path.
	outputUnitPrice, err := s.estimateOutputUnitPrice(base, request.BillableInputBytes, outputWindow, firstScenarioCost)
	if err != nil {
		return balancePreauthorizationEstimate{}, err
	}
	return balancePreauthorizationEstimate{
		HoldAmount:      quantizeBillingHoldUpFromFloat(maxCost),
		OutputWindow:    outputWindow,
		OutputUnitPrice: outputUnitPrice,
	}, nil
}

// estimateOutputUnitPrice derives the effective per-output-token price from the
// windowed-minus-baseline cost difference. The windowed cost is the already
// priced plain-input scenario (InputTokens + outputWindow), so only the
// output-free baseline needs an extra pricing call. Output pricing is
// independent of input disposition, so one representative scenario suffices.
// Returns zero (top-ups disabled) for non-positive results.
func (s *BalancePreauthorizationService) estimateOutputUnitPrice(
	base CostInput,
	billableInputBytes int,
	outputWindow int,
	windowedCost float64,
) (float64, error) {
	if outputWindow <= 0 {
		return 0, nil
	}
	baseline := base
	baseline.Tokens = UsageTokens{InputTokens: billableInputBytes, OutputTokens: 0}
	baselineCost, err := s.costCalculator.CalculateCostUnified(baseline)
	if err != nil {
		return 0, err
	}
	if baselineCost == nil || invalidNonnegativeMoney(baselineCost.ActualCost) ||
		invalidNonnegativeMoney(windowedCost) {
		return 0, ErrInvalidBillingPreauthorizationEstimate
	}
	delta := windowedCost - baselineCost.ActualCost
	if delta <= 0 {
		return 0, nil
	}
	return delta / float64(outputWindow), nil
}

// estimatePerRequestHold prices count/size/duration-metered endpoints once
// through the unified cost engine's per-request path. The reserved hold is the
// exact request price; settlement later refunds any positive difference. No
// output window applies, so the reserved-token field is reported as zero.
func (s *BalancePreauthorizationService) estimatePerRequestHold(
	ctx context.Context,
	request BalancePreauthorizationRequest,
) (balancePreauthorizationEstimate, error) {
	base, err := s.resolvedPricingCostInput(ctx, request.CostInput)
	if err != nil {
		return balancePreauthorizationEstimate{}, err
	}
	base.RequestCount = request.PerRequestEstimate.RequestCount
	base.UsageUnits = request.PerRequestEstimate.UsageUnits
	base.SizeTier = request.PerRequestEstimate.SizeTier

	breakdown, err := s.costCalculator.CalculateCostUnified(base)
	if err != nil {
		return balancePreauthorizationEstimate{}, err
	}
	if breakdown == nil || invalidNonnegativeMoney(breakdown.ActualCost) {
		return balancePreauthorizationEstimate{}, ErrInvalidBillingPreauthorizationEstimate
	}
	return balancePreauthorizationEstimate{
		HoldAmount:   quantizeBillingHoldUpFromFloat(breakdown.ActualCost),
		OutputWindow: 0,
	}, nil
}

// resolvedPricingCostInput freezes the pricing resolution once so an unknown
// paid model fails closed before any wallet mutation. A missing resolver is
// left untouched: CalculateCostUnified falls back to its legacy pricing path.
func (s *BalancePreauthorizationService) resolvedPricingCostInput(
	ctx context.Context,
	base CostInput,
) (CostInput, error) {
	base.Ctx = ctx
	if base.Resolved == nil && base.Resolver != nil {
		base.Resolved = base.Resolver.Resolve(ctx, PricingInput{
			Model:   base.Model,
			GroupID: base.GroupID,
			Group:   base.Group,
		})
		if base.Resolved == nil {
			return CostInput{}, ErrModelPricingUnavailable
		}
	}
	return base, nil
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
