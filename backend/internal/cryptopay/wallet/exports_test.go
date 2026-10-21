package wallet

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestExportsAuthenticateEveryCallRegardlessOfUnlock(t *testing.T) {
	for _, unlocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "locked", true: "unlocked"}[unlocked], func(t *testing.T) {
			m, mock := mockManager(t)
			raw := legacyVault(t)
			if unlocked {
				expectUnlock(mock, raw)
				if err := m.Unlock(t.Context(), testPassphrase, "", ""); err != nil {
					t.Fatal(err)
				}
			}
			expectRead(mock, raw, "")
			auditOK(mock, "mnemonic_export", "success")
			exported, err := m.ExportMnemonics(t.Context(), testPassphrase)
			if err != nil || exported.DepositMnemonic != testDeposit || exported.HotMnemonic != testHot {
				t.Fatal("mnemonic export failed", err)
			}
			expectRead(mock, raw, "")
			auditOK(mock, "backup", "success")
			backup, err := m.ExportVaultBackup(t.Context(), testPassphrase)
			if err != nil || backup.Format != "sub2api-signer-vault" || backup.Version != 1 || backup.Vault != raw {
				t.Fatal("legacy backup format changed", err)
			}
			expectRead(mock, raw, "")
			auditOK(mock, "mnemonic_export", "denied")
			if value, err := m.ExportMnemonics(t.Context(), "wrong"); value != nil || !errors.Is(err, ErrMnemonicExportRejected) {
				t.Fatal("unlocked state bypassed authentication", err)
			}
			expectRead(mock, raw, "")
			auditOK(mock, "backup", "denied")
			if value, err := m.ExportVaultBackup(t.Context(), "wrong"); value != nil || !errors.Is(err, ErrMnemonicExportRejected) {
				t.Fatal("backup bypassed authentication", err)
			}
			if m.unlocked() != unlocked {
				t.Fatal("export changed residency")
			}
		})
	}
}

func TestAuditFailureRejectsBothExportsAndRedactsDriverErrors(t *testing.T) {
	for _, op := range []string{"mnemonic_export", "backup"} {
		t.Run(op, func(t *testing.T) {
			m, mock, raw := residentManager(t)
			expectRead(mock, raw, "")
			secretError := errors.New("driver error: " + testPassphrase + testDeposit + testHot + raw)
			expectAudit(mock, op, "success").WillReturnError(secretError)
			expectAudit(mock, op, "denied").WillReturnError(secretError)
			var err error
			if op == "mnemonic_export" {
				var got *MnemonicExport
				got, err = m.ExportMnemonics(t.Context(), testPassphrase)
				if got != nil {
					t.Fatal("plaintext released without audit")
				}
			} else {
				var got *VaultBackup
				got, err = m.ExportVaultBackup(t.Context(), testPassphrase)
				if got != nil {
					t.Fatal("ciphertext released without audit")
				}
			}
			if !errors.Is(err, ErrAuditUnavailable) {
				t.Fatal("missing audit error", err)
			}
			for _, secret := range []string{testPassphrase, testDeposit, testHot, raw} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("secret leaked")
				}
			}
		})
	}
}

func TestExportMissingVaultAndInvalidPasswordNeverInitializes(t *testing.T) {
	m, mock := mockManager(t)
	for _, pass := range []string{"", " ", strings.Repeat("p", 1025)} {
		auditOK(mock, "mnemonic_export", "denied")
		if value, err := m.ExportMnemonics(t.Context(), pass); value != nil || !errors.Is(err, ErrMnemonicExportRejected) {
			t.Fatal("invalid password allowed", err)
		}
	}
	expectRead(mock, "", "")
	auditOK(mock, "backup", "denied")
	if got, err := m.ExportVaultBackup(t.Context(), testPassphrase); got != nil || !errors.Is(err, ErrVaultUnavailable) {
		t.Fatal("missing vault export succeeded", err)
	}
}

