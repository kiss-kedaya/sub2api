package cryptopay

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

func init() {
	// Every test in this package shares one process and one global throttle
	// registry; production's default pacing would make each of them wait on every
	// call. They run unpaced, as they did before the default existed, and the test
	// below puts the production default back for its own duration.
	defaultRPCThrottle = 0
}

// An operator who never touched rpc_throttle_ms still gets paced calls: unpaced,
// one scan stage fires its batches together, the provider answers 429, and the
// pool's cooldown then releases every waiting caller at the same instant again.
func TestApplyRPCThrottleUsesDefaultPacingWhenUnset(t *testing.T) {
	production := defaultRPCThrottle
	defaultRPCThrottle = 150 * time.Millisecond
	t.Cleanup(func() {
		defaultRPCThrottle = production
		rpc.SetEndpointThrottleInterval(0)
	})
	second := func(endpoint string) time.Duration {
		throttle := rpc.ThrottleFor(endpoint)
		if throttle == nil {
			t.Fatal("this configuration must pace calls")
		}
		_ = throttle.Wait(context.Background())
		start := time.Now()
		_ = throttle.Wait(context.Background())
		return time.Since(start)
	}

	applyRPCThrottle(CryptoConfig{})
	if got := second("https://unset.example"); got < 100*time.Millisecond {
		t.Fatalf("unset config must pace by default (about %v), second call waited %v", defaultRPCThrottle, got)
	}
	applyRPCThrottle(CryptoConfig{RPCThrottleMs: 1})
	if got := second("https://explicit.example"); got > 50*time.Millisecond {
		t.Fatalf("an explicit 1ms setting must all but disable pacing, second call waited %v", got)
	}
}
