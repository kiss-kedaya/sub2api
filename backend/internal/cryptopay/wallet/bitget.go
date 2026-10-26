package wallet

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/bitget"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type BitgetSignedOrder struct {
	Messages      string
	Authorization string
}

type bitgetRecordReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type bitgetRecord struct {
	ID, Signed, MAC, Chain, From, To, Token, Gross, Status string
	IntentJSON                                             []byte
	Index                                                  int64
	Version, Decimals                                      int
	Intent                                                 bitget.Intent
}

func (m *Manager) loadBitgetRecord(ctx context.Context, q bitgetRecordReader, jobID int64, lock string) (bitgetRecord, error) {
	var r bitgetRecord
	err := q.QueryRowContext(ctx, `SELECT b.order_id,b.intent,b.signed_messages,b.authorization_mac,
		s.chain,s.from_address,s.to_address,s.token,s.raw_amount,s.derivation_index,
		s.job_version,s.token_decimals,s.status
		FROM crypto_bitget_sweep_jobs b JOIN sweep_records s ON s.id=b.job_id WHERE b.job_id=$1 `+lock, jobID).
		Scan(&r.ID, &r.IntentJSON, &r.Signed, &r.MAC, &r.Chain, &r.From, &r.To, &r.Token, &r.Gross, &r.Index, &r.Version, &r.Decimals, &r.Status)
	if err != nil {
		return r, err
	}
	if json.Unmarshal(r.IntentJSON, &r.Intent) != nil || r.Version != 2 || r.Index < 0 || r.Index >= 1<<31 ||
		r.Intent.Chain != r.Chain || !strings.EqualFold(r.Intent.From, r.From) || !strings.EqualFold(r.Intent.To, r.To) ||
		!strings.EqualFold(r.Intent.Token, r.Token) || r.Intent.Gross != r.Gross || int64(r.Intent.Index) != r.Index ||
		r.Intent.Decimals != r.Decimals || r.Signed == "" ||
		!hmac.Equal([]byte(r.MAC), []byte(m.bitgetAuthorization(jobID, r.ID, r.Intent, r.Signed))) {
		return r, bitget.ErrUnsafeOrder
	}
	decimals, ok := bitget.SupportedTokenDecimals(r.Intent.Chain, r.Intent.Token)
	if !ok || decimals != r.Intent.Decimals {
		return r, bitget.ErrUnsafeOrder
	}
	return r, nil
}

// VerifyBitgetRecord authenticates durable data before it is used for receipt
// interpretation or a network send. It requires no signing grant to observe.
func (m *Manager) VerifyBitgetRecord(ctx context.Context, jobID int64, id string, in bitget.Intent, signed string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return ErrAuditUnavailable
	}
	r, err := m.loadBitgetRecord(ctx, m.db, jobID, "")
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAuditUnavailable
		}
		return err
	}
	if err = verifyBitgetSignedMessages(r.Intent, r.Signed); err != nil {
		return err
	}
	if r.ID != id || r.Intent != in || (signed != "" && r.Signed != signed) {
		return bitget.ErrUnsafeOrder
	}
	return nil
}

func verifyBitgetSignedMessages(in bitget.Intent, signed string) error {
	var msgs []bitget.Message
	if len(signed) > 128<<10 || json.Unmarshal([]byte(signed), &msgs) != nil || len(msgs) < 1 || len(msgs) > 2 {
		return bitget.ErrUnsafeOrder
	}
	for _, msg := range msgs {
		if msg.Signature == "" || !bitget.Hash(msg.Hash) {
			return bitget.ErrUnsafeOrder
		}
		raw, err := hex.DecodeString(strings.TrimPrefix(msg.Signature, "0x"))
		if err != nil || len(raw) != 65 || raw[64] < 27 || raw[64] > 28 {
			return bitget.ErrUnsafeOrder
		}
		raw[64] -= 27
		pub, err := crypto.SigToPub(common.FromHex(msg.Hash), raw)
		if err != nil || !strings.EqualFold(crypto.PubkeyToAddress(*pub).Hex(), in.From) {
			return bitget.ErrUnsafeOrder
		}
	}
	return nil
}

