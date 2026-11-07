package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestCooldown(now *time.Time) *ModelTransientCooldown {
	c := NewModelTransientCooldown()
	c.now = func() time.Time { return *now }
	return c
}

func TestModelTransientCooldownFirstFailureOnlyCounts(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := newTestCooldown(&now)
	require.True(t, c.Allow(1, "claude-opus-4-5"))
	c.ObserveFailure(1, "claude-opus-4-5")
	require.True(t, c.Allow(1, "claude-opus-4-5"), "one failure must not pause the account")
}

func TestModelTransientCooldownSecondFailureShortWindow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := newTestCooldown(&now)
	c.ObserveFailure(1, "claude-opus-4-5")
	c.ObserveFailure(1, "claude-opus-4-5")
	require.False(t, c.Allow(1, "claude-opus-4-5"))
	require.Equal(t, now.Add(10*time.Second), c.CooldownUntil(1, "claude-opus-4-5"))

	now = now.Add(11 * time.Second)
	require.True(t, c.Allow(1, "claude-opus-4-5"), "expired window must allow the account again")

	// The expired streak resets: a single new failure does not trigger another pause.
	c.ObserveFailure(1, "claude-opus-4-5")
	require.True(t, c.Allow(1, "claude-opus-4-5"))
}

func TestModelTransientCooldownThirdFailureLongWindow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := newTestCooldown(&now)
	for i := 0; i < 3; i++ {
		c.ObserveFailure(7, "gpt-6.1-sol")
	}
	require.False(t, c.Allow(7, "gpt-6.1-sol"))
	require.Equal(t, now.Add(45*time.Second), c.CooldownUntil(7, "gpt-6.1-sol"))
}

func TestModelTransientCooldownSuccessClearsStreak(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := newTestCooldown(&now)
	c.ObserveFailure(1, "m")
	c.ObserveFailure(1, "m")
	c.ObserveSuccess(1, "m")
	require.True(t, c.Allow(1, "m"))
	require.True(t, c.CooldownUntil(1, "m").IsZero())
}

func TestModelTransientCooldownIsScopedPerAccountAndModel(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := newTestCooldown(&now)
	c.ObserveFailure(1, "m")
	c.ObserveFailure(1, "m")
	require.False(t, c.Allow(1, "m"))
	require.True(t, c.Allow(1, "other"), "other models stay schedulable")
	require.True(t, c.Allow(2, "m"), "other accounts stay schedulable")
	require.True(t, c.Allow(1, "  M  ") == false, "model ids are case/space normalized")
}

func TestModelTransientCooldownNilAndEmptyInputsAreSafe(t *testing.T) {
	var nilCooldown *ModelTransientCooldown
	require.True(t, nilCooldown.Allow(1, "m"))
	nilCooldown.ObserveFailure(1, "m")
	nilCooldown.ObserveSuccess(1, "m")

	now := time.Unix(1_700_000_000, 0)
	c := newTestCooldown(&now)
	require.True(t, c.Allow(0, "m"))
	require.True(t, c.Allow(1, "   "))
	c.ObserveFailure(0, "")
}

func TestModelTransientCooldownEvictsExpiredEntries(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := newTestCooldown(&now)
	c.maxEntries = 2
	c.ObserveFailure(1, "a")
	c.ObserveFailure(2, "b")
	now = now.Add(modelTransientRetention + time.Minute)
	c.ObserveFailure(3, "c") // triggers eviction of the stale entries
	require.True(t, c.Allow(1, "a"), "stale entries were evicted")
	require.True(t, c.Allow(3, "c"), "a single failure only counts")
}
