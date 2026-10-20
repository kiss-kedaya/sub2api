package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tyler-smith/go-bip39"
	"golang.org/x/term"
)

const (
	vaultBackupFormat    = "sub2api-signer-vault"
	vaultBackupVersion   = 1
	vaultRequestLimit    = 4 << 10
	vaultBackupLimit     = 64 << 10
	vaultPassphraseLimit = 1024 // bytes, without trimming the actual passphrase
	vaultBackupFailures  = 5
	vaultBackupWindow    = 15 * time.Minute
	// Must match sweeper.jobs.sweepLockKey. Service callers must NOT take it.
	vaultSweepLockKey int64 = 0x43525950544f5357
)

// Keep this predicate identical to sweeper.jobs.unfinishedPredicate, including
// ambiguous legacy jobs that already have a broadcast or need manual review.
const vaultUnfinishedSweepPredicate = `status NOT IN ('swept','failed') AND job_version = 1
	OR job_version = 0 AND status <> 'swept' AND
	(status IN ('manual_review','gas_sent') OR gas_tx_hash <> '' OR sweep_tx_hash <> '')`

var (
	errBackupVaultMissing = errors.New("existing encrypted vault unavailable")
	errBackupAuth         = errors.New("vault authentication failed")
	errBackupSweepBusy    = errors.New("sweep is running in another instance")
	errBackupSweepPending = errors.New("unfinished sweep jobs prevent hot mnemonic import")
	errBackupSameSeed     = errors.New("hot seed must differ from deposit seed")
	errBackupLockCleanup  = errors.New("vault operation finished but sweep lock cleanup failed; unlock required")
)

// SignerVaultBackup contains only the existing encrypted blob. Neither the
// HTTP response nor its audit trail may contain either mnemonic.
type SignerVaultBackup struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	Vault   string `json:"vault"`
}

// vaultBackupGuard admits one operation across ALL clients and both routes.
// Busy callers do not queue additional scrypt jobs. Successful authentication
// does not erase earlier failures from the rolling window.
type vaultBackupGuard struct {
	mu       sync.Mutex
	busy     bool
	failures []time.Time
}

func (g *vaultBackupGuard) begin(now time.Time) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	for len(g.failures) > 0 && !g.failures[0].Add(vaultBackupWindow).After(now) {
		g.failures = g.failures[1:]
	}
	if len(g.failures) >= vaultBackupFailures {
		return g.failures[0].Add(vaultBackupWindow).Sub(now)
	}
	if g.busy {
		return time.Second
	}
	g.busy = true
	return 0
}

func (g *vaultBackupGuard) finish(badPassphrase bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if badPassphrase {
		g.failures = append(g.failures, time.Now())
	}
	g.busy = false
}

func (s *server) registerVaultBackupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/backup/vault", s.handleBackupVault)
	mux.HandleFunc("/backup/mnemonics", s.handleExportMnemonics)
	mux.HandleFunc("/vault/hot-mnemonic", s.handleImportHotMnemonic)
}

func vaultResponseHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// Unlike auditLog.write, this propagates persistence failures. In particular,
// encrypted backups MUST NOT leave the signer without a durable success audit.
func (s *server) recordVaultAudit(ctx context.Context, op, result string) error {
	if s.audit == nil || s.audit.store == nil || s.audit.store.db == nil {
		return errors.New("vault audit unavailable")
	}
	s.audit.mu.Lock()
	defer s.audit.mu.Unlock()
	return s.audit.store.writeAudit(ctx, auditEntry{Op: op, Result: result})
}

func (s *server) denyVaultRequest(w http.ResponseWriter, status int, op, message string) {
	// Do not log driver errors: only the constant operation/outcome are audit
	// data. A disconnected caller must not suppress a denied audit attempt.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.recordVaultAudit(ctx, op, "denied"); err != nil {
		log.Printf("signer vault denied audit unavailable: %s", op)
	}
	writeJSON(w, status, map[string]string{"error": message})
}

