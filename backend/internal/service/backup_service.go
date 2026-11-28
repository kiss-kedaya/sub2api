package service

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	settingKeyBackupS3Config = "backup_s3_config"
	settingKeyBackupSchedule = "backup_schedule"
	settingKeyBackupRecords  = "backup_records"

	maxBackupRecords           = 100
	backupObjectCleanupTimeout = 2 * time.Minute

	// maxBackupRetainCount is the largest retain_count the record list can honour:
	// one slot is always taken by the backup in flight.
	maxBackupRetainCount = maxBackupRecords - 1

	// maxBackupAgeRecipients caps the number of admin-configured age X25519 public
	// keys stored in backup_schedule to prevent bloated settings rows and headers.
	maxBackupAgeRecipients = 16

	// maxBackupRateMBps caps the admin-configured dump throughput limit (MB/s).
	// 0 means unlimited.
	maxBackupRateMBps = 500

	// backupRateChunkBytes bounds each Read passed through rateLimitedReader so
	// throttling paces pg_dump smoothly without large bursts.
	backupRateChunkBytes = 32 * 1024

	// backupScopeCritical marks a snapshot taken with backup.exclude_table_data:
	// every table's schema is present but the excluded tables' rows are not.
	backupScopeCritical = "critical"

	// defaultCriticalMaxArchiveBytes bounds a critical snapshot when the operator
	// set no backup.max_archive_bytes. pg_dump does not complain about an exclude
	// entry that matches nothing (a misspelt name, or the children of a partitioned
	// table), and the archive is staged on local disk before it is uploaded, so an
	// unbounded snapshot would keep writing until the disk fills up. A
	// whole-database backup, which has no exclude list, keeps its "no limit".
	defaultCriticalMaxArchiveBytes int64 = 10 << 30

	// Every file a backup stages in the temp directory (the archive, and its parts
	// when it is split) starts with staleBackupFilePrefix, and the startup sweep
	// only ever touches files that do. The prefix is one nobody types by hand: an
	// operator's own sub2api-backup-<date>.sql.gz left in /tmp must survive a
	// restart of a service that runs as root without a private /tmp.
	// A staged file older than staleBackupFileAge was left behind by a process that
	// died mid-backup (a backup is bounded by a 30 minute context).
	staleBackupFilePrefix = "sub2api-backup-stage-"
	staleBackupFileAge    = 2 * time.Hour

	// backupScheduledLeaderLockKey gates the scheduled full-database backup so
	// that only one instance in a clustered deployment performs the
	// dump-and-upload each cycle. Without it every instance runs the cron
	// independently, producing N concurrent pg_dumps against the same database,
	// N× peak memory while the archive is uploaded, and N identical objects that
	// overwrite the same timestamped key. Every other periodic job in this
	// package is already gated the same way; the scheduled backup was the last
	// one that still fanned out across every instance.
	backupScheduledLeaderLockKey = "backup:scheduled:leader"
	// backupScheduledLeaderLockTTL bounds crash recovery only; the lock is
	// released as soon as the backup finishes. It must exceed the job's
	// worst-case runtime (the scheduled backup context is bounded at 30m) so the
	// lock cannot expire mid-dump and let a peer start a second backup.
	backupScheduledLeaderLockTTL = 35 * time.Minute
)

var (
	ErrBackupS3NotConfigured = infraerrors.BadRequest("BACKUP_S3_NOT_CONFIGURED", "backup S3 storage is not configured")
	ErrBackupNotFound        = infraerrors.NotFound("BACKUP_NOT_FOUND", "backup record not found")
	ErrBackupInProgress      = infraerrors.Conflict("BACKUP_IN_PROGRESS", "a backup is already in progress")
	ErrRestoreInProgress     = infraerrors.Conflict("RESTORE_IN_PROGRESS", "a restore is already in progress")
	ErrBackupRecordsCorrupt  = infraerrors.InternalServer("BACKUP_RECORDS_CORRUPT", "backup records data is corrupted")
	ErrBackupS3ConfigCorrupt = infraerrors.InternalServer("BACKUP_S3_CONFIG_CORRUPT", "backup S3 config data is corrupted")

	// ErrBackupRestoreUnsupported is returned for snapshots that the in-app
	// restore must never replay: a partial (critical) snapshot lacks the data of
	// the excluded tables, and an encrypted archive is not gzip. Both are restored
	// by hand into an empty database.
	ErrBackupRestoreUnsupported = infraerrors.BadRequest("BACKUP_RESTORE_UNSUPPORTED", "partial or encrypted snapshots must be restored manually into an empty database")

	// ErrBackupCriticalNeedsRecipient is returned, before pg_dump starts, for a
	// critical snapshot that has no age recipient: it holds users, API keys and
	// upstream credentials and is never uploaded in plaintext. Whole-database
	// backups keep their optional encryption.
	ErrBackupCriticalNeedsRecipient = errors.New("a critical snapshot is never stored in plaintext: backup.age_recipients needs at least one age X25519 public key")

	// ErrBackupArchiveTooLarge is wrapped by the error returned when the archive
	// on disk grows past the size limit: backup.max_archive_bytes, or the built-in
	// limit of a critical snapshot that has none. The wrapping error says which.
	ErrBackupArchiveTooLarge = errors.New("backup archive exceeds the size limit")

	// ErrBackupScopedDumperUnsupported is returned when runtime critical-only
	// table exclusions are requested but the configured DBDumper does not
	// implement ScopedDBDumper, preventing silent fallback to a full dump.
	ErrBackupScopedDumperUnsupported = errors.New("database dumper does not support runtime table exclusions for critical-only backup")

	// ErrBackupScheduleCorrupt is returned when the persisted backup schedule JSON
	// cannot be unmarshaled.
	ErrBackupScheduleCorrupt = infraerrors.InternalServer("BACKUP_SCHEDULE_CORRUPT", "backup schedule config data is corrupted")

	// ErrBackupAgePrivateKeyForbidden rejects an age private key (AGE-SECRET-KEY-...)
	// pasted into an age_recipients field; the server must only ever store public keys.
	ErrBackupAgePrivateKeyForbidden = infraerrors.BadRequest(
		"BACKUP_AGE_PRIVATE_KEY_FORBIDDEN",
		"age private keys (AGE-SECRET-KEY-...) must never be stored on the server; enter age1... public keys only",
	)

	// ErrSecretEncryptionKeyNotConfigured prevents storing an S3 credential
	// before startup has initialized a persistent installation encryption key.
	ErrSecretEncryptionKeyNotConfigured = infraerrors.BadRequest(
		"SECRET_ENCRYPTION_KEY_NOT_CONFIGURED",
		"cannot store the S3 secret access key: persistent installation encryption is not initialized",
	)
)

// ─── 接口定义 ───

// BackupDumpOptions customizes a single database dump invocation.
type BackupDumpOptions struct {
	// ExcludeTableData lists tables (or pg_dump patterns such as public.usage_logs_*)
	// whose rows are skipped while preserving all table schemas.
	ExcludeTableData []string
}

// DBDumper abstracts database dump/restore operations
type DBDumper interface {
	Dump(ctx context.Context) (io.ReadCloser, error)
	Restore(ctx context.Context, data io.Reader) error
}

// ScopedDBDumper is an optional extension of DBDumper supporting per-run dump
// options such as runtime critical-tables-only snapshots.
type ScopedDBDumper interface {
	DBDumper
	DumpWithOptions(ctx context.Context, opts BackupDumpOptions) (io.ReadCloser, error)
}

// BackupOptions controls per-backup runtime behavior.
type BackupOptions struct {
	// CriticalOnly enables critical-tables-only snapshot mode for this backup run
	// (skipping high-volume log/monitoring/trace/ephemeral table rows while
	// keeping all table schemas and core business data).
	CriticalOnly bool
}

// DefaultCriticalExcludeTableData lists the high-volume log, telemetry,
// monitoring, capture, and ephemeral tables whose rows are skipped when
// "critical tables only" backup mode is enabled without an explicit
// backup.exclude_table_data override. Every table's schema (DDL) is still
// dumped; core business tables (users, balances, API keys, accounts, groups,
// channels, billing settlements, orders, deposits, debt, affiliates,
// subscriptions, redeem/promo codes, support tickets, proxies, settings,
// secrets, auth identities, passkeys, audit logs, and singleton state/watermark
// rows) are always backed up in full.
//
// Foreign-key invariant: no non-excluded table references any table in this
// list (e.g. billing_usage_entries references usage_logs, so both are excluded;
// request_capture_records references request_capture_tasks, so both are
// excluded; prompt_audit_events references prompt_audit_jobs, so both are
// excluded).
var DefaultCriticalExcludeTableData = []string{
	// Usage logs (including monthly partitions) and derived/dedup/rollup rows
	"public.usage_logs",
	"public.usage_logs_*",
	"public.billing_usage_entries",
	"public.usage_billing_dedup",
	"public.usage_billing_dedup_archive",
	"public.usage_cleanup_tasks",
	"public.usage_dashboard_daily",
	"public.usage_dashboard_daily_users",
	"public.usage_dashboard_hourly",
	"public.usage_dashboard_hourly_users",
	"public.usage_dashboard_hourly_dimensions",
	"public.usage_dashboard_hourly_dimension_coverage",
	"public.usage_group_daily_rollups",

	// Request capture, connection traces, small-request retention, traffic & prompt/moderation logs
	"public.request_capture_records",
	"public.request_capture_tasks",
	"public.request_connection_traces",
	"public.small_request_blobs",
	"public.small_request_records",
	"public.small_request_responses",
	"public.small_request_skips",
	"public.small_request_occurrences",
	"public.user_traffic_events",
	"public.user_traffic_hourly",
	"public.prompt_audit_events",
	"public.prompt_audit_jobs",
	"public.content_moderation_logs",
	"public.security_penalty_events",
	"public.security_penalty_request_context",

	// Channel / upstream monitoring histories, time-series metrics & scheduled test results
	"public.channel_monitor_histories",
	"public.channel_monitor_daily_rollups",
	"public.channel_monitor_v2_metrics_1m",
	"public.channel_monitor_v2_error_metrics_1m",
	"public.channel_monitor_v2_latency_histograms_1m",
	"public.channel_monitor_v2_user_metrics_1m",
	"public.channel_monitor_v2_metrics_rollup",
	"public.channel_monitor_v2_error_metrics_rollup",
	"public.channel_monitor_v2_latency_histograms_rollup",
	"public.channel_monitor_v2_user_metrics_rollup",
	"public.scheduled_test_results",
	"public.upstream_balance_samples",
	"public.upstream_monitor_alerts",
	"public.upstream_monitor_daily",
	"public.upstream_rate_changes",
	"public.local_reasoning_results",
	"public.group_quality_check_results",
	"public.credential_guard_events",

	// Ops logs, metrics, alerts & automation histories
	"public.ops_error_logs",
	"public.ops_system_logs",
	"public.ops_system_log_cleanup_audits",
	"public.ops_system_metrics",
	"public.ops_metrics_hourly",
	"public.ops_metrics_daily",
	"public.ops_alert_events",
	"public.ops_retry_attempts",
	"public.ops_job_heartbeats",
	"public.ops_ingress_reject_aggregates",
	"public.ops_automation_events",
	"public.ops_automation_outcomes",
	"public.account_ops_alerts",
	"public.account_ops_threshold_events",
	"public.account_schedulable_timer_audit",

	// Ephemeral outboxes, caches, leases & transient event streams
	"public.scheduler_outbox",
	"public.auth_cache_invalidation_outbox",
	"public.idempotency_records",
	"public.oauth_admission_leases",
	"public.gemini_cached_contents",
	"public.batch_image_events",
	"public.notification_deliveries",
}

