//go:build unit

package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"filippo.io/age"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ─── helpers ───

// knownDump carries marker strings that must never be visible in an encrypted
// archive on disk.
const knownDump = "-- PostgreSQL database dump\n\n" +
	"CREATE TABLE public.x (id integer);\n\n" +
	"COPY public.x (id) FROM stdin;\n1\n2\n3\n\\.\n"

// gzip header byte 8 (XFL) records the compression level: 0 default, 4 fastest.
const (
	gzipXFLDefault   = 0
	gzipXFLBestSpeed = 4
)

// newAgeIdentity generates a throw-away identity; no key material is committed.
func newAgeIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	return identity
}

// isolateTempDir points os.TempDir at an empty directory so a test can assert a
// failed backup leaves nothing behind.
func isolateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
	return dir
}

func requireDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "no archive file may be left behind")
}

func gunzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	out, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	return out
}

// decryptArchive reverses the age layer and returns the inner gzip stream.
func decryptArchive(t *testing.T, raw []byte, identity age.Identity) []byte {
	t.Helper()
	reader, err := age.Decrypt(bytes.NewReader(raw), identity)
	require.NoError(t, err)
	inner, err := io.ReadAll(reader)
	require.NoError(t, err)
	return inner
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	data := make([]byte, n)
	_, err := rand.Read(data)
	require.NoError(t, err)
	return data
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// streamDumper streams whatever source returns and records how it was used.
type streamDumper struct {
	source func() io.Reader

	dumpCalls      int
	closed         bool
	ctxErrAtClose  error
	dumpCtx        context.Context
	restoreCalls   int
	restoreHistory []byte
}

func (d *streamDumper) Dump(ctx context.Context) (io.ReadCloser, error) {
	d.dumpCalls++
	d.dumpCtx = ctx
	return &streamDumperReader{Reader: d.source(), owner: d}, nil
}

func (d *streamDumper) Restore(_ context.Context, data io.Reader) error {
	d.restoreCalls++
	d.restoreHistory, _ = io.ReadAll(data)
	return nil
}

type streamDumperReader struct {
	io.Reader
	owner *streamDumper
}

func (r *streamDumperReader) Close() error {
	r.owner.closed = true
	r.owner.ctxErrAtClose = r.owner.dumpCtx.Err()
	return nil
}

func stringDumper(content string) *streamDumper {
	return &streamDumper{source: func() io.Reader { return strings.NewReader(content) }}
}

// contentTypeStore records the Content-Type of every single-object upload.
type contentTypeStore struct {
	*mockObjectStore
	mu           sync.Mutex
	contentTypes []string
}

func (s *contentTypeStore) UploadFile(ctx context.Context, key, filePath, contentType string) (int64, error) {
	s.mu.Lock()
	s.contentTypes = append(s.contentTypes, contentType)
	s.mu.Unlock()
	return s.mockObjectStore.UploadFile(ctx, key, filePath, contentType)
}

// newCriticalTestService builds a service the way production does, so the test
// also proves NewBackupService carries cfg.Backup through.
func newCriticalTestService(repo *mockSettingRepo, dumper DBDumper, store BackupObjectStore, backup config.BackupConfig) *BackupService {
	cfg := &config.Config{
		Database: config.DatabaseConfig{Host: "localhost", Port: 5432, User: "test", DBName: "testdb"},
		Totp:     config.TotpConfig{EncryptionKeyConfigured: true},
		Backup:   backup,
	}
	factory := func(context.Context, *BackupS3Config) (BackupObjectStore, error) { return store, nil }
	return NewBackupService(repo, cfg, &plainEncryptor{}, factory, dumper)
}

func recipientsOf(identities ...*age.X25519Identity) []string {
	keys := make([]string, 0, len(identities))
	for _, identity := range identities {
		keys = append(keys, identity.Recipient().String())
	}
	return keys
}

// ─── legacy behavior ───

func TestCreateCompressedBackupFile_DefaultIsPlainGzipAtDefaultLevel(t *testing.T) {
	dir := isolateTempDir(t)
	svc := newCriticalTestService(newMockSettingRepo(), stringDumper(knownDump), newMockObjectStore(), config.BackupConfig{})

	path, size, err := svc.createCompressedBackupFile(context.Background())
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(path, ".sql.gz"), "temp archive keeps the legacy suffix: %s", path)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, int64(len(raw)), size)
	require.Equal(t, []byte{0x1f, 0x8b}, raw[:2], "gzip magic")
	require.Equal(t, byte(gzipXFLDefault), raw[8], "whole-database dump keeps the default gzip level")
	require.Equal(t, knownDump, string(gunzipBytes(t, raw)))

	require.NoError(t, cleanupBackupFiles(path))
	requireDirEmpty(t, dir)
}

