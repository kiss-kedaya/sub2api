package service

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBillingOutputHoldTrackerKeepsOneWindowAhead proves the tracker reserves
// one whole window beyond the observed upper bound, tops up in whole-window
// steps, and stays silent while still within the reserved lead. This bounds
// wallet round-trips to (output / window) regardless of frame count.
func TestBillingOutputHoldTrackerKeepsOneWindowAhead(t *testing.T) {
	// window=100 tokens, initial hold covers the first 100-token window.
	// output price 0.001, multiplier 1 => one window costs 0.1.
	tracker := NewBillingOutputHoldTracker(100, 100, 0.5, 0.001, 1)
	require.NotNil(t, tracker)

	// First output observed (40 bytes): plan keeps one window ahead of the
	// observed upper bound, so reserved 100 -> 200 (target grows by 0.1).
	first := tracker.ObserveOutputBytes(40)
	require.True(t, first.Required)
	require.Equal(t, 100, first.AdditionalTokens)
	require.InDelta(t, 0.6, first.TargetHoldAmount, 1e-9)

	// Still within the reserved lead (observed 80 < reserved 200): no top-up.
	require.False(t, tracker.ObserveOutputBytes(40).Required) // total 80
	require.InDelta(t, 0.6, tracker.TargetHoldAmount(), 1e-9)

	// Crossing into the next window (observed 120 -> needs lead to 220) tops up
	// one more window: reserved 200 -> 300.
	second := tracker.ObserveOutputBytes(40) // total 120
	require.True(t, second.Required)
	require.Equal(t, 100, second.AdditionalTokens)
	require.InDelta(t, 0.7, second.TargetHoldAmount, 1e-9)
}

// TestBillingOutputHoldTrackerJumpsMultipleWindows proves a single large burst
// reserves enough whole windows to cover the observed upper bound in one step.
func TestBillingOutputHoldTrackerJumpsMultipleWindows(t *testing.T) {
	tracker := NewBillingOutputHoldTracker(100, 100, 0.5, 0.001, 1)
	require.NotNil(t, tracker)

	// 450 bytes observed while 100 reserved: plan must cover up to 450 + window.
	decision := tracker.ObserveOutputBytes(450)
	require.True(t, decision.Required)
	// target = observed(450) + window(100) - reserved(100) = 450, rounded up to
	// whole windows => 500 additional tokens.
	require.Equal(t, 500, decision.AdditionalTokens)
	require.InDelta(t, 0.5+0.5, decision.TargetHoldAmount, 1e-9)
}

// TestBillingOutputHoldTrackerDisabledForFreeOrInvalid proves the tracker is
// nil (a no-op for the hot path) when top-ups cannot apply, so free-output or
// misconfigured requests never attempt wallet writes.
func TestBillingOutputHoldTrackerDisabledForFreeOrInvalid(t *testing.T) {
	require.Nil(t, NewBillingOutputHoldTracker(0, 100, 0.5, 0.001, 1))
	require.Nil(t, NewBillingOutputHoldTracker(100, 100, 0.5, 0, 1))
	require.Nil(t, NewBillingOutputHoldTracker(100, 100, 0.5, 0.001, 0))

	var nilTracker *BillingOutputHoldTracker
	require.False(t, nilTracker.ObserveOutputBytes(1000).Required)
	require.Zero(t, nilTracker.TargetHoldAmount())
}

// TestBillingOutputHoldTrackerConcurrentObserveIsSafe proves concurrent frame
// observations do not race and reserved tokens never regress.
func TestBillingOutputHoldTrackerConcurrentObserveIsSafe(t *testing.T) {
	tracker := NewBillingOutputHoldTracker(10, 10, 0.01, 0.001, 1)
	require.NotNil(t, tracker)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tracker.ObserveOutputBytes(20)
		}()
	}
	wg.Wait()

	// 50 * 20 = 1000 observed bytes; final target must cover at least 1000
	// tokens of output beyond the initial window and be internally consistent.
	require.GreaterOrEqual(t, tracker.TargetHoldAmount(), 1.0)
}
