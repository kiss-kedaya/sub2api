package wallet

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gagliardetto/solana-go"
)

const exportTestAddressID int64 = 37
const exportTestUserID int64 = 83

func exportTestAddress(t *testing.T, mnemonic, family string, index uint32) string {
	t.Helper()
	deposit, err := NewDeriver(mnemonic)
	if err != nil {
		t.Fatal("create fixture deriver")
	}
	defer clear(deposit.seed)
	if family == addresses.FamilyEVM {
		key, address, err := deposit.EVMKey(index)
		if err != nil {
			t.Fatal("derive fixture EVM address")
		}
		clear(key.D.Bits())
		key.D.SetInt64(0)
		return address.Hex()
	}
	key, address, err := deposit.SolanaKey(index)
	clear(key)
	if err != nil {
		t.Fatal("derive fixture Solana address")
	}
	return address.String()
}

func expectAddressExportStart(mock sqlmock.Sqlmock, raw string) {
	mock.ExpectBegin()
	expectRead(mock, raw, " FOR SHARE")
}

func expectExportAssignment(mock sqlmock.Sqlmock, family, address, status string, index int64) {
	mock.ExpectQuery(regexp.QuoteMeta(exportAssignmentSQL)).WithArgs(exportTestAddressID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "chain", "address", "derivation_index", "status"}).
			AddRow(exportTestAddressID, exportTestUserID, family, address, index, status))
}

func expectExportPool(mock sqlmock.Sqlmock, family, address, status string, index int64) {
	mock.ExpectQuery(regexp.QuoteMeta(exportPoolSQL)).WithArgs(family, index).
		WillReturnRows(sqlmock.NewRows([]string{"address", "assigned_user_id", "status"}).
			AddRow(address, exportTestUserID, status))
}

func expectPrivateKeyExportAudit(mock sqlmock.Sqlmock, family, address string, index uint32) *sqlmock.ExpectedExec {
	return mock.ExpectExec(regexp.QuoteMeta(auditSQL)).WithArgs(addressPrivateKeyExportOp,
		family, index, int64(0), address, "", "", "success", "")
}

func assertExportedPrivateKey(t *testing.T, value *AddressPrivateKeyExport, family, address string) {
	t.Helper()
	if value == nil || value.AddressID != exportTestAddressID || value.Address != address || value.Family != family || value.PrivateKey == "" {
		t.Fatal("export did not identify the selected deposit address")
	}
	if family == addresses.FamilyEVM {
		if value.Format != "hex" || len(value.PrivateKey) != 64 {
			t.Fatal("EVM private key is not 32-byte hex")
		}
		key, err := crypto.HexToECDSA(value.PrivateKey)
		if err != nil {
			t.Fatal("exported EVM private key cannot be imported")
		}
		defer func() {
			clear(key.D.Bits())
			key.D.SetInt64(0)
		}()
		if !strings.EqualFold(crypto.PubkeyToAddress(key.PublicKey).Hex(), address) {
			t.Fatal("exported EVM private key belongs to another address")
		}
		return
	}
	if value.Format != "base58" {
		t.Fatal("Solana private key is not base58")
	}
	key, err := solana.PrivateKeyFromBase58(value.PrivateKey)
	if err != nil {
		t.Fatal("exported Solana private key cannot be imported")
	}
	defer clear(key)
	if len(key) != 64 || key.PublicKey().String() != address {
		t.Fatal("exported Solana private key belongs to another address")
	}
}

func TestAddressPrivateKeyExportVerifiesSelectedAssignment(t *testing.T) {
	for _, test := range []struct {
		name, storedFamily, family, status, poolStatus string
		index                                          uint32
		lowercase                                      bool
	}{
		{name: "EVM index zero", storedFamily: "evm", family: "evm", status: "active", poolStatus: "assigned"},
		{name: "disabled EVM with case differences", storedFamily: "evm", family: "evm", status: "disabled", poolStatus: "assigned", index: 3, lowercase: true},
		{name: "legacy EVM chain", storedFamily: "bsc", family: "evm", status: "active", poolStatus: "assigned", index: 6},
		{name: "disabled Solana with retired pool", storedFamily: "solana", family: "solana", status: "disabled", poolStatus: "retired", index: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, mock := mockManager(t)
			raw := managedVault(t)
			poolAddress := exportTestAddress(t, testDeposit, test.family, test.index)
			address := poolAddress
			if test.lowercase {
				address = strings.ToLower(address)
			}
			expectAddressExportStart(mock, raw)
			expectExportAssignment(mock, test.storedFamily, address, test.status, int64(test.index))
			expectExportPool(mock, test.family, poolAddress, test.poolStatus, int64(test.index))
			expectPrivateKeyExportAudit(mock, test.family, address, test.index).WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectCommit()
			value, err := m.ExportAddressPrivateKey(t.Context(), exportTestAddressID)
			if err != nil {
				t.Fatal("selected address export failed")
			}
			assertExportedPrivateKey(t, value, test.family, address)
			if value.DerivationIndex != test.index || m.deposit != nil || m.hot != nil {
				t.Fatal("export used the wrong child or populated resident seed caches")
			}
		})
	}
}

