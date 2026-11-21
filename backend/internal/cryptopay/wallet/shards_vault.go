package wallet

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet/shards"
)

// Browser-side shard setup.
//
// The data key DK that seals crypto.signer.vault is generated in the admin
// browser; the browser splits it into three shamir-gf256 shares (2-of-3) and
// uploads only share A together with the re-encrypted vault ciphertext and a
// public commitment. Share B and C are shown once by the browser and never
// stored by the server. The server seals A with its installation key, so a
// database dump alone cannot open the vault: a second, operator-held share is
// always required.
//
// A migration window is unavoidable: the vault was historically sealed with
// the installation key, so the server decrypts it in memory once, re-seals it
// with the browser-supplied DK and clears both the DK and the plaintext
// immediately. That window is audited (`signer_shards_setup`). The stricter
// "server never touches plaintext" path (generate a brand-new wallet and
// offline-sign the old balances) is documented as an optional rotation and is
// intentionally not implemented here.
const (
	settingShards       = "crypto.signer.vault.shards"
	settingHotAddresses = "crypto.signer.hot.addresses"

	shardRecordVersion     = 1
	shareACipherContext    = "sub2api/crypto-wallet/shard-a/v1"
	autoShareCipherContext = "sub2api/crypto-wallet/auto-shard/v1"

	// dataKeyLease bounds how long one reconstructed data key stays resident.
	// The caller is expected to call LockSigner as soon as its operation ends;
	// the lease is a hard upper bound, not a session.
	dataKeyLease = 15 * time.Minute

	shareFailureLimit  = 5
	shareFailureWindow = 15 * time.Minute

	maxShareInputBytes = 4096
)

// ShardStatus is the public, secret-free description of the signer setup.
type ShardStatus struct {
	Configured   bool   `json:"configured"`
	Scheme       string `json:"scheme,omitempty"`
	Threshold    int    `json:"threshold,omitempty"`
	Total        int    `json:"total,omitempty"`
	VaultVersion int    `json:"vault_version"`
	Locked       bool   `json:"locked"`
	AutoEnabled  bool   `json:"auto_enabled"`
	CreatedAt    string `json:"created_at,omitempty"`
	CreatedBy    int64  `json:"created_by,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	// Commitment is a salted SHA-256 of the data key. It is public: the
	// browser uses it to verify that a recovery share belongs to this wallet,
	// and it cannot be inverted into the key.
	Commitment string `json:"dk_commitment,omitempty"`
}

type shardRecord struct {
	Version       int    `json:"version"`
	Scheme        string `json:"scheme"`
	Threshold     int    `json:"threshold"`
	Total         int    `json:"total"`
	ShareA        string `json:"share_a"`
	Commitment    string `json:"dk_commitment"`
	AutoShare     string `json:"auto_share,omitempty"`
	AutoEnabled   bool   `json:"auto_enabled,omitempty"`
	CreatedAt     string `json:"created_at"`
	CreatedBy     int64  `json:"created_by"`
	UpdatedAt     string `json:"updated_at"`
	AutoUpdatedAt string `json:"auto_updated_at,omitempty"`
}

// ShardSetupRequest carries what the browser uploads. DataKey is used only
// inside this call: it re-seals the vault once and is then cleared.
type ShardSetupRequest struct {
	DataKey      []byte
	ShareA       string
	ConfirmShare string
	Commitment   string // base64 of shards.Commitment(DataKey)
	// EnforceRiskAck is recorded in the audit reason. The HTTP layer verifies
	// the operator typed the risk phrase; the wallet only persists the fact.
	RiskAcknowledged bool
}

type hotAddressRecord struct {
	EVM    string `json:"evm"`
	Solana string `json:"solana"`
}

func readSetting(ctx context.Context, r vaultReader, key string) (string, error) {
	var value string
	err := r.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", ErrVaultUnavailable
	}
	return value, nil
}

type settingWriter interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func writeSetting(ctx context.Context, w settingWriter, key, value string) error {
	if _, err := w.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, NOW())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()`, key, value); err != nil {
		return ErrVaultUnavailable
	}
	return nil
}

