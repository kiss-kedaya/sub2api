package wallet

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/ethereum/go-ethereum/common"
)

// Manager owns seed residency and serializes every operation that uses seeds.
// Callers must not hold the sweep advisory lock when calling Unlock or Import.
// No method re-enters mu, and no DB connection is retained between operations.
type Manager struct {
	db               *sql.DB
	mu               sync.Mutex
	deposit, hot     *Deriver
	destination      string
	enabled          bool
	vaultFingerprint [32]byte
	guard            vaultGuard
}

// New creates a locked wallet, with signing enabled once a destination is set.
// It uses only the supplied database; it never reads environment credentials.
func New(db *sql.DB) *Manager { return &Manager{db: db, enabled: true} }

// SetDestination configures the only ERC20 recipient. Empty clears it; invalid
// input clears it as well so a previous recipient cannot survive bad config.
func (m *Manager) SetDestination(address string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.destination = ""
	address = strings.TrimSpace(address)
	if address == "" {
		return nil
	}
	if !common.IsHexAddress(address) || common.HexToAddress(address) == (common.Address{}) {
		return errors.New("invalid EVM sweep destination")
	}
	m.destination = common.HexToAddress(address).Hex()
	return nil
}

// SetEnabled controls signing without preventing address queries or exports.
func (m *Manager) SetEnabled(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enabled = enabled
}

// Lock erases both resident seeds. A subsequent Unlock must authenticate again.
func (m *Manager) Lock() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lockSeeds()
}

func (m *Manager) lockSeeds() {
	if m.deposit != nil {
		clear(m.deposit.seed)
	}
	if m.hot != nil {
		clear(m.hot.seed)
	}
	m.deposit, m.hot = nil, nil
	m.vaultFingerprint = [32]byte{}
}

func (m *Manager) unlocked() bool { return m.deposit != nil && m.hot != nil }

// Status reports current in-process state without writing on every poll.
func (m *Manager) Status(ctx context.Context) (*Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Status{OK: m.db != nil, Unlocked: m.unlocked(), SigningEnabled: m.enabled && m.destination != ""}, nil
}

// Unlock opens the existing vault without accepting seed replacement. A new
// vault requires two valid, different BIP39 mnemonics and a 12-character password.
// Address consistency, ciphertext and success audits commit before seed residency.
func (m *Manager) Unlock(ctx context.Context, passphrase, depositMnemonic, hotMnemonic string) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	const op = "unlock"
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
	err := m.unlock(ctx, passphrase, depositMnemonic, hotMnemonic, &badPassphrase)
	if err != nil {
		return m.denied(op, err)
	}
	return nil
}

