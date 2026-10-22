package wallet

import (
	"database/sql/driver"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

type blockingVaultRead struct {
	entered chan struct{}
	release chan struct{}
}

func (b blockingVaultRead) Match(value driver.Value) bool {
	close(b.entered)
	<-b.release
	return value == settingVault
}

func TestDerive500ConcurrentWithHotImportDoesNotReplaceActiveSeed(t *testing.T) {
	m, mock, raw := residentManager(t)
	gate := blockingVaultRead{entered: make(chan struct{}), release: make(chan struct{})}
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT value FROM settings WHERE key = $1 FOR SHARE`)).WithArgs(gate).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(raw))
	mock.ExpectExec(regexp.QuoteMeta(auditSQL)).WithArgs("derive", "", 0, 0, "", "", "500", "ok", "").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	expectImportStart(mock, raw, false)
	expectAddresses(mock, nil)
	mock.ExpectExec("UPDATE settings SET value").WithArgs(settingVault, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
	auditOK(mock, "hot_mnemonic_import", "success")
	mock.ExpectCommit()
	derived := make(chan error, 1)
	go func() {
		rows, err := m.Derive(t.Context(), 0, 500)
		if err == nil && (len(rows) != 500 || rows[0].EVM != "0x9858EfFD232B4033E47d90003D41EC34EcaEda94" || rows[499].Index != 499) {
			err = ErrDepositSeedMismatch
		}
		derived <- err
	}()
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("derive did not reach fingerprint check")
	}
	imported := make(chan error, 1)
	go func() { imported <- m.ImportHotMnemonic(t.Context(), testReplacement) }()
	close(gate.release)
	for _, done := range []chan error{derived, imported} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("derive/import deadlocked")
		}
	}
	if !m.ready() {
		t.Fatal("import must become ready immediately")
	}
}