// BackupObjectStore abstracts object storage for backup files
type BackupObjectStore interface {
	Upload(ctx context.Context, key string, body io.Reader, contentType string) (sizeBytes int64, err error)
	UploadFile(ctx context.Context, key string, filePath string, contentType string) (sizeBytes int64, err error)
	Download(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	PresignURL(ctx context.Context, key string, expiry time.Duration) (string, error)
	HeadBucket(ctx context.Context) error
}

// BackupObjectStoreFactory creates an object store from S3 config
type BackupObjectStoreFactory func(ctx context.Context, cfg *BackupS3Config) (BackupObjectStore, error)

// ─── 数据模型 ───

// BackupS3Config S3 兼容存储配置（支持 Cloudflare R2）
type BackupS3Config struct {
	Endpoint        string `json:"endpoint"` // e.g. https://<account_id>.r2.cloudflarestorage.com
	Region          string `json:"region"`   // R2 用 "auto"
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key,omitempty"` //nolint:revive // field name follows AWS convention
	Prefix          string `json:"prefix"`                      // S3 key 前缀，如 "backups/"
	ForcePathStyle  bool   `json:"force_path_style"`
}

// IsConfigured 检查必要字段是否已配置
func (c *BackupS3Config) IsConfigured() bool {
	return c.Bucket != "" && c.AccessKeyID != "" && c.SecretAccessKey != ""
}

// BackupScheduleConfig 定时备份配置
type BackupScheduleConfig struct {
	Enabled       bool     `json:"enabled"`
	CronExpr      string   `json:"cron_expr"`                // cron 表达式，如 "0 2 * * *" 每天凌晨2点
	RetainDays    int      `json:"retain_days"`              // 备份文件过期天数，默认14，0=不自动清理
	RetainCount   int      `json:"retain_count"`             // 最多保留份数，0=不限制
	CriticalOnly  bool     `json:"critical_only"`            // 仅备份关键表（跳过用量日志、监控、抓包等大表数据，保留全部表结构）
	AgeRecipients []string `json:"age_recipients,omitempty"` // 管理员后台配置的 age X25519 公钥（age1...），服务端仅存公钥不存私钥
	MaxRateMBps   int      `json:"max_rate_mbps,omitempty"`  // 备份导出限速（MB/s），0=不限速，上限 500
}

// BackupRecord 备份记录
type BackupRecord struct {
	ID            string       `json:"id"`
	Status        string       `json:"status"`      // pending, running, completed, failed
	BackupType    string       `json:"backup_type"` // postgres
	FileName      string       `json:"file_name"`
	S3Key         string       `json:"s3_key"`
	Parts         []BackupPart `json:"parts,omitempty"`
	SizeBytes     int64        `json:"size_bytes"`
	TriggeredBy   string       `json:"triggered_by"` // manual, scheduled
	ErrorMsg      string       `json:"error_message,omitempty"`
	StartedAt     string       `json:"started_at"`
	FinishedAt    string       `json:"finished_at,omitempty"`
	ExpiresAt     string       `json:"expires_at,omitempty"`     // 过期时间
	Progress      string       `json:"progress,omitempty"`       // "dumping", "uploading", ""
	RestoreStatus string       `json:"restore_status,omitempty"` // "", "running", "completed", "failed"
	RestoreError  string       `json:"restore_error,omitempty"`
	RestoredAt    string       `json:"restored_at,omitempty"`
	// Scope is "critical" for a snapshot whose excluded tables carry no rows;
	// empty means the whole database was dumped.
	Scope string `json:"scope,omitempty"`
	// Encrypted marks an age-encrypted archive instead of plain gzip.
	Encrypted bool `json:"encrypted,omitempty"`
	// SHA256 is the digest of the archive as written to disk: the age ciphertext
	// when encrypted, the whole archive (not a part) when it was split. It is only
	// recorded for non-default backups, so a default record stays byte-identical.
	// The record lives in the database, not in the bucket, so an object fetched for
	// a manual restore can be checked against it.
	SHA256 string `json:"sha256,omitempty"`
}

// hasCriticalArchiveSuffix reports whether a backup filename or object key ends
// with a critical snapshot archive suffix (.critical.sql.gz or .critical.sql.gz.age).
func hasCriticalArchiveSuffix(name string) bool {
	return strings.HasSuffix(name, ".critical.sql.gz") || strings.HasSuffix(name, ".critical.sql.gz.age")
}

// restoreUnsupported reports whether the in-app restore must refuse this record.
// The .age and .critical.sql.gz[.age] suffixes are second signals for a record
// whose flags were lost: a build that predates them rewrites the whole record
// list without Scope and Encrypted.
func (r *BackupRecord) restoreUnsupported() bool {
	return r.Encrypted || r.Scope == backupScopeCritical ||
		strings.HasSuffix(r.FileName, ".age") || strings.HasSuffix(r.S3Key, ".age") ||
		hasCriticalArchiveSuffix(r.FileName) || hasCriticalArchiveSuffix(r.S3Key)
}

// BackupDownloadPart 描述一个可下载的备份分卷。
type BackupDownloadPart struct {
	Index     int    `json:"index"`
	SizeBytes int64  `json:"size_bytes"`
	URL       string `json:"url"`
}

// BackupDownloadResponse 是单文件和分卷下载响应的兼容表示。
type BackupDownloadResponse struct {
	URL   string               `json:"url,omitempty"`
	Parts []BackupDownloadPart `json:"parts,omitempty"`
}

// BackupService 数据库备份恢复服务
type BackupService struct {
	settingRepo SettingRepository
	dbCfg       *config.DatabaseConfig
	backupCfg   config.BackupConfig
	encryptor   SecretEncryptor
	// encryptionKeyConfigured accepts an explicitly configured key or a stable
	// installation key loaded by bootstrap. Temporary process keys cannot safely
	// protect persisted secrets across restarts (#4524).
	encryptionKeyConfigured bool
	storeFactory            BackupObjectStoreFactory
	dumper                  DBDumper

	opMu      sync.Mutex // 保护 backingUp/restoring 标志
	backingUp bool
	restoring bool

	storeMu sync.Mutex // 保护 store/s3Cfg 缓存
	store   BackupObjectStore
	s3Cfg   *BackupS3Config

	recordsMu sync.Mutex // 保护 records 的 load/save 操作

	cronMu      sync.Mutex
	cronSched   *cron.Cron
	cronEntryID cron.EntryID

	// lockCache/db elect a single leader for the scheduled backup across
	// instances; instanceID identifies this process as the lock owner. Injected
	// via SetLeaderLock — when both are nil the backup runs ungated
	// (single-instance / test behavior).
	lockCache  LeaderLockCache
	db         *sql.DB
	instanceID string

	wg            sync.WaitGroup     // 追踪活跃的备份/恢复 goroutine
	shuttingDown  atomic.Bool        // 阻止新备份启动
	bgCtx         context.Context    // 所有后台操作的 parent context
	bgCancel      context.CancelFunc // 取消所有活跃后台操作
	partSizeBytes int64              // 分卷阈值；生产使用 4 GiB，测试可注入更小值

	// ageEncrypt wraps the archive sink in an age encrypting writer; tests swap it
	// to force an encryptor failure.
	ageEncrypt func(io.Writer, ...age.Recipient) (io.WriteCloser, error)

	// criticalMaxArchiveBytes is the size limit of a critical snapshot that has no
	// explicit backup.max_archive_bytes; tests inject a smaller value.
	criticalMaxArchiveBytes int64

	// createArchiveFile creates the staged archive; tests swap it to make Close fail.
	createArchiveFile func(pattern string) (backupArchiveFile, error)
}

func NewBackupService(
	settingRepo SettingRepository,
	cfg *config.Config,
	encryptor SecretEncryptor,
	storeFactory BackupObjectStoreFactory,
	dumper DBDumper,
) *BackupService {
	bgCtx, bgCancel := context.WithCancel(context.Background())
	return &BackupService{
		settingRepo:             settingRepo,
		dbCfg:                   &cfg.Database,
		backupCfg:               cfg.Backup,
		encryptor:               encryptor,
		encryptionKeyConfigured: cfg.Totp.HasPersistentEncryptionKey(),
		storeFactory:            storeFactory,
		dumper:                  dumper,
		bgCtx:                   bgCtx,
		bgCancel:                bgCancel,
		partSizeBytes:           defaultBackupPartSizeBytes,
		instanceID:              uuid.NewString(),
		ageEncrypt:              age.Encrypt,
		criticalMaxArchiveBytes: defaultCriticalMaxArchiveBytes,
		createArchiveFile:       createStagedArchive,
	}
}

// ServerCriticalModeForced reports whether deployment config
// (backup.exclude_table_data) enforces critical snapshot mode for every backup.
func (s *BackupService) ServerCriticalModeForced() bool {
	return s != nil && len(s.backupCfg.ExcludeTableData) > 0
}

// ServerAgeEncryptionForced reports whether deployment config
// (backup.age_recipients) enforces age public-key encryption for every backup.
func (s *BackupService) ServerAgeEncryptionForced() bool {
	return s != nil && len(s.backupCfg.AgeRecipients) > 0
}

// ServerAgeRecipientCount returns how many age recipients are configured at
// deployment level (backup.age_recipients).
func (s *BackupService) ServerAgeRecipientCount() int {
	if s == nil {
		return 0
	}
	return len(s.backupCfg.AgeRecipients)
}

// AgeEncryptionEnabled reports whether server-side age public-key encryption is
// configured for backup archives.
func (s *BackupService) AgeEncryptionEnabled() bool {
	return s.ServerAgeEncryptionForced()
}

// AgeEncryptionEnabledForSchedule reports whether backups will be age-encrypted
// given deployment config plus the persisted schedule config.
func (s *BackupService) AgeEncryptionEnabledForSchedule(cfg *BackupScheduleConfig) bool {
	if s.ServerAgeEncryptionForced() {
		return true
	}
	return cfg != nil && len(cfg.AgeRecipients) > 0
}

type resolvedBackupMode struct {
	scope            string
	excludeTables    []string
	requireRecipient bool
	nonDefault       bool
}

type resolvedBackupRun struct {
	mode          resolvedBackupMode
	ageRecipients []string
	maxRateMBps   int
}

func (r resolvedBackupRun) encrypted() bool {
	return len(r.ageRecipients) > 0
}

func (s *BackupService) resolveBackupMode(opts BackupOptions) resolvedBackupMode {
	var excludeTables []string
	requireRecipient := false
	if len(s.backupCfg.ExcludeTableData) > 0 {
		excludeTables = append([]string(nil), s.backupCfg.ExcludeTableData...)
		// Deployment-level backup.exclude_table_data always fails closed without
		// age recipients, matching the security contract introduced in #583.
		requireRecipient = true
	} else if opts.CriticalOnly {
		excludeTables = append([]string(nil), DefaultCriticalExcludeTableData...)
	}

	scope := ""
	if len(excludeTables) > 0 {
		scope = backupScopeCritical
	}

	return resolvedBackupMode{
		scope:            scope,
		excludeTables:    excludeTables,
		requireRecipient: requireRecipient,
		nonDefault:       scope != "" || s.backupEncrypted() || s.backupCfg.MaxArchiveBytes > 0,
	}
}

func (s *BackupService) resolveBackupRun(ctx context.Context, opts BackupOptions) (resolvedBackupRun, error) {
	mode := s.resolveBackupMode(opts)
	var schedule *BackupScheduleConfig
	if s.settingRepo != nil {
		var err error
		schedule, err = s.GetSchedule(ctx)
		if err != nil {
			return resolvedBackupRun{}, err
		}
	}

	var scheduleRecipients []string
	maxRateMBps := 0
	if schedule != nil {
		if len(schedule.AgeRecipients) > maxBackupAgeRecipients {
			return resolvedBackupRun{}, ErrBackupScheduleCorrupt
		}
		scheduleRecipients = schedule.AgeRecipients
		if schedule.MaxRateMBps < 0 || schedule.MaxRateMBps > maxBackupRateMBps {
			return resolvedBackupRun{}, ErrBackupScheduleCorrupt
		}
		maxRateMBps = schedule.MaxRateMBps
	}

	recipients := mergeBackupAgeRecipients(s.backupCfg.AgeRecipients, scheduleRecipients)
	if len(recipients) > 0 {
		mode.nonDefault = true
	}

	return resolvedBackupRun{
		mode:          mode,
		ageRecipients: recipients,
		maxRateMBps:   maxRateMBps,
	}, nil
}

// mergeBackupAgeRecipients combines deployment-level and admin-configured
// recipients in order (server keys first so they can never be stripped by DB
// settings), deduplicating non-empty trimmed entries case-insensitively while
// preserving blank entries intact so parseAgeRecipients fails closed.
func mergeBackupAgeRecipients(serverKeys, scheduleKeys []string) []string {
	total := len(serverKeys) + len(scheduleKeys)
	if total == 0 {
		return nil
	}
	merged := make([]string, 0, total)
	seen := make(map[string]struct{}, total)
	appendRaw := func(keys []string) {
		for _, k := range keys {
			trimmed := strings.TrimSpace(k)
			if trimmed == "" {
				merged = append(merged, k)
				continue
			}
			dedupKey := strings.ToLower(trimmed)
			if _, ok := seen[dedupKey]; ok {
				continue
			}
			seen[dedupKey] = struct{}{}
			merged = append(merged, trimmed)
		}
	}
	appendRaw(serverKeys)
	appendRaw(scheduleKeys)
	return merged
}

// NormalizeAndValidateBackupAgeRecipients trims, deduplicates, and validates
// admin-submitted age X25519 public keys. Private keys (AGE-SECRET-KEY-...) and
// invalid public keys are rejected without echoing the input material.
func NormalizeAndValidateBackupAgeRecipients(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// Scan every entry for private key material before checking line format or
	// input length limits so pasting a full `age-keygen` file always triggers the
	// explicit private-key warning.
	for _, entry := range raw {
		if isAgePrivateKey(entry) {
			return nil, ErrBackupAgePrivateKeyForbidden
		}
	}
	if len(raw) > maxBackupAgeRecipients*4 {
		return nil, infraerrors.BadRequest(
			"BACKUP_AGE_RECIPIENTS_TOO_MANY",
			fmt.Sprintf("at most %d age recipients are allowed", maxBackupAgeRecipients),
		)
	}
	initCap := min(len(raw), maxBackupAgeRecipients)
	normalized := make([]string, 0, initCap)
	seen := make(map[string]struct{}, initCap)
	for i, entry := range raw {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		recipient, err := age.ParseX25519Recipient(trimmed)
		if err != nil {
			return nil, infraerrors.BadRequest(
				"BACKUP_AGE_RECIPIENT_INVALID",
				fmt.Sprintf("age recipient #%d is not a valid age X25519 public key (expected age1...)", i+1),
			)
		}
		canonical := recipient.String()
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		normalized = append(normalized, canonical)
		if len(normalized) > maxBackupAgeRecipients {
			return nil, infraerrors.BadRequest(
				"BACKUP_AGE_RECIPIENTS_TOO_MANY",
				fmt.Sprintf("at most %d age recipients are allowed", maxBackupAgeRecipients),
			)
		}
	}
	if len(normalized) == 0 {
		return nil, nil
	}
	return normalized, nil
}

func isAgePrivateKey(key string) bool {
	return strings.Contains(strings.ToUpper(key), "AGE-SECRET-KEY-")
}

// backupScope is recorded on every backup: "critical" when table data is
// excluded, empty for a whole-database dump.
func (s *BackupService) backupScope() string {
	if len(s.backupCfg.ExcludeTableData) > 0 {
		return backupScopeCritical
	}
	return ""
}

// backupEncrypted reports whether archives are age-encrypted. Any configured
// recipient turns encryption on; there is no plaintext fallback.
func (s *BackupService) backupEncrypted() bool {
	return len(s.backupCfg.AgeRecipients) > 0
}

// archiveLimit is the byte limit for the archive on disk: the configured value,
// else the built-in limit of a critical snapshot, else 0 (no limit). builtin is
// true only for the second case.
func (s *BackupService) archiveLimit() (limit int64, builtin bool) {
	return s.archiveLimitForScope(s.backupScope())
}

func (s *BackupService) archiveLimitForScope(scope string) (limit int64, builtin bool) {
	if s.backupCfg.MaxArchiveBytes > 0 {
		return s.backupCfg.MaxArchiveBytes, false
	}
	if scope != "" {
		return s.criticalMaxArchiveBytes, true
	}
	return 0, false
}

// nonDefaultBackup reports whether the operator opted into any backup.* setting
// that changes how a backup runs. Anything new that touches the host outside a
// backup run stays off while this is false.
func (s *BackupService) nonDefaultBackup() bool {
	return s.backupScope() != "" || s.backupEncrypted() || s.backupCfg.MaxArchiveBytes > 0
}

// backupFileNameForScope names the archive; the .age suffix tells a human (and
// the download link) that it is not plain gzip.
func (s *BackupService) backupFileNameForScope(now time.Time, scope string, encrypted bool) string {
	suffix := ".sql.gz"
	if scope == backupScopeCritical {
		suffix = ".critical.sql.gz"
	}
	name := fmt.Sprintf("%s_%s%s", s.dbCfg.DBName, now.Format("20060102_150405"), suffix)
	if encrypted {
		name += ".age"
	}
	return name
}

// backupContentType is the Content-Type of a single-object upload.
func backupContentType(record *BackupRecord) string {
	if record.Encrypted {
		return "application/octet-stream"
	}
	return "application/gzip"
}

// SetLeaderLock injects the leader-lock cache and DB used to elect a single
// instance for the scheduled backup. When both are nil the scheduled backup runs
// ungated (single-instance / test behavior).
func (s *BackupService) SetLeaderLock(lockCache LeaderLockCache, db *sql.DB) {
	if s == nil {
		return
	}
	s.lockCache = lockCache
	s.db = db
}

// Start 启动定时备份调度器并清理孤立记录
func (s *BackupService) Start() {
	if BackgroundJobsDisabled() {
		return
	}
	s.logSnapshotMode(logger.L())
	if s.nonDefaultBackup() {
		s.sweepStaleBackupFiles(time.Now())
	}
	s.cronSched = cron.New()
	s.cronSched.Start()

	// 清理重启后孤立的 running 记录
	s.recoverStaleRecords()

	// 加载已有的定时配置
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	schedule, err := s.GetSchedule(ctx)
	if err != nil {
		logger.LegacyPrintf("service.backup", "[Backup] 加载定时备份配置失败: %v", err)
		return
	}
	if !s.nonDefaultBackup() && (schedule.CriticalOnly || len(schedule.AgeRecipients) > 0) {
		s.sweepStaleBackupFiles(time.Now())
	}
	if schedule.Enabled && schedule.CronExpr != "" {
		if err := s.applyCronSchedule(schedule); err != nil {
			logger.LegacyPrintf("service.backup", "[Backup] 应用定时备份配置失败: %v", err)
		}
	}
}

// logSnapshotMode announces a non-default backup configuration once at startup.
// It lists the table entries that will really be passed to pg_dump (pg_dump
// ignores an entry that matches nothing, so this is the one place a misspelt name
// shows up), and otherwise reports flags and a size only: never recipients or
// paths. max_archive_bytes is the limit actually applied, so a critical snapshot
// without an explicit value shows the built-in one. Nothing is logged for the
// default whole-database backup.
func (s *BackupService) logSnapshotMode(l *zap.Logger) {
	if !s.nonDefaultBackup() {
		return
	}
	limit, _ := s.archiveLimit()
	l.Info("[Backup] non-default backup settings in effect",
		zap.String("component", "service.backup"),
		zap.Bool("critical", s.backupScope() != ""),
		zap.Strings("exclude_table_data", s.backupCfg.ExcludeTableData),
		zap.Int("excluded_tables", len(s.backupCfg.ExcludeTableData)),
		zap.Bool("encrypted", s.backupEncrypted()),
		zap.Int64("max_archive_bytes", limit),
	)
}

// sweepStaleBackupFiles removes staged archives (and split parts) that a killed
// process left in the temp directory: a deploy that SIGKILLs the process during a
// backup otherwise strands a file as large as the archive. Only regular files with
// the staging prefix (staleBackupFilePrefix) that are older than staleBackupFileAge
// are touched: never a directory or a symlink, and never a file with a different
// name, such as a dump an operator copied there by hand. Files left by builds that
// staged under the earlier, shorter prefix are not swept. It only runs for a
// non-default backup configuration, so a default install is unchanged.
func (s *BackupService) sweepStaleBackupFiles(now time.Time) int {
	dir := os.TempDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), staleBackupFilePrefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || now.Sub(info.ModTime()) < staleBackupFileAge {
			continue
		}
		if os.Remove(filepath.Join(dir, entry.Name())) == nil {
			removed++
		}
	}
	if removed > 0 {
		logger.L().Info("[Backup] removed stale staged archives",
			zap.String("component", "service.backup"), zap.Int("removed", removed))
	}
	return removed
}

