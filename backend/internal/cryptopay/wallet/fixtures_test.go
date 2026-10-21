package wallet

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/base64"
	"regexp"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"golang.org/x/crypto/scrypt"
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
			WHEN chain IN ('bsc','polygon','plasma','arbitrum','base','xlayer') THEN 'evm'
			WHEN chain = 'solana' THEN 'solana'
			ELSE chain END AS chain_family, derivation_index, address FROM deposit_addresses`
)

var legacyOnce sync.Once
var legacyRaw string
var legacyErr error

// Produce an independent ciphertext using the legacy byte layout and literal
// JSON, without calling the new sealContents. The deterministic salt/nonce are
// exclusively a compatibility fixture and are never used by implementation.
func legacyVault(t *testing.T) string {
	t.Helper()
	legacyOnce.Do(func() {
		salt := []byte("legacy-test-salt")
		nonce := []byte("legacy-nonce")
		key, err := scrypt.Key([]byte(testPassphrase), salt, 1<<17, 8, 1, 32)
		if err != nil {
			legacyErr = err
			return
		}
		defer clear(key)
		block, err := aes.NewCipher(key)
		if err != nil {
			legacyErr = err
			return
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			legacyErr = err
			return
		}
		plaintext := []byte(`{"deposit":"` + testDeposit + `","hot":"` + testHot + `"}`)
		defer clear(plaintext)
		blob := append(append([]byte{}, salt...), nonce...)
		blob = gcm.Seal(blob, nonce, plaintext, nil)
		legacyRaw = base64.StdEncoding.EncodeToString(blob)
	})
	if legacyErr != nil {
		t.Fatal(legacyErr)
	}
	return legacyRaw
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
	m := New(db)
	if err := m.SetDestination(testDestination); err != nil {
		t.Fatal(err)
	}
	return m, mock
}

func residentManager(t *testing.T) (*Manager, sqlmock.Sqlmock, string) {
	t.Helper()
	m, mock := mockManager(t)
	raw := legacyVault(t)
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
	t.Cleanup(m.Lock)
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

func expectUnlock(mock sqlmock.Sqlmock, raw string) {
	expectLock(mock, true)
	expectRead(mock, raw, " FOR UPDATE")
	expectAddresses(mock, nil)
	auditOK(mock, "unlock", "ok")
	mock.ExpectCommit()
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
