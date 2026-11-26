//go:build unit

package repository

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// ─── helper process: a pg_dump that fails in a chosen way ───

const (
	stderrHelperEnv       = "SUB2API_PG_DUMP_STDERR_HELPER"
	stderrHelperSecretEnv = "SUB2API_PG_DUMP_STDERR_SECRET"
)

func TestPgDumperStderrHelperProcess(t *testing.T) {
	mode := os.Getenv(stderrHelperEnv)
	if mode == "" {
		return
	}
	secret := os.Getenv(stderrHelperSecretEnv)
	switch mode {
	case "lock-timeout":
		fmt.Fprintln(os.Stderr, "pg_dump: error: query failed: ERROR:  canceling statement due to lock timeout")
		fmt.Fprintln(os.Stderr, "pg_dump: detail: Query was: LOCK TABLE public.example_table IN ACCESS SHARE MODE")
		os.Exit(1)
	case "flood":
		for i := 0; i < 400; i++ {
			fmt.Fprintf(os.Stderr, "filler line %04d %s\n", i, strings.Repeat("x", 40))
		}
		fmt.Fprintln(os.Stderr, "pg_dump: error: the final line")
		os.Exit(1)
	case "leaky":
		fmt.Fprintln(os.Stderr, "pg_dump: error: connection to server failed: FATAL:  password authentication failed for user \"app\"")
		fmt.Fprintf(os.Stderr, "pg_dump: detail: dsn postgresql://app:%s@db.example.test:5432/app?sslmode=require\n", secret)
		fmt.Fprintf(os.Stderr, "pg_dump: detail: %s=%s host=db.example.test\n", "password", secret)
		fmt.Fprintf(os.Stderr, "pg_dump: detail: the bare value %s must go too\n", secret)
		os.Exit(1)
	case "warns-and-succeeds":
		fmt.Fprintln(os.Stderr, "pg_dump: warning: something harmless")
		_, _ = io.WriteString(os.Stdout, "backup-data")
	}
	os.Exit(0)
}

func stderrHelperCommand(ctx context.Context, mode, secret string) *exec.Cmd {
	executable, err := os.Executable()
	if err != nil {
		panic(err)
	}
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestPgDumperStderrHelperProcess$")
	cmd.Env = append(os.Environ(), stderrHelperEnv+"="+mode, stderrHelperSecretEnv+"="+secret)
	return cmd
}

// fakePassword is built at run time and looks like a passphrase, not like a key.
func fakePassword() string { return strings.Join([]string{"correct", "horse", "battery"}, "-") }

// dumpAndClose runs a full Dump -> read -> Close cycle against the helper and
// returns what Close reported.
func dumpAndClose(t *testing.T, mode string) (data string, closeErr error) {
	t.Helper()
	password := fakePassword()
	dumper, mock := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return stderrHelperCommand(ctx, mode, password)
	})
	dumper.cfg.Password = password
	expectBackupMigrationLock(mock)
	expectBackupMigrationUnlock(mock)

	reader, err := dumper.Dump(context.Background())
	require.NoError(t, err)
	out, err := io.ReadAll(reader)
	require.NoError(t, err)
	closeErr = reader.Close()
	require.NoError(t, mock.ExpectationsWereMet())
	return string(out), closeErr
}

// ─── stderr tail ───

func TestPgDumperCloseErrorCarriesTheStderrTail(t *testing.T) {
	_, err := dumpAndClose(t, "lock-timeout")

	require.ErrorContains(t, err, "pg_dump exited with error: exit status 1")
	require.ErrorContains(t, err, "canceling statement due to lock timeout")
	require.ErrorContains(t, err, "LOCK TABLE public.example_table")
	require.NotContains(t, err.Error(), "\n", "one line, so it reads well in the backup record")
}

func TestPgDumperStderrTailIsBounded(t *testing.T) {
	_, err := dumpAndClose(t, "flood")

	require.ErrorContains(t, err, "the final line", "the end of stderr is what is kept")
	require.NotContains(t, err.Error(), "filler line 0000", "the beginning is dropped")
	require.Less(t, len(err.Error()), pgDumpStderrTailBytes+512)
}

