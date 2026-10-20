package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

const (
	backupTestPassphrase = "public backup test passphrase"
	backupNewHotMnemonic = "letter advice cage absurd amount doctor acoustic avoid letter advice cage above"
)

var backupFixture struct {
	once sync.Once
	raw  string
	err  error
}

func backupTestVault(t *testing.T) string {
	t.Helper()
	backupFixture.once.Do(func() {
		backupFixture.raw, backupFixture.err = sealContents(testDepositMnemonic, testHotMnemonic, backupTestPassphrase)
	})
	if backupFixture.err != nil {
		t.Fatal(backupFixture.err)
	}
	return backupFixture.raw
}

func backupTestServer(t *testing.T, unlocked bool) (*server, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	// Rotation must work with ONE connection: the lock, reads, transaction
	// and audit may not silently hop back to the pool and deadlock.
	db.SetMaxOpenConns(1)
	st := &store{db: db}
	s := &server{store: st, audit: newAuditLog(st)}
	if unlocked {
		s.deriver, err = NewDeriver(testDepositMnemonic)
		if err != nil {
			t.Fatal(err)
		}
		s.hotDeriver, err = NewDeriver(testHotMnemonic)
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		_ = db.Close()
	})
	return s, mock
}

func expectBackupRead(mock sqlmock.Sqlmock, raw string) {
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT value FROM settings WHERE key = $1`)).
		WithArgs(settingVault).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(raw))
}

func expectVaultAudit(mock sqlmock.Sqlmock, op, result string, err error) {
	expected := mock.ExpectExec("INSERT INTO crypto_signer_audit").
		WithArgs(op, "", 0, 0, "", "", "", result, "")
	if err != nil {
		expected.WillReturnError(err)
	} else {
		expected.WillReturnResult(sqlmock.NewResult(0, 1))
	}
}

func backupRequest(t *testing.T, s *server, path, method, raw string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	s.registerVaultBackupRoutes(mux)
	mux.HandleFunc("/unlock", s.handleUnlock)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(method, path, strings.NewReader(raw)).WithContext(ctx))
	if !strings.Contains(rr.Header().Get("Cache-Control"), "no-store") ||
		!strings.Contains(rr.Header().Get("Cache-Control"), "no-cache") || rr.Header().Get("Pragma") != "no-cache" {
		t.Fatal("vault response is cacheable")
	}
	return rr
}

func backupBody(passphrase string) string {
	raw, _ := json.Marshal(map[string]string{"passphrase": passphrase})
	return string(raw)
}

func hotBackupBody(passphrase, mnemonic string) string {
	raw, _ := json.Marshal(map[string]string{"passphrase": passphrase, "hot_mnemonic": mnemonic})
	return string(raw)
}

func recoveryTestInput(t *testing.T, text string) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, text); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestVaultBackupAuthenticatesRegardlessOfUnlockState(t *testing.T) {
	for _, unlocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "locked", true: "unlocked"}[unlocked], func(t *testing.T) {
			s, mock := backupTestServer(t, unlocked)
			deposit, hot := s.deriver, s.hotDeriver
			fixture := backupTestVault(t)
			for _, attempt := range []struct {
				passphrase string
				status     int
			}{
				{backupTestPassphrase, http.StatusOK},
				{"wrong public passphrase", http.StatusForbidden},
				{"", http.StatusBadRequest},
			} {
				if attempt.passphrase != "" {
					expectBackupRead(mock, fixture)
				}
				outcome := "denied"
				if attempt.status == http.StatusOK {
					outcome = "success"
				}
				expectVaultAudit(mock, "backup", outcome, nil)
				rr := backupRequest(t, s, "/backup/vault", http.MethodPost, backupBody(attempt.passphrase))
				if rr.Code != attempt.status {
					t.Fatalf("status %d, want %d", rr.Code, attempt.status)
				}
				if attempt.status == http.StatusOK {
					backup, err := decodeVaultBackup(rr.Body.Bytes())
					if err != nil || backup.Vault != fixture {
						t.Fatal("backup did not preserve the original encrypted blob")
					}
				}
				for _, secret := range []string{testDepositMnemonic, testHotMnemonic, backupTestPassphrase} {
					if strings.Contains(rr.Body.String(), secret) {
						t.Fatal("backup response leaked a secret")
					}
				}
				if s.deriver != deposit || s.hotDeriver != hot {
					t.Fatal("export mutated the unlock state")
				}
			}
		})
	}
}

func TestVaultBackupReauthenticatesTheCurrentStoredVault(t *testing.T) {
	s, mock := backupTestServer(t, true)
	current, err := sealContents(testDepositMnemonic, testHotMnemonic, "different current password")
	if err != nil {
		t.Fatal(err)
	}
	expectBackupRead(mock, backupTestVault(t))
	expectVaultAudit(mock, "backup", "success", nil)
	if rr := backupRequest(t, s, "/backup/vault", http.MethodPost, backupBody(backupTestPassphrase)); rr.Code != http.StatusOK {
		t.Fatal("initial export failed")
	}
	expectBackupRead(mock, current)
	expectVaultAudit(mock, "backup", "denied", nil)
	if rr := backupRequest(t, s, "/backup/vault", http.MethodPost, backupBody(backupTestPassphrase)); rr.Code != http.StatusForbidden {
		t.Fatal("cached unlock/password bypassed the current vault")
	}
}

func TestVaultBackupRejectsInvalidRequestsBeforeReadingVault(t *testing.T) {
	for _, tc := range []struct{ name, method, body string }{
		{"get", http.MethodGet, backupBody(backupTestPassphrase)},
		{"missing", http.MethodPost, `{}`},
		{"null", http.MethodPost, `null`},
		{"blank", http.MethodPost, backupBody(" \t ")},
		{"long", http.MethodPost, backupBody(strings.Repeat("x", 1025))},
		{"unicode-byte-limit", http.MethodPost, backupBody(strings.Repeat("é", 513))},
		{"bad-json", http.MethodPost, `{"passphrase":`},
		{"trailing-json", http.MethodPost, backupBody(backupTestPassphrase) + `{}`},
		{"trailing-oversize", http.MethodPost, backupBody(backupTestPassphrase) + strings.Repeat(" ", 4096)},
		{"seed-input", http.MethodPost, `{"passphrase":"pw","deposit_mnemonic":"ignored?"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := backupTestServer(t, true)
			expectVaultAudit(mock, "backup", "denied", nil)
			rr := backupRequest(t, s, "/backup/vault", tc.method, tc.body)
			want := http.StatusBadRequest
			if tc.method != http.MethodPost {
				want = http.StatusMethodNotAllowed
			}
			if rr.Code != want {
				t.Fatalf("status %d, want %d", rr.Code, want)
			}
		})
	}
}

