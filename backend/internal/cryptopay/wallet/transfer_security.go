package wallet

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Only this exact signed transaction is authorized. Neither the configured
// destination nor sweep_auto grants authority to an arbitrary token outflow.
type transferAuthorization struct {
	ChainID int64  `json:"chain_id"`
	From    string `json:"from"`
	To      string `json:"to"`
	Token   string `json:"token"`
	Amount  string `json:"amount"`
}

func (m *Manager) signatureAuthorizationReason(chainID int64, txHash string) string {
	mac := hmac.New(sha256.New, m.securityKey)
	_, _ = mac.Write([]byte("sub2api/crypto-wallet/outgoing-authorization/v1:" + big.NewInt(chainID).String() + ":" + strings.ToLower(txHash)))
	return "authorized_evm:" + strings.ToLower(txHash) + ":" + hex.EncodeToString(mac.Sum(nil))
}

func (a transferAuthorization) matches(chainID int64, from, to, token, amount string) bool {
	return a.ChainID == chainID && strings.EqualFold(a.From, from) && strings.EqualFold(a.To, to) && strings.EqualFold(a.Token, token) && a.Amount == amount
}

// AuthorizedTokenTransfer verifies an exact transaction hash MAC written in
// the signature/audit transaction and all observed ERC20 fields. The audit
// cannot be manufactured through a database-only change without the MAC key.
func (m *Manager) AuthorizedTokenTransfer(ctx context.Context, chain string, chainID int64, txHash, from, to, token, amount string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.securityEnabled {
		return false, ErrSecurityLocked
	}
	tx, err := m.currentVaultTx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	state, found, err := m.loadSecurity(ctx, tx, "FOR SHARE")
	if err != nil {
		return false, err
	}
	if !found || state.Locked || m.securityIncident != nil {
		return false, ErrSecurityLocked
	}
	key := chain + ":" + strings.ToLower(txHash)
	if state.Acknowledged[key] {
		return true, nil
	}
	if legacy, exists := state.Legacy[key]; exists && legacy.matches(chainID, from, to, token, amount) {
		return true, nil
	}
	var index int64
	var kind string
	err = tx.QueryRowContext(ctx, `SELECT derivation_index,kind FROM crypto_signer_audit
		WHERE op='sign_evm' AND result='ok' AND chain_id=$1 AND reason=$2
		AND lower(to_address)=lower($3) AND lower(token)=lower($4) AND amount=$5
		ORDER BY occurred_at DESC LIMIT 1`, chainID, m.signatureAuthorizationReason(chainID, txHash), to, token, amount).Scan(&index, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, ErrAuditUnavailable
	}
	if index < 0 || index >= 1<<31 || (kind != "" && kind != "sweep") {
		return false, nil
	}
	_, address, err := m.deposit.EVMKey(uint32(index))
	return err == nil && strings.EqualFold(address.Hex(), from), err
}

// SecurityWatchEVMAddresses includes allocated, disabled and unassigned child
// addresses plus both seed index-zero addresses. It uses the authenticated
// snapshot, so changing the DB watch list cannot hide an outflow.
func (m *Manager) SecurityWatchEVMAddresses(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx, err := m.currentVaultTx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	state, found, err := m.loadSecurity(ctx, tx, "FOR SHARE")
	if err != nil {
		return nil, err
	}
	if !found || state.Locked || m.securityIncident != nil {
		return nil, ErrSecurityLocked
	}
	seen := map[string]bool{}
	for _, set := range []map[string]addressSnapshot{state.Assignments, state.Pool} {
		for _, row := range set {
			if row.Family == addresses.FamilyEVM {
				seen[strings.ToLower(row.Address)] = true
			}
		}
	}
	for _, deriver := range []*Deriver{m.deposit, m.hot} {
		_, address, err := deriver.EVMKey(0)
		if err != nil {
			return nil, ErrVaultUnavailable
		}
		seen[strings.ToLower(address.Hex())] = true
	}
	out := make([]string, 0, len(seen))
	for address := range seen {
		out = append(out, address)
	}
	return out, nil
}