// recoverStaleRecords 启动时将孤立的 running 记录标记为 failed，并清理已上传对象。
func (s *BackupService) recoverStaleRecords() {
	loadCtx, loadCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer loadCancel()

	records, err := s.loadRecords(loadCtx)
	if err != nil {
		return
	}
	for i := range records {
		if records[i].Status == "running" {
			staleRecord := records[i]
			records[i].Status = "failed"
			records[i].ErrorMsg = "interrupted by server restart"
			records[i].Progress = ""
			records[i].FinishedAt = time.Now().Format(time.RFC3339)
			s.saveRecoveredRecord(&records[i])

			if cleanupErr := s.cleanupStaleBackupObjects(&staleRecord); cleanupErr != nil {
				records[i].ErrorMsg = fmt.Sprintf("interrupted by server restart; cleanup failed, manual deletion may be required: %v", cleanupErr)
				s.saveRecoveredRecord(&records[i])
				logger.LegacyPrintf("service.backup", "[Backup] failed to clean stale backup objects for %s: %v", records[i].ID, cleanupErr)
			}
			logger.LegacyPrintf("service.backup", "[Backup] recovered stale running record: %s", records[i].ID)
		}
		if records[i].RestoreStatus == "running" {
			records[i].RestoreStatus = "failed"
			records[i].RestoreError = "interrupted by server restart"
			s.saveRecoveredRecord(&records[i])
			logger.LegacyPrintf("service.backup", "[Backup] recovered stale restoring record: %s", records[i].ID)
		}
	}
}

