package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// The one-shot migration command for moving the TOTP installation key out of
// the database. It never changes data: it prints the key, verifies that the key
// decrypts the stored authenticator ciphertext, and prints the remaining
// operator steps.
var (
	errTOTPKeyExportArguments = errors.New("TOTP installation key export requires only -export-totp-key -confirm")
	errTOTPKeyExportConfig    = errors.New("TOTP installation key export failed: unable to load configuration")
	errTOTPKeyExportDatabase  = errors.New("TOTP installation key export failed: database operation could not be completed")
	errTOTPKeyExportMissing   = errors.New("TOTP installation key export refused: security_secrets has no totp_encryption_key row; set TOTP_ENCRYPTION_KEY directly in the deployment environment")
	errTOTPKeyExportInvalid   = errors.New("TOTP installation key export refused: the stored key is not 32 bytes encoded as 64 hexadecimal characters")
	errTOTPKeyExportOutput    = errors.New("TOTP installation key export completed, but its result could not be written")
	errTOTPKeyExportReadOnly  = errors.New("read-only crypto test mode forbids exporting the shared installation key")
)

const totpKeyExportSQL = `SELECT value FROM security_secrets WHERE key = 'totp_encryption_key'`

// A command-only flag always consumes the invocation, including invalid or
// explicitly false flags, so it can never accidentally start the HTTP server.
func dispatchTOTPKeyExport(fs *flag.FlagSet, enabled, confirmed bool, run func() error) (bool, error) {
	if fs == nil {
		return false, nil
	}
	requested, conflicting := false, false
	visit := func(name string) {
		switch name {
		case "export-totp-key":
			requested = true
		case "confirm":
			// Shared with the administrator 2FA recovery command.
		default:
			conflicting = true
		}
	}
	fs.Visit(func(f *flag.Flag) { visit(f.Name) })
	// flag parsing stops at a positional argument or "--". A misplaced export
	// flag still signals export intent and must not start the server.
	for _, arg := range fs.Args() {
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if name == "export-totp-key" {
			requested = true
		}
	}
	if !requested {
		return false, nil
	}
	if !enabled || !confirmed || conflicting || fs.NArg() != 0 || run == nil {
		return true, errTOTPKeyExportArguments
	}
	return true, run()
}

func runTOTPKeyExportCommand() error {
	if service.CryptoAdminTestMode() {
		return errTOTPKeyExportReadOnly
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return executeTOTPKeyExport(ctx, os.Stdout, config.LoadForBootstrap, openAdmin2FAResetDatabase, verifyTOTPCiphertexts)
}

// executeTOTPKeyExport is separated from the flag plumbing so the migration
// output can be exercised without a live PostgreSQL instance.
func executeTOTPKeyExport(
	ctx context.Context,
	out io.Writer,
	loadConfig func() (*config.Config, error),
	openDatabase func(*config.Config) (*sql.DB, error),
	verifyCiphertexts func(context.Context, *sql.DB, string) (verified, failed int, err error),
) error {
	if service.CryptoAdminTestMode() {
		return errTOTPKeyExportReadOnly
	}
	cfg, err := loadConfig()
	if err != nil || cfg == nil {
		return errTOTPKeyExportConfig
	}
	db, err := openDatabase(cfg)
	if err != nil || db == nil {
		return errTOTPKeyExportDatabase
	}
	defer func() { _ = db.Close() }()

	key, err := readTOTPKeyFromDatabase(ctx, db)
	if err != nil {
		return err
	}
	verified, failed, err := verifyCiphertexts(ctx, db, key)
	if err != nil {
		return errTOTPKeyExportDatabase
	}
	text := totpKeyMigrationInstructions(key, verified, failed, strings.TrimSpace(os.Getenv("TOTP_ENCRYPTION_KEY")))
	if _, err := io.WriteString(out, text); err != nil {
		return errTOTPKeyExportOutput
	}
	return nil
}

func readTOTPKeyFromDatabase(ctx context.Context, db *sql.DB) (string, error) {
	var value string
	err := db.QueryRowContext(ctx, totpKeyExportSQL).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errTOTPKeyExportMissing
	}
	if err != nil {
		return "", errTOTPKeyExportDatabase
	}
	value = strings.TrimSpace(value)
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return "", errTOTPKeyExportInvalid
	}
	return value, nil
}