func decodeVaultRequest(w http.ResponseWriter, r *http.Request, dst any) error {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, vaultRequestLimit))
	defer clear(raw)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("expected a single JSON object")
	}
	return nil
}

func validVaultPassphrase(passphrase string) bool {
	return len(passphrase) <= vaultPassphraseLimit && strings.TrimSpace(passphrase) != ""
}

func (s *server) admitVaultRequest(w http.ResponseWriter, op string) bool {
	if retry := s.backup.begin(time.Now()); retry > 0 {
		seconds := int64((retry + time.Second - 1) / time.Second)
		w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
		s.denyVaultRequest(w, http.StatusTooManyRequests, op, "vault operation temporarily rate limited")
		return false
	}
	return true
}

func (s *server) handleBackupVault(w http.ResponseWriter, r *http.Request) {
	const op = "backup"
	vaultResponseHeaders(w)
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.denyVaultRequest(w, http.StatusMethodNotAllowed, op, "method not allowed")
		return
	}
	var req struct {
		Passphrase string `json:"passphrase"`
	}
	if err := decodeVaultRequest(w, r, &req); err != nil || !validVaultPassphrase(req.Passphrase) {
		s.denyVaultRequest(w, http.StatusBadRequest, op, "one JSON object with a nonempty passphrase of at most 1024 bytes is required; body limit 4KB")
		return
	}
	defer func() { req.Passphrase = "" }()
	if !s.admitVaultRequest(w, op) {
		return
	}
	badPassphrase := false
	defer func() { s.backup.finish(badPassphrase) }()
	// /unlock already holds this lock through scrypt. Sharing it prevents
	// overlap with an unlock without changing any unlock state or seeds.
	s.vaultMu.Lock()
	defer s.vaultMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	raw, err := s.readExistingBackupVault(ctx)
	if err != nil {
		s.denyVaultRequest(w, http.StatusServiceUnavailable, op, "existing encrypted vault unavailable")
		return
	}
	// Never consult the cached derivers as proof of authentication.
	contents, err := openVaultString(raw, req.Passphrase)
	if err != nil {
		badPassphrase = true
		s.denyVaultRequest(w, http.StatusForbidden, op, "vault authentication failed")
		return
	}
	contents.Deposit, contents.Hot = "", ""
	if err := s.recordVaultAudit(ctx, op, "success"); err != nil {
		s.denyVaultRequest(w, http.StatusServiceUnavailable, op, "vault audit unavailable")
		return
	}
	writeJSON(w, http.StatusOK, SignerVaultBackup{
		Format: vaultBackupFormat, Version: vaultBackupVersion, Vault: raw,
	})
}

func (s *server) readExistingBackupVault(ctx context.Context) (string, error) {
	if s.store == nil || s.store.db == nil {
		return "", errors.New("vault store unavailable")
	}
	raw, err := s.store.getSetting(ctx, settingVault)
	if err != nil || strings.TrimSpace(raw) == "" || len(raw) > vaultBackupLimit/2 {
		return "", errors.New("existing encrypted vault unavailable")
	}
	return raw, nil
}