func TestBackupService_CreateBackup_DefaultRecordAndUploadAreUnchanged(t *testing.T) {
	isolateTempDir(t)
	repo := newMockSettingRepo()
	seedS3Config(t, repo)
	store := &contentTypeStore{mockObjectStore: newMockObjectStore()}
	svc := newCriticalTestService(repo, stringDumper(knownDump), store, config.BackupConfig{})

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)

	require.True(t, strings.HasSuffix(record.FileName, ".sql.gz"))
	require.False(t, strings.HasSuffix(record.FileName, ".age"))
	require.True(t, strings.HasSuffix(record.S3Key, ".sql.gz"))
	require.Empty(t, record.Scope)
	require.False(t, record.Encrypted)
	require.Equal(t, []string{"application/gzip"}, store.contentTypes)

	object := store.objects[record.S3Key]
	require.Equal(t, []byte{0x1f, 0x8b}, object[:2])
	require.Equal(t, knownDump, string(gunzipBytes(t, object)))

	// omitempty keeps stored records byte-compatible with the previous version.
	encoded, err := json.Marshal(record)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), `"scope"`)
	require.NotContains(t, string(encoded), `"encrypted"`)
}

// ─── partial snapshots ───

func TestCreateCompressedBackupFile_PartialSnapshotUsesFastestGzip(t *testing.T) {
	isolateTempDir(t)
	svc := newCriticalTestService(newMockSettingRepo(), stringDumper(knownDump), newMockObjectStore(),
		config.BackupConfig{ExcludeTableData: []string{"public.big_table"}})

	path, _, err := svc.createCompressedBackupFile(context.Background())
	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	require.Equal(t, byte(gzipXFLBestSpeed), raw[8])
	require.Equal(t, knownDump, string(gunzipBytes(t, raw)))
}

// ─── encryption ───

func TestCreateCompressedBackupFile_EncryptsForEveryRecipient(t *testing.T) {
	tests := []struct {
		name    string
		exclude []string
		wantXFL byte
	}{
		{name: "whole database", wantXFL: gzipXFLDefault},
		{name: "critical snapshot", exclude: []string{"public.big_table"}, wantXFL: gzipXFLBestSpeed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateTempDir(t)
			first, second, stranger := newAgeIdentity(t), newAgeIdentity(t), newAgeIdentity(t)
			svc := newCriticalTestService(newMockSettingRepo(), stringDumper(knownDump), newMockObjectStore(), config.BackupConfig{
				ExcludeTableData: tt.exclude,
				AgeRecipients:    recipientsOf(first, second),
			})

			path, size, err := svc.createCompressedBackupFile(context.Background())
			require.NoError(t, err)
			require.True(t, strings.HasSuffix(path, ".sql.gz.age"), path)

			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, int64(len(raw)), size)

			// (a) nothing readable on disk: not the SQL, not even a gzip stream.
			require.True(t, bytes.HasPrefix(raw, []byte("age-encryption.org/v1\n")), "age header")
			require.False(t, bytes.HasPrefix(raw, []byte{0x1f, 0x8b}), "must not start as gzip")
			require.NotContains(t, string(raw), "COPY public.x")
			require.NotContains(t, string(raw), "CREATE TABLE")
			_, gzErr := gzip.NewReader(bytes.NewReader(raw))
			require.Error(t, gzErr, "the file on disk must not be a readable gzip stream")

			// (b) decrypt + gunzip gives back the exact dump, for each recipient.
			for _, identity := range []*age.X25519Identity{first, second} {
				inner := decryptArchive(t, raw, identity)
				require.Equal(t, tt.wantXFL, inner[8])
				require.Equal(t, knownDump, string(gunzipBytes(t, inner)))
			}

			// A key that was not a recipient cannot open it.
			_, err = age.Decrypt(bytes.NewReader(raw), stranger)
			require.Error(t, err)
		})
	}
}