func TestSignerMnemonicExportWithoutVaultFailsClosed(t *testing.T) {
	s := &server{}
	mux := http.NewServeMux()
	s.registerVaultBackupRoutes(mux)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/backup/mnemonics", strings.NewReader(`{"passphrase":"test"}`)))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want %d: %s", rr.Code, http.StatusServiceUnavailable, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "mnemonic") || strings.Contains(rr.Body.String(), "deposit_mnemonic") {
		t.Fatalf("missing vault leaked mnemonic data: %s", rr.Body.String())
	}
}

func TestVaultBackupMissingOrCorruptVaultFailsClosed(t *testing.T) {
	for _, raw := range []string{"", " ", "not valid base64", strings.Repeat("x", (vaultBackupLimit/2)+1)} {
		s, mock := backupTestServer(t, true)
		expectBackupRead(mock, raw)
		expectVaultAudit(mock, "backup", "denied", nil)
		rr := backupRequest(t, s, "/backup/vault", http.MethodPost, backupBody(backupTestPassphrase))
		if rr.Code == http.StatusOK || strings.Contains(rr.Body.String(), `"vault"`) {
			t.Fatal("unavailable vault exported or initialized")
		}
	}
}

func TestVaultBackupAuditFailureNeverReturnsCiphertext(t *testing.T) {
	s, mock := backupTestServer(t, true)
	fixture := backupTestVault(t)
	expectBackupRead(mock, fixture)
	sentinel := errors.New("untrusted driver error containing " + fixture)
	expectVaultAudit(mock, "backup", "success", sentinel)
	expectVaultAudit(mock, "backup", "denied", sentinel)
	rr := backupRequest(t, s, "/backup/vault", http.MethodPost, backupBody(backupTestPassphrase))
	if rr.Code != http.StatusServiceUnavailable || strings.Contains(rr.Body.String(), fixture) {
		t.Fatal("failed audit allowed backup export or leaked a driver error")
	}
}

