package main

import (
	"bytes"
	"context"
	"database/sql"
	"flag"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func newTOTPKeyExportFlagSet() (*flag.FlagSet, *bool, *bool) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	enabled := fs.Bool("export-totp-key", false, "")
	confirmed := fs.Bool("confirm", false, "")
	_ = fs.Bool("migrate-only", false, "")
	return fs, enabled, confirmed
}

func TestDispatchTOTPKeyExport(t *testing.T) {
	t.Run("not requested", func(t *testing.T) {
		fs, enabled, confirmed := newTOTPKeyExportFlagSet()
		require.NoError(t, fs.Parse(nil))
		called := false
		handled, err := dispatchTOTPKeyExport(fs, *enabled, *confirmed, func() error { called = true; return nil })
		require.False(t, handled)
		require.NoError(t, err)
		require.False(t, called)
	})

	t.Run("missing confirm", func(t *testing.T) {
		fs, enabled, confirmed := newTOTPKeyExportFlagSet()
		require.NoError(t, fs.Parse([]string{"-export-totp-key"}))
		handled, err := dispatchTOTPKeyExport(fs, *enabled, *confirmed, func() error { return nil })
		require.True(t, handled)
		require.ErrorIs(t, err, errTOTPKeyExportArguments)
	})

	t.Run("conflicting flag", func(t *testing.T) {
		fs, enabled, confirmed := newTOTPKeyExportFlagSet()
		require.NoError(t, fs.Parse([]string{"-export-totp-key", "-confirm", "-migrate-only"}))
		handled, err := dispatchTOTPKeyExport(fs, *enabled, *confirmed, func() error { return nil })
		require.True(t, handled)
		require.ErrorIs(t, err, errTOTPKeyExportArguments)
	})

	t.Run("positional argument", func(t *testing.T) {
		fs, enabled, confirmed := newTOTPKeyExportFlagSet()
		require.NoError(t, fs.Parse([]string{"-export-totp-key", "-confirm", "unexpected"}))
		handled, err := dispatchTOTPKeyExport(fs, *enabled, *confirmed, func() error { return nil })
		require.True(t, handled)
		require.ErrorIs(t, err, errTOTPKeyExportArguments)
	})

	t.Run("runs when confirmed", func(t *testing.T) {
		fs, enabled, confirmed := newTOTPKeyExportFlagSet()
		require.NoError(t, fs.Parse([]string{"-export-totp-key", "-confirm"}))
		called := false
		handled, err := dispatchTOTPKeyExport(fs, *enabled, *confirmed, func() error { called = true; return nil })
		require.True(t, handled)
		require.NoError(t, err)
		require.True(t, called)
	})
}

func openTOTPKeyExportDatabase(t *testing.T, rows *sqlmock.Rows) func(*config.Config) (*sql.DB, error) {
	t.Helper()
	return func(*config.Config) (*sql.DB, error) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		mock.ExpectQuery("SELECT value FROM security_secrets").WillReturnRows(rows)
		return db, nil
	}
}

func TestExecuteTOTPKeyExport(t *testing.T) {
	key := strings.Repeat("ab", 32)
	loadConfig := func() (*config.Config, error) { return &config.Config{}, nil }

	t.Run("prints key and migration steps", func(t *testing.T) {
		t.Setenv("TOTP_ENCRYPTION_KEY", "")
		out := &bytes.Buffer{}
		rows := sqlmock.NewRows([]string{"value"}).AddRow(key)
		verify := func(context.Context, *sql.DB, string) (int, int, error) { return 3, 0, nil }
		require.NoError(t, executeTOTPKeyExport(context.Background(), out, loadConfig, openTOTPKeyExportDatabase(t, rows), verify))
		text := out.String()
		require.Contains(t, text, "TOTP_ENCRYPTION_KEY="+key)
		require.Contains(t, text, "Verified 3 two-factor authenticator ciphertext(s)")
		require.Contains(t, text, "DELETE FROM security_secrets WHERE key = 'totp_encryption_key';")
		require.NotContains(t, text, "WARNING")
	})

	t.Run("warns about a conflicting environment key", func(t *testing.T) {
		t.Setenv("TOTP_ENCRYPTION_KEY", strings.Repeat("cd", 32))
		out := &bytes.Buffer{}
		rows := sqlmock.NewRows([]string{"value"}).AddRow(key)
		verify := func(context.Context, *sql.DB, string) (int, int, error) { return 1, 0, nil }
		require.NoError(t, executeTOTPKeyExport(context.Background(), out, loadConfig, openTOTPKeyExportDatabase(t, rows), verify))
		require.Contains(t, out.String(), "already sets a DIFFERENT TOTP_ENCRYPTION_KEY")
	})

	t.Run("reports failed ciphertexts without allowing row deletion", func(t *testing.T) {
		t.Setenv("TOTP_ENCRYPTION_KEY", "")
		out := &bytes.Buffer{}
		rows := sqlmock.NewRows([]string{"value"}).AddRow(key)
		verify := func(context.Context, *sql.DB, string) (int, int, error) { return 1, 2, nil }
		require.NoError(t, executeTOTPKeyExport(context.Background(), out, loadConfig, openTOTPKeyExportDatabase(t, rows), verify))
		require.Contains(t, out.String(), "2 could NOT be decrypted")
		require.Contains(t, out.String(), "do not delete the database row")
	})

	t.Run("missing row", func(t *testing.T) {
		out := &bytes.Buffer{}
		rows := sqlmock.NewRows([]string{"value"})
		verify := func(context.Context, *sql.DB, string) (int, int, error) { return 0, 0, nil }
		err := executeTOTPKeyExport(context.Background(), out, loadConfig, openTOTPKeyExportDatabase(t, rows), verify)
		require.ErrorIs(t, err, errTOTPKeyExportMissing)
	})

	t.Run("malformed stored key", func(t *testing.T) {
		out := &bytes.Buffer{}
		rows := sqlmock.NewRows([]string{"value"}).AddRow("not-hex")
		verify := func(context.Context, *sql.DB, string) (int, int, error) { return 0, 0, nil }
		err := executeTOTPKeyExport(context.Background(), out, loadConfig, openTOTPKeyExportDatabase(t, rows), verify)
		require.ErrorIs(t, err, errTOTPKeyExportInvalid)
	})

	t.Run("config failure", func(t *testing.T) {
		err := executeTOTPKeyExport(context.Background(), &bytes.Buffer{},
			func() (*config.Config, error) { return nil, errTOTPKeyExportConfig },
			func(*config.Config) (*sql.DB, error) { return nil, nil },
			func(context.Context, *sql.DB, string) (int, int, error) { return 0, 0, nil })
		require.ErrorIs(t, err, errTOTPKeyExportConfig)
	})
}
