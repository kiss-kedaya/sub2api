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

func (s *BackupService) createCompressedBackupFile(ctx context.Context) (string, int64, string, error) {
	return s.createCompressedBackupFileWithOptions(ctx, BackupOptions{})
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
	closeErr       error // what the reader's Close reports, like pg_dump's exit status
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
	return r.owner.closeErr
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
	dumper := stringDumper(knownDump)
	svc := newCriticalTestService(newMockSettingRepo(), dumper, newMockObjectStore(), config.BackupConfig{})

	path, size, _, err := svc.createCompressedBackupFile(context.Background())
	require.NoError(t, err)
	// pg_dump has just written EOF and may not have exited yet: cancelling its
	// context before Close would kill a healthy dump and fail a good backup.
	require.True(t, dumper.closed)
	require.NoError(t, dumper.ctxErrAtClose, "the dump context must still be alive when the reader is closed")
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
	require.NotContains(t, string(encoded), `"sha256"`)
	require.Empty(t, record.SHA256)
}

// ─── partial snapshots ───

func TestCreateCompressedBackupFile_PartialSnapshotUsesFastestGzip(t *testing.T) {
	isolateTempDir(t)
	identity := newAgeIdentity(t)
	svc := newCriticalTestService(newMockSettingRepo(), stringDumper(knownDump), newMockObjectStore(), config.BackupConfig{
		ExcludeTableData: []string{"public.big_table"},
		AgeRecipients:    recipientsOf(identity),
	})

	path, _, _, err := svc.createCompressedBackupFile(context.Background())
	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	inner := decryptArchive(t, raw, identity)
	require.Equal(t, byte(gzipXFLBestSpeed), inner[8])
	require.Equal(t, knownDump, string(gunzipBytes(t, inner)))
}

// ─── a critical snapshot is never plaintext ───

// A critical snapshot holds users, API keys and upstream credentials. Without a
// usable recipient the backup must fail before pg_dump starts, not fall back to a
// plaintext upload.
func TestCreateCompressedBackupFile_CriticalSnapshotWithoutRecipientFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		recipients []string
		wantErr    string
	}{
		{name: "unset", recipients: nil, wantErr: ErrBackupCriticalNeedsRecipient.Error()},
		{name: "empty list", recipients: []string{}, wantErr: ErrBackupCriticalNeedsRecipient.Error()},
		{name: "blank entry", recipients: []string{"  "}, wantErr: "not a valid age X25519 public key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := isolateTempDir(t)
			dumper := stringDumper(knownDump)
			svc := newCriticalTestService(newMockSettingRepo(), dumper, newMockObjectStore(), config.BackupConfig{
				ExcludeTableData: []string{"public.example_log_table"},
				AgeRecipients:    tt.recipients,
			})

			path, size, _, err := svc.createCompressedBackupFile(context.Background())

			require.ErrorContains(t, err, tt.wantErr)
			require.Empty(t, path)
			require.Zero(t, size)
			require.Zero(t, dumper.dumpCalls, "pg_dump must not even start")
			requireDirEmpty(t, dir)
		})
	}
}

func TestBackupService_CreateBackup_CriticalSnapshotWithoutRecipientUploadsNothing(t *testing.T) {
	dir := isolateTempDir(t)
	repo := newMockSettingRepo()
	seedS3Config(t, repo)
	store := &contentTypeStore{mockObjectStore: newMockObjectStore()}
	dumper := stringDumper(knownDump)
	svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{ExcludeTableData: []string{"public.example_log_table"}})

	record, err := svc.CreateBackup(context.Background(), "scheduled", 14)

	require.ErrorIs(t, err, ErrBackupCriticalNeedsRecipient)
	require.Equal(t, "failed", record.Status)
	require.Contains(t, record.ErrorMsg, "backup.age_recipients")
	require.Zero(t, dumper.dumpCalls)
	require.Empty(t, store.objects, "no plaintext object may reach the bucket")
	requireDirEmpty(t, dir)
}