func TestVaultBackupFailureLimitIsGlobalAndExpires(t *testing.T) {
	s, mock := backupTestServer(t, false)
	for range vaultBackupFailures {
		expectBackupRead(mock, backupTestVault(t))
		expectVaultAudit(mock, "backup", "denied", nil)
		if rr := backupRequest(t, s, "/backup/vault", http.MethodPost, backupBody("wrong")); rr.Code != http.StatusForbidden {
			t.Fatalf("password attempt status %d", rr.Code)
		}
	}
	for _, path := range []string{"/backup/vault", "/vault/hot-mnemonic"} {
		op, body := "backup", backupBody(backupTestPassphrase)
		if path != "/backup/vault" {
			op, body = "hot_mnemonic_import", hotBackupBody(backupTestPassphrase, backupNewHotMnemonic)
		}
		expectVaultAudit(mock, op, "denied", nil)
		rr := backupRequest(t, s, path, http.MethodPost, body)
		if rr.Code != http.StatusTooManyRequests || rr.Header().Get("Retry-After") == "" {
			t.Fatal("failure budget was bypassed via another caller/route")
		}
	}
	// Advance the guard's admission clock without sleeping fifteen minutes.
	if retry := s.backup.begin(time.Now().Add(vaultBackupWindow + time.Second)); retry != 0 {
		t.Fatal("expired failures still block admission")
	}
	s.backup.finish(false)
}

func TestVaultBackupConcurrentRequestsDoNotQueueScrypt(t *testing.T) {
	s, mock := backupTestServer(t, true)
	s.audit = nil // no audit pool access while the SQL matcher is deliberately blocked
	mock.ExpectQuery("SELECT value FROM settings").WithArgs(settingVault).
		WillDelayFor(350 * time.Millisecond).
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(backupTestVault(t)))
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- backupRequest(t, s, "/backup/vault", http.MethodPost, backupBody("wrong")) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.backup.mu.Lock()
		busy := s.backup.busy
		s.backup.mu.Unlock()
		if busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first request did not acquire the global admission slot")
		}
		time.Sleep(time.Millisecond)
	}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			rr := backupRequest(t, s, "/backup/vault", http.MethodPost, backupBody("another wrong password"))
			if rr.Code != http.StatusTooManyRequests {
				t.Errorf("concurrent admission status %d", rr.Code)
			}
		})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		<-first
		t.Fatal("concurrent requests queued behind expensive authentication")
	}
	if rr := <-first; rr.Code != http.StatusForbidden {
		t.Fatalf("first authentication status %d", rr.Code)
	}
	if len(s.backup.failures) != 1 {
		t.Fatal("busy rejections performed extra password attempts")
	}
}

func expectSweepLock(mock sqlmock.Sqlmock, acquired bool) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_lock($1)")).
		WithArgs(vaultSweepLockKey).WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(acquired))
}

func expectSweepUnlock(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).
		WithArgs(vaultSweepLockKey).WillReturnRows(sqlmock.NewRows([]string{"unlocked"}).AddRow(true))
}