func TestCreateCompressedBackupFile_EncryptorFailureLeavesNoPlaintext(t *testing.T) {
	dir := isolateTempDir(t)
	source := &countingReader{r: strings.NewReader(knownDump)}
	dumper := &streamDumper{source: func() io.Reader { return source }}
	svc := newCriticalTestService(newMockSettingRepo(), dumper, newMockObjectStore(), config.BackupConfig{
		AgeRecipients: recipientsOf(newAgeIdentity(t)),
	})
	svc.ageEncrypt = func(io.Writer, ...age.Recipient) (io.WriteCloser, error) {
		return nil, errors.New("encryptor unavailable")
	}

	path, size, err := svc.createCompressedBackupFile(context.Background())

	// (d) fail closed: an error, no archive, and not a single dump byte was
	// compressed or written anywhere.
	require.ErrorContains(t, err, "age encryptor")
	require.Empty(t, path)
	require.Zero(t, size)
	require.Zero(t, source.n, "the dump must not be consumed when encryption cannot start")
	require.True(t, dumper.closed, "the dump must be closed")
	requireDirEmpty(t, dir)
}

func TestCreateCompressedBackupFile_InvalidRecipientFailsBeforeDumping(t *testing.T) {
	dir := isolateTempDir(t)
	dumper := stringDumper(knownDump)
	badKey := "age1this-is-not-a-valid-recipient"
	svc := newCriticalTestService(newMockSettingRepo(), dumper, newMockObjectStore(), config.BackupConfig{
		AgeRecipients: []string{badKey},
	})

	path, _, err := svc.createCompressedBackupFile(context.Background())

	require.Error(t, err)
	require.Empty(t, path)
	require.NotContains(t, err.Error(), badKey, "the key text must not appear in the error")
	require.Zero(t, dumper.dumpCalls, "pg_dump must not even start")
	requireDirEmpty(t, dir)
}

// ─── size limit ───

func TestCreateCompressedBackupFile_SizeLimitAbortsEarlyAndCleansUp(t *testing.T) {
	const limit = 4096
	data := randomBytes(t, 4<<20) // incompressible, far larger than the limit

	tests := []struct {
		name    string
		exclude []string
		encrypt bool // the limit counts the bytes on disk, i.e. after encryption
	}{
		{name: "plain"},
		{name: "critical snapshot", exclude: []string{"public.t"}},
		{name: "encrypted", encrypt: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := isolateTempDir(t)
			backup := config.BackupConfig{MaxArchiveBytes: limit, ExcludeTableData: tt.exclude}
			if tt.encrypt {
				backup.AgeRecipients = recipientsOf(newAgeIdentity(t))
			}
			source := &countingReader{r: bytes.NewReader(data)}
			dumper := &streamDumper{source: func() io.Reader { return source }}
			svc := newCriticalTestService(newMockSettingRepo(), dumper, newMockObjectStore(), backup)

			path, size, err := svc.createCompressedBackupFile(context.Background())

			// (c) sentinel error, message with limit and written bytes, file gone.
			require.ErrorIs(t, err, ErrBackupArchiveTooLarge)
			require.ErrorContains(t, err, "limit 4096 bytes")
			require.ErrorContains(t, err, "written ")
			require.Empty(t, path)
			require.Zero(t, size)
			requireDirEmpty(t, dir)

			// Aborted straight away rather than after reading the whole dump,
			// and pg_dump was told to stop before its pipe was closed.
			require.Less(t, source.n, int64(len(data))/2, "the copy must stop soon after the limit is crossed")
			require.True(t, dumper.closed)
			require.ErrorIs(t, dumper.ctxErrAtClose, context.Canceled, "the dump context must be cancelled before Close")
		})
	}
}

