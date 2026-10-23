package wallet

import (
	"context"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"strconv"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/ethereum/go-ethereum/common"
)

// Manager owns seed residency and serializes every operation that uses seeds.
// Callers must not hold the sweep advisory lock when initializing or importing seeds.
// No method re-enters mu, and no DB connection is retained between operations.
type Manager struct {
	db                 *sql.DB
	mu                 sync.Mutex
	deposit, hot       *Deriver
	destination        string
	enabled            bool
	vaultFingerprint   [32]byte
	securityKey        []byte
	securityEnabled    bool
	securityRegistered bool
	securityStatus     SecurityStatus
	securityIncident   *securityIncident
	signingAuthority   []byte
	signingGrants      map[[32]byte]bool
	cipher             cipher.AEAD
	configured         bool
}

// New uses only the supplied persistent installation key. A missing or invalid
// key fails closed; random process-local fallback keys are never generated.
func New(db *sql.DB, masterKey ...string) *Manager {
	m := &Manager{db: db, enabled: true}
	m.initializeSigningAuthority()
	if len(masterKey) == 1 {
		m.cipher = newSeedCipher(masterKey[0])
		m.securityKey = newSecurityKey(masterKey[0])
	}
	return m
}

// SetDestination configures the only ERC20 recipient. Empty clears it; invalid
// input clears it as well so a previous recipient cannot survive bad config.
func (m *Manager) SetDestination(address string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revokeSigningGrants()
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
	if !enabled {
		m.revokeSigningGrants()
	}
}

// Close erases resident key material during service teardown. It is not a
// user-visible wallet lock; subsequent operations reload the persisted seeds.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearSeeds()
	m.revokeSigningGrants()
	clear(m.signingAuthority)
	m.signingAuthority = nil
}

func (m *Manager) clearSeeds() {
	if m.deposit != nil {
		clear(m.deposit.seed)
	}
	if m.hot != nil {
		clear(m.hot.seed)
	}
	m.deposit, m.hot = nil, nil
	m.vaultFingerprint = [32]byte{}
}

func (m *Manager) ready() bool { return m.deposit != nil && m.hot != nil }

// Status reads current ciphertext and automatically refreshes resident seeds.
// Polling never writes or performs blockchain operations.
func (m *Manager) Status(ctx context.Context) (*Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	st := &Status{OK: m.db != nil && m.cipher != nil, Enabled: m.enabled && m.destination != ""}
	if m.db == nil {
		return st, nil
	}
	raw, err := readVault(ctx, m.db, false)
	if err != nil {
		m.clearSeeds()
		return nil, err
	}
	m.configured = raw != ""
	st.Configured = m.configured
	if raw != "" {
		st.Ready = m.refreshContents(raw) == nil
	} else {
		m.clearSeeds()
	}
	if m.securityEnabled {
		status := m.securityStatus
		if raw != "" && !st.Ready {
			status = unavailableSecurityStatus("vault_unavailable")
		}
		if m.db != nil && st.Ready {
			var securityRaw string
			if err := m.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, settingSecurity).Scan(&securityRaw); err == nil {
				if state, err := m.decodeSecurity(securityRaw); err == nil {
					status = securityView(state)
				} else {
					status = unavailableSecurityStatus("security_snapshot_authentication_failed")
				}
			} else {
				status = unavailableSecurityStatus("security_snapshot_missing")
			}
		}
		if m.securityIncident != nil {
			status = m.securityStatus
		}
		st.Security = &status
		st.Enabled = st.Enabled && status.Initialized && !status.Locked
	}
	return st, nil
}

// Initialize installs two independent seeds exactly once. Existing ciphertext,
// including the legacy format, is never replaced through initialization.
func (m *Manager) Initialize(ctx context.Context, depositMnemonic, hotMnemonic string) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	err := m.initialize(ctx, depositMnemonic, hotMnemonic)
	if err != nil {
		return m.denied("wallet_init", err)
	}
	return nil
}

func (m *Manager) initialize(ctx context.Context, depositMnemonic, hotMnemonic string) error {
	if m.cipher == nil {
		return ErrVaultUnavailable
	}
	tx, err := m.beginVaultTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	raw, err := readVault(ctx, tx, true)
	if err != nil {
		return err
	}
	if raw != "" {
		return ErrSeedReplacement
	}
	contents := &vaultContents{Deposit: normalizeMnemonic(depositMnemonic), Hot: normalizeMnemonic(hotMnemonic)}
	defer func() { contents.Deposit, contents.Hot = "", "" }()
	deposit, hot, err := contentsDerivers(contents)
	if err != nil {
		return err
	}
	defer clear(deposit.seed)
	defer clear(hot.seed)
	if err := validateDepositAddresses(ctx, tx, deposit); err != nil {
		return err
	}
	sealed, err := m.sealContents(contents.Deposit, contents.Hot)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, NOW())
        ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`, settingVault, sealed); err != nil {
		return ErrVaultUnavailable
	}
	if err := writeAudit(ctx, tx, auditEntry{op: "wallet_init", result: "ok"}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		m.clearSeeds()
		return ErrVaultUnavailable
	}
	m.configured = true
	return m.refreshContents(sealed)
}

// Derive returns deposit addresses using the former signer's BIP44 paths.
func (m *Manager) Derive(ctx context.Context, start, count uint32) ([]addresses.Derived, error) {
	if err := mode.CheckWritable(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if count == 0 || count > 500 || start >= 1<<31 || uint64(start)+uint64(count) > 1<<31 {
		return nil, m.denied("derive", errors.New("derivation count must be 1-500 within nonhardened index range"))
	}
	storeTx, err := m.currentVaultTx(ctx)
	if err != nil {
		return nil, m.denied("derive", err)
	}
	defer func() { _ = storeTx.Rollback() }()
	if err := m.checkSecurityInTx(ctx, storeTx); err != nil {
		_ = storeTx.Rollback()
		return nil, m.denied("derive", err)
	}
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
		m.clearSeeds()
		err = ErrWalletNotConfigured
	} else if err != nil {
		err = ErrVaultUnavailable
	} else {
		err = m.refreshContents(raw)
	}
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// Called with mu held and (for signing/derivation) the ciphertext row locked.
// A concurrent managed-seed update is loaded before any key is used.
func (m *Manager) refreshContents(raw string) error {
	if raw == "" {
		m.clearSeeds()
		return ErrWalletNotConfigured
	}
	if m.ready() && sha256.Sum256([]byte(raw)) == m.vaultFingerprint {
		return nil
	}
	m.clearSeeds()
	contents, err := m.openContents(raw)
	if err != nil {
		return err
	}
	defer func() { contents.Deposit, contents.Hot = "", "" }()
	deposit, hot, err := contentsDerivers(contents)
	if err != nil {
		return err
	}
	m.deposit, m.hot = deposit, hot
	m.vaultFingerprint = sha256.Sum256([]byte(raw))
	m.configured = true
	m.securityRegistered = contents.SecurityVersion >= 1
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
