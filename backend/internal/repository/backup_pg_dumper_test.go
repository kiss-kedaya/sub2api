package repository

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestSafeRestoreSQLReaderRejectsShellAndFileCommands(t *testing.T) {
	for _, input := range []string{
		`\! id` + "\n",
		`\shell id` + "\n",
		`\copy users to '/tmp/users.csv'` + "\n",
		`\include '/tmp/payload.sql'` + "\n",
	} {
		reader, done := safeRestoreSQLReader(strings.NewReader(input))
		_, readErr := io.ReadAll(reader)
		scanErr := <-done
		require.Error(t, readErr)
		require.Error(t, scanErr)
		require.Contains(t, scanErr.Error(), "not allowed")
	}
}

func TestSafeRestoreSQLReaderPreservesDumpControlAndCopyData(t *testing.T) {
	input := "COPY users FROM stdin;\n\\!\tuser@example.com\n\\.\n"
	reader, done := safeRestoreSQLReader(strings.NewReader(input))
	output, readErr := io.ReadAll(reader)
	require.NoError(t, readErr)
	require.NoError(t, <-done)
	require.Equal(t, input, string(output))
}

func TestSafeRestoreSQLReaderRejectsAllPSQLBypassVectors(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		// No-space meta-commands
		{"no-space bang", `\!id` + "\n"},
		{"no-space out pipe", `\o|id` + "\n"},
		{"no-space g pipe", `\g|id` + "\n"},
		{"no-space include", `\i/etc/passwd` + "\n"},
		{"no-space write", `\w/tmp/out` + "\n"},
		{"no-space connect", `\c postgres` + "\n"},
		// Inline meta-commands after SQL
		{"inline after semicolon", "SELECT 1; \\! id\n"},
		{"inline without semicolon", "SELECT 1 \\! id\n"},
		{"inline gexec", "SELECT 'SELECT 1'\\gexec\n"},
		{"inline g pipe", "SELECT 1\\g|id\n"},
		{"inline o pipe", "SELECT 1\\o|id\n"},
		{"inline gset", "SELECT 1 AS a\\gset\n"},
		{"inline gx", "SELECT 1\\gx\n"},
		{"inline watch", "SELECT 1\\watch 1\n"},
		{"inline after block comment", "SELECT 1 /* ok */ \\! id\n"},
		// Previously missing meta-commands (fail-closed)
		{"include short", `\i /etc/passwd` + "\n"},
		{"include relative", `\ir /etc/passwd` + "\n"},
		{"write file", `\w /tmp/x` + "\n"},
		{"edit buffer", `\e /tmp/x` + "\n"},
		{"save history", `\s /tmp/x` + "\n"},
		{"gset", `\gset` + "\n"},
		{"gexec", `\gexec` + "\n"},
		{"gx", `\gx` + "\n"},
		{"lo_import", `\lo_import /etc/passwd` + "\n"},
		{"lo_export", `\lo_export 1 /tmp/x` + "\n"},
		{"setenv", `\setenv PAGER id` + "\n"},
		{"set var", `\set foo bar` + "\n"},
		{"watch", `\watch 1` + "\n"},
		{"connect", `\connect postgres` + "\n"},
		{"cd", `\cd /tmp` + "\n"},
		{"quit", `\q` + "\n"},
		{"describe tables", `\dt` + "\n"},
		// Desync attempts via comments, quotes, dollar quotes, identifiers, escape strings
		{"copy inside block comment", "/* COPY t FROM stdin; */\n\\!id\n"},
		{"copy inside nested block comment", "/* outer /* COPY t FROM stdin; */ still comment */\n\\!id\n"},
		{"copy inside line comment", "-- COPY t FROM stdin;\n\\!id\n"},
		{"copy inside single quote", "SELECT 'COPY t FROM stdin;';\n\\!id\n"},
		{"copy inside dollar quote", "SELECT $$COPY t FROM stdin;$$;\n\\!id\n"},
		{"copy inside tagged dollar quote", "SELECT $body$COPY t FROM stdin;$body$;\n\\!id\n"},
		{"copy inside double quote", "SELECT \"COPY t FROM stdin;\";\n\\!id\n"},
		{"identifier with dollar not dollar quote", "SELECT a$b$; \\! id -- $b$\n"},
		{"escape string doubled backslash closes quote", "SELECT E'foo\\\\'; \\! id\n"},
		{"trailing metacommand on copy line", "COPY users FROM stdin; \\! id\n\\.\n"},
		{"trailing sql on copy line", "COPY users FROM stdin; SELECT 1;\n\\.\n"},
		{"unclosed block comment on copy line", "COPY users FROM stdin; /* unclosed\n\\! id\n"},
		{"copy missing semicolon", "COPY users FROM stdin\n\\! id\n\\.\n"},
		// Server-side COPY FROM/TO PROGRAM and file access
		{"copy from program", "COPY users FROM PROGRAM 'id';\n"},
		{"copy query to program", "COPY (SELECT 1) TO PROGRAM 'id';\n"},
		{"copy schema table from program", "COPY public.users (id) FROM PROGRAM 'curl evil';\n"},
		{"copy from file", "COPY users FROM '/etc/passwd';\n"},
		{"copy to file", "COPY users TO '/tmp/out';\n"},
		{"copy from stdin binary format", "COPY users FROM stdin WITH (FORMAT binary);\n"},
		// standard_conforming_strings lexer-differential bypass attempts
		{"set standard_conforming_strings off", "SET standard_conforming_strings = off;\nSELECT '\\''; \\! id\n';\n"},
		{"set session standard_conforming_strings off", "SET SESSION standard_conforming_strings TO 'off';\n"},
		{"set local standard_conforming_strings false", "SET LOCAL standard_conforming_strings = false;\n"},
		{"set_config standard_conforming_strings", "SELECT set_config('standard_conforming_strings', 'off', false);\n"},
		{"set_config concat obfuscation", "SELECT set_config('standard_' || 'conforming_strings', 'off', false);\n"},
		{"set_config escape obfuscation", "SELECT set_config(E'standard_conforming\\x5fstrings', 'off', false);\n"},
		{"reset standard_conforming_strings", "RESET standard_conforming_strings;\n"},
		{"reset all", "RESET ALL;\n"},
		{"alter database standard_conforming_strings", "ALTER DATABASE sub2api SET standard_conforming_strings = off;\n"},
		// \restrict / \unrestrict abuse
		{"restrict with trailing metacommand", "\\restrict abc123 \\! id\n"},
		{"inline restrict", "SELECT 1; \\restrict abc123\n"},
		{"unmatched unrestrict", "\\unrestrict abc123\n"},
		{"nested restrict", "\\restrict abc123\n\\restrict def456\n"},
		{"mismatched unrestrict key", "\\restrict abc123\n\\unrestrict wrongkey\n"},
		// NUL byte & BOM + metacommand
		{"nul byte in sql", "SELECT 1;\x00\\! id\n"},
		{"bom before metacommand", "\xef\xbb\xbf\\!id\n"},
		// Bare CR ending -- line comment in psqlscan.l ("--"[^\r\n]*)
		{"bare cr breaks line comment into metacommand", "-- harmless comment\r\\! id\n"},
		{"bare cr after copy line comment into metacommand", "COPY users FROM stdin; -- ok\r\\! id\n\\.\n"},
		// Numeric + e' lexer differential (psql lexes 0e/.5e/1.e/0x1e/1_0e as one numeric token, then plain quote!)
		{"numeric 0e before quote differential", "SELECT 0e'a\\' \\! id\n'\n"},
		{"numeric leading dot .5e differential", "SELECT .5e'a\\' \\! id\n'\n"},
		{"numeric trailing dot 1.e differential", "SELECT 1.e'a\\' \\! id\n'\n"},
		{"numeric hex 0x1e differential", "SELECT 0x1e'a\\' \\! id\n'\n"},
		{"numeric underscore 1_0e differential", "SELECT 1_0e'a\\' \\! id\n'\n"},
		{"range operator 1..e escape quote differential", "SELECT 1..e'\\'' \\! id\n'\n"},
		// Multi-line E'...' continuation (<xe>{quote}{quotecontinue}) differential
		{"multiline escape string continuation differential", "SELECT E'a'\n'\\'' \\! id\n'\n"},
		{"multiline escape string continuation after line comment", "SELECT E'a' -- comment\n'\\'' \\! id\n'\n"},
		// Unicode-escaped identifiers, DO blocks, DISCARD, and ambiguous \' inside plain strings
		{"unicode escaped SET standard_conforming_strings", "SET U&\"\\0073tandard_conforming_strings\" = off;\n"},
		{"unicode escaped set_config call", "SELECT U&\"\\0073et_config\"('standard_conforming_strings', 'off', false);\n"},
		{"unicode escaped set_config arg", "SELECT set_config(U&'\\0073tandard_conforming_strings', 'off', false);\n"},
		{"set_config oversized padded first arg", "SELECT set_config('" + strings.Repeat(" ", 70) + "standard_conforming_strings', 'off', false);\n"},
		{"anonymous DO block toggling guc", "DO $$ BEGIN PERFORM set_config('standard_conforming_strings', 'off', false); END $$;\n"},
		{"discard all resets session gucs", "DISCARD ALL;\n"},
		{"ambiguous backslash before quote in plain string", "SELECT 'a\\'; SELECT '; \\! id\n"},
		{"ambiguous backslash double quote in plain string", "SELECT '\\''; \\! id\n'\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader, done := safeRestoreSQLReader(strings.NewReader(tc.input))
			_, readErr := io.ReadAll(reader)
			scanErr := <-done
			require.Error(t, readErr, "input must fail reader")
			require.Error(t, scanErr, "input must fail scanner")
			require.Contains(t, scanErr.Error(), "not allowed")
		})
	}
}

