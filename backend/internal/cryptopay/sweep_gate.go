package cryptopay

import (
	"context"
	"errors"
	"strings"
)

// Admin sweep readiness.
//
// The console used to derive "can I run a real sweep?" from four unrelated
// signals (wallet/status.ready, the channel switch, the security snapshot and
// the destination form), each with its own edge case. A v3 vault that is simply
// locked reports ready=false, so the run button stayed disabled even after the
// operator verified a recovery share — verification proves the share, it does
// not hold the data key resident. This file is the single authoritative
// judgement: the backend maps every prerequisite to one stable reason code and
// the frontend only renders that code.
const (
	// SweepGateReady means a real sweep can start without further operator input.
	SweepGateReady = ""
	// SweepGateWalletUnavailable means the wallet status could not be read.
	SweepGateWalletUnavailable = "wallet_unavailable"
	// SweepGateDestinationMissing means no EVM receiving address is configured.
	SweepGateDestinationMissing = "destination_missing"
	// SweepGateDestinationMismatch means settings drifted from the pinned lock
	// (only reachable through a direct database edit); sweeping would pay an
	// address the operator never approved.
	SweepGateDestinationMismatch = "destination_mismatch"
	// SweepGateDestinationInvalid means the configured EVM address is malformed
	// or the zero address.
	SweepGateDestinationInvalid = "destination_invalid"
	// SweepGateChannelDisabled means the deposit channel switch is off.
	SweepGateChannelDisabled = "channel_disabled"
	// SweepGateSecurityLocked means the wallet security guard paused the channel.
	SweepGateSecurityLocked = "security_locked"
	// SweepGateSignerLocked means a v3 vault has no resident data key. The fix
	// is the explicit verify+unlock step (a shard plus a fresh code), not a
	// disabled button with no explanation.
	SweepGateSignerLocked = "signer_locked"
	// SweepGateWalletNotReady means the vault exists but its seeds are not
	// usable and the signer is not the blocking reason.
	SweepGateWalletNotReady = "wallet_not_ready"
)

// SweepGate is the public, secret-free verdict for the sweep panel.
//
// Ready is the "just click 执行归集" flag. When it is false, Reason names the
// single blocking prerequisite and the console renders that reason next to a
// prominent unlock/configure action instead of leaving a dead disabled button.
type SweepGate struct {
	Ready  bool   `json:"ready"`
	Reason string `json:"reason,omitempty"`

	ChannelEnabled        bool `json:"channel_enabled"`
	WalletOK              bool `json:"wallet_ok"`
	WalletReady           bool `json:"wallet_ready"`
	SignerLocked          bool `json:"signer_locked"`
	SecurityLocked        bool `json:"security_locked"`
	DestinationConfigured bool `json:"destination_configured"`
	DestinationLocked     bool `json:"destination_locked"`

	// SignerUnlockedUntil is the RFC3339 end of the resident data-key window.
	// Empty when the signer is locked. The console renders a countdown from it.
	SignerUnlockedUntil string `json:"signer_unlocked_until,omitempty"`
}

// SweepGateInput is the raw state the gate is derived from. Keeping it a plain
// struct makes the whole matrix unit-testable without a database or a wallet.
type SweepGateInput struct {
	WalletAvailable       bool
	ChannelEnabled        bool
	WalletReady           bool
	SignerLocked          bool
	SignerUnlockedUntil   string
	SecurityLocked        bool
	DestinationConfigured bool
	DestinationLocked     bool
	// DestinationError is CryptoConfig.ValidateSweepReady(): nil when the
	// destination is present, well formed and still matches the lock.
	DestinationError error
}

// EvaluateSweepGate is the only place that decides whether the sweep button may
// run. Order matters: reasons that an operator cannot fix from the sweep panel
// come first, so the message is actionable.
func EvaluateSweepGate(in SweepGateInput) SweepGate {
	gate := SweepGate{
		ChannelEnabled:        in.ChannelEnabled,
		WalletOK:              in.WalletAvailable,
		WalletReady:           in.WalletReady,
		SignerLocked:          in.SignerLocked,
		SecurityLocked:        in.SecurityLocked,
		DestinationConfigured: in.DestinationConfigured,
		DestinationLocked:     in.DestinationLocked,
		SignerUnlockedUntil:   in.SignerUnlockedUntil,
	}
	switch {
	case !in.WalletAvailable:
		gate.Reason = SweepGateWalletUnavailable
	case !in.DestinationConfigured:
		gate.Reason = SweepGateDestinationMissing
	case errors.Is(in.DestinationError, ErrSweepDestinationLocked):
		gate.Reason = SweepGateDestinationMismatch
	case in.DestinationError != nil:
		gate.Reason = SweepGateDestinationInvalid
	case !in.ChannelEnabled:
		gate.Reason = SweepGateChannelDisabled
	case in.SecurityLocked:
		gate.Reason = SweepGateSecurityLocked
	case in.SignerLocked:
		gate.Reason = SweepGateSignerLocked
	case !in.WalletReady:
		gate.Reason = SweepGateWalletNotReady
	default:
		gate.Ready = true
	}
	return gate
}

// SweepGate reports the current admin sweep readiness. A wallet that cannot be
// read is reported as a gate reason instead of an error: the sweep page still
// needs to render, and "wallet_unavailable" is the honest answer.
func (s *Service) SweepGate(ctx context.Context) (*SweepGate, error) {
	cfg := s.Config()
	gate := &SweepGate{
		DestinationConfigured: strings.TrimSpace(cfg.HotWalletEVM) != "",
		DestinationLocked:     cfg.SweepDestinationLock.Active(),
	}
	in := SweepGateInput{
		DestinationConfigured: gate.DestinationConfigured,
		DestinationLocked:     gate.DestinationLocked,
		DestinationError:      cfg.ValidateSweepReady(),
	}
	status, err := s.WalletStatus(ctx)
	if err != nil {
		in.WalletAvailable = false
	} else {
		in.WalletAvailable = status.OK
		in.WalletReady = status.Ready
		in.ChannelEnabled = status.Enabled
		in.SignerLocked = status.Locked
		in.SecurityLocked = status.Security != nil &&
			(status.Security.Locked || status.Security.DepositsPaused)
		if status.Shards != nil {
			in.SignerUnlockedUntil = status.Shards.UnlockedUntil
		}
	}
	evaluated := EvaluateSweepGate(in)
	return &evaluated, nil
}
