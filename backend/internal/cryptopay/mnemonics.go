package cryptopay

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
)

// ExportMnemonics delegates one-time vault decryption to the local wallet.
func (s *Service) ExportMnemonics(ctx context.Context) (*sweeper.MnemonicExport, error) {
	if s == nil {
		return nil, errors.New("wallet unavailable")
	}
	s.mu.RLock()
	wallet := s.wallet
	s.mu.RUnlock()
	if wallet == nil {
		return nil, errors.New("wallet unavailable")
	}
	return wallet.ExportMnemonics(ctx)
}