func securityChainID(chain string) int64 {
	switch chain {
	case "ethereum":
		return 1
	case "bsc":
		return 56
	case "polygon":
		return 137
	case "plasma":
		return 9745
	case "arbitrum":
		return 42161
	case "base":
		return 8453
	case "xlayer":
		return 196
	default:
		return 0
	}
}

// Upgrade compatibility is captured only in the first authenticated baseline.
// A legacy sweep needs its complete signed raw transaction, exact recovered
// child sender and matching successful audit. Later DB inserts gain no trust.
func (m *Manager) captureLegacyAuthorizations(ctx context.Context, tx *sql.Tx) (map[string]transferAuthorization, error) {
	rows, err := tx.QueryContext(ctx, `SELECT chain,from_address,to_address,token,raw_amount,derivation_index,sweep_tx_hash,sweep_raw_tx
		FROM sweep_records WHERE job_version=1 AND sweep_raw_tx<>'' ORDER BY id LIMIT 10001`)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	type candidate struct {
		chain, from, to, token, amount, hash, raw string
		index                                     int64
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.chain, &c.from, &c.to, &c.token, &c.amount, &c.index, &c.hash, &c.raw); err != nil {
			_ = rows.Close()
			return nil, ErrVaultUnavailable
		}
		candidates = append(candidates, c)
	}
	rowsErr := rows.Err()
	_ = rows.Close()
	if rowsErr != nil || len(candidates) > 10000 {
		return nil, ErrVaultUnavailable
	}
	out := map[string]transferAuthorization{}
	for _, c := range candidates {
		if c.index < 0 || c.index >= 1<<31 || len(c.raw) > 8192 {
			continue
		}
		chainID := securityChainID(c.chain)
		raw, err := hex.DecodeString(strings.TrimPrefix(c.raw, "0x"))
		if err != nil {
			continue
		}
		var signed types.Transaction
		if signed.UnmarshalBinary(raw) != nil || signed.ChainId().Cmp(big.NewInt(chainID)) != 0 || signed.To() == nil || signed.Value().Sign() != 0 || !strings.EqualFold(signed.Hash().Hex(), c.hash) {
			continue
		}
		from, err := types.Sender(types.LatestSignerForChainID(big.NewInt(chainID)), &signed)
		if err != nil {
			continue
		}
		_, derived, err := m.deposit.EVMKey(uint32(c.index))
		if err != nil || from != derived || !strings.EqualFold(from.Hex(), c.from) || !strings.EqualFold(signed.To().Hex(), c.token) {
			continue
		}
		amount, ok := new(big.Int).SetString(c.amount, 10)
		if !ok || amount.Sign() <= 0 || amount.BitLen() > 256 || !common.IsHexAddress(c.to) {
			continue
		}
		data := append([]byte{}, erc20TransferSelector...)
		data = append(data, common.LeftPadBytes(common.HexToAddress(c.to).Bytes(), 32)...)
		data = append(data, common.LeftPadBytes(amount.Bytes(), 32)...)
		if !bytes.Equal(signed.Data(), data) {
			continue
		}
		var audited bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM crypto_signer_audit WHERE op='sign_evm' AND result='ok'
			AND kind IN ('','sweep') AND chain_id=$1 AND derivation_index=$2 AND lower(to_address)=lower($3)
			AND lower(token)=lower($4) AND amount=$5)`, chainID, c.index, c.to, c.token, c.amount).Scan(&audited); err != nil {
			return nil, ErrAuditUnavailable
		}
		if audited {
			out[c.chain+":"+strings.ToLower(c.hash)] = transferAuthorization{chainID, c.from, c.to, c.token, c.amount}
		}
	}
	return out, nil
}
