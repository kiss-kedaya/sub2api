package cryptopay

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
)

// ImportHotMnemonic changes only the Gas wallet seed. Existing deposit keys
// never change here. The wallet serializes against sweeps and
// refuses changes with unresolved transactions before replacing its vault.
func (s *Service) ImportHotMnemonic(ctx context.Context, mnemonic string) error {
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
	return wallet.ImportHotMnemonic(ctx, mnemonic)
}
