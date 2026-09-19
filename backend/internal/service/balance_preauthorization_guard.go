package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
)

var (
	ErrBalancePreauthorizationOwnershipTransferred = errors.New("balance preauthorization ownership transferred")
	ErrBalancePreauthorizationAlreadyRefunded      = errors.New("balance preauthorization already refunded")
	ErrBalancePreauthorizationAlreadyFinalized     = errors.New("balance preauthorization already finalized")
	ErrBalancePreauthorizationOutputObserved       = errors.New("balance preauthorization output already observed")
)

type balancePreauthorizationGuardState uint8

const (
	balancePreauthorizationGuardActive balancePreauthorizationGuardState = iota
	balancePreauthorizationGuardFinalized
	balancePreauthorizationGuardRefunded
)

type balancePreauthorizationGuardCore struct {
	mu sync.Mutex

	service           *BalancePreauthorizationService
	requestID         string
	apiKeyID          int64
	userID            int64
	attemptID         string
	holdAmount        float64
	outputWindow      int
	outputHoldTracker *BillingOutputHoldTracker
	outputObserved    bool
	ownerToken        uint64
	terminalState     balancePreauthorizationGuardState
}

// BalancePreauthorizationGuard is an ownership handle, not a copyable money
// value. TransferToWorker invalidates the old handle and returns the only new
// owner, making a handler's deferred Refund harmless after task handoff.
type BalancePreauthorizationGuard struct {
	core       *balancePreauthorizationGuardCore
	ownerToken uint64
}

func (g *BalancePreauthorizationGuard) TransferToWorker() (*BalancePreauthorizationGuard, bool) {
	if g == nil || g.core == nil {
		return nil, false
	}
	g.core.mu.Lock()
	defer g.core.mu.Unlock()
	if g.core.ownerToken != g.ownerToken || g.core.terminalState != balancePreauthorizationGuardActive {
		return nil, false
	}
	g.core.ownerToken++
	return &BalancePreauthorizationGuard{core: g.core, ownerToken: g.core.ownerToken}, true
}

func (g *BalancePreauthorizationGuard) IsCurrentOwner() bool {
	if g == nil || g.core == nil {
		return false
	}
	g.core.mu.Lock()
	defer g.core.mu.Unlock()
	return g.core.ownerToken == g.ownerToken && g.core.terminalState == balancePreauthorizationGuardActive
}

func (g *BalancePreauthorizationGuard) IsTransferred() bool {
	if g == nil || g.core == nil {
		return false
	}
	g.core.mu.Lock()
	defer g.core.mu.Unlock()
	return g.core.ownerToken != g.ownerToken
}

func (g *BalancePreauthorizationGuard) HoldAmount() float64 {
	if g == nil || g.core == nil {
		return 0
	}
	g.core.mu.Lock()
	defer g.core.mu.Unlock()
	return g.core.holdAmount
}

func (g *BalancePreauthorizationGuard) ReservedOutputTokens() int {
	if g == nil || g.core == nil {
		return 0
	}
	g.core.mu.Lock()
	defer g.core.mu.Unlock()
	return g.core.outputWindow
}