// Encryption stays optional for a whole-database backup: only critical snapshots
// are forced to carry a recipient.
func TestCreateCompressedBackupFile_WholeDatabaseBackupStaysPlaintextWithoutRecipient(t *testing.T) {
	isolateTempDir(t)
	svc := newCriticalTestService(newMockSettingRepo(), stringDumper(knownDump), newMockObjectStore(), config.BackupConfig{})

	path, _, _, err := svc.createCompressedBackupFile(context.Background())
	require.NoError(t, err)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
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

			path, size, _, err := svc.createCompressedBackupFile(context.Background())
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

	path, size, _, err := svc.createCompressedBackupFile(context.Background())

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

	path, _, _, err := svc.createCompressedBackupFile(context.Background())

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
		{name: "critical snapshot", exclude: []string{"public.t"}, encrypt: true}, // a critical snapshot is always encrypted
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

			path, size, _, err := svc.createCompressedBackupFile(context.Background())

			// (c) sentinel error, message with limit and written bytes, file gone.
			require.ErrorIs(t, err, ErrBackupArchiveTooLarge)
			require.ErrorContains(t, err, "limit 4096 bytes")
			require.ErrorContains(t, err, "backup.max_archive_bytes")
			require.NotContains(t, err.Error(), "built-in", "an explicit limit is not the built-in one")
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

	path, exact, _, err := newService(0).createCompressedBackupFile(context.Background())
	require.NoError(t, err, "0 means unlimited")
	require.NoError(t, cleanupBackupFiles(path))
	require.Greater(t, exact, int64(0))

	path, size, _, err := newService(exact).createCompressedBackupFile(context.Background())
	require.NoError(t, err, "an archive of exactly the limit is allowed")
	require.Equal(t, exact, size)
	require.NoError(t, cleanupBackupFiles(path))

	_, _, _, err = newService(exact - 1).createCompressedBackupFile(context.Background())
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

// ─── built-in limit of a critical snapshot ───

// pg_dump ignores an exclude entry that matches nothing (a misspelt name, the
// children of a partitioned table), so a critical snapshot with a wrong list would
// stage the whole database on local disk. Without an explicit limit it gets one.
func TestBackupService_ArchiveLimit(t *testing.T) {
	exclude := []string{"public.t"}
	tests := []struct {
		name        string
		backup      config.BackupConfig
		wantLimit   int64
		wantBuiltin bool
	}{
		{name: "whole database, nothing set", backup: config.BackupConfig{}},
		{name: "whole database, explicit limit", backup: config.BackupConfig{MaxArchiveBytes: 5}, wantLimit: 5},
		{name: "critical snapshot, nothing set", backup: config.BackupConfig{ExcludeTableData: exclude},
			wantLimit: defaultCriticalMaxArchiveBytes, wantBuiltin: true},
		{name: "critical snapshot, explicit limit", backup: config.BackupConfig{ExcludeTableData: exclude, MaxArchiveBytes: 5}, wantLimit: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newCriticalTestService(newMockSettingRepo(), stringDumper(""), newMockObjectStore(), tt.backup)
			limit, builtin := svc.archiveLimit()
			require.Equal(t, tt.wantLimit, limit)
			require.Equal(t, tt.wantBuiltin, builtin)
		})
	}
	require.Equal(t, int64(10<<30), defaultCriticalMaxArchiveBytes, "the documented default is 10 GiB")
}

