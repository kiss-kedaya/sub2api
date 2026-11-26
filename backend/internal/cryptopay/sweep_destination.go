package cryptopay

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/ethereum/go-ethereum/common"
)

// SweepDestinationLock pins the sweep destination. It is written once the
// browser-held signer shards exist, and from then on every settings save that
// would move the destination must go through the dedicated
// /admin/crypto/signer/sweep-destination endpoint (fresh 2FA + risk phrase).
//
// Threat model: whoever owns the server can edit settings.crypto.config. If they
// can also point hot_wallet_evm/hot_wallet_solana somewhere else, the next sweep
// turns every user deposit into an attacker payout. The lock does not make the
// settings database read-only; it makes the substitution loud and refuse to
// persist through the normal config API.
type SweepDestinationLock struct {
	EVM            string    `json:"evm,omitempty"`
	Solana         string    `json:"solana,omitempty"`
	LockedAt       time.Time `json:"locked_at"`
	LockedByUserID int64     `json:"locked_by_user_id,omitempty"`
}

// SweepDestination is the pair of destination addresses currently in force.
type SweepDestination struct {
	EVM    string
	Solana string
}

// Empty reports whether no destination is configured at all.
func (d SweepDestination) Empty() bool {
	return d.EVM == "" && d.Solana == ""
}

// Equal compares destinations the way the sweep actually uses them: EVM
// addresses case-insensitively (hex is case-insensitive; EIP-55 is a checksum,
// not a different address) and Solana addresses verbatim after trimming.
func (d SweepDestination) Equal(other SweepDestination) bool {
	return normalizeSweepEVMAddress(d.EVM) == normalizeSweepEVMAddress(other.EVM) &&
		normalizeSweepSolanaAddress(d.Solana) == normalizeSweepSolanaAddress(other.Solana)
}

// Destination returns the addresses pinned by the lock.
func (l *SweepDestinationLock) Destination() SweepDestination {
	if l == nil {
		return SweepDestination{}
	}
	return SweepDestination{EVM: strings.TrimSpace(l.EVM), Solana: strings.TrimSpace(l.Solana)}
}

// Active reports whether the lock pins anything. A config saved before this
// field existed, or before a destination was set, has no lock.
func (l *SweepDestinationLock) Active() bool {
	return l != nil && !l.Destination().Empty()
}

// SweepDestination returns the destination currently configured for sweeps.
func (cfg CryptoConfig) SweepDestination() SweepDestination {
	return SweepDestination{
		EVM:    strings.TrimSpace(cfg.HotWalletEVM),
		Solana: strings.TrimSpace(cfg.HotWalletSolana),
	}
}

// ErrSweepDestinationLocked is returned when the generic config API tries to
// move a pinned sweep destination. The operator must use the dedicated
// endpoint, which verifies a fresh authenticator code and the risk phrase.
var ErrSweepDestinationLocked = errors.Conflict(
	"SWEEP_DESTINATION_LOCKED",
	"归集目标地址已锁定，如需变更请走 /api/v1/admin/crypto/signer/sweep-destination 并验证 2FA + 风险短语",
)

// errSweepDestinationRequired rejects an update that would leave no destination.
var errSweepDestinationRequired = errors.BadRequest(
	"SWEEP_DESTINATION_REQUIRED",
	"至少需要提供一个归集目标地址（hot_wallet_evm 或 hot_wallet_solana）",
)

// errSweepDestinationInvalid rejects a malformed destination before it can be
// persisted. EVM input that mixes letter case must also pass the EIP-55
// checksum: a typo in a sweep destination sends funds to an unrecoverable
// address, and the checksum is the only cheap defense we have.
var errSweepDestinationInvalid = errors.BadRequest(
	"SWEEP_DESTINATION_INVALID",
	"归集目标地址格式无效",
)