func TestSafeRestoreSQLReaderAllowsLegitimatePgDump(t *testing.T) {
	dump := strings.Join([]string{
		"--",
		"-- PostgreSQL database dump",
		"--",
		"",
		`\restrict AbCdEf0123456789xyz`,
		"SET statement_timeout = 0;",
		"SET standard_conforming_strings = on;",
		"SELECT pg_catalog.set_config('search_path', '', false);",
		"/* outer /* nested \\! ignored */ comment */",
		"-- line comment with \\! ignored",
		`CREATE TABLE public.users (id integer, email text, "from" text);`,
		"CREATE FUNCTION public.greet() RETURNS text LANGUAGE sql AS $$",
		"  SELECT 'it''s \\! fine' || E'escaped \\' and \\\\ ok';",
		"  -- COPY fake FROM stdin; inside dollar quote",
		"$$;",
		"CREATE FUNCTION public.tagged() RETURNS text LANGUAGE sql AS $fn$",
		"  SELECT '\\gexec';",
		"$fn$;",
		`COPY public.users (id, email, "from") FROM stdin;`,
		"1\t\\N\t\\!not-a-command",
		"2\tuser@example.com\tline1\\\\line2",
		" \\.",
		`\.`,
		"SELECT pg_catalog.setval('public.users_id_seq', 2, true);",
		"COPY public.logs (id, msg) FROM stdin;\r",
		"1\thello\\N\r",
		"\\.\r",
		"COPY public.set_config (id) FROM stdin;",
		"1",
		`\.`,
		`\unrestrict AbCdEf0123456789xyz`,
		"",
	}, "\n")

	reader, done := safeRestoreSQLReader(strings.NewReader(dump))
	output, readErr := io.ReadAll(reader)
	require.NoError(t, readErr)
	require.NoError(t, <-done)
	require.Equal(t, dump, string(output))

	// UTF-8 BOM must be stripped identically from output forwarded to psql so
	// scanner and psql cannot diverge on BOM-prefixed tokens (e.g. BOM + E'...').
	bomInput := "\xef\xbb\xbfSELECT 'ok\\\\';\n"
	bomReader, bomDone := safeRestoreSQLReader(strings.NewReader(bomInput))
	bomOut, bomReadErr := io.ReadAll(bomReader)
	require.NoError(t, bomReadErr)
	require.NoError(t, <-bomDone)
	require.Equal(t, "SELECT 'ok\\\\';\n", string(bomOut))
}