func expectSweepUnlockFailure(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).
		WithArgs(vaultSweepLockKey).WillReturnError(errors.New("simulated lost unlock response"))
}

func expectNoPendingSweep(mock sqlmock.Sqlmock, pending bool) {
	mock.ExpectQuery("SELECT EXISTS \\(SELECT 1 FROM sweep_records WHERE ").
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(pending))
}

func expectExistingVaultRow(mock sqlmock.Sqlmock, raw string, missing bool) {
	expectation := mock.ExpectQuery(regexp.QuoteMeta("SELECT value FROM settings WHERE key = $1 FOR UPDATE")).WithArgs(settingVault)
	if missing {
		expectation.WillReturnError(sql.ErrNoRows)
		return
	}
	expectation.WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(raw))
}

func expectVaultUpdate(mock sqlmock.Sqlmock, err error) {
	expectation := mock.ExpectExec("UPDATE settings SET value = \\$2, updated_at = NOW\\(\\) WHERE key = \\$1").
		WithArgs(settingVault, sqlmock.AnyArg())
	if err != nil {
		expectation.WillReturnError(err)
	} else {
		expectation.WillReturnResult(sqlmock.NewResult(0, 1))
	}
}

func TestHotMnemonicImportUsesSweepSessionLockAndPreservesDeposit(t *testing.T) {
	s, mock := backupTestServer(t, true)
	original, err := sealContents(testDepositMnemonic, testHotMnemonic, backupTestPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	depositDeriver := s.deriver
	expectSweepLock(mock, true)
	expectNoPendingSweep(mock, false)
	mock.ExpectBegin()
	expectExistingVaultRow(mock, original, false)
	expectVaultUpdate(mock, nil)
	expectVaultAudit(mock, "hot_mnemonic_import", "success", nil)
	mock.ExpectCommit()
	expectSweepUnlock(mock)
	rr := backupRequest(t, s, "/vault/hot-mnemonic", http.MethodPost, hotBackupBody(backupTestPassphrase, backupNewHotMnemonic))
	if rr.Code != http.StatusOK {
		t.Fatalf("hot import status %d: %s", rr.Code, rr.Body.String())
	}
	var status struct {
		Updated  bool `json:"updated"`
		Unlocked bool `json:"unlocked"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil || !status.Updated || status.Unlocked {
		t.Fatal("import did not report updated and relocked")
	}
	if s.deriver != depositDeriver || s.hotDeriver != nil {
		t.Fatal("import changed deposit deriver or retained the old hot deriver")
	}
}

func TestHotMnemonicImportRequiresSweepLockAndClearHistory(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pending bool
		status  int
	}{
		{"another-sweep-running", false, http.StatusConflict},
		{"pending-new-job", true, http.StatusConflict},
		{"clear-sweep-history", false, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := backupTestServer(t, false)
			expectSweepLock(mock, tc.name != "another-sweep-running")
			if tc.name != "another-sweep-running" {
				expectNoPendingSweep(mock, tc.pending)
			}
			if !tc.pending && tc.name != "another-sweep-running" {
				raw, err := sealContents(testDepositMnemonic, testHotMnemonic, backupTestPassphrase)
				if err != nil {
					t.Fatal(err)
				}
				mock.ExpectBegin()
				expectExistingVaultRow(mock, raw, false)
				expectVaultUpdate(mock, nil)
				expectVaultAudit(mock, "hot_mnemonic_import", "success", nil)
				mock.ExpectCommit()
			}
			if tc.name != "another-sweep-running" {
				expectSweepUnlock(mock)
			}
			if tc.status != http.StatusOK {
				expectVaultAudit(mock, "hot_mnemonic_import", "denied", nil)
			}
			rr := backupRequest(t, s, "/vault/hot-mnemonic", http.MethodPost, hotBackupBody(backupTestPassphrase, backupNewHotMnemonic))
			if rr.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rr.Code, tc.status, rr.Body.String())
			}
		})
	}
}

func TestHotMnemonicImportRejectsWrongPasswordSameSeedAndMissingVault(t *testing.T) {
	for _, tc := range []struct {
		name, passphrase, mnemonic string
		missing                    bool
		status                     int
	}{
		{"wrong-password", "wrong public passphrase", backupNewHotMnemonic, false, http.StatusForbidden},
		{"same-mnemonic-different-whitespace", backupTestPassphrase, strings.ReplaceAll(testDepositMnemonic, " ", "  "), false, http.StatusBadRequest},
		{"no-vault", backupTestPassphrase, backupNewHotMnemonic, true, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := backupTestServer(t, true)
			if !tc.missing {
				expectSweepLock(mock, true)
				expectNoPendingSweep(mock, false)
				mock.ExpectBegin()
				expectExistingVaultRow(mock, backupTestVault(t), tc.missing)
				mock.ExpectRollback()
				expectSweepUnlock(mock)
			}
			if tc.missing {
				expectSweepLock(mock, true)
				expectNoPendingSweep(mock, false)
				mock.ExpectBegin()
				expectExistingVaultRow(mock, "", true)
				mock.ExpectRollback()
				expectSweepUnlock(mock)
			}
			expectVaultAudit(mock, "hot_mnemonic_import", "denied", nil)
			rr := backupRequest(t, s, "/vault/hot-mnemonic", http.MethodPost, hotBackupBody(tc.passphrase, tc.mnemonic))
			if rr.Code != tc.status {
				t.Fatalf("status %d, want %d", rr.Code, tc.status)
			}
			if s.hotDeriver == nil {
				t.Fatal("a rejected import changed the current unlocked signer")
			}
		})
	}
}

func TestHotMnemonicUpdateAndSuccessAuditAreAtomic(t *testing.T) {
	for _, phase := range []string{"update-failed", "audit-failed", "unlock-failed"} {
		t.Run(phase, func(t *testing.T) {
			s, mock := backupTestServer(t, true)
			if phase == "unlock-failed" {
				// The failing session is discarded; leave capacity for the
				// separate denied-attempt audit connection.
				s.store.db.SetMaxOpenConns(2)
			}
			expectSweepLock(mock, true)
			expectNoPendingSweep(mock, false)
			mock.ExpectBegin()
			expectExistingVaultRow(mock, backupTestVault(t), false)
			switch phase {
			case "update-failed":
				expectVaultUpdate(mock, errors.New("simulated database failure"))
				mock.ExpectRollback()
				expectSweepUnlock(mock)
				expectVaultAudit(mock, "hot_mnemonic_import", "denied", nil)
			case "audit-failed":
				expectVaultUpdate(mock, nil)
				expectVaultAudit(mock, "hot_mnemonic_import", "success", errors.New("audit table unavailable"))
				mock.ExpectRollback()
				expectSweepUnlock(mock)
				expectVaultAudit(mock, "hot_mnemonic_import", "denied", nil)
			case "unlock-failed":
				expectVaultUpdate(mock, nil)
				expectVaultAudit(mock, "hot_mnemonic_import", "success", nil)
				mock.ExpectCommit()
				expectSweepUnlockFailure(mock)
			}
			rr := backupRequest(t, s, "/vault/hot-mnemonic", http.MethodPost, hotBackupBody(backupTestPassphrase, backupNewHotMnemonic))
			if rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("unsafe import result status %d", rr.Code)
			}
			if (phase == "unlock-failed") != (s.hotDeriver == nil) {
				t.Fatal("hot deriver invalidation did not match update/lock outcome")
			}
		})
	}
}

func TestSweepLockUnlockFailureDiscardsTheDatabaseSession(t *testing.T) {
	s, mock := backupTestServer(t, false)
	expectSweepLock(mock, true)
	expectSweepUnlockFailure(mock)
	_, release, err := s.lockVaultSweep(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err == nil {
		t.Fatal("lost advisory unlock response was ignored")
	}
	stats := s.store.db.Stats()
	if stats.InUse != 0 || stats.Idle != 0 {
		t.Fatalf("possibly locked session returned to pool: %+v", stats)
	}
}

func TestExistingVaultUnlockRejectsSeedReplacementBeforeAuthentication(t *testing.T) {
	s, mock := backupTestServer(t, false)
	expectBackupRead(mock, backupTestVault(t))
	rr := requestJSON(t, s.handleUnlock, http.MethodPost, map[string]string{
		"passphrase":       backupTestPassphrase,
		"deposit_mnemonic": testDepositMnemonic,
		"hot_mnemonic":     backupNewHotMnemonic,
	})
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "existing seeds unchanged") {
		t.Fatal("existing vault seed replacement did not fail closed clearly")
	}
}

func TestExistingVaultUnlockStillAcceptsLegacyShortPassphrase(t *testing.T) {
	s, mock := backupTestServer(t, false)
	legacyVault, err := sealContents(testDepositMnemonic, testHotMnemonic, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	expectBackupRead(mock, legacyVault)
	expectVaultAudit(mock, "unlock", "ok", nil)
	rr := requestJSON(t, s.handleUnlock, http.MethodPost, map[string]string{"passphrase": "legacy"})
	if rr.Code != http.StatusOK || s.deriver == nil || s.hotDeriver == nil {
		t.Fatal("existing vault's legacy short passphrase stopped working")
	}
}

func TestNewVaultRequiresStrongPassphraseAndFourKilobyteRequestLimit(t *testing.T) {
	for _, tc := range []struct {
		name, passphrase string
		oversize         bool
	}{
		{"short", "short", false},
		{"long", strings.Repeat("x", 1025), false},
		{"request-oversize", strings.Repeat("x", 11), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := backupTestServer(t, false)
			body, _ := json.Marshal(map[string]string{"passphrase": tc.passphrase, "deposit_mnemonic": testDepositMnemonic, "hot_mnemonic": backupNewHotMnemonic})
			if tc.oversize {
				body = bytes.Repeat([]byte{' '}, vaultRequestLimit+1)
			}
			if tc.name == "short" {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT value FROM settings WHERE key = $1`)).
					WithArgs(settingVault).WillReturnRows(sqlmock.NewRows([]string{"value"}))
			}
			rr := backupRequest(t, s, "/unlock", http.MethodPost, string(body))
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("initialization status %d", rr.Code)
			}
		})
	}
}

