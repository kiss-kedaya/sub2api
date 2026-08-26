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
	return g.core.holdAmount
}

func (g *BalancePreauthorizationGuard) ReservedOutputTokens() int {
	if g == nil || g.core == nil {
		return 0
	}
	return g.core.outputWindow
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
// a no-op (nil) when no tracker exists (per-request/free/non-stream requests),
// when the guard is not the active owner, or when the observation stays within
// the reserved lead — so the hot path pays only an integer add in the common
// case. A returned error means the wallet top-up failed and the caller MUST
// abort the upstream stream rather than emit more billable output.
func (g *BalancePreauthorizationGuard) ObserveStreamingOutput(ctx context.Context, additionalBytes int) error {
	if g == nil || g.core == nil || g.core.outputHoldTracker == nil || additionalBytes <= 0 {
		return nil
	}
	decision := g.core.outputHoldTracker.ObserveOutputBytes(additionalBytes)
	if !decision.Required {
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

	walletCtx, cancel := detachedBalancePreauthorizationWalletContext(ctx)
	defer cancel()
	result, err := g.core.service.wallet.TopUpLiveBalance(
		walletCtx, g.core.userID, g.core.attemptID, decision.TargetHoldAmount,
	)
	if err != nil {
		return balancePreauthorizationUnavailable(err)
	}
	if !liveBalanceOperationSucceeded(result, LiveBalanceAttemptAuthorized) {
		if result.Outcome == LiveBalanceOutcomeInsufficient {
			return ErrBalanceWithholdingFailed
		}
		return balancePreauthorizationUnavailable(fmt.Errorf(
			"top up streaming output hold returned outcome=%d state=%d",
			result.Outcome, result.State,
		))
	}
	g.core.holdAmount = decision.TargetHoldAmount
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
