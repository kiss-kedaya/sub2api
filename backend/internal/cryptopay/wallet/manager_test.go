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

func TestAutomaticReloadAndStableAddressQueries(t *testing.T) {
	m, mock := mockManager(t)
	raw := managedVault(t)
	expectRead(mock, raw, "")
	status, err := m.Status(t.Context())
	if err != nil || !status.Configured || !status.Ready {
		t.Fatal("restart did not load seeds", err)
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
	expectRead(mock, raw, "")
	status, err = m.Status(t.Context())
	if err != nil || !status.OK || !status.Ready || !status.Enabled {
		t.Fatal("wrong unlocked status", err)
	}
	m.SetEnabled(false)
	expectRead(mock, raw, "")
	status, err = m.Status(t.Context())
	if err != nil || status.Enabled {
		t.Fatal("signing switch ignored", err)
	}
}

func TestNewVaultValidatesSeedsAndExistingAddresses(t *testing.T) {
	tests := []struct {
		name, deposit, hot string
		mismatch           bool
	}{
		{"same seed", testDeposit, testDeposit, false},
		{"invalid deposit", "bad", testHot, false},
		{"invalid hot", testDeposit, "bad", false},
		{"old deposits disagree", testDeposit, testHot, true},
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
			auditOK(mock, "wallet_init", "denied")
			err := m.Initialize(t.Context(), tt.deposit, tt.hot)
			if err == nil || m.ready() {
				t.Fatal("bad initialization was accepted")
			}
			if tt.mismatch && !errors.Is(err, ErrDepositSeedMismatch) {
				t.Fatal(err)
			}
		})
	}
}

func TestInitializeCommitsEncryptedSeedsBeforeUse(t *testing.T) {
	m, mock := mockManager(t)
	expectLock(mock, true)
	expectRead(mock, "", " FOR UPDATE")
	expectAddresses(mock, nil)
	var persisted string
	mock.ExpectExec("INSERT INTO settings").WithArgs(settingVault, sealedArg{capture: &persisted}).WillReturnResult(sqlmock.NewResult(1, 1))
	auditOK(mock, "wallet_init", "ok")
	mock.ExpectCommit()
	if err := m.Initialize(t.Context(), testDeposit, testHot); err != nil {
		t.Fatal(err)
	}
	if !m.ready() || persisted == "" || strings.Contains(persisted, testDeposit) {
		t.Fatal("vault not sealed and installed")
	}
	contents, err := m.openContents(persisted)
	if err != nil || contents.Deposit != testDeposit || contents.Hot != testHot {
		t.Fatal("persisted vault failed to recover", err)
	}
	if err := m.refreshContents(persisted); err != nil {
		t.Fatal("new vault fingerprint not installed", err)
	}
}

func TestInitializeNeverOverwritesExistingOrLegacyCiphertext(t *testing.T) {
	for _, raw := range []string{managedVault(t), "legacy-password-encrypted-seeds"} {
		m, mock := mockManager(t)
		expectLock(mock, true)
		expectRead(mock, raw, " FOR UPDATE")
		mock.ExpectRollback()
		auditOK(mock, "wallet_init", "denied")
		if err := m.Initialize(t.Context(), testDeposit, testHot); !errors.Is(err, ErrSeedReplacement) || m.ready() {
			t.Fatal(err)
		}
	}
}

func TestInitializeAuditFailureDoesNotInstallKeys(t *testing.T) {
	m, mock := mockManager(t)
	expectLock(mock, true)
	expectRead(mock, "", " FOR UPDATE")
	expectAddresses(mock, nil)
	mock.ExpectExec("INSERT INTO settings").WillReturnResult(sqlmock.NewResult(1, 1))
	expectAudit(mock, "wallet_init", "ok").WillReturnError(errors.New(testDeposit))
	mock.ExpectRollback()
	auditOK(mock, "wallet_init", "denied")
	if err := m.Initialize(t.Context(), testDeposit, testHot); !errors.Is(err, ErrAuditUnavailable) || m.ready() {
		t.Fatal(err)
	}
}

func TestManagedEncryptionRejectsMissingWrongKeyTamperAndLegacy(t *testing.T) {
	m := New(nil, testMasterKey)
	a, err := m.sealContents(testDeposit, testHot)
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.sealContents(testDeposit, testHot)
	if err != nil || a == b || strings.Contains(a, testDeposit) {
		t.Fatal("plaintext or nonce reuse", err)
	}
	for _, other := range []*Manager{New(nil), New(nil, "invalid"), New(nil, strings.Repeat("ab", 32))} {
		if v, err := other.openContents(a); v != nil || err == nil {
			t.Fatal("wrong key accepted")
		}
	}
	for _, raw := range []string{a[:len(a)-5] + "xxxxx", "legacy-password-encrypted-seeds", managedVaultPrefix + "bad"} {
		if v, err := m.openContents(raw); v != nil || err == nil {
			t.Fatal("invalid ciphertext accepted")
		}
	}
	if v, err := m.openContents(a); err != nil || v.Deposit != testDeposit || v.Hot != testHot {
		t.Fatal("restart key recovery failed", err)
	}
}

func TestMissingInstallationKeyCannotInitializeSeeds(t *testing.T) {
	m, mock := mockManager(t)
	m.cipher = nil
	auditOK(mock, "wallet_init", "denied")
	if err := m.Initialize(t.Context(), testDeposit, testHot); !errors.Is(err, ErrVaultUnavailable) || m.ready() {
		t.Fatal("missing key must not persist plaintext or initialize seeds", err)
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
			if !errors.Is(err, ErrVaultUnavailable) || m.ready() {
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
	statusManager := New(nil, testMasterKey)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				statusManager.SetEnabled(j%2 == 0)
				_, _ = statusManager.Status(t.Context())
				_ = statusManager.SetDestination(testDestination)
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