func TestPgDumperStderrTailNeverLeaksTheDatabasePassword(t *testing.T) {
	_, err := dumpAndClose(t, "leaky")

	require.ErrorContains(t, err, "pg_dump exited with error")
	require.ErrorContains(t, err, "password authentication failed", "the diagnosis itself stays readable")
	require.NotContains(t, err.Error(), fakePassword(), "the configured password must never reach the error")
	require.NotContains(t, err.Error(), "postgresql://app", "no connection URI")
	require.NotContains(t, err.Error(), "example.test:5432/app", "no connection URI")
}

func TestPgDumperSuccessIgnoresStderrNoise(t *testing.T) {
	data, err := dumpAndClose(t, "warns-and-succeeds")

	require.NoError(t, err, "warnings on stderr are not a failure")
	require.Equal(t, "backup-data", data)
}

func TestTailBufferKeepsTheLastBytes(t *testing.T) {
	tests := []struct {
		name   string
		writes []string
		want   string
	}{
		{name: "fits", writes: []string{"abc", "de"}, want: "abcde"},
		{name: "exact", writes: []string{"abcdefgh"}, want: "abcdefgh"},
		{name: "one write bigger than the buffer", writes: []string{"0123456789abcdefghij"}, want: "cdefghij"},
		{name: "trims the oldest across writes", writes: []string{"abcdef", "ghijk"}, want: "defghijk"},
		{name: "many small writes", writes: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}, want: "cdefghij"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := &tailBuffer{max: 8}
			for _, w := range tt.writes {
				n, err := buf.Write([]byte(w))
				require.NoError(t, err)
				require.Equal(t, len(w), n)
			}
			require.Equal(t, tt.want, string(buf.buf))
		})
	}
}

func TestPgDumpFailureDetail(t *testing.T) {
	pw := fakePassword()
	tests := []struct {
		name     string
		raw      string
		password string
		want     string
	}{
		{name: "empty", raw: "", want: ""},
		{name: "lines are joined", raw: "line one\r\n\r\nline two\n", want: "line one | line two"},
		{name: "configured password", raw: "bad value " + pw + " here", password: pw, want: "bad value *** here"},
		{name: "uri", raw: "see postgresql://user:x@host:5432/db?a=1 and postgres://u@h/d for more", want: "see postgres://*** and postgres://*** for more"},
		{name: "key value pair", raw: "password = 'two words' next", want: "password=*** next"},
		{name: "key value pair, upper case, unquoted", raw: "PASSWORD=abc host=h", want: "PASSWORD=*** host=h"},
		{name: "ssl password, double quoted", raw: `sslpassword="q x" ok`, want: "sslpassword=*** ok"},
		{name: "plain diagnosis is untouched", raw: "FATAL:  password authentication failed for user \"app\"", want: "FATAL:  password authentication failed for user \"app\""},
		{name: "control characters", raw: "a\x1b[31mred\x1b[0m\tb", want: "a[31mred[0m b"},
		{name: "invalid utf-8", raw: "ok \xff\xfe bytes", want: "ok  bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, pgDumpFailureDetail([]byte(tt.raw), tt.password))
		})
	}
}

// ─── executable pre-check ───

var errNotFound = errors.New("no such file or directory")

func TestPgDumperReportsAMissingPgDumpBeforeLockingOrStarting(t *testing.T) {
	factoryCalled := false
	dumper, mock := newTestPgDumper(t, func(context.Context, string, ...string) *exec.Cmd {
		factoryCalled = true
		return nil
	})
	dumper.backup = config.BackupConfig{PgDumpPath: "/opt/missing/bin/pg_dump", ExcludeTableData: []string{"public.example_table"}}
	dumper.niceAvailable = always(true)
	dumper.lookPath = func(name string) (string, error) {
		if name == "nice" {
			return "/usr/bin/nice", nil
		}
		return "", errNotFound
	}

	reader, err := dumper.Dump(context.Background())

	require.Nil(t, reader)
	require.ErrorContains(t, err, `pg_dump executable "/opt/missing/bin/pg_dump" cannot be run`)
	require.ErrorContains(t, err, "backup.pg_dump_path")
	require.ErrorIs(t, err, errNotFound)
	require.NotContains(t, err.Error(), "127", "not nice's exit status")
	require.False(t, factoryCalled, "no process may be created")
	require.NoError(t, mock.ExpectationsWereMet(), "no lock query was expected, so none may have run")
}