func TestVaultGuardSharesFailureWindowAndBusyAcrossOperations(t *testing.T) {
	m, mock := mockManager(t)
	m.guard.failures = []time.Time{time.Now(), time.Now(), time.Now(), time.Now(), time.Now()}
	auditOK(mock, "mnemonic_export", "denied")
	if _, err := m.ExportMnemonics(t.Context(), testPassphrase); !errors.Is(err, ErrMnemonicExportLimited) {
		t.Fatal(err)
	}
	auditOK(mock, "backup", "denied")
	if _, err := m.ExportVaultBackup(t.Context(), testPassphrase); !errors.Is(err, ErrMnemonicExportLimited) {
		t.Fatal(err)
	}
	m.guard.failures = nil
	m.guard.busy = true
	auditOK(mock, "hot_mnemonic_import", "denied")
	if err := m.ImportHotMnemonic(t.Context(), testPassphrase, testReplacement); !errors.Is(err, ErrMnemonicExportLimited) {
		t.Fatal(err)
	}
	m.guard.busy = false
	m.guard.failures = []time.Time{time.Now().Add(-16 * time.Minute)}
	if !m.guard.begin() {
		t.Fatal("expired failures retained")
	}
	m.guard.finish(true)
	if !m.guard.begin() {
		t.Fatal("single failure blocked")
	}
	m.guard.finish(false)
	if len(m.guard.failures) != 1 {
		t.Fatal("success erased recent failure")
	}
}

func expectImportStart(mock sqlmock.Sqlmock, raw string, pending bool) {
	expectLock(mock, true)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS (SELECT 1 FROM sweep_records WHERE (` + unfinishedSweepPredicate + `))`)).
		WillReturnRows(sqlmock.NewRows([]string{"pending"}).AddRow(pending))
	if !pending {
		expectRead(mock, raw, " FOR UPDATE")
	}
}

func TestImportHotMnemonicPersistsAuditsThenRelocks(t *testing.T) {
	m, mock, raw := residentManager(t)
	oldDepositSeed, oldHotSeed := m.deposit.seed, m.hot.seed
	expectImportStart(mock, raw, false)
	expectAddresses(mock, nil)
	var persisted string
	mock.ExpectExec("UPDATE settings SET value").WithArgs(settingVault, sealedArg{capture: &persisted}).WillReturnResult(sqlmock.NewResult(0, 1))
	auditOK(mock, "hot_mnemonic_import", "success")
	mock.ExpectCommit()
	if err := m.ImportHotMnemonic(t.Context(), testPassphrase, testReplacement); err != nil {
		t.Fatal(err)
	}
	if m.unlocked() {
		t.Fatal("import did not relock")
	}
	for _, seed := range [][]byte{oldDepositSeed, oldHotSeed} {
		for _, b := range seed {
			if b != 0 {
				t.Fatal("old resident seed not cleared")
			}
		}
	}
	c, err := openVaultString(persisted, testPassphrase)
	if err != nil || c.Deposit != testDeposit || c.Hot != testReplacement {
		t.Fatal("import replaced deposit seed or failed to update hot", err)
	}
	expectUnlock(mock, persisted)
	if err := m.Unlock(t.Context(), testPassphrase, "", ""); err != nil {
		t.Fatal(err)
	}
	expectRead(mock, persisted, "")
	auditOK(mock, "mnemonic_export", "success")
	value, err := m.ExportMnemonics(t.Context(), testPassphrase)
	if err != nil || value.HotMnemonic != testReplacement {
		t.Fatal("imported hot mnemonic not exported", err)
	}
}

func TestImportRollsBackOnLockJobsCredentialsSeedsAndAuditFailure(t *testing.T) {
	for _, name := range []string{"busy", "pending", "password", "same seed", "invalid seed", "audit failure", "commit failure"} {
		t.Run(name, func(t *testing.T) {
			m, mock, raw := residentManager(t)
			previous := m.hot
			pass, mnemonic := testPassphrase, testReplacement
			switch name {
			case "busy":
				expectLock(mock, false)
			case "pending":
				expectImportStart(mock, raw, true)
			default:
				expectImportStart(mock, raw, false)
			}
			switch name {
			case "password":
				pass = "wrong"
			case "same seed":
				mnemonic = testDeposit
			case "invalid seed":
				mnemonic = "invalid"
			case "audit failure", "commit failure":
				expectAddresses(mock, nil)
				mock.ExpectExec("UPDATE settings SET value").WithArgs(settingVault, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
				if name == "audit failure" {
					expectAudit(mock, "hot_mnemonic_import", "success").WillReturnError(errors.New(testHot))
				} else {
					auditOK(mock, "hot_mnemonic_import", "success")
					mock.ExpectCommit().WillReturnError(errors.New("ambiguous commit"))
				}
			}
			if name != "commit failure" {
				mock.ExpectRollback()
			}
			auditOK(mock, "hot_mnemonic_import", "denied")
			if err := m.ImportHotMnemonic(t.Context(), pass, mnemonic); err == nil {
				t.Fatal("invalid import allowed")
			}
			if name == "commit failure" {
				if m.unlocked() {
					t.Fatal("ambiguous commit must relock")
				}
			} else if !m.unlocked() || m.hot != previous {
				t.Fatal("failed import changed resident wallet")
			}
		})
	}
}
