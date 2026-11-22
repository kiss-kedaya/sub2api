package cryptopay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
)

// signerPoolTarget is how many free deposit addresses per family are
// pre-generated while the data key is available. A locked v3 vault cannot
// derive new addresses, so this pool is what keeps deposits flowing between
// operator unlocks. It is intentionally larger than the maintenance top-up
// batch (50).
const signerPoolTarget = 500

// SignerSetupRequest is the service-level shard setup payload. DataKey is the
// browser-generated vault key and is used only for the one migration window.
type SignerSetupRequest struct {
	DataKey          []byte
	ShareA           string
	ConfirmShare     string
	Commitment       string
	RiskAcknowledged bool
}

func (s *Service) walletManager() (*wallet.Manager, error) {
	if s == nil {
		return nil, errors.New("wallet unavailable")
	}
	s.mu.RLock()
	manager := s.wallet
	s.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("wallet unavailable")
	}
	return manager, nil
}

// SignerShardStatus reports the public shard configuration.
func (s *Service) SignerShardStatus(ctx context.Context) (*wallet.ShardStatus, error) {
	manager, err := s.walletManager()
	if err != nil {
		return nil, err
	}
	return manager.ShardStatus(ctx)
}

// SetupSignerShards migrates the vault to the browser-generated data key in one
// transaction, then warms the deposit address pool while the seeds are still in
// memory. The migration is serialized with sweeps.
func (s *Service) SetupSignerShards(ctx context.Context, request SignerSetupRequest) (*wallet.ShardStatus, error) {
	manager, err := s.walletManager()
	if err != nil {
		return nil, err
	}
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	status, err := manager.SetupShards(ctx, wallet.ShardSetupRequest{
		DataKey: request.DataKey, ShareA: request.ShareA, ConfirmShare: request.ConfirmShare,
		Commitment: request.Commitment, RiskAcknowledged: request.RiskAcknowledged,
	})
	if err != nil {
		return nil, err
	}
	// The shards are the only thing that can open the vault; from now on the
	// sweep destination must not be movable through the generic config API.
	// Failing closed here is deliberate: a silent lock failure would leave the
	// exact hole these shards are supposed to close.
	lockCreated, lockActive, lockErr := s.pinSweepDestinationLock(ctx)
	if lockErr != nil {
		return nil, fmt.Errorf("signer shards are configured but the sweep destination lock could not be saved: %w", lockErr)
	}
	s.notifySignerEvent(ctx, EventSignerSetup, status, lockCreated, lockActive)
	s.prewarmAddressPool(ctx, manager, request.ConfirmShare)
	return status, nil
}

// ResplitSignerShards replaces share A after a browser-side re-split.
func (s *Service) ResplitSignerShards(ctx context.Context, request SignerSetupRequest) (*wallet.ShardStatus, error) {
	manager, err := s.walletManager()
	if err != nil {
		return nil, err
	}
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	status, err := manager.ResplitShards(ctx, wallet.ShardSetupRequest{
		DataKey: request.DataKey, ShareA: request.ShareA, ConfirmShare: request.ConfirmShare,
		Commitment: request.Commitment, RiskAcknowledged: request.RiskAcknowledged,
	})
	if err != nil {
		return nil, err
	}
	// Re-split refreshes the lock timestamp but must never move the pinned
	// addresses: a re-split is not a destination change.
	lockCreated, lockActive, lockErr := s.pinSweepDestinationLock(ctx)
	if lockErr != nil {
		return nil, fmt.Errorf("signer shards were re-split but the sweep destination lock could not be refreshed: %w", lockErr)
	}
	s.notifySignerEvent(ctx, EventSignerResplit, status, lockCreated, lockActive)
	return status, nil
}

