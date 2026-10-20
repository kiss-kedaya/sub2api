package cryptopay

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
)

// ExportMnemonics delegates one-time vault decryption to the isolated signer.
func (s *Service) ExportMnemonics(ctx context.Context, passphrase string) (*sweeper.MnemonicExport, error) {
	if s == nil {
		return nil, errors.New("signer unavailable")
	}
	s.mu.RLock()
	signer := s.signer
	s.mu.RUnlock()
	if signer == nil {
		return nil, errors.New("signer unavailable")
	}
	return signer.ExportMnemonics(ctx, passphrase)
}
