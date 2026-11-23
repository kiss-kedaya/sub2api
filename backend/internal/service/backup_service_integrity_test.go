//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ─── archive digest ───

// The digest is computed while the archive is written, so it has to be the digest
// of the bytes that really are on disk: the ciphertext when encrypted.
func TestCreateCompressedBackupFile_ReturnsTheSHA256OfTheBytesOnDisk(t *testing.T) {
	tests := []struct {
		name   string
		backup config.BackupConfig
	}{
		{name: "whole database", backup: config.BackupConfig{}},
		{name: "encrypted", backup: config.BackupConfig{AgeRecipients: recipientsOf(newAgeIdentity(t))}},
		{name: "critical snapshot", backup: config.BackupConfig{
			ExcludeTableData: []string{"public.example_log_table"},
			AgeRecipients:    recipientsOf(newAgeIdentity(t)),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateTempDir(t)
			svc := newCriticalTestService(newMockSettingRepo(), stringDumper(knownDump), newMockObjectStore(), tt.backup)

			path, _, digest, err := svc.createCompressedBackupFile(context.Background())
			require.NoError(t, err)

			onDisk, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, sha256Hex(onDisk), digest)
			require.Len(t, digest, 64)
		})
	}
}

func TestBackupService_RecordsTheArchiveDigestOfNonDefaultBackups(t *testing.T) {
	identity := newAgeIdentity(t)
	tests := []struct {
		name         string
		backup       config.BackupConfig
		partSize     int64
		wantParts    bool
		wantRecorded bool
	}{
		{name: "default whole-database backup records none", backup: config.BackupConfig{}},
		{name: "encrypted", backup: config.BackupConfig{AgeRecipients: recipientsOf(identity)}, wantRecorded: true},
		{name: "critical snapshot", backup: config.BackupConfig{
			ExcludeTableData: []string{"public.example_log_table"},
			AgeRecipients:    recipientsOf(identity),
		}, wantRecorded: true},
		{name: "size limit only", backup: config.BackupConfig{MaxArchiveBytes: 1 << 20}, wantRecorded: true},
		{name: "critical snapshot, split into parts", backup: config.BackupConfig{
			ExcludeTableData: []string{"public.example_log_table"},
			AgeRecipients:    recipientsOf(identity),
		}, partSize: 1024, wantParts: true, wantRecorded: true},
	}
	for _, tt := range tests {
		for _, path := range []string{"CreateBackup", "StartBackup"} {
			t.Run(tt.name+"/"+path, func(t *testing.T) {
				isolateTempDir(t)
				repo := newMockSettingRepo()
				seedS3Config(t, repo)
				store := newMockObjectStore()
				svc := newCriticalTestService(repo, stringDumper(string(randomBytes(t, 4096))), store, tt.backup)
				if tt.partSize > 0 {
					svc.partSizeBytes = tt.partSize
				}

				var record *BackupRecord
				if path == "CreateBackup" {
					created, err := svc.CreateBackup(context.Background(), "manual", 14)
					require.NoError(t, err)
					record = created
				} else {
					started, err := svc.StartBackup(context.Background(), "manual", 14)
					require.NoError(t, err)
					svc.wg.Wait()
					final, err := svc.GetBackupRecord(context.Background(), started.ID)
					require.NoError(t, err)
					record = final
				}
				require.Equal(t, "completed", record.Status)

				// What the bucket holds, joined in order when the archive was split.
				var uploaded []byte
				if tt.wantParts {
					require.Greater(t, len(record.Parts), 1)
					for _, part := range record.Parts {
						uploaded = append(uploaded, store.objects[part.S3Key]...)
					}
				} else {
					uploaded = store.objects[record.S3Key]
				}
				require.NotEmpty(t, uploaded)

				encoded, err := json.Marshal(record)
				require.NoError(t, err)
				if !tt.wantRecorded {
					require.Empty(t, record.SHA256)
					require.NotContains(t, string(encoded), `"sha256"`, "a default record stays byte-identical")
					return
				}
				require.Equal(t, sha256Hex(uploaded), record.SHA256, "the record must hold the digest of what the bucket holds")
				require.Contains(t, string(encoded), `"sha256"`)

				// The record in the settings table is the one a restore would consult.
				stored, err := svc.GetBackupRecord(context.Background(), record.ID)
				require.NoError(t, err)
				require.Equal(t, record.SHA256, stored.SHA256)
			})
		}
	}
}