func (s *BackupService) saveRecoveredRecord(record *BackupRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.saveRecord(ctx, record); err != nil {
		logger.LegacyPrintf("service.backup", "[Backup] 保存恢复后的备份记录失败 %s: %v", record.ID, err)
	}
}

func (s *BackupService) cleanupStaleBackupObjects(record *BackupRecord) error {
	if len(backupObjectKeys(record)) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), backupObjectCleanupTimeout)
	defer cancel()
	return s.deleteBackupObjects(ctx, record)
}

// Stop 停止定时备份并等待活跃操作完成
func (s *BackupService) Stop() {
	s.shuttingDown.Store(true)

	s.cronMu.Lock()
	if s.cronSched != nil {
		s.cronSched.Stop()
	}
	s.cronMu.Unlock()

	// 等待活跃备份/恢复完成（最多 5 分钟）
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		logger.LegacyPrintf("service.backup", "[Backup] all active operations finished")
	case <-time.After(5 * time.Minute):
		logger.LegacyPrintf("service.backup", "[Backup] shutdown timeout after 5min, cancelling active operations")
		if s.bgCancel != nil {
			s.bgCancel() // 取消所有后台操作
		}
		// 给 goroutine 时间响应取消并完成清理
		select {
		case <-done:
			logger.LegacyPrintf("service.backup", "[Backup] active operations cancelled and cleaned up")
		case <-time.After(10 * time.Second):
			logger.LegacyPrintf("service.backup", "[Backup] goroutine cleanup timed out")
		}
	}
}

// ─── S3 配置管理 ───

// EncryptionKeyConfigured reports whether the secret encryption key survives
// restarts, either through explicit configuration or installation bootstrap.
func (s *BackupService) EncryptionKeyConfigured() bool {
	return s != nil && s.encryptionKeyConfigured
}

func (s *BackupService) GetS3Config(ctx context.Context) (*BackupS3Config, error) {
	cfg, err := s.loadS3Config(ctx)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return &BackupS3Config{}, nil
	}
	// 脱敏返回
	cfg.SecretAccessKey = ""
	return cfg, nil
}

func (s *BackupService) UpdateS3Config(ctx context.Context, cfg BackupS3Config) (*BackupS3Config, error) {
	// 如果没提供 secret，保留原有值
	if cfg.SecretAccessKey == "" {
		old, _ := s.loadS3Config(ctx)
		if old != nil {
			cfg.SecretAccessKey = old.SecretAccessKey
		}
	}

	// loadS3Config decrypts inherited secrets, so both paths must encrypt before storage.
	if cfg.SecretAccessKey != "" {
		// 拒绝用尚未持久化的临时密钥加密：该密钥每次重启都会变化，落库的密文在
		// 重启/升级后无法解密（#4524）。与支付、TOTP 的处理保持一致。
		if !s.encryptionKeyConfigured {
			return nil, ErrSecretEncryptionKeyNotConfigured
		}
		// 加密 SecretAccessKey
		encrypted, err := s.encryptor.Encrypt(cfg.SecretAccessKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt secret: %w", err)
		}
		cfg.SecretAccessKey = encrypted
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal s3 config: %w", err)
	}
	if err := s.settingRepo.Set(ctx, settingKeyBackupS3Config, string(data)); err != nil {
		return nil, fmt.Errorf("save s3 config: %w", err)
	}

	// 清除缓存的 S3 客户端
	s.storeMu.Lock()
	s.store = nil
	s.s3Cfg = nil
	s.storeMu.Unlock()

	cfg.SecretAccessKey = ""
	return &cfg, nil
}

func (s *BackupService) TestS3Connection(ctx context.Context, cfg BackupS3Config) error {
	// 入口校验：endpoint 由管理员任意填写，属于出站目标，必须只允许公网。
	// 字面量私网、解析到私网、DNS 失败统一拒绝，且不把细节回显给前端。
	if endpoint := strings.TrimSpace(cfg.Endpoint); endpoint != "" {
		normalized, err := validateAdminOutboundTarget(ctx, endpoint)
		if err != nil {
			return err
		}
		cfg.Endpoint = normalized
	}

	// 如果没提供 secret，用已保存的
	if cfg.SecretAccessKey == "" {
		old, _ := s.loadS3Config(ctx)
		if old != nil {
			cfg.SecretAccessKey = old.SecretAccessKey
		}
	}

	if cfg.Bucket == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return fmt.Errorf("incomplete S3 config: bucket, access_key_id, secret_access_key are required")
	}

	// 测试连接是管理员的探测原语：真正发请求时再做一次「连接前解析 IP 校验」
	// 与「重定向到私网拒绝」，堵住预校验与实际连接之间的 TOCTOU。
	ctx = WithObjectStoreHTTPClient(ctx, newAdminOutboundHTTPClient())
	store, err := s.storeFactory(ctx, &cfg)
	if err != nil {
		return err
	}
	return store.HeadBucket(ctx)
}

// ─── 定时备份管理 ───

func (s *BackupService) GetSchedule(ctx context.Context) (*BackupScheduleConfig, error) {
	raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupSchedule)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return &BackupScheduleConfig{}, nil
		}
		return nil, err
	}
	if raw == "" {
		return &BackupScheduleConfig{}, nil
	}
	var cfg BackupScheduleConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, ErrBackupScheduleCorrupt
	}
	for _, entry := range cfg.AgeRecipients {
		if isAgePrivateKey(entry) {
			return nil, ErrBackupScheduleCorrupt
		}
	}
	return &cfg, nil
}

func (s *BackupService) UpdateSchedule(ctx context.Context, cfg BackupScheduleConfig) (*BackupScheduleConfig, error) {
	if cfg.Enabled && cfg.CronExpr == "" {
		return nil, infraerrors.BadRequest("INVALID_CRON", "cron expression is required when schedule is enabled")
	}
	// 验证 cron 表达式
	if cfg.CronExpr != "" {
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, err := parser.Parse(cfg.CronExpr); err != nil {
			return nil, infraerrors.BadRequest("INVALID_CRON", fmt.Sprintf("invalid cron expression: %v", err))
		}
	}
	// A schedule that keeps more completed backups than the record list can hold
	// would push the oldest ones out of the list. A disabled schedule never runs,
	// so it may still carry an old value and must stay switchable off.
	if cfg.Enabled && cfg.RetainCount > maxBackupRetainCount {
		return nil, infraerrors.BadRequest("INVALID_RETENTION", fmt.Sprintf("retain_count must be at most %d", maxBackupRetainCount))
	}
	if cfg.MaxRateMBps < 0 || cfg.MaxRateMBps > maxBackupRateMBps {
		return nil, infraerrors.BadRequest("INVALID_BACKUP_RATE_LIMIT", fmt.Sprintf("max_rate_mbps must be between 0 and %d", maxBackupRateMBps))
	}
	normalizedRecipients, err := NormalizeAndValidateBackupAgeRecipients(cfg.AgeRecipients)
	if err != nil {
		return nil, err
	}
	cfg.AgeRecipients = normalizedRecipients

	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal schedule config: %w", err)
	}
	if err := s.settingRepo.Set(ctx, settingKeyBackupSchedule, string(data)); err != nil {
		return nil, fmt.Errorf("save schedule config: %w", err)
	}

	// 应用或停止定时任务
	if cfg.Enabled {
		if err := s.applyCronSchedule(&cfg); err != nil {
			return nil, err
		}
	} else {
		s.removeCronSchedule()
	}

	return &cfg, nil
}

func (s *BackupService) applyCronSchedule(cfg *BackupScheduleConfig) error {
	if BackgroundJobsDisabled() {
		return nil
	}
	s.cronMu.Lock()
	defer s.cronMu.Unlock()

	if s.cronSched == nil {
		return fmt.Errorf("cron scheduler not initialized")
	}

	// 移除旧任务
	if s.cronEntryID != 0 {
		s.cronSched.Remove(s.cronEntryID)
		s.cronEntryID = 0
	}

	entryID, err := s.cronSched.AddFunc(cfg.CronExpr, func() {
		s.runScheduledBackup()
	})
	if err != nil {
		return infraerrors.BadRequest("INVALID_CRON", fmt.Sprintf("failed to schedule: %v", err))
	}
	s.cronEntryID = entryID
	logger.LegacyPrintf("service.backup", "[Backup] 定时备份已启用: %s (仅关键表: %v, 加密: %v, 限速MB/s: %d)",
		cfg.CronExpr,
		cfg.CriticalOnly || s.ServerCriticalModeForced(),
		s.AgeEncryptionEnabledForSchedule(cfg),
		cfg.MaxRateMBps,
	)
	return nil
}

func (s *BackupService) removeCronSchedule() {
	s.cronMu.Lock()
	defer s.cronMu.Unlock()
	if s.cronSched != nil && s.cronEntryID != 0 {
		s.cronSched.Remove(s.cronEntryID)
		s.cronEntryID = 0
		logger.LegacyPrintf("service.backup", "[Backup] 定时备份已停用")
	}
}