func TestVaultExportOfflineRecoveryPreservesBothMnemonics(t *testing.T) {
	s, mock := backupTestServer(t, false)
	expectBackupRead(mock, backupTestVault(t))
	expectVaultAudit(mock, "backup", "success", nil)
	rr := backupRequest(t, s, "/backup/vault", http.MethodPost, backupBody(backupTestPassphrase))
	if rr.Code != http.StatusOK {
		t.Fatal("export failed")
	}
	dir := t.TempDir()
	input, output := filepath.Join(dir, "backup.json"), filepath.Join(dir, "recovered.json")
	if err := os.WriteFile(input, rr.Body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	var diagnostic bytes.Buffer
	if err := recoverVaultBackup(input, output, recoveryTestInput(t, backupTestPassphrase+"\r\n"), &diagnostic); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var got vaultContents
	if err := json.Unmarshal(raw, &got); err != nil || got.Deposit != testDepositMnemonic || got.Hot != testHotMnemonic {
		t.Fatal("recovery did not preserve both public test mnemonics byte for byte")
	}
	info, err := os.Stat(output)
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatal("recovery file is not owner-only")
	}
	if !strings.Contains(diagnostic.String(), output) || strings.Contains(diagnostic.String(), backupTestPassphrase) ||
		strings.Contains(diagnostic.String(), testDepositMnemonic) || strings.Contains(diagnostic.String(), testHotMnemonic) {
		t.Fatal("recovery diagnostics leaked secrets or omitted the output path")
	}
	if err := recoverVaultBackup(input, output, recoveryTestInput(t, backupTestPassphrase), io.Discard); err == nil {
		t.Fatal("recovery overwrote an existing plaintext file")
	}
	unchanged, _ := os.ReadFile(output)
	if !bytes.Equal(raw, unchanged) {
		t.Fatal("existing output was modified")
	}
}