func (m *Manager) sealMasterContext(plaintext []byte, context string) (string, error) {
	if len(m.masterKey) != 32 {
		return "", ErrVaultUnavailable
	}
	key, err := hkdf.Key(sha256.New, m.masterKey, nil, context, 32)
	if err != nil {
		return "", ErrVaultUnavailable
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", ErrVaultUnavailable
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return "", ErrVaultUnavailable
	}
	sealed := aead.Seal(nil, nil, plaintext, []byte(context))
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func (m *Manager) openMasterContext(value, context string) ([]byte, error) {
	if len(m.masterKey) != 32 {
		return nil, ErrVaultUnavailable
	}
	sealed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(sealed) < 12+16 {
		return nil, ErrVaultUnavailable
	}
	key, err := hkdf.Key(sha256.New, m.masterKey, nil, context, 32)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	plaintext, err := aead.Open(nil, nil, sealed, []byte(context))
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	return plaintext, nil
}

func decodeCommitment(value string) ([32]byte, error) {
	var out [32]byte
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(decoded) != len(out) {
		return out, ErrSignerShareInvalid
	}
	copy(out[:], decoded)
	return out, nil
}

// recoverDataKeyLocked combines the operator share with the server-held share
// A and proves the result against the published commitment and the vault. It
// never logs or returns share material. Caller owns the returned slice and
// must clear it.
func (m *Manager) recoverDataKey(ctx context.Context, operatorShare string) ([]byte, error) {
	if len(m.masterKey) != 32 || m.db == nil {
		return nil, ErrVaultUnavailable
	}
	if err := m.ensureShareAttemptsAllowed(ctx); err != nil {
		return nil, err
	}
	record, err := m.loadShardRecord(ctx)
	if err != nil {
		return nil, err
	}
	if record == nil || !recordConfigured(record) {
		return nil, ErrShardsNotConfigured
	}
	sealedShareA, err := m.openMasterContext(record.ShareA, shareACipherContext)
	if err != nil {
		return nil, ErrShardsNotConfigured
	}
	shareAText := strings.TrimSpace(string(sealedShareA))
	clear(sealedShareA)
	if shareAText == "" {
		return nil, ErrShardsNotConfigured
	}
	dataKey, combineErr := shards.ParseAndCombine([]string{shareAText, operatorShare})
	if combineErr != nil {
		m.recordShareFailure(ctx, "share_parse")
		return nil, ErrSignerShareInvalid
	}
	commitment, err := decodeCommitment(record.Commitment)
	if err != nil {
		clear(dataKey)
		return nil, ErrShardsNotConfigured
	}
	if shards.Commitment(dataKey) != commitment {
		clear(dataKey)
		m.recordShareFailure(ctx, "commitment")
		return nil, ErrSignerShareInvalid
	}
	raw, err := readVault(ctx, m.db, false)
	if err != nil || raw == "" || vaultVersion(raw) != 3 {
		clear(dataKey)
		return nil, ErrShardsNotConfigured
	}
	contents, err := openVaultContentsV3(dataKey, raw)
	if err != nil {
		clear(dataKey)
		m.recordShareFailure(ctx, "vault")
		return nil, ErrSignerShareInvalid
	}
	contents.Deposit, contents.Hot = "", ""
	return dataKey, nil
}

func recordConfigured(record *shardRecord) bool {
	return record != nil && record.Version == shardRecordVersion && strings.TrimSpace(record.ShareA) != "" &&
		strings.TrimSpace(record.Commitment) != ""
}

func (m *Manager) loadShardRecord(ctx context.Context) (*shardRecord, error) {
	raw, err := readSetting(ctx, m.db, settingShards)
	if err != nil || raw == "" {
		return nil, err
	}
	if len(raw) > 16<<10 {
		return nil, ErrVaultUnavailable
	}
	var record shardRecord
	if json.Unmarshal([]byte(raw), &record) != nil {
		return nil, ErrVaultUnavailable
	}
	return &record, nil
}

// ensureShareAttemptsAllowed fails closed after five rejected shares in the
// rolling window. Rejections are counted from the durable audit trail, so a
// multi-instance deployment shares the lockout.
func (m *Manager) ensureShareAttemptsAllowed(ctx context.Context) error {
	if m.db == nil {
		return ErrVaultUnavailable
	}
	var failed int
	query := `SELECT COUNT(*) FROM crypto_signer_audit
		WHERE op = 'signer_share_verify' AND result = 'denied' AND occurred_at > NOW() - make_interval(secs => $1)`
	if err := m.db.QueryRowContext(ctx, query, int(shareFailureWindow/time.Second)).Scan(&failed); err != nil {
		return ErrAuditUnavailable
	}
	if failed >= shareFailureLimit {
		return ErrSignerRateLimited
	}
	return nil
}

func (m *Manager) recordShareFailure(ctx context.Context, reason string) {
	// A failure marker is a security event: never suppress it silently.
	auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	auditCtx = WithAuditActor(auditCtx, AuditActorFromContext(ctx))
	if err := m.audit(auditCtx, auditEntry{op: "signer_share_verify", result: "denied", reason: reason}); err != nil {
		_ = err
	}
}

// VerifyShardShare proves one operator share belongs to the configured vault
// without changing any state. It is the "type one share back" gate before the
// browser enables the new vault.
func (m *Manager) VerifyShardShare(ctx context.Context, share string) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	if strings.TrimSpace(share) == "" || len(share) > maxShareInputBytes {
		return ErrSignerShareInvalid
	}
	dataKey, err := m.recoverDataKey(ctx, share)
	if err != nil {
		return err
	}
	clear(dataKey)
	auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	auditCtx = WithAuditActor(auditCtx, AuditActorFromContext(ctx))
	if err := m.audit(auditCtx, auditEntry{op: "signer_share_verify", result: "ok"}); err != nil {
		return err
	}
	return nil
}