func TestCreateCompressedBackupFile_SizeLimitBoundary(t *testing.T) {
	isolateTempDir(t)
	newService := func(limit int64) *BackupService {
		return newCriticalTestService(newMockSettingRepo(), stringDumper(knownDump), newMockObjectStore(),
			config.BackupConfig{MaxArchiveBytes: limit})
	}

	path, exact, err := newService(0).createCompressedBackupFile(context.Background())
	require.NoError(t, err, "0 means unlimited")
	require.NoError(t, cleanupBackupFiles(path))
	require.Greater(t, exact, int64(0))

	path, size, err := newService(exact).createCompressedBackupFile(context.Background())
	require.NoError(t, err, "an archive of exactly the limit is allowed")
	require.Equal(t, exact, size)
	require.NoError(t, cleanupBackupFiles(path))

	_, _, err = newService(exact - 1).createCompressedBackupFile(context.Background())
	require.ErrorIs(t, err, ErrBackupArchiveTooLarge)
}

func TestLimitedArchiveWriter_StopsWritingAfterFirstFailure(t *testing.T) {
	var sink bytes.Buffer
	w := &limitedArchiveWriter{w: &sink, limit: 5}

	_, err := w.Write([]byte("1234"))
	require.NoError(t, err)
	_, err = w.Write([]byte("5678"))
	require.ErrorIs(t, err, ErrBackupArchiveTooLarge)
	require.Equal(t, "12345678", sink.String())

	n, err := w.Write([]byte("more"))
	require.ErrorIs(t, err, ErrBackupArchiveTooLarge)
	require.Zero(t, n)
	require.Equal(t, "12345678", sink.String(), "nothing may be appended once the limit failed")
}

func TestBackupService_CreateBackup_SizeLimitFailsRecordAndUploadsNothing(t *testing.T) {
	dir := isolateTempDir(t)
	repo := newMockSettingRepo()
	seedS3Config(t, repo)
	store := &contentTypeStore{mockObjectStore: newMockObjectStore()}
	dumper := &streamDumper{source: func() io.Reader { return bytes.NewReader(randomBytes(t, 1<<20)) }}
	svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{MaxArchiveBytes: 1024})

	record, err := svc.CreateBackup(context.Background(), "scheduled", 14)

	require.ErrorIs(t, err, ErrBackupArchiveTooLarge)
	require.Equal(t, "failed", record.Status)
	require.Contains(t, record.ErrorMsg, "size limit")
	require.Empty(t, store.objects, "nothing may be uploaded")
	requireDirEmpty(t, dir)
}

// ─── records, names and upload content type ───