// EnforceSweepDestinationLock reports ErrSweepDestinationLocked when cfg would
// not persist the pinned destination. It is called on the normal config save
// path only; the dedicated endpoint is the documented way to move it.
func (cfg CryptoConfig) EnforceSweepDestinationLock() error {
	lock := cfg.SweepDestinationLock
	if !lock.Active() {
		return nil
	}
	if !lock.Destination().Equal(cfg.SweepDestination()) {
		return ErrSweepDestinationLocked
	}
	return nil
}

// normalizeSweepDestinationLock trims and validates a lock loaded from settings.
// A tampered lock must fail closed: silently dropping it would remove the very
// protection it exists for.
func normalizeSweepDestinationLock(cfg *CryptoConfig) error {
	if cfg == nil || cfg.SweepDestinationLock == nil {
		return nil
	}
	lock := *cfg.SweepDestinationLock
	lock.EVM = strings.TrimSpace(lock.EVM)
	lock.Solana = strings.TrimSpace(lock.Solana)
	if lock.EVM == "" && lock.Solana == "" {
		return fmt.Errorf("crypto config: sweep_destination_lock must pin at least one destination")
	}
	if lock.EVM != "" && !validEVMAddress(lock.EVM) {
		return fmt.Errorf("crypto config: sweep_destination_lock.evm is not a valid EVM address")
	}
	if lock.Solana != "" && !validSolanaAddress(lock.Solana) {
		return fmt.Errorf("crypto config: sweep_destination_lock.solana is not a valid Solana address")
	}
	if lock.LockedAt.IsZero() {
		return fmt.Errorf("crypto config: sweep_destination_lock.locked_at must be set")
	}
	lock.LockedAt = lock.LockedAt.UTC()
	cfg.SweepDestinationLock = &lock
	return nil
}

// normalizeNotifyConfig trims and validates the notification channels. An
// enabled channel set with no channel is a misconfiguration, not "disabled".
func normalizeNotifyConfig(cfg *CryptoConfig) error {
	if cfg == nil || cfg.Notify == nil {
		return nil
	}
	notifyCfg := *cfg.Notify
	notifyCfg.WebhookURL = strings.TrimSpace(notifyCfg.WebhookURL)
	notifyCfg.Email = strings.TrimSpace(notifyCfg.Email)
	if len(notifyCfg.WebhookURL) > maxNotifyURLBytes {
		return fmt.Errorf("crypto config: notify.webhook_url is too long")
	}
	if notifyCfg.WebhookURL != "" && !validRPCURL(notifyCfg.WebhookURL) {
		return fmt.Errorf("crypto config: notify.webhook_url must be an absolute http(s) URL")
	}
	if notifyCfg.Email != "" && !validNotifyEmail(notifyCfg.Email) {
		return fmt.Errorf("crypto config: notify.email is not a valid address")
	}
	if notifyCfg.Enabled && notifyCfg.WebhookURL == "" && notifyCfg.Email == "" {
		return fmt.Errorf("crypto config: notify.enabled requires a webhook_url or an email")
	}
	if !notifyCfg.Enabled && notifyCfg.WebhookURL == "" && notifyCfg.Email == "" {
		cfg.Notify = nil
		return nil
	}
	cfg.Notify = &notifyCfg
	return nil
}

