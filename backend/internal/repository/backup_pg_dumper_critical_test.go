//go:build unit

package repository

import (
	"context"
	"io"
	"os/exec"
	"runtime"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// capturedPgDump is what PgDumper handed to its command factory.
type capturedPgDump struct {
	name string
	args []string
	cmd  *exec.Cmd
}

// runDumpCapturing runs PgDumper.Dump with the given backup settings and a
// forced answer for "is nice available", returning the command it built. The
// migration lock round trip is mocked exactly as in the neighbouring tests.
func runDumpCapturing(t *testing.T, backup config.BackupConfig, niceAvailable func() bool) capturedPgDump {
	t.Helper()
	var captured capturedPgDump
	dumper, mock := newTestPgDumper(t, func(ctx context.Context, name string, args ...string) *exec.Cmd {
		captured.name = name
		captured.args = append([]string(nil), args...)
		captured.cmd = pgDumperHelperCommand(ctx, "success")
		return captured.cmd
	})
	dumper.backup = backup
	dumper.niceAvailable = niceAvailable
	expectBackupMigrationLock(mock)
	expectBackupMigrationUnlock(mock)

	reader, err := dumper.Dump(context.Background())
	require.NoError(t, err)
	_, err = io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.NoError(t, mock.ExpectationsWereMet())
	return captured
}

func always(v bool) func() bool { return func() bool { return v } }

// legacyPgDumpArgs is the argument list the dumper produced before partial
// snapshots existed. It is spelled out literally so any drift fails loudly.
var legacyPgDumpArgs = []string{
	"-h", "db.example.test",
	"-p", "5432",
	"-U", "sub2api",
	"-d", "sub2api",
	"--no-owner",
	"--no-acl",
	"--clean",
	"--if-exists",
}

func TestPgDumperLegacyCommandIsUnchanged(t *testing.T) {
	// nice being available must not matter: the whole-database dump never uses it.
	for _, nice := range []bool{false, true} {
		got := runDumpCapturing(t, config.BackupConfig{}, always(nice))
		require.Equal(t, "pg_dump", got.name)
		require.Equal(t, legacyPgDumpArgs, got.args)
	}
}

func TestPgDumperLegacyKeepsEnvironment(t *testing.T) {
	got := runDumpCapturing(t, config.BackupConfig{}, always(true))
	require.Contains(t, got.cmd.Env, "PGPASSWORD=secret")
	require.Contains(t, got.cmd.Env, "PGSSLMODE=require")
}

func TestPgDumperPathOverrideAppliesToLegacyDump(t *testing.T) {
	got := runDumpCapturing(t, config.BackupConfig{PgDumpPath: "/opt/pg/bin/pg_dump"}, always(true))
	require.Equal(t, "/opt/pg/bin/pg_dump", got.name)
	require.Equal(t, legacyPgDumpArgs, got.args)
}

func TestPgDumperPartialCommandExcludesTableDataWithoutClean(t *testing.T) {
	got := runDumpCapturing(t, config.BackupConfig{
		ExcludeTableData: []string{"public.table_a", "table_b"},
	}, always(false))

	require.Equal(t, "pg_dump", got.name)
	require.Equal(t, []string{
		"-h", "db.example.test",
		"-p", "5432",
		"-U", "sub2api",
		"-d", "sub2api",
		"--no-owner",
		"--no-acl",
		"--exclude-table-data=public.table_a",
		"--exclude-table-data=table_b",
		"--lock-wait-timeout=30s",
	}, got.args)
	require.NotContains(t, got.args, "--clean")
	require.NotContains(t, got.args, "--if-exists")
}

func TestPgDumperPartialCommandRunsUnderNiceWhenAvailable(t *testing.T) {
	got := runDumpCapturing(t, config.BackupConfig{
		PgDumpPath:       "/opt/pg/bin/pg_dump",
		ExcludeTableData: []string{"public.table_a"},
	}, always(true))

	require.Equal(t, "nice", got.name)
	require.Equal(t, []string{
		"-n", "19", "/opt/pg/bin/pg_dump",
		"-h", "db.example.test",
		"-p", "5432",
		"-U", "sub2api",
		"-d", "sub2api",
		"--no-owner",
		"--no-acl",
		"--exclude-table-data=public.table_a",
		"--lock-wait-timeout=30s",
	}, got.args)
}

func TestPgDumperPartialCommandFallsBackWithoutNice(t *testing.T) {
	got := runDumpCapturing(t, config.BackupConfig{
		PgDumpPath:       "/opt/pg/bin/pg_dump",
		ExcludeTableData: []string{"public.table_a"},
	}, always(false))

	require.Equal(t, "/opt/pg/bin/pg_dump", got.name)
	require.Equal(t, "-h", got.args[0])
	require.NotContains(t, got.args, "nice")
}

func TestPgDumperPartialKeepsEnvironmentAndLockSemantics(t *testing.T) {
	// runDumpCapturing already asserts the advisory lock is taken before the
	// command is built and released on Close; here we pin the credentials.
	got := runDumpCapturing(t, config.BackupConfig{ExcludeTableData: []string{"t"}}, always(true))
	require.Contains(t, got.cmd.Env, "PGPASSWORD=secret")
	require.Contains(t, got.cmd.Env, "PGSSLMODE=require")
}

func TestPgDumperNiceDetectionDefaultsToLinuxWithNiceOnPath(t *testing.T) {
	_, err := exec.LookPath("nice")
	want := runtime.GOOS == "linux" && err == nil
	require.Equal(t, want, (&PgDumper{}).useNice())
}