func TestPgDumperRestoreRejectsUnsafeSQLWithoutDeadlock(t *testing.T) {
	dumper, _ := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		// Exits immediately without reading stdin to also verify no pipe deadlock.
		return pgDumperHelperCommand(ctx, "failure")
	})

	// Safe SQL + child exits early => must report child failure without hanging.
	err := dumper.Restore(context.Background(), strings.NewReader("SELECT 1;\n"))
	require.Error(t, err)
	require.NotContains(t, err.Error(), "unsafe backup SQL")

	// Unsafe SQL => must reject with unsafe backup SQL.
	dumper2, _ := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return pgDumperHelperCommand(ctx, "empty")
	})
	err = dumper2.Restore(context.Background(), strings.NewReader("SELECT 1; \\! id\n"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsafe backup SQL")
}

func TestPgDumperRestoreKillsPsqlBeforeEOFOnRejection(t *testing.T) {
	runWithCommitMarker := func(t *testing.T, input string) (error, bool) {
		t.Helper()
		marker := filepath.Join(t.TempDir(), "committed.txt")
		dumper, _ := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			cmd := pgDumperHelperCommand(ctx, "restore-commit-on-eof")
			cmd.Env = append(cmd.Env, "SUB2API_PG_DUMP_COMMIT_MARKER="+marker)
			return cmd
		})
		err := dumper.Restore(context.Background(), strings.NewReader(input))
		_, statErr := os.Stat(marker)
		return err, statErr == nil
	}

	// Clean SQL reaches EOF and commits.
	err, committed := runWithCommitMarker(t, "SELECT 1;\n")
	require.NoError(t, err)
	require.True(t, committed, "valid restore stream must reach EOF and commit")

	// Rejected mid-stream metacommand must kill child before stdin EOF so --single-transaction never commits.
	err, committed = runWithCommitMarker(t, "SELECT 1;\n\\! id\n")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsafe backup SQL")
	require.False(t, committed, "rejected mid-stream restore must never let psql observe EOF and commit")

	// Rejected EOF finalization (forbidden statement without trailing semicolon) must also kill child before stdin EOF.
	err, committed = runWithCommitMarker(t, "SELECT 1;\nDISCARD ALL\n")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsafe backup SQL")
	require.False(t, committed, "rejected EOF-finalized restore must never let psql observe EOF and commit")
}