// UnlockSigner reconstructs the data key for one bounded operation and keeps
// it resident only until LockSigner is called (hard-capped by dataKeyLease).
func (m *Manager) UnlockSigner(ctx context.Context, share string) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	if strings.TrimSpace(share) == "" || len(share) > maxShareInputBytes {
		return ErrSignerShareInvalid
	}
	m.signerOpMu.Lock()
	defer m.signerOpMu.Unlock()
	dataKey, err := m.recoverDataKey(ctx, share)
	if err != nil {
		return err
	}
	m.installDataKey(dataKey)
	clear(dataKey)
	return nil
}

// UnlockAutoSigner uses the sealed automatic-sweep share. It is only valid
// when the operator explicitly enabled automatic sweeps (equivalent to the
// server holding A and B).
func (m *Manager) UnlockAutoSigner(ctx context.Context) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	m.signerOpMu.Lock()
	defer m.signerOpMu.Unlock()
	record, err := m.loadShardRecord(ctx)
	if err != nil {
		return err
	}
	if !recordConfigured(record) || !record.AutoEnabled || strings.TrimSpace(record.AutoShare) == "" {
		return ErrShardsNotConfigured
	}
	sealed, err := m.openMasterContext(record.AutoShare, autoShareCipherContext)
	if err != nil {
		return ErrSignerShareInvalid
	}
	share := strings.TrimSpace(string(sealed))
	clear(sealed)
	dataKey, err := m.recoverDataKey(ctx, share)
	if err != nil {
		return err
	}
	m.installDataKey(dataKey)
	clear(dataKey)
	return nil
}

func (m *Manager) installDataKey(dataKey []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearDataKey()
	m.dataKey = append([]byte(nil), dataKey...)
	m.dataKeyDeadline = time.Now().Add(dataKeyLease)
	// Force the next seed load to use the reconstructed key.
	m.clearSeeds()
}

// LockSigner zeroes the reconstructed data key and every cached seed. It is
// safe to call when already locked.
func (m *Manager) LockSigner() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearDataKey()
	m.clearSeeds()
}

// SignerLocked reports whether a v3 vault currently has no usable data key.
func (m *Manager) SignerLocked(ctx context.Context) (bool, error) {
	if m.db == nil {
		return false, ErrVaultUnavailable
	}
	raw, err := readVault(ctx, m.db, false)
	if err != nil {
		return false, err
	}
	version := vaultVersion(raw)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vaultVersion = version
	if version != 3 {
		return false, nil
	}
	return !m.dataKeyUsable(), nil
}

// Unseal runs fn with the data key available. The key and cached seeds are
// cleared the moment fn returns, including on panic.
func (m *Manager) Unseal(ctx context.Context, share string, fn func(context.Context) error) (err error) {
	if fn == nil {
		return ErrVaultUnavailable
	}
	if err := m.UnlockSigner(ctx, share); err != nil {
		return err
	}
	defer m.LockSigner()
	return fn(ctx)
}

