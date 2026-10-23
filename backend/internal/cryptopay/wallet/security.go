package wallet

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
)

const settingSecurity = "crypto.wallet.security.v1"

// ErrSecurityLocked means both signing and new deposit addresses are suspended.
var ErrSecurityLocked = errors.New("wallet security lock is active; operator review is required")

// SecurityStatus describes the persistent incident gate without revealing keys.
type SecurityStatus struct {
	Enabled        bool   `json:"enabled"`
	Initialized    bool   `json:"initialized"`
	Locked         bool   `json:"locked"`
	DepositsPaused bool   `json:"deposits_paused"`
	Reason         string `json:"reason,omitempty"`
	LockedAt       string `json:"locked_at,omitempty"`
	AlertPending   bool   `json:"alert_pending"`
}

type addressSnapshot struct {
	ID      int64  `json:"id"`
	UserID  int64  `json:"user_id,omitempty"`
	Family  string `json:"family"`
	Index   int64  `json:"index"`
	Address string `json:"address"`
}

type securityIncident struct{ reason, chain, txHash string }

type securityState struct {
	Version       int                              `json:"version"`
	Assignments   map[string]addressSnapshot       `json:"assignments"`
	Pool          map[string]addressSnapshot       `json:"pool"`
	Locked        bool                             `json:"locked"`
	Reason        string                           `json:"reason,omitempty"`
	LockedAt      string                           `json:"locked_at,omitempty"`
	Alerted       bool                             `json:"alerted"`
	IncidentChain string                           `json:"incident_chain,omitempty"`
	IncidentTx    string                           `json:"incident_tx,omitempty"`
	Acknowledged  map[string]bool                  `json:"acknowledged,omitempty"`
	Legacy        map[string]transferAuthorization `json:"legacy,omitempty"`
}

type authenticatedSecurityState struct {
	Payload json.RawMessage `json:"payload"`
	MAC     string          `json:"mac"`
}

func newSecurityKey(masterHex string) []byte {
	master, err := hex.DecodeString(masterHex)
	if err != nil || len(master) != 32 {
		clear(master)
		return nil
	}
	defer clear(master)
	key, err := hkdf.Key(sha256.New, master, nil, "sub2api/crypto-wallet/security-snapshot/v1", 32)
	if err != nil {
		return nil
	}
	return key
}

// EnableSecurityMonitoring activates the production guard. Direct wallet unit
// fixtures may omit it; the production cryptopay facade always enables it.
func (m *Manager) EnableSecurityMonitoring() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.securityEnabled = true
	m.securityStatus.Enabled = true
}

func emptySecurityState() *securityState {
	return &securityState{Version: 1, Assignments: map[string]addressSnapshot{}, Pool: map[string]addressSnapshot{}, Acknowledged: map[string]bool{}, Legacy: map[string]transferAuthorization{}}
}

