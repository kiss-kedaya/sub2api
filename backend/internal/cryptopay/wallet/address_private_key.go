package wallet

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gagliardetto/solana-go"
)

// AddressPrivateKeyExport contains one explicitly selected deposit private key.
// It is returned only after the export audit commits; it never contains seeds.
type AddressPrivateKeyExport struct {
	AddressID       int64  `json:"address_id"`
	Address         string `json:"address"`
	Family          string `json:"family"`
	DerivationIndex uint32 `json:"derivation_index"`
	PrivateKey      string `json:"private_key"`
	Format          string `json:"format"`
}

var (
	// ErrExportAddressNotFound means the requested deposit assignment is absent.
	ErrExportAddressNotFound = errors.New("deposit address not found")
	// ErrExportAddressUnsupportedFamily rejects unknown stored address families.
	ErrExportAddressUnsupportedFamily = errors.New("deposit address family is not supported")
	// ErrExportAddressMismatch prevents exporting an unverified assignment key.
	ErrExportAddressMismatch = errors.New("deposit address does not match managed wallet")
)

const addressPrivateKeyExportOp = "private_key_export"

const exportAssignmentSQL = `SELECT id, user_id, chain, address, derivation_index, status
	FROM deposit_addresses WHERE id = $1 FOR SHARE`

const exportPoolSQL = `SELECT address, assigned_user_id, status FROM crypto_address_pool
	WHERE chain_family = $1 AND derivation_index = $2 FOR SHARE`

// ExportAddressPrivateKey verifies the selected database assignment against the
// current encrypted deposit seed. Operator 2FA and session-bound grants belong
// to the HTTP boundary. Disabled assignments remain exportable for recovery.
func (m *Manager) ExportAddressPrivateKey(ctx context.Context, addressID int64) (*AddressPrivateKeyExport, error) {
	if err := mode.CheckWritable(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	value, err := m.exportAddressPrivateKey(ctx, addressID)
	if err != nil {
		if errors.Is(err, ErrAuditUnavailable) {
			return nil, err
		}
		// The transaction has already rolled back before this independent audit,
		// including when the connection pool has only one connection.
		return nil, m.denied(addressPrivateKeyExportOp, err)
	}
	return value, nil
}

func (m *Manager) exportAddressPrivateKey(ctx context.Context, addressID int64) (*AddressPrivateKeyExport, error) {
	if addressID <= 0 {
		return nil, ErrExportAddressNotFound
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.db == nil {
		return nil, ErrVaultUnavailable
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	defer func() { _ = tx.Rollback() }()

	// Use the same shared vault-row lock as currentVaultTx, without warming
	// either resident seed cache or deriving anything from the independent Gas
	// seed. A concurrent import cannot replace ciphertext before audit commit.
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = $1 FOR SHARE`, settingVault).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) || err == nil && raw == "" {
		return nil, ErrWalletNotConfigured
	}
	if err != nil || len(raw) > 32<<10 {
		return nil, ErrVaultUnavailable
	}
	contents, err := m.openContents(raw)
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	deposit, err := NewDeriver(contents.Deposit)
	contents.Deposit, contents.Hot = "", ""
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	defer clear(deposit.seed)

	var row addresses.Assignment
	err = tx.QueryRowContext(ctx, exportAssignmentSQL, addressID).
		Scan(&row.ID, &row.UserID, &row.Family, &row.Address, &row.DerivationIndex, &row.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrExportAddressNotFound
	}
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	family := row.Family
	if family != addresses.FamilyEVM && family != addresses.FamilySolana {
		family, err = addresses.FamilyForChain(family)
		if err != nil {
			return nil, ErrExportAddressUnsupportedFamily
		}
	}
	if row.ID != addressID || row.UserID <= 0 || row.DerivationIndex < 0 || row.DerivationIndex >= 1<<31 {
		return nil, ErrExportAddressMismatch
	}
	var poolAddress, poolStatus string
	var poolUserID sql.NullInt64
	err = tx.QueryRowContext(ctx, exportPoolSQL, family, row.DerivationIndex).
		Scan(&poolAddress, &poolUserID, &poolStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrExportAddressMismatch
	}
	if err != nil {
		return nil, ErrVaultUnavailable
	}
	if !poolUserID.Valid || poolUserID.Int64 != row.UserID || poolStatus != "assigned" && poolStatus != "retired" ||
		!exportAddressesEqual(family, row.Address, poolAddress) {
		return nil, ErrExportAddressMismatch
	}

	index := uint32(row.DerivationIndex)
	key, format, err := deriveExportPrivateKey(deposit, family, index, row.Address)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	if err := writeAudit(ctx, tx, auditEntry{op: addressPrivateKeyExportOp, kind: family,
		index: index, to: row.Address, result: "success"}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, ErrAuditUnavailable
	}

	// Encode plaintext only after durable authorization evidence exists. Failed
	// audits/commits never create a response string containing the private key.
	var encoded string
	if family == addresses.FamilySolana {
		encoded = solana.PrivateKey(key).String()
	} else {
		encoded = hex.EncodeToString(key)
	}
	return &AddressPrivateKeyExport{AddressID: row.ID, Address: row.Address,
		Family: family, DerivationIndex: index, PrivateKey: encoded, Format: format}, nil
}

func exportAddressesEqual(family, left, right string) bool {
	if family == addresses.FamilyEVM {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func deriveExportPrivateKey(deposit *Deriver, family string, index uint32, address string) ([]byte, string, error) {
	switch family {
	case addresses.FamilyEVM:
		key, derived, err := deposit.EVMKey(index)
		if err != nil {
			return nil, "", ErrExportAddressMismatch
		}
		defer func() {
			clear(key.D.Bits())
			key.D.SetInt64(0)
		}()
		if !exportAddressesEqual(family, address, derived.Hex()) {
			return nil, "", ErrExportAddressMismatch
		}
		return crypto.FromECDSA(key), "hex", nil
	case addresses.FamilySolana:
		key, derived, err := deposit.SolanaKey(index)
		if err != nil || address != derived.String() {
			clear(key)
			return nil, "", ErrExportAddressMismatch
		}
		return key, "base58", nil
	default:
		return nil, "", ErrExportAddressUnsupportedFamily
	}
}