func TestBackupService_BackupModesRecordScopeEncryptionAndContentType(t *testing.T) {
	tests := []struct {
		name            string
		exclude         []string
		encrypted       bool
		wantScope       string
		wantSuffix      string
		wantContentType string
	}{
		{name: "default", wantSuffix: ".sql.gz", wantContentType: "application/gzip"},
		{name: "critical only", exclude: []string{"public.t"}, wantScope: "critical", wantSuffix: ".sql.gz", wantContentType: "application/gzip"},
		{name: "encrypted only", encrypted: true, wantSuffix: ".sql.gz.age", wantContentType: "application/octet-stream"},
		{name: "critical and encrypted", exclude: []string{"public.t"}, encrypted: true, wantScope: "critical", wantSuffix: ".sql.gz.age", wantContentType: "application/octet-stream"},
	}
	for _, tt := range tests {
		for _, path := range []string{"CreateBackup", "StartBackup"} {
			t.Run(tt.name+"/"+path, func(t *testing.T) {
				isolateTempDir(t)
				repo := newMockSettingRepo()
				seedS3Config(t, repo)
				store := &contentTypeStore{mockObjectStore: newMockObjectStore()}
				identity := newAgeIdentity(t)
				backup := config.BackupConfig{ExcludeTableData: tt.exclude}
				if tt.encrypted {
					backup.AgeRecipients = recipientsOf(identity)
				}
				svc := newCriticalTestService(repo, stringDumper(knownDump), store, backup)

				var record *BackupRecord
				if path == "CreateBackup" {
					created, err := svc.CreateBackup(context.Background(), "manual", 14)
					require.NoError(t, err)
					record = created
				} else {
					started, err := svc.StartBackup(context.Background(), "manual", 14)
					require.NoError(t, err)
					// The record returned to the caller already carries the flags.
					require.Equal(t, tt.wantScope, started.Scope)
					require.Equal(t, tt.encrypted, started.Encrypted)
					svc.wg.Wait()
					final, err := svc.GetBackupRecord(context.Background(), started.ID)
					require.NoError(t, err)
					record = final
				}

				require.Equal(t, "completed", record.Status)
				require.Equal(t, tt.wantScope, record.Scope)
				require.Equal(t, tt.encrypted, record.Encrypted)
				require.True(t, strings.HasSuffix(record.FileName, tt.wantSuffix), record.FileName)
				require.True(t, strings.HasSuffix(record.S3Key, tt.wantSuffix), record.S3Key)
				require.Equal(t, []string{tt.wantContentType}, store.contentTypes)

				object := store.objects[record.S3Key]
				require.NotEmpty(t, object)
				if tt.encrypted {
					require.NotContains(t, string(object), "COPY public.x")
					object = decryptArchive(t, object, identity)
				}
				require.Equal(t, knownDump, string(gunzipBytes(t, object)))
			})
		}
	}
}

func TestBackupService_EncryptedArchiveSurvivesSplitUpload(t *testing.T) {
	isolateTempDir(t)
	repo := newMockSettingRepo()
	seedS3Config(t, repo)
	store := newMockObjectStore()
	identity := newAgeIdentity(t)
	content := string(randomBytes(t, 4096))
	svc := newCriticalTestService(repo, stringDumper(content), store, config.BackupConfig{
		ExcludeTableData: []string{"public.t"},
		AgeRecipients:    recipientsOf(identity),
	})
	svc.partSizeBytes = 1024

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.Greater(t, len(record.Parts), 1)
	require.True(t, record.Encrypted)
	require.Equal(t, "critical", record.Scope)

	var joined bytes.Buffer
	for _, part := range record.Parts {
		joined.Write(store.objects[part.S3Key])
	}
	require.Equal(t, content, string(gunzipBytes(t, decryptArchive(t, joined.Bytes(), identity))))
}

// ─── restore protection ───

