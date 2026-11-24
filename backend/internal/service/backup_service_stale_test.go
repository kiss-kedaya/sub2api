//go:build unit

package service

import (
	"context"
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

	// The names are spelled out on purpose: they are the on-disk contract.
	staleArchive := writeStagedFile(t, dir, "sub2api-backup-stage-111.sql.gz.age", old)
	stalePart := writeStagedFile(t, dir, "sub2api-backup-stage-part-222", old)
	freshArchive := writeStagedFile(t, dir, "sub2api-backup-stage-333.sql.gz", now.Add(-time.Minute))
	almostStale := writeStagedFile(t, dir, "sub2api-backup-stage-444.sql.gz", now.Add(-staleBackupFileAge+time.Minute))
	unrelated := writeStagedFile(t, dir, "unrelated-file.tmp", old)
	otherStaging := writeStagedFile(t, dir, "sub2api-restore-555.sql.gz", old)
	staleDir := filepath.Join(dir, "sub2api-backup-stage-dir")
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
			stale := writeStagedFile(t, dir, "sub2api-backup-stage-999.sql.gz.age", old)
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

// A sub2api-backup-<date>.sql.gz that an operator copied into /tmp is old enough to
// look stale, but it is not something this service staged. The sweep runs on every
// restart, as root when the service has no private /tmp, so it may only ever match
// what the service itself stages. The staged file next to them proves the sweep
// really ran, so "the other files survived" is not a vacuous pass.
func TestBackupService_StartLeavesAnOperatorsOwnFilesAlone(t *testing.T) {
	dir := isolateTempDir(t)
	threeHoursAgo := time.Now().Add(-3 * time.Hour)
	staged := writeStagedFile(t, dir, "sub2api-backup-stage-111.sql.gz.age", threeHoursAgo)
	notStaged := map[string]string{
		"sub2api-backup-20261101.sql.gz":     "an operator's own dump",
		"sub2api-backup-20261101.sql.gz.age": "an operator's own encrypted dump",
		"sub2api-backup-part-20261101":       "the name earlier builds gave split parts: no longer swept",
	}
	kept := make(map[string]string, len(notStaged))
	for name, why := range notStaged {
		kept[writeStagedFile(t, dir, name, threeHoursAgo)] = why
	}
	svc := newCriticalTestService(newMockSettingRepo(), stringDumper(""), newMockObjectStore(), config.BackupConfig{
		ExcludeTableData: []string{"public.example_log_table"},
		AgeRecipients:    recipientsOf(newAgeIdentity(t)),
	})

	svc.Start()
	svc.Stop()

	require.NoFileExists(t, staged, "the sweep ran: a stale file with the staging prefix is gone")
	for path, why := range kept {
		require.FileExists(t, path, "%s: not staged by this service, however old", why)
	}
}

// The sweep and the staging code have to agree on the prefix: whatever the service
// stages (a plain archive, an encrypted one, the parts of a split archive) must be
// removed by the sweep once it is stale, or the sweep silently does nothing.
func TestBackupService_SweepRemovesWhatTheServiceStages(t *testing.T) {
	dir := isolateTempDir(t)
	content := string(randomBytes(t, 4096))
	stage := func(backup config.BackupConfig) string {
		svc := newCriticalTestService(newMockSettingRepo(), stringDumper(content), newMockObjectStore(), backup)
		path, _, _, err := svc.createCompressedBackupFile(context.Background())
		require.NoError(t, err)
		return path
	}

	plain := stage(config.BackupConfig{})
	encrypted := stage(config.BackupConfig{
		ExcludeTableData: []string{"public.example_log_table"},
		AgeRecipients:    recipientsOf(newAgeIdentity(t)),
	})
	parts, err := splitBackupFile(encrypted, 1024)
	require.NoError(t, err)
	require.Greater(t, len(parts), 1)

	staged := []string{plain, encrypted}
	for _, part := range parts {
		staged = append(staged, part.Path)
	}
	old := time.Now().Add(-staleBackupFileAge - time.Hour)
	for _, path := range staged {
		require.NoError(t, os.Chtimes(path, old, old))
	}

	svc := newCriticalTestService(newMockSettingRepo(), stringDumper(""), newMockObjectStore(), config.BackupConfig{})
	require.Equal(t, len(staged), svc.sweepStaleBackupFiles(time.Now()))
	requireDirEmpty(t, dir)
}
