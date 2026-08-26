package service

import (
	"math"
	"sync"
)

// BillingOutputHoldTracker decides when a streaming request must raise its
// output hold, without doing any I/O. It is the CPU-cheap bridge between the
// SSE forwarding loop and the durable wallet: the hot path only feeds it byte
// counts (an O(1) integer add under a mutex) and it returns a non-zero target
// hold ONLY when an output-window boundary is crossed. A long stream therefore
// triggers at most (totalOutputTokens / windowTokens) wallet top-ups rather
// than one per frame.
//
// Byte counts are a deliberately conservative token upper bound: one output
// token always encodes at least one byte, so charging by emitted bytes can
// only over-reserve, never under-reserve. Settlement later refunds the
// difference against authoritative end-of-stream usage.
type BillingOutputHoldTracker struct {
	mu sync.Mutex

	// windowTokens is the reservation granularity. Larger windows mean fewer
	// wallet round-trips at the cost of holding slightly more headroom.
	windowTokens int
	// outputPricePerToken and rateMultiplier are frozen at preauthorization so
	// mid-stream top-ups price output identically to the initial hold.
	outputPricePerToken float64
	rateMultiplier      float64

	// reservedOutputTokens is the output-token count currently covered by the
	// live hold, including the initial window and every applied top-up.
	reservedOutputTokens int
	// observedOutputBytes accumulates emitted semantic output bytes, used as
	// the conservative observed token upper bound.
	observedOutputBytes int64
	// baseHoldAmount is the initial reserved amount (input + first output
	// window) that top-up targets are measured against; TopUpLiveBalance takes
	// a cumulative target, so each plan adds to this base.
	baseHoldAmount float64
	// toppedUpAmount is the additional output money reserved beyond the base.
	toppedUpAmount float64
}

// NewBillingOutputHoldTracker builds a tracker for a streaming request whose
// initial hold already covers windowTokens of output. Returns nil when top-ups
// cannot apply (non-positive window or free output), so the hot path simply
// skips top-up bookkeeping for those requests.
func NewBillingOutputHoldTracker(
	windowTokens int,
	reservedOutputTokens int,
	baseHoldAmount float64,
	outputPricePerToken float64,
	rateMultiplier float64,
) *BillingOutputHoldTracker {
	if windowTokens <= 0 || outputPricePerToken <= 0 || rateMultiplier <= 0 {
		return nil
	}
	if reservedOutputTokens < 0 {
		reservedOutputTokens = 0
	}
	return &BillingOutputHoldTracker{
		windowTokens:         windowTokens,
		outputPricePerToken:  outputPricePerToken,
		rateMultiplier:       rateMultiplier,
		reservedOutputTokens: reservedOutputTokens,
		baseHoldAmount:       baseHoldAmount,
	}
}

// BillingOutputHoldTopUpDecision is the outcome of observing more output. When
// Required is false the caller does nothing. When true the caller must raise
// the wallet hold to TargetHoldAmount (a cumulative target for
// TopUpLiveBalance) before emitting further output, and abort the upstream
// stream if that top-up fails.
type BillingOutputHoldTopUpDecision struct {
	Required         bool
	TargetHoldAmount float64
	AdditionalTokens int
}

// ObserveOutputBytes records additionalBytes of emitted semantic output and
// reports whether a top-up is now required. It performs no I/O: the only work
// is an integer add plus a whole-window comparison, so it is safe to call once
// per forwarded frame on the hot path. A top-up is proposed only when the
// conservative observed-token upper bound has caught up to the reserved window.
func (t *BillingOutputHoldTracker) ObserveOutputBytes(additionalBytes int) BillingOutputHoldTopUpDecision {
	if t == nil || additionalBytes <= 0 {
		return BillingOutputHoldTopUpDecision{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	t.observedOutputBytes += int64(additionalBytes)

	// One token is at least one byte, so emitted bytes bound emitted tokens
	// from above. Cap at MaxInt to keep the plan's int arithmetic well defined.
	// 该 cap 是 int64→int 窄化的 32 位安全护栏，不可删除：64 位构建下 math.MaxInt
	// == MaxInt64 且 observedOutputBytes 已是 int64，此分支为不可达死代码（需 9.2 EB
	// 流输出才触发）；仅 32 位构建下真正生效。注意即便触发，cap 后的值传入
	// PlanBillingOutputHoldTopUp 会因其 target := observed + windowTokens 溢出为负而
	// 静默返回 no-op（既有、不可达的 code-smell），故只影响 hold 上限、不影响结算守恒。
	observedUpperBound := t.observedOutputBytes
	if observedUpperBound > int64(math.MaxInt) {
		observedUpperBound = int64(math.MaxInt)
	}

	plan, err := PlanBillingOutputHoldTopUp(
		t.reservedOutputTokens,
		int(observedUpperBound),
		t.windowTokens,
		t.outputPricePerToken,
		t.rateMultiplier,
	)
	if err != nil || plan.AdditionalTokens <= 0 {
		return BillingOutputHoldTopUpDecision{}
	}

	t.reservedOutputTokens += plan.AdditionalTokens
	t.toppedUpAmount = QuantizeUsageBillingAmount(t.toppedUpAmount + plan.AdditionalAmount)
	return BillingOutputHoldTopUpDecision{
		Required:         true,
		TargetHoldAmount: QuantizeUsageBillingAmount(t.baseHoldAmount + t.toppedUpAmount),
		AdditionalTokens: plan.AdditionalTokens,
	}
}

// TargetHoldAmount returns the cumulative hold the wallet should currently
// carry, including every applied top-up.
func (t *BillingOutputHoldTracker) TargetHoldAmount() float64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return QuantizeUsageBillingAmount(t.baseHoldAmount + t.toppedUpAmount)
}
