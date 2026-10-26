package cryptopay

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
)

// ExportAddressPrivateKey delegates one selected deposit assignment to the
// managed wallet. The HTTP boundary requires operator 2FA or its valid,
// session-bound payment-verification grant.
func (s *Service) ExportAddressPrivateKey(ctx context.Context, addressID int64) (*wallet.AddressPrivateKeyExport, error) {
	if err := mode.CheckWritable(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errors.New("wallet unavailable")
	}
	s.mu.RLock()
	manager := s.wallet
	s.mu.RUnlock()
	if manager == nil {
		return nil, errors.New("wallet unavailable")
	}
	return manager.ExportAddressPrivateKey(ctx, addressID)
}