func TestCreateCompressedBackupFile_CriticalSnapshotStopsAtTheBuiltInLimit(t *testing.T) {
	dir := isolateTempDir(t)
	data := randomBytes(t, 4<<20) // incompressible, far larger than the limit
	source := &countingReader{r: bytes.NewReader(data)}
	dumper := &streamDumper{source: func() io.Reader { return source }}
	svc := newCriticalTestService(newMockSettingRepo(), dumper, newMockObjectStore(), config.BackupConfig{
		ExcludeTableData: []string{"public.t"},
		AgeRecipients:    recipientsOf(newAgeIdentity(t)),
	})
	svc.criticalMaxArchiveBytes = 4096 // no backup.max_archive_bytes: only the built-in limit is in force

	path, size, _, err := svc.createCompressedBackupFile(context.Background())

	require.ErrorIs(t, err, ErrBackupArchiveTooLarge)
	require.ErrorContains(t, err, "limit 4096 bytes")
	require.ErrorContains(t, err, "built-in default", "the message names which limit was hit")
	require.ErrorContains(t, err, "backup.max_archive_bytes", "and how to change it")
	require.Empty(t, path)
	require.Zero(t, size)
	requireDirEmpty(t, dir)
	require.Less(t, source.n, int64(len(data))/2, "the copy must stop soon after the limit is crossed")
	require.True(t, dumper.closed)
}

func TestCreateCompressedBackupFile_ExplicitLimitReplacesTheBuiltInOne(t *testing.T) {
	isolateTempDir(t)
	svc := newCriticalTestService(newMockSettingRepo(), stringDumper(knownDump), newMockObjectStore(), config.BackupConfig{
		ExcludeTableData: []string{"public.t"},
		AgeRecipients:    recipientsOf(newAgeIdentity(t)),
		MaxArchiveBytes:  1 << 30,
	})
	svc.criticalMaxArchiveBytes = 16 // would reject any archive if it still applied

	path, _, _, err := svc.createCompressedBackupFile(context.Background())
	require.NoError(t, err)
	require.NoError(t, cleanupBackupFiles(path))
}

func TestCreateCompressedBackupFile_WholeDatabaseBackupStaysUnlimited(t *testing.T) {
	isolateTempDir(t)
	data := randomBytes(t, 1<<20)
	dumper := &streamDumper{source: func() io.Reader { return bytes.NewReader(data) }}
	svc := newCriticalTestService(newMockSettingRepo(), dumper, newMockObjectStore(), config.BackupConfig{})
	svc.criticalMaxArchiveBytes = 16 // only a critical snapshot may ever be bound by it

	path, size, _, err := svc.createCompressedBackupFile(context.Background())
	require.NoError(t, err, "a whole-database backup keeps its \"no limit\"")
	require.Greater(t, size, int64(len(data))/2)
	require.NoError(t, cleanupBackupFiles(path))
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
		// There is no "critical without encryption" mode: a critical snapshot is
		// always encrypted (see ErrBackupCriticalNeedsRecipient).
		{name: "default", wantSuffix: ".sql.gz", wantContentType: "application/gzip"},
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

// A build that predates scope/encrypted (a rollback, or a mixed-version rollout)
// reads and rewrites the whole record list without them. The archive name still
// says .age, and that alone must keep the in-app restore away from psql.
func TestBackupService_RestoreRefusesEncryptedSnapshotWhoseFlagsWereLost(t *testing.T) {
	isolateTempDir(t)
	repo := newMockSettingRepo()
	seedS3Config(t, repo)
	store := newMockObjectStore()
	dumper := stringDumper(knownDump)
	svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{
		ExcludeTableData: []string{"public.t"},
		AgeRecipients:    recipientsOf(newAgeIdentity(t)),
	})
	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.True(t, record.Encrypted)
	require.Equal(t, backupScopeCritical, record.Scope)

	raw, err := repo.GetValue(context.Background(), settingKeyBackupRecords)
	require.NoError(t, err)
	var stored []map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	for _, entry := range stored {
		delete(entry, "scope")
		delete(entry, "encrypted")
	}
	rewritten, err := json.Marshal(stored)
	require.NoError(t, err)
	require.NoError(t, repo.Set(context.Background(), settingKeyBackupRecords, string(rewritten)))

	after, err := svc.GetBackupRecord(context.Background(), record.ID)
	require.NoError(t, err)
	require.Empty(t, after.Scope, "precondition: the flags are gone")
	require.False(t, after.Encrypted, "precondition: the flags are gone")

	require.ErrorIs(t, svc.RestoreBackup(context.Background(), record.ID), ErrBackupRestoreUnsupported)
	started, err := svc.StartRestore(context.Background(), record.ID)
	require.ErrorIs(t, err, ErrBackupRestoreUnsupported)
	require.Nil(t, started)
	svc.executeRestore(after, store)
	require.Zero(t, dumper.restoreCalls, "psql must never be reached")
}