// VerifySignerShare proves one operator share belongs to this wallet.
func (s *Service) VerifySignerShare(ctx context.Context, share string) error {
	manager, err := s.walletManager()
	if err != nil {
		return err
	}
	verifyErr := manager.VerifyShardShare(ctx, share)
	// The share itself never enters the event: only the outcome and a coarse
	// reason class, so a mailbox or phone leak cannot expose recovery material.
	detail := map[string]any{"valid": verifyErr == nil}
	if verifyErr != nil {
		detail["reason"] = signerVerifyReason(verifyErr)
	}
	s.NotifyEvent(ctx, EventSignerVerifyShare, detail)
	return verifyErr
}

// signerVerifyReason maps the wallet sentinels to stable, secret-free reasons.
func signerVerifyReason(err error) string {
	switch {
	case errors.Is(err, wallet.ErrSignerShareInvalid):
		return "invalid_share"
	case errors.Is(err, wallet.ErrSignerRateLimited):
		return "rate_limited"
	case errors.Is(err, wallet.ErrShardsNotConfigured):
		return "not_configured"
	case err == nil:
		return ""
	default:
		return "error"
	}
}

// pinSweepDestinationLock pins the current sweep destination through the signer
// lifecycle. A read-only or not-yet-configured deployment reports no lock and no
// error; a real persistence failure is returned so the caller can fail closed.
func (s *Service) pinSweepDestinationLock(ctx context.Context) (created bool, active bool, err error) {
	if s == nil || s.settings == nil {
		return false, false, nil
	}
	created, active, err = PinSweepDestinationLock(ctx, NewServiceSettingStore(s.settings), wallet.AuditActorUserID(ctx), time.Now())
	if err != nil {
		slog.Error("[cryptopay] sweep destination lock could not be persisted", "error", err)
		return false, false, err
	}
	return created, active, nil
}

// notifySignerEvent publishes a shard lifecycle event. Details are limited to
// public shard metadata; the data key, shares and the commitment never leave the
// vault, so they are never rendered into a notification.
func (s *Service) notifySignerEvent(ctx context.Context, event string, status *wallet.ShardStatus, lockCreated, lockActive bool) {
	detail := map[string]any{
		"sweep_destination_locked":       lockActive,
		"sweep_destination_lock_created": lockCreated,
	}
	if status != nil {
		detail["vault_version"] = status.VaultVersion
		if status.Scheme != "" {
			detail["scheme"] = status.Scheme
		}
		if status.Threshold > 0 {
			detail["threshold"] = status.Threshold
		}
		if status.Total > 0 {
			detail["total"] = status.Total
		}
	}
	s.NotifyEvent(ctx, event, detail)
}

// EnableAutoSweep stores the sealed automatic-sweep share after the operator
// explicitly accepted the risk.
func (s *Service) EnableAutoSweep(ctx context.Context, share string) (*wallet.ShardStatus, error) {
	manager, err := s.walletManager()
	if err != nil {
		return nil, err
	}
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	return manager.EnableAutoSweep(ctx, share)
}

// DisableAutoSweep removes the sealed automatic-sweep share.
func (s *Service) DisableAutoSweep(ctx context.Context) (*wallet.ShardStatus, error) {
	manager, err := s.walletManager()
	if err != nil {
		return nil, err
	}
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	return manager.DisableAutoSweep(ctx)
}

// prewarmAddressPool derives deposit addresses while the data key is resident.
// Failures are logged, not returned: the shard setup already committed and the
// pool is retried on the next unlocked operation.
func (s *Service) prewarmAddressPool(ctx context.Context, manager *wallet.Manager, share string) {
	if s.addresses == nil || strings.TrimSpace(share) == "" {
		return
	}
	err := manager.Unseal(ctx, share, func(unsealed context.Context) error {
		return s.addresses.TopUpTo(unsealed, signerPoolTarget)
	})
	if err != nil {
		slog.Warn("[cryptopay] deposit address pool prewarm failed", "error", err)
	}
}

