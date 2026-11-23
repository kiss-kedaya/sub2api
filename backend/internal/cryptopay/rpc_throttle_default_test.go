package cryptopay

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// Pacing is opt-in. Pacing every call by default once made a stage over 13 address
// batches take 2.4s instead of 5ms, kept the idle wallet guard scan holding the
// global scan lock back to back, and stopped BSC, Ethereum and Polygon from
// advancing. An operator who set nothing must get no pacing at all.
func TestUnsetRPCThrottleMeansNoPacing(t *testing.T) {
	t.Cleanup(func() { rpc.SetEndpointThrottleInterval(0) })

	applyRPCThrottle(CryptoConfig{})
	if rpc.ThrottleFor("https://unset.example") != nil {
		t.Fatal("an unset rpc_throttle_ms must not pace calls")
	}
	applyRPCThrottle(CryptoConfig{RPCThrottleMs: -5})
	if rpc.ThrottleFor("https://negative.example") != nil {
		t.Fatal("a negative value must not pace calls")
	}
	applyRPCThrottle(CryptoConfig{RPCThrottleMs: 40})
	throttle := rpc.ThrottleFor("https://explicit.example")
	if throttle == nil {
		t.Fatal("an explicit value must pace calls")
	}
	_ = throttle.Wait(t.Context())
	start := time.Now()
	_ = throttle.Wait(t.Context())
	if got := time.Since(start); got < 30*time.Millisecond {
		t.Fatalf("an explicit 40ms setting must space calls, second call waited %v", got)
	}
}