func (s *BackupService) runScheduledBackup() {
	s.wg.Add(1)
	defer s.wg.Done()

	ctx, cancel := context.WithTimeout(s.bgCtx, 30*time.Minute)
	defer cancel()

	// 多实例保护: 集群部署时只让 leader 执行定时备份, 避免每个实例各自对同一个
	// 数据库跑一次全量 dump、上传时峰值内存翻倍、以及多份同名对象互相覆盖。
	// 手动触发的备份 (CreateBackup/StartBackup) 不受此限, 运维仍可随时在任一节点强制备份。
	release, ok := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, backupScheduledLeaderLockKey, s.instanceID, backupScheduledLeaderLockTTL)
	if !ok {
		logger.LegacyPrintf("service.backup", "[Backup] 定时备份跳过: 本实例非 leader")
		return
	}
	defer release()

	// 读取定时备份配置中的过期天数与备份范围；读取失败或配置损坏时宁可跳过本次定时任务，
	// 也绝不能静默退化成 CriticalOnly=false 的无限制全库备份。
	schedule, err := s.GetSchedule(ctx)
	if err != nil {
		logger.LegacyPrintf("service.backup", "[Backup] 定时备份跳过: 读取定时备份配置失败: %v", err)
		return
	}
	expireDays := 14 // 默认14天过期
	criticalOnly := false
	if schedule != nil {
		if schedule.RetainDays > 0 {
			expireDays = schedule.RetainDays
		}
		criticalOnly = schedule.CriticalOnly
	}

	logger.LegacyPrintf("service.backup", "[Backup] 开始执行定时备份, 过期天数: %d, 仅关键表: %v", expireDays, criticalOnly || s.ServerCriticalModeForced())
	record, err := s.CreateBackupWithOptions(ctx, "scheduled", expireDays, BackupOptions{
		CriticalOnly: criticalOnly,
	})
	if err != nil {
		if errors.Is(err, ErrBackupInProgress) {
			logger.LegacyPrintf("service.backup", "[Backup] 定时备份跳过: 已有备份正在进行中")
		} else {
			logger.LegacyPrintf("service.backup", "[Backup] 定时备份失败: %v", err)
		}
		return
	}
	logger.LegacyPrintf("service.backup", "[Backup] 定时备份完成: id=%s scope=%s size=%d", record.ID, record.Scope, record.SizeBytes)

	// 清理过期备份（复用已加载的 schedule）
	if schedule == nil {
		return
	}
	if err := s.cleanupOldBackups(ctx, schedule); err != nil {
		logger.LegacyPrintf("service.backup", "[Backup] 清理过期备份失败: %v", err)
	}
}

// ─── 备份/恢复核心 ───

// CreateBackup 创建数据库备份并上传到 S3。
// expireDays: 备份过期天数，0=永不过期，默认14天
func (s *BackupService) CreateBackup(ctx context.Context, triggeredBy string, expireDays int) (*BackupRecord, error) {
	return s.CreateBackupWithOptions(ctx, triggeredBy, expireDays, BackupOptions{})
}

// CreateBackupWithOptions 创建数据库备份并上传到 S3，支持单次备份范围选项（如仅备份关键表）。
func (s *BackupService) CreateBackupWithOptions(ctx context.Context, triggeredBy string, expireDays int, opts BackupOptions) (*BackupRecord, error) {
	if s.shuttingDown.Load() {
		return nil, infraerrors.ServiceUnavailable("SERVER_SHUTTING_DOWN", "server is shutting down")
	}

	s.opMu.Lock()
	if s.backingUp {
		s.opMu.Unlock()
		return nil, ErrBackupInProgress
	}
	s.backingUp = true
	s.opMu.Unlock()
	defer func() {
		s.opMu.Lock()
		s.backingUp = false
		s.opMu.Unlock()
	}()

	s3Cfg, err := s.loadS3Config(ctx)
	if err != nil {
		return nil, err
	}
	if s3Cfg == nil || !s3Cfg.IsConfigured() {
		return nil, ErrBackupS3NotConfigured
	}

	objectStore, err := s.getOrCreateStore(ctx, s3Cfg)
	if err != nil {
		return nil, fmt.Errorf("init object store: %w", err)
	}

	run, err := s.resolveBackupRun(ctx, opts)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	backupID := uuid.New().String()[:8]
	fileName := s.backupFileNameForScope(now, run.mode.scope, run.encrypted())
	s3Key := s.buildS3Key(s3Cfg, fileName)

	var expiresAt string
	if expireDays > 0 {
		expiresAt = now.AddDate(0, 0, expireDays).Format(time.RFC3339)
	}

	record := &BackupRecord{
		ID:          backupID,
		Status:      "running",
		BackupType:  "postgres",
		Scope:       run.mode.scope,
		Encrypted:   run.encrypted(),
		FileName:    fileName,
		S3Key:       s3Key,
		TriggeredBy: triggeredBy,
		StartedAt:   now.Format(time.RFC3339),
		ExpiresAt:   expiresAt,
	}

	archivePath, sizeBytes, digest, err := s.createCompressedBackupFileForRun(ctx, run)
	if err != nil {
		record.Status = "failed"
		record.ErrorMsg = err.Error()
		record.FinishedAt = time.Now().Format(time.RFC3339)
		_ = s.saveRecord(ctx, record)
		return record, err
	}
	defer func() { _ = cleanupBackupFiles(archivePath) }()
	record.SizeBytes = sizeBytes
	record.SHA256 = recordedDigestForMode(run.mode, digest)
	if err := s.saveRecord(ctx, record); err != nil {
		return nil, fmt.Errorf("save initial record: %w", err)
	}
	if err := s.uploadBackupArchive(ctx, record, objectStore, s3Cfg, archivePath); err != nil {
		record.Status = "failed"
		record.ErrorMsg = err.Error()
		record.FinishedAt = time.Now().Format(time.RFC3339)
		_ = s.saveRecord(ctx, record)
		return record, err
	}

	record.Status = "completed"
	record.FinishedAt = time.Now().Format(time.RFC3339)
	if err := s.saveRecord(ctx, record); err != nil {
		logger.LegacyPrintf("service.backup", "[Backup] 保存备份记录失败: %v", err)
	}

	return record, nil
}

// StartBackup 异步创建备份，立即返回 running 状态的记录
func (s *BackupService) StartBackup(ctx context.Context, triggeredBy string, expireDays int) (*BackupRecord, error) {
	return s.StartBackupWithOptions(ctx, triggeredBy, expireDays, BackupOptions{})
}

// StartBackupWithOptions 异步创建备份，支持单次备份范围选项（如仅备份关键表）。
func (s *BackupService) StartBackupWithOptions(ctx context.Context, triggeredBy string, expireDays int, opts BackupOptions) (*BackupRecord, error) {
	if s.shuttingDown.Load() {
		return nil, infraerrors.ServiceUnavailable("SERVER_SHUTTING_DOWN", "server is shutting down")
	}

	s.opMu.Lock()
	if s.backingUp {
		s.opMu.Unlock()
		return nil, ErrBackupInProgress
	}
	s.backingUp = true
	s.opMu.Unlock()

	// 初始化阶段出错时自动重置标志
	launched := false
	defer func() {
		if !launched {
			s.opMu.Lock()
			s.backingUp = false
			s.opMu.Unlock()
		}
	}()

	// 在返回前加载 S3 配置和创建 store，避免 goroutine 中配置被修改
	s3Cfg, err := s.loadS3Config(ctx)
	if err != nil {
		return nil, err
	}
	if s3Cfg == nil || !s3Cfg.IsConfigured() {
		return nil, ErrBackupS3NotConfigured
	}

	objectStore, err := s.getOrCreateStore(ctx, s3Cfg)
	if err != nil {
		return nil, fmt.Errorf("init object store: %w", err)
	}

	run, err := s.resolveBackupRun(ctx, opts)
	if err != nil {
		return nil, err
	}
	recipients, err := parseAgeRecipients(run.ageRecipients)
	if err != nil {
		return nil, err
	}
	if run.mode.requireRecipient && len(recipients) == 0 {
		return nil, ErrBackupCriticalNeedsRecipient
	}

	now := time.Now()
	backupID := uuid.New().String()[:8]
	fileName := s.backupFileNameForScope(now, run.mode.scope, run.encrypted())
	s3Key := s.buildS3Key(s3Cfg, fileName)

	var expiresAt string
	if expireDays > 0 {
		expiresAt = now.AddDate(0, 0, expireDays).Format(time.RFC3339)
	}

	record := &BackupRecord{
		ID:          backupID,
		Status:      "running",
		BackupType:  "postgres",
		Scope:       run.mode.scope,
		Encrypted:   run.encrypted(),
		FileName:    fileName,
		S3Key:       s3Key,
		TriggeredBy: triggeredBy,
		StartedAt:   now.Format(time.RFC3339),
		ExpiresAt:   expiresAt,
		Progress:    "pending",
	}

	if err := s.saveRecord(ctx, record); err != nil {
		return nil, fmt.Errorf("save initial record: %w", err)
	}

	launched = true
	// 在启动 goroutine 前完成拷贝，避免数据竞争
	result := *record

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.opMu.Lock()
			s.backingUp = false
			s.opMu.Unlock()
		}()
		defer func() {
			if r := recover(); r != nil {
				logger.LegacyPrintf("service.backup", "[Backup] panic recovered: %v", r)
				record.Status = "failed"
				record.ErrorMsg = fmt.Sprintf("internal panic: %v", r)
				record.Progress = ""
				record.FinishedAt = time.Now().Format(time.RFC3339)
				_ = s.saveRecord(context.Background(), record)
			}
		}()
		s.executeBackup(record, objectStore, s3Cfg, run)
	}()

	return &result, nil
}

// executeBackup 后台执行备份（独立于 HTTP context）
func (s *BackupService) executeBackup(record *BackupRecord, objectStore BackupObjectStore, s3Cfg *BackupS3Config, run resolvedBackupRun) {
	ctx, cancel := context.WithTimeout(s.bgCtx, 30*time.Minute)
	defer cancel()

	// 阶段1: pg_dump -> gzip -> (可选 age) 临时文件
	record.Progress = "dumping"
	_ = s.saveRecord(ctx, record)
	archivePath, sizeBytes, digest, err := s.createCompressedBackupFileForRun(ctx, run)
	if err != nil {
		record.Status = "failed"
		record.ErrorMsg = err.Error()
		record.Progress = ""
		record.FinishedAt = time.Now().Format(time.RFC3339)
		_ = s.saveRecord(context.Background(), record)
		return
	}
	defer func() { _ = cleanupBackupFiles(archivePath) }()
	record.SizeBytes = sizeBytes
	record.SHA256 = recordedDigestForMode(run.mode, digest)

	// 阶段2: 单对象或分卷上传
	record.Progress = "uploading"
	_ = s.saveRecord(ctx, record)
	if err := s.uploadBackupArchive(ctx, record, objectStore, s3Cfg, archivePath); err != nil {
		record.Status = "failed"
		record.ErrorMsg = err.Error()
		record.Progress = ""
		record.FinishedAt = time.Now().Format(time.RFC3339)
		_ = s.saveRecord(context.Background(), record)
		return
	}

	record.SizeBytes = sizeBytes
	record.Status = "completed"
	record.Progress = ""
	record.FinishedAt = time.Now().Format(time.RFC3339)
	if err := s.saveRecord(context.Background(), record); err != nil {
		logger.LegacyPrintf("service.backup", "[Backup] 保存备份记录失败: %v", err)
	}
}

