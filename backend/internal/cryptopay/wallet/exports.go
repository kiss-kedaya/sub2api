package wallet

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"strings"
	"sync"
	"time"
)

const (
	vaultBackupFormat  = "sub2api-signer-vault"
	vaultBackupVersion = 1
	vaultFailureLimit  = 5
	vaultFailureWindow = 15 * time.Minute
)

// One guard covers unlock, both exports and hot import. Authentication failures
// remain in the rolling window even after a successful authentication.
type vaultGuard struct {
	mu       sync.Mutex
	busy     bool
	failures []time.Time
}

func (g *vaultGuard) begin() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()
	for len(g.failures) > 0 && !g.failures[0].Add(vaultFailureWindow).After(now) {
		g.failures = g.failures[1:]
	}
	if g.busy || len(g.failures) >= vaultFailureLimit {
		return false
	}
	g.busy = true
	return true
}

func (g *vaultGuard) finish(failed bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if failed {
		g.failures = append(g.failures, time.Now())
	}
	g.busy = false
}

func validPassphrase(passphrase string) bool {
	return len(passphrase) <= 1024 && strings.TrimSpace(passphrase) != ""
}

// ExportMnemonics authenticates the persisted ciphertext on every request,
// including while unlocked. Audit failure never returns plaintext.
func (m *Manager) ExportMnemonics(ctx context.Context, passphrase string) (*MnemonicExport, error) {
	const op = "mnemonic_export"
	contents, _, err := m.authenticatedExport(ctx, passphrase, op)
	if err != nil {
		return nil, err
	}
	defer func() { contents.Deposit, contents.Hot = "", "" }()
	return &MnemonicExport{DepositMnemonic: normalizeMnemonic(contents.Deposit), HotMnemonic: normalizeMnemonic(contents.Hot)}, nil
}

// ExportVaultBackup returns the exact existing ciphertext in the legacy v1
// envelope. It never reseals, initializes, unlocks or replaces resident seeds.
func (m *Manager) ExportVaultBackup(ctx context.Context, passphrase string) (*VaultBackup, error) {
	contents, raw, err := m.authenticatedExport(ctx, passphrase, "backup")
	if err != nil {
		return nil, err
	}
	contents.Deposit, contents.Hot = "", ""
	return &VaultBackup{Format: vaultBackupFormat, Version: vaultBackupVersion, Vault: raw}, nil
}

func (m *Manager) authenticatedExport(ctx context.Context, passphrase, op string) (*vaultContents, string, error) {
	if err := mode.CheckWritable(); err != nil {
		return nil, "", err
	}
	if !validPassphrase(passphrase) {
		return nil, "", m.denied(op, ErrMnemonicExportRejected)
	}
	if !m.guard.begin() {
		return nil, "", m.denied(op, ErrMnemonicExportLimited)
	}
	badPassphrase := false
	defer func() { m.guard.finish(badPassphrase) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, "", m.denied(op, err)
	}
	if m.db == nil {
		return nil, "", m.denied(op, ErrVaultUnavailable)
	}
	raw, err := readVault(ctx, m.db, false)
	if err != nil || raw == "" {
		return nil, "", m.denied(op, ErrVaultUnavailable)
	}
	contents, err := openVaultString(raw, passphrase)
	if err != nil {
		badPassphrase = true
		return nil, "", m.denied(op, ErrMnemonicExportRejected)
	}
	deposit, hot, err := contentsDerivers(contents)
	if err != nil {
		contents.Deposit, contents.Hot = "", ""
		return nil, "", m.denied(op, err)
	}
	clear(deposit.seed)
	clear(hot.seed)
	if err := m.audit(ctx, auditEntry{op: op, result: "success"}); err != nil {
		contents.Deposit, contents.Hot = "", ""
		return nil, "", m.denied(op, err)
	}
	return contents, raw, nil
}

// ImportHotMnemonic updates only the Gas seed. The PostgreSQL transaction owns
// the shared sweep lock, unfinished-job check, row update and success audit.
// A successful import relocks both seeds; Unlock is required before reuse.
func (m *Manager) ImportHotMnemonic(ctx context.Context, passphrase, mnemonic string) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	const op = "hot_mnemonic_import"
	if !validPassphrase(passphrase) {
		return m.denied(op, ErrMnemonicExportRejected)
	}
	if !m.guard.begin() {
		return m.denied(op, ErrMnemonicExportLimited)
	}
	badPassphrase := false
	defer func() { m.guard.finish(badPassphrase) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	err := m.importHotMnemonic(ctx, passphrase, normalizeMnemonic(mnemonic), &badPassphrase)
	if err != nil {
		return m.denied(op, err)
	}
	return nil
}

func (m *Manager) importHotMnemonic(ctx context.Context, passphrase, mnemonic string, badPassphrase *bool) error {
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
	contents, err := openVaultString(raw, passphrase)
	if err != nil {
		*badPassphrase = true
		return ErrMnemonicExportRejected
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
	sealed, err := sealContents(contents.Deposit, mnemonic, passphrase)
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
		m.lockSeeds()
		return errors.New("wallet update commit unconfirmed; unlock required")
	}
	m.lockSeeds()
	return nil
}
