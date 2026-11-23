//go:build unit

package config

import (
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/stretchr/testify/require"
)

// newTestAgeRecipient generates a throw-away age identity so no key material is
// ever committed. It returns the public key and the matching private key.
func newTestAgeRecipient(t *testing.T) (public, private string) {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	return identity.Recipient().String(), identity.String()
}

func TestLoadBackupConfigDefaultsKeepLegacyBehavior(t *testing.T) {
	resetViperWithJWTSecret(t)

	cfg, err := Load()
	require.NoError(t, err)

	require.Empty(t, cfg.Backup.PgDumpPath)
	require.Empty(t, cfg.Backup.ExcludeTableData)
	require.Empty(t, cfg.Backup.AgeRecipients)
	require.Zero(t, cfg.Backup.MaxArchiveBytes)
}

// TestLoadBackupConfigFromEnv guards the viper trap documented above
// setEnvReachableDefaults: a key with no registered default never receives its
// BACKUP_* environment variable, so the feature would silently stay off.
func TestLoadBackupConfigFromEnv(t *testing.T) {
	publicA, _ := newTestAgeRecipient(t)
	publicB, _ := newTestAgeRecipient(t)

	resetViperWithJWTSecret(t)
	t.Setenv("BACKUP_PG_DUMP_PATH", "  /opt/pg/bin/pg_dump  ")
	t.Setenv("BACKUP_EXCLUDE_TABLE_DATA", " public.table_a , table_b,, public.table_c ")
	t.Setenv("BACKUP_AGE_RECIPIENTS", " "+publicA+" ,"+publicB+",")
	t.Setenv("BACKUP_MAX_ARCHIVE_BYTES", "3221225472")

	cfg, err := Load()
	require.NoError(t, err)

	require.Equal(t, "/opt/pg/bin/pg_dump", cfg.Backup.PgDumpPath)
	require.Equal(t, []string{"public.table_a", "table_b", "public.table_c"}, cfg.Backup.ExcludeTableData)
	require.Equal(t, []string{publicA, publicB}, cfg.Backup.AgeRecipients)
	require.Equal(t, int64(3221225472), cfg.Backup.MaxArchiveBytes)
}

func TestLoadBackupConfigSingleEntryFromEnv(t *testing.T) {
	public, _ := newTestAgeRecipient(t)

	resetViperWithJWTSecret(t)
	t.Setenv("BACKUP_EXCLUDE_TABLE_DATA", "public.only_one")
	t.Setenv("BACKUP_AGE_RECIPIENTS", public)

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, []string{"public.only_one"}, cfg.Backup.ExcludeTableData)
	require.Equal(t, []string{public}, cfg.Backup.AgeRecipients)
}

func TestLoadBackupConfigEmptyEnvMeansOff(t *testing.T) {
	for name, value := range map[string]string{
		"empty":           "",
		"spaces":          "   ",
		"only separators": " , ,, ",
	} {
		t.Run(name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			t.Setenv("BACKUP_EXCLUDE_TABLE_DATA", value)
			t.Setenv("BACKUP_AGE_RECIPIENTS", value)

			cfg, err := Load()
			require.NoError(t, err)
			require.Empty(t, cfg.Backup.ExcludeTableData)
			require.Empty(t, cfg.Backup.AgeRecipients)
		})
	}

	resetViperWithJWTSecret(t)
	t.Setenv("BACKUP_PG_DUMP_PATH", "   ")
	cfg, err := Load()
	require.NoError(t, err)
	require.Empty(t, cfg.Backup.PgDumpPath)
}