func (m *Manager) GuardBitgetSubmit(ctx context.Context, jobID int64, id, signed string, send func() (string, error)) (string, error) {
	if err := mode.CheckWritable(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.enabled || !m.validSigningGrant(ctx) {
		return "", ErrSigningAuthorizationRequired
	}
	tx, err := m.currentVaultTx(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if err = m.checkSecurityInTx(ctx, tx); err != nil {
		return "", err
	}
	r, err := m.loadBitgetRecord(ctx, tx, jobID, "FOR SHARE")
	if err != nil {
		return "", ErrAuditUnavailable
	}
	in := r.Intent
	if err = verifyBitgetSignedMessages(in, signed); err != nil {
		return "", err
	}
	if id != r.ID || signed != r.Signed || !strings.EqualFold(in.To, m.destination) || (r.Status != "planned" && r.Status != "sweep_pending") {
		return "", bitget.ErrUnsafeOrder
	}
	if in.CreatedAt <= 0 || time.Now().Unix() < in.CreatedAt-30 || time.Now().Unix() > in.CreatedAt+600 {
		return "", bitget.ErrUnsafeOrder
	}
	if err = m.bitgetConfigGate(ctx, tx, in); err != nil {
		return "", err
	}
	return send()
}

func (m *Manager) bitgetConfigGate(ctx context.Context, tx *sql.Tx, in bitget.Intent) error {
	var raw string
	var cfg struct {
		Enabled     bool     `json:"enabled"`
		Provider    string   `json:"sweep_provider"`
		MaxFee      string   `json:"bitget_max_fee"`
		BPS         int      `json:"bitget_max_fee_bps"`
		Destination string   `json:"hot_wallet_evm"`
		Chains      []string `json:"chains"`
	}
	if tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='crypto.config' FOR SHARE`).Scan(&raw) != nil || json.Unmarshal([]byte(raw), &cfg) != nil || !cfg.Enabled || cfg.Provider != bitget.Provider || !strings.EqualFold(cfg.Destination, in.To) {
		return ErrSigningDisabled
	}
	cap, e := bitget.RawAmount(cfg.MaxFee, in.Decimals)
	fee, e2 := bitget.Uint(in.Fee)
	gross, e3 := bitget.Uint(in.Gross)
	if e != nil || e2 != nil || e3 != nil || fee.Cmp(cap) > 0 || cfg.BPS < 1 || cfg.BPS > 1000 || new(big.Int).Mul(fee, big.NewInt(10000)).Cmp(new(big.Int).Mul(gross, big.NewInt(int64(cfg.BPS)))) > 0 {
		return bitget.ErrUnsafeOrder
	}
	for _, chain := range cfg.Chains {
		if chain == in.Chain {
			return nil
		}
	}
	return ErrSigningDisabled
}

// CheckBitgetDelegate permits only delegations captured after an authenticated
// Bitget order reached a verified receipt. It never adopts unrelated code.
func (m *Manager) CheckBitgetDelegate(ctx context.Context, in bitget.Intent) error {
	if !bitget.ValidAddress(in.CurrentDelegate) || !bitget.Hash(in.CurrentCodeHash) {
		return bitget.ErrUnsafeOrder
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var expected string
	err := m.db.QueryRowContext(ctx, `SELECT reason FROM crypto_signer_audit WHERE op='bitget_delegate' AND result='ok' AND chain_id=$1 AND derivation_index=$2 AND lower(to_address)=lower($3) ORDER BY occurred_at DESC LIMIT 1`, in.ChainID, in.Index, in.CurrentDelegate).Scan(&expected)
	mac := m.bitgetDelegateMAC(in.ChainID, in.Index, in.CurrentDelegate, in.CurrentCodeHash)
	if err != nil || !hmac.Equal([]byte(expected), []byte(mac)) {
		return bitget.ErrUnsafeOrder
	}
	return nil
}
func (m *Manager) bitgetDelegateMAC(chainID int64, index uint32, address, codeHash string) string {
	data, _ := json.Marshal([]any{chainID, index, strings.ToLower(address), strings.ToLower(codeHash)})
	mac := hmac.New(sha256.New, m.securityKey)
	_, _ = mac.Write([]byte("sub2api/bitget-delegate/v1:"))
	_, _ = mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

func (m *Manager) RecordBitgetDelegate(ctx context.Context, jobID int64, hash, delegate, codeHash string, txRaw json.RawMessage) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	if !bitget.ValidAddress(delegate) || !bitget.Hash(hash) || !bitget.Hash(codeHash) {
		return bitget.ErrUnsafeOrder
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.currentVaultTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var raw []byte
	var mac, signed, id, bound string
	if tx.QueryRowContext(ctx, `SELECT order_id,intent,signed_messages,authorization_mac,bound_tx_hash FROM crypto_bitget_sweep_jobs WHERE job_id=$1 FOR SHARE`, jobID).Scan(&id, &raw, &signed, &mac, &bound) != nil {
		return ErrAuditUnavailable
	}
	var in bitget.Intent
	if json.Unmarshal(raw, &in) != nil || !strings.EqualFold(bound, hash) || signed == "" || !hmac.Equal([]byte(mac), []byte(m.bitgetAuthorization(jobID, id, in, signed))) || verifyBitgetSignedMessages(in, signed) != nil {
		return bitget.ErrUnsafeOrder
	}
	var mined struct {
		BlockHash string `json:"blockHash"`
	}
	if json.Unmarshal(txRaw, &mined) != nil || mined.BlockHash == "" {
		return bitget.ErrUnsafeOrder
	}
	want, e := bitget.MinedDelegate(in, signed, hash, mined.BlockHash, txRaw)
	if e != nil || !strings.EqualFold(want, delegate) || (in.CurrentDelegate != "" && (!strings.EqualFold(in.CurrentDelegate, delegate) || !strings.EqualFold(in.CurrentCodeHash, codeHash))) {
		return bitget.ErrUnsafeOrder
	}
	if err = writeAudit(ctx, tx, auditEntry{op: "bitget_delegate", index: in.Index, chainID: in.ChainID, to: delegate, result: "ok", reason: m.bitgetDelegateMAC(in.ChainID, in.Index, delegate, codeHash)}); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *Manager) bitgetAuthorization(jobID int64, orderID string, in bitget.Intent, signed string) string {
	data, _ := json.Marshal(struct {
		JobID    int64
		ID       string
		Intent   bitget.Intent
		Messages string
	}{jobID, orderID, in, signed})
	mac := hmac.New(sha256.New, m.securityKey)
	_, _ = mac.Write([]byte("sub2api/bitget-order/v2:"))
	_, _ = mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// SignBitgetOrder follows the vendor-attested transfer protocol, while keeping
// the existing fresh operator grant, destination gate, vault locks and audit.
// No private key, mnemonic or signing endpoint is exposed to Bitget or HTTP.
func (m *Manager) SignBitgetOrder(ctx context.Context, jobID int64, o *bitget.Order, in bitget.Intent) (*BitgetSignedOrder, error) {
	if err := mode.CheckWritable(); err != nil {
		return nil, err
	}
	decimals, supported := bitget.SupportedTokenDecimals(in.Chain, in.Token)
	if !supported || decimals != in.Decimals || in.CreatedAt <= 0 || time.Now().Unix() < in.CreatedAt-30 || time.Now().Unix() > in.CreatedAt+600 {
		return nil, bitget.ErrUnsafeOrder
	}
	if err := bitget.VerifyAttestation(o, in); err != nil {
		return nil, err
	}
	validated, err := bitget.ValidateSigning(o, in, time.Now())
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if !m.enabled || !m.validSigningGrant(ctx) || !strings.EqualFold(m.destination, in.To) || in.Index >= 1<<31 {
		return nil, ErrSigningAuthorizationRequired
	}
	tx, err := m.currentVaultTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = m.checkSecurityInTx(ctx, tx); err != nil {
		return nil, err
	}
	if !m.securityEnabled || !m.securityRegistered {
		return nil, ErrSigningDisabled
	}
	var cfg struct {
		Enabled     bool     `json:"enabled"`
		Provider    string   `json:"sweep_provider"`
		MaxFee      string   `json:"bitget_max_fee"`
		BPS         int      `json:"bitget_max_fee_bps"`
		Destination string   `json:"hot_wallet_evm"`
		Chains      []string `json:"chains"`
	}
	var raw string
	if tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='crypto.config' FOR SHARE`).Scan(&raw) != nil || json.Unmarshal([]byte(raw), &cfg) != nil || !cfg.Enabled || cfg.Provider != bitget.Provider || cfg.MaxFee != in.MaxFee || cfg.BPS != in.MaxFeeBPS || !strings.EqualFold(cfg.Destination, in.To) {
		return nil, ErrSigningDisabled
	}
	allowed := false
	for _, chain := range cfg.Chains {
		if chain == in.Chain {
			allowed = true
		}
	}
	if !allowed {
		return nil, ErrSigningDisabled
	}
	var chain, from, to, token, gross string
	var index uint32
	var version int
	if tx.QueryRowContext(ctx, `SELECT chain,from_address,to_address,token,raw_amount,derivation_index,job_version FROM sweep_records WHERE id=$1 AND status='planned' FOR SHARE`, jobID).Scan(&chain, &from, &to, &token, &gross, &index, &version) != nil || version != 2 || chain != in.Chain || !strings.EqualFold(from, in.From) || !strings.EqualFold(to, in.To) || !strings.EqualFold(token, in.Token) || gross != in.Gross || index != in.Index {
		return nil, bitget.ErrUnsafeOrder
	}
	key, address, err := m.deposit.EVMKey(in.Index)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	// geth uses a raw secp256k1 scalar, outside crypto/ecdsa's NIST key cache.
	defer clear(key.D.Bits()) //nolint:staticcheck // Best-effort erasure of geth's temporary scalar after its final use.
	if !strings.EqualFold(address.Hex(), in.From) {
		return nil, ErrDepositSeedMismatch
	}
	msgs := append([]bitget.Message(nil), o.Source.EVM.Messages...)
	for i, digest := range validated.Digests {
		sig, e := crypto.Sign(digest, key)
		if e != nil {
			return nil, ErrVaultUnavailable
		}
		sig[64] += 27
		msgs[i].Signature = "0x" + hex.EncodeToString(sig)
	}
	encoded, err := json.Marshal(msgs)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	signed := string(encoded)
	mac := m.bitgetAuthorization(jobID, o.ID, in, signed)
	if err = writeAudit(ctx, tx, auditEntry{op: "sign_bitget", kind: "sweep", index: in.Index, chainID: in.ChainID, to: in.To, token: in.Token, amount: in.Gross, result: "ok", reason: "authorized_bitget:" + o.ID + ":" + mac}); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, ErrAuditUnavailable
	}
	return &BitgetSignedOrder{Messages: signed, Authorization: mac}, nil
}