func (m *Manager) encodeSecurity(state *securityState) (string, error) {
	if len(m.securityKey) != 32 {
		return "", ErrVaultUnavailable
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	if len(payload) > 16<<20 {
		return "", ErrVaultUnavailable
	}
	mac := hmac.New(sha256.New, m.securityKey)
	_, err = mac.Write(payload)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(authenticatedSecurityState{Payload: payload, MAC: hex.EncodeToString(mac.Sum(nil))})
	return string(data), err
}

func (m *Manager) decodeSecurity(raw string) (*securityState, error) {
	if len(m.securityKey) != 32 || len(raw) > 16<<20 {
		return nil, ErrSecurityLocked
	}
	var envelope authenticatedSecurityState
	if json.Unmarshal([]byte(raw), &envelope) != nil {
		return nil, ErrSecurityLocked
	}
	got, err := hex.DecodeString(envelope.MAC)
	if err != nil {
		return nil, ErrSecurityLocked
	}
	mac := hmac.New(sha256.New, m.securityKey)
	_, err = mac.Write(envelope.Payload)
	if err != nil {
		return nil, ErrSecurityLocked
	}
	if !hmac.Equal(got, mac.Sum(nil)) {
		return nil, ErrSecurityLocked
	}
	var state securityState
	if json.Unmarshal(envelope.Payload, &state) != nil || state.Version != 1 || state.Assignments == nil || state.Pool == nil {
		return nil, ErrSecurityLocked
	}
	if state.Acknowledged == nil {
		state.Acknowledged = map[string]bool{}
	}
	if state.Legacy == nil {
		state.Legacy = map[string]transferAuthorization{}
	}
	return &state, nil
}

func (m *Manager) loadSecurity(ctx context.Context, tx *sql.Tx, lock string) (*securityState, bool, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1 `+lock, settingSecurity).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, ErrVaultUnavailable
	}
	state, err := m.decodeSecurity(raw)
	return state, true, err
}

func (m *Manager) storeSecurity(ctx context.Context, tx *sql.Tx, state *securityState) error {
	raw, err := m.encodeSecurity(state)
	if err != nil {
		return ErrVaultUnavailable
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW()) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, settingSecurity, raw)
	if err != nil {
		return ErrVaultUnavailable
	}
	return nil
}

func securityView(state *securityState) SecurityStatus {
	return SecurityStatus{Enabled: true, Initialized: true, Locked: state.Locked, DepositsPaused: state.Locked, Reason: state.Reason, LockedAt: state.LockedAt, AlertPending: state.Locked && !state.Alerted}
}

func unavailableSecurityStatus(reason string) SecurityStatus {
	return SecurityStatus{Enabled: true, Locked: true, DepositsPaused: true, Reason: reason, AlertPending: true}
}

func snapshotKey(userID int64, family string) string    { return fmt.Sprintf("%d:%s", userID, family) }
func poolSnapshotKey(index int64, family string) string { return fmt.Sprintf("%s:%d", family, index) }

func (m *Manager) validateSnapshotAddress(row addressSnapshot) error {
	if row.ID <= 0 || row.Index < 0 || row.Index >= 1<<31 {
		return ErrDepositSeedMismatch
	}
	switch row.Family {
	case addresses.FamilyEVM:
		_, address, err := m.deposit.EVMKey(uint32(row.Index))
		if err != nil || !strings.EqualFold(address.Hex(), row.Address) {
			return ErrDepositSeedMismatch
		}
	case addresses.FamilySolana:
		key, address, err := m.deposit.SolanaKey(uint32(row.Index))
		clear(key)
		if err != nil || address.String() != row.Address {
			return ErrDepositSeedMismatch
		}
	default:
		return ErrDepositSeedMismatch
	}
	return nil
}

func loadAddressSnapshots(ctx context.Context, tx *sql.Tx) (map[string]addressSnapshot, map[string]addressSnapshot, error) {
	rows, err := tx.QueryContext(ctx, `SELECT 'pool', id, 0, chain_family, derivation_index, address FROM crypto_address_pool
		UNION ALL SELECT 'assignment',id,user_id,CASE WHEN chain IN ('ethereum','bsc','polygon','plasma','arbitrum','base','xlayer') THEN 'evm' ELSE chain END,derivation_index,address FROM deposit_addresses`)
	if err != nil {
		return nil, nil, ErrVaultUnavailable
	}
	defer func() { _ = rows.Close() }()
	assignments, pool := map[string]addressSnapshot{}, map[string]addressSnapshot{}
	for rows.Next() {
		if len(assignments)+len(pool) > 100000 {
			return nil, nil, ErrVaultUnavailable
		}
		var kind string
		var row addressSnapshot
		if rows.Scan(&kind, &row.ID, &row.UserID, &row.Family, &row.Index, &row.Address) != nil {
			return nil, nil, ErrVaultUnavailable
		}
		if row.Family == addresses.FamilyEVM {
			row.Address = strings.ToLower(row.Address)
		}
		if kind == "pool" {
			key := poolSnapshotKey(row.Index, row.Family)
			if _, exists := pool[key]; exists {
				return nil, nil, ErrDepositSeedMismatch
			}
			pool[key] = row
		} else {
			key := snapshotKey(row.UserID, row.Family)
			if _, exists := assignments[key]; exists {
				return nil, nil, ErrDepositSeedMismatch
			}
			assignments[key] = row
		}
	}
	if rows.Err() != nil {
		return nil, nil, ErrVaultUnavailable
	}
	return assignments, pool, nil
}

func (m *Manager) compareAddressSnapshot(state *securityState, assignments, pool map[string]addressSnapshot) error {
	for key, old := range state.Assignments {
		if now, ok := assignments[key]; !ok || now != old {
			return errors.New("deposit_address_changed")
		}
	}
	for key, old := range state.Pool {
		if now, ok := pool[key]; !ok || now != old {
			return errors.New("deposit_pool_changed")
		}
	}
	owners := map[string]int64{}
	for key, row := range assignments {
		addressKey := row.Family + ":" + row.Address
		if owner, exists := owners[addressKey]; exists && owner != row.UserID {
			return errors.New("deposit_address_changed")
		}
		owners[addressKey] = row.UserID
		if row.UserID <= 0 {
			return errors.New("deposit_address_derivation_mismatch")
		}
		if _, ok := state.Assignments[key]; !ok {
			if err := m.validateSnapshotAddress(row); err != nil {
				return errors.New("deposit_address_derivation_mismatch")
			}
		}
	}
	for key, row := range pool {
		if _, ok := state.Pool[key]; !ok {
			if err := m.validateSnapshotAddress(row); err != nil {
				return errors.New("deposit_pool_derivation_mismatch")
			}
		}
	}
	return nil
}

func (m *Manager) persistIncident(ctx context.Context, tx *sql.Tx, state *securityState, reason, chain, txHash string) error {
	m.localIncident(reason, chain, txHash)
	if state == nil {
		state = emptySecurityState()
	}
	if !state.Locked {
		state.Locked = true
		state.Reason = m.securityIncident.reason
		state.LockedAt = m.securityStatus.LockedAt
		state.Alerted = false
		state.IncidentChain = m.securityIncident.chain
		state.IncidentTx = m.securityIncident.txHash
	}
	if err := m.storeSecurity(ctx, tx, state); err != nil {
		return err
	}
	if err := writeAudit(ctx, tx, auditEntry{op: "security_lock", result: "locked", reason: state.Reason}); err != nil {
		return err
	}
	registeredRaw := ""
	if !m.securityRegistered {
		var raw string
		if tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, settingVault).Scan(&raw) != nil {
			return ErrVaultUnavailable
		}
		contents, err := m.openContents(raw)
		if err != nil {
			return err
		}
		contents.SecurityVersion = 1
		registeredRaw, err = m.sealVaultContents(*contents)
		contents.Deposit, contents.Hot = "", ""
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE settings SET value=$2,updated_at=NOW() WHERE key=$1`, settingVault, registeredRaw); err != nil {
			return ErrVaultUnavailable
		}
	}
	if err := tx.Commit(); err != nil {
		return ErrVaultUnavailable
	}
	if registeredRaw != "" {
		if err := m.refreshContents(registeredRaw); err != nil {
			return err
		}
	}
	m.securityStatus = securityView(state)
	m.securityIncident = nil
	return ErrSecurityLocked
}

