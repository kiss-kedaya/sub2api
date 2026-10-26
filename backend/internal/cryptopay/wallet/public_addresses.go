package wallet

import "context"

// PublicHotAddresses returns only public gas-wallet addresses. Unlike an
// explicit wallet operation, polling for transfer classification neither writes
// an audit nor grants signing authority and remains available in read-only mode.
// Read the current vault on each call so an import on another instance cannot
// leave a long-lived cached gas wallet in the deposit classifier.
func (m *Manager) PublicHotAddresses(ctx context.Context) (*HotAddress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.db == nil {
		return nil, ErrVaultUnavailable
	}
	raw, err := readVault(ctx, m.db, false)
	if err != nil {
		m.clearSeeds()
		return nil, err
	}
	if raw == "" {
		m.clearSeeds()
		return nil, ErrWalletNotConfigured
	}
	if err := m.refreshContents(raw); err != nil {
		return nil, ErrVaultUnavailable
	}
	_, evm, err := m.hot.EVMKey(0)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	solKey, solana, err := m.hot.SolanaKey(0)
	clear(solKey)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	return &HotAddress{EVM: evm.Hex(), Solana: solana.String()}, nil
}
