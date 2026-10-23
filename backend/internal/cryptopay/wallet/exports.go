package wallet

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
)

// ExportMnemonics uses current ciphertext and requires a committed audit before
// returning plaintext. The HTTP boundary enforces fresh, single-use 2FA.
func (m *Manager) ExportMnemonics(ctx context.Context) (*MnemonicExport, error) {
	if err := mode.CheckWritable(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.currentVaultTx(ctx)
	if err != nil {
		return nil, m.denied("mnemonic_export", err)
	}
	defer func() { _ = tx.Rollback() }()
	raw, err := readVault(ctx, tx, false)
	if err != nil {
		return nil, err
	}
	contents, err := m.openContents(raw)
	if err != nil {
		return nil, err
	}
	defer func() { contents.Deposit, contents.Hot = "", "" }()
	if err := writeAudit(ctx, tx, auditEntry{op: "mnemonic_export", result: "success"}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, ErrAuditUnavailable
	}
	return &MnemonicExport{DepositMnemonic: normalizeMnemonic(contents.Deposit), HotMnemonic: normalizeMnemonic(contents.Hot)}, nil
}

// ImportHotMnemonic updates only the Gas seed. The PostgreSQL transaction owns
// the shared sweep lock, unfinished-job check, row update and success audit.
// Successful import takes effect immediately without a manual unlock.
func (m *Manager) ImportHotMnemonic(ctx context.Context, mnemonic string) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	const op = "hot_mnemonic_import"
	m.mu.Lock()
	defer m.mu.Unlock()
	err := m.importHotMnemonic(ctx, normalizeMnemonic(mnemonic))
	if err != nil {
		return m.denied(op, err)
	}
	return nil
}

func (m *Manager) importHotMnemonic(ctx context.Context, mnemonic string) error {
	tx, err := m.beginVaultTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var pending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM sweep_records WHERE (`+unfinishedSweepPredicate+`))`).Scan(&pending); err != nil {
		return ErrVaultUnavailable
	}
	if pending {
		return ErrSweepPending
	}
	raw, err := readVault(ctx, tx, true)
	if err != nil || raw == "" {
		return ErrVaultUnavailable
	}
	contents, err := m.openContents(raw)
	if err != nil {
		return ErrVaultUnavailable
	}
	defer func() { contents.Deposit, contents.Hot = "", "" }()
	// Keep the authenticated deposit plaintext byte-for-byte compatible.
	candidate := &vaultContents{Deposit: contents.Deposit, Hot: mnemonic}
	deposit, hot, err := contentsDerivers(candidate)
	candidate.Deposit, candidate.Hot = "", ""
	if err != nil {
		return err
	}
	defer clear(deposit.seed)
	defer clear(hot.seed)
	if err := validateDepositAddresses(ctx, tx, deposit); err != nil {
		return err
	}
	// Preserve the authenticated security-registration marker read from the
	// current vault, even when this instance has never warmed its seed cache.
	sealed, err := m.sealVaultContents(vaultContents{Deposit: contents.Deposit, Hot: mnemonic, SecurityVersion: contents.SecurityVersion})
	if err != nil {
		return ErrVaultUnavailable
	}
	result, err := tx.ExecContext(ctx, `UPDATE settings SET value = $2, updated_at = NOW() WHERE key = $1`, settingVault, sealed)
	if err != nil {
		return ErrVaultUnavailable
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return ErrVaultUnavailable
	}
	if err := writeAudit(ctx, tx, auditEntry{op: "hot_mnemonic_import", result: "success"}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		// A lost commit reply is ambiguous. Refuse further signing even when
		// the database might have accepted the update.
		m.clearSeeds()
		return errors.New("wallet update commit unconfirmed; check wallet status before retrying")
	}
	m.revokeSigningGrants()
	return m.refreshContents(sealed)
}
