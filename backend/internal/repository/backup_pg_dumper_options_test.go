//go:build unit

package repository

import (
	"context"
	"io"
	"os/exec"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func runDumpWithOptionsCapturing(t *testing.T, backup config.BackupConfig, opts service.BackupDumpOptions, niceAvailable func() bool) capturedPgDump {
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

	reader, err := dumper.DumpWithOptions(context.Background(), opts)
	require.NoError(t, err)
	_, err = io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.NoError(t, mock.ExpectationsWereMet())
	return captured
}

func TestNewPgDumperImplementsScopedDBDumper(t *testing.T) {
	dumper := NewPgDumper(newWiringConfig(config.BackupConfig{}), nil)
	_, ok := dumper.(service.ScopedDBDumper)
	require.True(t, ok, "NewPgDumper must satisfy service.ScopedDBDumper")
}

func TestPgDumperDumpWithOptions_RuntimeExcludesIncludingWildcardPartitionPattern(t *testing.T) {
	got := runDumpWithOptionsCapturing(t, config.BackupConfig{}, service.BackupDumpOptions{
		ExcludeTableData: []string{"public.usage_logs", "public.usage_logs_*", "public.ops_error_logs"},
	}, always(false))

	require.Equal(t, "pg_dump", got.name)
	require.Equal(t, []string{
		"-h", "db.example.test",
		"-p", "5432",
		"-U", "sub2api",
		"-d", "sub2api",
		"--no-owner",
		"--no-acl",
		"--exclude-table-data=public.usage_logs",
		"--exclude-table-data=public.usage_logs_*",
		"--exclude-table-data=public.ops_error_logs",
		"--lock-wait-timeout=30s",
	}, got.args)
	require.NotContains(t, got.args, "--clean")
	require.NotContains(t, got.args, "--if-exists")
}

func TestPgDumperDumpWithOptions_RunsUnderNiceWhenAvailable(t *testing.T) {
	got := runDumpWithOptionsCapturing(t, config.BackupConfig{}, service.BackupDumpOptions{
		ExcludeTableData: service.DefaultCriticalExcludeTableData,
	}, always(true))

	require.Equal(t, "nice", got.name)
	require.Equal(t, "-n", got.args[0])
	require.Equal(t, "19", got.args[1])
	require.Equal(t, "pg_dump", got.args[2])
	require.Contains(t, got.args, "--exclude-table-data=public.usage_logs")
	require.Contains(t, got.args, "--exclude-table-data=public.usage_logs_*")
	require.Contains(t, got.args, "--lock-wait-timeout=30s")
	require.NotContains(t, got.args, "--clean")
	require.NotContains(t, got.args, "--if-exists")
}

func TestPgDumperDumpWithOptions_EmptyOptionsKeepsLegacyFullDump(t *testing.T) {
	got := runDumpWithOptionsCapturing(t, config.BackupConfig{}, service.BackupDumpOptions{}, always(true))
	require.Equal(t, "pg_dump", got.name)
	require.Equal(t, legacyPgDumpArgs, got.args)
}

func TestPgDumperDumpWithOptions_DeploymentExcludeConfigCannotBeOverridden(t *testing.T) {
	backup := config.BackupConfig{
		ExcludeTableData: []string{"public.server_enforced_table"},
	}

	// Empty runtime options cannot bypass server exclude list.
	gotEmpty := runDumpWithOptionsCapturing(t, backup, service.BackupDumpOptions{}, always(false))
	require.Contains(t, gotEmpty.args, "--exclude-table-data=public.server_enforced_table")
	require.NotContains(t, gotEmpty.args, "--clean")

	// Custom runtime options cannot replace server exclude list.
	gotOverride := runDumpWithOptionsCapturing(t, backup, service.BackupDumpOptions{
		ExcludeTableData: []string{"public.other_table"},
	}, always(false))
	require.Contains(t, gotOverride.args, "--exclude-table-data=public.server_enforced_table")
	require.NotContains(t, gotOverride.args, "--exclude-table-data=public.other_table")
}

func TestPgDumperDumpWithOptions_RejectsUnsafePatternsBeforeLocking(t *testing.T) {
	for _, bad := range []string{
		"",
		"   ",
		"--clean",
		"-t",
		"*",
		"public.*",
		"pub*.usage_logs",
		"a.b.c",
		"public.a;drop table users",
		"public.a b",
		`public."users"`,
		"public.a\nb",
	} {
		t.Run(bad, func(t *testing.T) {
			commandCalled := false
			dumper, mock := newTestPgDumper(t, func(context.Context, string, ...string) *exec.Cmd {
				commandCalled = true
				return nil
			})

			reader, err := dumper.DumpWithOptions(context.Background(), service.BackupDumpOptions{
				ExcludeTableData: []string{bad},
			})
			require.Nil(t, reader)
			require.ErrorContains(t, err, "invalid exclude_table_data entry")
			require.False(t, commandCalled)
			require.NoError(t, mock.ExpectationsWereMet(), "must fail before acquiring migration lock")
		})
	}
}
