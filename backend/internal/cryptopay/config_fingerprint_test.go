package cryptopay

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The fingerprint decides whether a stored-config change triggers a reload, so
// it must move for anything that changes RPC behaviour and stay put otherwise.
// Getting the second half wrong reloads pools every ten minutes for no reason.
func TestConfigFingerprintTracksReloadRelevantFields(t *testing.T) {
	base := CryptoConfig{
		Enabled:       true,
		Chains:        []string{"bsc", "ethereum"},
		RPC:           map[string]string{"bsc": "https://a", "ethereum": "https://b"},
		ScanRange:     map[string]int{"bsc": 500, "ethereum": 2000},
		Confirmations: map[string]int{"bsc": 3, "ethereum": 64},
	}
	same := base
	same.RPC = map[string]string{"bsc": "https://a", "ethereum": "https://b"}
	same.ScanRange = map[string]int{"bsc": 500, "ethereum": 2000}
	same.Confirmations = map[string]int{"bsc": 3, "ethereum": 64}

	require.Equal(t, configFingerprint(base), configFingerprint(same),
		"an equal config must not look changed")

	t.Run("rpc endpoint changed", func(t *testing.T) {
		mutated := base
		mutated.RPC = map[string]string{"bsc": "https://new", "ethereum": "https://b"}
		require.NotEqual(t, configFingerprint(base), configFingerprint(mutated))
	})

	t.Run("rpc limits changed", func(t *testing.T) {
		mutated := base
		mutated.RPCLimits = map[string]RPCEndpointLimits{"https://a": {MaxBlocks: 100, MaxAddresses: 20}}
		require.NotEqual(t, configFingerprint(base), configFingerprint(mutated))
	})

	t.Run("rpc throttle changed", func(t *testing.T) {
		mutated := base
		mutated.RPCThrottleMs = 250
		require.NotEqual(t, configFingerprint(base), configFingerprint(mutated))
	})

	t.Run("scan range changed", func(t *testing.T) {
		mutated := base
		mutated.ScanRange = map[string]int{"bsc": 250, "ethereum": 2000}
		require.NotEqual(t, configFingerprint(base), configFingerprint(mutated))
	})

	t.Run("confirmations changed", func(t *testing.T) {
		mutated := base
		mutated.Confirmations = map[string]int{"bsc": 5, "ethereum": 64}
		require.NotEqual(t, configFingerprint(base), configFingerprint(mutated))
	})

	t.Run("chain removed", func(t *testing.T) {
		mutated := base
		mutated.Chains = []string{"bsc"}
		require.NotEqual(t, configFingerprint(base), configFingerprint(mutated))
	})

	t.Run("enabled toggled", func(t *testing.T) {
		mutated := base
		mutated.Enabled = false
		require.NotEqual(t, configFingerprint(base), configFingerprint(mutated))
	})

	t.Run("map order does not matter", func(t *testing.T) {
		a := base
		a.RPC = map[string]string{"bsc": "https://a", "ethereum": "https://b"}
		b := base
		b.RPC = map[string]string{"ethereum": "https://b", "bsc": "https://a"}
		require.Equal(t, configFingerprint(a), configFingerprint(b),
			"map iteration order must not cause a spurious reload")
	})
}