func (m *Manager) unlock(ctx context.Context, passphrase, depositMnemonic, hotMnemonic string, badPassphrase *bool) error {
	tx, err := m.beginVaultTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	raw, err := readVault(ctx, tx, true)
	if err != nil {
		return err
	}
	initializing := raw == ""
	var contents *vaultContents
	if !initializing {
		if depositMnemonic != "" || hotMnemonic != "" {
			return ErrSeedReplacement
		}
		contents, err = openVaultString(raw, passphrase)
		if err != nil {
			*badPassphrase = true
			return ErrMnemonicExportRejected
		}
	} else {
		if utf8.RuneCountInString(passphrase) < 12 {
			return errors.New("a new vault requires a passphrase of at least 12 characters")
		}
		contents = &vaultContents{Deposit: normalizeMnemonic(depositMnemonic), Hot: normalizeMnemonic(hotMnemonic)}
	}
	defer func() { contents.Deposit, contents.Hot = "", "" }()
	deposit, hot, err := contentsDerivers(contents)
	if err != nil {
		return err
	}
	resident := false
	defer func() {
		if !resident {
			clear(deposit.seed)
			clear(hot.seed)
		}
	}()
	if err := validateDepositAddresses(ctx, tx, deposit); err != nil {
		return err
	}
	if initializing {
		sealed, err := sealContents(contents.Deposit, contents.Hot, passphrase)
		if err != nil {
			return ErrVaultUnavailable
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, NOW())
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`, settingVault, sealed); err != nil {
			return ErrVaultUnavailable
		}
		if err := writeAudit(ctx, tx, auditEntry{op: "vault_init", result: "ok"}); err != nil {
			return err
		}
		raw = sealed
	}
	if err := writeAudit(ctx, tx, auditEntry{op: "unlock", result: "ok"}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return ErrVaultUnavailable
	}
	m.lockSeeds()
	m.deposit, m.hot = deposit, hot
	m.vaultFingerprint = sha256.Sum256([]byte(raw))
	resident = true
	return nil
}

// Derive returns deposit addresses using the former signer's BIP44 paths.
func (m *Manager) Derive(ctx context.Context, start, count uint32) ([]addresses.Derived, error) {
	if err := mode.CheckWritable(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.unlocked() {
		return nil, m.denied("derive", ErrVaultLocked)
	}
	if count == 0 || count > 500 || start >= 1<<31 || uint64(start)+uint64(count) > 1<<31 {
		return nil, m.denied("derive", errors.New("derivation count must be 1-500 within nonhardened index range"))
	}
	storeTx, err := m.currentVaultTx(ctx)
	if err != nil {
		return nil, m.denied("derive", err)
	}
	defer func() { _ = storeTx.Rollback() }()
	out := make([]addresses.Derived, 0, count)
	for i := uint32(0); i < count; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		index := start + i
		_, evm, err := m.deposit.EVMKey(index)
		if err != nil {
			return nil, errors.New("deposit address derivation failed")
		}
		solKey, sol, err := m.deposit.SolanaKey(index)
		clear(solKey)
		if err != nil {
			return nil, errors.New("deposit address derivation failed")
		}
		out = append(out, addresses.Derived{Index: int64(index), EVM: evm.Hex(), Solana: sol.String()})
	}
	if err := writeAudit(ctx, storeTx, auditEntry{op: "derive", index: start, amount: strconv.FormatUint(uint64(count), 10), result: "ok"}); err != nil {
		return nil, err
	}
	if err := storeTx.Commit(); err != nil {
		return nil, ErrAuditUnavailable
	}
	return out, nil
}

// HotAddresses returns public addresses at index zero from the independent seed.
func (m *Manager) HotAddresses(ctx context.Context) (*HotAddress, error) {
	if err := mode.CheckWritable(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !m.unlocked() {
		return nil, m.denied("hot_address", ErrVaultLocked)
	}
	storeTx, err := m.currentVaultTx(ctx)
	if err != nil {
		return nil, m.denied("hot_address", err)
	}
	defer func() { _ = storeTx.Rollback() }()
	_, evm, err := m.hot.EVMKey(0)
	if err != nil {
		return nil, errors.New("hot address derivation failed")
	}
	solKey, sol, err := m.hot.SolanaKey(0)
	clear(solKey)
	if err != nil {
		return nil, errors.New("hot address derivation failed")
	}
	if err := writeAudit(ctx, storeTx, auditEntry{op: "hot_address", result: "ok"}); err != nil {
		return nil, err
	}
	if err := storeTx.Commit(); err != nil {
		return nil, ErrAuditUnavailable
	}
	return &HotAddress{EVM: evm.Hex(), Solana: sol.String()}, nil
}

// Called only with mu held. The shared row lock prevents a concurrent import
// from committing between the fingerprint check and the returned address/signature.
// On failure rollback completes before the caller requests a separate audit.
func (m *Manager) currentVaultTx(ctx context.Context) (*sql.Tx, error) {
	if m.db == nil {
		return nil, ErrVaultUnavailable
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = $1 FOR SHARE`, settingVault).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		m.lockSeeds()
		err = ErrVaultLocked
	} else if err != nil {
		err = ErrVaultUnavailable
	} else {
		err = m.checkFingerprint(raw)
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (m *Manager) checkFingerprint(raw string) error {
	if raw == "" || sha256.Sum256([]byte(raw)) != m.vaultFingerprint {
		m.lockSeeds()
		return ErrVaultLocked
	}
	return nil
}

func normalizeMnemonic(mnemonic string) string { return strings.Join(strings.Fields(mnemonic), " ") }

func contentsDerivers(c *vaultContents) (*Deriver, *Deriver, error) {
	deposit, err := NewDeriver(c.Deposit)
	if err != nil {
		return nil, nil, errors.New("encrypted vault must contain two valid independent mnemonics")
	}
	hot, err := NewDeriver(c.Hot)
	if err != nil {
		clear(deposit.seed)
		return nil, nil, errors.New("encrypted vault must contain two valid independent mnemonics")
	}
	if string(deposit.seed) == string(hot.seed) {
		clear(deposit.seed)
		clear(hot.seed)
		return nil, nil, errors.New("hot seed must differ from deposit seed")
	}
	return deposit, hot, nil
}
