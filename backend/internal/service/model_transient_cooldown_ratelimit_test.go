package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRateLimitServiceTransientCooldownCountsProviderFaults(t *testing.T) {
	rl := &RateLimitService{modelTransient: NewModelTransientCooldown()}
	account := &Account{ID: 42}

	rl.recordModelTransientOutcome(account, 503, []string{"claude-opus-4-5"})
	require.True(t, rl.ModelTransientAllowed(42, "claude-opus-4-5"), "one fault only counts")

	rl.recordModelTransientOutcome(account, 502, []string{"claude-opus-4-5"})
	require.False(t, rl.ModelTransientAllowed(42, "claude-opus-4-5"), "two faults pause the pair")

	// A different model or account is unaffected.
	require.True(t, rl.ModelTransientAllowed(42, "gpt-6.1-sol"))
	require.True(t, rl.ModelTransientAllowed(43, "claude-opus-4-5"))
}

func TestRateLimitServiceTransientCooldownIgnoresClientAndRateLimitErrors(t *testing.T) {
	rl := &RateLimitService{modelTransient: NewModelTransientCooldown()}
	account := &Account{ID: 1}
	for _, status := range []int{400, 401, 403, 404, 409, 422, 429} {
		rl.recordModelTransientOutcome(account, status, []string{"m"})
	}
	require.True(t, rl.ModelTransientAllowed(1, "m"), "client and rate-limit errors must not feed the transient cooldown")
}

func TestRateLimitServiceTransientCooldownNilSafe(t *testing.T) {
	var rl *RateLimitService
	require.True(t, rl.ModelTransientAllowed(1, "m"))
	rl.recordModelTransientOutcome(&Account{ID: 1}, 503, []string{"m"})

	empty := &RateLimitService{}
	require.True(t, empty.ModelTransientAllowed(1, "m"))
	empty.recordModelTransientOutcome(&Account{ID: 1}, 503, []string{"m"})
	empty.recordModelTransientOutcome(nil, 503, []string{"m"})
	empty.recordModelTransientOutcome(&Account{ID: 1}, 503, nil)
}