func (s *server) handleImportHotMnemonic(w http.ResponseWriter, r *http.Request) {
	const op = "hot_mnemonic_import"
	vaultResponseHeaders(w)
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.denyVaultRequest(w, http.StatusMethodNotAllowed, op, "method not allowed")
		return
	}
	var req struct {
		Passphrase  string `json:"passphrase"`
		HotMnemonic string `json:"hot_mnemonic"`
	}
	if err := decodeVaultRequest(w, r, &req); err != nil || !validVaultPassphrase(req.Passphrase) {
		s.denyVaultRequest(w, http.StatusBadRequest, op, "passphrase and hot_mnemonic required; passphrase limit 1024 bytes; body limit 4KB")
		return
	}
	req.HotMnemonic = strings.Join(strings.Fields(req.HotMnemonic), " ")
	defer func() { req.Passphrase, req.HotMnemonic = "", "" }()
	if !bip39.IsMnemonicValid(req.HotMnemonic) {
		s.denyVaultRequest(w, http.StatusBadRequest, op, "valid independent hot mnemonic required")
		return
	}
	if !s.admitVaultRequest(w, op) {
		return
	}
	badPassphrase := false
	defer func() { s.backup.finish(badPassphrase) }()
	s.vaultMu.Lock()
	defer s.vaultMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.importHotBackupVault(ctx, req.Passphrase, req.HotMnemonic); err != nil {
		status, message := http.StatusServiceUnavailable, "hot mnemonic update could not be persisted and audited"
		switch {
		case errors.Is(err, errBackupAuth):
			badPassphrase, status, message = true, http.StatusForbidden, errBackupAuth.Error()
		case errors.Is(err, errBackupSameSeed):
			status, message = http.StatusBadRequest, errBackupSameSeed.Error()
		case errors.Is(err, errBackupSweepBusy), errors.Is(err, errBackupSweepPending):
			status, message = http.StatusConflict, err.Error()
		case errors.Is(err, errBackupVaultMissing):
			message = errBackupVaultMissing.Error()
		case errors.Is(err, errBackupLockCleanup):
			message = errBackupLockCleanup.Error()
		}
		s.denyVaultRequest(w, status, op, message)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"updated": true, "unlocked": false})
}