func TestLoadRejectsInvalidBackupConfigFromEnv(t *testing.T) {
	_, private := newTestAgeRecipient(t)

	tests := []struct {
		name    string
		env     map[string]string
		wantKey string
		secret  string // text that must never be echoed in the error
	}{
		{
			name:    "table name with dash",
			env:     map[string]string{"BACKUP_EXCLUDE_TABLE_DATA": "public.table-a"},
			wantKey: "backup.exclude_table_data[0]",
			secret:  "table-a",
		},
		{
			name:    "second entry carries a wildcard",
			env:     map[string]string{"BACKUP_EXCLUDE_TABLE_DATA": "public.ok,public.*"},
			wantKey: "backup.exclude_table_data[1]",
			secret:  "public.*",
		},
		{
			name:    "age private key instead of public key",
			env:     map[string]string{"BACKUP_AGE_RECIPIENTS": private},
			wantKey: "backup.age_recipients[0]",
			secret:  private,
		},
		{
			name:    "age recipient garbage",
			env:     map[string]string{"BACKUP_AGE_RECIPIENTS": "age1notarealkey"},
			wantKey: "backup.age_recipients[0]",
			secret:  "notarealkey",
		},
		{
			name:    "negative size limit",
			env:     map[string]string{"BACKUP_MAX_ARCHIVE_BYTES": "-1"},
			wantKey: "backup.max_archive_bytes",
		},
		{
			name:    "pg_dump path that nice would parse as an option",
			env:     map[string]string{"BACKUP_PG_DUMP_PATH": "-n"},
			wantKey: "backup.pg_dump_path",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			cfg, err := Load()
			require.Nil(t, cfg)
			require.ErrorContains(t, err, tt.wantKey)
			if tt.secret != "" {
				require.NotContains(t, err.Error(), tt.secret, "the offending value must not be echoed")
			}
		})
	}
}

func TestValidateBackupExcludeTablePattern(t *testing.T) {
	valid := []string{"t", "public.t", "_t1", "Schema_1.Table_2", "a.b"}
	invalid := []string{
		"public.table-a",  // dash
		"public.*",        // wildcard
		"pub*.t",          // wildcard in schema
		"public.t?",       // wildcard
		"a b",             // space
		"public.a.b",      // three parts
		"1table",          // leading digit
		"public.1table",   // leading digit after dot
		"--exclude",       // looks like an option
		"-t",              // looks like an option
		"public.",         // empty table
		".t",              // empty schema
		`public."t"`,      // quoting
		"public.t;drop",   // statement separator
		"public.t\nother", // newline
	}

	for _, value := range valid {
		t.Run("valid "+value, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			cfg, err := Load()
			require.NoError(t, err)
			cfg.Backup.ExcludeTableData = []string{value}
			require.NoError(t, cfg.Validate())
		})
	}
	for _, value := range invalid {
		t.Run("invalid "+strings.ReplaceAll(value, "\n", `\n`), func(t *testing.T) {
			resetViperWithJWTSecret(t)
			cfg, err := Load()
			require.NoError(t, err)
			cfg.Backup.ExcludeTableData = []string{value}
			require.ErrorContains(t, cfg.Validate(), "backup.exclude_table_data[0]")
		})
	}
}

func TestValidateBackupNormalizesAndAcceptsLimit(t *testing.T) {
	public, _ := newTestAgeRecipient(t)

	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)
	cfg.Backup = BackupConfig{
		PgDumpPath:       "  /usr/lib/postgresql/17/bin/pg_dump ",
		ExcludeTableData: []string{"  public.a ", "", "b"},
		AgeRecipients:    []string{" " + public + " ", ""},
		MaxArchiveBytes:  0,
	}

	require.NoError(t, cfg.Validate())
	require.Equal(t, "/usr/lib/postgresql/17/bin/pg_dump", cfg.Backup.PgDumpPath)
	require.Equal(t, []string{"public.a", "b"}, cfg.Backup.ExcludeTableData)
	require.Equal(t, []string{public}, cfg.Backup.AgeRecipients)

	cfg.Backup.MaxArchiveBytes = 1
	require.NoError(t, cfg.Validate())
	cfg.Backup.MaxArchiveBytes = -1
	require.ErrorContains(t, cfg.Validate(), "backup.max_archive_bytes")
}