func TestVaultRecoveryRejectsInvalidEnvelopeAndWrongPassphrase(t *testing.T) {
	valid, _ := json.Marshal(SignerVaultBackup{Format: vaultBackupFormat, Version: 1, Vault: backupTestVault(t)})
	for name, raw := range map[string][]byte{
		"wrong-format":   []byte(`{"format":"other","version":1,"vault":"opaque"}`),
		"wrong-version":  []byte(`{"format":"sub2api-signer-vault","version":2,"vault":"opaque"}`),
		"missing-vault":  []byte(`{"format":"sub2api-signer-vault","version":1}`),
		"trailing":       append(append([]byte(nil), valid...), []byte(`{}`)...),
		"oversize":       bytes.Repeat([]byte{'x'}, vaultBackupLimit+1),
		"wrong-password": valid,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			input, output := filepath.Join(dir, "backup.json"), filepath.Join(dir, "new.json")
			if err := os.WriteFile(input, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			err := recoverVaultBackup(input, output, recoveryTestInput(t, "wrong password\n"), io.Discard)
			if err == nil {
				t.Fatal("invalid recovery succeeded")
			}
			if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed recovery left a plaintext output file")
			}
		})
	}
}

func TestRecoveryPassphraseInputPreservesSpacesAndEnforcesLimit(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		valid    bool
	}{
		{" public password \r\n", " public password ", true},
		{"legacy", "legacy", true},
		{strings.Repeat("x", 1024) + "\n", strings.Repeat("x", 1024), true},
		{strings.Repeat("x", 1025), "", false},
		{"\n", "", false},
		{"   \n", "", false},
		{"", "", false},
	} {
		got, err := readRecoveryPassphrase(recoveryTestInput(t, tc.in), io.Discard)
		if (err == nil) != tc.valid || string(got) != tc.want {
			t.Fatal("stdin passphrase validation or whitespace handling failed")
		}
		clear(got)
	}
}

