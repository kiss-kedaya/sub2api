package cryptopay

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const gateTestEVM = "0x1111111111111111111111111111111111111111"

// The sweep gate matrix is the contract the console renders: exactly one stable
// reason code per blocking prerequisite, and Ready only when every one passes.
func TestEvaluateSweepGateMatrix(t *testing.T) {
	ready := SweepGateInput{
		WalletAvailable:       true,
		ChannelEnabled:        true,
		WalletReady:           true,
		DestinationConfigured: true,
		DestinationLocked:     true,
	}
	tests := map[string]struct {
		mutate func(*SweepGateInput)
		reason string
		ready  bool
	}{
		"all prerequisites met": {
			mutate: func(*SweepGateInput) {},
			reason: SweepGateReady,
			ready:  true,
		},
		"wallet status unavailable": {
			mutate: func(in *SweepGateInput) { in.WalletAvailable = false },
			reason: SweepGateWalletUnavailable,
		},
		"destination missing": {
			mutate: func(in *SweepGateInput) { in.DestinationConfigured = false },
			reason: SweepGateDestinationMissing,
		},
		"destination drifted from the pinned lock": {
			mutate: func(in *SweepGateInput) { in.DestinationError = ErrSweepDestinationLocked },
			reason: SweepGateDestinationMismatch,
		},
		"destination malformed": {
			mutate: func(in *SweepGateInput) { in.DestinationError = errSweepReadyDestinationInvalid },
			reason: SweepGateDestinationInvalid,
		},
		"channel switch off": {
			mutate: func(in *SweepGateInput) { in.ChannelEnabled = false },
			reason: SweepGateChannelDisabled,
		},
		"security guard paused the channel": {
			mutate: func(in *SweepGateInput) { in.SecurityLocked = true },
			reason: SweepGateSecurityLocked,
		},
		"v3 vault locked - needs verify and unlock": {
			mutate: func(in *SweepGateInput) {
				in.WalletReady = false
				in.SignerLocked = true
			},
			reason: SweepGateSignerLocked,
		},
		"vault not ready without a lock": {
			mutate: func(in *SweepGateInput) { in.WalletReady = false },
			reason: SweepGateWalletNotReady,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			in := ready
			tc.mutate(&in)
			gate := EvaluateSweepGate(in)
			require.Equal(t, tc.reason, gate.Reason)
			require.Equal(t, tc.ready, gate.Ready)
			require.Equal(t, in.DestinationConfigured, gate.DestinationConfigured)
			require.Equal(t, in.DestinationLocked, gate.DestinationLocked)
			require.Equal(t, in.SignerUnlockedUntil, gate.SignerUnlockedUntil)
		})
	}
}

// The unlocked lease is reported verbatim so the console can count down by the
// server's deadline instead of inventing its own 15:00.
func TestEvaluateSweepGatePublishesTheUnlockDeadline(t *testing.T) {
	gate := EvaluateSweepGate(SweepGateInput{
		WalletAvailable:       true,
		WalletReady:           true,
		ChannelEnabled:        true,
		DestinationConfigured: true,
		DestinationLocked:     true,
		SignerUnlockedUntil:   "2026-11-23T01:00:00Z",
	})
	require.True(t, gate.Ready)
	require.Equal(t, "2026-11-23T01:00:00Z", gate.SignerUnlockedUntil)

	locked := EvaluateSweepGate(SweepGateInput{
		WalletAvailable:       true,
		SignerLocked:          true,
		ChannelEnabled:        true,
		DestinationConfigured: true,
		DestinationLocked:     true,
	})
	require.False(t, locked.Ready)
	require.True(t, locked.SignerLocked)
	require.Empty(t, locked.SignerUnlockedUntil)
}

func TestValidateSweepReadyKeepsStableSentinels(t *testing.T) {
	require.ErrorIs(t, CryptoConfig{}.ValidateSweepReady(), errSweepReadyDestinationMissing)

	invalid := CryptoConfig{HotWalletEVM: "0xnot-an-address"}
	require.ErrorIs(t, invalid.ValidateSweepReady(), errSweepReadyDestinationInvalid)

	pinned := CryptoConfig{
		HotWalletEVM: gateTestEVM,
		SweepDestinationLock: &SweepDestinationLock{
			EVM: "0x2222222222222222222222222222222222222222",
		},
	}
	require.ErrorIs(t, pinned.ValidateSweepReady(), ErrSweepDestinationLocked)
}
