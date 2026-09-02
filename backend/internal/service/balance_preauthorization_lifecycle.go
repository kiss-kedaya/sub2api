package service

import (
	"context"
	"errors"
	"fmt"
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
	balancePreauthorizationWalletTimeout       = 3 * time.Second
)

// 余额不足导致预扣/流式续扣失败时映射为 403，而非 402/429，这是刻意的计费决策：
// 属于客户端无法靠重试或等待消除的确定性拒绝（额度不足即拒绝本次请求）。
//   - 不用 429：429 会诱导客户端退避后重试，放大无法结算的无效请求负载；
//   - 不用 402：402 虽不诱导退避，但语义上暗示"充值即可放行本次"，与此处的确定性拒绝
//     不符，且用 403 与上游权限类拒绝语义保持一致。
//
// 注意：该 sentinel 同时用于入口预扣(lifecycle line 243)与流式续扣(guard.go:130、
// passthrough:1787)两条路径；两处返回前均已执行 compensateAuthorizationFailure/退款，
// 不会漏扣或残留 hold——修改状态码不得改变这一补偿前置。
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
	// PreauthorizationEstimateTokenUpperBound prices a request-local input-token
	// estimate plus an explicit or bounded output window. Provider usage remains
	// authoritative at settlement; this estimate is only the initial hold.
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
// the request. For the token estimate kind, EstimatedInputTokens (falling back
// to BillableInputBytes) and InitialOutputWindowTokens determine the initial
// hold; CostInput.Tokens is ignored. For the per-request estimate kind,
// PerRequestEstimate supplies the billing units and the output window is unused.
type BalancePreauthorizationRequest struct {
	RequestID                 string
	APIKeyID                  int64
	UserID                    int64
	AuthorizationFingerprint  string
	BillingType               int8
	BillableInputBytes        int
	EstimatedInputTokens      int
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
	return s.cfg == nil || (s.cfg.RunMode != config.RunModeSimple && s.cfg.Billing.BalancePreauthorizationEnabled)
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
	if s.cfg != nil && (s.cfg.RunMode == config.RunModeSimple || !s.cfg.Billing.BalancePreauthorizationEnabled) {
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
	// 活期钱包缓存缺失（冷启动或被驱逐）时，从 PG 权威快照按其 watermark
	//（外部调整 outbox 的重放边界）在水位处初始化后再授权。
	// allowInitialize 传 !HasUnsettled 是关键守恒条件：HasUnsettled 表示存在
	// Authorized/FinalizationPending/Pending 的用量结算（在途 hold），此时 PG
	// u.balance 未必反映这些在途扣减，用快照重建会漏扣（掩盖在途占用）或重扣
	//（已扣金额被恢复后再次结算），故仅在无未结算时才允许初始化；存在未结算时
	// 本调用返回 NotFound，随后走 compensate + unavailable 失败闭合，绝不带病初始化。
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

// estimateTokenUpperBoundHold prices chat/text traffic from the current
// request's local token estimate. The reserve uses the normal input price and
// an explicit output limit (or bounded window), then finalization reconciles it
// to provider-reported usage. No historical usage query belongs on this path.
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
	inputTokens := request.EstimatedInputTokens
	if inputTokens <= 0 {
		inputTokens = request.BillableInputBytes
	}
	input := base
	input.Tokens = UsageTokens{InputTokens: inputTokens, OutputTokens: outputWindow}
	breakdown, err := s.costCalculator.CalculateCostUnified(input)
	if err != nil {
		return balancePreauthorizationEstimate{}, err
	}
	if breakdown == nil || invalidNonnegativeMoney(breakdown.ActualCost) {
		return balancePreauthorizationEstimate{}, ErrInvalidBillingPreauthorizationEstimate
	}

	// Effective per-output-token price via difference: the output window is the
	// only term that varies between the plain-input scenario already priced
	// above and an output-free baseline, so (windowed - baseline) / windowTokens
	// matches the exact policy (intervals, priority tier, long-context, time
	// multipliers) used at settlement without reaching into pricing internals.
	// Reuse the already-computed windowed breakdown. Only the output-free
	// baseline requires another pricing call; recomputing the same windowed
	// amount here added an avoidable resolver/calculator pass on every paid
	// streaming request.
	outputUnitPrice, err := s.estimateOutputUnitPrice(base, inputTokens, outputWindow, breakdown)
	if err != nil {
		return balancePreauthorizationEstimate{}, err
	}
	return balancePreauthorizationEstimate{
		HoldAmount:      quantizeBillingHoldUpFromFloat(breakdown.ActualCost),
		OutputWindow:    outputWindow,
		OutputUnitPrice: outputUnitPrice,
	}, nil
}

// estimateOutputUnitPrice derives the effective per-output-token price from a
// windowed-minus-baseline cost difference. Both the windowed and baseline
// prices are computed here over the SAME plain-input disposition (InputTokens
// only), differing solely in the output window, so the difference isolates the
// output marginal price regardless of how the hold itself weights cache_read vs
// input. This keeps output-unit-price derivation self-contained and decoupled
// from the cache-aware hold estimate. Output pricing is independent of input
// disposition, so one representative input scenario suffices.
// Returns zero (top-ups disabled) for non-positive results.
func (s *BalancePreauthorizationService) estimateOutputUnitPrice(
	base CostInput,
	inputTokens int,
	outputWindow int,
	windowedBreakdown *CostBreakdown,
) (float64, error) {
	if outputWindow <= 0 {
		return 0, nil
	}
	if windowedBreakdown == nil || invalidNonnegativeMoney(windowedBreakdown.ActualCost) {
		return 0, ErrInvalidBillingPreauthorizationEstimate
	}
	baseline := base
	baseline.Tokens = UsageTokens{InputTokens: inputTokens, OutputTokens: 0}
	baselineCost, err := s.costCalculator.CalculateCostUnified(baseline)
	if err != nil {
		return 0, err
	}
	if windowedBreakdown == nil || invalidNonnegativeMoney(windowedBreakdown.ActualCost) ||
		baselineCost == nil || invalidNonnegativeMoney(baselineCost.ActualCost) {
		return 0, ErrInvalidBillingPreauthorizationEstimate
	}
	delta := windowedBreakdown.ActualCost - baselineCost.ActualCost
	// delta<=0 表示在当前定价下，输出窗口未产生额外边际成本（免费输出，或已并入
	// 打包/按次价，由 maxCost 场景 hold 覆盖）。此时返回 0 会使 OutputUnitPrice=0，
	// NewBillingOutputHoldTracker 返回 nil（见 billing_output_hold_tracker.go），
	// 从而禁用流式补扣——这是安全的：既然输出无边际计价，长流不会随长度增加欠扣，
	// 结算时仍按实际用量退差。
	// 前提1（守恒关键）：差分必须用与 baseline 同一输入处置（plain-input）的 windowed
	// 成本（firstScenarioCost / scenarios[0]），否则会把输入/缓存侧价差混入输出单价，
	// 污染补扣目标额。切勿对真实计费的输出误禁补扣。
	// 前提2：差分仅在 outputWindow 处单点采样边际价，故"长流不欠扣"依赖输出边际价在
	// 整段流长上均匀；若将来出现阶梯/阈值型输出定价（窗口内免费、越过阈值才计费），
	// 该分支需改为不在此禁用补扣，否则长流会欠扣。
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
// resolvedPricingCostInput freezes Resolver.Resolve once so the hold and the
// output-baseline probe use one pricing snapshot. Re-resolving per probe would
// add I/O and could make a single request observe different prices.
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

// compensateAuthorizationFailure 在授权阶段任何不确定结果（授权报错/余额不足/结果校验失败/
// MarkAuthorized 失败）时回滚。此路径必发生在上游请求之前且记录仍处 Prepared 态，故"全额退回
// 钱包 hold + 将 PG 预扣转入退款态"是守恒安全的：上游未产生费用则全额退款，用户不会被扣款却无对应请求。
// 该补偿是尽力而为：用独立后台 ctx（不随请求 ctx 取消，保证客户端断开仍退款）+ 3s 有界超时，
// 且忽略 begin/refund/complete 的错误——因为 PG 记录持久，失败时记录仍停留在可恢复的非终态，
// balance preauthorization 恢复 worker 会将其重新全额退款收敛（仅 Prepared 态才允许放弃授权退款；
// 已 Authorized 的记录由恢复路径改为全额结算，避免把崩溃窗口变成免费调用），因此这里既不阻塞也不永久卡死请求路径。
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
		request.BillableInputBytes < 0 || request.EstimatedInputTokens < 0 || request.InitialOutputWindowTokens < 0 {
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