func (s *BackupService) createCompressedBackupFileForRun(ctx context.Context, run resolvedBackupRun) (string, int64, string, error) {
	mode := run.mode

	// Resolve the recipients before pg_dump starts or anything touches the disk:
	// a bad key must fail the backup, never degrade it into a plaintext archive.
	recipients, err := parseAgeRecipients(run.ageRecipients)
	if err != nil {
		return "", 0, "", err
	}
	// Fail closed: deployment-level backup.exclude_table_data without a recipient
	// must never turn a critical snapshot into plaintext.
	if mode.requireRecipient && len(recipients) == 0 {
		return "", 0, "", ErrBackupCriticalNeedsRecipient
	}

	// Cancelling this context kills pg_dump at once when the copy aborts (size
	// limit, disk error) instead of waiting for it to hit the closed pipe.
	dumpCtx, cancelDump := context.WithCancel(ctx)
	defer cancelDump()
	dumpReader, err := s.dumpDatabase(dumpCtx, mode.excludeTables)
	if err != nil {
		return "", 0, "", fmt.Errorf("pg_dump: %w", err)
	}
	pattern := staleBackupFilePrefix + "*.sql.gz"
	if len(recipients) > 0 {
		pattern += ".age"
	}
	archive, err := s.createArchiveFile(pattern)
	if err != nil {
		// Close waits for pg_dump to exit; if it is still waiting for a table lock
		// that could take the whole lock timeout (or forever for a whole dump).
		cancelDump()
		_ = dumpReader.Close()
		return "", 0, "", fmt.Errorf("create backup archive: %w", err)
	}
	archivePath := archive.Name()

	limit, builtinLimit := s.archiveLimitForScope(mode.scope)
	digest := sha256.New()
	sink := &limitedArchiveWriter{w: io.MultiWriter(archive, digest), limit: limit, builtin: builtinLimit}
	var src io.Reader = dumpReader
	if run.maxRateMBps > 0 {
		src = newRateLimitedReader(dumpCtx, dumpReader, int64(run.maxRateMBps)<<20)
	}
	copyErr := s.writeBackupArchiveForScope(sink, src, recipients, mode.scope)
	if copyErr != nil {
		cancelDump()
	}
	if closeErr := dumpReader.Close(); copyErr == nil && closeErr != nil {
		copyErr = closeErr
	}
	if closeErr := archive.Close(); copyErr == nil && closeErr != nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = cleanupBackupFiles(archivePath)
		return "", 0, "", fmt.Errorf("gzip/dump failed: %w", copyErr)
	}

	info, err := os.Stat(archivePath)
	if err != nil {
		_ = cleanupBackupFiles(archivePath)
		return "", 0, "", fmt.Errorf("stat backup archive: %w", err)
	}
	return archivePath, info.Size(), hex.EncodeToString(digest.Sum(nil)), nil
}

func (s *BackupService) dumpDatabase(ctx context.Context, excludeTables []string) (io.ReadCloser, error) {
	if s.dumper == nil {
		return nil, errors.New("database dumper is not configured")
	}
	if len(excludeTables) > 0 {
		if scoped, ok := s.dumper.(ScopedDBDumper); ok {
			return scoped.DumpWithOptions(ctx, BackupDumpOptions{ExcludeTableData: excludeTables})
		}
		// When exclusions came from runtime options rather than deployment config
		// (backup.exclude_table_data), never silently fall back to Dump(ctx) which
		// would run a whole-database dump under a "critical" record.
		if len(s.backupCfg.ExcludeTableData) == 0 {
			return nil, ErrBackupScopedDumperUnsupported
		}
	}
	return s.dumper.Dump(ctx)
}

// backupArchiveFile is the staged archive. It is an *os.File in production; tests
// wrap it to make Close fail.
type backupArchiveFile interface {
	io.WriteCloser
	Name() string
}

func createStagedArchive(pattern string) (backupArchiveFile, error) {
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// recordedDigestForMode is the digest a record stores. Only a non-default
// backup records one, so a default whole-database record stays byte-identical
// to before.
func recordedDigestForMode(mode resolvedBackupMode, digest string) string {
	if mode.nonDefault {
		return digest
	}
	return ""
}

// writeBackupArchiveForScope streams src through gzip into dst, with an age
// encryptor in between when recipients are set (dump -> gzip -> age -> dst).
// Closers run innermost first, so the gzip trailer and the age final chunk are
// both in dst before the caller closes the file. A partial snapshot is re-taken
// often, so it trades compression ratio for CPU; the whole-database dump keeps
// the default.
func (s *BackupService) writeBackupArchiveForScope(dst io.Writer, src io.Reader, recipients []age.Recipient, scope string) (err error) {
	out := dst
	if len(recipients) > 0 {
		enc, encErr := s.ageEncrypt(dst, recipients...)
		if encErr != nil {
			return fmt.Errorf("create age encryptor: %w", encErr)
		}
		defer func() {
			if closeErr := enc.Close(); err == nil && closeErr != nil {
				err = closeErr
			}
		}()
		out = enc
	}

	level := gzip.DefaultCompression
	if scope != "" {
		level = gzip.BestSpeed
	}
	gzWriter, _ := gzip.NewWriterLevel(out, level) // the error is only for an invalid level
	_, err = io.Copy(gzWriter, src)
	if closeErr := gzWriter.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	return err
}

// parseAgeRecipients turns configured age public keys into recipients. The key
// text is left out of the error on purpose.
func parseAgeRecipients(keys []string) ([]age.Recipient, error) {
	for _, key := range keys {
		if isAgePrivateKey(key) {
			return nil, ErrBackupAgePrivateKeyForbidden
		}
	}
	recipients := make([]age.Recipient, 0, len(keys))
	for i, key := range keys {
		trimmed := strings.TrimSpace(key)
		recipient, err := age.ParseX25519Recipient(trimmed)
		if err != nil {
			return nil, infraerrors.BadRequest(
				"BACKUP_AGE_RECIPIENT_INVALID",
				fmt.Sprintf("backup age recipient %d is not a valid age X25519 public key", i+1),
			)
		}
		recipients = append(recipients, recipient)
	}
	return recipients, nil
}

// rateLimitedReader paces reads from r to at most bytesPerSec bytes/sec while
// honouring ctx cancellation. Throttling pg_dump's stdout pipe backpressures
// PostgreSQL reads, gzip/age CPU, and staged disk writes together.
type rateLimitedReader struct {
	ctx         context.Context
	r           io.Reader
	bytesPerSec int64
	nextAllowed time.Time
	now         func() time.Time
}

func newRateLimitedReader(ctx context.Context, r io.Reader, bytesPerSec int64) io.Reader {
	if bytesPerSec <= 0 {
		return r
	}
	return &rateLimitedReader{
		ctx:         ctx,
		r:           r,
		bytesPerSec: bytesPerSec,
		now:         time.Now,
	}
}

func (rl *rateLimitedReader) Read(p []byte) (int, error) {
	if err := rl.ctx.Err(); err != nil {
		return 0, err
	}
	now := rl.now()
	if !rl.nextAllowed.IsZero() {
		if wait := rl.nextAllowed.Sub(now); wait >= time.Millisecond {
			timer := time.NewTimer(wait)
			select {
			case <-rl.ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return 0, rl.ctx.Err()
			case <-timer.C:
			}
			now = rl.now()
		}
	}
	if len(p) > backupRateChunkBytes {
		p = p[:backupRateChunkBytes]
	}
	n, err := rl.r.Read(p)
	if n > 0 {
		cost := time.Duration(float64(n) / float64(rl.bytesPerSec) * float64(time.Second))
		if rl.nextAllowed.IsZero() || rl.nextAllowed.Before(now) {
			rl.nextAllowed = now.Add(cost)
		} else {
			rl.nextAllowed = rl.nextAllowed.Add(cost)
		}
	}
	return n, err
}

// limitedArchiveWriter counts the bytes that reach the archive on disk and fails
// the write that takes the total past limit (0 = unlimited). After the first
// failure it refuses everything, so a trailing flush cannot keep growing the file.
type limitedArchiveWriter struct {
	w       io.Writer
	limit   int64
	builtin bool // limit is the built-in one of a critical snapshot, not backup.max_archive_bytes
	written int64
	err     error
}

func (l *limitedArchiveWriter) Write(p []byte) (int, error) {
	if l.err != nil {
		return 0, l.err
	}
	n, err := l.w.Write(p)
	l.written += int64(n)
	if err == nil && l.limit > 0 && l.written > l.limit {
		source := "backup.max_archive_bytes"
		if l.builtin {
			source = "built-in default for a critical snapshot; set backup.max_archive_bytes to change it"
		}
		err = fmt.Errorf("%w: limit %d bytes (%s), written %d bytes", ErrBackupArchiveTooLarge, l.limit, source, l.written)
	}
	l.err = err
	return n, err
}

func (s *BackupService) uploadBackupArchive(ctx context.Context, record *BackupRecord, objectStore BackupObjectStore, cfg *BackupS3Config, archivePath string) error {
	info, err := os.Stat(archivePath)
	if err != nil {
		return fmt.Errorf("stat backup archive: %w", err)
	}
	partSize := s.partSizeBytes
	if partSize <= 0 {
		partSize = defaultBackupPartSizeBytes
	}
	if info.Size() <= partSize {
		if _, err := objectStore.UploadFile(ctx, record.S3Key, archivePath, backupContentType(record)); err != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), backupObjectCleanupTimeout)
			cleanupErr := deleteBackupObjectKeys(cleanupCtx, objectStore, record)
			cleanupCancel()
			return errors.Join(fmt.Errorf("backup upload: %w", err), cleanupErr)
		}
		record.Parts = nil
		return nil
	}

	localParts, err := splitBackupFile(archivePath, partSize)
	if err != nil {
		return fmt.Errorf("split backup archive: %w", err)
	}
	defer func() {
		paths := make([]string, 0, len(localParts))
		for _, part := range localParts {
			paths = append(paths, part.Path)
		}
		_ = cleanupBackupFiles(paths...)
	}()
	if cfg == nil {
		return errors.New("backup S3 config is unavailable for split upload")
	}

	record.S3Key = ""
	record.Parts = make([]BackupPart, 0, len(localParts))
	partRoot := strings.TrimRight(s.buildS3Key(cfg, record.ID), "/")
	for _, part := range localParts {
		record.Parts = append(record.Parts, BackupPart{
			Index:     part.Index,
			S3Key:     s.buildBackupPartKey(partRoot, part.Index),
			SizeBytes: part.SizeBytes,
			SHA256:    part.SHA256,
		})
	}
	if err := s.saveRecord(ctx, record); err != nil {
		return fmt.Errorf("save split backup plan: %w", err)
	}
	for i, part := range localParts {
		if _, err := objectStore.UploadFile(ctx, record.Parts[i].S3Key, part.Path, "application/octet-stream"); err != nil {
			// PUT 可能已经在对象存储端成功、但客户端因超时收到错误；
			// 因此失败时清理整份分卷计划，而不只清理此前返回成功的卷。
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), backupObjectCleanupTimeout)
			cleanupErr := deleteBackupObjectKeys(cleanupCtx, objectStore, record)
			cleanupCancel()
			return errors.Join(fmt.Errorf("upload backup part %d: %w", part.Index, err), cleanupErr)
		}
	}
	return nil
}

