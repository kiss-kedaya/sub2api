package service

import (
	"errors"
	"math"

	"github.com/shopspring/decimal"
)

var ErrInvalidBillingPreauthorizationEstimate = errors.New("invalid billing preauthorization estimate")

// LiveBalanceOutcome is the stable result of an atomic wallet operation.
// Redis and transport errors are returned separately as Go errors.
type LiveBalanceOutcome int64

const (
	LiveBalanceOutcomeInsufficient LiveBalanceOutcome = iota
	LiveBalanceOutcomeApplied
	LiveBalanceOutcomeIdempotent
	LiveBalanceOutcomeConflict
	LiveBalanceOutcomeNotFound
)

// LiveBalanceAttemptState identifies the Redis-side request state.
type LiveBalanceAttemptState int64

const (
	LiveBalanceAttemptNone LiveBalanceAttemptState = iota
	LiveBalanceAttemptAuthorized
	LiveBalanceAttemptFinalized
	LiveBalanceAttemptRefunded
)

type LiveBalanceResult struct {
	Outcome          LiveBalanceOutcome
	State            LiveBalanceAttemptState
	AvailableBalance float64
	ReservedAmount   float64
	ActualAmount     float64
}

// BillingPreauthorizationEstimateInput contains already-resolved, per-token
// prices. BillableInputBytes must be measured after request transformation so
// injected prompts and protocol bridges are included without running a tokenizer.
type BillingPreauthorizationEstimateInput struct {
	BillableInputBytes         int
	InputPricePerToken         float64
	CacheReadPricePerToken     float64
	CacheCreationPricePerToken float64
	OutputPricePerToken        float64
	RateMultiplier             float64
	InitialOutputWindowTokens  int
}

type BillingPreauthorizationEstimate struct {
	InputTokenUpperBound int
	ReservedOutputTokens int
	HoldAmount           float64
}

// EstimateBillingPreauthorization computes a conservative initial hold in O(1).
// A text token cannot encode fewer than one source byte, so transformed payload
// bytes are a safe token upper bound without tiktoken. Cache disposition is not
// known before the response, therefore the highest input/cache-read/cache-write
// price is used. A small output window bounds early output exposure without
// reserving the request's potentially huge max_tokens value.
func EstimateBillingPreauthorization(in BillingPreauthorizationEstimateInput) (BillingPreauthorizationEstimate, error) {
	if in.BillableInputBytes < 0 || in.InitialOutputWindowTokens < 0 ||
		invalidNonnegativeMoney(in.InputPricePerToken) ||
		invalidNonnegativeMoney(in.CacheReadPricePerToken) ||
		invalidNonnegativeMoney(in.CacheCreationPricePerToken) ||
		invalidNonnegativeMoney(in.OutputPricePerToken) ||
		invalidNonnegativeMoney(in.RateMultiplier) {
		return BillingPreauthorizationEstimate{}, ErrInvalidBillingPreauthorizationEstimate
	}

	inputRate := math.Max(
		in.InputPricePerToken,
		math.Max(in.CacheReadPricePerToken, in.CacheCreationPricePerToken),
	)
	inputCost := decimal.NewFromInt(int64(in.BillableInputBytes)).
		Mul(decimal.NewFromFloat(inputRate))
	outputCost := decimal.NewFromInt(int64(in.InitialOutputWindowTokens)).
		Mul(decimal.NewFromFloat(in.OutputPricePerToken))
	hold := inputCost.Add(outputCost).Mul(decimal.NewFromFloat(in.RateMultiplier))

	return BillingPreauthorizationEstimate{
		InputTokenUpperBound: in.BillableInputBytes,
		ReservedOutputTokens: in.InitialOutputWindowTokens,
		HoldAmount:           quantizeBillingHoldUp(hold),
	}, nil
}

type BillingOutputHoldTopUp struct {
	AdditionalTokens int
	AdditionalAmount float64
}

// PlanBillingOutputHoldTopUp keeps one output window ahead of the observed
// upper bound. Callers can use provider usage checkpoints when available and
// otherwise pass emitted billable bytes as the conservative token upper bound.
func PlanBillingOutputHoldTopUp(reservedTokens, observedTokenUpperBound, windowTokens int, outputPricePerToken, rateMultiplier float64) (BillingOutputHoldTopUp, error) {
	if reservedTokens < 0 || observedTokenUpperBound < 0 || windowTokens <= 0 ||
		invalidNonnegativeMoney(outputPricePerToken) || invalidNonnegativeMoney(rateMultiplier) {
		return BillingOutputHoldTopUp{}, ErrInvalidBillingPreauthorizationEstimate
	}

	target := observedTokenUpperBound + windowTokens
	if target <= reservedTokens {
		return BillingOutputHoldTopUp{}, nil
	}
	additional := target - reservedTokens
	additional = ((additional + windowTokens - 1) / windowTokens) * windowTokens
	amount := decimal.NewFromInt(int64(additional)).
		Mul(decimal.NewFromFloat(outputPricePerToken)).
		Mul(decimal.NewFromFloat(rateMultiplier))
	return BillingOutputHoldTopUp{
		AdditionalTokens: additional,
		AdditionalAmount: quantizeBillingHoldUp(amount),
	}, nil
}

func invalidNonnegativeMoney(value float64) bool {
	return value < 0 || math.IsNaN(value) || math.IsInf(value, 0)
}

func quantizeBillingHoldUp(value decimal.Decimal) float64 {
	quantized, _ := value.RoundCeil(UsageBillingMonetaryScale).Float64()
	return quantized
}
