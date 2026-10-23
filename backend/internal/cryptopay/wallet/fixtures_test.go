package wallet

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// Public BIP39 test vectors only. No environment, RPC, wallet files or live DB.
const (
	testDeposit     = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	testHot         = "legal winner thank year wave sausage worth useful legal winner thank yellow"
	testReplacement = "letter advice cage absurd amount doctor acoustic avoid letter advice cage above"
	testPassphrase  = "offline fixture passphrase"
	testDestination = "0x1111111111111111111111111111111111111111"
	testToken       = "0x2222222222222222222222222222222222222222"
	addressQuery    = `SELECT chain_family, derivation_index, address FROM crypto_address_pool
		UNION SELECT CASE
			WHEN chain IN ('ethereum','bsc','polygon','plasma','arbitrum','base','xlayer') THEN 'evm'
			WHEN chain = 'solana' THEN 'solana'
			ELSE chain END AS chain_family, derivation_index, address FROM deposit_addresses`
)

const testMasterKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func managedVault(t *testing.T) string {
	t.Helper()
	m := New(nil, testMasterKey)
	raw, err := m.sealContents(testDeposit, testHot)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mockManager(t *testing.T) (*Manager, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	// A denial must release any transaction before asking the pool for an
	// audit connection. This makes that deadlock observable in every test.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		_ = db.Close()
	})
	m := New(db, testMasterKey)
	if err := m.SetDestination(testDestination); err != nil {
		t.Fatal(err)
	}
	return m, mock
}

func residentManager(t *testing.T) (*Manager, sqlmock.Sqlmock, string) {
	t.Helper()
	m, mock := mockManager(t)
	raw := managedVault(t)
	var err error
	m.deposit, err = NewDeriver(testDeposit)
	if err != nil {
		t.Fatal(err)
	}
	m.hot, err = NewDeriver(testHot)
	if err != nil {
		t.Fatal(err)
	}
	m.vaultFingerprint = sha256.Sum256([]byte(raw))
	t.Cleanup(m.Close)
	return m, mock, raw
}

func expectAudit(mock sqlmock.Sqlmock, op, result string) *sqlmock.ExpectedExec {
	return mock.ExpectExec(regexp.QuoteMeta(auditSQL)).WithArgs(op, "", 0, 0, "", "", "", result, "")
}

func auditOK(mock sqlmock.Sqlmock, op, result string) {
	expectAudit(mock, op, result).WillReturnResult(sqlmock.NewResult(1, 1))
}

func expectRead(mock sqlmock.Sqlmock, raw, suffix string) {
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT value FROM settings WHERE key = $1` + suffix)).
		WithArgs(settingVault).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(raw))
}

func expectLock(mock sqlmock.Sqlmock, locked bool) {
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT pg_try_advisory_xact_lock($1)`)).WithArgs(vaultSweepLockKey).
		WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(locked))
}

func expectAddresses(mock sqlmock.Sqlmock, rows *sqlmock.Rows) {
	if rows == nil {
		rows = sqlmock.NewRows([]string{"family", "index", "address"})
	}
	mock.ExpectQuery(regexp.QuoteMeta(addressQuery)).WillReturnRows(rows).RowsWillBeClosed()
}

// compile-time contract for the public address service, without importing sweeper.
var _ interface {
	Status(context.Context) (*Status, error)
} = (*Manager)(nil)
var _ sqlmock.Argument = sealedArg{}

type sealedArg struct{ capture *string }

func (a sealedArg) Match(value driver.Value) bool {
	raw, ok := value.(string)
	if !ok || raw == "" {
		return false
	}
	if a.capture != nil {
		*a.capture = raw
	}
	return true
}
