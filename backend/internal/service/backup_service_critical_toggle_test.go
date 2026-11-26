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

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
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