func (s *BackupService) buildBackupPartKey(root string, index int) string {
	return fmt.Sprintf("%s/payload.part-%06d", strings.TrimRight(root, "/"), index)
}

// RestoreBackup 从 S3 下载备份并流式恢复到数据库
func (s *BackupService) RestoreBackup(ctx context.Context, backupID string) error {
	s.opMu.Lock()
	if s.restoring {
		s.opMu.Unlock()
		return ErrRestoreInProgress
	}
	s.restoring = true
	s.opMu.Unlock()
	defer func() {
		s.opMu.Lock()
		s.restoring = false
		s.opMu.Unlock()
	}()

	record, err := s.GetBackupRecord(ctx, backupID)
	if err != nil {
		return err
	}
	if record.restoreUnsupported() {
		return ErrBackupRestoreUnsupported
	}
	if record.Status != "completed" {
		return infraerrors.BadRequest("BACKUP_NOT_COMPLETED", "can only restore from a completed backup")
	}

	s3Cfg, err := s.loadS3Config(ctx)
	if err != nil {
		return err
	}
	objectStore, err := s.getOrCreateStore(ctx, s3Cfg)
	if err != nil {
		return fmt.Errorf("init object store: %w", err)
	}

	if len(record.Parts) > 0 {
		archivePath, err := s.downloadBackupParts(ctx, objectStore, record.Parts)
		if err != nil {
			return err
		}
		defer func() { _ = cleanupBackupFiles(archivePath) }()
		return s.restoreArchive(ctx, archivePath)
	}

	// 旧记录从 S3 流式下载
	body, err := objectStore.Download(ctx, record.S3Key)
	if err != nil {
		return fmt.Errorf("S3 download failed: %w", err)
	}
	defer func() { _ = body.Close() }()

	// 流式解压 gzip -> psql（不将全部数据加载到内存）
	gzReader, err := gzip.NewReader(body)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gzReader.Close() }()

	// 流式恢复
	if err := s.dumper.Restore(ctx, gzReader); err != nil {
		return fmt.Errorf("pg restore: %w", err)
	}

	return nil
}

// StartRestore 异步恢复备份，立即返回
func (s *BackupService) StartRestore(ctx context.Context, backupID string) (*BackupRecord, error) {
	if s.shuttingDown.Load() {
		return nil, infraerrors.ServiceUnavailable("SERVER_SHUTTING_DOWN", "server is shutting down")
	}

	s.opMu.Lock()
	if s.restoring {
		s.opMu.Unlock()
		return nil, ErrRestoreInProgress
	}
	s.restoring = true
	s.opMu.Unlock()

	// 初始化阶段出错时自动重置标志
	launched := false
	defer func() {
		if !launched {
			s.opMu.Lock()
			s.restoring = false
			s.opMu.Unlock()
		}
	}()

	record, err := s.GetBackupRecord(ctx, backupID)
	if err != nil {
		return nil, err
	}
	if record.restoreUnsupported() {
		return nil, ErrBackupRestoreUnsupported
	}
	if record.Status != "completed" {
		return nil, infraerrors.BadRequest("BACKUP_NOT_COMPLETED", "can only restore from a completed backup")
	}

	s3Cfg, err := s.loadS3Config(ctx)
	if err != nil {
		return nil, err
	}
	objectStore, err := s.getOrCreateStore(ctx, s3Cfg)
	if err != nil {
		return nil, fmt.Errorf("init object store: %w", err)
	}

	record.RestoreStatus = "running"
	_ = s.saveRecord(ctx, record)

	launched = true
	result := *record

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.opMu.Lock()
			s.restoring = false
			s.opMu.Unlock()
		}()
		defer func() {
			if r := recover(); r != nil {
				logger.LegacyPrintf("service.backup", "[Backup] restore panic recovered: %v", r)
				record.RestoreStatus = "failed"
				record.RestoreError = fmt.Sprintf("internal panic: %v", r)
				_ = s.saveRecord(context.Background(), record)
			}
		}()
		s.executeRestore(record, objectStore)
	}()

	return &result, nil
}

// executeRestore 后台执行恢复
func (s *BackupService) executeRestore(record *BackupRecord, objectStore BackupObjectStore) {
	// StartRestore already refused these; this is the last guard before psql, so a
	// future caller of executeRestore cannot replay a partial or encrypted snapshot.
	if record.restoreUnsupported() {
		record.RestoreStatus = "failed"
		record.RestoreError = ErrBackupRestoreUnsupported.Message
		_ = s.saveRecord(context.Background(), record)
		return
	}

	ctx, cancel := context.WithTimeout(s.bgCtx, 30*time.Minute)
	defer cancel()

	if len(record.Parts) > 0 {
		archivePath, err := s.downloadBackupParts(ctx, objectStore, record.Parts)
		if err != nil {
			record.RestoreStatus = "failed"
			record.RestoreError = err.Error()
			_ = s.saveRecord(context.Background(), record)
			return
		}
		defer func() { _ = cleanupBackupFiles(archivePath) }()
		if err := s.restoreArchive(ctx, archivePath); err != nil {
			record.RestoreStatus = "failed"
			record.RestoreError = fmt.Sprintf("pg restore: %v", err)
			_ = s.saveRecord(context.Background(), record)
			return
		}
		record.RestoreStatus = "completed"
		record.RestoredAt = time.Now().Format(time.RFC3339)
		if err := s.saveRecord(context.Background(), record); err != nil {
			logger.LegacyPrintf("service.backup", "[Backup] 保存恢复记录失败: %v", err)
		}
		return
	}

	body, err := objectStore.Download(ctx, record.S3Key)
	if err != nil {
		record.RestoreStatus = "failed"
		record.RestoreError = fmt.Sprintf("S3 download failed: %v", err)
		_ = s.saveRecord(context.Background(), record)
		return
	}
	defer func() { _ = body.Close() }()

	gzReader, err := gzip.NewReader(body)
	if err != nil {
		record.RestoreStatus = "failed"
		record.RestoreError = fmt.Sprintf("gzip reader: %v", err)
		_ = s.saveRecord(context.Background(), record)
		return
	}
	defer func() { _ = gzReader.Close() }()

	if err := s.dumper.Restore(ctx, gzReader); err != nil {
		record.RestoreStatus = "failed"
		record.RestoreError = fmt.Sprintf("pg restore: %v", err)
		_ = s.saveRecord(context.Background(), record)
		return
	}

	record.RestoreStatus = "completed"
	record.RestoredAt = time.Now().Format(time.RFC3339)
	if err := s.saveRecord(context.Background(), record); err != nil {
		logger.LegacyPrintf("service.backup", "[Backup] 保存恢复记录失败: %v", err)
	}
}

func (s *BackupService) downloadBackupParts(ctx context.Context, objectStore BackupObjectStore, parts []BackupPart) (path string, err error) {
	if len(parts) == 0 {
		return "", errors.New("backup parts are empty")
	}
	ordered := append([]BackupPart(nil), parts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Index < ordered[j].Index })
	for i, part := range ordered {
		if part.Index != i+1 || part.S3Key == "" || part.SizeBytes <= 0 {
			return "", fmt.Errorf("invalid backup part metadata at index %d", i+1)
		}
	}

	archive, err := os.CreateTemp("", "sub2api-restore-*.sql.gz")
	if err != nil {
		return "", fmt.Errorf("create restore archive: %w", err)
	}
	path = archive.Name()
	cleanup := func() {
		_ = archive.Close()
		_ = cleanupBackupFiles(path)
	}

	for _, part := range ordered {
		body, downloadErr := objectStore.Download(ctx, part.S3Key)
		if downloadErr != nil {
			cleanup()
			return "", fmt.Errorf("download backup part %d: %w", part.Index, downloadErr)
		}
		hash := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(archive, hash), body)
		closeErr := body.Close()
		if copyErr != nil {
			cleanup()
			return "", fmt.Errorf("read backup part %d: %w", part.Index, copyErr)
		}
		if closeErr != nil {
			cleanup()
			return "", fmt.Errorf("close backup part %d: %w", part.Index, closeErr)
		}
		if written != part.SizeBytes {
			cleanup()
			return "", fmt.Errorf("backup part %d size mismatch: got %d, want %d", part.Index, written, part.SizeBytes)
		}
		if part.SHA256 != "" && !strings.EqualFold(part.SHA256, hex.EncodeToString(hash.Sum(nil))) {
			cleanup()
			return "", fmt.Errorf("backup part %d checksum mismatch", part.Index)
		}
	}
	if err := archive.Close(); err != nil {
		_ = cleanupBackupFiles(path)
		return "", fmt.Errorf("close restore archive: %w", err)
	}
	return path, nil
}

func (s *BackupService) restoreArchive(ctx context.Context, archivePath string) error {
	archive, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open restore archive: %w", err)
	}
	defer func() { _ = archive.Close() }()

	gzReader, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gzReader.Close() }()
	if err := s.dumper.Restore(ctx, gzReader); err != nil {
		return fmt.Errorf("pg restore: %w", err)
	}
	return nil
}

// ─── 备份记录管理 ───

func (s *BackupService) ListBackups(ctx context.Context) ([]BackupRecord, error) {
	records, err := s.loadRecords(ctx)
	if err != nil {
		return nil, err
	}
	// 倒序返回（最新在前）
	sort.Slice(records, func(i, j int) bool {
		return records[i].StartedAt > records[j].StartedAt
	})
	return records, nil
}

func (s *BackupService) GetBackupRecord(ctx context.Context, backupID string) (*BackupRecord, error) {
	records, err := s.loadRecords(ctx)
	if err != nil {
		return nil, err
	}
	for i := range records {
		if records[i].ID == backupID {
			return &records[i], nil
		}
	}
	return nil, ErrBackupNotFound
}

func (s *BackupService) DeleteBackup(ctx context.Context, backupID string) error {
	s.recordsMu.Lock()
	defer s.recordsMu.Unlock()

	records, err := s.loadRecordsLocked(ctx)
	if err != nil {
		return err
	}

	var found *BackupRecord
	var remaining []BackupRecord
	for i := range records {
		if records[i].ID == backupID {
			found = &records[i]
		} else {
			remaining = append(remaining, records[i])
		}
	}
	if found == nil {
		return ErrBackupNotFound
	}
	if found.Status == "running" {
		// 后台上传仍可能依赖 Parts 计划；删除对象会让随后完成的记录引用失效卷。
		return ErrBackupInProgress
	}

	// 从对象存储删除所有单文件或分卷对象。删除不完整时保留记录，便于重试。
	if err := s.deleteBackupObjects(ctx, found); err != nil {
		return err
	}

	return s.saveRecordsLocked(ctx, remaining)
}