func TestSignerBackupRecoveryCLIHelper(t *testing.T) {
	if os.Getenv("SIGNER_BACKUP_CLI_TEST_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"signer"}, os.Args[i+1:]...)
			break
		}
	}
	flag.CommandLine = flag.NewFlagSet("signer", flag.ExitOnError)
	main()
	os.Exit(0)
}

func TestSignerBackupRecoveryCLIIsOfflineAndDoesNotPrintSeeds(t *testing.T) {
	dir := t.TempDir()
	input, output := filepath.Join(dir, "backup.json"), filepath.Join(dir, "new.json")
	raw, _ := json.Marshal(SignerVaultBackup{Format: vaultBackupFormat, Version: 1, Vault: backupTestVault(t)})
	if err := os.WriteFile(input, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal("cannot resolve test executable")
	}
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestSignerBackupRecoveryCLIHelper$", "--", "--recover-backup", input, "--output", output)
	cmd.Env = append(os.Environ(), "SIGNER_BACKUP_CLI_TEST_HELPER=1", "SIGNER_DB_DSN=deliberately-invalid-no-database", "SIGNER_VAULT_BOOTSTRAP_PASSPHRASE=ignored-wrong-environment-value")
	cmd.Stdin = strings.NewReader(backupTestPassphrase + "\n")
	log, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("offline recovery failed: %v: %s", err, log)
	}
	if !bytes.Contains(log, []byte(output)) || bytes.Contains(log, []byte(testDepositMnemonic)) || bytes.Contains(log, []byte(testHotMnemonic)) || bytes.Contains(log, []byte(backupTestPassphrase)) {
		t.Fatal("CLI log is missing the path or contains sensitive material")
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatal("CLI did not create the selected output")
	}
}
