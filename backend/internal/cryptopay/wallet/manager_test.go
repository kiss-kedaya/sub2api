package wallet

import (
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestUnlockLegacyCiphertextAndAddressQueries(t *testing.T) {
	m, mock := mockManager(t)
	raw := legacyVault(t)
	depositDeriver, err := NewDeriver(testDeposit)
	if err != nil {
		t.Fatal(err)
	}
	_, depositEVM, err := depositDeriver.EVMKey(0)
	if err != nil {
		t.Fatal(err)
	}
	_, depositSolana, err := depositDeriver.SolanaKey(0)
	if err != nil {
		t.Fatal(err)
	}
	expectLock(mock, true)
	expectRead(mock, raw, " FOR UPDATE")
	expectAddresses(mock, sqlmock.NewRows([]string{"family", "index", "address"}).
		AddRow("evm", 0, depositEVM.Hex()).AddRow("solana", 0, depositSolana.String()))
	auditOK(mock, "unlock", "ok")
	mock.ExpectCommit()
	if err := m.Unlock(t.Context(), testPassphrase, "", ""); err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	expectRead(mock, raw, " FOR SHARE")
	mock.ExpectExec(regexp.QuoteMeta(auditSQL)).WithArgs("derive", "", 0, 0, "", "", "2", "ok", "").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	got, err := m.Derive(t.Context(), 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].EVM != "0x9858EfFD232B4033E47d90003D41EC34EcaEda94" || got[0].EVM == got[1].EVM || got[0].Solana == got[1].Solana {
		t.Fatal("legacy deposit derivation changed")
	}
	mock.ExpectBegin()
	expectRead(mock, raw, " FOR SHARE")
	auditOK(mock, "hot_address", "ok")
	mock.ExpectCommit()
	hot, err := m.HotAddresses(t.Context())
	if err != nil || hot.Index != 0 || hot.EVM == got[0].EVM || hot.Solana == got[0].Solana {
		t.Fatal("hot wallet is not independent index zero", err)
	}
	status, err := m.Status(t.Context())
	if err != nil || !status.OK || !status.Unlocked || !status.SigningEnabled {
		t.Fatal("wrong unlocked status", err)
	}
	m.SetEnabled(false)
	status, err = m.Status(t.Context())
	if err != nil || status.SigningEnabled {
		t.Fatal("signing switch ignored", err)
	}
}

func TestNewVaultValidatesSeedsAndExistingAddresses(t *testing.T) {
	tests := []struct {
		name, deposit, hot, password string
		mismatch                     bool
	}{
		{"same seed", testDeposit, testDeposit, testPassphrase, false},
		{"invalid deposit", "bad", testHot, testPassphrase, false},
		{"invalid hot", testDeposit, "bad", testPassphrase, false},
		{"short password", testDeposit, testHot, "short", false},
		{"old deposits disagree", testDeposit, testHot, testPassphrase, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, mock := mockManager(t)
			expectLock(mock, true)
			expectRead(mock, "", " FOR UPDATE")
			if tt.mismatch {
				expectAddresses(mock, sqlmock.NewRows([]string{"family", "index", "address"}).AddRow("evm", 0, testDestination))
			}
			mock.ExpectRollback()
			auditOK(mock, "unlock", "denied")
			err := m.Unlock(t.Context(), tt.password, tt.deposit, tt.hot)
			if err == nil || m.unlocked() {
				t.Fatal("bad initialization was accepted")
			}
			if tt.mismatch && !errors.Is(err, ErrDepositSeedMismatch) {
				t.Fatal(err)
			}
		})
	}
}

func TestCreateNewVaultCommitsBeforeUnlock(t *testing.T) {
	m, mock := mockManager(t)
	expectLock(mock, true)
	expectRead(mock, "", " FOR UPDATE")
	expectAddresses(mock, nil)
	var persisted string
	mock.ExpectExec("INSERT INTO settings").WithArgs(settingVault, sealedArg{capture: &persisted}).WillReturnResult(sqlmock.NewResult(1, 1))
	auditOK(mock, "vault_init", "ok")
	auditOK(mock, "unlock", "ok")
	mock.ExpectCommit()
	if err := m.Unlock(t.Context(), testPassphrase, testDeposit, testHot); err != nil {
		t.Fatal(err)
	}
	if !m.unlocked() || persisted == "" || strings.Contains(persisted, testDeposit) {
		t.Fatal("vault not sealed and installed")
	}
	contents, err := openVaultString(persisted, testPassphrase)
	if err != nil || contents.Deposit != testDeposit || contents.Hot != testHot {
		t.Fatal("persisted vault failed to recover", err)
	}
	if err := m.checkFingerprint(persisted); err != nil {
		t.Fatal("new vault fingerprint not installed", err)
	}
}

func TestUnlockRejectsReplacementAndAuditFailure(t *testing.T) {
	for _, name := range []string{"seed replacement", "audit failure", "wrong password", "address mismatch"} {
		t.Run(name, func(t *testing.T) {
			m, mock := mockManager(t)
			raw := legacyVault(t)
			expectLock(mock, true)
			expectRead(mock, raw, " FOR UPDATE")
			deposit, pass := "", testPassphrase
			switch name {
			case "seed replacement":
				deposit = testReplacement
			case "wrong password":
				pass = "wrong"
			case "address mismatch":
				expectAddresses(mock, sqlmock.NewRows([]string{"family", "index", "address"}).AddRow("solana", 0, "invalid"))
			case "audit failure":
				expectAddresses(mock, nil)
				expectAudit(mock, "unlock", "ok").WillReturnError(errors.New(testDeposit + testPassphrase))
			}
			mock.ExpectRollback()
			auditOK(mock, "unlock", "denied")
			err := m.Unlock(t.Context(), pass, deposit, "")
			if err == nil || m.unlocked() {
				t.Fatal("invalid unlock returned seeds")
			}
			if strings.Contains(err.Error(), testDeposit) || strings.Contains(err.Error(), testPassphrase) {
				t.Fatal("secret leaked into error")
			}
		})
	}
}

func TestStaleManagerRelocksAllSeedQueries(t *testing.T) {
	for _, op := range []string{"derive", "hot_address", "sign_evm"} {
		t.Run(op, func(t *testing.T) {
			m, mock, raw := residentManager(t)
			mock.ExpectBegin()
			expectRead(mock, raw+"rotated", " FOR SHARE")
			mock.ExpectRollback()
			auditOK(mock, op, "denied")
			var err error
			switch op {
			case "derive":
				_, err = m.Derive(t.Context(), 0, 1)
			case "hot_address":
				_, err = m.HotAddresses(t.Context())
			case "sign_evm":
				_, err = m.SignEVM(t.Context(), sweepRequest())
			}
			if !errors.Is(err, ErrVaultLocked) || m.unlocked() {
				t.Fatal("stale seed remained usable", err)
			}
		})
	}
}

func TestDeriveLimitsAndStatusConcurrency(t *testing.T) {
	m, mock, _ := residentManager(t)
	for _, v := range [][2]uint32{{0, 0}, {0, 501}, {1 << 31, 1}, {(1 << 31) - 1, 2}} {
		auditOK(mock, "derive", "denied")
		if got, err := m.Derive(t.Context(), v[0], v[1]); got != nil || err == nil {
			t.Fatal("invalid range allowed")
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				m.SetEnabled(j%2 == 0)
				_, _ = m.Status(t.Context())
				_ = m.SetDestination(testDestination)
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("manager setters/status deadlocked")
	}
}