func TestPgDumperHelperProcess(t *testing.T) {
	mode := os.Getenv("SUB2API_PG_DUMP_TEST_HELPER")
	if mode == "" {
		return
	}
	switch mode {
	case "success":
		_, _ = io.WriteString(os.Stdout, "backup-data")
	case "failure":
		_, _ = io.WriteString(os.Stdout, "partial-backup")
		os.Exit(7)
	case "empty":
	case "restore-commit-on-eof":
		_, _ = io.Copy(io.Discard, os.Stdin)
		if marker := os.Getenv("SUB2API_PG_DUMP_COMMIT_MARKER"); marker != "" {
			_ = os.WriteFile(marker, []byte("COMMITTED"), 0o600)
		}
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func pgDumperHelperCommand(ctx context.Context, mode string) *exec.Cmd {
	executable, err := os.Executable()
	if err != nil {
		panic(err)
	}
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestPgDumperHelperProcess$")
	cmd.Env = append(os.Environ(), "SUB2API_PG_DUMP_TEST_HELPER="+mode)
	return cmd
}

func newTestPgDumper(t *testing.T, commandContext func(context.Context, string, ...string) *exec.Cmd) (*PgDumper, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &PgDumper{
		cfg: &config.DatabaseConfig{
			Host:     "db.example.test",
			Port:     5432,
			User:     "sub2api",
			Password: "secret",
			DBName:   "sub2api",
			SSLMode:  "require",
		},
		db:             db,
		commandContext: commandContext,
	}, mock
}

func expectBackupMigrationLock(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_lock($1)")).
		WithArgs(migrationsAdvisoryLockID).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
}

func expectBackupMigrationUnlock(mock sqlmock.Sqlmock) {
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).
		WithArgs(migrationsAdvisoryLockID).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestPgDumperHoldsMigrationLockThroughReaderClose(t *testing.T) {
	var mock sqlmock.Sqlmock
	commandCreated := false
	dumper, createdMock := newTestPgDumper(t, func(ctx context.Context, name string, args ...string) *exec.Cmd {
		commandCreated = true
		require.Equal(t, "pg_dump", name)
		require.Contains(t, args, "--clean")
		require.NoError(t, mock.ExpectationsWereMet(), "migration lock must be acquired before pg_dump is created")
		return pgDumperHelperCommand(ctx, "success")
	})
	mock = createdMock
	expectBackupMigrationLock(mock)

	reader, err := dumper.Dump(context.Background())
	require.NoError(t, err)
	require.True(t, commandCreated)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, "backup-data", string(data))

	expectBackupMigrationUnlock(mock)
	require.Error(t, mock.ExpectationsWereMet(), "migration lock was released before the reader closed")
	require.NoError(t, reader.Close())
	require.NoError(t, mock.ExpectationsWereMet())
	require.NoError(t, reader.Close(), "reader close must be idempotent")
}

func TestPgDumperReleasesMigrationLockWhenStdoutPipeSetupFails(t *testing.T) {
	dumper, mock := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		cmd := pgDumperHelperCommand(ctx, "empty")
		cmd.Stdout = io.Discard
		return cmd
	})
	expectBackupMigrationLock(mock)
	expectBackupMigrationUnlock(mock)

	reader, err := dumper.Dump(context.Background())
	require.Nil(t, reader)
	require.ErrorContains(t, err, "create stdout pipe")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgDumperReleasesMigrationLockWhenProcessStartFails(t *testing.T) {
	dumper, mock := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "/path/that/does/not/exist/pg_dump")
	})
	expectBackupMigrationLock(mock)
	expectBackupMigrationUnlock(mock)

	reader, err := dumper.Dump(context.Background())
	require.Nil(t, reader)
	require.ErrorContains(t, err, "start pg_dump")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgDumperReleasesMigrationLockWhenProcessFails(t *testing.T) {
	dumper, mock := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return pgDumperHelperCommand(ctx, "failure")
	})
	expectBackupMigrationLock(mock)

	reader, err := dumper.Dump(context.Background())
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, "partial-backup", string(data))
	expectBackupMigrationUnlock(mock)
	require.ErrorContains(t, reader.Close(), "pg_dump exited with error")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgDumperReportsUnlockFailureAndDiscardsConnection(t *testing.T) {
	dumper, mock := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return pgDumperHelperCommand(ctx, "success")
	})
	expectBackupMigrationLock(mock)

	reader, err := dumper.Dump(context.Background())
	require.NoError(t, err)
	_, err = io.ReadAll(reader)
	require.NoError(t, err)
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).
		WithArgs(migrationsAdvisoryLockID).
		WillReturnError(errors.New("unlock unavailable"))
	require.ErrorContains(t, reader.Close(), "release backup migration lock")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgDumperDoesNotStartProcessWhenMigrationLockFails(t *testing.T) {
	commandCreated := false
	dumper, mock := newTestPgDumper(t, func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		commandCreated = true
		return pgDumperHelperCommand(ctx, "empty")
	})
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_lock($1)")).
		WithArgs(migrationsAdvisoryLockID).
		WillReturnError(errors.New("database unavailable"))

	reader, err := dumper.Dump(context.Background())
	require.Nil(t, reader)
	require.ErrorContains(t, err, "acquire backup migration lock")
	require.False(t, commandCreated)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgDumperRejectsNilDatabase(t *testing.T) {
	dumper := &PgDumper{cfg: &config.DatabaseConfig{}}
	reader, err := dumper.Dump(context.Background())
	require.Nil(t, reader)
	require.ErrorContains(t, err, "nil sql db")
}