func (m *Manager) localIncident(reason, chain, txHash string) {
	m.revokeSigningGrants()
	if m.securityIncident == nil {
		m.securityIncident = &securityIncident{reason, chain, txHash}
	}
	if !m.securityStatus.Locked {
		m.securityStatus = unavailableSecurityStatus(reason)
		m.securityStatus.LockedAt = time.Now().UTC().Format(time.RFC3339)
	}
}

// CheckSecurity validates persistent snapshots. It runs on a five-second
// scanner/background cadence, not on AI request hot paths. New rows are accepted
// only after deterministic derivation; changing/deleting an old row locks.
func (m *Manager) CheckSecurity(ctx context.Context) (SecurityStatus, error) {
	if err := mode.CheckWritable(); err != nil {
		return SecurityStatus{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.securityEnabled {
		return SecurityStatus{}, nil
	}
	if m.db == nil || len(m.securityKey) != 32 {
		return unavailableSecurityStatus("security_unavailable"), ErrVaultUnavailable
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return SecurityStatus{}, ErrVaultUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR UPDATE`, settingVault).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return SecurityStatus{Enabled: true}, ErrWalletNotConfigured
	}
	if err != nil {
		return SecurityStatus{}, ErrVaultUnavailable
	}
	if err = m.refreshContents(raw); err != nil {
		return unavailableSecurityStatus("vault_unavailable"), err
	}
	state, found, err := m.loadSecurity(ctx, tx, "FOR UPDATE")
	if err != nil {
		if !errors.Is(err, ErrSecurityLocked) {
			return SecurityStatus{}, err
		}
		err = m.persistIncident(ctx, tx, nil, "security_snapshot_authentication_failed", "", "")
		return m.securityStatus, err
	}
	if m.securityIncident != nil {
		i := m.securityIncident
		err = m.persistIncident(ctx, tx, state, i.reason, i.chain, i.txHash)
		return m.securityStatus, err
	}
	if !found {
		if m.securityRegistered {
			err = m.persistIncident(ctx, tx, nil, "security_snapshot_missing", "", "")
			return m.securityStatus, err
		}
		state = emptySecurityState()
	}
	if state.Locked {
		m.securityStatus = securityView(state)
		return m.securityStatus, ErrSecurityLocked
	}
	assignments, pool, err := loadAddressSnapshots(ctx, tx)
	if err != nil {
		if errors.Is(err, ErrDepositSeedMismatch) {
			err = m.persistIncident(ctx, tx, state, "deposit_address_derivation_mismatch", "", "")
			return m.securityStatus, err
		}
		return SecurityStatus{}, err
	}
	if err = m.compareAddressSnapshot(state, assignments, pool); err != nil {
		err = m.persistIncident(ctx, tx, state, err.Error(), "", "")
		return m.securityStatus, err
	}
	changed := !found || len(assignments) != len(state.Assignments) || len(pool) != len(state.Pool)
	state.Assignments, state.Pool = assignments, pool
	if !found {
		state.Legacy, err = m.captureLegacyAuthorizations(ctx, tx)
		if err != nil {
			return SecurityStatus{}, err
		}
	}
	if changed {
		if err = m.storeSecurity(ctx, tx, state); err != nil {
			return SecurityStatus{}, err
		}
	}
	if !m.securityRegistered {
		contents, err := m.openContents(raw)
		if err != nil {
			return SecurityStatus{}, err
		}
		contents.SecurityVersion = 1
		sealed, err := m.sealVaultContents(*contents)
		contents.Deposit, contents.Hot = "", ""
		if err != nil {
			return SecurityStatus{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE settings SET value=$2,updated_at=NOW() WHERE key=$1`, settingVault, sealed); err != nil {
			return SecurityStatus{}, ErrVaultUnavailable
		}
		if err = tx.Commit(); err != nil {
			return SecurityStatus{}, ErrVaultUnavailable
		}
		m.securityRegistered = true
		if err = m.refreshContents(sealed); err != nil {
			return SecurityStatus{}, err
		}
	} else if err = tx.Commit(); err != nil {
		return SecurityStatus{}, ErrVaultUnavailable
	}
	m.securityStatus = securityView(state)
	return m.securityStatus, nil
}

// checkSecurityInTx is mandatory for every signature. The shared row lock is
// retained through the signature/audit commit; missing state never unlocks it.
func (m *Manager) checkSecurityInTx(ctx context.Context, tx *sql.Tx) error {
	if !m.securityEnabled {
		return nil
	}
	if m.securityIncident != nil {
		return ErrSecurityLocked
	}
	state, found, err := m.loadSecurity(ctx, tx, "FOR SHARE")
	if err != nil {
		return err
	}
	if !m.securityRegistered || !found || state.Locked {
		return ErrSecurityLocked
	}
	m.securityStatus = securityView(state)
	return nil
}

// SecurityLocked is a cheap local gate. Authoritative DB checks are performed
// by CheckSecurity and by every signing operation.
func (m *Manager) SecurityLocked() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.securityEnabled && (m.securityStatus.Locked || !m.securityStatus.Initialized)
}

