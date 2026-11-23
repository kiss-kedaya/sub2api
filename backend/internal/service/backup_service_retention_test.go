//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/require"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ─── helpers ───

func retentionID(i int) string  { return fmt.Sprintf("rec-%03d", i) }
func retentionKey(i int) string { return fmt.Sprintf("backups/rec-%03d.sql.gz", i) }

// retentionRecord is the i-th record of a history: a higher i started later.
func retentionRecord(i int, status string) BackupRecord {
	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Hour)
	return BackupRecord{
		ID:         retentionID(i),
		Status:     status,
		BackupType: "postgres",
		FileName:   fmt.Sprintf("rec-%03d.sql.gz", i),
		S3Key:      retentionKey(i),
		StartedAt:  started.Format(time.RFC3339),
	}
}

// seedRetentionRecords stores the records as they would sit in the settings table
// and puts an object in the bucket for every completed one.
func seedRetentionRecords(t *testing.T, repo *mockSettingRepo, store *mockObjectStore, records []BackupRecord) {
	t.Helper()
	for _, r := range records {
		if r.Status == "completed" {
			store.objects[r.S3Key] = []byte("archive " + r.ID)
		}
	}
	data, err := json.Marshal(records)
	require.NoError(t, err)
	require.NoError(t, repo.Set(context.Background(), settingKeyBackupRecords, string(data)))
}

func newRetentionService(t *testing.T) (*BackupService, *mockSettingRepo, *mockObjectStore) {
	t.Helper()
	repo := newMockSettingRepo()
	seedS3Config(t, repo)
	store := newMockObjectStore()
	return newTestBackupService(repo, &mockDumper{}, store), repo, store
}

func storedRecordIDs(t *testing.T, svc *BackupService) []string {
	t.Helper()
	records, err := svc.loadRecords(context.Background())
	require.NoError(t, err)
	ids := make([]string, 0, len(records))
	for _, r := range records {
		ids = append(ids, r.ID)
	}
	return ids
}

// ─── UpdateSchedule: retain_count must fit the record list ───

func TestBackupService_UpdateSchedule_RetainCountMustFitTheRecordList(t *testing.T) {
	require.Equal(t, maxBackupRecords-1, maxBackupRetainCount, "one slot always belongs to the backup in flight")

	tests := []struct {
		name    string
		cfg     BackupScheduleConfig
		wantErr bool
	}{
		{name: "at the limit", cfg: BackupScheduleConfig{Enabled: true, CronExpr: "0 * * * *", RetainCount: maxBackupRetainCount}},
		{name: "one past the limit", cfg: BackupScheduleConfig{Enabled: true, CronExpr: "0 * * * *", RetainCount: maxBackupRecords}, wantErr: true},
		{name: "a week of hourly backups", cfg: BackupScheduleConfig{Enabled: true, CronExpr: "0 * * * *", RetainCount: 168}, wantErr: true},
		{name: "zero keeps the unlimited meaning", cfg: BackupScheduleConfig{Enabled: true, CronExpr: "0 * * * *", RetainCount: 0}},
		{name: "a disabled schedule can still be saved", cfg: BackupScheduleConfig{Enabled: false, CronExpr: "0 * * * *", RetainCount: 500}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockSettingRepo()
			svc := newTestBackupService(repo, &mockDumper{}, newMockObjectStore())
			svc.cronSched = cron.New()

			saved, err := svc.UpdateSchedule(context.Background(), tt.cfg)

			stored, getErr := repo.GetValue(context.Background(), settingKeyBackupSchedule)
			require.NoError(t, getErr)
			if tt.wantErr {
				require.Nil(t, saved)
				require.Equal(t, "INVALID_RETENTION", infraerrors.Reason(err))
				require.Equal(t, 400, infraerrors.Code(err))
				require.ErrorContains(t, err, fmt.Sprintf("at most %d", maxBackupRetainCount))
				require.Empty(t, stored, "a rejected schedule must not be stored")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.cfg.RetainCount, saved.RetainCount)
			require.NotEmpty(t, stored)
		})
	}
}

// ─── saveRecord: never push a live backup out of the list ───

// A run of failed backups (an expired credential, a too small limit) must not
// squeeze the good snapshots out of the list: the failed records go instead.
func TestBackupService_SaveRecord_DropsFailedRecordsBeforeCompletedOnes(t *testing.T) {
	svc, repo, store := newRetentionService(t)
	const completed = 10
	var seeded []BackupRecord
	for i := 0; i < completed; i++ {
		seeded = append(seeded, retentionRecord(i, "completed"))
	}
	for i := completed; i < maxBackupRecords; i++ {
		seeded = append(seeded, retentionRecord(i, "failed"))
	}
	seedRetentionRecords(t, repo, store, seeded)

	newest := retentionRecord(maxBackupRecords, "failed")
	require.NoError(t, svc.saveRecord(context.Background(), &newest))

	ids := storedRecordIDs(t, svc)
	require.Len(t, ids, maxBackupRecords)
	for i := 0; i < completed; i++ {
		require.Contains(t, ids, retentionID(i), "a completed snapshot must stay in the list")
		require.Contains(t, store.objects, retentionKey(i))
	}
	require.NotContains(t, ids, retentionID(completed), "the oldest failed record is the one that goes")
	require.Contains(t, ids, retentionID(maxBackupRecords))
	require.Empty(t, store.deletedKeys, "a failed record owns no object")
}