// ExportMnemonicsWithShare exports the mnemonic pair only while a recovery
// share is combined, then clears the data key.
func (s *Service) ExportMnemonicsWithShare(ctx context.Context, share string) (*sweeper.MnemonicExport, error) {
	manager, err := s.walletManager()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(share) == "" {
		return manager.ExportMnemonics(ctx)
	}
	var result *sweeper.MnemonicExport
	err = manager.Unseal(ctx, share, func(unsealed context.Context) error {
		exported, exportErr := manager.ExportMnemonics(unsealed)
		result = exported
		return exportErr
	})
	return result, err
}

// ImportHotMnemonicWithShare replaces the gas seed with a recovery share.
func (s *Service) ImportHotMnemonicWithShare(ctx context.Context, mnemonic, share string) error {
	manager, err := s.walletManager()
	if err != nil {
		return err
	}
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	if strings.TrimSpace(share) == "" {
		return manager.ImportHotMnemonic(ctx, mnemonic)
	}
	return manager.Unseal(ctx, share, func(unsealed context.Context) error {
		return manager.ImportHotMnemonic(unsealed, mnemonic)
	})
}

// ExportAddressPrivateKeyWithShare exports one deposit key with a recovery share.
func (s *Service) ExportAddressPrivateKeyWithShare(ctx context.Context, addressID int64, share string) (*wallet.AddressPrivateKeyExport, error) {
	manager, err := s.walletManager()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(share) == "" {
		return manager.ExportAddressPrivateKey(ctx, addressID)
	}
	var result *wallet.AddressPrivateKeyExport
	err = manager.Unseal(ctx, share, func(unsealed context.Context) error {
		exported, exportErr := manager.ExportAddressPrivateKey(unsealed, addressID)
		result = exported
		return exportErr
	})
	return result, err
}

// signerNeedsUnlock reports whether the vault is locked and therefore sweep
// signing needs either an operator share or the sealed auto-sweep share.
func (s *Service) signerNeedsUnlock(ctx context.Context) (bool, error) {
	manager, err := s.walletManager()
	if err != nil {
		return false, err
	}
	return manager.SignerLocked(ctx)
}

// unsealForRun prepares the signer for one sweep run. Auto sweeps use the
// sealed share; manual sweeps must supply a recovery share once the vault is
// locked. The caller owns LockSigner (via the returned cleanup).
func (s *Service) unsealForRun(ctx context.Context, manual bool, share string) (func(), error) {
	locked, err := s.signerNeedsUnlock(ctx)
	if err != nil {
		return nil, err
	}
	if !locked {
		return func() {}, nil
	}
	manager, err := s.walletManager()
	if err != nil {
		return nil, err
	}
	source := "operator_share"
	switch {
	case strings.TrimSpace(share) != "":
		if err := manager.UnlockSigner(ctx, share); err != nil {
			return nil, err
		}
	case manual:
		return nil, wallet.ErrSignerShareRequired
	default:
		source = "auto_share"
		if err := manager.UnlockAutoSigner(ctx); err != nil {
			return nil, err
		}
	}
	detail := map[string]any{"source": source}
	if deadline, ok := manager.SignerLeaseDeadline(); ok {
		detail["lease_expires_at"] = deadline.Format(time.RFC3339)
	}
	s.NotifyEvent(ctx, EventSignerUnlock, detail)
	return manager.LockSigner, nil
}

// topUpAddressPoolIfUnlocked keeps the pool warm after any unlocked sweep.
// It never fails the sweep; a dry pool is surfaced by PoolStatus.
func (s *Service) topUpAddressPoolIfUnlocked(ctx context.Context) {
	if s.addresses == nil {
		return
	}
	manager, err := s.walletManager()
	if err != nil {
		return
	}
	if locked, err := manager.SignerLocked(ctx); err != nil || locked {
		return
	}
	if err := s.addresses.TopUpTo(ctx, signerPoolTarget); err != nil {
		slog.Warn("[cryptopay] deposit address pool top-up failed", "error", err)
	}
}
