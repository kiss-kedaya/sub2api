package cryptopay

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
)

// ExportVaultBackup returns only the already-encrypted vault. The isolated
// signer verifies the vault passphrase on EVERY request, even when unlocked.
func (s *Service) ExportVaultBackup(ctx context.Context, passphrase string) (*sweeper.VaultBackup, error) {
	s.mu.RLock()
	signer := s.signer
	s.mu.RUnlock()
	if signer == nil {
		return nil, errors.New("signer unavailable")
	}
	return signer.ExportVaultBackup(ctx, passphrase)
}

// ImportHotMnemonic changes only the Gas wallet seed. Existing deposit keys
// never change here. The signer independently serializes against sweeps and
// refuses changes with unresolved transactions before replacing its vault.
func (s *Service) ImportHotMnemonic(ctx context.Context, passphrase, mnemonic string) error {
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	s.mu.RLock()
	signer := s.signer
	s.mu.RUnlock()
	if signer == nil {
		return errors.New("signer unavailable")
	}
	return signer.ImportHotMnemonic(ctx, passphrase, mnemonic)
}
