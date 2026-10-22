package wallet

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func expectExport(mock sqlmock.Sqlmock, raw string) {
	mock.ExpectBegin()
	expectRead(mock, raw, " FOR SHARE")
	expectRead(mock, raw, "")
}

func TestManagedExportNeedsNoPasswordAndLoadsAfterRestart(t *testing.T) {
	m, mock := mockManager(t)
	raw := managedVault(t)
	expectExport(mock, raw)
	auditOK(mock, "mnemonic_export", "success")
	mock.ExpectCommit()
	value, err := m.ExportMnemonics(t.Context())
	if err != nil || value.DepositMnemonic != testDeposit || value.HotMnemonic != testHot {
		t.Fatal(err)
	}
}

func TestExportAuditFailureAndCommitFailureNeverReturnSeeds(t *testing.T) {
	for _, commit := range []bool{false, true} {
		m, mock, raw := residentManager(t)
		expectExport(mock, raw)
		if commit {
			auditOK(mock, "mnemonic_export", "success")
			mock.ExpectCommit().WillReturnError(errors.New(testDeposit))
		} else {
			expectAudit(mock, "mnemonic_export", "success").WillReturnError(errors.New(testHot))
			mock.ExpectRollback()
		}
		value, err := m.ExportMnemonics(t.Context())
		if value != nil || !errors.Is(err, ErrAuditUnavailable) || strings.Contains(err.Error(), testDeposit) || strings.Contains(err.Error(), testHot) {
			t.Fatal("unaudited export or secret leak", err)
		}
	}
}

func TestMissingOrCorruptCiphertextCannotExportOrUseCachedSeeds(t *testing.T) {
	for _, raw := range []string{"", "corrupted", "legacy-password-encrypted-seeds"} {
		m, mock, _ := residentManager(t)
		mock.ExpectBegin()
		expectRead(mock, raw, " FOR SHARE")
		mock.ExpectRollback()
		auditOK(mock, "mnemonic_export", "denied")
		if value, err := m.ExportMnemonics(t.Context()); value != nil || err == nil || m.ready() {
			t.Fatal("stale seed released", err)
		}
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

func TestImportHotMnemonicPersistsAuditsAndWorksImmediately(t *testing.T) {
	m, mock, raw := residentManager(t)
	oldDepositSeed, oldHotSeed := m.deposit.seed, m.hot.seed
	expectImportStart(mock, raw, false)
	expectAddresses(mock, nil)
	var persisted string
	mock.ExpectExec("UPDATE settings SET value").WithArgs(settingVault, sealedArg{capture: &persisted}).WillReturnResult(sqlmock.NewResult(0, 1))
	auditOK(mock, "hot_mnemonic_import", "success")
	mock.ExpectCommit()
	if err := m.ImportHotMnemonic(t.Context(), testReplacement); err != nil {
		t.Fatal(err)
	}
	if !m.ready() {
		t.Fatal("import did not become ready")
	}
	for _, seed := range [][]byte{oldDepositSeed, oldHotSeed} {
		for _, b := range seed {
			if b != 0 {
				t.Fatal("old resident seed not cleared")
			}
		}
	}
	c, err := m.openContents(persisted)
	if err != nil || c.Deposit != testDeposit || c.Hot != testReplacement {
		t.Fatal("import replaced deposit seed or failed to update hot", err)
	}
	expectExport(mock, persisted)
	auditOK(mock, "mnemonic_export", "success")
	mock.ExpectCommit()
	value, err := m.ExportMnemonics(t.Context())
	if err != nil || value.HotMnemonic != testReplacement {
		t.Fatal("imported hot mnemonic not exported", err)
	}
}

func TestImportRollsBackOnLockJobsCredentialsSeedsAndAuditFailure(t *testing.T) {
	for _, name := range []string{"busy", "pending", "ciphertext", "same seed", "invalid seed", "audit failure", "commit failure"} {
		t.Run(name, func(t *testing.T) {
			m, mock, raw := residentManager(t)
			previous := m.hot
			mnemonic := testReplacement
			switch name {
			case "busy":
				expectLock(mock, false)
			case "pending":
				expectImportStart(mock, raw, true)
			default:
				if name == "ciphertext" {
					raw = "corrupt"
				}
				expectImportStart(mock, raw, false)
			}
			switch name {
			case "ciphertext":
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
			if err := m.ImportHotMnemonic(t.Context(), mnemonic); err == nil {
				t.Fatal("invalid import allowed")
			}
			if name == "commit failure" {
				if m.ready() {
					t.Fatal("ambiguous commit must relock")
				}
			} else if !m.ready() || m.hot != previous {
				t.Fatal("failed import changed resident wallet")
			}
		})
	}
}