func TestPgDumperReportsAMissingNice(t *testing.T) {
	dumper, _ := newTestPgDumper(t, func(context.Context, string, ...string) *exec.Cmd { return nil })
	dumper.backup = config.BackupConfig{ExcludeTableData: []string{"public.example_table"}}
	dumper.niceAvailable = always(true)
	dumper.lookPath = func(name string) (string, error) {
		if name == "nice" {
			return "", errNotFound
		}
		return "/usr/bin/" + name, nil
	}

	reader, err := dumper.Dump(context.Background())

	require.Nil(t, reader)
	require.ErrorContains(t, err, "nice is not available")
	require.ErrorIs(t, err, errNotFound)
}

func TestPgDumperChecksOnlyPgDumpWhenNiceIsNotUsed(t *testing.T) {
	dumper, _ := newTestPgDumper(t, func(context.Context, string, ...string) *exec.Cmd { return nil })
	dumper.backup = config.BackupConfig{PgDumpPath: "/opt/missing/bin/pg_dump"} // whole-database dump
	var asked []string
	dumper.lookPath = func(name string) (string, error) {
		asked = append(asked, name)
		return "", errNotFound
	}

	_, err := dumper.Dump(context.Background())

	require.ErrorContains(t, err, "/opt/missing/bin/pg_dump")
	require.Equal(t, []string{"/opt/missing/bin/pg_dump"}, asked)
}

func TestPgDumperRunsNormallyWhenTheExecutablesAreFound(t *testing.T) {
	dumper, mock := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return pgDumperHelperCommand(ctx, "success")
	})
	dumper.lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	expectBackupMigrationLock(mock)
	expectBackupMigrationUnlock(mock)

	reader, err := dumper.Dump(context.Background())
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, "backup-data", string(data))
	require.NoError(t, reader.Close())
	require.NoError(t, mock.ExpectationsWereMet())
}

// ─── NewPgDumper wiring ───

func newWiringConfig(backup config.BackupConfig) *config.Config {
	return &config.Config{
		Database: config.DatabaseConfig{Host: "db.example.test", Port: 5432, User: "sub2api", DBName: "sub2api"},
		Backup:   backup,
	}
}

func (d *PgDumper) pgDumpCommand() (string, []string) {
	return d.pgDumpCommandForExcludes(d.backup.ExcludeTableData)
}

// The only hand-off from the configuration to pg_dump's command line is the
// backup field NewPgDumper copies; every other test builds a PgDumper literal.
func TestNewPgDumperPassesTheBackupConfigToTheCommand(t *testing.T) {
	dumper, ok := NewPgDumper(newWiringConfig(config.BackupConfig{
		PgDumpPath:       "/opt/pg/bin/pg_dump",
		ExcludeTableData: []string{"public.example_log_table"},
	}), nil).(*PgDumper)
	require.True(t, ok)
	dumper.niceAvailable = always(false)

	name, args := dumper.pgDumpCommand()

	require.Equal(t, "/opt/pg/bin/pg_dump", name)
	require.Contains(t, args, "--exclude-table-data=public.example_log_table")
	require.Contains(t, args, "--lock-wait-timeout="+pgDumpLockWaitTimeout)
	require.NotContains(t, args, "--clean")
	require.NotContains(t, args, "--if-exists")
}

func TestNewPgDumperWithoutBackupSettingsKeepsTheLegacyCommand(t *testing.T) {
	dumper, ok := NewPgDumper(newWiringConfig(config.BackupConfig{}), nil).(*PgDumper)
	require.True(t, ok)

	name, args := dumper.pgDumpCommand()

	require.Equal(t, "pg_dump", name)
	require.Equal(t, legacyPgDumpArgs, args)
}

// A wrong pg_dump_path is reported by the constructor-built dumper before the
// migration lock is taken; the pre-check is wired in NewPgDumper, not only in tests.
func TestNewPgDumperChecksTheExecutableBeforeLocking(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	missing := filepath.Join(t.TempDir(), "no-such-pg_dump")
	dumper := NewPgDumper(newWiringConfig(config.BackupConfig{PgDumpPath: missing}), db)

	reader, err := dumper.Dump(context.Background())

	require.Nil(t, reader)
	require.ErrorContains(t, err, "cannot be run")
	require.ErrorContains(t, err, "no-such-pg_dump")
	require.NoError(t, mock.ExpectationsWereMet(), "no query was expected: nothing may be locked")
}