func TestBackupService_RestoreRefusesPartialAndEncryptedSnapshots(t *testing.T) {
	tests := []struct {
		name   string
		record BackupRecord
	}{
		{name: "encrypted", record: BackupRecord{Encrypted: true}},
		{name: "critical", record: BackupRecord{Scope: "critical"}},
		{name: "critical and encrypted", record: BackupRecord{Scope: "critical", Encrypted: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockSettingRepo()
			seedS3Config(t, repo)
			store := newMockObjectStore()
			dumper := stringDumper("")
			svc := newTestBackupService(repo, dumper, store)

			// A perfectly restorable gzip object: without the guard psql would run.
			record := tt.record
			record.ID = "snap-1"
			record.Status = "completed"
			record.S3Key = "backups/snap-1.sql.gz"
			store.objects[record.S3Key] = gzipBackupBytes(t, []byte("select 1;\n"))
			require.NoError(t, svc.saveRecord(context.Background(), &record))

			err := svc.RestoreBackup(context.Background(), record.ID)
			require.ErrorIs(t, err, ErrBackupRestoreUnsupported)
			require.Equal(t, "BACKUP_RESTORE_UNSUPPORTED", infraerrors.Reason(err))
			require.Equal(t, 400, infraerrors.Code(err))
			require.Equal(t, "partial or encrypted snapshots must be restored manually into an empty database", infraerrors.Message(err))

			started, err := svc.StartRestore(context.Background(), record.ID)
			require.ErrorIs(t, err, ErrBackupRestoreUnsupported)
			require.Nil(t, started)
			svc.wg.Wait()

			stored, err := svc.GetBackupRecord(context.Background(), record.ID)
			require.NoError(t, err)
			require.Empty(t, stored.RestoreStatus, "a refused restore must not touch the record")

			// The async worker itself is guarded too.
			svc.executeRestore(stored, store)
			after, err := svc.GetBackupRecord(context.Background(), record.ID)
			require.NoError(t, err)
			require.Equal(t, "failed", after.RestoreStatus)
			require.Contains(t, after.RestoreError, "restored manually")

			require.Zero(t, dumper.restoreCalls, "psql must never be reached")
			require.False(t, svc.restoring, "the restore slot must be released after a refusal")
		})
	}
}

func TestBackupService_RestoreStillWorksForWholeDatabaseBackups(t *testing.T) {
	isolateTempDir(t)
	repo := newMockSettingRepo()
	seedS3Config(t, repo)
	store := newMockObjectStore()
	dumper := stringDumper(knownDump)
	svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{})

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)

	require.NoError(t, svc.RestoreBackup(context.Background(), record.ID))
	require.Equal(t, 1, dumper.restoreCalls)
	require.Equal(t, knownDump, string(dumper.restoreHistory))

	_, err = svc.StartRestore(context.Background(), record.ID)
	require.NoError(t, err)
	svc.wg.Wait()
	require.Equal(t, 2, dumper.restoreCalls)
}

// ─── startup log ───

func TestBackupService_LogSnapshotMode(t *testing.T) {
	identity := newAgeIdentity(t)
	recipient := recipientsOf(identity)[0]
	secretTable := "public.secret_table_name"
	secretPath := "/opt/secret/location/pg_dump"

	observe := func(backup config.BackupConfig) []observer.LoggedEntry {
		core, logs := observer.New(zapcore.InfoLevel)
		newCriticalTestService(newMockSettingRepo(), stringDumper(""), newMockObjectStore(), backup).
			logSnapshotMode(zap.New(core))
		return logs.All()
	}

	t.Run("default configuration logs nothing", func(t *testing.T) {
		require.Empty(t, observe(config.BackupConfig{PgDumpPath: secretPath}))
	})

	t.Run("snapshot mode logs counts, flag and limit only", func(t *testing.T) {
		entries := observe(config.BackupConfig{
			PgDumpPath:       secretPath,
			ExcludeTableData: []string{secretTable, "other_table"},
			AgeRecipients:    []string{recipient},
			MaxArchiveBytes:  1 << 30,
		})
		require.Len(t, entries, 1)
		entry := entries[0]
		require.Equal(t, zapcore.InfoLevel, entry.Level)

		fields := entry.ContextMap()
		require.Equal(t, int64(2), fields["excluded_tables"])
		require.Equal(t, true, fields["encrypted"])
		require.Equal(t, int64(1<<30), fields["max_archive_bytes"])

		rendered := entry.Message
		for key, value := range fields {
			rendered += " " + key + "=" + fmt.Sprint(value)
		}
		for _, secret := range []string{secretTable, "other_table", secretPath, recipient, "age1"} {
			require.NotContains(t, rendered, secret)
		}
	})

	t.Run("each setting alone is announced", func(t *testing.T) {
		require.Len(t, observe(config.BackupConfig{ExcludeTableData: []string{"t"}}), 1)
		require.Len(t, observe(config.BackupConfig{AgeRecipients: []string{recipient}}), 1)
		require.Len(t, observe(config.BackupConfig{MaxArchiveBytes: 1}), 1)
	})
}
