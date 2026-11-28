//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// scopedStreamDumper implements ScopedDBDumper and records whether Dump or
// DumpWithOptions was invoked, along with the exact ExcludeTableData list.
type scopedStreamDumper struct {
	*streamDumper
	dumpWithOptionsCalls int
	lastExcludeTables    []string
}

func newScopedStreamDumper(content string) *scopedStreamDumper {
	return &scopedStreamDumper{streamDumper: stringDumper(content)}
}

func (d *scopedStreamDumper) DumpWithOptions(ctx context.Context, opts BackupDumpOptions) (io.ReadCloser, error) {
	d.dumpWithOptionsCalls++
	d.lastExcludeTables = append([]string(nil), opts.ExcludeTableData...)
	return d.Dump(ctx)
}

// createCompressedBackupFileWithOptions is a unit-test helper wrapping resolveBackupRun +
// createCompressedBackupFileForRun so production files carry no test-only unused methods.
func (s *BackupService) createCompressedBackupFileWithOptions(ctx context.Context, opts BackupOptions) (string, int64, string, error) {
	run, err := s.resolveBackupRun(ctx, opts)
	if err != nil {
		return "", 0, "", err
	}
	return s.createCompressedBackupFileForRun(ctx, run)
}

func TestDefaultCriticalExcludeTableData_Invariants(t *testing.T) {
	require.NotEmpty(t, DefaultCriticalExcludeTableData)

	pattern := regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*\.)?[A-Za-z_][A-Za-z0-9_*]*$`)
	seen := make(map[string]struct{}, len(DefaultCriticalExcludeTableData))
	for _, entry := range DefaultCriticalExcludeTableData {
		require.True(t, pattern.MatchString(entry), "invalid table pattern: %q", entry)
		_, dup := seen[entry]
		require.False(t, dup, "duplicate excluded table entry: %q", entry)
		seen[entry] = struct{}{}
	}

	// Must exclude the largest log/telemetry tables, including usage_logs partitions
	// and FK-dependent billing_usage_entries.
	for _, mustExclude := range []string{
		"public.usage_logs",
		"public.usage_logs_*",
		"public.billing_usage_entries",
		"public.usage_billing_dedup",
		"public.request_capture_records",
		"public.request_capture_tasks",
		"public.request_connection_traces",
		"public.small_request_blobs",
		"public.small_request_records",
		"public.small_request_responses",
		"public.channel_monitor_histories",
		"public.channel_monitor_v2_metrics_1m",
		"public.ops_error_logs",
		"public.ops_system_logs",
	} {
		require.Contains(t, seen, mustExclude, "expected high-volume table %q to be excluded", mustExclude)
	}

	// Must NEVER exclude core business, financial, auth, config, or singleton
	// state/watermark tables.
	for _, mustKeep := range []string{
		"public.users",
		"public.api_keys",
		"public.api_key_hashes",
		"public.api_key_secrets",
		"public.accounts",
		"public.account_groups",
		"public.groups",
		"public.channels",
		"public.proxies",
		"public.settings",
		"public.security_secrets",
		"public.payment_orders",
		"public.payment_audit_logs",
		"public.deposit_addresses",
		"public.deposit_transactions",
		"public.sweep_records",
		"public.billing_balance_settlements",
		"public.live_balance_adjustment_outbox",
		"public.live_balance_adjustment_heads",
		"public.user_debt",
		"public.user_debt_clawbacks",
		"public.user_subscriptions",
		"public.redeem_codes",
		"public.redeem_code_hashes",
		"public.promo_codes",
		"public.user_affiliates",
		"public.user_affiliate_ledger",
		"public.support_tickets",
		"public.support_ticket_messages",
		"public.auth_identities",
		"public.passkey_credentials",
		"public.audit_logs",
		"public.usage_group_rollup_state",
		"public.usage_dashboard_aggregation_watermark",
		"public.small_request_state",
		"public.schema_migrations",
	} {
		require.NotContains(t, seen, mustKeep, "core/state table %q must never be excluded", mustKeep)
	}
}

func TestBackupService_ScheduleCriticalOnlyPersistence(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(repo, &mockDumper{}, newMockObjectStore())
	svc.cronSched = cron.New()

	// Legacy JSON without critical_only defaults to false.
	require.NoError(t, repo.Set(context.Background(), settingKeyBackupSchedule, `{"enabled":true,"cron_expr":"0 3 * * *","retain_days":7,"retain_count":5}`))
	loaded, err := svc.GetSchedule(context.Background())
	require.NoError(t, err)
	require.False(t, loaded.CriticalOnly)
	require.Equal(t, 7, loaded.RetainDays)

	// Enabling critical_only persists cleanly.
	saved, err := svc.UpdateSchedule(context.Background(), BackupScheduleConfig{
		Enabled:      true,
		CronExpr:     "0 3 * * *",
		RetainDays:   7,
		RetainCount:  5,
		CriticalOnly: true,
	})
	require.NoError(t, err)
	require.True(t, saved.CriticalOnly)

	reloaded, err := svc.GetSchedule(context.Background())
	require.NoError(t, err)
	require.True(t, reloaded.CriticalOnly)

	// Disabling critical_only persists cleanly.
	_, err = svc.UpdateSchedule(context.Background(), BackupScheduleConfig{
		Enabled:      true,
		CronExpr:     "0 3 * * *",
		RetainDays:   7,
		RetainCount:  5,
		CriticalOnly: false,
	})
	require.NoError(t, err)
	reloaded, err = svc.GetSchedule(context.Background())
	require.NoError(t, err)
	require.False(t, reloaded.CriticalOnly)
}

func TestBackupService_RunScheduledBackup_RespectsCriticalOnlyToggle(t *testing.T) {
	t.Run("critical_only enabled without age recipients succeeds as plain critical snapshot", func(t *testing.T) {
		isolateTempDir(t)
		repo := newMockSettingRepo()
		seedS3Config(t, repo)
		store := &contentTypeStore{mockObjectStore: newMockObjectStore()}
		dumper := newScopedStreamDumper(knownDump)
		svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{})
		svc.cronSched = cron.New()

		_, err := svc.UpdateSchedule(context.Background(), BackupScheduleConfig{
			Enabled:      true,
			CronExpr:     "0 3 * * *",
			RetainDays:   7,
			RetainCount:  5,
			CriticalOnly: true,
		})
		require.NoError(t, err)

		svc.runScheduledBackup()

		require.Equal(t, 1, dumper.dumpWithOptionsCalls, "must invoke ScopedDBDumper.DumpWithOptions")
		require.Equal(t, DefaultCriticalExcludeTableData, dumper.lastExcludeTables)

		records, err := svc.ListBackups(context.Background())
		require.NoError(t, err)
		require.Len(t, records, 1)
		rec := records[0]
		require.Equal(t, "completed", rec.Status)
		require.Equal(t, "scheduled", rec.TriggeredBy)
		require.Equal(t, backupScopeCritical, rec.Scope)
		require.False(t, rec.Encrypted)
		require.True(t, strings.HasSuffix(rec.FileName, ".critical.sql.gz"), rec.FileName)
		require.True(t, strings.HasSuffix(rec.S3Key, ".critical.sql.gz"), rec.S3Key)
		require.Len(t, rec.SHA256, 64)
		require.Equal(t, []string{"application/gzip"}, store.contentTypes)

		object := store.objects[rec.S3Key]
		require.Equal(t, []byte{0x1f, 0x8b}, object[:2])
		require.Equal(t, byte(gzipXFLBestSpeed), object[8], "critical snapshot uses fastest gzip")
		require.Equal(t, knownDump, string(gunzipBytes(t, object)))

		// In-app online restore must refuse critical snapshots even when unencrypted.
		require.ErrorIs(t, svc.RestoreBackup(context.Background(), rec.ID), ErrBackupRestoreUnsupported)
		started, err := svc.StartRestore(context.Background(), rec.ID)
		require.ErrorIs(t, err, ErrBackupRestoreUnsupported)
		require.Nil(t, started)
		require.Zero(t, dumper.restoreCalls)
	})

	t.Run("critical_only enabled with age recipients encrypts archive", func(t *testing.T) {
		isolateTempDir(t)
		repo := newMockSettingRepo()
		seedS3Config(t, repo)
		store := &contentTypeStore{mockObjectStore: newMockObjectStore()}
		dumper := newScopedStreamDumper(knownDump)
		identity := newAgeIdentity(t)
		svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{
			AgeRecipients: recipientsOf(identity),
		})
		svc.cronSched = cron.New()

		_, err := svc.UpdateSchedule(context.Background(), BackupScheduleConfig{
			Enabled:      true,
			CronExpr:     "0 3 * * *",
			RetainDays:   7,
			RetainCount:  5,
			CriticalOnly: true,
		})
		require.NoError(t, err)

		svc.runScheduledBackup()

		require.Equal(t, 1, dumper.dumpWithOptionsCalls)
		require.Equal(t, DefaultCriticalExcludeTableData, dumper.lastExcludeTables)

		records, err := svc.ListBackups(context.Background())
		require.NoError(t, err)
		require.Len(t, records, 1)
		rec := records[0]
		require.Equal(t, "completed", rec.Status)
		require.Equal(t, backupScopeCritical, rec.Scope)
		require.True(t, rec.Encrypted)
		require.True(t, strings.HasSuffix(rec.FileName, ".critical.sql.gz.age"), rec.FileName)
		require.Equal(t, []string{"application/octet-stream"}, store.contentTypes)

		object := store.objects[rec.S3Key]
		inner := decryptArchive(t, object, identity)
		require.Equal(t, byte(gzipXFLBestSpeed), inner[8])
		require.Equal(t, knownDump, string(gunzipBytes(t, inner)))
	})

	t.Run("critical_only disabled keeps legacy full-database dump", func(t *testing.T) {
		isolateTempDir(t)
		repo := newMockSettingRepo()
		seedS3Config(t, repo)
		store := &contentTypeStore{mockObjectStore: newMockObjectStore()}
		dumper := newScopedStreamDumper(knownDump)
		svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{})
		svc.cronSched = cron.New()

		_, err := svc.UpdateSchedule(context.Background(), BackupScheduleConfig{
			Enabled:      true,
			CronExpr:     "0 3 * * *",
			RetainDays:   7,
			RetainCount:  5,
			CriticalOnly: false,
		})
		require.NoError(t, err)

		svc.runScheduledBackup()

		require.Zero(t, dumper.dumpWithOptionsCalls, "full dump uses legacy Dump()")
		require.Equal(t, 1, dumper.dumpCalls)

		records, err := svc.ListBackups(context.Background())
		require.NoError(t, err)
		require.Len(t, records, 1)
		rec := records[0]
		require.Equal(t, "completed", rec.Status)
		require.Empty(t, rec.Scope)
		require.False(t, rec.Encrypted)
		require.Empty(t, rec.SHA256)
		require.False(t, strings.Contains(rec.FileName, ".critical.sql.gz"))
		require.True(t, strings.HasSuffix(rec.FileName, ".sql.gz"))

		object := store.objects[rec.S3Key]
		require.Equal(t, byte(gzipXFLDefault), object[8])
	})
}

func TestBackupService_StartBackupWithOptions_CriticalOnlyAndSizeLimit(t *testing.T) {
	t.Run("async manual critical backup records scope and digest", func(t *testing.T) {
		isolateTempDir(t)
		repo := newMockSettingRepo()
		seedS3Config(t, repo)
		store := newMockObjectStore()
		dumper := newScopedStreamDumper(knownDump)
		svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{})

		started, err := svc.StartBackupWithOptions(context.Background(), "manual", 7, BackupOptions{CriticalOnly: true})
		require.NoError(t, err)
		require.Equal(t, backupScopeCritical, started.Scope)
		require.True(t, strings.HasSuffix(started.FileName, ".critical.sql.gz"))
		svc.wg.Wait()

		final, err := svc.GetBackupRecord(context.Background(), started.ID)
		require.NoError(t, err)
		require.Equal(t, "completed", final.Status)
		require.Equal(t, backupScopeCritical, final.Scope)
		require.Len(t, final.SHA256, 64)
		require.Equal(t, 1, dumper.dumpWithOptionsCalls)
		require.Equal(t, DefaultCriticalExcludeTableData, dumper.lastExcludeTables)
	})

	t.Run("ui critical backup enforces built-in critical size limit", func(t *testing.T) {
		dir := isolateTempDir(t)
		repo := newMockSettingRepo()
		seedS3Config(t, repo)
		store := newMockObjectStore()
		data := randomBytes(t, 1<<20)
		dumper := &scopedStreamDumper{streamDumper: &streamDumper{source: func() io.Reader { return bytes.NewReader(data) }}}
		svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{})
		svc.criticalMaxArchiveBytes = 4096

		record, err := svc.CreateBackupWithOptions(context.Background(), "manual", 7, BackupOptions{CriticalOnly: true})
		require.ErrorIs(t, err, ErrBackupArchiveTooLarge)
		require.Equal(t, "failed", record.Status)
		require.Contains(t, record.ErrorMsg, "built-in default")
		require.Empty(t, store.objects)
		requireDirEmpty(t, dir)
	})
}

func TestBackupService_ServerExcludeTableDataTakesPrecedenceAndFailsClosed(t *testing.T) {
	t.Run("custom server exclude list overrides built-in list even when CriticalOnly is false", func(t *testing.T) {
		isolateTempDir(t)
		repo := newMockSettingRepo()
		seedS3Config(t, repo)
		store := newMockObjectStore()
		dumper := newScopedStreamDumper(knownDump)
		identity := newAgeIdentity(t)
		customExcludes := []string{"public.custom_big_table"}
		svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{
			ExcludeTableData: customExcludes,
			AgeRecipients:    recipientsOf(identity),
		})
		require.True(t, svc.ServerCriticalModeForced())
		require.True(t, svc.AgeEncryptionEnabled())

		record, err := svc.CreateBackupWithOptions(context.Background(), "manual", 7, BackupOptions{CriticalOnly: false})
		require.NoError(t, err)
		require.Equal(t, backupScopeCritical, record.Scope)
		require.True(t, record.Encrypted)
		require.Equal(t, customExcludes, dumper.lastExcludeTables)
	})

	t.Run("server exclude list without age recipients still fails closed even with CriticalOnly=true", func(t *testing.T) {
		isolateTempDir(t)
		repo := newMockSettingRepo()
		seedS3Config(t, repo)
		store := newMockObjectStore()
		dumper := newScopedStreamDumper(knownDump)
		svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{
			ExcludeTableData: []string{"public.custom_big_table"},
		})

		record, err := svc.CreateBackupWithOptions(context.Background(), "manual", 7, BackupOptions{CriticalOnly: true})
		require.ErrorIs(t, err, ErrBackupCriticalNeedsRecipient)
		require.Equal(t, "failed", record.Status)
		require.Zero(t, dumper.dumpCalls)
		require.Empty(t, store.objects)
	})
}

func TestBackupService_RestoreRefusesPlainCriticalSnapshotWhoseScopeWasLost(t *testing.T) {
	isolateTempDir(t)
	repo := newMockSettingRepo()
	seedS3Config(t, repo)
	store := newMockObjectStore()
	dumper := newScopedStreamDumper(knownDump)
	svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{})

	record, err := svc.CreateBackupWithOptions(context.Background(), "manual", 7, BackupOptions{CriticalOnly: true})
	require.NoError(t, err)
	require.Equal(t, backupScopeCritical, record.Scope)
	require.False(t, record.Encrypted)

	// Simulate an older rollback binary stripping the "scope" field from JSON.
	raw, err := repo.GetValue(context.Background(), settingKeyBackupRecords)
	require.NoError(t, err)
	var stored []map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	for _, entry := range stored {
		delete(entry, "scope")
	}
	rewritten, err := json.Marshal(stored)
	require.NoError(t, err)
	require.NoError(t, repo.Set(context.Background(), settingKeyBackupRecords, string(rewritten)))

	after, err := svc.GetBackupRecord(context.Background(), record.ID)
	require.NoError(t, err)
	require.Empty(t, after.Scope)

	require.ErrorIs(t, svc.RestoreBackup(context.Background(), record.ID), ErrBackupRestoreUnsupported)
	started, err := svc.StartRestore(context.Background(), record.ID)
	require.ErrorIs(t, err, ErrBackupRestoreUnsupported)
	require.Nil(t, started)
	svc.executeRestore(after, store)
	require.Zero(t, dumper.restoreCalls)
}

func TestBackupService_StartSweepsStaleFilesWhenScheduleCriticalOnlyEnabled(t *testing.T) {
	dir := isolateTempDir(t)
	old := time.Now().Add(-staleBackupFileAge - time.Hour)
	stale := writeStagedFile(t, dir, "sub2api-backup-stage-777.sql.gz", old)

	repo := newMockSettingRepo()
	require.NoError(t, repo.Set(context.Background(), settingKeyBackupSchedule, `{"enabled":true,"cron_expr":"0 3 * * *","retain_days":7,"retain_count":5,"critical_only":true}`))
	svc := newCriticalTestService(repo, stringDumper(""), newMockObjectStore(), config.BackupConfig{})

	svc.Start()
	svc.Stop()

	_, err := os.Stat(stale)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestBackupService_RuntimeCriticalOnlyFailsClosedWhenDumperNotScoped(t *testing.T) {
	t.Run("plain DBDumper without ScopedDBDumper refuses runtime critical backup", func(t *testing.T) {
		dir := isolateTempDir(t)
		repo := newMockSettingRepo()
		seedS3Config(t, repo)
		store := newMockObjectStore()
		dumper := stringDumper(knownDump) // implements DBDumper only, not ScopedDBDumper
		svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{})

		record, err := svc.CreateBackupWithOptions(context.Background(), "manual", 7, BackupOptions{CriticalOnly: true})
		require.ErrorIs(t, err, ErrBackupScopedDumperUnsupported)
		require.NotNil(t, record)
		require.Equal(t, "failed", record.Status)
		require.Zero(t, dumper.dumpCalls, "must never silently fall back to full Dump()")
		require.Empty(t, store.objects)
		requireDirEmpty(t, dir)
	})

	t.Run("nil dumper returns error instead of panicking", func(t *testing.T) {
		dir := isolateTempDir(t)
		repo := newMockSettingRepo()
		seedS3Config(t, repo)
		store := newMockObjectStore()
		svc := newCriticalTestService(repo, nil, store, config.BackupConfig{})

		record, err := svc.CreateBackupWithOptions(context.Background(), "manual", 7, BackupOptions{CriticalOnly: true})
		require.Error(t, err)
		require.NotNil(t, record)
		require.Equal(t, "failed", record.Status)
		require.Empty(t, store.objects)
		requireDirEmpty(t, dir)
	})
}

func TestBackupService_RunScheduledBackup_AbortsWhenScheduleReadFailsOrCorrupt(t *testing.T) {
	t.Run("transient DB error aborts scheduled run instead of falling back to full dump", func(t *testing.T) {
		isolateTempDir(t)
		repo := newMockSettingRepo()
		seedS3Config(t, repo)
		repo.getValueErr = errors.New("db connection reset")
		store := newMockObjectStore()
		dumper := newScopedStreamDumper(knownDump)
		svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{})

		_, err := svc.GetSchedule(context.Background())
		require.ErrorContains(t, err, "db connection reset")

		svc.runScheduledBackup()
		require.Zero(t, dumper.dumpCalls)
		require.Zero(t, dumper.dumpWithOptionsCalls)
		require.Empty(t, store.objects)
	})

	t.Run("corrupted schedule JSON aborts scheduled run instead of falling back to full dump", func(t *testing.T) {
		isolateTempDir(t)
		repo := newMockSettingRepo()
		seedS3Config(t, repo)
		require.NoError(t, repo.Set(context.Background(), settingKeyBackupSchedule, `{not-valid-json`))
		store := newMockObjectStore()
		dumper := newScopedStreamDumper(knownDump)
		svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{})

		_, err := svc.GetSchedule(context.Background())
		require.ErrorIs(t, err, ErrBackupScheduleCorrupt)

		svc.runScheduledBackup()
		require.Zero(t, dumper.dumpCalls)
		require.Zero(t, dumper.dumpWithOptionsCalls)
		require.Empty(t, store.objects)
	})

	t.Run("ErrSettingNotFound returns empty default schedule", func(t *testing.T) {
		repo := newMockSettingRepo()
		repo.getValueErr = ErrSettingNotFound
		svc := newCriticalTestService(repo, newScopedStreamDumper(knownDump), newMockObjectStore(), config.BackupConfig{})

		cfg, err := svc.GetSchedule(context.Background())
		require.NoError(t, err)
		require.Equal(t, &BackupScheduleConfig{}, cfg)
	})
}

func TestBackupRecord_RestoreUnsupportedUsesExactCriticalSuffix(t *testing.T) {
	require.True(t, (&BackupRecord{FileName: "db_20261126_030000.critical.sql.gz"}).restoreUnsupported())
	require.True(t, (&BackupRecord{FileName: "db_20261126_030000.critical.sql.gz.age"}).restoreUnsupported())
	require.True(t, (&BackupRecord{S3Key: "backups/2026/11/26/db_20261126_030000.critical.sql.gz"}).restoreUnsupported())
	require.False(t, (&BackupRecord{
		FileName: "db_20261126_030000.sql.gz",
		S3Key:    "backups/.critical.sql.gz/2026/11/26/db_20261126_030000.sql.gz",
	}).restoreUnsupported(), "prefix containing .critical.sql.gz must not falsely classify a full backup as critical")
}

func TestBackupService_ResolveBackupModeDoesNotAliasDefaultExcludeSlice(t *testing.T) {
	svc := newCriticalTestService(newMockSettingRepo(), newScopedStreamDumper(knownDump), newMockObjectStore(), config.BackupConfig{})
	originalFirst := DefaultCriticalExcludeTableData[0]
	mode := svc.resolveBackupMode(BackupOptions{CriticalOnly: true})
	require.NotEmpty(t, mode.excludeTables)
	mode.excludeTables[0] = "public.mutated_entry"
	require.Equal(t, originalFirst, DefaultCriticalExcludeTableData[0])
}

func TestBackupService_AdminAgeRecipients_ValidationAndPersistence(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(repo, &mockDumper{}, newMockObjectStore())
	svc.cronSched = cron.New()

	id1, id2 := newAgeIdentity(t), newAgeIdentity(t)
	pub1, pub2 := id1.Recipient().String(), id2.Recipient().String()

	// Trims whitespace, drops blank lines, deduplicates preserving order, and saves max_rate_mbps.
	saved, err := svc.UpdateSchedule(context.Background(), BackupScheduleConfig{
		Enabled:       false,
		CronExpr:      "0 3 * * *",
		RetainDays:    14,
		RetainCount:   10,
		CriticalOnly:  true,
		AgeRecipients: []string{"  " + pub1 + "  ", "", pub2, pub1},
		MaxRateMBps:   15,
	})
	require.NoError(t, err)
	require.Equal(t, []string{pub1, pub2}, saved.AgeRecipients)
	require.Equal(t, 15, saved.MaxRateMBps)
	require.True(t, svc.AgeEncryptionEnabledForSchedule(saved))
	require.False(t, svc.ServerAgeEncryptionForced())

	reloaded, err := svc.GetSchedule(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{pub1, pub2}, reloaded.AgeRecipients)
	require.Equal(t, 15, reloaded.MaxRateMBps)

	// Rejects age private key (both uppercase, lowercase, and full multi-line age-keygen paste) without echoing the secret.
	secret := id1.String()
	for _, attempt := range [][]string{
		{secret},
		{strings.ToLower(secret)},
		{"# created: 2026-11-27T00:00:00Z", "# public key: " + pub1, secret},
	} {
		_, err = svc.UpdateSchedule(context.Background(), BackupScheduleConfig{
			AgeRecipients: attempt,
		})
		require.ErrorIs(t, err, ErrBackupAgePrivateKeyForbidden)
		require.Equal(t, "BACKUP_AGE_PRIVATE_KEY_FORBIDDEN", infraerrors.Reason(err))
		require.NotContains(t, err.Error(), secret)
		require.NotContains(t, err.Error(), strings.ToLower(secret))
	}

	// Audit log redaction wipes age_recipients and any embedded AGE-SECRET-KEY-* values.
	redactedScheduleBody := RedactAuditBody([]byte(`{"enabled":true,"age_recipients":["`+secret+`"],"note":"`+secret+`"}`), "application/json")
	require.NotContains(t, redactedScheduleBody, secret)
	require.Contains(t, redactedScheduleBody, `"age_recipients":"***"`)
	require.Contains(t, redactedScheduleBody, `"note":"***"`)

	// Rejects invalid public key without echoing input.
	badKey := "age1invalid-not-bech32-key"
	_, err = svc.UpdateSchedule(context.Background(), BackupScheduleConfig{
		AgeRecipients: []string{badKey},
	})
	require.Error(t, err)
	require.Equal(t, "BACKUP_AGE_RECIPIENT_INVALID", infraerrors.Reason(err))
	require.NotContains(t, err.Error(), badKey)

	// Rejects > maxBackupAgeRecipients keys.
	tooMany := make([]string, 0, maxBackupAgeRecipients+1)
	for i := 0; i < maxBackupAgeRecipients+1; i++ {
		tooMany = append(tooMany, newAgeIdentity(t).Recipient().String())
	}
	_, err = svc.UpdateSchedule(context.Background(), BackupScheduleConfig{
		AgeRecipients: tooMany,
	})
	require.Error(t, err)
	require.Equal(t, "BACKUP_AGE_RECIPIENTS_TOO_MANY", infraerrors.Reason(err))

	// Rejects out-of-range max_rate_mbps.
	for _, badRate := range []int{-1, maxBackupRateMBps + 1} {
		_, err = svc.UpdateSchedule(context.Background(), BackupScheduleConfig{
			MaxRateMBps: badRate,
		})
		require.Error(t, err)
		require.Equal(t, "INVALID_BACKUP_RATE_LIMIT", infraerrors.Reason(err))
	}

	// Clearing age_recipients disables DB encryption cleanly.
	cleared, err := svc.UpdateSchedule(context.Background(), BackupScheduleConfig{
		Enabled:       false,
		CronExpr:      "0 3 * * *",
		RetainDays:    14,
		RetainCount:   10,
		AgeRecipients: []string{"  "},
		MaxRateMBps:   0,
	})
	require.NoError(t, err)
	require.Nil(t, cleared.AgeRecipients)
	require.Zero(t, cleared.MaxRateMBps)
	require.False(t, svc.AgeEncryptionEnabledForSchedule(cleared))
}

func TestBackupService_AdminAgeRecipients_EncryptsScheduledAndManualBackups(t *testing.T) {
	isolateTempDir(t)
	repo := newMockSettingRepo()
	seedS3Config(t, repo)
	store := &contentTypeStore{mockObjectStore: newMockObjectStore()}
	dumper := newScopedStreamDumper(knownDump)
	svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{})
	svc.cronSched = cron.New()

	adminID, stranger := newAgeIdentity(t), newAgeIdentity(t)
	_, err := svc.UpdateSchedule(context.Background(), BackupScheduleConfig{
		Enabled:       true,
		CronExpr:      "0 3 * * *",
		RetainDays:    7,
		RetainCount:   5,
		CriticalOnly:  true,
		AgeRecipients: recipientsOf(adminID),
		MaxRateMBps:   20,
	})
	require.NoError(t, err)

	// 1) Scheduled critical backup uses DB age_recipients.
	svc.runScheduledBackup()
	records, err := svc.ListBackups(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)
	schedRec := records[0]
	require.Equal(t, "completed", schedRec.Status)
	require.Equal(t, backupScopeCritical, schedRec.Scope)
	require.True(t, schedRec.Encrypted)
	require.True(t, strings.HasSuffix(schedRec.FileName, ".critical.sql.gz.age"), schedRec.FileName)
	require.Len(t, schedRec.SHA256, 64)
	require.Equal(t, "application/octet-stream", store.contentTypes[0])

	schedObj := store.objects[schedRec.S3Key]
	require.NotContains(t, string(schedObj), "COPY public.x")
	require.Equal(t, knownDump, string(gunzipBytes(t, decryptArchive(t, schedObj, adminID))))
	_, err = age.Decrypt(bytes.NewReader(schedObj), stranger)
	require.Error(t, err)
	require.ErrorIs(t, svc.RestoreBackup(context.Background(), schedRec.ID), ErrBackupRestoreUnsupported)

	// 2) Manual async whole-database backup ALSO uses DB age_recipients.
	started, err := svc.StartBackupWithOptions(context.Background(), "manual", 7, BackupOptions{CriticalOnly: false})
	require.NoError(t, err)
	require.True(t, started.Encrypted)
	require.Empty(t, started.Scope)
	require.True(t, strings.HasSuffix(started.FileName, ".sql.gz.age"), started.FileName)
	svc.wg.Wait()

	manualRec, err := svc.GetBackupRecord(context.Background(), started.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", manualRec.Status)
	require.True(t, manualRec.Encrypted)
	require.Len(t, manualRec.SHA256, 64)
	manualObj := store.objects[manualRec.S3Key]
	require.Equal(t, knownDump, string(gunzipBytes(t, decryptArchive(t, manualObj, adminID))))
	require.ErrorIs(t, svc.RestoreBackup(context.Background(), manualRec.ID), ErrBackupRestoreUnsupported)

	// 3) Clearing DB age_recipients returns to plaintext backups.
	_, err = svc.UpdateSchedule(context.Background(), BackupScheduleConfig{
		Enabled:      false,
		CronExpr:     "0 3 * * *",
		RetainDays:   7,
		RetainCount:  5,
		CriticalOnly: false,
	})
	require.NoError(t, err)

	plainRec, err := svc.CreateBackupWithOptions(context.Background(), "manual", 7, BackupOptions{CriticalOnly: false})
	require.NoError(t, err)
	require.False(t, plainRec.Encrypted)
	require.True(t, strings.HasSuffix(plainRec.FileName, ".sql.gz"))
	require.False(t, strings.HasSuffix(plainRec.FileName, ".age"))
	require.Equal(t, knownDump, string(gunzipBytes(t, store.objects[plainRec.S3Key])))
}

func TestBackupService_ServerAgeRecipientsCannotBeRemovedBySchedule(t *testing.T) {
	isolateTempDir(t)
	repo := newMockSettingRepo()
	seedS3Config(t, repo)
	store := newMockObjectStore()
	dumper := newScopedStreamDumper(knownDump)
	serverID, adminID, stranger := newAgeIdentity(t), newAgeIdentity(t), newAgeIdentity(t)

	svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{
		AgeRecipients: recipientsOf(serverID),
	})
	require.True(t, svc.ServerAgeEncryptionForced())
	require.Equal(t, 1, svc.ServerAgeRecipientCount())

	// Even when admin saves empty age_recipients, server env key remains enforced.
	saved, err := svc.UpdateSchedule(context.Background(), BackupScheduleConfig{
		AgeRecipients: nil,
	})
	require.NoError(t, err)
	require.True(t, svc.AgeEncryptionEnabledForSchedule(saved))

	rec1, err := svc.CreateBackup(context.Background(), "manual", 7)
	require.NoError(t, err)
	require.True(t, rec1.Encrypted)
	require.Equal(t, knownDump, string(gunzipBytes(t, decryptArchive(t, store.objects[rec1.S3Key], serverID))))

	// Adding an admin recipient encrypts to BOTH server key and admin key.
	_, err = svc.UpdateSchedule(context.Background(), BackupScheduleConfig{
		AgeRecipients: recipientsOf(adminID),
	})
	require.NoError(t, err)

	rec2, err := svc.CreateBackup(context.Background(), "manual", 7)
	require.NoError(t, err)
	require.True(t, rec2.Encrypted)
	obj2 := store.objects[rec2.S3Key]
	require.Equal(t, knownDump, string(gunzipBytes(t, decryptArchive(t, obj2, serverID))))
	require.Equal(t, knownDump, string(gunzipBytes(t, decryptArchive(t, obj2, adminID))))
	_, err = age.Decrypt(bytes.NewReader(obj2), stranger)
	require.Error(t, err)
}

func TestBackupService_ManualBackupFailsClosedOnCorruptOrTamperedSchedule(t *testing.T) {
	t.Run("corrupt schedule JSON blocks both CreateBackup and StartBackup", func(t *testing.T) {
		dir := isolateTempDir(t)
		repo := newMockSettingRepo()
		seedS3Config(t, repo)
		require.NoError(t, repo.Set(context.Background(), settingKeyBackupSchedule, `{not-valid-json`))
		store := newMockObjectStore()
		dumper := newScopedStreamDumper(knownDump)
		svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{})

		_, err := svc.StartBackupWithOptions(context.Background(), "manual", 7, BackupOptions{})
		require.ErrorIs(t, err, ErrBackupScheduleCorrupt)
		_, err = svc.CreateBackupWithOptions(context.Background(), "manual", 7, BackupOptions{})
		require.ErrorIs(t, err, ErrBackupScheduleCorrupt)
		require.Zero(t, dumper.dumpCalls)
		require.Zero(t, dumper.dumpWithOptionsCalls)
		require.Empty(t, store.objects)
		requireDirEmpty(t, dir)
	})

	t.Run("tampered DB schedule with private key or invalid key fails closed before pg_dump", func(t *testing.T) {
		secret := newAgeIdentity(t).String()
		for _, tc := range []struct {
			name        string
			rawSchedule string
			wantErr     error
			forbidSub   string
		}{
			{
				name:        "private key in DB",
				rawSchedule: `{"age_recipients":["` + secret + `"]}`,
				wantErr:     ErrBackupScheduleCorrupt,
				forbidSub:   secret,
			},
			{
				name:        "invalid public key in DB",
				rawSchedule: `{"age_recipients":["age1tampered-bad-key"]}`,
				forbidSub:   "age1tampered-bad-key",
			},
			{
				name:        "blank entry in DB",
				rawSchedule: `{"age_recipients":["   "]}`,
			},
			{
				name:        "negative max_rate_mbps in DB",
				rawSchedule: `{"max_rate_mbps":-5}`,
				wantErr:     ErrBackupScheduleCorrupt,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dir := isolateTempDir(t)
				repo := newMockSettingRepo()
				seedS3Config(t, repo)
				require.NoError(t, repo.Set(context.Background(), settingKeyBackupSchedule, tc.rawSchedule))
				store := newMockObjectStore()
				dumper := newScopedStreamDumper(knownDump)
				svc := newCriticalTestService(repo, dumper, store, config.BackupConfig{})

				_, err := svc.StartBackupWithOptions(context.Background(), "manual", 7, BackupOptions{})
				require.Error(t, err)
				if tc.wantErr != nil {
					require.ErrorIs(t, err, tc.wantErr)
				}
				if tc.forbidSub != "" {
					require.NotContains(t, err.Error(), tc.forbidSub)
				}
				if tc.name == "private key in DB" {
					// GetSchedule must never echo a tampered private key on secret-free GET.
					_, getErr := svc.GetSchedule(context.Background())
					require.ErrorIs(t, getErr, ErrBackupScheduleCorrupt)
					require.NotContains(t, getErr.Error(), tc.forbidSub)
				}
				require.Zero(t, dumper.dumpCalls)
				require.Zero(t, dumper.dumpWithOptionsCalls)
				require.Empty(t, store.objects)
				requireDirEmpty(t, dir)
			})
		}
	})
}

func TestBackupService_StartSweepsStaleFilesWhenScheduleAgeRecipientsEnabled(t *testing.T) {
	dir := isolateTempDir(t)
	old := time.Now().Add(-staleBackupFileAge - time.Hour)
	stale := writeStagedFile(t, dir, "sub2api-backup-stage-888.sql.gz.age", old)

	repo := newMockSettingRepo()
	pub := newAgeIdentity(t).Recipient().String()
	require.NoError(t, repo.Set(context.Background(), settingKeyBackupSchedule, `{"enabled":false,"age_recipients":["`+pub+`"]}`))
	svc := newCriticalTestService(repo, stringDumper(""), newMockObjectStore(), config.BackupConfig{})

	svc.Start()
	svc.Stop()

	_, err := os.Stat(stale)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRateLimitedReader_PacesAndHonorsCancellation(t *testing.T) {
	t.Run("non-positive rate returns original reader directly", func(t *testing.T) {
		orig := strings.NewReader("abc")
		require.Same(t, orig, newRateLimitedReader(context.Background(), orig, 0))
		require.Same(t, orig, newRateLimitedReader(context.Background(), orig, -10))
	})

	t.Run("paces reads accurately while preserving data", func(t *testing.T) {
		payload := bytes.Repeat([]byte("A"), 96*1024) // 3 chunks of 32 KiB
		// 192 KiB/s => each 32 KiB chunk costs ~166ms; 3 chunks require 2 waits (~333ms).
		start := time.Now()
		got, err := io.ReadAll(newRateLimitedReader(context.Background(), bytes.NewReader(payload), 192*1024))
		elapsed := time.Since(start)
		require.NoError(t, err)
		require.Equal(t, payload, got)
		require.GreaterOrEqual(t, elapsed, 250*time.Millisecond)
	})

	t.Run("context cancellation aborts wait promptly", func(t *testing.T) {
		payload := bytes.Repeat([]byte("B"), 128*1024)
		ctx, cancel := context.WithCancel(context.Background())
		// 32 KiB/s => second chunk would wait 1s unless cancelled.
		rl := newRateLimitedReader(ctx, bytes.NewReader(payload), 32*1024)
		buf := make([]byte, 32*1024)
		n, err := rl.Read(buf)
		require.NoError(t, err)
		require.Equal(t, 32*1024, n)

		go func() {
			time.Sleep(30 * time.Millisecond)
			cancel()
		}()
		start := time.Now()
		_, err = rl.Read(buf)
		elapsed := time.Since(start)
		require.ErrorIs(t, err, context.Canceled)
		require.Less(t, elapsed, 300*time.Millisecond)
	})
}
