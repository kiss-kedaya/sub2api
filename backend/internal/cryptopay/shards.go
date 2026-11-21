package cryptopay

import (
	"context"
	"errors"
	"log/slog"
	"strings"

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
	return manager.ResplitShards(ctx, wallet.ShardSetupRequest{
		DataKey: request.DataKey, ShareA: request.ShareA, ConfirmShare: request.ConfirmShare,
		Commitment: request.Commitment, RiskAcknowledged: request.RiskAcknowledged,
	})
}

// VerifySignerShare proves one operator share belongs to this wallet.
func (s *Service) VerifySignerShare(ctx context.Context, share string) error {
	manager, err := s.walletManager()
	if err != nil {
		return err
	}
	return manager.VerifyShardShare(ctx, share)
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
	switch {
	case strings.TrimSpace(share) != "":
		if err := manager.UnlockSigner(ctx, share); err != nil {
			return nil, err
		}
	case manual:
		return nil, wallet.ErrSignerShareRequired
	default:
		if err := manager.UnlockAutoSigner(ctx); err != nil {
			return nil, err
		}
	}
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