func validNotifyEmail(value string) bool {
	if len(value) > 254 {
		return false
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil {
		return false
	}
	return parsed.Address == value || strings.EqualFold(parsed.Address, value)
}

// normalizeSweepEVMAddress is the comparison form for EVM destinations:
// trimmed, lowercase hex. EIP-55 casing is display metadata, not identity.
func normalizeSweepEVMAddress(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

// normalizeSweepSolanaAddress trims only: base58 is case-sensitive.
func normalizeSweepSolanaAddress(address string) string {
	return strings.TrimSpace(address)
}

// validateSweepEVMInput validates an operator-provided EVM destination. The
// generic config validator accepts any casing (existing rows must keep loading),
// but an explicit destination change requires the EIP-55 checksum when the
// input mixes case, so a typo cannot silently retarget sweeps.
func validateSweepEVMInput(address string) bool {
	trimmed := strings.TrimSpace(address)
	if !validEVMAddress(trimmed) {
		return false
	}
	hexPart := trimmed[2:]
	if hexPart == strings.ToLower(hexPart) || hexPart == strings.ToUpper(hexPart) {
		// Unmixed case carries no checksum; still a valid canonical address.
		return true
	}
	return common.HexToAddress(trimmed).Hex() == trimmed
}

// ValidateSweepDestinationInput validates a destination pair submitted to the
// dedicated endpoint: at least one address, each non-empty address well formed.
func ValidateSweepDestinationInput(destination SweepDestination) error {
	evm := strings.TrimSpace(destination.EVM)
	solana := strings.TrimSpace(destination.Solana)
	if evm == "" && solana == "" {
		return errSweepDestinationRequired
	}
	if evm != "" && !validateSweepEVMInput(evm) {
		return errSweepDestinationInvalid
	}
	if solana != "" && !validSolanaAddress(solana) {
		return errSweepDestinationInvalid
	}
	return nil
}

// SweepDestinationUpdate is one verified operator request to change (or confirm)
// the sweep destination. A nil field keeps the currently stored value.
type SweepDestinationUpdate struct {
	HotWalletEVM    *string
	HotWalletSolana *string
	ActorUserID     int64
	Now             time.Time
}

// SweepDestinationChange is the outcome of SweepDestinationUpdate.
type SweepDestinationChange struct {
	// Changed reports whether the hot-wallet settings row changed.
	Changed bool
	// Locked reports whether the lock timestamp/owner was (re)written.
	Locked bool
	// LockCreated reports whether this call created the lock for the first time.
	LockCreated bool
	Previous    SweepDestination
	Current     SweepDestination
	Lock        *SweepDestinationLock
}

type sweepDestinationChangeJSON struct {
	Saved       bool                  `json:"saved"`
	Applied     bool                  `json:"applied"`
	Changed     bool                  `json:"changed"`
	Locked      bool                  `json:"locked"`
	LockCreated bool                  `json:"lock_created"`
	Previous    sweepDestinationJSON  `json:"previous"`
	Current     sweepDestinationJSON  `json:"current"`
	Lock        *SweepDestinationLock `json:"sweep_destination_lock,omitempty"`
}

type sweepDestinationJSON struct {
	HotWalletEVM    string `json:"hot_wallet_evm"`
	HotWalletSolana string `json:"hot_wallet_solana"`
}

// JSON renders the admin response body. Callers wrap it under "data". applied
// says whether the running service already uses the saved destination: false
// while a sweep or scan delays the apply (it finishes on its own).
func (c SweepDestinationChange) JSON(applied bool) any {
	current := sweepDestinationJSON{HotWalletEVM: c.Current.EVM, HotWalletSolana: c.Current.Solana}
	previous := sweepDestinationJSON{HotWalletEVM: c.Previous.EVM, HotWalletSolana: c.Previous.Solana}
	return sweepDestinationChangeJSON{
		Saved:       true,
		Applied:     applied,
		Changed:     c.Changed,
		Locked:      c.Locked,
		LockCreated: c.LockCreated,
		Previous:    previous,
		Current:     current,
		Lock:        c.Lock,
	}
}

// UpdateSweepDestination applies a verified destination change.
//
// The caller (the admin handler) has already verified the fresh authenticator
// code, the risk phrase and the acknowledgement flag. This function owns the
// persistence rules only:
//   - idempotent: re-submitting the addresses that are already pinned succeeds
//     without touching the lock timestamp;
//   - a real change rewrites the destination and re-pins the lock to it;
//   - a CAS conflict is returned to the caller (no silent retry), because
//     retrying could persist a destination the operator never approved.
func UpdateSweepDestination(ctx context.Context, store SettingStore, update SweepDestinationUpdate) (*SweepDestinationChange, error) {
	if store == nil {
		return nil, fmt.Errorf("crypto config store is unavailable")
	}
	if update.HotWalletEVM == nil && update.HotWalletSolana == nil {
		return nil, errSweepDestinationRequired
	}
	if update.Now.IsZero() {
		update.Now = time.Now()
	}

	existing, expected, err := LoadConfigForUpdate(ctx, store)
	if err != nil {
		return nil, err
	}
	next := existing
	if update.HotWalletEVM != nil {
		next.HotWalletEVM = strings.TrimSpace(*update.HotWalletEVM)
	}
	if update.HotWalletSolana != nil {
		next.HotWalletSolana = strings.TrimSpace(*update.HotWalletSolana)
	}
	current := next.SweepDestination()
	if err := ValidateSweepDestinationInput(current); err != nil {
		return nil, err
	}
	previous := existing.SweepDestination()
	lock := existing.SweepDestinationLock
	lockMatches := lock.Active() && lock.Destination().Equal(current)

	// Re-submitting the currently pinned destination must be a no-op: the
	// console retries and double-clicks must not move the audit timestamp.
	if lockMatches && previous.Equal(current) {
		return &SweepDestinationChange{Previous: previous, Current: current, Lock: lock}, nil
	}

	// The destination did not move, but the settings row drifted from the lock
	// (only possible through a direct database edit): repair the row and keep
	// the original lock timestamp/owner.
	if lockMatches {
		next.SweepDestinationLock = lock
		if err := SaveConfigIfUnchanged(ctx, store, next, expected); err != nil {
			return nil, err
		}
		return &SweepDestinationChange{
			Changed: !previous.Equal(current), Previous: previous, Current: current, Lock: lock,
		}, nil
	}

	newLock := &SweepDestinationLock{
		EVM:            current.EVM,
		Solana:         current.Solana,
		LockedAt:       update.Now.UTC(),
		LockedByUserID: update.ActorUserID,
	}
	next.SweepDestinationLock = newLock
	if err := SaveConfigIfUnchanged(ctx, store, next, expected); err != nil {
		return nil, err
	}
	return &SweepDestinationChange{
		Changed:     !previous.Equal(current),
		Locked:      true,
		LockCreated: !lock.Active(),
		Previous:    previous,
		Current:     current,
		Lock:        newLock,
	}, nil
}

// PinSweepDestinationLock is the internal path used by the signer shard
// lifecycle. It pins whatever destination is configured right now:
//   - no active lock: create it from the current hot wallets (skipped when both
//     are empty, because there is nothing to protect yet);
//   - active lock: only refresh locked_at/locked_by, never the addresses, so a
//     re-split can never silently retarget sweeps.
//
// A CAS conflict is retried: the only concurrent writers here are other config
// saves, and pinning the current value is exactly what this call means.
// created reports a first-time lock; active reports whether a lock is in force
// afterwards.
func PinSweepDestinationLock(ctx context.Context, store SettingStore, actorUserID int64, now time.Time) (created bool, active bool, err error) {
	if store == nil {
		return false, false, fmt.Errorf("crypto config store is unavailable")
	}
	if now.IsZero() {
		now = time.Now()
	}
	for attempt := 0; attempt < 3; attempt++ {
		cfg, expected, loadErr := LoadConfigForUpdate(ctx, store)
		if loadErr != nil {
			return false, false, loadErr
		}
		destination := cfg.SweepDestination()
		var nextLock *SweepDestinationLock
		if lock := cfg.SweepDestinationLock; lock.Active() {
			copied := *lock
			copied.LockedAt = now.UTC()
			if actorUserID > 0 {
				copied.LockedByUserID = actorUserID
			}
			nextLock = &copied
		} else {
			if destination.Empty() {
				return false, false, nil
			}
			nextLock = &SweepDestinationLock{
				EVM:            destination.EVM,
				Solana:         destination.Solana,
				LockedAt:       now.UTC(),
				LockedByUserID: actorUserID,
			}
			created = true
		}
		cfg.SweepDestinationLock = nextLock
		if saveErr := SaveConfigIfUnchanged(ctx, store, cfg, expected); saveErr != nil {
			if saveErr == ErrConfigConflict {
				continue
			}
			return false, false, saveErr
		}
		return created, true, nil
	}
	return false, false, ErrConfigConflict
}
