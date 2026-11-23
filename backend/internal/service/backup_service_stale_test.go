//go:build unit

package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func writeStagedFile(t *testing.T, dir, name string, modTime time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("staged"), 0o600))
	require.NoError(t, os.Chtimes(path, modTime, modTime))
	return path
}

// A deploy that kills the process mid-backup strands a file as large as the
// archive. Only old files with the staging prefix may be swept.
func TestBackupService_SweepStaleBackupFiles(t *testing.T) {
	dir := isolateTempDir(t)
	svc := newCriticalTestService(newMockSettingRepo(), stringDumper(""), newMockObjectStore(), config.BackupConfig{})
	now := time.Now()
	old := now.Add(-staleBackupFileAge - time.Minute)

	staleArchive := writeStagedFile(t, dir, "sub2api-backup-111.sql.gz.age", old)
	stalePart := writeStagedFile(t, dir, "sub2api-backup-part-222", old)
	freshArchive := writeStagedFile(t, dir, "sub2api-backup-333.sql.gz", now.Add(-time.Minute))
	almostStale := writeStagedFile(t, dir, "sub2api-backup-444.sql.gz", now.Add(-staleBackupFileAge+time.Minute))
	unrelated := writeStagedFile(t, dir, "unrelated-file.tmp", old)
	otherStaging := writeStagedFile(t, dir, "sub2api-restore-555.sql.gz", old)
	staleDir := filepath.Join(dir, "sub2api-backup-dir")
	require.NoError(t, os.Mkdir(staleDir, 0o700))
	require.NoError(t, os.Chtimes(staleDir, old, old))

	require.Equal(t, 2, svc.sweepStaleBackupFiles(now))

	require.NoFileExists(t, staleArchive)
	require.NoFileExists(t, stalePart)
	for _, kept := range []string{freshArchive, almostStale, unrelated, otherStaging} {
		require.FileExists(t, kept)
	}
	require.DirExists(t, staleDir, "a directory with the prefix is not a staged archive")
}

// Start sweeps only when the operator opted into a non-default backup
// configuration: a default install behaves exactly as before.
func TestBackupService_StartSweepsStaleFilesOnlyForNonDefaultBackups(t *testing.T) {
	tests := []struct {
		name        string
		backup      config.BackupConfig
		wantRemoved bool
	}{
		{name: "default configuration leaves the temp directory alone", backup: config.BackupConfig{}},
		{name: "critical snapshot", backup: config.BackupConfig{
			ExcludeTableData: []string{"public.example_log_table"},
			AgeRecipients:    recipientsOf(newAgeIdentity(t)),
		}, wantRemoved: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := isolateTempDir(t)
			old := time.Now().Add(-staleBackupFileAge - time.Hour)
			stale := writeStagedFile(t, dir, "sub2api-backup-999.sql.gz.age", old)
			svc := newCriticalTestService(newMockSettingRepo(), stringDumper(""), newMockObjectStore(), tt.backup)

			svc.Start()
			svc.Stop()

			if tt.wantRemoved {
				require.NoFileExists(t, stale)
			} else {
				require.FileExists(t, stale)
			}
		})
	}
}
