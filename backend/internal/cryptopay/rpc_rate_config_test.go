package cryptopay

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/stretchr/testify/require"
)

// rpc_rate_per_sec is a new optional key: validated like its neighbours, patched
// like them, and absent from the stored JSON until someone sets it, so a config
// saved before it existed keeps exactly the same fingerprint.
func TestRPCRatePerSecIsValidatedPatchedAndOmittedWhenUnset(t *testing.T) {
	for _, bad := range []int{-1, maxRPCRatePerSec + 1} {
		_, err := NormalizeConfig(CryptoConfig{RPCRatePerSec: bad})
		require.Error(t, err, "rpc_rate_per_sec=%d", bad)
	}
	for _, good := range []int{0, 1, 20, maxRPCRatePerSec} {
		cfg, err := NormalizeConfig(CryptoConfig{RPC: map[string]string{ChainBSC: "https://rpc.example"}, RPCRatePerSec: good})
		require.NoError(t, err, "rpc_rate_per_sec=%d", good)
		require.Equal(t, good, cfg.RPCRatePerSec)
	}

	raw, err := json.Marshal(CryptoConfig{})
	require.NoError(t, err)
	require.NotContains(t, string(raw), "rpc_rate_per_sec", "an unset key must not appear in stored configs")

	rate := 35
	merged := CryptoConfigPatch{RPCRatePerSec: &rate}.Apply(CryptoConfig{RPCThrottleMs: 40})
	require.Equal(t, 35, merged.RPCRatePerSec)
	require.Equal(t, 40, merged.RPCThrottleMs, "other keys are untouched")
	require.Equal(t, 9, CryptoConfigPatch{}.Apply(CryptoConfig{RPCRatePerSec: 9}).RPCRatePerSec, "an omitted key keeps the stored value")
}

func TestApplyRPCThrottleSetsTheRateCeiling(t *testing.T) {
	t.Cleanup(func() {
		rpc.SetEndpointRateCeiling(0)
		rpc.SetEndpointThrottleInterval(0)
	})
	applyRPCThrottle(CryptoConfig{})
	require.Equal(t, 20.0, rpc.EndpointRateCeiling(), "unset means the default")
	applyRPCThrottle(CryptoConfig{RPCRatePerSec: 7})
	require.Equal(t, 7.0, rpc.EndpointRateCeiling())
	applyRPCThrottle(CryptoConfig{RPCRatePerSec: maxRPCRatePerSec * 5}) // never stored, but never trusted either
	require.Equal(t, float64(maxRPCRatePerSec), rpc.EndpointRateCeiling())
	applyRPCThrottle(CryptoConfig{})
	require.Equal(t, 20.0, rpc.EndpointRateCeiling(), "removing the key restores the default on the next reload")
	require.Nil(t, rpc.ThrottleFor("https://spacing.example"), "the rate ceiling does not switch the opt-in fixed spacing on")
}

// The node-status page keeps every field it had and gains the traffic counters of
// the node, as numbers only.
func TestChainStatusNodeCarriesTrafficCounters(t *testing.T) {
	a := "https://10.255.255.1/bsc/SECRETKEY" // a private address: the client refuses it, which is a recorded connection error
	_, _ = rpc.NewPool(a).BlockNumber(context.Background())
	cfg, pools := bscOnly(a)
	node := newFakeNode(t, fakeNodeOpts{chainID: 56, head: 10_000})
	h := newStatusHarness(t, cfg, pools, map[string]*fakeNode{a: node})
	h.expectCursors(cursorRow{ChainBSC, 9_990, h.clock.Now().Add(-5 * time.Second)})
	h.expectPending(nil)

	rep, err := h.svc.ChainStatus(context.Background())
	require.NoError(t, err)
	got := byChain(rep)[ChainBSC].Nodes[0]
	require.Equal(t, NodeStatusOK, got.Status, "the existing fields are unchanged")
	require.Equal(t, "10.255.255.1", got.Host)
	require.NotNil(t, got.Traffic)
	require.EqualValues(t, 1, got.Traffic.Requests)
	require.EqualValues(t, 1, got.Traffic.ConnErrors)

	raw, err := json.Marshal(got)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "SECRETKEY")
	var object map[string]any
	require.NoError(t, json.Unmarshal(raw, &object))
	require.Equal(t, []string{"avg_ms", "conn_errors", "http_402", "http_429", "http_other", "max_ms", "new_conns", "ok",
		"rate_per_sec", "requests", "stale_retries", "throttled", "timeouts", "window_seconds"},
		keysOf(chainStatusJSONObject(t, object["traffic"])))
}