// importHotBackupVault is called with vaultMu held. One reserved PG connection
// owns the session lock, sweep check, row lock, update and success audit.
func (s *server) importHotBackupVault(ctx context.Context, passphrase, hotMnemonic string) (retErr error) {
	if s.store == nil || s.store.db == nil || s.audit == nil || s.audit.store == nil || s.audit.store.db != s.store.db {
		return errors.New("vault audit unavailable")
	}
	conn, release, err := s.lockVaultSweep(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := release(); err != nil && retErr == nil {
			retErr = errBackupLockCleanup
		}
	}()
	var pending bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM sweep_records WHERE (`+
		vaultUnfinishedSweepPredicate+`))`).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return errBackupSweepPending
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = $1 FOR UPDATE`, settingVault).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errBackupVaultMissing
		}
		return err
	}
	if strings.TrimSpace(raw) == "" || len(raw) > vaultBackupLimit/2 {
		return errBackupVaultMissing
	}
	contents, err := openVaultString(raw, passphrase)
	if err != nil {
		return errBackupAuth
	}
	defer func() { contents.Deposit, contents.Hot = "", "" }()
	if hotMnemonic == strings.Join(strings.Fields(contents.Deposit), " ") {
		return errBackupSameSeed
	}
	// There is deliberately no deposit input. Preserve its authenticated
	// plaintext byte for byte, and never create a missing vault.
	sealed, err := sealContents(contents.Deposit, hotMnemonic, passphrase)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE settings SET value = $2, updated_at = NOW() WHERE key = $1`, settingVault, sealed)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return errors.New("vault update did not affect exactly one existing row")
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO crypto_signer_audit
		  (op, kind, derivation_index, chain_id, to_address, token, amount, result, reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		"hot_mnemonic_import", "", 0, 0, "", "", "", "success", ""); err != nil {
		return err
	}
	// A lost COMMIT reply has an ambiguous outcome. Fail closed locally even
	// then: never continue signing with a possibly obsolete cached hot seed.
	s.hotDeriver = nil
	return tx.Commit()
}

func (s *server) lockVaultSweep(ctx context.Context) (*sql.Conn, func() error, error) {
	conn, err := s.store.db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	discard := func() { _ = conn.Raw(func(any) error { return driver.ErrBadConn }) }
	var locked bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, vaultSweepLockKey).Scan(&locked); err != nil {
		discard() // acquisition might have succeeded with a lost reply
		_ = conn.Close()
		return nil, nil, err
	}
	if !locked {
		_ = conn.Close()
		return nil, nil, errBackupSweepBusy
	}
	release := func() error {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		err := conn.QueryRowContext(cleanup, `SELECT pg_advisory_unlock($1)`, vaultSweepLockKey).Scan(&unlocked)
		if err != nil || !unlocked {
			discard() // a possibly locked session must never reenter the pool
			if err == nil {
				err = errors.New("sweep advisory lock was not held")
			}
		}
		return errors.Join(err, conn.Close())
	}
	return conn, release, nil
}

func decodeVaultBackup(raw []byte) (*SignerVaultBackup, error) {
	var backup SignerVaultBackup
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) > vaultBackupLimit || decoder.Decode(&backup) != nil ||
		backup.Format != vaultBackupFormat || backup.Version != vaultBackupVersion ||
		strings.TrimSpace(backup.Vault) == "" || decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid or unsupported signer vault backup")
	}
	return &backup, nil
}

func readRecoveryPassphrase(in *os.File, diagnostic io.Writer) ([]byte, error) {
	var passphrase []byte
	var err error
	if term.IsTerminal(int(in.Fd())) {
		_, _ = fmt.Fprint(diagnostic, "Vault passphrase: ")
		passphrase, err = term.ReadPassword(int(in.Fd()))
		_, _ = fmt.Fprintln(diagnostic)
	} else {
		// Read a bounded single line. Preserve spaces in the password and
		// remove only the line terminator, including Windows CRLF.
		passphrase, err = bufio.NewReader(io.LimitReader(in, vaultPassphraseLimit+3)).ReadBytes('\n')
		if err == io.EOF && len(passphrase) > 0 {
			err = nil
		}
		if bytes.HasSuffix(passphrase, []byte{'\n'}) {
			passphrase = bytes.TrimSuffix(passphrase, []byte{'\n'})
			passphrase = bytes.TrimSuffix(passphrase, []byte{'\r'})
		}
	}
	if err != nil || !validVaultPassphrase(string(passphrase)) {
		clear(passphrase)
		return nil, errors.New("could not read a nonempty vault passphrase of at most 1024 bytes")
	}
	return passphrase, nil
}

// recoverVaultBackup is strictly offline. The only plaintext output is a NEW
// operator-selected file, created with O_EXCL and owner-only POSIX permissions.
func recoverVaultBackup(inputPath, outputPath string, in *os.File, diagnostic io.Writer) error {
	if strings.TrimSpace(outputPath) == "" || outputPath == "-" {
		return errors.New("--output must name a new private file")
	}
	if _, err := os.Lstat(outputPath); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("recovery output already exists or is inaccessible; refusing to overwrite")
	}
	f, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("open backup file: %w", err)
	}
	raw, err := io.ReadAll(io.LimitReader(f, vaultBackupLimit+1))
	_ = f.Close()
	if err != nil {
		return errors.New("could not read backup file")
	}
	backup, err := decodeVaultBackup(raw)
	if err != nil {
		return err
	}
	passphrase, err := readRecoveryPassphrase(in, diagnostic)
	if err != nil {
		return err
	}
	defer clear(passphrase)
	contents, err := openVaultString(backup.Vault, string(passphrase))
	if err != nil {
		return errors.New("backup authentication failed")
	}
	defer func() { contents.Deposit, contents.Hot = "", "" }()
	if !bip39.IsMnemonicValid(contents.Deposit) || !bip39.IsMnemonicValid(contents.Hot) {
		return errors.New("backup does not contain two valid mnemonics")
	}
	plaintext, err := json.Marshal(contents)
	if err != nil {
		return errors.New("could not encode recovered mnemonics")
	}
	defer clear(plaintext)
	out, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create NEW recovery output: %w", err)
	}
	committed := false
	defer func() {
		_ = out.Close()
		if !committed {
			_ = os.Remove(outputPath)
		}
	}()
	if n, err := out.Write(plaintext); err != nil || n != len(plaintext) {
		return errors.New("could not write recovery output")
	}
	if err := out.Sync(); err != nil {
		return errors.New("could not sync recovery output")
	}
	if err := out.Close(); err != nil {
		return errors.New("could not close recovery output")
	}
	committed = true
	_, _ = fmt.Fprintf(diagnostic, "Recovered mnemonics to %s\n", outputPath)
	return nil
}