func TestAddressPrivateKeyExportReloadsCiphertextInsteadOfCachedSeed(t *testing.T) {
	m, mock, _ := residentManager(t)
	raw, err := m.sealContents(testReplacement, testHot)
	if err != nil {
		t.Fatal("seal replacement fixture")
	}
	address := exportTestAddress(t, testReplacement, addresses.FamilyEVM, 4)
	expectAddressExportStart(mock, raw)
	expectExportAssignment(mock, "evm", address, "active", 4)
	expectExportPool(mock, "evm", address, "assigned", 4)
	expectPrivateKeyExportAudit(mock, "evm", address, 4).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	value, err := m.ExportAddressPrivateKey(t.Context(), exportTestAddressID)
	if err != nil {
		t.Fatal("fresh ciphertext was not used")
	}
	assertExportedPrivateKey(t, value, addresses.FamilyEVM, address)
}

func TestAddressPrivateKeyExportRejectsInvalidAssignment(t *testing.T) {
	for _, test := range []struct {
		name, family string
		index        int64
		missing      bool
		want         error
	}{
		{name: "missing assignment", family: "evm", missing: true, want: ErrExportAddressNotFound},
		{name: "negative derivation index", family: "evm", index: -1, want: ErrExportAddressMismatch},
		{name: "hardened derivation index", family: "evm", index: 1 << 31, want: ErrExportAddressMismatch},
		{name: "uint32 overflow", family: "solana", index: 1 << 32, want: ErrExportAddressMismatch},
		{name: "unsupported family", family: "bitcoin", want: ErrExportAddressUnsupportedFamily},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, mock := mockManager(t)
			expectAddressExportStart(mock, managedVault(t))
			if test.missing {
				mock.ExpectQuery(regexp.QuoteMeta(exportAssignmentSQL)).WithArgs(exportTestAddressID).WillReturnError(sql.ErrNoRows)
			} else {
				expectExportAssignment(mock, test.family, testDestination, "disabled", test.index)
			}
			mock.ExpectRollback()
			auditOK(mock, addressPrivateKeyExportOp, "denied")
			value, err := m.ExportAddressPrivateKey(t.Context(), exportTestAddressID)
			if value != nil || !errors.Is(err, test.want) {
				t.Fatal("invalid assignment released a private key or returned an unsafe error")
			}
		})
	}
}

func TestAddressPrivateKeyExportRejectsPoolAndDerivationMismatch(t *testing.T) {
	for _, name := range []string{"pool missing", "unassigned pool", "different pool owner", "free pool", "different pool address", "EVM derivation mismatch", "Solana case mismatch"} {
		t.Run(name, func(t *testing.T) {
			m, mock := mockManager(t)
			family := addresses.FamilyEVM
			if name == "Solana case mismatch" {
				family = addresses.FamilySolana
			}
			address := exportTestAddress(t, testDeposit, family, 7)
			poolAddress, poolStatus := address, "assigned"
			var poolUserID any = exportTestUserID
			switch name {
			case "unassigned pool":
				poolUserID = nil
			case "different pool owner":
				poolUserID = exportTestUserID + 1
			case "free pool":
				poolStatus = "free"
			case "different pool address":
				poolAddress = testDestination
			case "EVM derivation mismatch":
				address, poolAddress = testDestination, testDestination
			case "Solana case mismatch":
				address = strings.ToUpper(address)
				poolAddress = address
			}
			expectAddressExportStart(mock, managedVault(t))
			expectExportAssignment(mock, family, address, "disabled", 7)
			query := mock.ExpectQuery(regexp.QuoteMeta(exportPoolSQL)).WithArgs(family, int64(7))
			if name == "pool missing" {
				query.WillReturnError(sql.ErrNoRows)
			} else {
				query.WillReturnRows(sqlmock.NewRows([]string{"address", "assigned_user_id", "status"}).AddRow(poolAddress, poolUserID, poolStatus))
			}
			mock.ExpectRollback()
			auditOK(mock, addressPrivateKeyExportOp, "denied")
			value, err := m.ExportAddressPrivateKey(t.Context(), exportTestAddressID)
			if value != nil || !errors.Is(err, ErrExportAddressMismatch) {
				t.Fatal("unverified pool/assignment released a private key")
			}
		})
	}
}