// GetBackupDownloadURL 获取备份文件预签名下载 URL
func (s *BackupService) GetBackupDownloadURL(ctx context.Context, backupID string) (BackupDownloadResponse, error) {
	var download BackupDownloadResponse
	record, err := s.GetBackupRecord(ctx, backupID)
	if err != nil {
		return download, err
	}
	if record.Status != "completed" {
		return download, infraerrors.BadRequest("BACKUP_NOT_COMPLETED", "backup is not completed")
	}

	s3Cfg, err := s.loadS3Config(ctx)
	if err != nil {
		return download, err
	}
	objectStore, err := s.getOrCreateStore(ctx, s3Cfg)
	if err != nil {
		return download, err
	}

	if len(record.Parts) > 0 {
		parts := append([]BackupPart(nil), record.Parts...)
		sort.Slice(parts, func(i, j int) bool { return parts[i].Index < parts[j].Index })
		for i, part := range parts {
			if part.Index != i+1 || part.S3Key == "" || part.SizeBytes <= 0 {
				return download, fmt.Errorf("invalid backup part metadata at index %d", i+1)
			}
			url, presignErr := objectStore.PresignURL(ctx, part.S3Key, 1*time.Hour)
			if presignErr != nil {
				return download, fmt.Errorf("presign backup part %d: %w", part.Index, presignErr)
			}
			download.Parts = append(download.Parts, BackupDownloadPart{
				Index:     part.Index,
				SizeBytes: part.SizeBytes,
				URL:       url,
			})
		}
		return download, nil
	}
	if record.S3Key == "" {
		return download, errors.New("backup object key is empty")
	}
	url, err := objectStore.PresignURL(ctx, record.S3Key, 1*time.Hour)
	if err != nil {
		return download, fmt.Errorf("presign url: %w", err)
	}
	download.URL = url
	return download, nil
}

// ─── 内部方法 ───

func (s *BackupService) loadS3Config(ctx context.Context) (*BackupS3Config, error) {
	raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupS3Config)
	if err != nil || raw == "" {
		return nil, nil //nolint:nilnil // no config is a valid state
	}
	var cfg BackupS3Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, ErrBackupS3ConfigCorrupt
	}
	// 解密 SecretAccessKey
	if cfg.SecretAccessKey != "" {
		decrypted, err := s.encryptor.Decrypt(cfg.SecretAccessKey)
		if err != nil {
			// 兼容未加密的旧数据：如果解密失败，保持原值
			logger.LegacyPrintf("service.backup", "[Backup] S3 SecretAccessKey 解密失败（可能是旧的未加密数据）: %v", err)
		} else {
			cfg.SecretAccessKey = decrypted
		}
	}
	return &cfg, nil
}

func (s *BackupService) getOrCreateStore(ctx context.Context, cfg *BackupS3Config) (BackupObjectStore, error) {
	s.storeMu.Lock()
	defer s.storeMu.Unlock()

	if s.store != nil && s.s3Cfg != nil {
		return s.store, nil
	}

	if cfg == nil {
		return nil, ErrBackupS3NotConfigured
	}

	store, err := s.storeFactory(ctx, cfg)
	if err != nil {
		return nil, err
	}
	s.store = store
	s.s3Cfg = cfg
	return store, nil
}

func (s *BackupService) buildS3Key(cfg *BackupS3Config, fileName string) string {
	prefix := strings.TrimRight(cfg.Prefix, "/")
	if prefix == "" {
		prefix = "backups"
	}
	return fmt.Sprintf("%s/%s/%s", prefix, time.Now().Format("2006/01/02"), fileName)
}

// loadRecords 加载备份记录，区分"无数据"和"数据损坏"
func (s *BackupService) loadRecords(ctx context.Context) ([]BackupRecord, error) {
	s.recordsMu.Lock()
	defer s.recordsMu.Unlock()
	return s.loadRecordsLocked(ctx)
}

// loadRecordsLocked 在已持有 recordsMu 锁的情况下加载记录
func (s *BackupService) loadRecordsLocked(ctx context.Context) ([]BackupRecord, error) {
	raw, err := s.settingRepo.GetValue(ctx, settingKeyBackupRecords)
	if err != nil || raw == "" {
		return nil, nil //nolint:nilnil // no records is a valid state
	}
	var records []BackupRecord
	if err := json.Unmarshal([]byte(raw), &records); err != nil {
		return nil, ErrBackupRecordsCorrupt
	}
	return records, nil
}

// saveRecordsLocked 在已持有 recordsMu 锁的情况下保存记录
func (s *BackupService) saveRecordsLocked(ctx context.Context, records []BackupRecord) error {
	data, err := json.Marshal(records)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, settingKeyBackupRecords, string(data))
}

// saveRecord 保存单条记录（带互斥锁保护）
func (s *BackupService) saveRecord(ctx context.Context, record *BackupRecord) error {
	s.recordsMu.Lock()
	defer s.recordsMu.Unlock()

	records, _ := s.loadRecordsLocked(ctx)

	// 更新已有记录或追加
	found := false
	for i := range records {
		if records[i].ID == record.ID {
			records[i] = *record
			found = true
			break
		}
	}
	if !found {
		records = append(records, *record)
	}

	return s.saveRecordsLocked(ctx, s.trimRecordsLocked(records))
}

// trimRecordsLocked keeps the record list within maxBackupRecords. The oldest
// records go first and failed ones before completed ones: a failed record owns no
// object (its upload was rolled back), but a completed record is the only handle
// on an object in the bucket, so it is dropped only after that object is deleted.
// A completed record whose object cannot be deleted stays in the list, because an
// object nobody can see can never be cleaned up; the next save retries it. Running
// and pending records are never dropped. The caller holds recordsMu.
func (s *BackupService) trimRecordsLocked(records []BackupRecord) []BackupRecord {
	excess := len(records) - maxBackupRecords
	if excess <= 0 {
		return records
	}

	// By start time, not by position: cleanupOldBackups stores the list newest first.
	order := make([]int, len(records))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return records[order[a]].StartedAt < records[order[b]].StartedAt })

	ctx, cancel := context.WithTimeout(context.Background(), backupObjectCleanupTimeout)
	defer cancel()
	drop := make(map[int]bool, excess)
	for _, status := range []string{"failed", "completed"} {
		for _, i := range order {
			if excess == 0 {
				break
			}
			if records[i].Status != status {
				continue
			}
			if status == "completed" {
				if err := s.deleteBackupObjects(ctx, &records[i]); err != nil {
					logger.LegacyPrintf("service.backup", "[Backup] 记录数已达上限，但备份 %s 的对象删除失败，保留记录待下次重试: %v", records[i].ID, err)
					continue
				}
			}
			drop[i] = true
			excess--
		}
	}
	if len(drop) == 0 {
		return records
	}
	kept := make([]BackupRecord, 0, len(records)-len(drop))
	for i := range records {
		if !drop[i] {
			kept = append(kept, records[i])
		}
	}
	return kept
}

func (s *BackupService) cleanupOldBackups(ctx context.Context, schedule *BackupScheduleConfig) error {
	if schedule == nil {
		return nil
	}

	s.recordsMu.Lock()
	defer s.recordsMu.Unlock()

	records, err := s.loadRecordsLocked(ctx)
	if err != nil {
		return err
	}

	// 按时间倒序
	sort.Slice(records, func(i, j int) bool {
		return records[i].StartedAt > records[j].StartedAt
	})

	var toDelete []BackupRecord
	var toKeep []BackupRecord

	for i, r := range records {
		shouldDelete := false

		// 按保留份数清理
		if schedule.RetainCount > 0 && i >= schedule.RetainCount {
			shouldDelete = true
		}

		// 按保留天数清理
		if schedule.RetainDays > 0 && r.StartedAt != "" {
			startedAt, err := time.Parse(time.RFC3339, r.StartedAt)
			if err == nil && time.Since(startedAt) > time.Duration(schedule.RetainDays)*24*time.Hour {
				shouldDelete = true
			}
		}

		if shouldDelete && r.Status == "completed" {
			toDelete = append(toDelete, r)
		} else {
			toKeep = append(toKeep, r)
		}
	}

	var cleanupErrs []error
	deletedCount := 0
	for _, r := range toDelete {
		if err := s.deleteBackupObjects(ctx, &r); err != nil {
			// 对象删除失败时保留记录，避免丢失后续重试所需的 key。
			toKeep = append(toKeep, r)
			cleanupErrs = append(cleanupErrs, fmt.Errorf("cleanup backup %s: %w", r.ID, err))
			continue
		}
		deletedCount++
	}

	if len(toDelete) > 0 {
		if err := s.saveRecordsLocked(ctx, toKeep); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("save backup records after cleanup: %w", err))
		}
		if deletedCount > 0 {
			logger.LegacyPrintf("service.backup", "[Backup] 自动清理了 %d 个过期备份", deletedCount)
		}
		return errors.Join(cleanupErrs...)
	}
	return nil
}

// backupObjectKeys 返回一条备份记录关联的全部对象 key。
// 新记录使用 Parts，旧记录使用 S3Key；两者同时存在时也全部返回，便于清理异常残留对象。
func backupObjectKeys(record *BackupRecord) []string {
	if record == nil {
		return nil
	}
	keys := make([]string, 0, len(record.Parts)+1)
	seen := make(map[string]struct{}, len(record.Parts)+1)
	appendKey := func(key string) {
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	appendKey(record.S3Key)
	parts := append([]BackupPart(nil), record.Parts...)
	sort.Slice(parts, func(i, j int) bool { return parts[i].Index < parts[j].Index })
	for _, part := range parts {
		appendKey(part.S3Key)
	}
	return keys
}

// deleteBackupObjects 尝试删除记录关联的所有对象，并聚合删除错误。
func (s *BackupService) deleteBackupObjects(ctx context.Context, record *BackupRecord) error {
	if len(backupObjectKeys(record)) == 0 {
		return nil
	}
	s3Cfg, err := s.loadS3Config(ctx)
	if err != nil {
		return err
	}
	if s3Cfg == nil || !s3Cfg.IsConfigured() {
		// 兼容没有配置对象存储的旧记录：记录仍可被删除。
		return nil
	}
	objectStore, err := s.getOrCreateStore(ctx, s3Cfg)
	if err != nil {
		return err
	}
	return deleteBackupObjectKeys(ctx, objectStore, record)
}

func deleteBackupObjectKeys(ctx context.Context, objectStore BackupObjectStore, record *BackupRecord) error {
	keys := backupObjectKeys(record)
	if len(keys) == 0 {
		return nil
	}
	var errs []error
	for _, key := range keys {
		if deleteErr := objectStore.Delete(ctx, key); deleteErr != nil {
			errs = append(errs, fmt.Errorf("delete backup object %q: %w", key, deleteErr))
		}
	}
	return errors.Join(errs...)
}