// ShardStatus reports the public signer configuration without any mutation.
func (m *Manager) ShardStatus(ctx context.Context) (*ShardStatus, error) {
	if m.db == nil {
		return nil, ErrVaultUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.shardStatusLocked(ctx)
}

func (m *Manager) shardStatusLocked(ctx context.Context) (*ShardStatus, error) {
	raw, err := readVault(ctx, m.db, false)
	if err != nil {
		return nil, err
	}
	version := vaultVersion(raw)
	status := &ShardStatus{VaultVersion: version}
	record, err := m.loadShardRecord(ctx)
	if err != nil {
		return nil, err
	}
	if recordConfigured(record) {
		status.Configured = true
		status.Scheme = record.Scheme
		status.Threshold = record.Threshold
		status.Total = record.Total
		status.AutoEnabled = record.AutoEnabled
		status.CreatedAt = record.CreatedAt
		status.CreatedBy = record.CreatedBy
		status.UpdatedAt = record.UpdatedAt
		status.Commitment = record.Commitment
	}
	status.Locked = version == 3 && !m.dataKeyUsable()
	return status, nil
}

func (m *Manager) shardSetupParams(req ShardSetupRequest) (string, string, [32]byte, error) {
	var commitment [32]byte
	if len(req.DataKey) != 32 {
		return "", "", commitment, ErrSignerShareInvalid
	}
	if strings.TrimSpace(req.ShareA) == "" || strings.TrimSpace(req.ConfirmShare) == "" ||
		len(req.ShareA) > maxShareInputBytes || len(req.ConfirmShare) > maxShareInputBytes {
		return "", "", commitment, ErrSignerShareInvalid
	}
	combined, err := shards.ParseAndCombine([]string{req.ShareA, req.ConfirmShare})
	if err != nil {
		return "", "", commitment, ErrSignerShareInvalid
	}
	defer clear(combined)
	if !equalBytes(combined, req.DataKey) {
		return "", "", commitment, ErrSignerShareInvalid
	}
	commitment = shards.Commitment(req.DataKey)
	published, err := decodeCommitment(req.Commitment)
	if err != nil || published != commitment {
		return "", "", commitment, ErrSignerShareInvalid
	}
	shareAText := strings.TrimSpace(req.ShareA)
	parsed, err := shards.ParseShare(shareAText)
	if err != nil || parsed.Threshold != shards.DefaultThreshold || parsed.Total != shards.DefaultTotal {
		return "", "", commitment, ErrSignerShareInvalid
	}
	return shareAText, base64.StdEncoding.EncodeToString(commitment[:]), commitment, nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var different byte
	for i := range a {
		different |= a[i] ^ b[i]
	}
	return different == 0
}

// SetupShards migrates the installation-key vault to a data-key vault in one
// transaction and stores the sealed share A. It refuses to overwrite an
// existing shard set: re-splitting is an explicit, separate operation.
func (m *Manager) SetupShards(ctx context.Context, req ShardSetupRequest) (*ShardStatus, error) {
	if err := mode.CheckWritable(); err != nil {
		return nil, err
	}
	shareAText, commitment, _, err := m.shardSetupParams(req)
	if err != nil {
		return nil, m.denied(ctx, "signer_shards_setup", err)
	}
	m.signerOpMu.Lock()
	defer m.signerOpMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cipher == nil || m.db == nil || len(m.masterKey) != 32 {
		return nil, ErrVaultUnavailable
	}
	sealedShareA, err := m.sealMasterContext([]byte(shareAText), shareACipherContext)
	if err != nil {
		return nil, err
	}
	tx, err := m.beginVaultTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	existingRaw, err := readSetting(ctx, tx, settingShards)
	if err != nil {
		return nil, err
	}
	if existingRaw != "" {
		var existing shardRecord
		if json.Unmarshal([]byte(existingRaw), &existing) == nil && recordConfigured(&existing) {
			return nil, ErrShardsAlreadyConfigured
		}
	}
	raw, err := readVault(ctx, tx, true)
	if err != nil || raw == "" {
		return nil, ErrWalletNotConfigured
	}
	if vaultVersion(raw) != 2 {
		return nil, ErrShardsNotAllowed
	}
	contents, err := m.openContents(raw)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	defer func() { contents.Deposit, contents.Hot = "", "" }()
	// The migration is only valid if the seeds still match every deposit
	// address already handed to users.
	deposit, hot, err := contentsDerivers(contents)
	if err != nil {
		return nil, ErrDepositSeedMismatch
	}
	defer clear(deposit.seed)
	defer clear(hot.seed)
	if err := validateDepositAddresses(ctx, tx, deposit); err != nil {
		return nil, err
	}
	newVault, err := sealVaultContentsV3(req.DataKey, *contents)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	// Round-trip before committing: a browser format mismatch must not leave
	// the wallet sealed with a key the server cannot use.
	check, err := openVaultContentsV3(req.DataKey, newVault)
	if err != nil || check.Deposit == "" || check.Hot == "" {
		return nil, ErrVaultUnavailable
	}
	check.Deposit, check.Hot = "", ""
	if _, err := tx.ExecContext(ctx, `UPDATE settings SET value = $2, updated_at = NOW() WHERE key = $1`, settingVault, newVault); err != nil {
		return nil, ErrVaultUnavailable
	}
	now := time.Now().UTC().Format(time.RFC3339)
	record := shardRecord{
		Version: shardRecordVersion, Scheme: shards.Scheme,
		Threshold: shards.DefaultThreshold, Total: shards.DefaultTotal,
		ShareA: sealedShareA, Commitment: commitment,
		CreatedAt: now, CreatedBy: AuditActorUserID(ctx), UpdatedAt: now,
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	if err := writeSetting(ctx, tx, settingShards, string(encoded)); err != nil {
		return nil, err
	}
	if err := m.persistHotAddresses(ctx, tx, hot); err != nil {
		return nil, err
	}
	reason := "migrated_from_installation_key"
	if req.RiskAcknowledged {
		reason += ";risk_acknowledged"
	}
	if err := writeAudit(ctx, tx, auditEntry{op: "signer_shards_setup", result: "ok", reason: reason}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, ErrVaultUnavailable
	}
	m.vaultVersion = 3
	m.clearSeeds()
	m.clearDataKey()
	return m.shardStatusLocked(ctx)
}

// ResplitShards replaces share A after the browser re-split the same data key
// into a new 2-of-3 set. The data key is proved against the existing
// commitment and vault first; the old automatic-sweep share is invalidated
// because it belongs to the old split.
func (m *Manager) ResplitShards(ctx context.Context, req ShardSetupRequest) (*ShardStatus, error) {
	if err := mode.CheckWritable(); err != nil {
		return nil, err
	}
	shareAText, commitment, _, err := m.shardSetupParams(req)
	if err != nil {
		return nil, m.denied(ctx, "signer_shards_resplit", err)
	}
	m.signerOpMu.Lock()
	defer m.signerOpMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil || len(m.masterKey) != 32 {
		return nil, ErrVaultUnavailable
	}
	tx, err := m.beginVaultTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	existingRaw, err := readSetting(ctx, tx, settingShards)
	if err != nil || existingRaw == "" {
		return nil, ErrShardsNotConfigured
	}
	var record shardRecord
	if json.Unmarshal([]byte(existingRaw), &record) != nil || !recordConfigured(&record) {
		return nil, ErrShardsNotConfigured
	}
	if record.Commitment != commitment {
		return nil, ErrSignerShareInvalid
	}
	raw, err := readVault(ctx, tx, true)
	if err != nil || vaultVersion(raw) != 3 {
		return nil, ErrShardsNotAllowed
	}
	contents, err := openVaultContentsV3(req.DataKey, raw)
	if err != nil {
		return nil, ErrSignerShareInvalid
	}
	contents.Deposit, contents.Hot = "", ""
	sealedShareA, err := m.sealMasterContext([]byte(shareAText), shareACipherContext)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	record.ShareA = sealedShareA
	record.UpdatedAt = now
	record.CreatedAt = now
	record.CreatedBy = AuditActorUserID(ctx)
	record.AutoShare = ""
	record.AutoEnabled = false
	record.AutoUpdatedAt = now
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	if err := writeSetting(ctx, tx, settingShards, string(encoded)); err != nil {
		return nil, err
	}
	if err := writeAudit(ctx, tx, auditEntry{op: "signer_shards_resplit", result: "ok", reason: "old_auto_share_invalidated"}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, ErrVaultUnavailable
	}
	return m.shardStatusLocked(ctx)
}

// EnableAutoSweep stores the operator share sealed with the installation key.
// This is the explicit risk the operator acknowledges: the server can now
// reconstruct the data key without a human present.
func (m *Manager) EnableAutoSweep(ctx context.Context, share string) (*ShardStatus, error) {
	if err := mode.CheckWritable(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(share) == "" || len(share) > maxShareInputBytes {
		return nil, ErrSignerShareInvalid
	}
	m.signerOpMu.Lock()
	defer m.signerOpMu.Unlock()
	dataKey, err := m.recoverDataKey(ctx, share)
	if err != nil {
		return nil, err
	}
	clear(dataKey)
	sealed, err := m.sealMasterContext([]byte(strings.TrimSpace(share)), autoShareCipherContext)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.beginVaultTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	existingRaw, err := readSetting(ctx, tx, settingShards)
	if err != nil || existingRaw == "" {
		return nil, ErrShardsNotConfigured
	}
	var record shardRecord
	if json.Unmarshal([]byte(existingRaw), &record) != nil || !recordConfigured(&record) {
		return nil, ErrShardsNotConfigured
	}
	now := time.Now().UTC().Format(time.RFC3339)
	record.AutoShare = sealed
	record.AutoEnabled = true
	record.AutoUpdatedAt = now
	record.UpdatedAt = now
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	if err := writeSetting(ctx, tx, settingShards, string(encoded)); err != nil {
		return nil, err
	}
	if err := writeAudit(ctx, tx, auditEntry{op: "signer_auto_sweep", result: "enabled", reason: "risk_acknowledged"}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, ErrVaultUnavailable
	}
	return m.shardStatusLocked(ctx)
}

// DisableAutoSweep removes the sealed automatic-sweep share. The operator
// must again supply share B for every sweep.
func (m *Manager) DisableAutoSweep(ctx context.Context) (*ShardStatus, error) {
	if err := mode.CheckWritable(); err != nil {
		return nil, err
	}
	m.signerOpMu.Lock()
	defer m.signerOpMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.beginVaultTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	existingRaw, err := readSetting(ctx, tx, settingShards)
	if err != nil || existingRaw == "" {
		return nil, ErrShardsNotConfigured
	}
	var record shardRecord
	if json.Unmarshal([]byte(existingRaw), &record) != nil || !recordConfigured(&record) {
		return nil, ErrShardsNotConfigured
	}
	now := time.Now().UTC().Format(time.RFC3339)
	record.AutoShare = ""
	record.AutoEnabled = false
	record.AutoUpdatedAt = now
	record.UpdatedAt = now
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	if err := writeSetting(ctx, tx, settingShards, string(encoded)); err != nil {
		return nil, err
	}
	if err := writeAudit(ctx, tx, auditEntry{op: "signer_auto_sweep", result: "disabled"}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, ErrVaultUnavailable
	}
	return m.shardStatusLocked(ctx)
}

// AutoSweepEnabled reports whether the sealed automatic-sweep share exists.
func (m *Manager) AutoSweepEnabled(ctx context.Context) (bool, error) {
	record, err := m.loadShardRecord(ctx)
	if err != nil {
		return false, err
	}
	return recordConfigured(record) && record.AutoEnabled && strings.TrimSpace(record.AutoShare) != "", nil
}

// persistHotAddresses stores the public gas-wallet addresses so status pages
// keep working while the vault is locked. Public addresses are not secrets.
func (m *Manager) persistHotAddresses(ctx context.Context, w settingWriter, hot *Deriver) error {
	if hot == nil {
		return nil
	}
	_, evm, err := hot.EVMKey(0)
	if err != nil {
		return ErrVaultUnavailable
	}
	solKey, sol, err := hot.SolanaKey(0)
	clear(solKey)
	if err != nil {
		return ErrVaultUnavailable
	}
	raw, err := json.Marshal(hotAddressRecord{EVM: evm.Hex(), Solana: sol.String()})
	if err != nil {
		return ErrVaultUnavailable
	}
	return writeSetting(ctx, w, settingHotAddresses, string(raw))
}

// cachedHotAddresses is the locked-mode fallback for HotAddresses.
func (m *Manager) cachedHotAddresses(ctx context.Context) (*HotAddress, bool) {
	raw, err := readSetting(ctx, m.db, settingHotAddresses)
	if err != nil || raw == "" {
		return nil, false
	}
	var record hotAddressRecord
	if json.Unmarshal([]byte(raw), &record) != nil || record.EVM == "" {
		return nil, false
	}
	return &HotAddress{Index: 0, EVM: record.EVM, Solana: record.Solana}, true
}

// AuditActorUserID returns the acting administrator, or 0 when unattributed.
func AuditActorUserID(ctx context.Context) int64 {
	actor := AuditActorFromContext(ctx)
	if actor.UserID == nil {
		return 0
	}
	return *actor.UserID
}