func TestAddressPrivateKeyExportFailsClosedOnVaultAndDatabaseErrors(t *testing.T) {
	for _, name := range []string{"missing vault", "empty vault", "corrupt vault", "vault read", "assignment read", "pool read"} {
		t.Run(name, func(t *testing.T) {
			m, mock, raw := residentManager(t)
			want := ErrVaultUnavailable
			mock.ExpectBegin()
			switch name {
			case "missing vault":
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT value FROM settings WHERE key = $1 FOR SHARE`)).WithArgs(settingVault).WillReturnError(sql.ErrNoRows)
				want = ErrWalletNotConfigured
			case "empty vault":
				expectRead(mock, "", " FOR SHARE")
				want = ErrWalletNotConfigured
			case "corrupt vault":
				expectRead(mock, "corrupted", " FOR SHARE")
			case "vault read":
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT value FROM settings WHERE key = $1 FOR SHARE`)).WithArgs(settingVault).WillReturnError(errors.New(testDeposit))
			case "assignment read":
				expectRead(mock, raw, " FOR SHARE")
				mock.ExpectQuery(regexp.QuoteMeta(exportAssignmentSQL)).WithArgs(exportTestAddressID).WillReturnError(errors.New(testHot))
			case "pool read":
				expectRead(mock, raw, " FOR SHARE")
				expectExportAssignment(mock, "evm", testDestination, "active", 1)
				mock.ExpectQuery(regexp.QuoteMeta(exportPoolSQL)).WithArgs("evm", int64(1)).WillReturnError(errors.New(testDeposit))
			}
			mock.ExpectRollback()
			auditOK(mock, addressPrivateKeyExportOp, "denied")
			value, err := m.ExportAddressPrivateKey(t.Context(), exportTestAddressID)
			if value != nil || !errors.Is(err, want) || strings.Contains(err.Error(), testDeposit) || strings.Contains(err.Error(), testHot) {
				t.Fatal("vault/database failure released key material or driver details")
			}
		})
	}
}

func TestAddressPrivateKeyExportRequiresCommittedAudit(t *testing.T) {
	for _, commitFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "audit failure", true: "commit failure"}[commitFailure], func(t *testing.T) {
			m, mock := mockManager(t)
			address := exportTestAddress(t, testDeposit, addresses.FamilyEVM, 8)
			expectAddressExportStart(mock, managedVault(t))
			expectExportAssignment(mock, "evm", address, "active", 8)
			expectExportPool(mock, "evm", address, "assigned", 8)
			audit := expectPrivateKeyExportAudit(mock, "evm", address, 8)
			if commitFailure {
				audit.WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit().WillReturnError(errors.New(testDeposit))
			} else {
				audit.WillReturnError(errors.New(testHot))
				mock.ExpectRollback()
			}
			value, err := m.ExportAddressPrivateKey(t.Context(), exportTestAddressID)
			if value != nil || !errors.Is(err, ErrAuditUnavailable) || strings.Contains(err.Error(), testDeposit) || strings.Contains(err.Error(), testHot) {
				t.Fatal("uncommitted export released private key material")
			}
		})
	}
}

func TestAddressPrivateKeyExportRejectsInvalidIDAndCancellation(t *testing.T) {
	for _, id := range []int64{0, -1} {
		m, mock := mockManager(t)
		auditOK(mock, addressPrivateKeyExportOp, "denied")
		if value, err := m.ExportAddressPrivateKey(t.Context(), id); value != nil || !errors.Is(err, ErrExportAddressNotFound) {
			t.Fatal("invalid address ID was accepted")
		}
	}
	m, mock := mockManager(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	auditOK(mock, addressPrivateKeyExportOp, "denied")
	if value, err := m.ExportAddressPrivateKey(ctx, exportTestAddressID); value != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled export was accepted")
	}
}

func TestAddressPrivateKeyExportReadOnlyDoesNotTouchDatabase(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "false")
	m, _ := mockManager(t)
	if value, err := m.ExportAddressPrivateKey(t.Context(), exportTestAddressID); value != nil || !errors.Is(err, mode.ErrReadOnly) {
		t.Fatal("read-only export touched the vault or audit")
	}
}