// verifyTOTPCiphertexts decrypts every stored authenticator secret with the
// exported key. It never returns plaintext or ciphertext, only counts.
func verifyTOTPCiphertexts(ctx context.Context, db *sql.DB, key string) (int, int, error) {
	encryptor, err := repository.NewAESEncryptor(&config.Config{Totp: config.TotpConfig{EncryptionKey: key}})
	if err != nil {
		return 0, 0, err
	}
	rows, err := db.QueryContext(ctx, `SELECT totp_secret_encrypted FROM users WHERE totp_secret_encrypted IS NOT NULL AND totp_secret_encrypted <> ''`)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = rows.Close() }()
	verified, failed := 0, 0
	for rows.Next() {
		var encrypted string
		if rows.Scan(&encrypted) != nil {
			return 0, 0, errTOTPKeyExportDatabase
		}
		if _, err := encryptor.Decrypt(encrypted); err != nil {
			failed++
			continue
		}
		verified++
	}
	if rows.Err() != nil {
		return 0, 0, errTOTPKeyExportDatabase
	}
	return verified, failed, nil
}

func totpKeyMigrationInstructions(key string, verified, failed int, environmentKey string) string {
	var builder strings.Builder
	_, _ = builder.WriteString("TOTP installation key export\n")
	_, _ = builder.WriteString("============================\n")
	_, _ = builder.WriteString("TOTP_ENCRYPTION_KEY=")
	_, _ = builder.WriteString(key)
	_, _ = builder.WriteString("\n")
	fmt.Fprintf(&builder, "Verified %d two-factor authenticator ciphertext(s) with this key", verified)
	if failed > 0 {
		fmt.Fprintf(&builder, "; %d could NOT be decrypted", failed)
	}
	_, _ = builder.WriteString(".\n")

	switch {
	case environmentKey == "":
		_, _ = builder.WriteString("\nThe deployment environment does not set TOTP_ENCRYPTION_KEY yet.\n")
	case strings.EqualFold(environmentKey, key):
		_, _ = builder.WriteString("\nThe deployment environment already sets this exact key; only steps 4 and 5 remain.\n")
	default:
		_, _ = builder.WriteString("\nWARNING: the deployment environment already sets a DIFFERENT TOTP_ENCRYPTION_KEY. The environment value wins,\n")
		_, _ = builder.WriteString("so existing two-factor logins would break. Replace it with the exported value before restarting.\n")
	}
	if failed > 0 {
		_, _ = builder.WriteString("WARNING: some authenticator ciphertext could not be decrypted; do not delete the database row and do not rotate the key.\n")
	}

	_, _ = builder.WriteString("\nMigration steps:\n")
	_, _ = builder.WriteString("  1. Append the TOTP_ENCRYPTION_KEY line above to the deployment environment file (for example /opt/sub2api/.env).\n")
	_, _ = builder.WriteString("     Keep it out of shell history, chat logs and version control.\n")
	_, _ = builder.WriteString("  2. Restart every instance and confirm startup does NOT log that the legacy installation key from security_secrets is used.\n")
	_, _ = builder.WriteString("  3. Verify an administrator two-factor login and the crypto wallet status still work.\n")
	_, _ = builder.WriteString("  4. Remove the transitional row, but only after every instance reads the environment value:\n")
	_, _ = builder.WriteString("       DELETE FROM security_secrets WHERE key = 'totp_encryption_key';\n")
	_, _ = builder.WriteString("  5. Restart once more and confirm the startup log contains no TOTP key warning.\n")
	return builder.String()
}
