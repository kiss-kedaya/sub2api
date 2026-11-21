package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// A retry that succeeds bills its own result: the retained attempt of the same
// logical request is dropped, never billed twice (issue #456).
func TestRetainedOpenAIUsageRetrySuccessBillsOnlyTheRetry(t *testing.T) {
	retained := &retainedOpenAIUsage{}
	billed := []string{}
	retained.retain(func() { billed = append(billed, "first_attempt") })

	// The retry finished: its terminal result is billed.
	retained.settle(func() { billed = append(billed, "retry") })
	// The deferred safety net runs afterwards and must not bill the retained attempt.
	retained.settle(nil)

	require.Equal(t, []string{"retry"}, billed)
}

// When the failover loop gives up without a result of its own (no more accounts,
// slot exhausted, client gone), the retained attempt of the accepted upstream
// request is settled exactly once.
func TestRetainedOpenAIUsageExhaustionBillsTheAttemptOnce(t *testing.T) {
	retained := &retainedOpenAIUsage{}
	billed := 0
	retained.retain(func() { billed++ })

	retained.settle(nil)
	retained.settle(nil) // idempotent: every exit path may call it

	require.Equal(t, 1, billed)
}

// The latest attempt supersedes earlier ones: a failover chain bills the request
// once, at the most recent observed usage.
func TestRetainedOpenAIUsageLatestAttemptWins(t *testing.T) {
	retained := &retainedOpenAIUsage{}
	billed := []string{}
	retained.retain(func() { billed = append(billed, "attempt_1") })
	retained.retain(func() { billed = append(billed, "attempt_2") })

	retained.settle(nil)

	require.Equal(t, []string{"attempt_2"}, billed)
}

// A request billed through another channel (e.g. a moderation refusal with a fixed
// price) discards the retained attempt so it is not charged twice.
func TestRetainedOpenAIUsageDiscardForgetsTheAttempt(t *testing.T) {
	retained := &retainedOpenAIUsage{}
	billed := 0
	retained.retain(func() { billed++ })

	retained.discard()
	retained.settle(nil)

	require.Zero(t, billed)
}

// Nil receivers stay safe: the handler always uses a concrete value, but the helper
// must not depend on it in tests or future call sites.
func TestRetainedOpenAIUsageNilSafety(t *testing.T) {
	var nilRetained *retainedOpenAIUsage
	require.NotPanics(t, func() { nilRetained.retain(func() {}) })
	require.NotPanics(t, func() { nilRetained.discard() })

	billed := 0
	nilRetained.settle(func() { billed++ })
	require.Equal(t, 1, billed, "a nil receiver bills the terminal result as before")
}

func TestOpenAISettleUsageWrapsOnlyNonNilResults(t *testing.T) {
	submit := func(*service.OpenAIForwardResult) {}

	require.Nil(t, openAISettleUsage(nil, submit))

	result := &service.OpenAIForwardResult{}
	bill := openAISettleUsage(result, submit)
	require.NotNil(t, bill)
}