func TestBackupRecord_RestoreUnsupportedReadsTheAgeSuffix(t *testing.T) {
	require.True(t, (&BackupRecord{FileName: "db_20261123_120000.sql.gz.age", S3Key: "backups/a.sql.gz"}).restoreUnsupported(),
		"the file name alone must be enough")
	require.True(t, (&BackupRecord{FileName: "a.sql.gz", S3Key: "backups/db_20261123_120000.sql.gz.age"}).restoreUnsupported(),
		"the object key alone must be enough")
	require.False(t, (&BackupRecord{FileName: "a.sql.gz", S3Key: "backups/a.sql.gz"}).restoreUnsupported(),
		"a plain whole-database backup stays restorable")
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
	tableName := "public.example_log_table"
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

	t.Run("critical snapshot logs the entries in effect, flags and the limit applied", func(t *testing.T) {
		entries := observe(config.BackupConfig{
			PgDumpPath:       secretPath,
			ExcludeTableData: []string{tableName, "other_table"},
			AgeRecipients:    []string{recipient},
			MaxArchiveBytes:  1 << 30,
		})
		require.Len(t, entries, 1)
		entry := entries[0]
		require.Equal(t, zapcore.InfoLevel, entry.Level)
		require.Equal(t, "[Backup] non-default backup settings in effect", entry.Message)

		fields := entry.ContextMap()
		require.Equal(t, true, fields["critical"])
		require.Equal(t, []any{tableName, "other_table"}, fields["exclude_table_data"],
			"the entries pg_dump will really get: the one place a misspelt name shows up")
		require.Equal(t, int64(2), fields["excluded_tables"])
		require.Equal(t, true, fields["encrypted"])
		require.Equal(t, int64(1<<30), fields["max_archive_bytes"])

		rendered := entry.Message
		for key, value := range fields {
			rendered += " " + key + "=" + fmt.Sprint(value)
		}
		for _, secret := range []string{secretPath, recipient, "age1"} {
			require.NotContains(t, rendered, secret, "recipients and paths never reach the log")
		}
	})

	t.Run("a critical snapshot without an explicit limit logs the built-in one", func(t *testing.T) {
		entries := observe(config.BackupConfig{ExcludeTableData: []string{"t"}})
		require.Len(t, entries, 1)
		require.Equal(t, defaultCriticalMaxArchiveBytes, entries[0].ContextMap()["max_archive_bytes"])
	})

	t.Run("a whole-database backup with only encryption is not called critical", func(t *testing.T) {
		entries := observe(config.BackupConfig{AgeRecipients: []string{recipient}})
		require.Len(t, entries, 1)
		fields := entries[0].ContextMap()
		require.Equal(t, false, fields["critical"])
		require.Equal(t, int64(0), fields["excluded_tables"])
		require.Equal(t, int64(0), fields["max_archive_bytes"], "no limit applies")
	})

	t.Run("each setting alone is announced", func(t *testing.T) {
		require.Len(t, observe(config.BackupConfig{ExcludeTableData: []string{"t"}}), 1)
		require.Len(t, observe(config.BackupConfig{AgeRecipients: []string{recipient}}), 1)
		require.Len(t, observe(config.BackupConfig{MaxArchiveBytes: 1}), 1)
	})
}