// RepriceForRoute changes admission pricing before any output is observed.
// It retains the durable request/fingerprint and the same wallet attempt;
// existing holds are never released until finalization or refund.
// RequestID may be omitted; AuthorizationFingerprint must be omitted because
// repricing does not prepare or replace the durable authorization identity.
func (g *BalancePreauthorizationGuard) RepriceForRoute(ctx context.Context, request BalancePreauthorizationRequest) error {
	if g == nil || g.core == nil {
		return balancePreauthorizationUnavailable(errors.New("balance preauthorization guard is nil"))
	}
	ctx = nonNilContext(ctx)
	g.core.mu.Lock()
	defer g.core.mu.Unlock()
	if g.core.ownerToken != g.ownerToken {
		return ErrBalancePreauthorizationOwnershipTransferred
	}
	switch g.core.terminalState {
	case balancePreauthorizationGuardFinalized:
		return ErrBalancePreauthorizationAlreadyFinalized
	case balancePreauthorizationGuardRefunded:
		return ErrBalancePreauthorizationAlreadyRefunded
	}
	if g.core.outputObserved {
		return ErrBalancePreauthorizationOutputObserved
	}
	if (strings.TrimSpace(request.RequestID) != "" && strings.TrimSpace(request.RequestID) != g.core.requestID) ||
		strings.TrimSpace(request.AuthorizationFingerprint) != "" || request.APIKeyID != g.core.apiKeyID || request.UserID != g.core.userID {
		return balancePreauthorizationUnavailable(ErrInvalidBillingPreauthorizationEstimate)
	}
	if err := ctx.Err(); err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	if request.BillingType == BillingTypeSubscription {
		g.core.outputHoldTracker = nil
		g.core.outputWindow = 0
		return nil
	}
	if request.BillingType != BillingTypeBalance || request.BillableInputBytes < 0 ||
		request.EstimatedInputTokens < 0 || request.InitialOutputWindowTokens < 0 {
		return balancePreauthorizationUnavailable(ErrInvalidBillingPreauthorizationEstimate)
	}
	if g.core.service == nil || g.core.service.costCalculator == nil || g.core.service.wallet == nil {
		return balancePreauthorizationUnavailable(errors.New("balance preauthorization dependency is unavailable"))
	}
	estimate, err := g.core.service.estimateHold(ctx, request)
	if err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	if err := ctx.Err(); err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	if estimate.HoldAmount > g.core.holdAmount {
		walletCtx, cancel := context.WithTimeout(ctx, balancePreauthorizationWalletTimeout)
		defer cancel()
		if err := g.topUpHold(walletCtx, estimate.HoldAmount); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	// Use the new price's base, not the possibly larger retained hold. Output
	// consumes that surplus before any further wallet top-up is needed.
	g.core.outputHoldTracker = NewBillingOutputHoldTracker(
		estimate.OutputWindow, estimate.OutputWindow, estimate.HoldAmount, estimate.OutputUnitPrice, 1,
	)
	g.core.outputWindow = estimate.OutputWindow
	return nil
}

// wrapStreamOutputHoldTopUpFailure 为流式中途补扣失败统一包装错误，供四条流式
// 路径（passthrough/responses/Anthropic/Gemini）共用，消除各路径逐字复制的包装串。
// 必须用 %w 包装：reporter 依赖 errors.Is(cause, ErrBalanceWithholdingFailed) 识别
// 余额不足并发 403 信号，降级为 %v/%s 会断链、静默丢失该信号。err==nil 时返回 nil，
// 故调用点无需额外守卫；本函数只包装错误，绝不触碰钱包/中止/上报，中止仍由各调用点自行 return。
func wrapStreamOutputHoldTopUpFailure(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("stream output hold top-up failed: %w", err)
}

// detachedBalancePreauthorizationWalletContext keeps a money mutation alive
// when the client or upstream stream context is canceled, while retaining a
// strict bound for an unhealthy wallet backend. A delivered output chunk must
// either extend its hold or fail closed; inheriting request cancellation here
// turns a client disconnect into a false billing-service outage and can leave
// the wallet behind already-emitted output.
func detachedBalancePreauthorizationWalletContext(parent context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if parent != nil {
		base = context.WithoutCancel(parent)
	}
	return context.WithTimeout(base, balancePreauthorizationWalletTimeout)
}

// ObserveStreamingOutput records additionalBytes of emitted output and raises
// the live hold when the reserved output window is about to be exceeded. It is
// wallet-free when no tracker exists or retained holds cover the output. Even
// free output seals route pricing. Observations and repricing share core.mu.
// A returned error means the guard cannot cover output and the caller MUST
// abort the upstream stream rather than emit more billable output.
func (g *BalancePreauthorizationGuard) ObserveStreamingOutput(ctx context.Context, additionalBytes int) error {
	if g == nil || g.core == nil || additionalBytes <= 0 {
		return nil
	}
	g.core.mu.Lock()
	defer g.core.mu.Unlock()
	// Ownership/terminal checks mirror Finalize: a transferred or settled guard
	// must not keep mutating the wallet from a stale hot-path goroutine.
	if g.core.ownerToken != g.ownerToken {
		return ErrBalancePreauthorizationOwnershipTransferred
	}
	if g.core.terminalState != balancePreauthorizationGuardActive {
		return nil
	}
	g.core.outputObserved = true
	if g.core.outputHoldTracker == nil {
		return nil
	}
	g.core.outputHoldTracker.ObserveOutputBytes(additionalBytes)
	target := g.core.outputHoldTracker.TargetHoldAmount()
	if target <= g.core.holdAmount {
		return nil
	}

	walletCtx, cancel := detachedBalancePreauthorizationWalletContext(ctx)
	defer cancel()
	return g.topUpHold(walletCtx, target)
}

// topUpHold requires core.mu. The wallet's cumulative target makes retries
// after an ambiguous failure idempotent, including a zero-hold attempt.
func (g *BalancePreauthorizationGuard) topUpHold(ctx context.Context, target float64) error {
	result, err := g.core.service.wallet.TopUpLiveBalance(
		ctx, g.core.userID, g.core.attemptID, target,
	)
	if err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	if !liveBalanceAuthorizationSucceeded(result, target) {
		if result.Outcome == LiveBalanceOutcomeInsufficient {
			return ErrBalanceWithholdingFailed
		}
		return balancePreauthorizationUnavailable(fmt.Errorf(
			"top up streaming output hold returned outcome=%d state=%d",
			result.Outcome, result.State,
		))
	}
	holdRepo, ok := g.core.service.repo.(balancePreauthorizationHoldRepository)
	if !ok {
		return balancePreauthorizationUnavailable(errors.New("balance preauthorization hold repository is unavailable"))
	}
	if err := holdRepo.AdvanceBalancePreauthorizationHold(
		ctx,
		g.core.requestID,
		g.core.apiKeyID,
		result.ReservedAmount,
	); err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	g.core.holdAmount = math.Max(g.core.holdAmount, result.ReservedAmount)
	return nil
}

func (g *BalancePreauthorizationGuard) Finalize(ctx context.Context, actual float64, requestFingerprint string) error {
	if g == nil || g.core == nil {
		return nil
	}
	actual = QuantizeUsageBillingAmount(actual)
	requestFingerprint = strings.TrimSpace(requestFingerprint)
	if actual < 0 || math.IsNaN(actual) || math.IsInf(actual, 0) || requestFingerprint == "" {
		return ErrInvalidBillingPreauthorizationEstimate
	}
	ctx = nonNilContext(ctx)

	g.core.mu.Lock()
	defer g.core.mu.Unlock()
	// owner 校验必须保留：Finalize/Refund/ObserveStreamingOutput 三处共享同一所有权不变量。
	// TransferToWorker 递增 core.ownerToken 后旧 handle 失配。此处（结算）与 ObserveStreamingOutput
	// （补扣）对失配返回 ErrBalancePreauthorizationOwnershipTransferred；Refund 对失配刻意返回 nil
	// （陈旧 handler defer 的 no-op）。三者共同防止对同一预扣重复结算或撤销 worker 已产生的计费。
	if g.core.ownerToken != g.ownerToken {
		return ErrBalancePreauthorizationOwnershipTransferred
	}
	switch g.core.terminalState {
	case balancePreauthorizationGuardFinalized:
		return nil
	case balancePreauthorizationGuardRefunded:
		return ErrBalancePreauthorizationAlreadyRefunded
	}

	if err := g.core.service.repo.BeginBalancePreauthorizationFinalization(
		ctx, g.core.requestID, g.core.apiKeyID, actual, requestFingerprint,
	); err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	if actual == 0 {
		if err := g.finalizeZeroCost(ctx); err != nil {
			return err
		}
	} else if err := g.finalizePositiveCost(ctx, actual); err != nil {
		return err
	}
	g.core.terminalState = balancePreauthorizationGuardFinalized
	return nil
}

func (g *BalancePreauthorizationGuard) finalizeZeroCost(ctx context.Context) error {
	result, err := g.core.service.wallet.RefundLiveBalance(ctx, g.core.userID, g.core.attemptID)
	if err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	if !liveBalanceRefundSucceeded(result) {
		return balancePreauthorizationUnavailable(fmt.Errorf("refund zero-cost live balance returned outcome=%d state=%d", result.Outcome, result.State))
	}
	if err := g.core.service.repo.CompleteBalancePreauthorizationRefund(ctx, g.core.requestID, g.core.apiKeyID); err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	return nil
}

func (g *BalancePreauthorizationGuard) finalizePositiveCost(ctx context.Context, actual float64) error {
	result, err := g.core.service.wallet.FinalizeLiveBalance(ctx, g.core.userID, g.core.attemptID, actual)
	if err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	if !liveBalanceFinalizationSucceeded(result, actual) {
		return balancePreauthorizationUnavailable(fmt.Errorf("finalize live balance returned outcome=%d state=%d", result.Outcome, result.State))
	}
	if err := g.core.service.repo.CompleteBalancePreauthorizationSettlement(ctx, g.core.requestID, g.core.apiKeyID); err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	return nil
}

// Refund is idempotent for the current owner. A stale, transferred handle is a
// deliberate no-op so handler defer cleanup cannot undo worker-owned billing.
func (g *BalancePreauthorizationGuard) Refund(ctx context.Context) error {
	if g == nil || g.core == nil {
		return nil
	}
	ctx = nonNilContext(ctx)
	g.core.mu.Lock()
	defer g.core.mu.Unlock()
	if g.core.ownerToken != g.ownerToken {
		return nil
	}
	switch g.core.terminalState {
	case balancePreauthorizationGuardRefunded:
		return nil
	case balancePreauthorizationGuardFinalized:
		return ErrBalancePreauthorizationAlreadyFinalized
	}
	if err := g.core.service.repo.BeginBalancePreauthorizationRefund(ctx, g.core.requestID, g.core.apiKeyID); err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	result, err := g.core.service.wallet.RefundLiveBalance(ctx, g.core.userID, g.core.attemptID)
	if err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	if !liveBalanceRefundSucceeded(result) {
		return balancePreauthorizationUnavailable(fmt.Errorf("refund live balance returned outcome=%d state=%d", result.Outcome, result.State))
	}
	if err := g.core.service.repo.CompleteBalancePreauthorizationRefund(ctx, g.core.requestID, g.core.apiKeyID); err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	g.core.terminalState = balancePreauthorizationGuardRefunded
	return nil
}