// When only completed records are left to drop, their objects go first, so the
// bucket never keeps an object that no record can reach.
func TestBackupService_SaveRecord_DeletesTheObjectBeforeDroppingACompletedRecord(t *testing.T) {
	svc, repo, store := newRetentionService(t)
	var seeded []BackupRecord
	for i := 0; i < maxBackupRecords; i++ {
		seeded = append(seeded, retentionRecord(i, "completed"))
	}
	seedRetentionRecords(t, repo, store, seeded)

	newest := retentionRecord(maxBackupRecords, "completed")
	store.objects[newest.S3Key] = []byte("archive " + newest.ID)
	require.NoError(t, svc.saveRecord(context.Background(), &newest))

	ids := storedRecordIDs(t, svc)
	require.Len(t, ids, maxBackupRecords)
	require.NotContains(t, ids, retentionID(0), "the oldest completed record is dropped")
	require.Equal(t, []string{retentionKey(0)}, store.deletedKeys, "its object is deleted, and only its object")
	require.NotContains(t, store.objects, retentionKey(0))
	require.Len(t, store.objects, len(ids), "every object left in the bucket belongs to a record in the list")
}

// An object that cannot be deleted must stay reachable: its record is kept for a
// later retry and the next oldest completed record makes room instead.
func TestBackupService_SaveRecord_KeepsACompletedRecordWhoseObjectCannotBeDeleted(t *testing.T) {
	svc, repo, store := newRetentionService(t)
	var seeded []BackupRecord
	for i := 0; i < maxBackupRecords; i++ {
		seeded = append(seeded, retentionRecord(i, "completed"))
	}
	seedRetentionRecords(t, repo, store, seeded)
	store.failDeleteKeys[retentionKey(0)] = errors.New("delete denied")

	newest := retentionRecord(maxBackupRecords, "completed")
	store.objects[newest.S3Key] = []byte("archive " + newest.ID)
	require.NoError(t, svc.saveRecord(context.Background(), &newest))

	ids := storedRecordIDs(t, svc)
	require.Len(t, ids, maxBackupRecords)
	require.Contains(t, ids, retentionID(0), "its object is still in the bucket, so the record stays")
	require.Contains(t, store.objects, retentionKey(0))
	require.NotContains(t, ids, retentionID(1), "the next oldest made room")
	require.NotContains(t, store.objects, retentionKey(1))
	require.Len(t, store.objects, len(ids), "no object is left without a record")
}

func TestBackupService_SaveRecord_NeverDropsARunningRecord(t *testing.T) {
	svc, repo, store := newRetentionService(t)
	seeded := []BackupRecord{retentionRecord(0, "running")} // the oldest of all
	for i := 1; i < maxBackupRecords; i++ {
		seeded = append(seeded, retentionRecord(i, "completed"))
	}
	seedRetentionRecords(t, repo, store, seeded)

	newest := retentionRecord(maxBackupRecords, "completed")
	store.objects[newest.S3Key] = []byte("archive " + newest.ID)
	require.NoError(t, svc.saveRecord(context.Background(), &newest))

	ids := storedRecordIDs(t, svc)
	require.Len(t, ids, maxBackupRecords)
	require.Contains(t, ids, retentionID(0), "an in-flight backup keeps its record")
	require.NotContains(t, ids, retentionID(1))
}

// cleanupOldBackups stores the list newest first, so "oldest" must come from the
// start time and not from the position in the list.
func TestBackupService_SaveRecord_DropsTheOldestByStartTimeNotByPosition(t *testing.T) {
	svc, repo, store := newRetentionService(t)
	var seeded []BackupRecord
	for i := maxBackupRecords - 1; i >= 0; i-- {
		seeded = append(seeded, retentionRecord(i, "completed"))
	}
	seedRetentionRecords(t, repo, store, seeded)

	newest := retentionRecord(maxBackupRecords, "completed")
	store.objects[newest.S3Key] = []byte("archive " + newest.ID)
	require.NoError(t, svc.saveRecord(context.Background(), &newest))

	ids := storedRecordIDs(t, svc)
	require.Len(t, ids, maxBackupRecords)
	require.NotContains(t, ids, retentionID(0), "the oldest by start time is dropped")
	require.Contains(t, ids, retentionID(maxBackupRecords-1), "the newest of the stored list must survive")
	require.Equal(t, []string{retentionKey(0)}, store.deletedKeys)
}

// Updating a record that is already in a full list changes nothing else.
func TestBackupService_SaveRecord_UpdatingInPlaceInAFullListDropsNothing(t *testing.T) {
	svc, repo, store := newRetentionService(t)
	var seeded []BackupRecord
	for i := 0; i < maxBackupRecords; i++ {
		seeded = append(seeded, retentionRecord(i, "completed"))
	}
	seedRetentionRecords(t, repo, store, seeded)

	update := retentionRecord(maxBackupRecords-1, "completed")
	update.RestoreStatus = "completed"
	require.NoError(t, svc.saveRecord(context.Background(), &update))

	require.Len(t, storedRecordIDs(t, svc), maxBackupRecords)
	require.Empty(t, store.deletedKeys)
	require.Len(t, store.objects, maxBackupRecords)
}
