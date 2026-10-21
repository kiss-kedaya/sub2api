package cryptopay

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
)

// ExportVaultBackup returns only the already-encrypted vault. The local
// wallet verifies the vault passphrase on EVERY request, even when unlocked.
func (s *Service) ExportVaultBackup(ctx context.Context, passphrase string) (*sweeper.VaultBackup, error) {
	s.mu.RLock()
	wallet := s.wallet
	s.mu.RUnlock()
	if wallet == nil {
		return nil, errors.New("wallet unavailable")
	}
	return wallet.ExportVaultBackup(ctx, passphrase)
}

// ImportHotMnemonic changes only the Gas wallet seed. Existing deposit keys
// never change here. The wallet serializes against sweeps and
// refuses changes with unresolved transactions before replacing its vault.
func (s *Service) ImportHotMnemonic(ctx context.Context, passphrase, mnemonic string) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	s.mu.RLock()
	wallet := s.wallet
	s.mu.RUnlock()
	if wallet == nil {
		return errors.New("wallet unavailable")
	}
	return wallet.ImportHotMnemonic(ctx, passphrase, mnemonic)
}