// BindBitgetTransaction authorizes only the final hash tied to a persisted,
// authenticated order and signature. Receipt fields are checked independently
// by the sweeper and by the outgoing-token monitor.
func (m *Manager) BindBitgetTransaction(ctx context.Context, jobID int64, hash, collector string, expected bitget.Intent) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	if !bitget.Hash(hash) {
		return bitget.ErrUnsafeOrder
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.currentVaultTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = m.checkSecurityInTx(ctx, tx); err != nil {
		return err
	}
	var id, signed, mac, bound string
	var rawIntent, rawOrder []byte
	err = tx.QueryRowContext(ctx, `SELECT order_id,intent,prepared,signed_messages,authorization_mac,bound_tx_hash FROM crypto_bitget_sweep_jobs WHERE job_id=$1 FOR UPDATE`, jobID).Scan(&id, &rawIntent, &rawOrder, &signed, &mac, &bound)
	if err != nil {
		return ErrAuditUnavailable
	}
	var in bitget.Intent
	var o bitget.Order
	if json.Unmarshal(rawIntent, &in) != nil || in != expected || json.Unmarshal(rawOrder, &o) != nil || o.ID != id || signed == "" || !hmac.Equal([]byte(mac), []byte(m.bitgetAuthorization(jobID, id, in, signed))) || bitget.VerifyAttestation(&o, in) != nil {
		return bitget.ErrUnsafeOrder
	}
	if bound != "" && !strings.EqualFold(bound, hash) {
		return bitget.ErrUnsafeOrder
	}
	if strings.EqualFold(bound, hash) {
		return tx.Commit()
	}
	if in.Fee != "0" && (!bitget.ValidAddress(collector) || strings.EqualFold(collector, in.From) || strings.EqualFold(collector, in.To)) {
		return bitget.ErrUnsafeOrder
	}
	for _, transfer := range []struct{ to, amount string }{{in.To, in.Net}, {collector, in.Fee}} {
		if transfer.amount == "0" {
			continue
		}
		if err = writeAudit(ctx, tx, auditEntry{op: "sign_evm", kind: "sweep", index: in.Index, chainID: in.ChainID, to: transfer.to, token: in.Token, amount: transfer.amount, result: "ok", reason: m.signatureAuthorizationReason(in.ChainID, hash)}); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE crypto_bitget_sweep_jobs SET bound_tx_hash=$2,updated_at=NOW() WHERE job_id=$1`, jobID, strings.ToLower(hash)); err != nil {
		return ErrAuditUnavailable
	}
	if err = tx.Commit(); err != nil {
		return ErrAuditUnavailable
	}
	return nil
}
