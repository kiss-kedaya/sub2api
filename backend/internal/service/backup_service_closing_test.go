//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"filippo.io/age"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// ─── every closing step may fail, and then nothing half-written stays ───

type closeFailingWriter struct {
	io.WriteCloser
	err error
}

// Close really closes (so the last chunk is flushed and the file can be removed on
// every platform) and then reports the failure, as a full disk would.
func (w closeFailingWriter) Close() error {
	_ = w.WriteCloser.Close()
	return w.err
}

type closeFailingFile struct {
	*os.File
	err error
}

func (f closeFailingFile) Close() error {
	_ = f.File.Close()
	return f.err
}

// A pg_dump that exits non-zero after producing output, an age encryptor that
// cannot write its last chunk and an archive file that cannot be closed all leave
// a valid-looking but truncated archive. None of them may end as "completed".
func TestBackupService_CreateBackup_ClosingErrorsNeverEndInACompletedRecord(t *testing.T) {
	tests := []struct {
		name    string
		wantErr string
		inject  func(svc *BackupService, dumper *streamDumper)
	}{
		{
			name:    "pg_dump exits non-zero",
			wantErr: "exit status 1",
			inject: func(_ *BackupService, dumper *streamDumper) {
				dumper.closeErr = errors.New("pg_dump exited with error: exit status 1")
			},
		},
		{
			name:    "age encryptor cannot finish",
			wantErr: "age final chunk",
			inject: func(svc *BackupService, _ *streamDumper) {
				svc.ageEncrypt = func(w io.Writer, recipients ...age.Recipient) (io.WriteCloser, error) {
					enc, err := age.Encrypt(w, recipients...)
					if err != nil {
						return nil, err
					}
					return closeFailingWriter{WriteCloser: enc, err: errors.New("age final chunk not written")}, nil
				}
			},
		},
		{
			name:    "archive file cannot be closed",
			wantErr: "archive close failed",
			inject: func(svc *BackupService, _ *streamDumper) {
				svc.createArchiveFile = func(pattern string) (backupArchiveFile, error) {
					file, err := os.CreateTemp("", pattern)
					if err != nil {
						return nil, err
					}
					return closeFailingFile{File: file, err: errors.New("archive close failed")}, nil
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := isolateTempDir(t)
			repo := newMockSettingRepo()
			seedS3Config(t, repo)
			store := &contentTypeStore{mockObjectStore: newMockObjectStore()}
			dumper := stringDumper(knownDump)
			// Encrypted, so that the dump, age and file closers are all in the chain.
			svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{AgeRecipients: recipientsOf(newAgeIdentity(t))})
			tt.inject(svc, dumper)

			path, size, digest, err := svc.createCompressedBackupFile(context.Background())
			require.ErrorContains(t, err, tt.wantErr)
			require.Empty(t, path)
			require.Zero(t, size)
			require.Empty(t, digest)
			requireDirEmpty(t, dir)

			record, err := svc.CreateBackup(context.Background(), "scheduled", 14)
			require.ErrorContains(t, err, tt.wantErr)
			require.Equal(t, "failed", record.Status)
			require.Contains(t, record.ErrorMsg, tt.wantErr)
			require.Empty(t, store.objects, "a truncated archive must never reach the bucket")
			require.Empty(t, store.contentTypes)
			requireDirEmpty(t, dir)
		})
	}
}

// The asynchronous path fails the same way: the record ends as failed.
func TestBackupService_StartBackup_DumpExitFailureEndsAsAFailedRecord(t *testing.T) {
	dir := isolateTempDir(t)
	repo := newMockSettingRepo()
	seedS3Config(t, repo)
	store := newMockObjectStore()
	dumper := stringDumper(knownDump)
	dumper.closeErr = errors.New("pg_dump exited with error: exit status 1")
	svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{})

	started, err := svc.StartBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	svc.wg.Wait()

	final, err := svc.GetBackupRecord(context.Background(), started.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", final.Status)
	require.Contains(t, final.ErrorMsg, "exit status 1")
	require.Empty(t, final.SHA256)
	require.Empty(t, store.objects)
	requireDirEmpty(t, dir)
}

// When the staging file cannot be created, pg_dump may still be waiting for a table
// lock. Close waits for the process, so its context has to be cancelled first.
func TestCreateCompressedBackupFile_StagingFailureCancelsPgDumpBeforeClosingIt(t *testing.T) {
	dir := isolateTempDir(t)
	dumper := stringDumper(knownDump)
	svc := newCriticalTestService(newMockSettingRepo(), dumper, newMockObjectStore(), config.BackupConfig{})
	svc.createArchiveFile = func(string) (backupArchiveFile, error) { return nil, errors.New("no space left on device") }

	path, size, digest, err := svc.createCompressedBackupFile(context.Background())

	require.ErrorContains(t, err, "create backup archive")
	require.ErrorContains(t, err, "no space left on device")
	require.Empty(t, path)
	require.Zero(t, size)
	require.Empty(t, digest)
	require.True(t, dumper.closed, "the dump reader must still be closed")
	require.ErrorIs(t, dumper.ctxErrAtClose, context.Canceled, "pg_dump must be told to stop before Close waits for it")
	requireDirEmpty(t, dir)
}