// ValidateAssignment protects the exact address returned by the customer API.
// It reads only one authenticated setting and checks the requested assignment;
// full deletion/change detection remains in the periodic snapshot pass.
func (m *Manager) ValidateAssignment(ctx context.Context, row addresses.Assignment) error {
	if _, err := m.CheckSecurity(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	if !m.securityEnabled {
		m.mu.Unlock()
		return nil
	}
	tx, err := m.currentVaultTx(ctx)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	state, found, err := m.loadSecurity(ctx, tx, "FOR SHARE")
	reason := ""
	if err == nil && found && !state.Locked && m.securityIncident == nil {
		address := row.Address
		if row.Family == addresses.FamilyEVM {
			address = strings.ToLower(address)
		}
		actual := addressSnapshot{ID: row.ID, UserID: row.UserID, Family: row.Family, Index: row.DerivationIndex, Address: address}
		if previous, ok := state.Assignments[snapshotKey(row.UserID, row.Family)]; !ok || previous != actual {
			reason = "deposit_address_changed"
		}
		if m.validateSnapshotAddress(actual) != nil {
			reason = "deposit_address_derivation_mismatch"
		}
	} else if err == nil {
		err = ErrSecurityLocked
	}
	_ = tx.Rollback()
	m.mu.Unlock()
	if reason != "" {
		return m.LockSecurity(ctx, reason, "", "")
	}
	return err
}

// LockSecurity persists an observed incident before the caller alerts anyone.
func (m *Manager) LockSecurity(ctx context.Context, reason, chain, txHash string) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.securityEnabled {
		return ErrSecurityLocked
	}
	m.localIncident(reason, chain, txHash)
	if m.db == nil {
		return ErrVaultUnavailable
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrVaultUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	var raw string
	if tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR UPDATE`, settingVault).Scan(&raw) != nil {
		return ErrVaultUnavailable
	}
	state, _, loadErr := m.loadSecurity(ctx, tx, "FOR UPDATE")
	if loadErr != nil && !errors.Is(loadErr, ErrSecurityLocked) {
		return loadErr
	}
	return m.persistIncident(ctx, tx, state, reason, chain, txHash)
}

// DeliverSecurityAlert serializes operator notification across instances using
// the incident row. Delivery failure leaves the durable pending flag intact.
// The callback must not call back into Manager.
func (m *Manager) DeliverSecurityAlert(ctx context.Context, deliver func(SecurityStatus) error) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return ErrVaultUnavailable
	}
	if m.securityIncident != nil {
		return ErrSecurityLocked
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrVaultUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	state, found, err := m.loadSecurity(ctx, tx, "FOR UPDATE")
	if err != nil {
		return err
	}
	if !found || !state.Locked || state.Alerted {
		return nil
	}
	if err = deliver(securityView(state)); err != nil {
		return err
	}
	state.Alerted = true
	if err = m.storeSecurity(ctx, tx, state); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return ErrVaultUnavailable
	}
	m.securityStatus = securityView(state)
	return nil
}

// RecoverSecurity requires a fresh operator-2FA HTTP boundary. It never changes
// seeds or balances; every current address must still derive from the vault.
func (m *Manager) RecoverSecurity(ctx context.Context) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return ErrVaultUnavailable
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrVaultUnavailable
	}
	defer func() { _ = tx.Rollback() }()
	var raw string
	if tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR UPDATE`, settingVault).Scan(&raw) != nil {
		return ErrVaultUnavailable
	}
	if err = m.refreshContents(raw); err != nil {
		return err
	}
	old, found, err := m.loadSecurity(ctx, tx, "FOR UPDATE")
	if err != nil {
		return err
	}
	if !found || !old.Locked {
		return errors.New("wallet has no persistent incident to recover")
	}
	assignments, pool, err := loadAddressSnapshots(ctx, tx)
	if err != nil {
		return err
	}
	next := emptySecurityState()
	if err = m.compareAddressSnapshot(next, assignments, pool); err != nil {
		return err
	}
	next.Assignments, next.Pool = assignments, pool
	next.Acknowledged = old.Acknowledged
	next.Legacy = old.Legacy
	if old.IncidentChain != "" && old.IncidentTx != "" {
		next.Acknowledged[old.IncidentChain+":"+strings.ToLower(old.IncidentTx)] = true
	}
	if err = m.storeSecurity(ctx, tx, next); err != nil {
		return err
	}
	if err = writeAudit(ctx, tx, auditEntry{op: "security_recover", result: "ok"}); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return ErrVaultUnavailable
	}
	m.securityStatus = securityView(next)
	m.securityIncident = nil
	return nil
}
