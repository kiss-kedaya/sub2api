// Package config provides configuration loading, defaults, and validation.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"net/textproto"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"filippo.io/age"
	"github.com/spf13/viper"
	"golang.org/x/net/http/httpguts"
)

const (
	RunModeStandard = "standard"
	RunModeSimple   = "simple"
)

const (
	DatabaseMigrationModeApply    = "apply"
	DatabaseMigrationModeValidate = "validate"
)

// 会话绑定指纹（security.session_binding_mode）允许的取值。
// 绑定指纹用于把会话限制在签发时的客户端网络环境上：
//   - off: 不计算也不校验绑定（仅作为回滚开关）；
//   - loose: IPv4 按 /24、IPv6 按 /64 归一后与 User-Agent 一起哈希（默认，容忍同一网段内的 IP 漂移）；
//   - strict: 完整 IP 与 User-Agent 一起哈希（任意变化即失效）。
const (
	SessionBindingModeOff    = "off"
	SessionBindingModeLoose  = "loose"
	SessionBindingModeStrict = "strict"
)

// 会话指纹不一致时的处置方式（security.session_binding_on_mismatch）：
//   - rebind: 只放行「浏览器家族 + 系统家族没变、只是 IP 变了」的刷新，并用当前指纹重新绑定新令牌
//     （默认；换网段/浏览器升级不掉线）；家族变了仍撤销。只对首次出示的当前令牌生效——
//     已被轮换掉的旧令牌在宽限窗口内再次出现，指纹不一致一律按重放处理。access token 校验仍返回 401，
//     由前端刷新后重绑；
//   - revoke: 撤销整个会话家族，必须重新登录（升级前的旧行为，要严格的用这个）。
const (
	SessionBindingOnMismatchRebind = "rebind"
	SessionBindingOnMismatchRevoke = "revoke"
)

// refresh token / 登录会话寿命的默认值。配置默认值与服务层对「手写 Config」的
// 防御性回退共用同一组常量，避免两处各写一个数字。
const (
	// DefaultRefreshTokenExpireDays 是每枚 refresh token 的滑动有效期（天）。
	DefaultRefreshTokenExpireDays = 14
	// DefaultSessionMaxLifetimeDays 是登录会话从首次登录起的绝对寿命上限（天）。
	DefaultSessionMaxLifetimeDays = 90
	// DefaultRefreshTokenReuseLeewaySeconds 是已轮换 refresh token 的「重试宽限」（秒）。
	// 窗口越长越容易被踩点（偷到令牌的人抢在受害者之前换一次，受害者随后带着旧令牌回来仍落在窗口内），
	// 所以取 30 秒：覆盖「响应丢失后立刻重试」，不覆盖「过一会儿再试」。
	DefaultRefreshTokenReuseLeewaySeconds = 30
	// MaxRefreshTokenReuseLeewaySeconds 是宽限的上限：再大就等于关掉重放检测。
	MaxRefreshTokenReuseLeewaySeconds = 120
)

// DefaultKeyRotationCutoff is the default value of security.key_rotation_cutoff.
// Active API keys created before this instant are treated as potentially leaked
// by the key-rotation gate and must be rotated (or put behind an IP allowlist)
// before the user may redeem codes or top up.
const DefaultKeyRotationCutoff = "2026-11-21T00:00:00+08:00"

// 使用量记录队列溢出策略
const (
	UsageRecordOverflowPolicyDrop   = "drop"
	UsageRecordOverflowPolicySample = "sample"
	UsageRecordOverflowPolicySync   = "sync"
)

// DefaultCSPPolicy is the default Content-Security-Policy with nonce support
// __CSP_NONCE__ will be replaced with actual nonce at request time by the SecurityHeaders middleware
const DefaultCSPPolicy = "default-src 'self'; worker-src 'self' blob:; script-src 'self' __CSP_NONCE__ https://challenges.cloudflare.com https://*.alicdn.com https://static.cloudflareinsights.com https://turing.captcha.qcloud.com https://turing.captcha.gtimg.com https://ca.turing.captcha.qcloud.com https://global.turing.captcha.gtimg.com https://www.tycaptcha.com https://cloudcache.tencentcs.com https://*.stripe.com https://static.airwallex.com https://checkout.airwallex.com https://static-demo.airwallex.com https://checkout-demo.airwallex.com; style-src 'self' 'unsafe-inline' https://*.captcha.gtimg.com https://fonts.googleapis.com https://*.alicdn.com https://static.airwallex.com https://checkout.airwallex.com https://static-demo.airwallex.com https://checkout-demo.airwallex.com; img-src 'self' data: blob: https:; font-src 'self' data: https://fonts.gstatic.com; connect-src 'self' https://turing.captcha.qcloud.com https://www.tycaptcha.com https://rce.tencentrio.com https:; frame-src 'self' https://challenges.cloudflare.com https://turing.captcha.qcloud.com https://ca.turing.captcha.qcloud.com https://www.tycaptcha.com https://*.stripe.com https://checkout.airwallex.com https://checkout-demo.airwallex.com; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

// UMQ（用户消息队列）模式常量
const (
	// UMQModeSerialize: 账号级串行锁 + RPM 自适应延迟
	UMQModeSerialize = "serialize"
	// UMQModeThrottle: 仅 RPM 自适应前置延迟，不阻塞并发
	UMQModeThrottle = "throttle"
)

// 连接池隔离策略常量
// 用于控制上游 HTTP 连接池的隔离粒度，影响连接复用和资源消耗
const (
	// ConnectionPoolIsolationProxy: 按代理隔离
	// 同一代理地址共享连接池，适合代理数量少、账户数量多的场景
	ConnectionPoolIsolationProxy = "proxy"
	// ConnectionPoolIsolationAccount: 按账户隔离
	// 每个账户独立连接池，适合账户数量少、需要严格隔离的场景
	ConnectionPoolIsolationAccount = "account"
	// ConnectionPoolIsolationAccountProxy: 按账户+代理组合隔离（默认）
	// 同一账户+代理组合共享连接池，提供最细粒度的隔离
	ConnectionPoolIsolationAccountProxy = "account_proxy"
)

// DefaultUpstreamResponseReadMaxBytes 上游非流式响应体的默认读取上限。
// 128 MB 可容纳 2-3 张 4K PNG（base64 膨胀 33%，单张 4K PNG 最坏约 67MB base64）。
// 可通过 gateway.upstream_response_read_max_bytes 配置项覆盖。
const DefaultUpstreamResponseReadMaxBytes int64 = 128 * 1024 * 1024

// DefaultModelsListReadMaxBytes 上游模型列表响应体的默认读取上限。
// 可通过 gateway.models_list_read_max_bytes 配置项覆盖。
const DefaultModelsListReadMaxBytes int64 = 8 * 1024 * 1024

type Config struct {
	Server                  ServerConfig                  `mapstructure:"server"`
	Log                     LogConfig                     `mapstructure:"log"`
	CORS                    CORSConfig                    `mapstructure:"cors"`
	Security                SecurityConfig                `mapstructure:"security"`
	Billing                 BillingConfig                 `mapstructure:"billing"`
	Turnstile               TurnstileConfig               `mapstructure:"turnstile"`
	Cloudflare              CloudflareConfig              `mapstructure:"cloudflare"`
	Database                DatabaseConfig                `mapstructure:"database"`
	Redis                   RedisConfig                   `mapstructure:"redis"`
	UsageStatsCache         UsageStatsCacheConfig         `mapstructure:"usage_stats_cache"`
	Ops                     OpsConfig                     `mapstructure:"ops"`
	JWT                     JWTConfig                     `mapstructure:"jwt"`
	Totp                    TotpConfig                    `mapstructure:"totp"`
	WebAuthn                WebAuthnConfig                `mapstructure:"webauthn"`
	LinuxDo                 LinuxDoConnectConfig          `mapstructure:"linuxdo_connect"`
	WeChat                  WeChatConnectConfig           `mapstructure:"wechat_connect"`
	OIDC                    OIDCConnectConfig             `mapstructure:"oidc_connect"`
	DingTalk                DingTalkConnectConfig         `mapstructure:"dingtalk_connect"`
	GitHubOAuth             EmailOAuthProviderConfig      `mapstructure:"github_oauth"`
	GoogleOAuth             EmailOAuthProviderConfig      `mapstructure:"google_oauth"`
	Default                 DefaultConfig                 `mapstructure:"default"`
	RateLimit               RateLimitConfig               `mapstructure:"rate_limit"`
	Pricing                 PricingConfig                 `mapstructure:"pricing"`
	Gateway                 GatewayConfig                 `mapstructure:"gateway"`
	APIKeyAuth              APIKeyAuthCacheConfig         `mapstructure:"api_key_auth_cache"`
	APIKeyCreate            APIKeyCreateConfig            `mapstructure:"api_key_create"`
	SubscriptionCache       SubscriptionCacheConfig       `mapstructure:"subscription_cache"`
	SubscriptionMaintenance SubscriptionMaintenanceConfig `mapstructure:"subscription_maintenance"`
	Dashboard               DashboardCacheConfig          `mapstructure:"dashboard_cache"`
	DashboardAgg            DashboardAggregationConfig    `mapstructure:"dashboard_aggregation"`
	UsageCleanup            UsageCleanupConfig            `mapstructure:"usage_cleanup"`
	Concurrency             ConcurrencyConfig             `mapstructure:"concurrency"`
	TokenRefresh            TokenRefreshConfig            `mapstructure:"token_refresh"`
	RunMode                 string                        `mapstructure:"run_mode" yaml:"run_mode"`
	Timezone                string                        `mapstructure:"timezone"` // e.g. "Asia/Shanghai", "UTC"
	Gemini                  GeminiConfig                  `mapstructure:"gemini"`
	Update                  UpdateConfig                  `mapstructure:"update"`
	Idempotency             IdempotencyConfig             `mapstructure:"idempotency"`
	BatchImage              BatchImageConfig              `mapstructure:"batch_image"`
	ImageStorage            ImageStorageConfig            `mapstructure:"image_storage"`
	Backup                  BackupConfig                  `mapstructure:"backup"`
	Plugins                 PluginConfig                  `mapstructure:"plugins"`
}

// PluginConfig 控制管理员手动上传的本地进程插件。
// 默认不包含插件，也不允许安装未签名插件；TrustedPublishers 用于追加第三方发布者。
type PluginConfig struct {
	DataDir              string            `mapstructure:"data_dir"`
	AllowUnsigned        bool              `mapstructure:"allow_unsigned"`
	TrustedPublishers    map[string]string `mapstructure:"trusted_publishers"`
	MaxUploadBytes       int64             `mapstructure:"max_upload_bytes"`
	MaxUncompressedBytes int64             `mapstructure:"max_uncompressed_bytes"`
	StartTimeoutSeconds  int               `mapstructure:"start_timeout_seconds"`
}

type LogConfig struct {
	Level           string            `mapstructure:"level"`
	Format          string            `mapstructure:"format"`
	ServiceName     string            `mapstructure:"service_name"`
	Environment     string            `mapstructure:"env"`
	Caller          bool              `mapstructure:"caller"`
	StacktraceLevel string            `mapstructure:"stacktrace_level"`
	Output          LogOutputConfig   `mapstructure:"output"`
	Rotation        LogRotationConfig `mapstructure:"rotation"`
	Sampling        LogSamplingConfig `mapstructure:"sampling"`
}

type LogOutputConfig struct {
	ToStdout bool   `mapstructure:"to_stdout"`
	ToFile   bool   `mapstructure:"to_file"`
	FilePath string `mapstructure:"file_path"`
}

type LogRotationConfig struct {
	MaxSizeMB  int  `mapstructure:"max_size_mb"`
	MaxBackups int  `mapstructure:"max_backups"`
	MaxAgeDays int  `mapstructure:"max_age_days"`
	Compress   bool `mapstructure:"compress"`
	LocalTime  bool `mapstructure:"local_time"`
}

type LogSamplingConfig struct {
	Enabled    bool `mapstructure:"enabled"`
	Initial    int  `mapstructure:"initial"`
	Thereafter int  `mapstructure:"thereafter"`
}

type GeminiConfig struct {
	OAuth GeminiOAuthConfig `mapstructure:"oauth"`
	Quota GeminiQuotaConfig `mapstructure:"quota"`
}

type GeminiOAuthConfig struct {
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
	Scopes       string `mapstructure:"scopes"`
}

type GeminiQuotaConfig struct {
	Tiers  map[string]GeminiTierQuotaConfig `mapstructure:"tiers"`
	Policy string                           `mapstructure:"policy"`
}

type GeminiTierQuotaConfig struct {
	ProRPD          *int64 `mapstructure:"pro_rpd" json:"pro_rpd"`
	FlashRPD        *int64 `mapstructure:"flash_rpd" json:"flash_rpd"`
	CooldownMinutes *int   `mapstructure:"cooldown_minutes" json:"cooldown_minutes"`
}

type UpdateConfig struct {
	// ProxyURL 用于访问 GitHub 的代理地址
	// 支持 http/https/socks5/socks5h 协议
	// 例如: "http://127.0.0.1:7890", "socks5://127.0.0.1:1080"
	ProxyURL string `mapstructure:"proxy_url"`
}

type IdempotencyConfig struct {
	// ObserveOnly 为 true 时处于观察期：未携带 Idempotency-Key 的请求继续放行。
	ObserveOnly bool `mapstructure:"observe_only"`
	// DefaultTTLSeconds 关键写接口的幂等记录默认 TTL（秒）。
	DefaultTTLSeconds int `mapstructure:"default_ttl_seconds"`
	// SystemOperationTTLSeconds 系统操作接口的幂等记录 TTL（秒）。
	SystemOperationTTLSeconds int `mapstructure:"system_operation_ttl_seconds"`
	// ProcessingTimeoutSeconds processing 状态锁超时（秒）。
	ProcessingTimeoutSeconds int `mapstructure:"processing_timeout_seconds"`
	// FailedRetryBackoffSeconds 失败退避窗口（秒）。
	FailedRetryBackoffSeconds int `mapstructure:"failed_retry_backoff_seconds"`
	// MaxStoredResponseLen 持久化响应体最大长度（字节）。
	MaxStoredResponseLen int `mapstructure:"max_stored_response_len"`
	// CleanupIntervalSeconds 过期记录清理周期（秒）。
	CleanupIntervalSeconds int `mapstructure:"cleanup_interval_seconds"`
	// CleanupBatchSize 每次清理的最大记录数。
	CleanupBatchSize int `mapstructure:"cleanup_batch_size"`
}

type BatchImageConfig struct {
	Enabled                           bool   `mapstructure:"enabled"`
	MaxItemsPerJobDefault             int    `mapstructure:"max_items_per_job_default"`
	MaxItemsPerJobTrial               int    `mapstructure:"max_items_per_job_trial"`
	MaxOutputImagesPerJob             int    `mapstructure:"max_output_images_per_job"`
	MaxOutputImagesPerItem            int    `mapstructure:"max_output_images_per_item"`
	MaxPromptCharsPerItem             int    `mapstructure:"max_prompt_chars_per_item"`
	MaxReferenceImagesPerJob          int    `mapstructure:"max_reference_images_per_job"`
	MaxReferenceInlineBytesPerJob     int    `mapstructure:"max_reference_inline_bytes_per_job"`
	DefaultResponseMimeType           string `mapstructure:"default_response_mime_type"`
	DefaultImageSize                  string `mapstructure:"default_image_size"`
	MaxDownloadItemsZip               int    `mapstructure:"max_download_items_zip"`
	MaxDownloadBytesPerRequest        int64  `mapstructure:"max_download_bytes_per_request"`
	MaxDownloadDurationSeconds        int    `mapstructure:"max_download_duration_seconds"`
	MaxDownloadConcurrencyPerUser     int    `mapstructure:"max_download_concurrency_per_user"`
	InputRetentionAfterTerminalHours  int    `mapstructure:"input_retention_after_terminal_hours"`
	OutputRetentionAfterTerminalHours int    `mapstructure:"output_retention_after_terminal_hours"`
	OutputRetentionMaxDays            int    `mapstructure:"output_retention_max_days"`
	CleanupIntervalMinutes            int    `mapstructure:"cleanup_interval_minutes"`
	CleanupBatchSize                  int    `mapstructure:"cleanup_batch_size"`
	QueueEnabled                      bool   `mapstructure:"queue_enabled"`
	QueueReadyKey                     string `mapstructure:"queue_ready_key"`
	QueueDelayedKey                   string `mapstructure:"queue_delayed_key"`
	QueueActiveKey                    string `mapstructure:"queue_active_key"`
	InflightKeyPrefix                 string `mapstructure:"inflight_key_prefix"`
	LockKeyPrefix                     string `mapstructure:"lock_key_prefix"`
	IdempotencyKeyPrefix              string `mapstructure:"idempotency_key_prefix"`
	InflightTTLSeconds                int    `mapstructure:"inflight_ttl_seconds"`
	JobLockTTLSeconds                 int    `mapstructure:"job_lock_ttl_seconds"`
	DefaultRequeueDelaySeconds        int    `mapstructure:"default_requeue_delay_seconds"`
	ErrorRetryDelaySeconds            int    `mapstructure:"error_retry_delay_seconds"`
	LockConflictDelaySeconds          int    `mapstructure:"lock_conflict_delay_seconds"`
	StaleActiveAfterSeconds           int    `mapstructure:"stale_active_after_seconds"`
	DelayedMoverIntervalSeconds       int    `mapstructure:"delayed_mover_interval_seconds"`
	RecoveryIntervalSeconds           int    `mapstructure:"recovery_interval_seconds"`
	DelayedMoveLimit                  int    `mapstructure:"delayed_move_limit"`
	RecoverLimit                      int    `mapstructure:"recover_limit"`
	VertexEnabled                     bool   `mapstructure:"vertex_enabled"`
	VertexProjectID                   string `mapstructure:"vertex_project_id"`
	VertexLocation                    string `mapstructure:"vertex_location"`
	// VertexManagedGCSBucket is a server-owned bucket for batch JSONL input/output.
	// Disable Cloud Storage soft delete on this bucket to avoid retaining deleted batch objects.
	VertexManagedGCSBucket       string `mapstructure:"vertex_managed_gcs_bucket"`
	VertexManagedGCSPrefix       string `mapstructure:"vertex_managed_gcs_prefix"`
	VertexInputRetentionHours    int    `mapstructure:"vertex_input_retention_hours"`
	VertexOutputRetentionHours   int    `mapstructure:"vertex_output_retention_hours"`
	VertexBatchPredictionBaseURL string `mapstructure:"vertex_batch_prediction_base_url"`
	VertexGCSBaseURL             string `mapstructure:"vertex_gcs_base_url"`
}

// ImageStorageConfig 配置异步图片任务结果上传的 S3 兼容对象存储。
// Enabled 同时作为异步图片任务功能的总开关：未启用或未配置完整凭证时，
// 异步生图接口整体禁用，避免把上游返回的大 base64 结果塞进 Redis。
type ImageStorageConfig struct {
	Enabled         bool   `mapstructure:"enabled"`
	Endpoint        string `mapstructure:"endpoint"` // e.g. https://<account_id>.r2.cloudflarestorage.com
	Region          string `mapstructure:"region"`   // R2 用 "auto"
	Bucket          string `mapstructure:"bucket"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	SecretAccessKey string `mapstructure:"secret_access_key"`
	Prefix          string `mapstructure:"prefix"`               // S3 key 前缀，如 "images/"
	ForcePathStyle  bool   `mapstructure:"force_path_style"`     // MinIO/路径风格桶
	PublicBaseURL   string `mapstructure:"public_base_url"`      // 配了则返回 public_base_url/key 直链；否则 presigned
	PresignExpiry   int    `mapstructure:"presign_expiry_hours"` // public_base_url 为空时的 presigned 过期时长(小时)
	MaxDownloadByte int64  `mapstructure:"max_download_bytes"`   // 下载上游 url 图片的字节上限
}

// IsConfigured 检查对象存储必要字段是否已配置
func (c *ImageStorageConfig) IsConfigured() bool {
	return c.Bucket != "" && c.AccessKeyID != "" && c.SecretAccessKey != ""
}

// Active 返回异步图片任务是否可用：开关打开且凭证齐全
func (c *ImageStorageConfig) Active() bool {
	return c.Enabled && c.IsConfigured()
}

// MissingCredentialKeys 返回 IsConfigured 所缺的配置键名。
// 用于启动日志：只说"凭证不完整"会让运维以为自己漏填了，而实际可能是值填了却没被读到。
func (c *ImageStorageConfig) MissingCredentialKeys() []string {
	var missing []string
	if c.Bucket == "" {
		missing = append(missing, "image_storage.bucket")
	}
	if c.AccessKeyID == "" {
		missing = append(missing, "image_storage.access_key_id")
	}
	if c.SecretAccessKey == "" {
		missing = append(missing, "image_storage.secret_access_key")
	}
	return missing
}

// BackupConfig tunes the built-in database backup. Every field defaults to its
// zero value, which keeps the original behaviour: a full pg_dump --clean
// --if-exists, default gzip, no encryption, no size limit.
type BackupConfig struct {
	// PgDumpPath overrides the pg_dump executable; empty resolves "pg_dump" via PATH.
	PgDumpPath string `mapstructure:"pg_dump_path"`
	// ExcludeTableData lists tables (name or schema.name) whose rows are not
	// dumped while their schema still is. A non-empty list switches the backup to
	// the partial "critical snapshot" mode.
	ExcludeTableData []string `mapstructure:"exclude_table_data"`
	// AgeRecipients are age X25519 public keys (age1...). When set, the archive is
	// encrypted to them before it leaves the host; when unset nothing is encrypted.
	AgeRecipients []string `mapstructure:"age_recipients"`
	// MaxArchiveBytes aborts a backup whose archive on disk (after compression and
	// encryption) grows past this size. 0 means no limit.
	MaxArchiveBytes int64 `mapstructure:"max_archive_bytes"`
}

// backupExcludeTablePattern only admits "table" or "schema.table" made of plain
// identifier characters: no wildcards, quotes, dashes or spaces. pg_dump still
// folds an unquoted name to lower case and silently ignores an entry that matches
// no table (a partitioned table's children included), so a size limit, not this
// pattern, is what bounds a snapshot whose exclude list is wrong.
var backupExcludeTablePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)?$`)

// normalizeAndValidate trims the values and rejects anything unsafe. Error
// messages carry only the key and entry index, never the offending value: an
// age private key pasted into age_recipients must not end up in a log.
func (b *BackupConfig) normalizeAndValidate() error {
	b.PgDumpPath = strings.TrimSpace(b.PgDumpPath)
	b.ExcludeTableData = normalizeStringSlice(b.ExcludeTableData)
	b.AgeRecipients = normalizeStringSlice(b.AgeRecipients)

	// The path becomes an argument of nice(1) in partial mode, so a leading dash
	// would be parsed as a nice option.
	if strings.HasPrefix(b.PgDumpPath, "-") {
		return fmt.Errorf("backup.pg_dump_path must not start with '-'")
	}
	for i, table := range b.ExcludeTableData {
		if !backupExcludeTablePattern.MatchString(table) {
			return fmt.Errorf("backup.exclude_table_data[%d] must be a table or schema.table name using only letters, digits and underscores", i)
		}
	}
	for i, recipient := range b.AgeRecipients {
		if _, err := age.ParseX25519Recipient(recipient); err != nil {
			return fmt.Errorf("backup.age_recipients[%d] is not a valid age X25519 public key", i)
		}
	}
	if b.MaxArchiveBytes < 0 {
		return fmt.Errorf("backup.max_archive_bytes must be non-negative")
	}
	// A critical snapshot carries users, API keys and upstream credentials, so it
	// must never be uploaded as plaintext. An empty list (a misspelt variable name,
	// a secret injected as an empty string, only blanks and separators) is refused
	// here instead of being read as "encryption off"; the backup service checks it
	// again right before pg_dump starts.
	if len(b.ExcludeTableData) > 0 && len(b.AgeRecipients) == 0 {
		return fmt.Errorf("backup.age_recipients must hold at least one age X25519 public key when backup.exclude_table_data is set")
	}
	return nil
}

type LinuxDoConnectConfig struct {
	Enabled             bool   `mapstructure:"enabled"`
	ClientID            string `mapstructure:"client_id"`
	ClientSecret        string `mapstructure:"client_secret"`
	AuthorizeURL        string `mapstructure:"authorize_url"`
	TokenURL            string `mapstructure:"token_url"`
	UserInfoURL         string `mapstructure:"userinfo_url"`
	Scopes              string `mapstructure:"scopes"`
	RedirectURL         string `mapstructure:"redirect_url"`          // 后端回调地址（需在提供方后台登记）
	FrontendRedirectURL string `mapstructure:"frontend_redirect_url"` // 前端接收 token 的路由（默认：/auth/linuxdo/callback）
	TokenAuthMethod     string `mapstructure:"token_auth_method"`     // client_secret_post / client_secret_basic / none
	UsePKCE             bool   `mapstructure:"use_pkce"`

	// 可选：用于从 userinfo JSON 中提取字段的 gjson 路径。
	// 为空时，服务端会尝试一组常见字段名。
	UserInfoEmailPath    string `mapstructure:"userinfo_email_path"`
	UserInfoIDPath       string `mapstructure:"userinfo_id_path"`
	UserInfoUsernamePath string `mapstructure:"userinfo_username_path"`
}

type WeChatConnectConfig struct {
	Enabled             bool   `mapstructure:"enabled"`
	AppID               string `mapstructure:"app_id"`
	AppSecret           string `mapstructure:"app_secret"`
	OpenAppID           string `mapstructure:"open_app_id"`
	OpenAppSecret       string `mapstructure:"open_app_secret"`
	MPAppID             string `mapstructure:"mp_app_id"`
	MPAppSecret         string `mapstructure:"mp_app_secret"`
	MobileAppID         string `mapstructure:"mobile_app_id"`
	MobileAppSecret     string `mapstructure:"mobile_app_secret"`
	OpenEnabled         bool   `mapstructure:"open_enabled"`
	MPEnabled           bool   `mapstructure:"mp_enabled"`
	MobileEnabled       bool   `mapstructure:"mobile_enabled"`
	Mode                string `mapstructure:"mode"`
	Scopes              string `mapstructure:"scopes"`
	RedirectURL         string `mapstructure:"redirect_url"`
	FrontendRedirectURL string `mapstructure:"frontend_redirect_url"`
}

type OIDCConnectConfig struct {
	Enabled                 bool   `mapstructure:"enabled"`
	ProviderName            string `mapstructure:"provider_name"` // 显示名: "Keycloak" 等
	ClientID                string `mapstructure:"client_id"`
	ClientSecret            string `mapstructure:"client_secret"`
	IssuerURL               string `mapstructure:"issuer_url"`
	DiscoveryURL            string `mapstructure:"discovery_url"`
	AuthorizeURL            string `mapstructure:"authorize_url"`
	TokenURL                string `mapstructure:"token_url"`
	UserInfoURL             string `mapstructure:"userinfo_url"`
	JWKSURL                 string `mapstructure:"jwks_url"`
	Scopes                  string `mapstructure:"scopes"`                // 默认 "openid email profile"
	RedirectURL             string `mapstructure:"redirect_url"`          // 后端回调地址（需在提供方后台登记）
	FrontendRedirectURL     string `mapstructure:"frontend_redirect_url"` // 前端接收 token 的路由（默认：/auth/oidc/callback）
	TokenAuthMethod         string `mapstructure:"token_auth_method"`     // client_secret_post / client_secret_basic / none
	UsePKCE                 bool   `mapstructure:"use_pkce"`
	ValidateIDToken         bool   `mapstructure:"validate_id_token"`
	UsePKCEExplicit         bool   `mapstructure:"-" yaml:"-"`
	ValidateIDTokenExplicit bool   `mapstructure:"-" yaml:"-"`
	AllowedSigningAlgs      string `mapstructure:"allowed_signing_algs"`   // 默认 "RS256,ES256,PS256"
	ClockSkewSeconds        int    `mapstructure:"clock_skew_seconds"`     // 默认 120
	RequireEmailVerified    bool   `mapstructure:"require_email_verified"` // 默认 false

	// 可选：用于从 userinfo JSON 中提取字段的 gjson 路径。
	// 为空时，服务端会尝试一组常见字段名。
	UserInfoEmailPath    string `mapstructure:"userinfo_email_path"`
	UserInfoIDPath       string `mapstructure:"userinfo_id_path"`
	UserInfoUsernamePath string `mapstructure:"userinfo_username_path"`
}

type DingTalkConnectConfig struct {
	Enabled             bool   `mapstructure:"enabled"`
	ClientID            string `mapstructure:"client_id"`
	ClientSecret        string `mapstructure:"client_secret"`
	AuthorizeURL        string `mapstructure:"authorize_url"`
	TokenURL            string `mapstructure:"token_url"`
	UserInfoURL         string `mapstructure:"userinfo_url"`
	Scopes              string `mapstructure:"scopes"`
	RedirectURL         string `mapstructure:"redirect_url"`
	FrontendRedirectURL string `mapstructure:"frontend_redirect_url"`

	// 平台底座 + 业务行为
	DingTalkAppKind string `mapstructure:"dingtalk_app_kind"` // 仅 "internal_app"（V4 fail-closed）
	AppType         string `mapstructure:"app_type"`          // "public" (default) | "internal"

	// Corp 限定（none | internal_only）
	CorpRestrictionPolicy   string `mapstructure:"corp_restriction_policy"`
	InternalCorpID          string `mapstructure:"internal_corp_id"`
	BypassRegistration      bool   `mapstructure:"bypass_registration"`
	SyncCorpEmail           bool   `mapstructure:"sync_corp_email"`
	SyncDisplayName         bool   `mapstructure:"sync_display_name"`
	SyncDept                bool   `mapstructure:"sync_dept"`
	SyncCorpEmailAttrKey    string `mapstructure:"sync_corp_email_attr_key"`
	SyncDisplayNameAttrKey  string `mapstructure:"sync_display_name_attr_key"`
	SyncDeptAttrKey         string `mapstructure:"sync_dept_attr_key"`
	SyncCorpEmailAttrName   string `mapstructure:"sync_corp_email_attr_name"`
	SyncDisplayNameAttrName string `mapstructure:"sync_display_name_attr_name"`
	SyncDeptAttrName        string `mapstructure:"sync_dept_attr_name"`

	// 邮箱 + Username
	RequireEmail            bool   `mapstructure:"require_email"`
	UsernameOverwritePolicy string `mapstructure:"username_overwrite_policy"`

	// Attribute（私有版扩展点；开源版仅声明）
	UsernameAttributeKey         string   `mapstructure:"username_attribute_key"`
	EnableAttributeMatching      bool     `mapstructure:"enable_attribute_matching"`
	EnableAttributeSync          bool     `mapstructure:"enable_attribute_sync"`
	AttributeSyncFields          []string `mapstructure:"attribute_sync_fields"`
	AttributeSyncOverwritePolicy string   `mapstructure:"attribute_sync_overwrite_policy"`
}

type EmailOAuthProviderConfig struct {
	Enabled             bool   `mapstructure:"enabled"`
	ClientID            string `mapstructure:"client_id"`
	ClientSecret        string `mapstructure:"client_secret"`
	AuthorizeURL        string `mapstructure:"authorize_url"`
	TokenURL            string `mapstructure:"token_url"`
	UserInfoURL         string `mapstructure:"userinfo_url"`
	EmailsURL           string `mapstructure:"emails_url"`
	Scopes              string `mapstructure:"scopes"`
	RedirectURL         string `mapstructure:"redirect_url"`
	FrontendRedirectURL string `mapstructure:"frontend_redirect_url"`
}

const (
	defaultWeChatConnectMode             = "open"
	defaultWeChatConnectScopes           = "snsapi_login"
	defaultWeChatConnectFrontendRedirect = "/auth/wechat/callback"
)

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeWeChatConnectMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "mp":
		return "mp"
	case "mobile":
		return "mobile"
	default:
		return defaultWeChatConnectMode
	}
}

func normalizeWeChatConnectStoredMode(openEnabled, mpEnabled, mobileEnabled bool, mode string) string {
	mode = normalizeWeChatConnectMode(mode)
	switch mode {
	case "open":
		if openEnabled {
			return "open"
		}
	case "mp":
		if mpEnabled {
			return "mp"
		}
	case "mobile":
		if mobileEnabled {
			return "mobile"
		}
	}
	switch {
	case openEnabled:
		return "open"
	case mpEnabled:
		return "mp"
	case mobileEnabled:
		return "mobile"
	default:
		return mode
	}
}

func defaultWeChatConnectScopesForMode(mode string) string {
	switch normalizeWeChatConnectMode(mode) {
	case "mp":
		return "snsapi_userinfo"
	case "mobile":
		return ""
	default:
		return defaultWeChatConnectScopes
	}
}

func normalizeWeChatConnectScopes(raw, mode string) string {
	switch normalizeWeChatConnectMode(mode) {
	case "mp":
		switch strings.TrimSpace(raw) {
		case "snsapi_base":
			return "snsapi_base"
		case "snsapi_userinfo":
			return "snsapi_userinfo"
		default:
			return defaultWeChatConnectScopesForMode(mode)
		}
	case "mobile":
		return ""
	default:
		return defaultWeChatConnectScopes
	}
}

func shouldApplyLegacyWeChatEnv(configKey, envKey string) bool {
	if viper.InConfig(configKey) {
		return false
	}
	_, hasNewEnv := os.LookupEnv(envKey)
	return !hasNewEnv
}

func hasExplicitConfigOrEnv(configKey, envKey string) bool {
	if viper.InConfig(configKey) {
		return true
	}
	_, ok := os.LookupEnv(envKey)
	return ok
}

func applyLegacyWeChatConnectEnvCompatibility(cfg *WeChatConnectConfig) {
	if cfg == nil {
		return
	}

	legacyOpenAppID := ""
	if shouldApplyLegacyWeChatEnv("wechat_connect.open_app_id", "WECHAT_CONNECT_OPEN_APP_ID") &&
		shouldApplyLegacyWeChatEnv("wechat_connect.app_id", "WECHAT_CONNECT_APP_ID") {
		legacyOpenAppID = strings.TrimSpace(os.Getenv("WECHAT_OAUTH_OPEN_APP_ID"))
		if legacyOpenAppID != "" {
			cfg.OpenAppID = legacyOpenAppID
		}
	}

	legacyOpenAppSecret := ""
	if shouldApplyLegacyWeChatEnv("wechat_connect.open_app_secret", "WECHAT_CONNECT_OPEN_APP_SECRET") &&
		shouldApplyLegacyWeChatEnv("wechat_connect.app_secret", "WECHAT_CONNECT_APP_SECRET") {
		legacyOpenAppSecret = strings.TrimSpace(os.Getenv("WECHAT_OAUTH_OPEN_APP_SECRET"))
		if legacyOpenAppSecret != "" {
			cfg.OpenAppSecret = legacyOpenAppSecret
		}
	}

	legacyMPAppID := ""
	if shouldApplyLegacyWeChatEnv("wechat_connect.mp_app_id", "WECHAT_CONNECT_MP_APP_ID") &&
		shouldApplyLegacyWeChatEnv("wechat_connect.app_id", "WECHAT_CONNECT_APP_ID") {
		legacyMPAppID = strings.TrimSpace(os.Getenv("WECHAT_OAUTH_MP_APP_ID"))
		if legacyMPAppID != "" {
			cfg.MPAppID = legacyMPAppID
		}
	}

	legacyMPAppSecret := ""
	if shouldApplyLegacyWeChatEnv("wechat_connect.mp_app_secret", "WECHAT_CONNECT_MP_APP_SECRET") &&
		shouldApplyLegacyWeChatEnv("wechat_connect.app_secret", "WECHAT_CONNECT_APP_SECRET") {
		legacyMPAppSecret = strings.TrimSpace(os.Getenv("WECHAT_OAUTH_MP_APP_SECRET"))
		if legacyMPAppSecret != "" {
			cfg.MPAppSecret = legacyMPAppSecret
		}
	}

	if shouldApplyLegacyWeChatEnv("wechat_connect.frontend_redirect_url", "WECHAT_CONNECT_FRONTEND_REDIRECT_URL") {
		if legacyFrontend := strings.TrimSpace(os.Getenv("WECHAT_OAUTH_FRONTEND_REDIRECT_URL")); legacyFrontend != "" {
			cfg.FrontendRedirectURL = legacyFrontend
		}
	}

	hasLegacyOpen := legacyOpenAppID != "" && legacyOpenAppSecret != ""
	hasLegacyMP := legacyMPAppID != "" && legacyMPAppSecret != ""

	if shouldApplyLegacyWeChatEnv("wechat_connect.enabled", "WECHAT_CONNECT_ENABLED") && (hasLegacyOpen || hasLegacyMP) {
		cfg.Enabled = true
	}
	if shouldApplyLegacyWeChatEnv("wechat_connect.open_enabled", "WECHAT_CONNECT_OPEN_ENABLED") && hasLegacyOpen {
		cfg.OpenEnabled = true
	}
	if shouldApplyLegacyWeChatEnv("wechat_connect.mp_enabled", "WECHAT_CONNECT_MP_ENABLED") && hasLegacyMP {
		cfg.MPEnabled = true
	}
	if shouldApplyLegacyWeChatEnv("wechat_connect.mode", "WECHAT_CONNECT_MODE") {
		switch {
		case hasLegacyMP && !hasLegacyOpen:
			cfg.Mode = "mp"
		case hasLegacyOpen:
			cfg.Mode = "open"
		}
	}
	if shouldApplyLegacyWeChatEnv("wechat_connect.scopes", "WECHAT_CONNECT_SCOPES") {
		switch {
		case hasLegacyMP && !hasLegacyOpen:
			cfg.Scopes = defaultWeChatConnectScopesForMode("mp")
		case hasLegacyOpen:
			cfg.Scopes = defaultWeChatConnectScopesForMode("open")
		}
	}
}

func normalizeWeChatConnectConfig(cfg *WeChatConnectConfig) {
	if cfg == nil {
		return
	}

	cfg.AppID = strings.TrimSpace(cfg.AppID)
	cfg.AppSecret = strings.TrimSpace(cfg.AppSecret)
	cfg.OpenAppID = strings.TrimSpace(cfg.OpenAppID)
	cfg.OpenAppSecret = strings.TrimSpace(cfg.OpenAppSecret)
	cfg.MPAppID = strings.TrimSpace(cfg.MPAppID)
	cfg.MPAppSecret = strings.TrimSpace(cfg.MPAppSecret)
	cfg.MobileAppID = strings.TrimSpace(cfg.MobileAppID)
	cfg.MobileAppSecret = strings.TrimSpace(cfg.MobileAppSecret)
	cfg.Mode = normalizeWeChatConnectMode(cfg.Mode)
	cfg.RedirectURL = strings.TrimSpace(cfg.RedirectURL)
	cfg.FrontendRedirectURL = strings.TrimSpace(cfg.FrontendRedirectURL)

	cfg.AppID = firstNonEmptyString(cfg.AppID, cfg.OpenAppID, cfg.MPAppID, cfg.MobileAppID)
	cfg.AppSecret = firstNonEmptyString(cfg.AppSecret, cfg.OpenAppSecret, cfg.MPAppSecret, cfg.MobileAppSecret)
	cfg.OpenAppID = firstNonEmptyString(cfg.OpenAppID, cfg.AppID)
	cfg.OpenAppSecret = firstNonEmptyString(cfg.OpenAppSecret, cfg.AppSecret)
	cfg.MPAppID = firstNonEmptyString(cfg.MPAppID, cfg.AppID)
	cfg.MPAppSecret = firstNonEmptyString(cfg.MPAppSecret, cfg.AppSecret)
	cfg.MobileAppID = firstNonEmptyString(cfg.MobileAppID, cfg.AppID)
	cfg.MobileAppSecret = firstNonEmptyString(cfg.MobileAppSecret, cfg.AppSecret)

	if !cfg.OpenEnabled && !cfg.MPEnabled && !cfg.MobileEnabled && cfg.Enabled {
		switch cfg.Mode {
		case "mp":
			cfg.MPEnabled = true
		case "mobile":
			cfg.MobileEnabled = true
		default:
			cfg.OpenEnabled = true
		}
	}
	cfg.Mode = normalizeWeChatConnectStoredMode(cfg.OpenEnabled, cfg.MPEnabled, cfg.MobileEnabled, cfg.Mode)
	cfg.Scopes = normalizeWeChatConnectScopes(cfg.Scopes, cfg.Mode)
	if cfg.FrontendRedirectURL == "" {
		cfg.FrontendRedirectURL = defaultWeChatConnectFrontendRedirect
	}
}

// TokenRefreshConfig OAuth token自动刷新配置
type TokenRefreshConfig struct {
	// 是否启用自动刷新
	Enabled bool `mapstructure:"enabled"`
	// 检查间隔（分钟）
	CheckIntervalMinutes int `mapstructure:"check_interval_minutes"`
	// 提前刷新时间（小时），在token过期前多久开始刷新
	RefreshBeforeExpiryHours float64 `mapstructure:"refresh_before_expiry_hours"`
	// 最大重试次数
	MaxRetries int `mapstructure:"max_retries"`
	// 重试退避基础时间（秒）
	RetryBackoffSeconds int `mapstructure:"retry_backoff_seconds"`
	// 每次从数据库读取的候选账号上限
	CandidatePageSize int `mapstructure:"candidate_page_size"`
	// 每个平台允许的并发刷新数
	ProviderConcurrency int `mapstructure:"provider_concurrency"`
	// 每个平台、每个进程允许的刷新请求速率
	ProviderQPS int `mapstructure:"provider_qps"`
	// 一个周期内连续临时失败达到此值后停止该平台
	ProviderFailureThreshold int `mapstructure:"provider_failure_threshold"`
	// 单次上游刷新尝试的超时（秒）
	AttemptTimeoutSeconds int `mapstructure:"attempt_timeout_seconds"`
	// 单个后台刷新周期的总超时（秒）
	CycleTimeoutSeconds int `mapstructure:"cycle_timeout_seconds"`
}

// DefaultPricingFallbackFile 是模型定价回退文件的默认相对路径。
const DefaultPricingFallbackFile = "./resources/model-pricing/model_prices_and_context_window.json"

type PricingConfig struct {
	// 价格数据远程URL（默认使用LiteLLM镜像）
	RemoteURL string `mapstructure:"remote_url"`
	// 哈希校验文件URL
	HashURL string `mapstructure:"hash_url"`
	// 本地数据目录
	DataDir string `mapstructure:"data_dir"`
	// 回退文件路径
	FallbackFile string `mapstructure:"fallback_file"`
	// 覆盖补丁文件路径（可选）：条目按字段浅合并覆盖目录/回退数据，优先级最高
	OverrideFile string `mapstructure:"override_file"`
	// 更新间隔（小时）
	UpdateIntervalHours int `mapstructure:"update_interval_hours"`
	// 哈希校验间隔（分钟）
	HashCheckIntervalMinutes int `mapstructure:"hash_check_interval_minutes"`
}

type ServerConfig struct {
	// DelegatePayments leaves payment and wallet operations to a separate backend.
	DelegatePayments         bool     `mapstructure:"delegate_payments"`
	Host                     string   `mapstructure:"host"`
	Port                     int      `mapstructure:"port"`
	Mode                     string   `mapstructure:"mode"`                  // debug/release
	EnableServerTiming       bool     `mapstructure:"enable_server_timing"`  // Admin UI Server-Timing response header
	FrontendURL              string   `mapstructure:"frontend_url"`          // 前端基础 URL，用于生成邮件中的外部链接
	ReadHeaderTimeout        int      `mapstructure:"read_header_timeout"`   // 读取请求头超时（秒）
	MaxHeaderBytes           int      `mapstructure:"max_header_bytes"`      // 请求头最大字节数（HTTP/2 映射为 header-list 上限）
	IdleTimeout              int      `mapstructure:"idle_timeout"`          // 空闲连接超时（秒）
	TrustedProxies           []string `mapstructure:"trusted_proxies"`       // 可信代理列表（CIDR/IP）
	TrustedProxiesConfigured bool     `mapstructure:"-" json:"-" yaml:"-"`   // 是否显式配置了可信代理列表
	MaxRequestBodySize       int64    `mapstructure:"max_request_body_size"` // 全局最大请求体限制
	// SettingsInjectionCacheTTLSeconds bounds how long a replica may serve the
	// rendered index.html (which embeds window.__APP_CONFIG__) without re-reading
	// settings. It is the fallback for a lost cross-instance invalidation, so keep
	// it short: two replicas injecting different flags for the same site is a
	// production incident, not a cosmetic delay.
	SettingsInjectionCacheTTLSeconds int       `mapstructure:"settings_injection_cache_ttl_seconds"`
	H2C                              H2CConfig `mapstructure:"h2c"` // HTTP/2 Cleartext 配置
}

// H2CConfig HTTP/2 Cleartext 配置
type H2CConfig struct {
	Enabled                      bool   `mapstructure:"enabled"`                          // 是否启用 H2C
	MaxConcurrentStreams         uint32 `mapstructure:"max_concurrent_streams"`           // 最大并发流数量
	IdleTimeout                  int    `mapstructure:"idle_timeout"`                     // 空闲超时（秒）
	MaxReadFrameSize             int    `mapstructure:"max_read_frame_size"`              // 最大帧大小（字节）
	MaxUploadBufferPerConnection int    `mapstructure:"max_upload_buffer_per_connection"` // 每个连接的上传缓冲区（字节）
	MaxUploadBufferPerStream     int    `mapstructure:"max_upload_buffer_per_stream"`     // 每个流的上传缓冲区（字节）
}

type CORSConfig struct {
	AllowedOrigins   []string `mapstructure:"allowed_origins"`
	AllowCredentials bool     `mapstructure:"allow_credentials"`
}

// WebAuthnConfig configures this deployment as a WebAuthn relying party.
// RPID and RPOrigins are security boundaries and must never be inferred from
// untrusted request Host or Origin headers.
type WebAuthnConfig struct {
	Enabled       bool     `mapstructure:"enabled"`
	RPDisplayName string   `mapstructure:"rp_display_name"`
	RPID          string   `mapstructure:"rp_id"`
	RPOrigins     []string `mapstructure:"rp_origins"`
}

const MaxForwardedClientIPHeaders = 16

type ForwardedClientIPSettings struct {
	TrustForwardedIP bool
	Headers          []string
}

type SecurityConfig struct {
	// AccountPoolOwnerUserID grants one verified admin visibility of the entire pool.
	// Zero disables pool-wide access; this server-only setting is not editable through the UI.
	AccountPoolOwnerUserID int64                `mapstructure:"account_pool_owner_user_id"`
	URLAllowlist           URLAllowlistConfig   `mapstructure:"url_allowlist"`
	ResponseHeaders        ResponseHeaderConfig `mapstructure:"response_headers"`
	CSP                    CSPConfig            `mapstructure:"csp"`
	ProxyFallback          ProxyFallbackConfig  `mapstructure:"proxy_fallback"`
	ProxyProbe             ProxyProbeConfig     `mapstructure:"proxy_probe"`
	// TrustForwardedIPForAPIKeyACL enables the legacy forwarded-header
	// compatibility mode. Headers are only trusted when the TCP peer is in
	// server.trusted_proxies, and X-Forwarded-For is read right-to-left.
	// When disabled, server.trusted_proxies is authoritative for all client-IP
	// consumers.
	TrustForwardedIPForAPIKeyACL bool `mapstructure:"trust_forwarded_ip_for_api_key_acl"`
	// KeyRotationGateEnabled forces a user whose active API keys may all be
	// leaked (created before KeyRotationCutoff and without an IP allowlist) to
	// rotate a key or configure an IP allowlist before redeeming codes or
	// topping up. Defaults to true; set false to roll the gate back.
	KeyRotationGateEnabled bool `mapstructure:"key_rotation_gate_enabled"`
	// KeyRotationCutoff is the RFC3339 instant that separates "possibly leaked"
	// API keys from rotated ones. Defaults to config.DefaultKeyRotationCutoff.
	KeyRotationCutoff string `mapstructure:"key_rotation_cutoff"`
	// SessionBindingMode controls how the session binding fingerprint is
	// computed and whether it is enforced at all. See SessionBindingMode*
	// constants. Defaults to "loose"; set "off" to roll the binding back.
	SessionBindingMode string `mapstructure:"session_binding_mode"`
	// SessionBindingOnMismatch decides what a fingerprint mismatch does: "rebind"
	// (default) lets a refresh through when only the IP changed (browser family and OS
	// family unchanged) and re-binds the freshly issued tokens to the current fingerprint;
	// "revoke" revokes the whole session family and forces a new login (the behaviour
	// before rebind existed). See SessionBindingOnMismatch*.
	SessionBindingOnMismatch string `mapstructure:"session_binding_on_mismatch"`
	// OAuthCodeExchangeEnabled makes the GitHub / Google OAuth callbacks
	// redirect the browser with a single-use, 60-second code instead of
	// putting the access / refresh tokens in the URL fragment. The frontend
	// exchanges the code through POST /api/v1/auth/oauth/exchange. Defaults to
	// true; set false to fall back to the legacy fragment flow for one release.
	OAuthCodeExchangeEnabled bool `mapstructure:"oauth_code_exchange_enabled"`
	// AuthSubjectPlaintextDualWrite keeps writing the legacy plaintext subject
	// columns (auth_identities.provider_subject, auth_identity_channels.channel_subject,
	// pending_auth_sessions.provider_subject) next to the keyed blind index.
	// Default false: new rows only persist provider_subject_hash / prefix, so a
	// database dump cannot be reversed into third-party subjects (email included).
	// Reads keep the hash-first + plaintext fallback path, so rows written before
	// the backfill still resolve. Set true only as a transitional switch while
	// older binaries are still running, or to roll the default back.
	AuthSubjectPlaintextDualWrite bool `mapstructure:"auth_subject_plaintext_dual_write"`
	// HoneypotExemptNets lists CIDRs/IPs belonging to the operator's own edge.
	// They are never banned by the scanner trap. Kept in config so real
	// addresses never land in this public repository.
	HoneypotExemptNets            []string                                   `mapstructure:"honeypot_exempt_nets" json:"-" yaml:"-"`
	ForwardedClientIPHeaders      []string                                   `mapstructure:"forwarded_client_ip_headers" json:"forwarded_client_ip_headers" yaml:"forwarded_client_ip_headers"`
	forwardedClientIPSettingsLive *atomic.Pointer[ForwardedClientIPSettings] `mapstructure:"-" json:"-" yaml:"-"`
}

// NormalizeSessionBindingMode 归一化并校验 security.session_binding_mode。
func NormalizeSessionBindingMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", SessionBindingModeLoose:
		return SessionBindingModeLoose, nil
	case SessionBindingModeOff:
		return SessionBindingModeOff, nil
	case SessionBindingModeStrict:
		return SessionBindingModeStrict, nil
	default:
		return "", fmt.Errorf("security.session_binding_mode must be one of: off, loose, strict")
	}
}

// SessionBindingLoose 报告会话指纹是否按网段归一（loose 模式）。
func (c *Config) SessionBindingLoose() bool {
	return c != nil && strings.EqualFold(strings.TrimSpace(c.Security.SessionBindingMode), SessionBindingModeLoose)
}

// NormalizeSessionBindingOnMismatch 归一化并校验 security.session_binding_on_mismatch；
// 空值取默认的 rebind。
func NormalizeSessionBindingOnMismatch(action string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "", SessionBindingOnMismatchRebind:
		return SessionBindingOnMismatchRebind, nil
	case SessionBindingOnMismatchRevoke:
		return SessionBindingOnMismatchRevoke, nil
	default:
		return "", fmt.Errorf("security.session_binding_on_mismatch must be one of: rebind, revoke")
	}
}

// SessionBindingRevokeOnMismatch 报告指纹不一致时是否撤销整个会话家族（仅显式配置 revoke 时为 true；
// 空值与未知值都按默认的 rebind 处理，手写 Config 的测试与旧部署因此不会被意外踢下线）。
func (c *Config) SessionBindingRevokeOnMismatch() bool {
	return c != nil && strings.EqualFold(strings.TrimSpace(c.Security.SessionBindingOnMismatch), SessionBindingOnMismatchRevoke)
}

// SessionBindingDisabled 报告会话绑定是否被显式关闭。
func (c *Config) SessionBindingDisabled() bool {
	return c != nil && strings.EqualFold(strings.TrimSpace(c.Security.SessionBindingMode), SessionBindingModeOff)
}

func NormalizeForwardedClientIPHeaders(headers []string) ([]string, error) {
	normalized := make([]string, 0, len(headers))
	seen := make(map[string]struct{}, len(headers))
	for _, header := range headers {
		header = strings.TrimSpace(header)
		if !httpguts.ValidHeaderFieldName(header) {
			return nil, fmt.Errorf("invalid HTTP header field name %q", header)
		}
		canonical := textproto.CanonicalMIMEHeaderKey(header)
		key := strings.ToLower(canonical)
		if _, exists := seen[key]; exists {
			continue
		}
		if len(normalized) == MaxForwardedClientIPHeaders {
			return nil, fmt.Errorf("forwarded client IP headers must contain at most %d unique names", MaxForwardedClientIPHeaders)
		}
		seen[key] = struct{}{}
		normalized = append(normalized, canonical)
	}
	return normalized, nil
}

func cloneForwardedClientIPHeaders(headers []string) []string {
	if len(headers) == 0 {
		return []string{}
	}
	return append([]string(nil), headers...)
}

func (c *Config) ForwardedClientIPSettings() ForwardedClientIPSettings {
	if c == nil {
		return ForwardedClientIPSettings{Headers: []string{}}
	}
	live := c.Security.forwardedClientIPSettingsLive
	if live != nil {
		if snapshot := live.Load(); snapshot != nil {
			return ForwardedClientIPSettings{
				TrustForwardedIP: snapshot.TrustForwardedIP,
				Headers:          cloneForwardedClientIPHeaders(snapshot.Headers),
			}
		}
	}
	return ForwardedClientIPSettings{
		TrustForwardedIP: c.Security.TrustForwardedIPForAPIKeyACL,
		Headers:          cloneForwardedClientIPHeaders(c.Security.ForwardedClientIPHeaders),
	}
}

func (c *Config) TrustForwardedIPForAPIKeyACL() bool {
	return c.ForwardedClientIPSettings().TrustForwardedIP
}

// ForwardedClientIPTrustEnabled reports whether the legacy forwarded-header
// compatibility mode currently overrides server.trusted_proxies.
func (c *Config) ForwardedClientIPTrustEnabled() bool {
	return c != nil && c.TrustForwardedIPForAPIKeyACL()
}

// KeyRotationGateSettings returns the effective key-rotation gate switch and
// cutoff. An empty or unparsable cutoff falls back to
// DefaultKeyRotationCutoff so a typo cannot silently switch the gate off for
// every API key. Validate() rejects an unparsable value at startup; the
// fallback only covers programmatically constructed configs (tests, embedders).
func (c *Config) KeyRotationGateSettings() (enabled bool, cutoff time.Time) {
	if c == nil {
		return true, DefaultKeyRotationCutoffTime()
	}
	enabled = c.Security.KeyRotationGateEnabled
	if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(c.Security.KeyRotationCutoff)); err == nil {
		return enabled, parsed
	}
	return enabled, DefaultKeyRotationCutoffTime()
}

// DefaultKeyRotationCutoffTime returns DefaultKeyRotationCutoff as a time value.
func DefaultKeyRotationCutoffTime() time.Time {
	parsed, err := time.Parse(time.RFC3339, DefaultKeyRotationCutoff)
	if err != nil {
		// The constant is compile-time data; a failure here is a programming error.
		panic(fmt.Sprintf("invalid DefaultKeyRotationCutoff %q: %v", DefaultKeyRotationCutoff, err))
	}
	return parsed
}

func (c *Config) SetForwardedClientIPSettings(enabled bool, headers []string) {
	if c == nil {
		return
	}
	headers = cloneForwardedClientIPHeaders(headers)
	if c.Security.forwardedClientIPSettingsLive == nil {
		c.Security.forwardedClientIPSettingsLive = &atomic.Pointer[ForwardedClientIPSettings]{}
	}
	c.Security.forwardedClientIPSettingsLive.Store(&ForwardedClientIPSettings{
		TrustForwardedIP: enabled,
		Headers:          headers,
	})
}

func (c *Config) SetTrustForwardedIPForAPIKeyACL(enabled bool) {
	if c == nil {
		return
	}
	c.SetForwardedClientIPSettings(enabled, c.ForwardedClientIPSettings().Headers)
}

type URLAllowlistConfig struct {
	Enabled           bool     `mapstructure:"enabled"`
	UpstreamHosts     []string `mapstructure:"upstream_hosts"`
	PricingHosts      []string `mapstructure:"pricing_hosts"`
	CRSHosts          []string `mapstructure:"crs_hosts"`
	AllowPrivateHosts bool     `mapstructure:"allow_private_hosts"`
	// 关闭 URL 白名单校验时，是否允许 http URL（默认只允许 https）
	AllowInsecureHTTP bool `mapstructure:"allow_insecure_http"`
	// UpstreamChannelAllowPrivateHosts 显式允许管理员把「上游渠道站点地址」
	// 与验证码节点填成私网/环回地址（默认 false）。
	// 上游渠道会被服务端主动请求，允许内网地址等于开了一个内网探测/请求原语；
	// 只有确实在内网部署中转站点时才应打开。
	UpstreamChannelAllowPrivateHosts bool `mapstructure:"upstream_channel_allow_private_hosts"`
}

type ResponseHeaderConfig struct {
	Enabled           bool     `mapstructure:"enabled"`
	AdditionalAllowed []string `mapstructure:"additional_allowed"`
	ForceRemove       []string `mapstructure:"force_remove"`
}

type CSPConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Policy  string `mapstructure:"policy"`
}

type ProxyFallbackConfig struct {
	// AllowDirectOnError 当辅助服务的代理初始化失败时是否允许回退直连。
	// 仅影响以下非 AI 账号连接的辅助服务：
	//   - GitHub Release 更新检查
	//   - 定价数据拉取
	// 不影响 AI 账号网关连接（Claude/OpenAI/Gemini/Antigravity），
	// 这些关键路径的代理失败始终返回错误，不会回退直连。
	// 默认 false：避免因代理配置错误导致服务器真实 IP 泄露。
	AllowDirectOnError bool `mapstructure:"allow_direct_on_error"`
}

type ProxyProbeConfig struct {
	InsecureSkipVerify bool `mapstructure:"insecure_skip_verify"` // 已禁用：禁止跳过 TLS 证书验证
	// URLs 按优先级排列的自定义探测 URL 列表。
	// 留空时使用内置默认列表（ip-api → ipify）。
	// 某些 AI API 专用代理只允许访问特定域名，配置多个备选可提高探测成功率。
	URLs []ProbeURLConfig `mapstructure:"urls"`
}

// ProbeURLConfig 描述一个探测端点及其响应解析方式。
type ProbeURLConfig struct {
	URL    string `mapstructure:"url"`
	Parser string `mapstructure:"parser"` // "ip-api" / "ipify" / "chatgpt-trace"
}

func normalizeProxyProbeURLs(targets []ProbeURLConfig) ([]ProbeURLConfig, error) {
	if len(targets) == 0 {
		return nil, nil
	}

	normalized := make([]ProbeURLConfig, 0, len(targets))
	for i, target := range targets {
		rawURL := strings.TrimSpace(target.URL)
		parser := strings.ToLower(strings.TrimSpace(target.Parser))
		if rawURL == "" {
			return nil, fmt.Errorf("entry %d: url is required", i)
		}
		if parser == "" {
			return nil, fmt.Errorf("entry %d: parser is required", i)
		}
		switch parser {
		case "ip-api", "ipify", "chatgpt-trace":
		default:
			return nil, fmt.Errorf("entry %d: unsupported parser %q", i, target.Parser)
		}

		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Host == "" {
			return nil, fmt.Errorf("entry %d: invalid url %q", i, target.URL)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, fmt.Errorf("entry %d: url scheme must be http or https", i)
		}

		normalized = append(normalized, ProbeURLConfig{
			URL:    rawURL,
			Parser: parser,
		})
	}
	return normalized, nil
}

type BillingConfig struct {
	CircuitBreaker CircuitBreakerConfig `mapstructure:"circuit_breaker"`
	// BalancePreauthorizationEnabled controls request-time balance holds. When
	// disabled, completed usage is still charged by the normal settlement path.
	BalancePreauthorizationEnabled bool `mapstructure:"balance_preauthorization_enabled"`
	// BalanceMergeSettlementEnabled routes completed balance charges through the
	// durable billing_balance_settlements queue instead of issuing one
	// UPDATE users SET balance per request. The queue worker coalesces all pending
	// charges of the same user into a single balance update, which removes the
	// users.balance row-lock queue that serialises a single user's concurrent
	// requests. Off (default) keeps the legacy per-request synchronous UPDATE.
	//
	// Amounts, the final balance and failure behaviour are identical in both
	// modes; only the moment at which users.balance becomes visible moves from
	// "inside the request's billing transaction" to "the next settlement flush"
	// (bounded by the worker poll interval). Roll back by setting the env to
	// false and restarting: the worker keeps running, so queued charges still
	// drain.
	BalanceMergeSettlementEnabled bool `mapstructure:"balance_merge_settlement_enabled"`
	// BalanceMergeSettlementMaxBacklog caps the number of charges waiting in the
	// merge-settlement queue. Only meaningful while BalanceMergeSettlementEnabled
	// is true: once the pending row count exceeds it, new requests on this process
	// are charged synchronously again (the exact code path used when the switch is
	// off) until the backlog drops below half of it. Rows already queued keep
	// being settled by the worker, none is lost or charged twice. 0 disables the
	// row-count trigger.
	BalanceMergeSettlementMaxBacklog int `mapstructure:"balance_merge_settlement_max_backlog"`
	// BalanceMergeSettlementMaxAgeSeconds is the age trigger of the same guard:
	// when the oldest pending charge has waited longer than this, new requests are
	// charged synchronously until it drops below half of it. 0 disables the age
	// trigger.
	BalanceMergeSettlementMaxAgeSeconds int `mapstructure:"balance_merge_settlement_max_age_seconds"`
	// MinimumBalanceReserve is the conservative preflight floor for balance billing.
	// Requests in balance mode are rejected when the cached balance is below this
	// amount, even if it is still positive. Set to 0 to keep the legacy balance > 0 gate.
	MinimumBalanceReserve float64 `mapstructure:"minimum_balance_reserve"`
	// OpenAIIncompleteInputCacheReadPercent is the share (0-100) of an estimated
	// input that is priced as a cache read when an OpenAI-family stream ends
	// without a usage report (client disconnect, upstream EOF, mid-stream break).
	// The default is 0: an estimate never invents a cache discount. That matches
	// the upstream new-api abnormal-end fallback, which bills the whole prompt
	// (including cache hits) at the plain input price, so the operator no longer
	// absorbs the difference (issue #456). Set 95 to restore the pre-#456
	// heuristic. Reported usage is never re-split; this only affects estimates.
	OpenAIIncompleteInputCacheReadPercent int `mapstructure:"openai_incomplete_input_cache_read_percent"`
	// AnthropicIncompleteInputCacheReadPercent is the share (0-100) of an estimated
	// input priced as cache read when an Anthropic-family request ends without a usage
	// report (client disconnect or mid-stream break). Default is 0 (plain input price)
	// so estimates do not invent a ~7-10x cache discount before message_start arrives.
	AnthropicIncompleteInputCacheReadPercent int `mapstructure:"anthropic_incomplete_input_cache_read_percent"`
	// GeminiIncompleteInputCacheReadPercent is the share (0-100) of an estimated
	// input priced as cache read when a Gemini/Antigravity request ends without a usage
	// report. Default is 0 (plain input price).
	GeminiIncompleteInputCacheReadPercent int `mapstructure:"gemini_incomplete_input_cache_read_percent"`
	// UserPlatformQuotaCacheTTLSeconds 用户 × 平台 quota 缓存 TTL（秒），默认 86400=1天，覆盖典型 daily 窗口。
	// 消费点：
	//   - billing_cache_service.cacheWriteWorker 异步累加
	//   - billing_cache_service.checkUserPlatformQuotaEligibility 首次缓存装载
	// 读写两端必须共用同一 TTL，避免缓存生命周期不一致导致 quota 计数漂移。
	UserPlatformQuotaCacheTTLSeconds int `mapstructure:"user_platform_quota_cache_ttl_seconds"`
	// UserPlatformQuotaSentinelTTLSeconds sentinel(无 limit 占位)entry 的 TTL,
	// 显著短于 quota cache 默认 86400s 以控 Redis 内存;默认 3600=1h。
	UserPlatformQuotaSentinelTTLSeconds int `mapstructure:"user_platform_quota_sentinel_ttl_seconds"`
	// InflightReservation 余额模式在途请求预留（Redis），防止并发请求在预检时看到同一份余额而集体透支。
	InflightReservation InflightReservationConfig `mapstructure:"inflight_reservation"`
}

// InflightReservationConfig 余额模式在途预留配置。
// 准入时按 输入估算 + 输出单价 × max_tokens 估算单请求费用，在 Redis 中原子地
// 校验 缓存余额 - 在途预留合计 >= 估算 后登记预留，请求结束（任意路径）释放。
// 估算失败或 Redis 不可用时 fail-open，退回旧的仅余额 > 阈值检查。
type InflightReservationConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// TTLSeconds 单条预留的最长存活时间；进程崩溃等泄漏的预留到期自动失效。
	TTLSeconds int `mapstructure:"ttl_seconds"`
	// DefaultMaxTokens 请求未携带 max_tokens 时用于估算的输出 token 数。
	DefaultMaxTokens int `mapstructure:"default_max_tokens"`
	// MaxOutputTokens 估算输出 token 的上限（max_tokens 超出时截断）。
	MaxOutputTokens int `mapstructure:"max_output_tokens"`
	// MaxInputTokens 输入 token 估算（请求体字节数 / 4）的上限。
	MaxInputTokens int `mapstructure:"max_input_tokens"`
	// MinInputTokens 输入 token 估算的**下限**。
	//
	// 上游（Codex / Responses 类渠道）会在客户端请求体之外注入大量上下文，
	// 实测一个 99B 的最小请求最终 prompt_tokens≈4387（其中约 3.2k 命中缓存）。
	// 只按 bodyBytes/4 估算会把预留算成实际的 1/50，并发时准入形同虚设。
	// 因此输入估算至少取这个值：宁可多留一点，也不能让并发集体透支。
	// 0 表示不设下限（保持旧行为，仅用于回滚）。
	MinInputTokens int `mapstructure:"min_input_tokens"`
	// MaxReservationUSD 单请求预留金额上限；0 表示不设上限。
	MaxReservationUSD float64 `mapstructure:"max_reservation_usd"`
	// FailClosedOnUnpriced 无法为请求估算费用（模型/分组/渠道均无定价）时是否拒绝请求。
	// 默认 false：放行且不预留（fail-open，节流告警日志）。
	FailClosedOnUnpriced bool `mapstructure:"fail_closed_on_unpriced"`
	// FailClosedOnReserveError 预留过程本身出错（余额读取失败 / Redis 不可用）时
	// 是否拒绝请求。默认 false 保持旧行为；生产建议 true——否则上述故障期间
	// 准入闸门整体失效，小额余额并发照样透支。
	FailClosedOnReserveError bool `mapstructure:"fail_closed_on_reserve_error"`
}

type CircuitBreakerConfig struct {
	Enabled             bool `mapstructure:"enabled"`
	FailureThreshold    int  `mapstructure:"failure_threshold"`
	ResetTimeoutSeconds int  `mapstructure:"reset_timeout_seconds"`
	HalfOpenRequests    int  `mapstructure:"half_open_requests"`
}

type ConcurrencyConfig struct {
	// PingInterval: 并发等待期间的 SSE ping 间隔（秒）
	PingInterval int `mapstructure:"ping_interval"`
}

type ImageConcurrencyConfig struct {
	// Enabled: 是否启用图片生成独立并发限制，默认关闭以保持现有行为
	Enabled bool `mapstructure:"enabled"`
	// MaxConcurrentRequests: 当前进程允许同时处理的图片生成请求数，0表示不限制
	MaxConcurrentRequests int `mapstructure:"max_concurrent_requests"`
	// OverflowMode: 图片并发达到上限后的处理方式：reject/wait
	OverflowMode string `mapstructure:"overflow_mode"`
	// WaitTimeoutSeconds: overflow_mode=wait 时等待图片并发槽位的超时时间（秒）
	WaitTimeoutSeconds int `mapstructure:"wait_timeout_seconds"`
	// MaxWaitingRequests: overflow_mode=wait 时当前进程允许排队等待的图片请求数
	MaxWaitingRequests int `mapstructure:"max_waiting_requests"`
}

const (
	ImageConcurrencyOverflowModeReject = "reject"
	ImageConcurrencyOverflowModeWait   = "wait"
)

// GatewayConfig API网关相关配置
type GatewayConfig struct {
	APIKeyQueue                      APIKeyQueueConfig `mapstructure:"api_key_queue"`
	GeminiCachedContentMaxTTLSeconds int               `mapstructure:"gemini_cached_content_max_ttl_seconds"`
	// 等待上游响应头的超时时间（秒），0表示无超时
	// 注意：这不影响流式数据传输，只控制等待响应头的时间
	ResponseHeaderTimeout int `mapstructure:"response_header_timeout"`
	// OpenAIResponseHeaderTimeout: OpenAI/Codex 上游等待响应头的超时时间（秒），0表示无超时
	// OpenAI/Codex 请求可能在上游排队较久；默认不使用通用响应头超时截断。
	OpenAIResponseHeaderTimeout         int  `mapstructure:"openai_response_header_timeout"`
	DisableOpenAITransportErrorCooldown bool `mapstructure:"disable_openai_transport_error_cooldown"`
	// GrokResponseHeaderTimeout bounds the pre-first-byte wait for xAI/Grok.
	// A zero value uses the provider-safe default instead of the generic gateway timeout.
	GrokResponseHeaderTimeout int `mapstructure:"grok_response_header_timeout"`
	// OpenAIFirstOutputTimeoutSeconds: native HTTP Responses 首个语义输出超时（秒），0表示禁用。
	OpenAIFirstOutputTimeoutSeconds int `mapstructure:"openai_first_output_timeout_seconds"`
	// OpenAIHighEffortFirstOutputTimeoutSeconds: high/xhigh/max 推理的首个语义输出超时（秒）。
	// 0 表示回退到 OpenAIFirstOutputTimeoutSeconds。
	OpenAIHighEffortFirstOutputTimeoutSeconds int `mapstructure:"openai_high_effort_first_output_timeout_seconds"`
	// MinOutputTokens 是协议转换时 max_output_tokens 的下限。太小的值会让上游
	// 直接 400，所以转换层不会发出比这个更小的 cap。默认 128，最低 16。
	MinOutputTokens int `mapstructure:"min_output_tokens"`
	// 请求体最大字节数，用于网关请求体大小限制
	MaxBodySize int64 `mapstructure:"max_body_size"`
	// MaxSameAccountRetriesGemini 是 Gemini 三条转发路径对可重试上游错误
	// （429/5xx 等）在同一账号上的重试次数，不含首次请求。默认 4（即每号最多 5 次
	// 请求），范围 0-10；调小可以让调度更快换号，代价是单号容忍瞬时报错的能力下降。
	MaxSameAccountRetriesGemini int `mapstructure:"max_same_account_retries_gemini"`
	// TextMaxBodySize limits endpoints that cannot carry inline image/video payloads.
	TextMaxBodySize int64 `mapstructure:"text_max_body_size"`
	// 非流式上游响应体读取上限（字节），用于防止无界读取导致内存放大
	UpstreamResponseReadMaxBytes int64 `mapstructure:"upstream_response_read_max_bytes"`
	// 上游模型列表响应体读取上限（字节）
	ModelsListReadMaxBytes int64 `mapstructure:"models_list_read_max_bytes"`
	// 代理探测响应体读取上限（字节）
	ProxyProbeResponseReadMaxBytes int64 `mapstructure:"proxy_probe_response_read_max_bytes"`
	// Gemini 上游响应头调试日志开关（默认关闭，避免高频日志开销）
	GeminiDebugResponseHeaders bool `mapstructure:"gemini_debug_response_headers"`
	// DeepSeekResponseCompatEnabled 控制我们二开时加的 DeepSeek 响应改写链路
	// （DSML 内联工具调用解析、reasoning 重组、call_id 重建、工具注入与代执行）。
	//
	// 官方 DeepSeek 现在原生返回结构化 Responses（reasoning_text /
	// encrypted_content / 正确的 function_call id+call_id 分离）。这套改写会把
	// 原本正确的内容再加工一遍，实测导致 reasoning output item 丢失、chat 的
	// message.role 变成空串、Responses 顶层字段被剥掉。
	//
	// 默认 false：不改写响应，直接透传上游。只有接入不支持原生工具调用的老式
	// DeepSeek 兼容中转时才需要打开。
	DeepSeekResponseCompatEnabled bool `mapstructure:"deepseek_response_compat_enabled"`
	// ThinkingBudgetRectifyEnabled 控制「上游报 budget_tokens 约束错误后，自动把
	// thinking.type 改成 enabled、budget_tokens 改成 32000、max_tokens 改成 64000
	// 再重发一次」这条兼容链路。
	//
	// 这是替客户端改写请求参数（会显著放大客户端原本的预算），与官方语义不一致，
	// 因此默认关闭。只有接入了确实要求固定 budget 区间的上游时才需要打开。
	ThinkingBudgetRectifyEnabled bool `mapstructure:"thinking_budget_rectify_enabled"`
	// ConnectionPoolIsolation: 上游连接池隔离策略（proxy/account/account_proxy）
	ConnectionPoolIsolation string `mapstructure:"connection_pool_isolation"`
	// ForceCodexCLI: 强制将 OpenAI `/v1/responses` 请求按 Codex CLI 处理。
	// 用于网关未透传/改写 User-Agent 时的兼容兜底（默认关闭，避免影响其他客户端）。
	ForceCodexCLI bool `mapstructure:"force_codex_cli"`
	// CodexQuotaOverdraftEnabled enables the guarded Codex 5h/7d quota-overdraft
	// detector and scheduler integration. The detector defaults on for
	// compatibility with the production rollout. It is also the deployment
	// emergency upper bound: false always fail-closes the runtime, while the
	// value remains the legacy fallback when an older database has no admin key.
	CodexQuotaOverdraftEnabled bool `mapstructure:"codex_quota_overdraft_enabled"`
	// CodexQuotaOverdraftBusinessInjectionEnabled permits adding the synthetic
	// no-op tool pair to real user requests. It is separate and opt-in because
	// upstream token accounting may include that pair; probe-only operation is
	// the safe default. It is used as a legacy fallback only when the DB key is
	// absent.
	CodexQuotaOverdraftBusinessInjectionEnabled bool `mapstructure:"codex_quota_overdraft_business_injection_enabled"`
	// DisableCodexIdentityEnforcement: 关闭「强制统一 Codex 出站身份」。上游 /backend-api/codex
	// 在容量紧张时按客户端身份分优先级降载，被降载的请求会拿到 HTTP 200 + 流内
	// server_is_overloaded，该次请求失败。默认强制统一出口：所有 OAuth 出站的
	// User-Agent / originator / version 都改写为网关规范身份，确保没有请求带着第三方或陈旧身份
	// 出站。置 true 后退回「仅按最终 User-Agent 配对 originator」的收口语义，供上游策略变动时回滚。
	//
	// 取反义命名是为了让零值安全：该开关会发布为进程级快照，未经 viper 加载而手工构造的
	// Config（测试、工具）其零值必须落在「强制统一开启」这一侧，否则会静默丢掉这层保护。
	DisableCodexIdentityEnforcement bool `mapstructure:"disable_codex_identity_enforcement"`
	// DisableCodexOriginatorNormalization: 已废弃，等价于 DisableCodexIdentityEnforcement。
	// 保留以兼容既有配置文件；加载时会折叠进新键，不要在新代码里直接读取。
	DisableCodexOriginatorNormalization bool `mapstructure:"disable_codex_originator_normalization"`
	// CodexImageGenerationBridgeEnabled: 是否为 Codex `/v1/responses` 自动注入 image_generation 工具和桥接指令。
	// 默认关闭，避免纯文本 Codex 请求被意外改写；显式携带 image_generation 工具的请求仍按分组能力转发。
	CodexImageGenerationBridgeEnabled bool `mapstructure:"codex_image_generation_bridge_enabled"`
	// ForcedCodexInstructionsTemplateFile: 服务端强制附加到 Codex 顶层 instructions 的模板文件路径。
	// 模板渲染后会直接覆盖最终 instructions；若需要保留客户端 system 转换结果，请在模板中显式引用 {{ .ExistingInstructions }}。
	ForcedCodexInstructionsTemplateFile string `mapstructure:"forced_codex_instructions_template_file"`
	// ForcedCodexInstructionsTemplate: 启动时从模板文件读取并缓存的模板内容。
	// 该字段不直接参与配置反序列化，仅用于请求热路径避免重复读盘。
	ForcedCodexInstructionsTemplate string `mapstructure:"-"`
	// OpenAIPassthroughAllowTimeoutHeaders: OpenAI 透传模式是否放行客户端超时头
	// 关闭（默认）可避免 x-stainless-timeout 等头导致上游提前断流。
	OpenAIPassthroughAllowTimeoutHeaders bool `mapstructure:"openai_passthrough_allow_timeout_headers"`
	// OpenAICompactModel: /responses/compact 上游使用的模型。
	// compact 端点支持模型滞后于普通 /responses 时，可用该配置降级规避上游错误。
	OpenAICompactModel string `mapstructure:"openai_compact_model"`
	// OpenAICodexTicket: ChatGPT OAuth 账号按 (账号, 模型) 捕获 292 长度
	// x-codex-turn-state，并在住宅 IP 业务请求中注入该头。默认关闭。
	OpenAICodexTicket OpenAICodexTicketConfig `mapstructure:"openai_codex_ticket"`
	// OpenAIWS: OpenAI Responses WebSocket 配置（默认开启，可按需回滚到 HTTP）
	OpenAIWS GatewayOpenAIWSConfig `mapstructure:"openai_ws"`
	// Live: ChatGPT Frameless Live 会话配置。
	Live GatewayLiveConfig `mapstructure:"live"`
	// OpenAIScheduler: OpenAI 高级调度器粘性逃逸配置
	OpenAIScheduler GatewayOpenAISchedulerConfig `mapstructure:"openai_scheduler"`
	// OpenAIHTTP2: OpenAI HTTP 上游协议策略（默认启用 HTTP/2，可按代理能力回退 HTTP/1.1）
	OpenAIHTTP2 GatewayOpenAIHTTP2Config `mapstructure:"openai_http2"`
	// OpenAIProxyStreamCircuit: Responses SSE 代理断流熔断策略。
	OpenAIProxyStreamCircuit GatewayOpenAIProxyStreamCircuitConfig `mapstructure:"openai_proxy_stream_circuit"`
	// ImageConcurrency: 图片生成独立并发限制配置（默认关闭）
	ImageConcurrency ImageConcurrencyConfig `mapstructure:"image_concurrency"`

	// HTTP 上游连接池配置（性能优化：支持高并发场景调优）
	// MaxIdleConns: 所有主机的最大空闲连接总数
	MaxIdleConns int `mapstructure:"max_idle_conns"`
	// MaxIdleConnsPerHost: 每个主机的最大空闲连接数（关键参数，影响连接复用率）
	MaxIdleConnsPerHost int `mapstructure:"max_idle_conns_per_host"`
	// MaxConnsPerHost: 每个主机的最大连接数（包括活跃+空闲），0表示无限制
	MaxConnsPerHost int `mapstructure:"max_conns_per_host"`
	// IdleConnTimeoutSeconds: 空闲连接超时时间（秒）
	IdleConnTimeoutSeconds int `mapstructure:"idle_conn_timeout_seconds"`
	// MaxUpstreamClients: 上游连接池客户端最大缓存数量
	// 当使用连接池隔离策略时，系统会为不同的账户/代理组合创建独立的 HTTP 客户端
	// 此参数限制缓存的客户端数量，超出后会淘汰最久未使用的客户端
	// 建议值：预估的活跃账户数 * 1.2（留有余量）
	MaxUpstreamClients int `mapstructure:"max_upstream_clients"`
	// ClientIdleTTLSeconds: 上游连接池客户端空闲回收阈值（秒）
	// 超过此时间未使用的客户端会被标记为可回收
	// 建议值：根据用户访问频率设置，一般 10-30 分钟
	ClientIdleTTLSeconds int `mapstructure:"client_idle_ttl_seconds"`
	// ConcurrencySlotTTLMinutes: 并发槽位过期时间（分钟）
	// 应大于最长 LLM 请求时间，防止请求完成前槽位过期
	ConcurrencySlotTTLMinutes int `mapstructure:"concurrency_slot_ttl_minutes"`
	// SessionIdleTimeoutMinutes: 会话空闲超时时间（分钟），默认 5 分钟
	// 用于 Anthropic OAuth/SetupToken 账号的会话数量限制功能
	// 空闲超过此时间的会话将被自动释放
	SessionIdleTimeoutMinutes int `mapstructure:"session_idle_timeout_minutes"`

	// StreamDataIntervalTimeout: 流数据间隔超时（秒），0表示禁用
	StreamDataIntervalTimeout int `mapstructure:"stream_data_interval_timeout"`
	// StreamKeepaliveInterval: 流式 keepalive 间隔（秒），0表示禁用
	StreamKeepaliveInterval int `mapstructure:"stream_keepalive_interval"`
	// ImageStreamDataIntervalTimeout: 图片流数据间隔超时（秒），0表示禁用
	ImageStreamDataIntervalTimeout int `mapstructure:"image_stream_data_interval_timeout"`
	// ImageStreamKeepaliveInterval: 图片流式 keepalive 间隔（秒），0表示禁用
	ImageStreamKeepaliveInterval int `mapstructure:"image_stream_keepalive_interval"`
	// ImageNonstreamKeepaliveInterval: 图片非流式 JSON keepalive 间隔（秒），0表示禁用
	ImageNonstreamKeepaliveInterval int `mapstructure:"image_nonstream_keepalive_interval"`
	// MaxLineSize: 上游 SSE 单行最大字节数（0使用默认值）
	MaxLineSize int `mapstructure:"max_line_size"`

	// 是否记录上游错误响应体摘要（避免输出请求内容）
	LogUpstreamErrorBody bool `mapstructure:"log_upstream_error_body"`
	// 上游错误响应体记录最大字节数（超过会截断）
	LogUpstreamErrorBodyMaxBytes int `mapstructure:"log_upstream_error_body_max_bytes"`

	// API-key 账号在客户端未提供 anthropic-beta 时，是否按需自动补齐（默认关闭以保持兼容）
	InjectBetaForAPIKey bool `mapstructure:"inject_beta_for_apikey"`

	// 是否允许对部分 400 错误触发 failover（默认关闭以避免改变语义）
	FailoverOn400 bool `mapstructure:"failover_on_400"`

	// 账户切换最大次数（遇到上游错误时切换到其他账户的次数上限）
	MaxAccountSwitches int `mapstructure:"max_account_switches"`
	// Gemini 账户切换最大次数（Gemini 平台单独配置，因 API 限制更严格）
	MaxAccountSwitchesGemini int `mapstructure:"max_account_switches_gemini"`

	// AccountCircuitBreaker: 账号错误率断路器。每个账号维护一个进程内滑动窗口，
	// 窗口内上游尝试的失败率过高时把该账号临时停止调度一小段时间，到期自动恢复。
	AccountCircuitBreaker AccountCircuitBreakerConfig `mapstructure:"account_circuit_breaker"`

	// RetryGuard: 单个客户端请求的重试/故障转移硬上限（总尝试次数、总耗时、同账号退避）
	// 以及网关自身 503/429 的 Retry-After 区间。
	RetryGuard GatewayRetryGuardConfig `mapstructure:"retry_guard"`

	// Antigravity 429 fallback 限流时间（分钟），解析重置时间失败时使用
	AntigravityFallbackCooldownMinutes int `mapstructure:"antigravity_fallback_cooldown_minutes"`

	// Scheduling: 账号调度相关配置
	Scheduling GatewaySchedulingConfig `mapstructure:"scheduling"`

	// TLSFingerprint: TLS指纹伪装配置
	TLSFingerprint TLSFingerprintConfig `mapstructure:"tls_fingerprint"`

	// UsageRecord: 使用量记录异步队列配置（有界队列 + 固定 worker）
	UsageRecord GatewayUsageRecordConfig `mapstructure:"usage_record"`

	// UserGroupRateCacheTTLSeconds: 用户分组倍率热路径缓存 TTL（秒）
	UserGroupRateCacheTTLSeconds int `mapstructure:"user_group_rate_cache_ttl_seconds"`
	// ModelsListCacheTTLSeconds: /v1/models 模型列表短缓存 TTL（秒）
	ModelsListCacheTTLSeconds int `mapstructure:"models_list_cache_ttl_seconds"`

	// UserMessageQueue: 用户消息串行队列配置
	// 对 role:"user" 的真实用户消息实施账号级串行化 + RPM 自适应延迟
	UserMessageQueue UserMessageQueueConfig `mapstructure:"user_message_queue"`

	// Grok: Grok/xAI gateway scheduling and free-tier soft-gate settings.
	Grok GatewayGrokConfig `mapstructure:"grok"`

	// CNProviders: 国产 OpenAI 兼容供应商（kimi/zhipu/deepseek）的余额检测配置。
	// 仅作用于 payg（按量付费）账号：周期探测余额，低于阈值则临时停调。
	CNProviders GatewayCNProvidersConfig `mapstructure:"cn_providers"`
}

// AccountCircuitBreakerConfig 控制账号错误率断路器（gateway.account_circuit_breaker.*）。
//
// 口径：窗口内「对该账号发起的上游尝试」数 >= MinRequests，且失败数/尝试数 严格大于 ErrorRate
// 时熔断：账号被写成临时不可调度 CooldownSeconds 秒，到期自动恢复。每个进程只看自己的流量。
// 失败 = 连接/传输错误、上游响应超时、上游 5xx、429、401/402/403/407；客户端断开、
// 本地 ctx 取消/超时、Grok 内容安全 403、Antigravity 容量不足 503 不计入失败。
// 同一个客户端请求在同一账号上无论重试几次只算一个样本（最后一次的结果）。
// 环境变量形如 GATEWAY_ACCOUNT_CIRCUIT_BREAKER_ENABLED；enabled 只接受 true/false
// （viper 不认 yes/no/on/off，写了会让配置加载失败）。
type AccountCircuitBreakerConfig struct {
	// Enabled 总开关；false 时不记录、不熔断。
	Enabled bool `mapstructure:"enabled"`
	// WindowSeconds 滑动窗口长度（秒），内部切成 10 个桶。
	WindowSeconds int `mapstructure:"window_seconds"`
	// ErrorRate 触发熔断的失败率阈值，取值 (0,1)，严格大于才触发（恰好等于不触发）。
	// 写成 (1,100) 之间的数会被按百分数解释（95 -> 0.95）并在启动日志里告警。
	ErrorRate float64 `mapstructure:"error_rate"`
	// MinRequests 窗口内至少这么多次尝试才允许熔断（样本不足不熔断）；低于 5 的值会被提到 5。
	MinRequests int `mapstructure:"min_requests"`
	// CooldownSeconds 熔断后临时不可调度的秒数。
	CooldownSeconds int `mapstructure:"cooldown_seconds"`
	// BackoffEnabled 打开后，同一账号在窗口内连续再次熔断时冷却时间按 2 倍递增，
	// 上限 MaxCooldownSeconds。默认关闭（每次都是 CooldownSeconds）。
	BackoffEnabled     bool `mapstructure:"backoff_enabled"`
	MaxCooldownSeconds int  `mapstructure:"max_cooldown_seconds"`
	// ValveMinAccounts 是安全阀的参与账号门槛：同一平台参与统计的账号（含本账号、含 1 分钟内已被熔断的）
	// 不少于这个数，并且没有任何一个健康账号（仍在接流量、样本数达标、自己不满足熔断条件、最近 2 个桶里
	// 至少有一次成功）时，视为本机/网络问题而不是上游问题，不再继续熔断并打 ERROR 日志。
	// 只要还有健康账号就照常熔断。
	ValveMinAccounts int `mapstructure:"valve_min_accounts"`
	// FastTripMinRequests 是快速熔断门槛：最近 2 个桶（约 30–60 秒）里样本数不少于该值且一次成功都没有，
	// 就不等失败率在整个窗口里越线直接熔断（长期健康的账号突然全坏时，窗口会被旧的成功样本稀释）。0 = 关闭。
	FastTripMinRequests int `mapstructure:"fast_trip_min_requests"`
}

// GatewayRetryGuardConfig 控制单个客户端请求的重试硬上限（gateway.retry_guard.*）。
type GatewayRetryGuardConfig struct {
	// MaxAttempts 单个请求最多向上游发起的尝试次数（同账号重试与换号都算）。
	// 默认 30：填充调度要求候选账号都试过才把错误还给用户，上限要容得下这个预算。
	// 想要等同于改动之前几乎不设限的行为，设成 1024。
	MaxAttempts int `mapstructure:"max_attempts"`
	// MaxElapsedSeconds 单个请求从「第一次上游失败」起，超过该秒数后不再发起新的尝试（默认 120）。
	// 计时不含排队等并发槽位的时间，也不含第一次尝试本身的耗时（慢请求不会还没重试就被判超时）。
	MaxElapsedSeconds int `mapstructure:"max_elapsed_seconds"`
	// BackoffBaseMs / BackoffMaxMs 同账号重试的带抖动指数退避：base、2*base、4*base…，上限 max。
	BackoffBaseMs int `mapstructure:"backoff_base_ms"`
	BackoffMaxMs  int `mapstructure:"backoff_max_ms"`
	// RetryAfterEnabled 关闭后网关自己生成的 503/429 一律不带 Retry-After（状态码与响应体始终不变）。
	RetryAfterEnabled bool `mapstructure:"retry_after_enabled"`
	// RetryAfterMinSeconds / RetryAfterMaxSeconds 是「没有可用账号」的 503 在拿不到熔断剩余冷却时的
	// Retry-After 取值区间（秒，随机）；能拿到时用「剩余冷却 + 1–5 秒随机」（最多 30 秒）。
	// 用户并发/队列已满的 429 固定 1–3 秒，重试预算用尽后透传上游 5xx 固定 5–10 秒；
	// 额度/余额/Key 限流类 429 不带 Retry-After（等多久都不会好）。
	RetryAfterMinSeconds int `mapstructure:"retry_after_min_seconds"`
	RetryAfterMaxSeconds int `mapstructure:"retry_after_max_seconds"`
}

// APIKeyQueueConfig applies only to keys with a positive concurrency limit.
// Zero max_waiting leaves the previous immediate-rejection behavior unchanged.
type APIKeyQueueConfig struct {
	MaxWaiting     int `mapstructure:"max_waiting"`
	TimeoutSeconds int `mapstructure:"timeout_seconds"`
}

// GatewayGrokConfig holds Grok-specific gateway scheduling knobs.
//
// Free-quota soft gate keys (gateway.grok.*):
//   - free_quota_soft_gate_enabled: enable local rolling-window scheduling guard for
//     OAuth accounts whose subscription_tier/plan_type is explicitly "free".
//     Default true is safe only because free-tier detection is strict (unknown/paid fail open).
//   - free_quota_token_limit: nominal rolling-window token allowance.
//   - free_quota_soft_gate_percent: stop new scheduling before the nominal limit (1-100).
//   - free_quota_window_hours: local usage rolling window length in hours.
//   - free_quota_stats_cache_seconds: cache TTL for free-tier usage stats
//     (hot path never blocks on DB; misses fail open and refresh in background).
type GatewayGrokConfig struct {
	// PasswordAuthEnabled controls the optional password-to-SSO OAuth flow.
	// It defaults to false and must be explicitly enabled by the operator.
	// When true, POST /admin/grok/oauth/password is functional (not ignored).
	PasswordAuthEnabled bool `mapstructure:"password_auth_enabled"`
	// FreeQuotaSoftGateEnabled enables a local rolling-window scheduling guard
	// for explicitly free Grok OAuth accounts only.
	FreeQuotaSoftGateEnabled bool `mapstructure:"free_quota_soft_gate_enabled"`
	// FreeQuotaTokenLimit is the nominal rolling-window allowance.
	FreeQuotaTokenLimit int64 `mapstructure:"free_quota_token_limit"`
	// FreeQuotaSoftGatePercent stops new scheduling before the nominal limit.
	FreeQuotaSoftGatePercent int `mapstructure:"free_quota_soft_gate_percent"`
	// FreeQuotaWindowHours controls the local rolling usage window.
	FreeQuotaWindowHours int `mapstructure:"free_quota_window_hours"`
	// FreeQuotaStatsCacheSeconds is the soft-gate stats cache TTL. Hot path never
	// waits on usage_logs; misses fail open and refresh asynchronously.
	FreeQuotaStatsCacheSeconds int `mapstructure:"free_quota_stats_cache_seconds"`
}

// GatewayCNProvidersConfig 国产 OpenAI 兼容供应商（kimi/zhipu/deepseek）的余额检测配置。
//
// 仅作用于 payg（按量付费）账号（kimi/deepseek 有公开余额端点；zhipu 无，仅靠响应式 429/402）。
//   - balance_check_enabled: 是否启用周期余额检测（默认 true）
//   - balance_threshold: 余额低于此值（账户货币单位，默认 0.5）触发临时停调
//   - balance_check_interval_minutes: 余额检测周期（分钟，默认 10）
type GatewayCNProvidersConfig struct {
	BalanceCheckEnabled         bool    `mapstructure:"balance_check_enabled"`
	BalanceThreshold            float64 `mapstructure:"balance_threshold"`
	BalanceCheckIntervalMinutes int     `mapstructure:"balance_check_interval_minutes"`
}

type GatewayLiveConfig struct {
	// MaxSessionDurationSeconds 是 Live 会话的硬上限。
	MaxSessionDurationSeconds int `mapstructure:"max_session_duration_seconds"`
}

// GatewayOpenAIHTTP2Config OpenAI HTTP 上游协议配置。
// 默认启用 HTTP/2；在部分代理不兼容时按策略回退 HTTP/1.1。
type GatewayOpenAIHTTP2Config struct {
	// Enabled: 是否启用 OpenAI HTTP/2 优先策略
	Enabled bool `mapstructure:"enabled"`
	// AllowProxyFallbackToHTTP1: HTTP/HTTPS 代理出现明确 H2 兼容错误时，临时回退 HTTP/1.1
	AllowProxyFallbackToHTTP1 bool `mapstructure:"allow_proxy_fallback_to_http1"`
	// FallbackErrorThreshold: 回退窗口内累计多少次兼容错误后触发回退
	FallbackErrorThreshold int `mapstructure:"fallback_error_threshold"`
	// FallbackWindowSeconds: 统计兼容错误的时间窗口（秒）
	FallbackWindowSeconds int `mapstructure:"fallback_window_seconds"`
	// FallbackTTLSeconds: 触发后回退 HTTP/1.1 的持续时间（秒）
	FallbackTTLSeconds int `mapstructure:"fallback_ttl_seconds"`
}

// GatewayOpenAIProxyStreamCircuitConfig controls the bounded, in-process
// proxy-ID circuit used for incomplete OpenAI Responses SSE streams.
type GatewayOpenAIProxyStreamCircuitConfig struct {
	// Disabled: 完全关闭代理断流熔断（默认开启）。
	Disabled bool `mapstructure:"disabled"`
	// FailureThreshold: 统计窗口内多少次断流后隔离代理。
	FailureThreshold int `mapstructure:"failure_threshold"`
	// WindowSeconds: 断流统计窗口（秒）。
	WindowSeconds int `mapstructure:"window_seconds"`
	// TTLSeconds: 代理隔离持续时间（秒）。
	TTLSeconds int `mapstructure:"ttl_seconds"`
}

// UserMessageQueueConfig 用户消息串行队列配置
// 用于 Anthropic OAuth/SetupToken 账号的用户消息串行化发送
type UserMessageQueueConfig struct {
	// Mode: 模式选择
	// "serialize" = 账号级串行锁 + RPM 自适应延迟
	// "throttle" = 仅 RPM 自适应前置延迟，不阻塞并发
	// "" = 禁用（默认）
	Mode string `mapstructure:"mode"`
	// Enabled: 已废弃，仅向后兼容（等同于 mode: "serialize"）
	Enabled bool `mapstructure:"enabled"`
	// LockTTLMs: 串行锁 TTL（毫秒），应大于最长请求时间
	LockTTLMs int `mapstructure:"lock_ttl_ms"`
	// WaitTimeoutMs: 等待获取锁的超时时间（毫秒）
	WaitTimeoutMs int `mapstructure:"wait_timeout_ms"`
	// MinDelayMs: RPM 自适应延迟下限（毫秒）
	MinDelayMs int `mapstructure:"min_delay_ms"`
	// MaxDelayMs: RPM 自适应延迟上限（毫秒）
	MaxDelayMs int `mapstructure:"max_delay_ms"`
	// CleanupIntervalSeconds: 孤儿锁清理间隔（秒），0 表示禁用
	CleanupIntervalSeconds int `mapstructure:"cleanup_interval_seconds"`
}

// WaitTimeout 返回等待超时的 time.Duration
func (c *UserMessageQueueConfig) WaitTimeout() time.Duration {
	if c.WaitTimeoutMs <= 0 {
		return 30 * time.Second
	}
	return time.Duration(c.WaitTimeoutMs) * time.Millisecond
}

// GetEffectiveMode 返回生效的模式
// 注意：Mode 字段已在 load() 中做过白名单校验和规范化，此处无需重复验证
func (c *UserMessageQueueConfig) GetEffectiveMode() string {
	if c.Mode == UMQModeSerialize || c.Mode == UMQModeThrottle {
		return c.Mode
	}
	if c.Enabled {
		return UMQModeSerialize // 向后兼容
	}
	return ""
}

// OpenAICodexTicketConfig 控制 ChatGPT OAuth 的 x-codex-turn-state 门票。
// 打票走 harvest_proxy_url（SOCKS），业务出站仍用账号住宅 proxy_id，只替换该请求头。
// 门票默认有效 3600 秒，临近过期前 refresh_before_seconds 重新打票。
type OpenAICodexTicketConfig struct {
	Enabled                      bool     `mapstructure:"enabled"`
	TargetLength                 int      `mapstructure:"target_length"`
	TTLSeconds                   int      `mapstructure:"ttl_seconds"`
	RefreshBeforeSeconds         int      `mapstructure:"refresh_before_seconds"`
	HarvestProxyURL              string   `mapstructure:"harvest_proxy_url"`
	HarvestProbeIntervalSeconds  int      `mapstructure:"harvest_probe_interval_seconds"`
	HarvestAttemptTimeoutSeconds int      `mapstructure:"harvest_attempt_timeout_seconds"`
	FailClosed                   bool     `mapstructure:"fail_closed"`
	Models                       []string `mapstructure:"models"`
}

// DefaultOpenAIWSClientFirstMessageTimeoutSeconds preserves the legacy ingress deadline.
const DefaultOpenAIWSClientFirstMessageTimeoutSeconds = 30

// GatewayOpenAIWSConfig OpenAI Responses WebSocket 配置。
// 注意：默认全局开启；如需回滚可使用 force_http 或关闭 enabled。
type GatewayOpenAIWSConfig struct {
	// ModeRouterV2Enabled: 新版 WS mode 路由开关（默认 false；关闭时保持 legacy 行为）
	ModeRouterV2Enabled bool `mapstructure:"mode_router_v2_enabled"`
	// IngressModeDefault: ingress 默认模式（off/ctx_pool/passthrough/http_bridge）
	IngressModeDefault string `mapstructure:"ingress_mode_default"`
	// ClientFirstMessageTimeoutSeconds bounds the total time to read and decompress
	// the first client response.create message after the WebSocket upgrade.
	ClientFirstMessageTimeoutSeconds int `mapstructure:"client_first_message_timeout_seconds"`
	// IngressInterTurnIdleTimeoutSeconds bounds the time a client may remain idle
	// between completed ingress turns. Zero disables this protection.
	IngressInterTurnIdleTimeoutSeconds int `mapstructure:"ingress_inter_turn_idle_timeout_seconds"`
	// MaxIngressConnectionsPerAPIKey bounds live client WebSocket ingress sessions
	// per API key across all instances. Zero disables this protection.
	MaxIngressConnectionsPerAPIKey int `mapstructure:"max_ingress_connections_per_api_key"`
	// Enabled: 全局总开关（默认 true）
	Enabled bool `mapstructure:"enabled"`
	// OAuthEnabled: 是否允许 OpenAI OAuth 账号使用 WS
	OAuthEnabled bool `mapstructure:"oauth_enabled"`
	// APIKeyEnabled: 是否允许 OpenAI API Key 账号使用 WS
	APIKeyEnabled bool `mapstructure:"apikey_enabled"`
	// ForceHTTP: 全局强制 HTTP（用于紧急回滚）
	ForceHTTP bool `mapstructure:"force_http"`
	// AllowStoreRecovery: 允许在 WSv2 下按策略恢复 store=true（默认 false）
	AllowStoreRecovery bool `mapstructure:"allow_store_recovery"`
	// IngressPreviousResponseRecoveryEnabled: ingress 模式收到 previous_response_not_found 时，是否允许自动去掉 previous_response_id 重试一次（默认 true）
	IngressPreviousResponseRecoveryEnabled bool `mapstructure:"ingress_previous_response_recovery_enabled"`
	// StoreDisabledConnMode: store=false 且无可复用会话连接时的建连策略（strict/adaptive/off）
	// - strict: 强制新建连接（隔离优先）
	// - adaptive: 仅在高风险失败后强制新建连接（性能与隔离折中）
	// - off: 不强制新建连接（复用优先）
	StoreDisabledConnMode string `mapstructure:"store_disabled_conn_mode"`
	// StoreDisabledForceNewConn: store=false 且无可复用粘连连接时是否强制新建连接（默认 true，保障会话隔离）
	// 兼容旧配置；当 StoreDisabledConnMode 为空时才生效。
	StoreDisabledForceNewConn bool `mapstructure:"store_disabled_force_new_conn"`
	// PrewarmGenerateEnabled: 是否启用 WSv2 generate=false 预热（默认 false）
	PrewarmGenerateEnabled bool `mapstructure:"prewarm_generate_enabled"`
	// ClientReadLimitBytes: 入站客户端 WS 单帧读取上限。
	ClientReadLimitBytes int64 `mapstructure:"client_read_limit_bytes"`
	// HTTPBridgeEnabled: 首包过大时，保持客户端 WS，改用 HTTP Responses 上游。
	HTTPBridgeEnabled bool `mapstructure:"http_bridge_enabled"`
	// HTTPBridgeThresholdBytes: 触发 HTTP bridge 的入站 WS payload 阈值。
	HTTPBridgeThresholdBytes int64 `mapstructure:"http_bridge_threshold_bytes"`

	// Feature 开关：v2 优先于 v1
	ResponsesWebsockets   bool `mapstructure:"responses_websockets"`
	ResponsesWebsocketsV2 bool `mapstructure:"responses_websockets_v2"`

	// 连接池参数
	MaxConnsPerAccount int `mapstructure:"max_conns_per_account"`
	MinIdlePerAccount  int `mapstructure:"min_idle_per_account"`
	MaxIdlePerAccount  int `mapstructure:"max_idle_per_account"`
	// DynamicMaxConnsByAccountConcurrencyEnabled: 是否按账号并发动态计算连接池上限
	DynamicMaxConnsByAccountConcurrencyEnabled bool `mapstructure:"dynamic_max_conns_by_account_concurrency_enabled"`
	// OAuthMaxConnsFactor: OAuth 账号连接池系数（effective=ceil(concurrency*factor)）
	OAuthMaxConnsFactor float64 `mapstructure:"oauth_max_conns_factor"`
	// APIKeyMaxConnsFactor: API Key 账号连接池系数（effective=ceil(concurrency*factor)）
	APIKeyMaxConnsFactor  float64 `mapstructure:"apikey_max_conns_factor"`
	DialTimeoutSeconds    int     `mapstructure:"dial_timeout_seconds"`
	ReadTimeoutSeconds    int     `mapstructure:"read_timeout_seconds"`
	WriteTimeoutSeconds   int     `mapstructure:"write_timeout_seconds"`
	PoolTargetUtilization float64 `mapstructure:"pool_target_utilization"`
	QueueLimitPerConn     int     `mapstructure:"queue_limit_per_conn"`
	// EventFlushBatchSize: WS 流式写出批量 flush 阈值（事件条数）
	EventFlushBatchSize int `mapstructure:"event_flush_batch_size"`
	// EventFlushIntervalMS: WS 流式写出最大等待时间（毫秒）；0 表示仅按 batch 触发
	EventFlushIntervalMS int `mapstructure:"event_flush_interval_ms"`
	// PrewarmCooldownMS: 连接池预热触发冷却时间（毫秒）
	PrewarmCooldownMS int `mapstructure:"prewarm_cooldown_ms"`
	// FallbackCooldownSeconds: WS 回退冷却窗口，避免 WS/HTTP 抖动；0 表示关闭冷却
	FallbackCooldownSeconds int `mapstructure:"fallback_cooldown_seconds"`
	// RetryBackoffInitialMS: WS 重试初始退避（毫秒）；<=0 表示关闭退避
	RetryBackoffInitialMS int `mapstructure:"retry_backoff_initial_ms"`
	// RetryBackoffMaxMS: WS 重试最大退避（毫秒）
	RetryBackoffMaxMS int `mapstructure:"retry_backoff_max_ms"`
	// RetryJitterRatio: WS 重试退避抖动比例（0-1）
	RetryJitterRatio float64 `mapstructure:"retry_jitter_ratio"`
	// RetryTotalBudgetMS: WS 单次请求重试总预算（毫秒）；0 表示关闭预算限制
	RetryTotalBudgetMS int `mapstructure:"retry_total_budget_ms"`
	// PayloadLogSampleRate: payload_schema 日志采样率（0-1）
	PayloadLogSampleRate float64 `mapstructure:"payload_log_sample_rate"`

	// 账号调度与粘连参数
	LBTopK int `mapstructure:"lb_top_k"`
	// StickySessionTTLSeconds: session_hash -> account_id 粘连 TTL
	StickySessionTTLSeconds int `mapstructure:"sticky_session_ttl_seconds"`
	// SessionHashReadOldFallback: 会话哈希迁移期是否允许“新 key 未命中时回退读旧 SHA-256 key”
	SessionHashReadOldFallback bool `mapstructure:"session_hash_read_old_fallback"`
	// SessionHashDualWriteOld: 会话哈希迁移期是否双写旧 SHA-256 key（短 TTL）
	SessionHashDualWriteOld bool `mapstructure:"session_hash_dual_write_old"`
	// MetadataBridgeEnabled: RequestMetadata 迁移期是否保留旧 ctxkey.* 兼容桥接
	MetadataBridgeEnabled bool `mapstructure:"metadata_bridge_enabled"`
	// StickyResponseIDTTLSeconds: response_id -> account_id 粘连 TTL
	StickyResponseIDTTLSeconds int `mapstructure:"sticky_response_id_ttl_seconds"`
	// StickyPreviousResponseTTLSeconds: 兼容旧键（当新键未设置时回退）
	StickyPreviousResponseTTLSeconds int `mapstructure:"sticky_previous_response_ttl_seconds"`

	SchedulerScoreWeights GatewayOpenAIWSSchedulerScoreWeights `mapstructure:"scheduler_score_weights"`
}

// GatewayOpenAIWSSchedulerScoreWeights 账号调度打分权重。
type GatewayOpenAIWSSchedulerScoreWeights struct {
	Priority  float64 `mapstructure:"priority"`
	Load      float64 `mapstructure:"load"`
	Queue     float64 `mapstructure:"queue"`
	ErrorRate float64 `mapstructure:"error_rate"`
	TTFT      float64 `mapstructure:"ttft"`
	// Reset 倾向「会话窗口最早重置」的账号（use-it-or-lose-it）。
	// >0 时，剩余重置时间越短的账号得分越高，从而被优先用尽。默认 0（关闭，不改变原有行为）。
	Reset float64 `mapstructure:"reset"`
	// QuotaHeadroom 倾向 7d 剩余额度更健康的账号；默认 0（关闭，不改变原有行为）。
	QuotaHeadroom float64 `mapstructure:"quota_headroom"`
	// UpstreamCost 倾向上游声明倍率更低的账号；默认 0（关闭，不改变原有行为）。
	UpstreamCost float64 `mapstructure:"upstream_cost"`
	// PreviousResponse/SessionSticky 仅在开启 OpenAI 高级调度的粘性加权时生效。
	PreviousResponse float64 `mapstructure:"previous_response"`
	SessionSticky    float64 `mapstructure:"session_sticky"`
}

func (w GatewayOpenAIWSSchedulerScoreWeights) BaseWeightSum() float64 {
	return w.Priority + w.Load + w.Queue + w.ErrorRate + w.TTFT + w.Reset + w.QuotaHeadroom + w.UpstreamCost
}

func (w GatewayOpenAIWSSchedulerScoreWeights) TotalWeightSum() float64 {
	return w.BaseWeightSum() + w.PreviousResponse + w.SessionSticky
}

func (w GatewayOpenAIWSSchedulerScoreWeights) IsValid() bool {
	for _, weight := range []float64{
		w.Priority, w.Load, w.Queue, w.ErrorRate, w.TTFT, w.Reset,
		w.QuotaHeadroom, w.UpstreamCost, w.PreviousResponse, w.SessionSticky,
	} {
		if weight < 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
			return false
		}
	}
	baseSum := w.BaseWeightSum()
	return baseSum > 0 && !math.IsNaN(baseSum) && !math.IsInf(baseSum, 0) &&
		!math.IsNaN(w.TotalWeightSum()) && !math.IsInf(w.TotalWeightSum(), 0)
}

// GatewayOpenAISchedulerConfig OpenAI 高级调度器配置。
type GatewayOpenAISchedulerConfig struct {
	// StickyEscapeEnabled: 是否允许 session_hash sticky 在账号健康度劣化时临时逃逸
	StickyEscapeEnabled bool `mapstructure:"sticky_escape_enabled"`
	// StickyEscapeTTFTMs: TTFT EWMA 超过该阈值时跳过 sticky
	StickyEscapeTTFTMs int `mapstructure:"sticky_escape_ttft_ms"`
	// StickyEscapeErrorRate: 错误率 EWMA 超过该阈值时跳过 sticky
	StickyEscapeErrorRate float64 `mapstructure:"sticky_escape_error_rate"`
}

// GatewayUsageRecordConfig 使用量记录异步队列配置
type GatewayUsageRecordConfig struct {
	// WorkerCount: worker 初始数量（自动扩缩容开启时作为初始并发上限）
	WorkerCount int `mapstructure:"worker_count"`
	// QueueSize: 队列容量（有界）
	QueueSize int `mapstructure:"queue_size"`
	// TaskTimeoutSeconds: 单个使用量记录任务超时（秒）
	TaskTimeoutSeconds int `mapstructure:"task_timeout_seconds"`
	// OverflowPolicy: 队列满时策略（drop/sample/sync）
	OverflowPolicy string `mapstructure:"overflow_policy"`
	// OverflowSamplePercent: sample 策略下，同步回写采样百分比（1-100）
	OverflowSamplePercent int `mapstructure:"overflow_sample_percent"`

	// AutoScaleEnabled: 是否启用 worker 自动扩缩容
	AutoScaleEnabled bool `mapstructure:"auto_scale_enabled"`
	// AutoScaleMinWorkers: 自动扩缩容最小 worker 数
	AutoScaleMinWorkers int `mapstructure:"auto_scale_min_workers"`
	// AutoScaleMaxWorkers: 自动扩缩容最大 worker 数
	AutoScaleMaxWorkers int `mapstructure:"auto_scale_max_workers"`
	// AutoScaleUpQueuePercent: 队列占用率达到该阈值时触发扩容
	AutoScaleUpQueuePercent int `mapstructure:"auto_scale_up_queue_percent"`
	// AutoScaleDownQueuePercent: 队列占用率低于该阈值时触发缩容
	AutoScaleDownQueuePercent int `mapstructure:"auto_scale_down_queue_percent"`
	// AutoScaleUpStep: 每次扩容步长
	AutoScaleUpStep int `mapstructure:"auto_scale_up_step"`
	// AutoScaleDownStep: 每次缩容步长
	AutoScaleDownStep int `mapstructure:"auto_scale_down_step"`
	// AutoScaleCheckIntervalSeconds: 自动扩缩容检测间隔（秒）
	AutoScaleCheckIntervalSeconds int `mapstructure:"auto_scale_check_interval_seconds"`
	// AutoScaleCooldownSeconds: 自动扩缩容冷却时间（秒）
	AutoScaleCooldownSeconds int `mapstructure:"auto_scale_cooldown_seconds"`
}

// TLSFingerprintConfig TLS指纹伪装配置
// 用于模拟 Claude CLI (Node.js) 的 TLS 握手特征，避免被识别为非官方客户端
type TLSFingerprintConfig struct {
	// Enabled: 是否全局启用TLS指纹功能
	Enabled bool `mapstructure:"enabled"`
	// Profiles: 预定义的TLS指纹配置模板
	// key 为模板名称，如 "claude_cli_v2", "chrome_120" 等
	Profiles map[string]TLSProfileConfig `mapstructure:"profiles"`
}

// TLSProfileConfig 单个TLS指纹模板的配置
// 所有列表字段为空时使用内置默认值（Claude CLI 2.x / Node.js 20.x）
// 建议通过 TLS 指纹采集工具 (tests/tls-fingerprint-web) 获取完整配置
type TLSProfileConfig struct {
	// Name: 模板显示名称
	Name string `mapstructure:"name"`
	// EnableGREASE: 是否启用GREASE扩展（Chrome使用，Node.js不使用）
	EnableGREASE bool `mapstructure:"enable_grease"`
	// CipherSuites: TLS加密套件列表
	CipherSuites []uint16 `mapstructure:"cipher_suites"`
	// Curves: 椭圆曲线列表
	Curves []uint16 `mapstructure:"curves"`
	// PointFormats: 点格式列表
	PointFormats []uint16 `mapstructure:"point_formats"`
	// SignatureAlgorithms: 签名算法列表
	SignatureAlgorithms []uint16 `mapstructure:"signature_algorithms"`
	// ALPNProtocols: ALPN协议列表（如 ["h2", "http/1.1"]）
	ALPNProtocols []string `mapstructure:"alpn_protocols"`
	// SupportedVersions: 支持的TLS版本列表（如 [0x0304, 0x0303] 即 TLS1.3, TLS1.2）
	SupportedVersions []uint16 `mapstructure:"supported_versions"`
	// KeyShareGroups: Key Share中发送的曲线组（如 [29] 即 X25519）
	KeyShareGroups []uint16 `mapstructure:"key_share_groups"`
	// PSKModes: PSK密钥交换模式（如 [1] 即 psk_dhe_ke）
	PSKModes []uint16 `mapstructure:"psk_modes"`
	// Extensions: TLS扩展类型ID列表，按发送顺序排列
	// 空则使用内置默认顺序 [0,11,10,35,16,22,23,13,43,45,51]
	// GREASE值(如0x0a0a)会自动插入GREASE扩展
	Extensions []uint16 `mapstructure:"extensions"`
}

// GatewaySchedulingConfig accounts scheduling configuration.
type GatewaySchedulingConfig struct {
	// 粘性会话排队配置
	StickySessionMaxWaiting  int           `mapstructure:"sticky_session_max_waiting"`
	StickySessionWaitTimeout time.Duration `mapstructure:"sticky_session_wait_timeout"`

	// 兜底排队配置
	FallbackWaitTimeout time.Duration `mapstructure:"fallback_wait_timeout"`
	FallbackMaxWaiting  int           `mapstructure:"fallback_max_waiting"`

	// 兜底层账户选择策略: "last_used"(按最后使用时间排序，默认) 或 "random"(随机)
	FallbackSelectionMode string `mapstructure:"fallback_selection_mode"`

	// PreferSoonestReset 开启后，负载感知选择会优先选用「会话窗口最早重置」的账号
	// （use-it-or-lose-it：先用尽即将重置的账号，保留重置时间还很久的账号）。
	// 默认 false，保持原有「优先级 → 负载率 → LRU」行为不变。
	PreferSoonestReset bool `mapstructure:"prefer_soonest_reset"`

	// 负载计算
	LoadBatchEnabled    bool `mapstructure:"load_batch_enabled"`
	LoadBatchCacheTTLMS int  `mapstructure:"load_batch_cache_ttl_ms"`
	// 快照桶读取时的 MGET 分块大小
	SnapshotMGetChunkSize int `mapstructure:"snapshot_mget_chunk_size"`
	// 快照重建时的缓存写入分块大小
	SnapshotWriteChunkSize int `mapstructure:"snapshot_write_chunk_size"`

	// 过期槽位清理周期（0 表示禁用）
	SlotCleanupInterval time.Duration `mapstructure:"slot_cleanup_interval"`

	// 受控回源配置
	DbFallbackEnabled bool `mapstructure:"db_fallback_enabled"`
	// RequestFreshnessEnabled enables the legacy per-request PostgreSQL
	// freshness projection. The normal scheduler path receives durable state
	// through the outbox-backed snapshot; keeping this disabled makes the
	// request hot path database-free. Enable only as an emergency compatibility
	// switch while diagnosing snapshot propagation.
	RequestFreshnessEnabled bool `mapstructure:"request_freshness_enabled"`
	// 受控回源超时（秒），0 表示不额外收紧超时
	DbFallbackTimeoutSeconds int `mapstructure:"db_fallback_timeout_seconds"`
	// 受控回源限流（实例级 QPS），0 表示不限制
	DbFallbackMaxQPS int `mapstructure:"db_fallback_max_qps"`

	// Outbox 轮询与滞后阈值配置
	// Outbox 轮询周期（秒）
	OutboxPollIntervalSeconds int `mapstructure:"outbox_poll_interval_seconds"`
	// Outbox 滞后告警阈值（秒）
	OutboxLagWarnSeconds int `mapstructure:"outbox_lag_warn_seconds"`
	// Outbox 触发强制重建阈值（秒）
	OutboxLagRebuildSeconds int `mapstructure:"outbox_lag_rebuild_seconds"`
	// Outbox 连续滞后触发次数
	OutboxLagRebuildFailures int `mapstructure:"outbox_lag_rebuild_failures"`
	// Outbox 积压触发重建阈值（行数）
	OutboxBacklogRebuildRows int `mapstructure:"outbox_backlog_rebuild_rows"`

	// 全量重建周期配置
	// 全量重建周期（秒），0 表示禁用
	FullRebuildIntervalSeconds int `mapstructure:"full_rebuild_interval_seconds"`
}

func (s *ServerConfig) Address() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// DatabaseConfig 数据库连接配置
// 性能优化：新增连接池参数，避免频繁创建/销毁连接
type DatabaseConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
	DBName   string `mapstructure:"dbname"`
	SSLMode  string `mapstructure:"sslmode"`
	// MigrationMode controls whether startup applies pending migrations or only
	// validates that the database already matches the embedded migration set.
	MigrationMode string `mapstructure:"migration_mode"`
	// 连接池配置（性能优化：可配置化连接池参数）
	// MaxOpenConns: 最大打开连接数，控制数据库连接上限，防止资源耗尽
	MaxOpenConns int `mapstructure:"max_open_conns"`
	// MaxIdleConns: 最大空闲连接数，保持热连接减少建连延迟
	MaxIdleConns int `mapstructure:"max_idle_conns"`
	// ConnMaxLifetimeMinutes: 连接最大存活时间，防止长连接导致的资源泄漏
	ConnMaxLifetimeMinutes int `mapstructure:"conn_max_lifetime_minutes"`
	// ConnMaxIdleTimeMinutes: 空闲连接最大存活时间，及时释放不活跃连接
	ConnMaxIdleTimeMinutes int `mapstructure:"conn_max_idle_time_minutes"`
	// UserPlatformQuotaFlusherEnabled: 是否启用 user×platform 配额写聚合 flusher
	UserPlatformQuotaFlusherEnabled bool `mapstructure:"user_platform_quota_flusher_enabled"`
	// UserPlatformQuotaFlushIntervalMs: flusher 刷写间隔（毫秒）
	UserPlatformQuotaFlushIntervalMs int `mapstructure:"user_platform_quota_flush_interval_ms"`
	// UserPlatformQuotaFlushBatchSize: flusher 单批最大条数
	// 建议 ≤ 6000（单条 UPSERT 原子上限）
	UserPlatformQuotaFlushBatchSize int `mapstructure:"user_platform_quota_flush_batch_size"`
}

func (d *DatabaseConfig) DSN() string {
	// 当密码为空时不包含 password 参数，避免 libpq 解析错误
	if d.Password == "" {
		return fmt.Sprintf(
			"host=%s port=%d user=%s dbname=%s sslmode=%s",
			d.Host, d.Port, d.User, d.DBName, d.SSLMode,
		)
	}
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.Password, d.DBName, d.SSLMode,
	)
}

// DSNWithTimezone returns DSN with timezone setting
func (d *DatabaseConfig) DSNWithTimezone(tz string) string {
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	// 当密码为空时不包含 password 参数，避免 libpq 解析错误
	if d.Password == "" {
		return fmt.Sprintf(
			"host=%s port=%d user=%s dbname=%s sslmode=%s TimeZone=%s",
			d.Host, d.Port, d.User, d.DBName, d.SSLMode, tz,
		)
	}
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s TimeZone=%s",
		d.Host, d.Port, d.User, d.Password, d.DBName, d.SSLMode, tz,
	)
}

// RedisConfig Redis 连接配置
// 性能优化：新增连接池和超时参数，提升高并发场景下的吞吐量
type RedisConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
	// 连接池与超时配置（性能优化：可配置化连接池参数）
	// DialTimeoutSeconds: 建立连接超时，防止慢连接阻塞
	DialTimeoutSeconds int `mapstructure:"dial_timeout_seconds"`
	// ReadTimeoutSeconds: 读取超时，避免慢查询阻塞连接池
	ReadTimeoutSeconds int `mapstructure:"read_timeout_seconds"`
	// WriteTimeoutSeconds: 写入超时，避免慢写入阻塞连接池
	WriteTimeoutSeconds int `mapstructure:"write_timeout_seconds"`
	// PoolSize: 连接池大小，控制最大并发连接数。
	// 这也是背压阀：Redis 变慢时池子到顶后新请求在 PoolTimeoutMS 内取不到连接就快速失败，
	// 而不是继续新建连接把已经很慢的 Redis 拖得更慢。
	PoolSize int `mapstructure:"pool_size"`
	// MinIdleConns: 最小空闲连接数，保持热连接减少冷启动延迟。
	// 池会一直把空闲连接补足到这个数量（连接被回收后立刻补建），哪怕根本没有流量，所以保持小值（建议 <=16）。
	MinIdleConns int `mapstructure:"min_idle_conns"`
	// MaxIdleConns: 空闲连接上限；0 表示不限制。
	// 连接用完归还时若空闲数已达上限，该连接会被直接关闭，所以突发期撑大的连接池会很快缩回来
	// （多个请求同时归还时上限可能被多放过几条，后续请求每取放一次就会再关掉一条超限的）。
	// go-redis 没有后台回收线程，ConnMaxIdleTime 只在取连接时惰性检查，流量大时 FIFO 轮转会让所有连接都保持“刚用过”，
	// 因此 MaxIdleConns 是唯一不依赖流量的收缩手段。小于 MinIdleConns 时按 MinIdleConns 处理。
	MaxIdleConns int `mapstructure:"max_idle_conns"`
	// PoolFIFO: 空闲连接先进先出。默认 LIFO 下压在栈底的连接永远不会被取到，也就永远不会被 ConnMaxIdleTime/ConnMaxLifetime 检查到。
	PoolFIFO bool `mapstructure:"pool_fifo"`
	// PoolTimeoutMS: 连接池耗尽时等待空闲连接的最长时间（毫秒）。
	// go-redis 对池超时默认重试 MaxRetries(3) 次，单条命令最坏等待约 (1+3)×该值。
	PoolTimeoutMS int `mapstructure:"pool_timeout_ms"`
	// ConnMaxIdleTimeSeconds: 空闲连接最长存活时间（秒），0 表示不按空闲时间回收；应小于 Redis 服务端的 timeout。
	ConnMaxIdleTimeSeconds int `mapstructure:"conn_max_idle_time_seconds"`
	// ConnMaxLifetimeSeconds: 连接最长存活时间（秒），0 表示不限制；实际到期时间带 ±10% 抖动，避免连接同一时刻集中重建。
	ConnMaxLifetimeSeconds int `mapstructure:"conn_max_lifetime_seconds"`
	// PoolStatsIntervalSeconds: 连接池状态日志间隔（秒），0 表示关闭。
	PoolStatsIntervalSeconds int `mapstructure:"pool_stats_interval_seconds"`
	// EnableTLS: 是否启用 TLS/SSL 连接
	EnableTLS bool `mapstructure:"enable_tls"`
}

func (r *RedisConfig) Address() string {
	return fmt.Sprintf("%s:%d", r.Host, r.Port)
}

type OpsConfig struct {
	// Enabled controls whether ops features should run.
	//
	// NOTE: vNext still has a DB-backed feature flag (ops_monitoring_enabled) for runtime on/off.
	// This config flag is the "hard switch" for deployments that want to disable ops completely.
	Enabled bool `mapstructure:"enabled"`

	// UsePreaggregatedTables prefers ops_metrics_hourly/daily for long-window dashboard queries.
	UsePreaggregatedTables bool `mapstructure:"use_preaggregated_tables"`

	// Cleanup controls periodic deletion of old ops data to prevent unbounded growth.
	Cleanup OpsCleanupConfig `mapstructure:"cleanup"`

	// MetricsCollectorCache controls Redis caching for expensive per-window collector queries.
	MetricsCollectorCache OpsMetricsCollectorCacheConfig `mapstructure:"metrics_collector_cache"`

	// Pre-aggregation configuration.
	Aggregation OpsAggregationConfig `mapstructure:"aggregation"`
}

type OpsCleanupConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Schedule string `mapstructure:"schedule"`

	// Retention days. Error and metrics targets accept 0 as an explicit truncate;
	// system logs require a positive value because their runtime setting is bounded.
	ErrorLogRetentionDays      int `mapstructure:"error_log_retention_days"`
	SystemLogRetentionDays     int `mapstructure:"system_log_retention_days"`
	MinuteMetricsRetentionDays int `mapstructure:"minute_metrics_retention_days"`
	HourlyMetricsRetentionDays int `mapstructure:"hourly_metrics_retention_days"`
}

type OpsAggregationConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

type OpsMetricsCollectorCacheConfig struct {
	Enabled bool          `mapstructure:"enabled"`
	TTL     time.Duration `mapstructure:"ttl"`
}

// UsageStatsCacheConfig controls the Redis cache in front of the dashboard and
// usage aggregation queries. TTL is intentionally short: cached numbers may lag
// freshly written usage logs by at most TTL. The one exception is one API key's
// multi-day aggregate (/v1/usage reports, key usage panels), which is kept for a fixed
// 15 minutes whenever TTL is shorter (repository.usageStatsKeyWindowTTL).
type UsageStatsCacheConfig struct {
	Enabled bool          `mapstructure:"enabled"`
	TTL     time.Duration `mapstructure:"ttl"`
	// MaxConcurrentLoads caps how many cache-miss aggregates of one user or API key (the usage page, the key
	// list, /v1/usage) one process runs on the database at once; 0 means no cap. The surplus waits up to
	// LoadQueueTimeout for a slot and is then answered 429 USAGE_STATS_BUSY (Retry-After: 2) instead of piling
	// connections on. One user / API key holds at most max(2, MaxConcurrentLoads/4) slots at once.
	MaxConcurrentLoads int `mapstructure:"max_concurrent_loads"`
	// LoadQueueTimeout is how long a miss waits for one of those slots; 0 turns the surplus away at once.
	LoadQueueTimeout time.Duration `mapstructure:"load_queue_timeout"`
}

type JWTConfig struct {
	Secret     string `mapstructure:"secret"`
	ExpireHour int    `mapstructure:"expire_hour"`
	// AccessTokenExpireMinutes: Access Token有效期（分钟）
	// - >0: 使用分钟配置（优先级高于 ExpireHour），默认 60 分钟
	// - =0: 回退使用 ExpireHour（仅用于显式回退的旧配置）
	AccessTokenExpireMinutes int `mapstructure:"access_token_expire_minutes"`
	// RefreshTokenExpireDays: 每枚 Refresh Token 的滑动有效期（天），默认 14 天。
	// 每次轮换都从「现在」起重新计算，所以只要 14 天内用过一次就一直保持登录，
	// 闲置超过该天数才需要重新登录。会话的绝对寿命上限见 SessionMaxLifetimeDays。
	RefreshTokenExpireDays int `mapstructure:"refresh_token_expire_days"`
	// SessionMaxLifetimeDays: 登录会话从首次登录起的绝对寿命上限（天），默认 90。
	// 轮换不会延长它，到期必须重新登录；不能小于 RefreshTokenExpireDays——小于时启动会自动抬高
	// 到相等并打一条警告（不让升级后启动失败：旧版本把 refresh_token_expire_days 同时当绝对上限，
	// 老部署可能只把它调大过）。
	SessionMaxLifetimeDays int `mapstructure:"session_max_lifetime_days"`
	// RefreshTokenReuseLeewaySeconds: 已轮换（已消费）的 Refresh Token 在被消费后的这段
	// 时间内、由消费它的那个客户端（指纹一致）再次出示，视为「响应丢失后的合法重试」——
	// 不撤销会话，再签发一对新令牌；别的客户端出示、超过宽限或名额用完的重放仍撤销整个会话家族。
	// 默认 30 秒，0 = 关闭（旧行为：任何重放都撤销），上限 120 秒。
	RefreshTokenReuseLeewaySeconds int `mapstructure:"refresh_token_reuse_leeway_seconds"`
	// RefreshWindowMinutes: 刷新窗口（分钟），在Access Token过期前多久开始允许刷新
	RefreshWindowMinutes int `mapstructure:"refresh_window_minutes"`
}

// TotpConfig TOTP 双因素认证配置
type TotpConfig struct {
	// EncryptionKey 用于加密 TOTP 密钥的 AES-256 密钥（32 字节 hex 编码）
	// 未手动配置时，启动阶段会从数据库加载或生成持久安装密钥。
	EncryptionKey string `mapstructure:"encryption_key"`
	// EncryptionKeyConfigured 标记加密密钥是否为手动配置（非自动生成）
	EncryptionKeyConfigured bool `mapstructure:"-"`
	// EncryptionKeyPersistent marks an installation key loaded or persisted by
	// database bootstrap. Process-local configuration defaults never set it.
	EncryptionKeyPersistent bool `mapstructure:"-"`
	// EncryptionKeySource records where the effective installation key came
	// from after database bootstrap. Deployments should converge on
	// TotpEncryptionKeySourceEnv; TotpEncryptionKeySourceDatabase is the
	// transitional fallback for a key that still lives in security_secrets.
	EncryptionKeySource string `mapstructure:"-"`
}

// TOTP installation key provenance. The database value is a migration
// fallback only: the deployment environment is authoritative.
const (
	TotpEncryptionKeySourceEnv      = "env"
	TotpEncryptionKeySourceDatabase = "database"
)

// EncryptionKeyUsesEnvironment reports whether the active key came from the
// deployment environment (config file or TOTP_ENCRYPTION_KEY).
func (c TotpConfig) EncryptionKeyUsesEnvironment() bool {
	return c.EncryptionKeySource == TotpEncryptionKeySourceEnv
}

// HasPersistentEncryptionKey distinguishes a stable installation key from the
// temporary key used while loading configuration before database bootstrap.
func (c TotpConfig) HasPersistentEncryptionKey() bool {
	return c.EncryptionKeyConfigured || c.EncryptionKeyPersistent
}

type TurnstileConfig struct {
	Required bool `mapstructure:"required"`
}

// CloudflareConfig holds credentials for user IP allowlisting via IP Access Rules.
type CloudflareConfig struct {
	APIToken  string `mapstructure:"api_token"`
	ZoneID    string `mapstructure:"zone_id"`
	AccountID string `mapstructure:"account_id"`
}

type DefaultConfig struct {
	AdminEmail      string  `mapstructure:"admin_email"`
	AdminPassword   string  `mapstructure:"admin_password"`
	UserConcurrency int     `mapstructure:"user_concurrency"`
	UserBalance     float64 `mapstructure:"user_balance"`
	APIKeyPrefix    string  `mapstructure:"api_key_prefix"`
	RateMultiplier  float64 `mapstructure:"rate_multiplier"`
}

type RateLimitConfig struct {
	OverloadCooldownMinutes int `mapstructure:"overload_cooldown_minutes"`  // 529过载冷却时间(分钟)
	OAuth401CooldownMinutes int `mapstructure:"oauth_401_cooldown_minutes"` // OAuth 401临时不可调度冷却(分钟)
}

// APIKeyAuthCacheConfig API Key 认证缓存配置
type APIKeyAuthCacheConfig struct {
	L1Size             int                    `mapstructure:"l1_size"`
	L1TTLSeconds       int                    `mapstructure:"l1_ttl_seconds"`
	L2TTLSeconds       int                    `mapstructure:"l2_ttl_seconds"`
	NegativeTTLSeconds int                    `mapstructure:"negative_ttl_seconds"`
	JitterPercent      int                    `mapstructure:"jitter_percent"`
	Singleflight       bool                   `mapstructure:"singleflight"`
	LookupConcurrency  int                    `mapstructure:"lookup_concurrency"`
	InvalidAbuse       InvalidAuthAbuseConfig `mapstructure:"invalid_abuse"`
}

// APIKeyCreateConfig 用户创建 API Key 的防滥用限制（0 表示不限制）
type APIKeyCreateConfig struct {
	// MaxActivePerUser 单个用户同时存在（未删除）的 API Key 上限
	MaxActivePerUser int `mapstructure:"max_active_per_user"`
	// MaxPerUserPerHour 单个用户每小时可创建的 API Key 次数（删除不返还次数）
	MaxPerUserPerHour int `mapstructure:"max_per_user_per_hour"`
}

type InvalidAuthAbuseConfig struct {
	Enabled       bool `mapstructure:"enabled"`
	Threshold     int  `mapstructure:"threshold"`
	WindowSeconds int  `mapstructure:"window_seconds"`
	BlockSeconds  int  `mapstructure:"block_seconds"`
	Capacity      int  `mapstructure:"capacity"`
}

// SubscriptionCacheConfig 订阅认证 L1 缓存配置
type SubscriptionCacheConfig struct {
	L1Size        int `mapstructure:"l1_size"`
	L1TTLSeconds  int `mapstructure:"l1_ttl_seconds"`
	JitterPercent int `mapstructure:"jitter_percent"`
}

// SubscriptionMaintenanceConfig 订阅窗口维护后台任务配置。
// 用于将“请求路径触发的维护动作”有界化，避免高并发下 goroutine 膨胀。
type SubscriptionMaintenanceConfig struct {
	WorkerCount int `mapstructure:"worker_count"`
	QueueSize   int `mapstructure:"queue_size"`
}

// DashboardCacheConfig 仪表盘统计缓存配置
type DashboardCacheConfig struct {
	// Enabled: 是否启用仪表盘缓存
	Enabled bool `mapstructure:"enabled"`
	// KeyPrefix: Redis key 前缀，用于多环境隔离
	KeyPrefix string `mapstructure:"key_prefix"`
	// StatsFreshTTLSeconds: 缓存命中认为“新鲜”的时间窗口（秒）
	StatsFreshTTLSeconds int `mapstructure:"stats_fresh_ttl_seconds"`
	// StatsTTLSeconds: Redis 缓存总 TTL（秒）
	StatsTTLSeconds int `mapstructure:"stats_ttl_seconds"`
	// StatsRefreshTimeoutSeconds: 异步刷新超时（秒）
	StatsRefreshTimeoutSeconds int `mapstructure:"stats_refresh_timeout_seconds"`
}

// DashboardAggregationConfig 仪表盘预聚合配置
type DashboardAggregationConfig struct {
	// Enabled: 是否启用预聚合作业
	Enabled bool `mapstructure:"enabled"`
	// IntervalSeconds: 聚合刷新间隔（秒）
	IntervalSeconds int `mapstructure:"interval_seconds"`
	// LookbackSeconds: 回看窗口（秒）
	LookbackSeconds int `mapstructure:"lookback_seconds"`
	// BackfillEnabled: 是否允许全量回填
	BackfillEnabled bool `mapstructure:"backfill_enabled"`
	// BackfillMaxDays: 回填最大跨度（天）
	BackfillMaxDays int `mapstructure:"backfill_max_days"`
	// Retention: 各表保留窗口（天）
	Retention DashboardAggregationRetentionConfig `mapstructure:"retention"`
	// RecomputeDays: 启动时重算最近 N 天；大于 0 时同时开启每小时一次的「最近 3 小时」迟到行自愈
	RecomputeDays int `mapstructure:"recompute_days"`
	// DimensionRollupsEnabled controls writing dimension-level usage rollups.
	DimensionRollupsEnabled bool `mapstructure:"dimension_rollups_enabled"`
	// ReadRollupsEnabled controls reading dimension-level usage rollups.
	ReadRollupsEnabled bool `mapstructure:"read_rollups_enabled"`
	// RollupStalenessSeconds caps lag for a watermark from an earlier hour.
	// Within the open hour, readers use covered closed hours and read the live tail from usage_logs.
	RollupStalenessSeconds int `mapstructure:"rollup_staleness_seconds"`
	// BackfillBatchHours bounds each dimension-rollup backfill query.
	BackfillBatchHours int `mapstructure:"backfill_batch_hours"`
	// RollupStorageBudgetGB prevents backfill from consuming excessive disk.
	RollupStorageBudgetGB int `mapstructure:"rollup_storage_budget_gb"`
}

// DashboardAggregationRetentionConfig 预聚合保留窗口
type DashboardAggregationRetentionConfig struct {
	UsageLogsDays         int `mapstructure:"usage_logs_days"`
	UsageBillingDedupDays int `mapstructure:"usage_billing_dedup_days"`
	HourlyDays            int `mapstructure:"hourly_days"`
	DailyDays             int `mapstructure:"daily_days"`
}

// UsageCleanupConfig 使用记录清理任务配置
type UsageCleanupConfig struct {
	// Enabled: 是否启用清理任务执行器
	Enabled bool `mapstructure:"enabled"`
	// MaxRangeDays: 单次任务允许的最大时间跨度（天）
	MaxRangeDays int `mapstructure:"max_range_days"`
	// BatchSize: 单批删除数量
	BatchSize int `mapstructure:"batch_size"`
	// WorkerIntervalSeconds: 后台任务轮询间隔（秒）
	WorkerIntervalSeconds int `mapstructure:"worker_interval_seconds"`
	// TaskTimeoutSeconds: 单次任务最大执行时长（秒）
	TaskTimeoutSeconds int `mapstructure:"task_timeout_seconds"`
}

func NormalizeRunMode(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case RunModeStandard, RunModeSimple:
		return normalized
	default:
		return RunModeStandard
	}
}

// Load 读取并校验完整配置（要求 jwt.secret 已显式提供）。
func Load() (*Config, error) {
	return load(false)
}

// LoadForBootstrap 读取启动阶段配置。
//
// 启动阶段允许 jwt.secret 先留空，后续由数据库初始化流程补齐并再次完整校验。
func LoadForBootstrap() (*Config, error) {
	return load(true)
}

func load(allowMissingJWTSecret bool) (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	configureConfigSource(viper.SetConfigFile, viper.AddConfigPath)

	// 环境变量支持
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	if tz, ok := os.LookupEnv("TZ"); ok && strings.TrimSpace(tz) != "" {
		// AutomaticEnv 会先把 timezone 映射到 TIMEZONE；显式 Set 保证标准 TZ 变量优先。
		viper.Set("timezone", strings.TrimSpace(tz))
	}
	if err := viper.BindEnv("server.enable_server_timing", "ENABLE_SERVER_TIMING"); err != nil {
		return nil, fmt.Errorf("bind ENABLE_SERVER_TIMING: %w", err)
	}

	// 默认值
	setDefaults()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config error: %w", err)
		}
		// 配置文件不存在时使用默认值
	}
	trustedProxiesEnv, trustedProxiesEnvConfigured := os.LookupEnv("SERVER_TRUSTED_PROXIES")
	forwardedClientIPHeadersEnv, forwardedClientIPHeadersEnvConfigured := os.LookupEnv("SECURITY_FORWARDED_CLIENT_IP_HEADERS")
	trustedProxiesConfigured := viper.InConfig("server.trusted_proxies") ||
		viper.IsSet("server.trusted_proxies") || trustedProxiesEnvConfigured

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config error: %w", err)
	}
	if trustedProxiesEnvConfigured {
		cfg.Server.TrustedProxies = normalizeStringSlice(strings.Split(trustedProxiesEnv, ","))
	}
	if forwardedClientIPHeadersEnvConfigured {
		cfg.Security.ForwardedClientIPHeaders = normalizeStringSlice(strings.Split(forwardedClientIPHeadersEnv, ","))
	}
	if honeypotExemptEnv, ok := os.LookupEnv("SECURITY_HONEYPOT_EXEMPT_NETS"); ok {
		cfg.Security.HoneypotExemptNets = normalizeStringSlice(strings.Split(honeypotExemptEnv, ","))
	}
	cfg.Server.TrustedProxiesConfigured = trustedProxiesConfigured
	if cfg.Gateway.OpenAIScheduler.StickyEscapeTTFTMs == 0 {
		cfg.Gateway.OpenAIScheduler.StickyEscapeTTFTMs = 15000
	}
	if cfg.Gateway.OpenAIScheduler.StickyEscapeErrorRate == 0 {
		cfg.Gateway.OpenAIScheduler.StickyEscapeErrorRate = 0.5
	}
	// Kept as a backstop: setEnvReachableDefaults now registers this key with its
	// effective default (true), so IsSet always reports true and this branch no
	// longer fires. It still guards the default if that registration is dropped.
	if !cfg.Gateway.OpenAIScheduler.StickyEscapeEnabled && !viper.IsSet("gateway.openai_scheduler.sticky_escape_enabled") {
		cfg.Gateway.OpenAIScheduler.StickyEscapeEnabled = true
	}

	cfg.RunMode = NormalizeRunMode(cfg.RunMode)
	cfg.Database.MigrationMode = strings.ToLower(strings.TrimSpace(cfg.Database.MigrationMode))
	cfg.Server.Mode = strings.ToLower(strings.TrimSpace(cfg.Server.Mode))
	if cfg.Server.Mode == "" {
		cfg.Server.Mode = "debug"
	}
	cfg.Server.FrontendURL = strings.TrimSpace(cfg.Server.FrontendURL)
	cfg.JWT.Secret = strings.TrimSpace(cfg.JWT.Secret)
	cfg.LinuxDo.ClientID = strings.TrimSpace(cfg.LinuxDo.ClientID)
	cfg.LinuxDo.ClientSecret = strings.TrimSpace(cfg.LinuxDo.ClientSecret)
	cfg.LinuxDo.AuthorizeURL = strings.TrimSpace(cfg.LinuxDo.AuthorizeURL)
	cfg.LinuxDo.TokenURL = strings.TrimSpace(cfg.LinuxDo.TokenURL)
	cfg.LinuxDo.UserInfoURL = strings.TrimSpace(cfg.LinuxDo.UserInfoURL)
	cfg.LinuxDo.Scopes = strings.TrimSpace(cfg.LinuxDo.Scopes)
	cfg.LinuxDo.RedirectURL = strings.TrimSpace(cfg.LinuxDo.RedirectURL)
	cfg.LinuxDo.FrontendRedirectURL = strings.TrimSpace(cfg.LinuxDo.FrontendRedirectURL)
	cfg.LinuxDo.TokenAuthMethod = strings.ToLower(strings.TrimSpace(cfg.LinuxDo.TokenAuthMethod))
	cfg.LinuxDo.UserInfoEmailPath = strings.TrimSpace(cfg.LinuxDo.UserInfoEmailPath)
	cfg.LinuxDo.UserInfoIDPath = strings.TrimSpace(cfg.LinuxDo.UserInfoIDPath)
	cfg.LinuxDo.UserInfoUsernamePath = strings.TrimSpace(cfg.LinuxDo.UserInfoUsernamePath)
	applyLegacyWeChatConnectEnvCompatibility(&cfg.WeChat)
	normalizeWeChatConnectConfig(&cfg.WeChat)
	cfg.OIDC.ProviderName = strings.TrimSpace(cfg.OIDC.ProviderName)
	cfg.OIDC.ClientID = strings.TrimSpace(cfg.OIDC.ClientID)
	cfg.OIDC.ClientSecret = strings.TrimSpace(cfg.OIDC.ClientSecret)
	cfg.OIDC.IssuerURL = strings.TrimSpace(cfg.OIDC.IssuerURL)
	cfg.OIDC.DiscoveryURL = strings.TrimSpace(cfg.OIDC.DiscoveryURL)
	cfg.OIDC.AuthorizeURL = strings.TrimSpace(cfg.OIDC.AuthorizeURL)
	cfg.OIDC.TokenURL = strings.TrimSpace(cfg.OIDC.TokenURL)
	cfg.OIDC.UserInfoURL = strings.TrimSpace(cfg.OIDC.UserInfoURL)
	cfg.OIDC.JWKSURL = strings.TrimSpace(cfg.OIDC.JWKSURL)
	cfg.OIDC.Scopes = strings.TrimSpace(cfg.OIDC.Scopes)
	cfg.OIDC.RedirectURL = strings.TrimSpace(cfg.OIDC.RedirectURL)
	cfg.OIDC.FrontendRedirectURL = strings.TrimSpace(cfg.OIDC.FrontendRedirectURL)
	cfg.OIDC.TokenAuthMethod = strings.ToLower(strings.TrimSpace(cfg.OIDC.TokenAuthMethod))
	cfg.OIDC.AllowedSigningAlgs = strings.TrimSpace(cfg.OIDC.AllowedSigningAlgs)
	cfg.OIDC.UserInfoEmailPath = strings.TrimSpace(cfg.OIDC.UserInfoEmailPath)
	cfg.OIDC.UserInfoIDPath = strings.TrimSpace(cfg.OIDC.UserInfoIDPath)
	cfg.OIDC.UserInfoUsernamePath = strings.TrimSpace(cfg.OIDC.UserInfoUsernamePath)
	cfg.OIDC.UsePKCEExplicit = hasExplicitConfigOrEnv("oidc_connect.use_pkce", "OIDC_CONNECT_USE_PKCE")
	cfg.OIDC.ValidateIDTokenExplicit = hasExplicitConfigOrEnv("oidc_connect.validate_id_token", "OIDC_CONNECT_VALIDATE_ID_TOKEN")
	cfg.Dashboard.KeyPrefix = strings.TrimSpace(cfg.Dashboard.KeyPrefix)
	cfg.CORS.AllowedOrigins = normalizeStringSlice(cfg.CORS.AllowedOrigins)
	cfg.Security.ResponseHeaders.AdditionalAllowed = normalizeStringSlice(cfg.Security.ResponseHeaders.AdditionalAllowed)
	cfg.Security.ResponseHeaders.ForceRemove = normalizeStringSlice(cfg.Security.ResponseHeaders.ForceRemove)
	cfg.Security.CSP.Policy = strings.TrimSpace(cfg.Security.CSP.Policy)
	forwardedClientIPHeaders, err := NormalizeForwardedClientIPHeaders(cfg.Security.ForwardedClientIPHeaders)
	if err != nil {
		return nil, fmt.Errorf("security.forwarded_client_ip_headers: %w", err)
	}
	cfg.Security.ForwardedClientIPHeaders = forwardedClientIPHeaders
	cfg.SetForwardedClientIPSettings(cfg.Security.TrustForwardedIPForAPIKeyACL, forwardedClientIPHeaders)
	cfg.Log.Level = strings.ToLower(strings.TrimSpace(cfg.Log.Level))
	cfg.Log.Format = strings.ToLower(strings.TrimSpace(cfg.Log.Format))
	cfg.Log.ServiceName = strings.TrimSpace(cfg.Log.ServiceName)
	cfg.Log.Environment = strings.TrimSpace(cfg.Log.Environment)
	cfg.Log.StacktraceLevel = strings.ToLower(strings.TrimSpace(cfg.Log.StacktraceLevel))
	cfg.Log.Output.FilePath = strings.TrimSpace(cfg.Log.Output.FilePath)
	cfg.Gateway.ForcedCodexInstructionsTemplateFile = strings.TrimSpace(cfg.Gateway.ForcedCodexInstructionsTemplateFile)
	if cfg.Gateway.ForcedCodexInstructionsTemplateFile != "" {
		content, err := os.ReadFile(cfg.Gateway.ForcedCodexInstructionsTemplateFile)
		if err != nil {
			return nil, fmt.Errorf("read forced codex instructions template %q: %w", cfg.Gateway.ForcedCodexInstructionsTemplateFile, err)
		}
		cfg.Gateway.ForcedCodexInstructionsTemplate = string(content)
	}

	// 兼容旧键 gateway.disable_codex_originator_normalization：语义已被
	// disable_codex_identity_enforcement 取代（身份改写升级为强制统一出口），
	// 任一为 true 即关闭强制统一。
	if cfg.Gateway.DisableCodexOriginatorNormalization {
		cfg.Gateway.DisableCodexIdentityEnforcement = true
	}

	// 兼容旧键 gateway.openai_ws.sticky_previous_response_ttl_seconds。
	// 新键未配置（<=0）时回退旧键；新键优先。
	if cfg.Gateway.OpenAIWS.StickyResponseIDTTLSeconds <= 0 && cfg.Gateway.OpenAIWS.StickyPreviousResponseTTLSeconds > 0 {
		cfg.Gateway.OpenAIWS.StickyResponseIDTTLSeconds = cfg.Gateway.OpenAIWS.StickyPreviousResponseTTLSeconds
	}

	// Normalize UMQ mode: 白名单校验，非法值在加载时一次性 warn 并清空
	if m := cfg.Gateway.UserMessageQueue.Mode; m != "" && m != UMQModeSerialize && m != UMQModeThrottle {
		slog.Warn("invalid user_message_queue mode, disabling",
			"mode", m,
			"valid_modes", []string{UMQModeSerialize, UMQModeThrottle})
		cfg.Gateway.UserMessageQueue.Mode = ""
	}

	// Auto-generate TOTP encryption key if not set (32 bytes = 64 hex chars for AES-256)
	cfg.Totp.EncryptionKey = strings.TrimSpace(cfg.Totp.EncryptionKey)
	if cfg.Totp.EncryptionKey == "" {
		key, err := generateJWTSecret(32) // Reuse the same random generation function
		if err != nil {
			return nil, fmt.Errorf("generate totp encryption key error: %w", err)
		}
		cfg.Totp.EncryptionKey = key
		cfg.Totp.EncryptionKeyConfigured = false
		slog.Info("TOTP installation key will be loaded or persisted during database initialization.")
	} else {
		cfg.Totp.EncryptionKeyConfigured = true
	}

	originalJWTSecret := cfg.JWT.Secret
	if allowMissingJWTSecret && originalJWTSecret == "" {
		// 启动阶段允许先无 JWT 密钥，后续在数据库初始化后补齐。
		cfg.JWT.Secret = strings.Repeat("0", 32)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config error: %w", err)
	}

	if allowMissingJWTSecret && originalJWTSecret == "" {
		cfg.JWT.Secret = ""
	}

	if !cfg.Security.URLAllowlist.Enabled {
		slog.Warn("security.url_allowlist.enabled=false; allowlist/SSRF checks disabled (minimal format validation only).")
	}
	if !cfg.Security.ResponseHeaders.Enabled {
		slog.Warn("security.response_headers.enabled=false; configurable header filtering disabled (default allowlist only).")
	}

	if cfg.JWT.Secret != "" && isWeakJWTSecret(cfg.JWT.Secret) {
		slog.Warn("JWT secret appears weak; use a 32+ character random secret in production.")
	}
	if len(cfg.Security.ResponseHeaders.AdditionalAllowed) > 0 || len(cfg.Security.ResponseHeaders.ForceRemove) > 0 {
		slog.Info("response header policy configured",
			"additional_allowed", cfg.Security.ResponseHeaders.AdditionalAllowed,
			"force_remove", cfg.Security.ResponseHeaders.ForceRemove,
		)
	}

	return &cfg, nil
}

func configureConfigSource(setConfigFile, addConfigPath func(string)) {
	if configFile := strings.TrimSpace(os.Getenv("CONFIG_FILE")); configFile != "" {
		setConfigFile(configFile)
		return
	}

	// Add config paths in priority order.
	if dataDir := strings.TrimSpace(os.Getenv("DATA_DIR")); dataDir != "" {
		addConfigPath(dataDir)
	}
	addConfigPath("/app/data")
	addConfigPath(".")
	addConfigPath("./config")
	addConfigPath("/etc/sub2api")
}

func setDefaults() {
	viper.SetDefault("run_mode", RunModeStandard)

	// Server
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.mode", "release")
	viper.SetDefault("server.delegate_payments", false)
	viper.SetDefault("server.enable_server_timing", false)
	viper.SetDefault("server.frontend_url", "")
	viper.SetDefault("server.read_header_timeout", 10) // 10秒读取请求头
	viper.SetDefault("server.max_header_bytes", 64*1024)
	viper.SetDefault("server.idle_timeout", 120) // 120秒空闲超时
	viper.SetDefault("server.max_request_body_size", int64(256*1024*1024))
	// Rendered index.html cache TTL (seconds). Short on purpose: it caps how long
	// a replica can keep injecting stale window.__APP_CONFIG__ when a settings
	// invalidation broadcast never arrives.
	viper.SetDefault("server.settings_injection_cache_ttl_seconds", 45)
	// H2C 默认配置
	viper.SetDefault("server.h2c.enabled", false)
	viper.SetDefault("server.h2c.max_concurrent_streams", uint32(50))      // 50 个并发流
	viper.SetDefault("server.h2c.idle_timeout", 75)                        // 75 秒
	viper.SetDefault("server.h2c.max_read_frame_size", 1<<20)              // 1MB（够用）
	viper.SetDefault("server.h2c.max_upload_buffer_per_connection", 2<<20) // 2MB
	viper.SetDefault("server.h2c.max_upload_buffer_per_stream", 512<<10)   // 512KB

	// Log
	viper.SetDefault("log.level", "info")
	viper.SetDefault("log.format", "console")
	viper.SetDefault("log.service_name", "sub2api")
	viper.SetDefault("log.env", "production")
	viper.SetDefault("log.caller", true)
	viper.SetDefault("log.stacktrace_level", "error")
	viper.SetDefault("log.output.to_stdout", true)
	viper.SetDefault("log.output.to_file", true)
	viper.SetDefault("log.output.file_path", "")
	viper.SetDefault("log.rotation.max_size_mb", 100)
	viper.SetDefault("log.rotation.max_backups", 10)
	viper.SetDefault("log.rotation.max_age_days", 7)
	viper.SetDefault("log.rotation.compress", true)
	viper.SetDefault("log.rotation.local_time", true)
	viper.SetDefault("log.sampling.enabled", true)
	viper.SetDefault("log.sampling.initial", 100)
	viper.SetDefault("log.sampling.thereafter", 100)

	// CORS
	viper.SetDefault("cors.allowed_origins", []string{})
	viper.SetDefault("cors.allow_credentials", true)

	// WebAuthn / Passkeys are opt-in because every deployment must explicitly
	// declare its relying-party domain and trusted browser origins.
	viper.SetDefault("webauthn.enabled", false)
	viper.SetDefault("webauthn.rp_display_name", "Sub2API")
	viper.SetDefault("webauthn.rp_id", "")
	viper.SetDefault("webauthn.rp_origins", []string{})

	// Security
	viper.SetDefault("security.account_pool_owner_user_id", int64(0))
	viper.SetDefault("security.url_allowlist.enabled", false)
	viper.SetDefault("security.url_allowlist.upstream_hosts", []string{
		"api.openai.com",
		"api.anthropic.com",
		"api.kimi.com",
		"api.moonshot.ai",
		"api.moonshot.cn",
		"open.bigmodel.cn",
		"api.minimaxi.com", // MiniMax CN quota + inference
		"api.minimax.io",   // MiniMax intl; frozen allowlists must add this host to use the intl site
		"generativelanguage.googleapis.com",
		"cloudcode-pa.googleapis.com",
		"*.openai.azure.com",
	})
	viper.SetDefault("security.url_allowlist.pricing_hosts", []string{
		"raw.githubusercontent.com",
	})
	viper.SetDefault("security.url_allowlist.crs_hosts", []string{})
	viper.SetDefault("security.url_allowlist.allow_private_hosts", true)
	// 上游渠道站点地址默认禁止私网：老部署若确实指向内网，需显式打开
	// security.url_allowlist.upstream_channel_allow_private_hosts。
	viper.SetDefault("security.url_allowlist.upstream_channel_allow_private_hosts", false)
	viper.SetDefault("security.url_allowlist.allow_insecure_http", true)
	viper.SetDefault("security.response_headers.enabled", true)
	viper.SetDefault("security.response_headers.additional_allowed", []string{})
	viper.SetDefault("security.response_headers.force_remove", []string{})
	viper.SetDefault("security.csp.enabled", true)
	viper.SetDefault("security.csp.policy", DefaultCSPPolicy)
	viper.SetDefault("security.proxy_probe.insecure_skip_verify", false)
	viper.SetDefault("security.trust_forwarded_ip_for_api_key_acl", true)
	viper.SetDefault("security.key_rotation_gate_enabled", true)
	viper.SetDefault("security.session_binding_mode", SessionBindingModeLoose)
	viper.SetDefault("security.session_binding_on_mismatch", SessionBindingOnMismatchRebind)
	viper.SetDefault("security.oauth_code_exchange_enabled", true)
	// 第三方身份 subject 明文双写默认关闭：新行只落 keyed 盲索引 + 前缀。
	// 读取路径仍保留明文回退，回填前的历史行不受影响。
	viper.SetDefault("security.auth_subject_plaintext_dual_write", false)
	viper.SetDefault("security.key_rotation_cutoff", DefaultKeyRotationCutoff)

	// Security - disable direct fallback on proxy error
	viper.SetDefault("security.proxy_fallback.allow_direct_on_error", false)

	// Billing
	viper.SetDefault("billing.circuit_breaker.enabled", true)
	viper.SetDefault("billing.circuit_breaker.failure_threshold", 5)
	viper.SetDefault("billing.circuit_breaker.reset_timeout_seconds", 30)
	viper.SetDefault("billing.circuit_breaker.half_open_requests", 3)
	viper.SetDefault("billing.balance_preauthorization_enabled", false)
	// 余额扣费合并结算默认关闭。开启后扣费先落持久化队列（billing_balance_settlements，
	// status=settlement_pending），由 UsageBalanceSettlementWorker 按用户合并成一次
	// users.balance 更新。回滚：环境变量置 false 重启即可，worker 始终运行，队列里的
	// 扣费照常结算，不会丢。
	viper.SetDefault("billing.balance_merge_settlement_enabled", false)
	// 合并结算的积压保护（仅在上面的开关为 true 时才有意义）：待结算行数超过
	// max_backlog 或最老一笔等待超过 max_age_seconds，本进程新请求退回逐请求同步扣费，
	// 回落到阈值的一半以下才恢复入队；队列里已有的扣费继续结算，不丢、不重复。
	// 默认值只在队列明显失控时才触发（正常稳态只有几百行、不到 1 秒），0 表示不按该维度退回。
	viper.SetDefault("billing.balance_merge_settlement_max_backlog", 20000)
	viper.SetDefault("billing.balance_merge_settlement_max_age_seconds", 30)
	viper.SetDefault("billing.minimum_balance_reserve", 0.000001)
	// 无 usage 的 OpenAI 流式异常结束按请求体估算输入时，缓存读占比（0-100）。
	// 0 = 不臆造缓存折扣，与上游 new-api 异常结束回退（整段 prompt 按普通输入计）
	// 对齐；95 = #456 之前的旧口径。仅影响估算，绝不改写上游真实上报的 usage。
	viper.SetDefault("billing.openai_incomplete_input_cache_read_percent", 0)
	viper.SetDefault("billing.anthropic_incomplete_input_cache_read_percent", 0)
	viper.SetDefault("billing.gemini_incomplete_input_cache_read_percent", 0)
	viper.SetDefault("billing.user_platform_quota_cache_ttl_seconds", 86400)
	viper.SetDefault("billing.user_platform_quota_sentinel_ttl_seconds", 3600)
	// 在途预留默认关闭。开启后会在准入时按估算费用占用余额，生产保持关，需要时显式打开。
	viper.SetDefault("billing.inflight_reservation.enabled", false)
	viper.SetDefault("billing.inflight_reservation.ttl_seconds", 900)
	viper.SetDefault("billing.inflight_reservation.default_max_tokens", 8192)
	viper.SetDefault("billing.inflight_reservation.max_output_tokens", 128000)
	viper.SetDefault("billing.inflight_reservation.max_input_tokens", 200000)
	viper.SetDefault("billing.inflight_reservation.min_input_tokens", 4096)
	viper.SetDefault("billing.inflight_reservation.max_reservation_usd", 0)
	viper.SetDefault("billing.inflight_reservation.fail_closed_on_unpriced", false)
	viper.SetDefault("billing.inflight_reservation.fail_closed_on_reserve_error", false)
	viper.SetDefault("billing.inflight_reservation.fail_closed_on_reserve_error", false)

	// Turnstile
	viper.SetDefault("turnstile.required", false)
	viper.SetDefault("cloudflare.api_token", "")
	viper.SetDefault("cloudflare.zone_id", "")
	viper.SetDefault("cloudflare.account_id", "")

	// LinuxDo Connect OAuth 登录
	viper.SetDefault("linuxdo_connect.enabled", false)
	viper.SetDefault("linuxdo_connect.client_id", "")
	viper.SetDefault("linuxdo_connect.client_secret", "")
	viper.SetDefault("linuxdo_connect.authorize_url", "https://connect.linux.do/oauth2/authorize")
	viper.SetDefault("linuxdo_connect.token_url", "https://connect.linux.do/oauth2/token")
	viper.SetDefault("linuxdo_connect.userinfo_url", "https://connect.linux.do/api/user")
	viper.SetDefault("linuxdo_connect.scopes", "user")
	viper.SetDefault("linuxdo_connect.redirect_url", "")
	viper.SetDefault("linuxdo_connect.frontend_redirect_url", "/auth/linuxdo/callback")
	viper.SetDefault("linuxdo_connect.token_auth_method", "client_secret_post")
	viper.SetDefault("linuxdo_connect.use_pkce", false)
	viper.SetDefault("linuxdo_connect.userinfo_email_path", "")
	viper.SetDefault("linuxdo_connect.userinfo_id_path", "")
	viper.SetDefault("linuxdo_connect.userinfo_username_path", "")

	// WeChat Connect OAuth 登录
	viper.SetDefault("wechat_connect.enabled", false)
	viper.SetDefault("wechat_connect.app_id", "")
	viper.SetDefault("wechat_connect.app_secret", "")
	viper.SetDefault("wechat_connect.open_app_id", "")
	viper.SetDefault("wechat_connect.open_app_secret", "")
	viper.SetDefault("wechat_connect.mp_app_id", "")
	viper.SetDefault("wechat_connect.mp_app_secret", "")
	viper.SetDefault("wechat_connect.mobile_app_id", "")
	viper.SetDefault("wechat_connect.mobile_app_secret", "")
	viper.SetDefault("wechat_connect.open_enabled", false)
	viper.SetDefault("wechat_connect.mp_enabled", false)
	viper.SetDefault("wechat_connect.mobile_enabled", false)
	viper.SetDefault("wechat_connect.mode", defaultWeChatConnectMode)
	viper.SetDefault("wechat_connect.scopes", defaultWeChatConnectScopes)
	viper.SetDefault("wechat_connect.redirect_url", "")
	viper.SetDefault("wechat_connect.frontend_redirect_url", defaultWeChatConnectFrontendRedirect)

	// Generic OIDC OAuth 登录
	viper.SetDefault("oidc_connect.enabled", false)
	viper.SetDefault("oidc_connect.provider_name", "OIDC")
	viper.SetDefault("oidc_connect.client_id", "")
	viper.SetDefault("oidc_connect.client_secret", "")
	viper.SetDefault("oidc_connect.issuer_url", "")
	viper.SetDefault("oidc_connect.discovery_url", "")
	viper.SetDefault("oidc_connect.authorize_url", "")
	viper.SetDefault("oidc_connect.token_url", "")
	viper.SetDefault("oidc_connect.userinfo_url", "")
	viper.SetDefault("oidc_connect.jwks_url", "")
	viper.SetDefault("oidc_connect.scopes", "openid email profile")
	viper.SetDefault("oidc_connect.redirect_url", "")
	viper.SetDefault("oidc_connect.frontend_redirect_url", "/auth/oidc/callback")
	viper.SetDefault("oidc_connect.token_auth_method", "client_secret_post")
	viper.SetDefault("oidc_connect.use_pkce", true)
	viper.SetDefault("oidc_connect.validate_id_token", true)
	viper.SetDefault("oidc_connect.allowed_signing_algs", "RS256,ES256,PS256")
	viper.SetDefault("oidc_connect.clock_skew_seconds", 120)
	viper.SetDefault("oidc_connect.require_email_verified", false)
	viper.SetDefault("oidc_connect.userinfo_email_path", "")
	viper.SetDefault("oidc_connect.userinfo_id_path", "")
	viper.SetDefault("oidc_connect.userinfo_username_path", "")

	// DingTalk Connect OAuth 登录
	viper.SetDefault("dingtalk_connect.enabled", false)
	viper.SetDefault("dingtalk_connect.authorize_url", "https://login.dingtalk.com/oauth2/auth")
	viper.SetDefault("dingtalk_connect.token_url", "https://api.dingtalk.com/v1.0/oauth2/userAccessToken")
	viper.SetDefault("dingtalk_connect.userinfo_url", "https://api.dingtalk.com/v1.0/contact/users/me")
	viper.SetDefault("dingtalk_connect.scopes", "openid")
	viper.SetDefault("dingtalk_connect.frontend_redirect_url", "/auth/dingtalk/callback")
	viper.SetDefault("dingtalk_connect.dingtalk_app_kind", "internal_app")
	viper.SetDefault("dingtalk_connect.app_type", "public")
	viper.SetDefault("dingtalk_connect.corp_restriction_policy", "none")
	viper.SetDefault("dingtalk_connect.require_email", true)
	viper.SetDefault("dingtalk_connect.username_overwrite_policy", "if_empty")

	// Database
	viper.SetDefault("database.host", "localhost")
	viper.SetDefault("database.port", 5432)
	viper.SetDefault("database.user", "postgres")
	viper.SetDefault("database.password", "postgres")
	viper.SetDefault("database.dbname", "sub2api")
	viper.SetDefault("database.sslmode", "prefer")
	viper.SetDefault("database.migration_mode", DatabaseMigrationModeApply)
	viper.SetDefault("database.max_open_conns", 256)
	viper.SetDefault("database.max_idle_conns", 128)
	viper.SetDefault("database.conn_max_lifetime_minutes", 30)
	viper.SetDefault("database.conn_max_idle_time_minutes", 5)
	viper.SetDefault("database.user_platform_quota_flusher_enabled", false)
	viper.SetDefault("database.user_platform_quota_flush_interval_ms", 2000)
	viper.SetDefault("database.user_platform_quota_flush_batch_size", 1000)

	// Redis
	viper.SetDefault("redis.host", "localhost")
	viper.SetDefault("redis.port", 6379)
	viper.SetDefault("redis.username", "")
	viper.SetDefault("redis.password", "")
	viper.SetDefault("redis.db", 0)
	viper.SetDefault("redis.dial_timeout_seconds", 5)
	viper.SetDefault("redis.read_timeout_seconds", 3)
	viper.SetDefault("redis.write_timeout_seconds", 3)
	viper.SetDefault("redis.pool_size", 512)
	viper.SetDefault("redis.min_idle_conns", 8)
	viper.SetDefault("redis.max_idle_conns", 64)
	viper.SetDefault("redis.pool_fifo", true)
	viper.SetDefault("redis.pool_timeout_ms", 1000)
	viper.SetDefault("redis.conn_max_idle_time_seconds", 300)
	viper.SetDefault("redis.conn_max_lifetime_seconds", 1800)
	viper.SetDefault("redis.pool_stats_interval_seconds", 60)
	viper.SetDefault("redis.enable_tls", false)

	// Batch Image queue
	viper.SetDefault("batch_image.enabled", false)
	viper.SetDefault("batch_image.max_items_per_job_default", 200)
	viper.SetDefault("batch_image.max_items_per_job_trial", 50)
	viper.SetDefault("batch_image.max_output_images_per_job", 200)
	viper.SetDefault("batch_image.max_output_images_per_item", 4)
	viper.SetDefault("batch_image.max_prompt_chars_per_item", 8000)
	viper.SetDefault("batch_image.max_reference_images_per_job", 1000)
	viper.SetDefault("batch_image.max_reference_inline_bytes_per_job", 134217728)
	viper.SetDefault("batch_image.default_response_mime_type", "image/png")
	viper.SetDefault("batch_image.default_image_size", "1K")
	viper.SetDefault("batch_image.max_download_items_zip", 200)
	viper.SetDefault("batch_image.max_download_bytes_per_request", 536870912)
	viper.SetDefault("batch_image.max_download_duration_seconds", 600)
	viper.SetDefault("batch_image.max_download_concurrency_per_user", 1)
	viper.SetDefault("batch_image.input_retention_after_terminal_hours", 24)
	viper.SetDefault("batch_image.output_retention_after_terminal_hours", 72)
	viper.SetDefault("batch_image.output_retention_max_days", 7)
	viper.SetDefault("batch_image.cleanup_interval_minutes", 30)
	viper.SetDefault("batch_image.cleanup_batch_size", 100)
	viper.SetDefault("batch_image.queue_enabled", false)
	viper.SetDefault("batch_image.queue_ready_key", "batch_image:queue:ready")
	viper.SetDefault("batch_image.queue_delayed_key", "batch_image:queue:delayed")
	viper.SetDefault("batch_image.queue_active_key", "batch_image:queue:active")
	viper.SetDefault("batch_image.inflight_key_prefix", "batch_image:queue:inflight:")
	viper.SetDefault("batch_image.lock_key_prefix", "batch_image:queue:lock:")
	viper.SetDefault("batch_image.idempotency_key_prefix", "batch_image:queue:idem:")
	viper.SetDefault("batch_image.inflight_ttl_seconds", 604800)
	viper.SetDefault("batch_image.job_lock_ttl_seconds", 300)
	viper.SetDefault("batch_image.default_requeue_delay_seconds", 30)
	viper.SetDefault("batch_image.error_retry_delay_seconds", 60)
	viper.SetDefault("batch_image.lock_conflict_delay_seconds", 5)
	viper.SetDefault("batch_image.stale_active_after_seconds", 600)
	viper.SetDefault("batch_image.delayed_mover_interval_seconds", 5)
	viper.SetDefault("batch_image.recovery_interval_seconds", 300)
	viper.SetDefault("batch_image.delayed_move_limit", 100)
	viper.SetDefault("batch_image.recover_limit", 100)
	viper.SetDefault("batch_image.vertex_enabled", false)
	viper.SetDefault("batch_image.vertex_project_id", "")
	viper.SetDefault("batch_image.vertex_location", "global")
	viper.SetDefault("batch_image.vertex_managed_gcs_bucket", "")
	viper.SetDefault("batch_image.vertex_managed_gcs_prefix", "batch-image/{env}/{batch_id}")
	viper.SetDefault("batch_image.vertex_input_retention_hours", 24)
	viper.SetDefault("batch_image.vertex_output_retention_hours", 72)
	viper.SetDefault("batch_image.vertex_batch_prediction_base_url", "")
	viper.SetDefault("batch_image.vertex_gcs_base_url", "")

	// Image storage (async image task result offload to S3-compatible object storage)
	viper.SetDefault("image_storage.enabled", false)
	viper.SetDefault("image_storage.region", "auto")
	viper.SetDefault("image_storage.prefix", "images/")
	viper.SetDefault("image_storage.force_path_style", false)
	viper.SetDefault("image_storage.presign_expiry_hours", 24)
	viper.SetDefault("image_storage.max_download_bytes", 33554432)
	// Registered with empty defaults so AutomaticEnv can reach them: viper only
	// decodes keys present in AllKeys(), so a credential that is supplied purely
	// via IMAGE_STORAGE_* and never appears in config.yaml would be dropped and
	// silently disable the whole async image feature.
	viper.SetDefault("image_storage.endpoint", "")
	viper.SetDefault("image_storage.bucket", "")
	viper.SetDefault("image_storage.access_key_id", "")
	viper.SetDefault("image_storage.secret_access_key", "")
	viper.SetDefault("image_storage.public_base_url", "")

	// Built-in backup. All zero = original behaviour. Every key must be registered
	// here or BACKUP_* environment variables are silently ignored by viper.
	viper.SetDefault("backup.pg_dump_path", "")
	viper.SetDefault("backup.exclude_table_data", []string{})
	viper.SetDefault("backup.age_recipients", []string{})
	viper.SetDefault("backup.max_archive_bytes", int64(0))

	// Ops (vNext)
	viper.SetDefault("ops.enabled", true)
	viper.SetDefault("ops.use_preaggregated_tables", true)
	viper.SetDefault("ops.cleanup.enabled", true)
	viper.SetDefault("ops.cleanup.schedule", "0 2 * * *")
	// Retention days: vNext defaults to 30 days across ops datasets.
	viper.SetDefault("ops.cleanup.error_log_retention_days", 1)
	viper.SetDefault("ops.cleanup.system_log_retention_days", 30)
	viper.SetDefault("ops.cleanup.minute_metrics_retention_days", 30)
	viper.SetDefault("ops.cleanup.hourly_metrics_retention_days", 30)
	viper.SetDefault("ops.aggregation.enabled", true)
	viper.SetDefault("ops.metrics_collector_cache.enabled", true)
	// TTL should be slightly larger than collection interval (1m) to maximize cross-replica cache hits.
	viper.SetDefault("ops.metrics_collector_cache.ttl", 65*time.Second)

	// Usage stats cache: one minute of reuse for dashboard/usage aggregates. The
	// write path never invalidates it, so TTL is the maximum staleness.
	viper.SetDefault("usage_stats_cache.enabled", true)
	viper.SetDefault("usage_stats_cache.ttl", 60*time.Second)
	// At most 8 user/API-key aggregates scan usage_logs at once per process; the rest queue for up to 8 s.
	viper.SetDefault("usage_stats_cache.max_concurrent_loads", 8)
	viper.SetDefault("usage_stats_cache.load_queue_timeout", 8*time.Second)

	// JWT
	viper.SetDefault("jwt.secret", "")
	viper.SetDefault("jwt.expire_hour", 24)
	viper.SetDefault("jwt.access_token_expire_minutes", 60) // 默认 60 分钟；显式设为 0 才回退到 expire_hour
	// 每枚 Refresh Token 的滑动有效期；每次轮换重新计时，闲置这么久才需要重新登录
	viper.SetDefault("jwt.refresh_token_expire_days", DefaultRefreshTokenExpireDays)
	// 会话从首次登录起的绝对上限（天），必须 >= refresh_token_expire_days
	viper.SetDefault("jwt.session_max_lifetime_days", DefaultSessionMaxLifetimeDays)
	// 已轮换 Refresh Token 的重试宽限（秒）；0 = 关闭
	viper.SetDefault("jwt.refresh_token_reuse_leeway_seconds", DefaultRefreshTokenReuseLeewaySeconds)
	viper.SetDefault("jwt.refresh_window_minutes", 2) // 过期前2分钟开始允许刷新

	// TOTP
	viper.SetDefault("totp.encryption_key", "")

	// Default
	// Admin credentials are created via the setup flow (web wizard / CLI / AUTO_SETUP).
	// Do not ship fixed defaults here to avoid insecure "known credentials" in production.
	viper.SetDefault("default.admin_email", "")
	viper.SetDefault("default.admin_password", "")
	viper.SetDefault("default.user_concurrency", 5)
	viper.SetDefault("default.user_balance", 0)
	viper.SetDefault("default.api_key_prefix", "sk-")
	viper.SetDefault("default.rate_multiplier", 1.0)

	// RateLimit
	viper.SetDefault("rate_limit.overload_cooldown_minutes", 10)
	viper.SetDefault("rate_limit.oauth_401_cooldown_minutes", 10)

	// Pricing - 从 model-price-repo main 分支同步模型定价和上下文窗口数据
	viper.SetDefault("pricing.remote_url", "https://raw.githubusercontent.com/Wei-Shaw/model-price-repo/main/model_prices_and_context_window.json")
	viper.SetDefault("pricing.hash_url", "https://raw.githubusercontent.com/Wei-Shaw/model-price-repo/main/model_prices_and_context_window.sha256")
	viper.SetDefault("pricing.data_dir", "./data")
	viper.SetDefault("pricing.fallback_file", DefaultPricingFallbackFile)
	viper.SetDefault("pricing.override_file", "")
	viper.SetDefault("pricing.update_interval_hours", 24)
	viper.SetDefault("pricing.hash_check_interval_minutes", 10)

	// 本地进程插件。插件必须由管理员手动上传，项目默认不携带任何插件能力。
	viper.SetDefault("plugins.data_dir", "")
	viper.SetDefault("plugins.allow_unsigned", false)
	viper.SetDefault("plugins.trusted_publishers", map[string]string{})
	viper.SetDefault("plugins.max_upload_bytes", int64(128*1024*1024))
	viper.SetDefault("plugins.max_uncompressed_bytes", int64(256*1024*1024))
	viper.SetDefault("plugins.start_timeout_seconds", 15)

	// Timezone (default to Asia/Shanghai for Chinese users)
	viper.SetDefault("timezone", "Asia/Shanghai")

	// API Key auth cache
	viper.SetDefault("api_key_auth_cache.l1_size", 65535)
	viper.SetDefault("api_key_auth_cache.l1_ttl_seconds", 15)
	viper.SetDefault("api_key_auth_cache.l2_ttl_seconds", 300)
	viper.SetDefault("api_key_auth_cache.negative_ttl_seconds", 30)
	viper.SetDefault("api_key_auth_cache.jitter_percent", 10)
	viper.SetDefault("api_key_auth_cache.singleflight", true)
	viper.SetDefault("api_key_auth_cache.lookup_concurrency", 64)
	viper.SetDefault("api_key_auth_cache.invalid_abuse.enabled", true)
	viper.SetDefault("api_key_auth_cache.invalid_abuse.threshold", 120)
	viper.SetDefault("api_key_auth_cache.invalid_abuse.window_seconds", 60)
	viper.SetDefault("api_key_auth_cache.invalid_abuse.block_seconds", 60)
	viper.SetDefault("api_key_auth_cache.invalid_abuse.capacity", 16384)
	viper.SetDefault("api_key_create.max_active_per_user", 200)
	viper.SetDefault("api_key_create.max_per_user_per_hour", 60)

	// Subscription auth L1 cache
	viper.SetDefault("subscription_cache.l1_size", 16384)
	viper.SetDefault("subscription_cache.l1_ttl_seconds", 10)
	viper.SetDefault("subscription_cache.jitter_percent", 10)

	// Dashboard cache
	viper.SetDefault("dashboard_cache.enabled", true)
	viper.SetDefault("dashboard_cache.key_prefix", "sub2api:")
	viper.SetDefault("dashboard_cache.stats_fresh_ttl_seconds", 15)
	viper.SetDefault("dashboard_cache.stats_ttl_seconds", 30)
	viper.SetDefault("dashboard_cache.stats_refresh_timeout_seconds", 180)

	// Dashboard aggregation
	viper.SetDefault("dashboard_aggregation.enabled", true)
	viper.SetDefault("dashboard_aggregation.interval_seconds", 60)
	viper.SetDefault("dashboard_aggregation.lookback_seconds", 120)
	viper.SetDefault("dashboard_aggregation.backfill_enabled", false)
	viper.SetDefault("dashboard_aggregation.backfill_max_days", 31)
	viper.SetDefault("dashboard_aggregation.retention.usage_logs_days", 90)
	viper.SetDefault("dashboard_aggregation.retention.usage_billing_dedup_days", 365)
	viper.SetDefault("dashboard_aggregation.retention.hourly_days", 180)
	viper.SetDefault("dashboard_aggregation.retention.daily_days", 730)
	viper.SetDefault("dashboard_aggregation.recompute_days", 2)
	viper.SetDefault("dashboard_aggregation.dimension_rollups_enabled", false)
	viper.SetDefault("dashboard_aggregation.read_rollups_enabled", false)
	viper.SetDefault("dashboard_aggregation.rollup_staleness_seconds", 60)
	viper.SetDefault("dashboard_aggregation.backfill_batch_hours", 1)
	viper.SetDefault("dashboard_aggregation.rollup_storage_budget_gb", 30)

	// Usage cleanup task
	viper.SetDefault("usage_cleanup.enabled", true)
	viper.SetDefault("usage_cleanup.max_range_days", 31)
	viper.SetDefault("usage_cleanup.batch_size", 5000)
	viper.SetDefault("usage_cleanup.worker_interval_seconds", 10)
	viper.SetDefault("usage_cleanup.task_timeout_seconds", 1800)

	// Idempotency
	viper.SetDefault("idempotency.observe_only", true)
	viper.SetDefault("idempotency.default_ttl_seconds", 86400)
	viper.SetDefault("idempotency.system_operation_ttl_seconds", 3600)
	viper.SetDefault("idempotency.processing_timeout_seconds", 30)
	viper.SetDefault("idempotency.failed_retry_backoff_seconds", 5)
	viper.SetDefault("idempotency.max_stored_response_len", 64*1024)
	viper.SetDefault("idempotency.cleanup_interval_seconds", 60)
	viper.SetDefault("idempotency.cleanup_batch_size", 500)

	// Gateway
	viper.SetDefault("gateway.response_header_timeout", 600) // 600秒(10分钟)等待上游响应头，LLM高负载时可能排队较久
	viper.SetDefault("gateway.api_key_queue.max_waiting", 0)
	viper.SetDefault("gateway.api_key_queue.timeout_seconds", 30)
	viper.SetDefault("gateway.gemini_cached_content_max_ttl_seconds", 86400)
	viper.SetDefault("gateway.openai_response_header_timeout", 0)
	viper.SetDefault("gateway.disable_openai_transport_error_cooldown", false)
	viper.SetDefault("gateway.grok_response_header_timeout", 120)
	viper.SetDefault("gateway.openai_first_output_timeout_seconds", 0)
	viper.SetDefault("gateway.openai_high_effort_first_output_timeout_seconds", 0)
	viper.SetDefault("gateway.log_upstream_error_body", true)
	viper.SetDefault("gateway.log_upstream_error_body_max_bytes", 2048)
	viper.SetDefault("gateway.inject_beta_for_apikey", false)
	viper.SetDefault("gateway.failover_on_400", false)
	viper.SetDefault("gateway.max_account_switches", 10)
	viper.SetDefault("gateway.max_account_switches_gemini", 3)
	viper.SetDefault("gateway.account_circuit_breaker.enabled", true)
	viper.SetDefault("gateway.account_circuit_breaker.window_seconds", 300)
	viper.SetDefault("gateway.account_circuit_breaker.error_rate", 0.95)
	viper.SetDefault("gateway.account_circuit_breaker.min_requests", 20)
	viper.SetDefault("gateway.account_circuit_breaker.cooldown_seconds", 60)
	viper.SetDefault("gateway.account_circuit_breaker.backoff_enabled", false)
	viper.SetDefault("gateway.account_circuit_breaker.max_cooldown_seconds", 300)
	viper.SetDefault("gateway.account_circuit_breaker.valve_min_accounts", 3)
	viper.SetDefault("gateway.account_circuit_breaker.fast_trip_min_requests", 100)
	viper.SetDefault("gateway.retry_guard.max_attempts", 30)
	viper.SetDefault("gateway.retry_guard.max_elapsed_seconds", 120)
	viper.SetDefault("gateway.retry_guard.backoff_base_ms", 100)
	viper.SetDefault("gateway.retry_guard.backoff_max_ms", 1000)
	viper.SetDefault("gateway.retry_guard.retry_after_enabled", true)
	viper.SetDefault("gateway.retry_guard.retry_after_min_seconds", 5)
	viper.SetDefault("gateway.retry_guard.retry_after_max_seconds", 15)
	viper.SetDefault("gateway.force_codex_cli", false)
	viper.SetDefault("gateway.codex_quota_overdraft_enabled", true)
	viper.SetDefault("gateway.codex_quota_overdraft_business_injection_enabled", false)
	viper.SetDefault("gateway.disable_codex_identity_enforcement", false)
	viper.SetDefault("gateway.disable_codex_originator_normalization", false)
	viper.SetDefault("gateway.codex_image_generation_bridge_enabled", false)
	viper.SetDefault("gateway.openai_passthrough_allow_timeout_headers", false)
	viper.SetDefault("gateway.openai_compact_model", "gpt-5.4")
	viper.SetDefault("gateway.openai_codex_ticket.enabled", false)
	viper.SetDefault("gateway.openai_codex_ticket.target_length", 292)
	viper.SetDefault("gateway.openai_codex_ticket.ttl_seconds", 3600)
	viper.SetDefault("gateway.openai_codex_ticket.refresh_before_seconds", 600)
	viper.SetDefault("gateway.openai_codex_ticket.harvest_proxy_url", "")
	viper.SetDefault("gateway.openai_codex_ticket.harvest_probe_interval_seconds", 6)
	viper.SetDefault("gateway.openai_codex_ticket.harvest_attempt_timeout_seconds", 25)
	viper.SetDefault("gateway.openai_codex_ticket.fail_closed", true)
	viper.SetDefault("gateway.openai_codex_ticket.models", []string{"gpt-6-astra", "gpt-5.6-sol"})
	viper.SetDefault("gateway.live.max_session_duration_seconds", 3600)
	// OpenAI Responses WebSocket（默认开启；可通过 force_http 紧急回滚）
	viper.SetDefault("gateway.openai_ws.enabled", true)
	viper.SetDefault("gateway.openai_ws.mode_router_v2_enabled", false)
	viper.SetDefault("gateway.openai_ws.ingress_mode_default", "ctx_pool")
	viper.SetDefault("gateway.openai_ws.client_first_message_timeout_seconds", DefaultOpenAIWSClientFirstMessageTimeoutSeconds)
	viper.SetDefault("gateway.openai_ws.ingress_inter_turn_idle_timeout_seconds", 300)
	viper.SetDefault("gateway.openai_ws.max_ingress_connections_per_api_key", 64)
	viper.SetDefault("gateway.openai_ws.oauth_enabled", true)
	viper.SetDefault("gateway.openai_ws.apikey_enabled", true)
	viper.SetDefault("gateway.openai_ws.force_http", false)
	viper.SetDefault("gateway.openai_ws.allow_store_recovery", false)
	viper.SetDefault("gateway.openai_ws.ingress_previous_response_recovery_enabled", true)
	viper.SetDefault("gateway.openai_ws.store_disabled_conn_mode", "strict")
	viper.SetDefault("gateway.openai_ws.store_disabled_force_new_conn", true)
	viper.SetDefault("gateway.openai_ws.prewarm_generate_enabled", false)
	viper.SetDefault("gateway.openai_ws.client_read_limit_bytes", 64*1024*1024)
	viper.SetDefault("gateway.openai_ws.http_bridge_enabled", true)
	viper.SetDefault("gateway.openai_ws.http_bridge_threshold_bytes", 15*1024*1024)
	viper.SetDefault("gateway.openai_ws.responses_websockets", false)
	viper.SetDefault("gateway.openai_ws.responses_websockets_v2", true)
	viper.SetDefault("gateway.openai_ws.max_conns_per_account", 128)
	viper.SetDefault("gateway.openai_ws.min_idle_per_account", 4)
	viper.SetDefault("gateway.openai_ws.max_idle_per_account", 12)
	viper.SetDefault("gateway.openai_ws.dynamic_max_conns_by_account_concurrency_enabled", true)
	viper.SetDefault("gateway.openai_ws.oauth_max_conns_factor", 1.0)
	viper.SetDefault("gateway.openai_ws.apikey_max_conns_factor", 1.0)
	viper.SetDefault("gateway.openai_ws.dial_timeout_seconds", 10)
	viper.SetDefault("gateway.openai_ws.read_timeout_seconds", 900)
	viper.SetDefault("gateway.openai_ws.write_timeout_seconds", 120)
	viper.SetDefault("gateway.openai_ws.pool_target_utilization", 0.7)
	viper.SetDefault("gateway.openai_ws.queue_limit_per_conn", 64)
	viper.SetDefault("gateway.openai_ws.event_flush_batch_size", 1)
	viper.SetDefault("gateway.openai_ws.event_flush_interval_ms", 10)
	viper.SetDefault("gateway.openai_ws.prewarm_cooldown_ms", 300)
	viper.SetDefault("gateway.openai_ws.fallback_cooldown_seconds", 30)
	viper.SetDefault("gateway.openai_ws.retry_backoff_initial_ms", 120)
	viper.SetDefault("gateway.openai_ws.retry_backoff_max_ms", 2000)
	viper.SetDefault("gateway.openai_ws.retry_jitter_ratio", 0.2)
	viper.SetDefault("gateway.openai_ws.retry_total_budget_ms", 5000)
	viper.SetDefault("gateway.openai_ws.payload_log_sample_rate", 0.2)
	viper.SetDefault("gateway.openai_ws.lb_top_k", 7)
	viper.SetDefault("gateway.openai_ws.sticky_session_ttl_seconds", 3600)
	viper.SetDefault("gateway.openai_ws.session_hash_read_old_fallback", true)
	viper.SetDefault("gateway.openai_ws.session_hash_dual_write_old", true)
	viper.SetDefault("gateway.openai_ws.metadata_bridge_enabled", true)
	viper.SetDefault("gateway.openai_ws.sticky_response_id_ttl_seconds", 3600)
	viper.SetDefault("gateway.openai_ws.sticky_previous_response_ttl_seconds", 3600)
	viper.SetDefault("gateway.openai_ws.scheduler_score_weights.priority", 1.0)
	viper.SetDefault("gateway.openai_ws.scheduler_score_weights.load", 1.0)
	viper.SetDefault("gateway.openai_ws.scheduler_score_weights.queue", 0.7)
	viper.SetDefault("gateway.openai_ws.scheduler_score_weights.error_rate", 0.8)
	viper.SetDefault("gateway.openai_ws.scheduler_score_weights.ttft", 0.5)
	viper.SetDefault("gateway.openai_ws.scheduler_score_weights.reset", 0.0)
	viper.SetDefault("gateway.openai_ws.scheduler_score_weights.quota_headroom", 0.0)
	viper.SetDefault("gateway.openai_ws.scheduler_score_weights.upstream_cost", 0.0)
	viper.SetDefault("gateway.openai_ws.scheduler_score_weights.previous_response", 5.0)
	viper.SetDefault("gateway.openai_ws.scheduler_score_weights.session_sticky", 3.0)
	// OpenAI HTTP upstream protocol strategy
	viper.SetDefault("gateway.openai_http2.enabled", true)
	viper.SetDefault("gateway.openai_http2.allow_proxy_fallback_to_http1", true)
	viper.SetDefault("gateway.openai_http2.fallback_error_threshold", 2)
	viper.SetDefault("gateway.openai_http2.fallback_window_seconds", 60)
	viper.SetDefault("gateway.openai_http2.fallback_ttl_seconds", 600)
	viper.SetDefault("gateway.openai_proxy_stream_circuit.disabled", false)
	viper.SetDefault("gateway.openai_proxy_stream_circuit.failure_threshold", 2)
	viper.SetDefault("gateway.openai_proxy_stream_circuit.window_seconds", 60)
	viper.SetDefault("gateway.openai_proxy_stream_circuit.ttl_seconds", 600)
	// Grok free-tier local soft gate (scheduler-only; admin QueryQuota does not use this).
	// Enabled by default because free detection requires an explicit free tier marker.
	viper.SetDefault("gateway.grok.free_quota_soft_gate_enabled", true)
	viper.SetDefault("gateway.grok.password_auth_enabled", false)
	// Free soft-gate nominal limit: 500k tokens / rolling 24h (operator policy).
	viper.SetDefault("gateway.grok.free_quota_token_limit", int64(500_000))
	viper.SetDefault("gateway.grok.free_quota_soft_gate_percent", 95)
	viper.SetDefault("gateway.grok.free_quota_window_hours", 24)
	viper.SetDefault("gateway.grok.free_quota_stats_cache_seconds", 60)
	// 国产供应商余额检测（kimi/deepseek payg；zhipu 无余额端点，仅靠响应式 429/402）。
	viper.SetDefault("gateway.cn_providers.balance_check_enabled", true)
	viper.SetDefault("gateway.cn_providers.balance_threshold", 0.5)
	viper.SetDefault("gateway.cn_providers.balance_check_interval_minutes", 10)
	viper.SetDefault("gateway.image_concurrency.enabled", false)
	viper.SetDefault("gateway.image_concurrency.max_concurrent_requests", 0)
	viper.SetDefault("gateway.image_concurrency.overflow_mode", ImageConcurrencyOverflowModeReject)
	viper.SetDefault("gateway.image_concurrency.wait_timeout_seconds", 30)
	viper.SetDefault("gateway.image_concurrency.max_waiting_requests", 100)
	viper.SetDefault("gateway.antigravity_fallback_cooldown_minutes", 1)
	viper.SetDefault("gateway.antigravity_extra_retries", 10)
	viper.SetDefault("gateway.min_output_tokens", 128)
	viper.SetDefault("gateway.max_same_account_retries_gemini", 4)
	viper.SetDefault("gateway.max_body_size", int64(256*1024*1024))
	viper.SetDefault("gateway.text_max_body_size", int64(32*1024*1024))
	viper.SetDefault("gateway.upstream_response_read_max_bytes", DefaultUpstreamResponseReadMaxBytes)
	viper.SetDefault("gateway.models_list_read_max_bytes", DefaultModelsListReadMaxBytes)
	viper.SetDefault("gateway.proxy_probe_response_read_max_bytes", int64(1024*1024))
	viper.SetDefault("gateway.gemini_debug_response_headers", false)
	viper.SetDefault("gateway.connection_pool_isolation", ConnectionPoolIsolationAccountProxy)
	// HTTP 上游连接池配置（针对 5000+ 并发用户优化）
	viper.SetDefault("gateway.max_idle_conns", 2560)          // 最大空闲连接总数（高并发场景可调大）
	viper.SetDefault("gateway.max_idle_conns_per_host", 120)  // 每主机最大空闲连接（HTTP/2 场景默认）
	viper.SetDefault("gateway.max_conns_per_host", 1024)      // 每主机最大连接数（含活跃；流式/HTTP1.1 场景可调大，如 2400+）
	viper.SetDefault("gateway.idle_conn_timeout_seconds", 90) // 空闲连接超时（秒）
	viper.SetDefault("gateway.max_upstream_clients", 5000)
	viper.SetDefault("gateway.client_idle_ttl_seconds", 900)
	viper.SetDefault("gateway.concurrency_slot_ttl_minutes", 30) // 并发槽位过期时间（支持超长请求）
	viper.SetDefault("gateway.stream_data_interval_timeout", 180)
	viper.SetDefault("gateway.stream_keepalive_interval", 10)
	viper.SetDefault("gateway.image_stream_data_interval_timeout", 900)
	viper.SetDefault("gateway.image_stream_keepalive_interval", 10)
	viper.SetDefault("gateway.image_nonstream_keepalive_interval", 0)
	viper.SetDefault("gateway.max_line_size", 500*1024*1024)
	viper.SetDefault("gateway.scheduling.sticky_session_max_waiting", 3)
	viper.SetDefault("gateway.scheduling.sticky_session_wait_timeout", 120*time.Second)
	viper.SetDefault("gateway.scheduling.fallback_wait_timeout", 30*time.Second)
	viper.SetDefault("gateway.scheduling.fallback_max_waiting", 100)
	viper.SetDefault("gateway.scheduling.fallback_selection_mode", "last_used")
	viper.SetDefault("gateway.scheduling.prefer_soonest_reset", false)
	viper.SetDefault("gateway.scheduling.load_batch_enabled", true)
	viper.SetDefault("gateway.scheduling.load_batch_cache_ttl_ms", 200)
	viper.SetDefault("gateway.scheduling.snapshot_mget_chunk_size", 128)
	viper.SetDefault("gateway.scheduling.snapshot_write_chunk_size", 256)
	viper.SetDefault("gateway.scheduling.slot_cleanup_interval", 30*time.Second)
	viper.SetDefault("gateway.scheduling.db_fallback_enabled", true)
	// Scheduler snapshots are the source of truth on the request hot path.
	// The old per-request freshness SQL projection is opt-in for emergency
	// rollback only; default false is required for the 0-DB invariant.
	viper.SetDefault("gateway.scheduling.request_freshness_enabled", false)
	viper.SetDefault("gateway.scheduling.db_fallback_timeout_seconds", 0)
	viper.SetDefault("gateway.scheduling.db_fallback_max_qps", 0)
	viper.SetDefault("gateway.scheduling.outbox_poll_interval_seconds", 1)
	viper.SetDefault("gateway.scheduling.outbox_lag_warn_seconds", 5)
	viper.SetDefault("gateway.scheduling.outbox_lag_rebuild_seconds", 10)
	viper.SetDefault("gateway.scheduling.outbox_lag_rebuild_failures", 3)
	viper.SetDefault("gateway.scheduling.outbox_backlog_rebuild_rows", 10000)
	viper.SetDefault("gateway.scheduling.full_rebuild_interval_seconds", 300)
	viper.SetDefault("gateway.usage_record.worker_count", 128)
	viper.SetDefault("gateway.usage_record.queue_size", 16384)
	viper.SetDefault("gateway.usage_record.task_timeout_seconds", 5)
	// 默认 sync：队列满时由提交方内联执行（提交点在响应写出之后，不阻塞客户端）。
	// sample/drop 会在溢出时静默丢弃计费任务，造成扣费与 usage_logs 对账缺口（issue #3656），
	// 仅供显式配置的运维场景使用。
	viper.SetDefault("gateway.usage_record.overflow_policy", UsageRecordOverflowPolicySync)
	viper.SetDefault("gateway.usage_record.overflow_sample_percent", 10)
	viper.SetDefault("gateway.usage_record.auto_scale_enabled", true)
	viper.SetDefault("gateway.usage_record.auto_scale_min_workers", 128)
	viper.SetDefault("gateway.usage_record.auto_scale_max_workers", 512)
	viper.SetDefault("gateway.usage_record.auto_scale_up_queue_percent", 70)
	viper.SetDefault("gateway.usage_record.auto_scale_down_queue_percent", 15)
	viper.SetDefault("gateway.usage_record.auto_scale_up_step", 32)
	viper.SetDefault("gateway.usage_record.auto_scale_down_step", 16)
	viper.SetDefault("gateway.usage_record.auto_scale_check_interval_seconds", 3)
	viper.SetDefault("gateway.usage_record.auto_scale_cooldown_seconds", 10)
	viper.SetDefault("gateway.user_group_rate_cache_ttl_seconds", 30)
	viper.SetDefault("gateway.models_list_cache_ttl_seconds", 15)
	// TLS指纹伪装配置（默认关闭，需要账号级别单独启用）
	// 用户消息串行队列默认值
	viper.SetDefault("gateway.user_message_queue.enabled", false)
	viper.SetDefault("gateway.user_message_queue.lock_ttl_ms", 120000)
	viper.SetDefault("gateway.user_message_queue.wait_timeout_ms", 30000)
	viper.SetDefault("gateway.user_message_queue.min_delay_ms", 200)
	viper.SetDefault("gateway.user_message_queue.max_delay_ms", 2000)
	viper.SetDefault("gateway.user_message_queue.cleanup_interval_seconds", 60)

	viper.SetDefault("gateway.tls_fingerprint.enabled", true)
	viper.SetDefault("concurrency.ping_interval", 10)

	// TokenRefresh
	viper.SetDefault("token_refresh.enabled", true)
	viper.SetDefault("token_refresh.check_interval_minutes", 5)        // 每5分钟检查一次
	viper.SetDefault("token_refresh.refresh_before_expiry_hours", 0.5) // 提前30分钟刷新（适配Google 1小时token）
	viper.SetDefault("token_refresh.max_retries", 3)                   // 最多重试3次
	viper.SetDefault("token_refresh.retry_backoff_seconds", 2)         // 重试退避基础2秒
	viper.SetDefault("token_refresh.candidate_page_size", 200)
	viper.SetDefault("token_refresh.provider_concurrency", 4)
	viper.SetDefault("token_refresh.provider_qps", 2)
	viper.SetDefault("token_refresh.provider_failure_threshold", 3)
	viper.SetDefault("token_refresh.attempt_timeout_seconds", 15)
	viper.SetDefault("token_refresh.cycle_timeout_seconds", 240)

	// Gemini OAuth - configure via environment variables or config file
	// GEMINI_OAUTH_CLIENT_ID and GEMINI_OAUTH_CLIENT_SECRET
	// Default: uses Gemini CLI public credentials (set via environment)
	viper.SetDefault("gemini.oauth.client_id", "")
	viper.SetDefault("gemini.oauth.client_secret", "")
	viper.SetDefault("gemini.oauth.scopes", "")
	viper.SetDefault("gemini.quota.policy", "")

	// Subscription Maintenance (bounded queue + worker pool)
	viper.SetDefault("subscription_maintenance.worker_count", 2)
	viper.SetDefault("subscription_maintenance.queue_size", 1024)

	setEnvReachableDefaults()
}

// setEnvReachableDefaults registers zero-valued defaults for keys that are
// documented in deploy/config.example.yaml but had no default of their own.
//
// viper.Unmarshal only decodes the keys returned by AllKeys(), which unions
// SetDefault keys, config-file keys and explicitly bound BindEnv keys.
// AutomaticEnv can override a key already in that union, but it never adds one,
// and the viper_bind_struct escape hatch is compiled out (we build with
// -tags embed). So a key that lives only in the example file was unreachable by
// environment variable: the value was read from the process environment and
// then silently dropped. Deployments driven purely by env — which is what
// deploy/docker-compose.yml does — got the zero value with no warning.
//
// The values below are deliberately zero rather than the documented example
// values: an absent key already unmarshalled to the zero value, so registering
// zero keeps behavior identical while making the key addressable from the
// environment. Any subsystem that wants a richer default still applies it after
// unmarshal, exactly as before.
func setEnvReachableDefaults() {
	viper.SetDefault("gateway.forced_codex_instructions_template_file", "")
	viper.SetDefault("gateway.session_idle_timeout_minutes", 0)
	viper.SetDefault("gateway.user_message_queue.mode", "")
	viper.SetDefault("update.proxy_url", "")

	// sticky_escape_enabled is the one exception to the zero-value rule: its
	// effective default is true, applied post-unmarshal via a viper.IsSet guard.
	// Registering false would make IsSet always report true and permanently
	// disable sticky escape, so register the effective default instead. An
	// explicit false in config or env still wins.
	viper.SetDefault("gateway.openai_scheduler.sticky_escape_enabled", true)
	viper.SetDefault("gateway.openai_scheduler.sticky_escape_error_rate", 0.0)
	viper.SetDefault("gateway.openai_scheduler.sticky_escape_ttft_ms", 0)
	// DeepSeek response compat is opt-in; registering the default keeps
	// GATEWAY_DEEPSEEK_RESPONSE_COMPAT_ENABLED reachable from the environment.
	viper.SetDefault("gateway.deepseek_response_compat_enabled", false)
	viper.SetDefault("gateway.thinking_budget_rectify_enabled", false)

	// server.trusted_proxies and security.forwarded_client_ip_headers are the
	// other exception: load() distinguishes explicit configuration from absence
	// (issue #4600), and viper.IsSet also reports registered defaults, so a
	// SetDefault would make trusted proxies look permanently configured. Both
	// environment variables are parsed by hand in load() via os.LookupEnv and
	// were never silently dropped; binding them here records that reachability
	// where AllKeys() — and the env-reachability guard — can see it, without
	// affecting IsSet while the variables are absent. BindEnv only errors when
	// called without arguments.
	_ = viper.BindEnv("server.trusted_proxies", "SERVER_TRUSTED_PROXIES")
	_ = viper.BindEnv("security.forwarded_client_ip_headers", "SECURITY_FORWARDED_CLIENT_IP_HEADERS")
	// honeypot_exempt_nets is bound the same way: load() reads it by hand via
	// os.LookupEnv, and the bind exists so AllKeys() (and the env-reachability
	// guard) can see the key.
	_ = viper.BindEnv("security.honeypot_exempt_nets", "SECURITY_HONEYPOT_EXEMPT_NETS")

	// Third-party login providers. These carry client secrets and are exactly
	// the settings an operator expects to inject via the environment, but every
	// key here was previously unreachable that way.
	for _, provider := range []string{"github_oauth", "google_oauth"} {
		viper.SetDefault(provider+".enabled", false)
		viper.SetDefault(provider+".client_id", "")
		viper.SetDefault(provider+".client_secret", "")
		viper.SetDefault(provider+".authorize_url", "")
		viper.SetDefault(provider+".token_url", "")
		viper.SetDefault(provider+".userinfo_url", "")
		viper.SetDefault(provider+".emails_url", "")
		viper.SetDefault(provider+".scopes", "")
		viper.SetDefault(provider+".redirect_url", "")
		viper.SetDefault(provider+".frontend_redirect_url", "")
	}

	viper.SetDefault("dingtalk_connect.client_id", "")
	viper.SetDefault("dingtalk_connect.client_secret", "")
	viper.SetDefault("dingtalk_connect.internal_corp_id", "")
	viper.SetDefault("dingtalk_connect.redirect_url", "")
	viper.SetDefault("dingtalk_connect.bypass_registration", false)
	viper.SetDefault("dingtalk_connect.username_attribute_key", "")
	viper.SetDefault("dingtalk_connect.enable_attribute_matching", false)
	viper.SetDefault("dingtalk_connect.enable_attribute_sync", false)
	viper.SetDefault("dingtalk_connect.attribute_sync_fields", []string{})
	viper.SetDefault("dingtalk_connect.attribute_sync_overwrite_policy", "")
	viper.SetDefault("dingtalk_connect.sync_display_name", false)
	viper.SetDefault("dingtalk_connect.sync_display_name_attr_key", "")
	viper.SetDefault("dingtalk_connect.sync_display_name_attr_name", "")
	viper.SetDefault("dingtalk_connect.sync_dept", false)
	viper.SetDefault("dingtalk_connect.sync_dept_attr_key", "")
	viper.SetDefault("dingtalk_connect.sync_dept_attr_name", "")
	viper.SetDefault("dingtalk_connect.sync_corp_email", false)
	viper.SetDefault("dingtalk_connect.sync_corp_email_attr_key", "")
	viper.SetDefault("dingtalk_connect.sync_corp_email_attr_name", "")
}

func (c *Config) Validate() error {
	if c.Gateway.APIKeyQueue.MaxWaiting < 0 || c.Gateway.APIKeyQueue.MaxWaiting > 10000 || c.Gateway.APIKeyQueue.TimeoutSeconds < 0 || c.Gateway.APIKeyQueue.TimeoutSeconds > 3600 {
		return fmt.Errorf("gateway.api_key_queue must have 0..10000 waiters and 0..3600 timeout seconds")
	}
	if c.Security.AccountPoolOwnerUserID < 0 {
		return fmt.Errorf("security.account_pool_owner_user_id must be non-negative")
	}
	keyRotationCutoff := strings.TrimSpace(c.Security.KeyRotationCutoff)
	if keyRotationCutoff == "" {
		keyRotationCutoff = DefaultKeyRotationCutoff
	}
	if _, err := time.Parse(time.RFC3339, keyRotationCutoff); err != nil {
		return fmt.Errorf("security.key_rotation_cutoff must be an RFC3339 timestamp: %w", err)
	}
	c.Security.KeyRotationCutoff = keyRotationCutoff
	sessionBindingMode, err := NormalizeSessionBindingMode(c.Security.SessionBindingMode)
	if err != nil {
		return err
	}
	c.Security.SessionBindingMode = sessionBindingMode
	sessionBindingOnMismatch, err := NormalizeSessionBindingOnMismatch(c.Security.SessionBindingOnMismatch)
	if err != nil {
		return err
	}
	c.Security.SessionBindingOnMismatch = sessionBindingOnMismatch
	forwardedClientIPHeaders, err := NormalizeForwardedClientIPHeaders(c.Security.ForwardedClientIPHeaders)
	if err != nil {
		return fmt.Errorf("security.forwarded_client_ip_headers: %w", err)
	}
	c.Security.ForwardedClientIPHeaders = forwardedClientIPHeaders
	c.SetForwardedClientIPSettings(c.Security.TrustForwardedIPForAPIKeyACL, forwardedClientIPHeaders)
	proxyProbeURLs, err := normalizeProxyProbeURLs(c.Security.ProxyProbe.URLs)
	if err != nil {
		return fmt.Errorf("security.proxy_probe.urls: %w", err)
	}
	c.Security.ProxyProbe.URLs = proxyProbeURLs
	if err := c.Backup.normalizeAndValidate(); err != nil {
		return err
	}
	if c.Plugins.MaxUploadBytes <= 0 || c.Plugins.MaxUploadBytes > 1024*1024*1024 {
		return fmt.Errorf("plugins.max_upload_bytes must be between 1 and 1073741824")
	}
	if c.Plugins.MaxUncompressedBytes < c.Plugins.MaxUploadBytes || c.Plugins.MaxUncompressedBytes > 2*1024*1024*1024 {
		return fmt.Errorf("plugins.max_uncompressed_bytes must be between max_upload_bytes and 2147483648")
	}
	if c.Plugins.StartTimeoutSeconds < 1 || c.Plugins.StartTimeoutSeconds > 120 {
		return fmt.Errorf("plugins.start_timeout_seconds must be between 1 and 120")
	}
	if c.Server.ReadHeaderTimeout < 1 || c.Server.ReadHeaderTimeout > 60 {
		return fmt.Errorf("server.read_header_timeout must be between 1 and 60 seconds")
	}
	if c.Server.MaxHeaderBytes < 8*1024 || c.Server.MaxHeaderBytes > 1024*1024 {
		return fmt.Errorf("server.max_header_bytes must be between 8192 and 1048576 bytes")
	}
	if c.Server.IdleTimeout <= 0 {
		return fmt.Errorf("server.idle_timeout must be positive")
	}
	if c.Server.MaxRequestBodySize < 0 {
		return fmt.Errorf("server.max_request_body_size must be non-negative")
	}
	if c.Server.H2C.Enabled {
		if c.Server.H2C.MaxConcurrentStreams == 0 {
			return fmt.Errorf("server.h2c.max_concurrent_streams must be positive")
		}
		if c.Server.H2C.IdleTimeout <= 0 {
			return fmt.Errorf("server.h2c.idle_timeout must be positive")
		}
		if c.Server.H2C.MaxReadFrameSize < 16*1024 || c.Server.H2C.MaxReadFrameSize > 16*1024*1024-1 {
			return fmt.Errorf("server.h2c.max_read_frame_size must be between 16384 and 16777215 bytes")
		}
		if c.Server.H2C.MaxUploadBufferPerConnection < 65535 {
			return fmt.Errorf("server.h2c.max_upload_buffer_per_connection must be at least 65535 bytes")
		}
		if c.Server.H2C.MaxUploadBufferPerStream <= 0 {
			return fmt.Errorf("server.h2c.max_upload_buffer_per_stream must be positive")
		}
	}
	if c.APIKeyCreate.MaxActivePerUser < 0 {
		return fmt.Errorf("api_key_create.max_active_per_user must be non-negative")
	}
	if c.APIKeyCreate.MaxPerUserPerHour < 0 {
		return fmt.Errorf("api_key_create.max_per_user_per_hour must be non-negative")
	}
	if c.APIKeyAuth.InvalidAbuse.Enabled {
		if c.APIKeyAuth.InvalidAbuse.Threshold < 10 {
			return fmt.Errorf("api_key_auth_cache.invalid_abuse.threshold must be at least 10")
		}
		if c.APIKeyAuth.InvalidAbuse.WindowSeconds < 1 || c.APIKeyAuth.InvalidAbuse.WindowSeconds > 3600 {
			return fmt.Errorf("api_key_auth_cache.invalid_abuse.window_seconds must be between 1 and 3600")
		}
		if c.APIKeyAuth.InvalidAbuse.BlockSeconds < 1 || c.APIKeyAuth.InvalidAbuse.BlockSeconds > 3600 {
			return fmt.Errorf("api_key_auth_cache.invalid_abuse.block_seconds must be between 1 and 3600")
		}
		if c.APIKeyAuth.InvalidAbuse.Capacity < 256 || c.APIKeyAuth.InvalidAbuse.Capacity > 1_000_000 {
			return fmt.Errorf("api_key_auth_cache.invalid_abuse.capacity must be between 256 and 1000000")
		}
	}
	jwtSecret := strings.TrimSpace(c.JWT.Secret)
	if jwtSecret == "" {
		return fmt.Errorf("jwt.secret is required")
	}
	// NOTE: 按 UTF-8 编码后的字节长度计算。
	// 选择 bytes 而不是 rune 计数，确保二进制/随机串的长度语义更接近“熵”而非“字符数”。
	if len([]byte(jwtSecret)) < 32 {
		return fmt.Errorf("jwt.secret must be at least 32 bytes")
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	case "":
		return fmt.Errorf("log.level is required")
	default:
		return fmt.Errorf("log.level must be one of: debug/info/warn/error")
	}
	switch c.Log.Format {
	case "json", "console":
	case "":
		return fmt.Errorf("log.format is required")
	default:
		return fmt.Errorf("log.format must be one of: json/console")
	}
	switch c.Log.StacktraceLevel {
	case "none", "error", "fatal":
	case "":
		return fmt.Errorf("log.stacktrace_level is required")
	default:
		return fmt.Errorf("log.stacktrace_level must be one of: none/error/fatal")
	}
	if !c.Log.Output.ToStdout && !c.Log.Output.ToFile {
		return fmt.Errorf("log.output.to_stdout and log.output.to_file cannot both be false")
	}
	if c.Log.Rotation.MaxSizeMB <= 0 {
		return fmt.Errorf("log.rotation.max_size_mb must be positive")
	}
	if c.Log.Rotation.MaxBackups < 0 {
		return fmt.Errorf("log.rotation.max_backups must be non-negative")
	}
	if c.Log.Rotation.MaxAgeDays < 0 {
		return fmt.Errorf("log.rotation.max_age_days must be non-negative")
	}
	if c.Log.Sampling.Enabled {
		if c.Log.Sampling.Initial <= 0 {
			return fmt.Errorf("log.sampling.initial must be positive when sampling is enabled")
		}
		if c.Log.Sampling.Thereafter <= 0 {
			return fmt.Errorf("log.sampling.thereafter must be positive when sampling is enabled")
		}
	} else {
		if c.Log.Sampling.Initial < 0 {
			return fmt.Errorf("log.sampling.initial must be non-negative")
		}
		if c.Log.Sampling.Thereafter < 0 {
			return fmt.Errorf("log.sampling.thereafter must be non-negative")
		}
	}

	if c.SubscriptionMaintenance.WorkerCount < 0 {
		return fmt.Errorf("subscription_maintenance.worker_count must be non-negative")
	}
	if c.SubscriptionMaintenance.QueueSize < 0 {
		return fmt.Errorf("subscription_maintenance.queue_size must be non-negative")
	}

	// Gemini OAuth 配置校验：client_id 与 client_secret 必须同时设置或同时留空。
	// 留空时表示使用内置的 Gemini CLI OAuth 客户端（其 client_secret 通过环境变量注入）。
	geminiClientID := strings.TrimSpace(c.Gemini.OAuth.ClientID)
	geminiClientSecret := strings.TrimSpace(c.Gemini.OAuth.ClientSecret)
	if (geminiClientID == "") != (geminiClientSecret == "") {
		return fmt.Errorf("gemini.oauth.client_id and gemini.oauth.client_secret must be both set or both empty")
	}

	if strings.TrimSpace(c.Server.FrontendURL) != "" {
		if err := ValidateAbsoluteHTTPURL(c.Server.FrontendURL); err != nil {
			return fmt.Errorf("server.frontend_url invalid: %w", err)
		}
		u, err := url.Parse(strings.TrimSpace(c.Server.FrontendURL))
		if err != nil {
			return fmt.Errorf("server.frontend_url invalid: %w", err)
		}
		if u.RawQuery != "" || u.ForceQuery {
			return fmt.Errorf("server.frontend_url invalid: must not include query")
		}
		if u.User != nil {
			return fmt.Errorf("server.frontend_url invalid: must not include userinfo")
		}
		warnIfInsecureURL("server.frontend_url", c.Server.FrontendURL)
	}
	if c.WebAuthn.Enabled {
		c.WebAuthn.RPDisplayName = strings.TrimSpace(c.WebAuthn.RPDisplayName)
		c.WebAuthn.RPID = strings.ToLower(strings.TrimSpace(c.WebAuthn.RPID))
		c.WebAuthn.RPOrigins = normalizeStringSlice(c.WebAuthn.RPOrigins)
		if c.WebAuthn.RPDisplayName == "" {
			return fmt.Errorf("webauthn.rp_display_name is required when passkeys are enabled")
		}
		if c.WebAuthn.RPID == "" {
			return fmt.Errorf("webauthn.rp_id is required when passkeys are enabled")
		}
		if strings.Contains(c.WebAuthn.RPID, "://") || strings.ContainsAny(c.WebAuthn.RPID, "/:") {
			return fmt.Errorf("webauthn.rp_id must be a domain without scheme, port, or path")
		}
		if len(c.WebAuthn.RPOrigins) == 0 {
			return fmt.Errorf("webauthn.rp_origins must contain at least one origin when passkeys are enabled")
		}
		for i, origin := range c.WebAuthn.RPOrigins {
			u, err := url.Parse(origin)
			if err != nil || u.Scheme == "" || u.Host == "" {
				return fmt.Errorf("webauthn.rp_origins contains invalid origin %q", origin)
			}
			if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
				return fmt.Errorf("webauthn.rp_origins entry %q must not include userinfo, path, query, or fragment", origin)
			}
			u.Scheme = strings.ToLower(u.Scheme)
			u.Host = strings.ToLower(u.Host)
			host := strings.ToLower(u.Hostname())
			localDevelopment := host == "localhost" || host == "127.0.0.1" || host == "::1"
			if u.Scheme != "https" && (u.Scheme != "http" || !localDevelopment) {
				return fmt.Errorf("webauthn.rp_origins entry %q must use HTTPS (HTTP is allowed only for localhost)", origin)
			}
			if host != c.WebAuthn.RPID && !strings.HasSuffix(host, "."+c.WebAuthn.RPID) {
				return fmt.Errorf("webauthn.rp_origins entry %q is not within relying party ID %q", origin, c.WebAuthn.RPID)
			}
			c.WebAuthn.RPOrigins[i] = u.Scheme + "://" + u.Host
		}
	}
	if c.JWT.ExpireHour <= 0 {
		return fmt.Errorf("jwt.expire_hour must be positive")
	}
	if c.JWT.ExpireHour > 168 {
		return fmt.Errorf("jwt.expire_hour must be <= 168 (7 days)")
	}
	if c.JWT.ExpireHour > 24 {
		slog.Warn("jwt.expire_hour is high; consider shorter expiration for security", "expire_hour", c.JWT.ExpireHour)
	}
	// JWT Refresh Token配置验证
	if c.JWT.AccessTokenExpireMinutes < 0 {
		return fmt.Errorf("jwt.access_token_expire_minutes must be non-negative")
	}
	if c.JWT.AccessTokenExpireMinutes > 720 {
		slog.Warn("jwt.access_token_expire_minutes is high; consider shorter expiration for security", "access_token_expire_minutes", c.JWT.AccessTokenExpireMinutes)
	}
	if c.JWT.RefreshTokenExpireDays <= 0 {
		return fmt.Errorf("jwt.refresh_token_expire_days must be positive")
	}
	if c.JWT.RefreshTokenExpireDays > 90 {
		slog.Warn("jwt.refresh_token_expire_days is high; it is the idle (sliding) validity of every refresh token, consider a shorter value for security", "refresh_token_expire_days", c.JWT.RefreshTokenExpireDays)
	}
	if c.JWT.SessionMaxLifetimeDays <= 0 {
		return fmt.Errorf("jwt.session_max_lifetime_days must be positive")
	}
	if c.JWT.SessionMaxLifetimeDays < c.JWT.RefreshTokenExpireDays {
		// 不让升级后启动失败：旧版本把 refresh_token_expire_days 同时当会话绝对上限，老部署可能只把它调大过
		// （例如 120 天）而不知道有 session_max_lifetime_days。会话上限不能短于一枚令牌的有效期，抬高到相等并提示。
		slog.Warn("jwt.session_max_lifetime_days is shorter than jwt.refresh_token_expire_days; raising it to match (set jwt.session_max_lifetime_days explicitly to silence this)",
			"session_max_lifetime_days", c.JWT.SessionMaxLifetimeDays, "refresh_token_expire_days", c.JWT.RefreshTokenExpireDays)
		c.JWT.SessionMaxLifetimeDays = c.JWT.RefreshTokenExpireDays
	}
	if c.JWT.SessionMaxLifetimeDays > 365 {
		slog.Warn("jwt.session_max_lifetime_days is above one year; users could stay signed in indefinitely, consider a shorter cap", "session_max_lifetime_days", c.JWT.SessionMaxLifetimeDays)
	}
	if c.JWT.RefreshTokenReuseLeewaySeconds < 0 {
		return fmt.Errorf("jwt.refresh_token_reuse_leeway_seconds must be non-negative (0 disables the leeway)")
	}
	if c.JWT.RefreshTokenReuseLeewaySeconds > MaxRefreshTokenReuseLeewaySeconds {
		return fmt.Errorf("jwt.refresh_token_reuse_leeway_seconds must be <= %d (a longer leeway would defeat refresh token replay detection); use 0 to disable", MaxRefreshTokenReuseLeewaySeconds)
	}
	if c.JWT.RefreshWindowMinutes < 0 {
		return fmt.Errorf("jwt.refresh_window_minutes must be non-negative")
	}
	if c.Security.CSP.Enabled && strings.TrimSpace(c.Security.CSP.Policy) == "" {
		return fmt.Errorf("security.csp.policy is required when CSP is enabled")
	}
	if c.LinuxDo.Enabled {
		if strings.TrimSpace(c.LinuxDo.ClientID) == "" {
			return fmt.Errorf("linuxdo_connect.client_id is required when linuxdo_connect.enabled=true")
		}
		if strings.TrimSpace(c.LinuxDo.AuthorizeURL) == "" {
			return fmt.Errorf("linuxdo_connect.authorize_url is required when linuxdo_connect.enabled=true")
		}
		if strings.TrimSpace(c.LinuxDo.TokenURL) == "" {
			return fmt.Errorf("linuxdo_connect.token_url is required when linuxdo_connect.enabled=true")
		}
		if strings.TrimSpace(c.LinuxDo.UserInfoURL) == "" {
			return fmt.Errorf("linuxdo_connect.userinfo_url is required when linuxdo_connect.enabled=true")
		}
		if strings.TrimSpace(c.LinuxDo.RedirectURL) == "" {
			return fmt.Errorf("linuxdo_connect.redirect_url is required when linuxdo_connect.enabled=true")
		}
		method := strings.ToLower(strings.TrimSpace(c.LinuxDo.TokenAuthMethod))
		switch method {
		case "", "client_secret_post", "client_secret_basic", "none":
		default:
			return fmt.Errorf("linuxdo_connect.token_auth_method must be one of: client_secret_post/client_secret_basic/none")
		}
		if (method == "" || method == "client_secret_post" || method == "client_secret_basic") &&
			strings.TrimSpace(c.LinuxDo.ClientSecret) == "" {
			return fmt.Errorf("linuxdo_connect.client_secret is required when linuxdo_connect.enabled=true and token_auth_method is client_secret_post/client_secret_basic")
		}
		if strings.TrimSpace(c.LinuxDo.FrontendRedirectURL) == "" {
			return fmt.Errorf("linuxdo_connect.frontend_redirect_url is required when linuxdo_connect.enabled=true")
		}

		if err := ValidateAbsoluteHTTPURL(c.LinuxDo.AuthorizeURL); err != nil {
			return fmt.Errorf("linuxdo_connect.authorize_url invalid: %w", err)
		}
		if err := ValidateAbsoluteHTTPURL(c.LinuxDo.TokenURL); err != nil {
			return fmt.Errorf("linuxdo_connect.token_url invalid: %w", err)
		}
		if err := ValidateAbsoluteHTTPURL(c.LinuxDo.UserInfoURL); err != nil {
			return fmt.Errorf("linuxdo_connect.userinfo_url invalid: %w", err)
		}
		if err := ValidateAbsoluteHTTPURL(c.LinuxDo.RedirectURL); err != nil {
			return fmt.Errorf("linuxdo_connect.redirect_url invalid: %w", err)
		}
		if err := ValidateFrontendRedirectURL(c.LinuxDo.FrontendRedirectURL); err != nil {
			return fmt.Errorf("linuxdo_connect.frontend_redirect_url invalid: %w", err)
		}

		warnIfInsecureURL("linuxdo_connect.authorize_url", c.LinuxDo.AuthorizeURL)
		warnIfInsecureURL("linuxdo_connect.token_url", c.LinuxDo.TokenURL)
		warnIfInsecureURL("linuxdo_connect.userinfo_url", c.LinuxDo.UserInfoURL)
		warnIfInsecureURL("linuxdo_connect.redirect_url", c.LinuxDo.RedirectURL)
		warnIfInsecureURL("linuxdo_connect.frontend_redirect_url", c.LinuxDo.FrontendRedirectURL)
	}
	if c.WeChat.Enabled {
		weChat := c.WeChat
		normalizeWeChatConnectConfig(&weChat)

		if weChat.OpenEnabled {
			if strings.TrimSpace(weChat.OpenAppID) == "" {
				return fmt.Errorf("wechat_connect.open_app_id is required when wechat_connect.open_enabled=true")
			}
			if strings.TrimSpace(weChat.OpenAppSecret) == "" {
				return fmt.Errorf("wechat_connect.open_app_secret is required when wechat_connect.open_enabled=true")
			}
		}
		if weChat.MPEnabled {
			if strings.TrimSpace(weChat.MPAppID) == "" {
				return fmt.Errorf("wechat_connect.mp_app_id is required when wechat_connect.mp_enabled=true")
			}
			if strings.TrimSpace(weChat.MPAppSecret) == "" {
				return fmt.Errorf("wechat_connect.mp_app_secret is required when wechat_connect.mp_enabled=true")
			}
		}
		if weChat.MobileEnabled {
			if strings.TrimSpace(weChat.MobileAppID) == "" {
				return fmt.Errorf("wechat_connect.mobile_app_id is required when wechat_connect.mobile_enabled=true")
			}
			if strings.TrimSpace(weChat.MobileAppSecret) == "" {
				return fmt.Errorf("wechat_connect.mobile_app_secret is required when wechat_connect.mobile_enabled=true")
			}
		}
		if v := strings.TrimSpace(weChat.RedirectURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("wechat_connect.redirect_url invalid: %w", err)
			}
			warnIfInsecureURL("wechat_connect.redirect_url", v)
		}
		if err := ValidateFrontendRedirectURL(weChat.FrontendRedirectURL); err != nil {
			return fmt.Errorf("wechat_connect.frontend_redirect_url invalid: %w", err)
		}
		warnIfInsecureURL("wechat_connect.frontend_redirect_url", weChat.FrontendRedirectURL)
	}
	if c.OIDC.Enabled {
		if strings.TrimSpace(c.OIDC.ClientID) == "" {
			return fmt.Errorf("oidc_connect.client_id is required when oidc_connect.enabled=true")
		}
		if strings.TrimSpace(c.OIDC.IssuerURL) == "" {
			return fmt.Errorf("oidc_connect.issuer_url is required when oidc_connect.enabled=true")
		}
		if strings.TrimSpace(c.OIDC.RedirectURL) == "" {
			return fmt.Errorf("oidc_connect.redirect_url is required when oidc_connect.enabled=true")
		}
		if strings.TrimSpace(c.OIDC.FrontendRedirectURL) == "" {
			return fmt.Errorf("oidc_connect.frontend_redirect_url is required when oidc_connect.enabled=true")
		}
		if !scopeContainsOpenID(c.OIDC.Scopes) {
			return fmt.Errorf("oidc_connect.scopes must contain openid")
		}

		method := strings.ToLower(strings.TrimSpace(c.OIDC.TokenAuthMethod))
		switch method {
		case "", "client_secret_post", "client_secret_basic", "none":
		default:
			return fmt.Errorf("oidc_connect.token_auth_method must be one of: client_secret_post/client_secret_basic/none")
		}
		if (method == "" || method == "client_secret_post" || method == "client_secret_basic") &&
			strings.TrimSpace(c.OIDC.ClientSecret) == "" {
			return fmt.Errorf("oidc_connect.client_secret is required when oidc_connect.enabled=true and token_auth_method is client_secret_post/client_secret_basic")
		}
		if c.OIDC.ClockSkewSeconds < 0 || c.OIDC.ClockSkewSeconds > 600 {
			return fmt.Errorf("oidc_connect.clock_skew_seconds must be between 0 and 600")
		}
		if c.OIDC.ValidateIDToken && strings.TrimSpace(c.OIDC.AllowedSigningAlgs) == "" {
			return fmt.Errorf("oidc_connect.allowed_signing_algs is required when oidc_connect.validate_id_token=true")
		}

		if err := ValidateAbsoluteHTTPURL(c.OIDC.IssuerURL); err != nil {
			return fmt.Errorf("oidc_connect.issuer_url invalid: %w", err)
		}
		if v := strings.TrimSpace(c.OIDC.DiscoveryURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("oidc_connect.discovery_url invalid: %w", err)
			}
		}
		if v := strings.TrimSpace(c.OIDC.AuthorizeURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("oidc_connect.authorize_url invalid: %w", err)
			}
		}
		if v := strings.TrimSpace(c.OIDC.TokenURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("oidc_connect.token_url invalid: %w", err)
			}
		}
		if v := strings.TrimSpace(c.OIDC.UserInfoURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("oidc_connect.userinfo_url invalid: %w", err)
			}
		}
		if v := strings.TrimSpace(c.OIDC.JWKSURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("oidc_connect.jwks_url invalid: %w", err)
			}
		}
		if err := ValidateAbsoluteHTTPURL(c.OIDC.RedirectURL); err != nil {
			return fmt.Errorf("oidc_connect.redirect_url invalid: %w", err)
		}
		if err := ValidateFrontendRedirectURL(c.OIDC.FrontendRedirectURL); err != nil {
			return fmt.Errorf("oidc_connect.frontend_redirect_url invalid: %w", err)
		}

		warnIfInsecureURL("oidc_connect.issuer_url", c.OIDC.IssuerURL)
		warnIfInsecureURL("oidc_connect.discovery_url", c.OIDC.DiscoveryURL)
		warnIfInsecureURL("oidc_connect.authorize_url", c.OIDC.AuthorizeURL)
		warnIfInsecureURL("oidc_connect.token_url", c.OIDC.TokenURL)
		warnIfInsecureURL("oidc_connect.userinfo_url", c.OIDC.UserInfoURL)
		warnIfInsecureURL("oidc_connect.jwks_url", c.OIDC.JWKSURL)
		warnIfInsecureURL("oidc_connect.redirect_url", c.OIDC.RedirectURL)
		warnIfInsecureURL("oidc_connect.frontend_redirect_url", c.OIDC.FrontendRedirectURL)
	}
	if c.Billing.CircuitBreaker.Enabled {
		if c.Billing.CircuitBreaker.FailureThreshold <= 0 {
			return fmt.Errorf("billing.circuit_breaker.failure_threshold must be positive")
		}
		if c.Billing.CircuitBreaker.ResetTimeoutSeconds <= 0 {
			return fmt.Errorf("billing.circuit_breaker.reset_timeout_seconds must be positive")
		}
		if c.Billing.CircuitBreaker.HalfOpenRequests <= 0 {
			return fmt.Errorf("billing.circuit_breaker.half_open_requests must be positive")
		}
	}
	if c.Billing.MinimumBalanceReserve < 0 {
		return fmt.Errorf("billing.minimum_balance_reserve must be non-negative")
	}
	if c.Billing.BalanceMergeSettlementMaxBacklog < 0 || c.Billing.BalanceMergeSettlementMaxBacklog > 1_000_000 {
		return fmt.Errorf("billing.balance_merge_settlement_max_backlog must be between 0 and 1000000")
	}
	if c.Billing.BalanceMergeSettlementMaxAgeSeconds < 0 || c.Billing.BalanceMergeSettlementMaxAgeSeconds > 86400 {
		return fmt.Errorf("billing.balance_merge_settlement_max_age_seconds must be between 0 and 86400")
	}
	if c.Billing.OpenAIIncompleteInputCacheReadPercent < 0 || c.Billing.OpenAIIncompleteInputCacheReadPercent > 100 {
		return fmt.Errorf("billing.openai_incomplete_input_cache_read_percent must be between 0 and 100")
	}
	if c.Billing.AnthropicIncompleteInputCacheReadPercent < 0 || c.Billing.AnthropicIncompleteInputCacheReadPercent > 100 {
		return fmt.Errorf("billing.anthropic_incomplete_input_cache_read_percent must be between 0 and 100")
	}
	if c.Billing.GeminiIncompleteInputCacheReadPercent < 0 || c.Billing.GeminiIncompleteInputCacheReadPercent > 100 {
		return fmt.Errorf("billing.gemini_incomplete_input_cache_read_percent must be between 0 and 100")
	}
	if c.Billing.InflightReservation.TTLSeconds < 0 || c.Billing.InflightReservation.DefaultMaxTokens < 0 ||
		c.Billing.InflightReservation.MaxOutputTokens < 0 || c.Billing.InflightReservation.MaxInputTokens < 0 ||
		c.Billing.InflightReservation.MaxReservationUSD < 0 {
		return fmt.Errorf("billing.inflight_reservation values must be non-negative")
	}
	if c.Database.MaxOpenConns <= 0 {
		return fmt.Errorf("database.max_open_conns must be positive")
	}
	if c.Database.MigrationMode == "" {
		c.Database.MigrationMode = DatabaseMigrationModeApply
	}
	switch c.Database.MigrationMode {
	case DatabaseMigrationModeApply, DatabaseMigrationModeValidate:
	default:
		return fmt.Errorf("database.migration_mode must be %q or %q", DatabaseMigrationModeApply, DatabaseMigrationModeValidate)
	}
	if c.Database.MaxIdleConns < 0 {
		return fmt.Errorf("database.max_idle_conns must be non-negative")
	}
	if c.Database.MaxIdleConns > c.Database.MaxOpenConns {
		return fmt.Errorf("database.max_idle_conns cannot exceed database.max_open_conns")
	}
	if c.Database.ConnMaxLifetimeMinutes < 0 {
		return fmt.Errorf("database.conn_max_lifetime_minutes must be non-negative")
	}
	if c.Database.ConnMaxIdleTimeMinutes < 0 {
		return fmt.Errorf("database.conn_max_idle_time_minutes must be non-negative")
	}
	if c.Redis.DialTimeoutSeconds <= 0 {
		return fmt.Errorf("redis.dial_timeout_seconds must be positive")
	}
	if c.Redis.ReadTimeoutSeconds <= 0 {
		return fmt.Errorf("redis.read_timeout_seconds must be positive")
	}
	if c.Redis.WriteTimeoutSeconds <= 0 {
		return fmt.Errorf("redis.write_timeout_seconds must be positive")
	}
	if c.Redis.PoolSize <= 0 {
		return fmt.Errorf("redis.pool_size must be positive")
	}
	if c.Redis.MinIdleConns < 0 {
		return fmt.Errorf("redis.min_idle_conns must be non-negative")
	}
	if c.Redis.MinIdleConns > c.Redis.PoolSize {
		return fmt.Errorf("redis.min_idle_conns cannot exceed redis.pool_size")
	}
	// max_idle_conns 不与 pool_size / min_idle_conns 做交叉校验：旧配置（例如 pool_size=32）
	// 加上新的默认值不能因此启动失败，buildRedisOptions 会把它规整到合理范围。
	if c.Redis.MaxIdleConns < 0 {
		return fmt.Errorf("redis.max_idle_conns must be non-negative")
	}
	if c.Redis.PoolTimeoutMS <= 0 {
		return fmt.Errorf("redis.pool_timeout_ms must be positive")
	}
	if c.Redis.ConnMaxIdleTimeSeconds < 0 {
		return fmt.Errorf("redis.conn_max_idle_time_seconds must be non-negative")
	}
	if c.Redis.ConnMaxLifetimeSeconds < 0 {
		return fmt.Errorf("redis.conn_max_lifetime_seconds must be non-negative")
	}
	if c.Redis.PoolStatsIntervalSeconds < 0 {
		return fmt.Errorf("redis.pool_stats_interval_seconds must be non-negative")
	}
	if c.BatchImage.QueueEnabled {
		if strings.TrimSpace(c.BatchImage.QueueReadyKey) == "" {
			return fmt.Errorf("batch_image.queue_ready_key must not be empty")
		}
		if strings.TrimSpace(c.BatchImage.QueueDelayedKey) == "" {
			return fmt.Errorf("batch_image.queue_delayed_key must not be empty")
		}
		if strings.TrimSpace(c.BatchImage.QueueActiveKey) == "" {
			return fmt.Errorf("batch_image.queue_active_key must not be empty")
		}
		if strings.TrimSpace(c.BatchImage.InflightKeyPrefix) == "" {
			return fmt.Errorf("batch_image.inflight_key_prefix must not be empty")
		}
		if strings.TrimSpace(c.BatchImage.LockKeyPrefix) == "" {
			return fmt.Errorf("batch_image.lock_key_prefix must not be empty")
		}
		if c.BatchImage.InflightTTLSeconds <= 0 {
			return fmt.Errorf("batch_image.inflight_ttl_seconds must be positive")
		}
		if c.BatchImage.JobLockTTLSeconds <= 0 {
			return fmt.Errorf("batch_image.job_lock_ttl_seconds must be positive")
		}
		if c.BatchImage.StaleActiveAfterSeconds <= 0 {
			return fmt.Errorf("batch_image.stale_active_after_seconds must be positive")
		}
		if c.BatchImage.DelayedMoveLimit <= 0 {
			return fmt.Errorf("batch_image.delayed_move_limit must be positive")
		}
		if c.BatchImage.RecoverLimit <= 0 {
			return fmt.Errorf("batch_image.recover_limit must be positive")
		}
	}
	if c.BatchImage.VertexEnabled {
		if strings.TrimSpace(c.BatchImage.VertexManagedGCSBucket) == "" {
			return fmt.Errorf("batch_image.vertex_managed_gcs_bucket must not be empty when vertex is enabled")
		}
		if strings.Contains(c.BatchImage.VertexManagedGCSBucket, "://") {
			return fmt.Errorf("batch_image.vertex_managed_gcs_bucket must be a bucket name, not a URI")
		}
		if strings.TrimSpace(c.BatchImage.VertexLocation) == "" {
			return fmt.Errorf("batch_image.vertex_location must not be empty when vertex is enabled")
		}
		if strings.TrimSpace(c.BatchImage.VertexManagedGCSPrefix) == "" {
			return fmt.Errorf("batch_image.vertex_managed_gcs_prefix must not be empty when vertex is enabled")
		}
		if !strings.Contains(c.BatchImage.VertexManagedGCSPrefix, "{batch_id}") {
			return fmt.Errorf("batch_image.vertex_managed_gcs_prefix must contain {batch_id}")
		}
		if c.BatchImage.VertexInputRetentionHours <= 0 {
			return fmt.Errorf("batch_image.vertex_input_retention_hours must be positive")
		}
		if c.BatchImage.VertexOutputRetentionHours <= 0 {
			return fmt.Errorf("batch_image.vertex_output_retention_hours must be positive")
		}
	}
	if c.Dashboard.Enabled {
		if c.Dashboard.StatsFreshTTLSeconds <= 0 {
			return fmt.Errorf("dashboard_cache.stats_fresh_ttl_seconds must be positive")
		}
		if c.Dashboard.StatsTTLSeconds <= 0 {
			return fmt.Errorf("dashboard_cache.stats_ttl_seconds must be positive")
		}
		if c.Dashboard.StatsRefreshTimeoutSeconds <= 0 {
			return fmt.Errorf("dashboard_cache.stats_refresh_timeout_seconds must be positive")
		}
		if c.Dashboard.StatsFreshTTLSeconds > c.Dashboard.StatsTTLSeconds {
			return fmt.Errorf("dashboard_cache.stats_fresh_ttl_seconds must be <= dashboard_cache.stats_ttl_seconds")
		}
	} else {
		if c.Dashboard.StatsFreshTTLSeconds < 0 {
			return fmt.Errorf("dashboard_cache.stats_fresh_ttl_seconds must be non-negative")
		}
		if c.Dashboard.StatsTTLSeconds < 0 {
			return fmt.Errorf("dashboard_cache.stats_ttl_seconds must be non-negative")
		}
		if c.Dashboard.StatsRefreshTimeoutSeconds < 0 {
			return fmt.Errorf("dashboard_cache.stats_refresh_timeout_seconds must be non-negative")
		}
	}
	if c.DashboardAgg.Enabled {
		if c.DashboardAgg.IntervalSeconds <= 0 {
			return fmt.Errorf("dashboard_aggregation.interval_seconds must be positive")
		}
		if c.DashboardAgg.LookbackSeconds < 0 {
			return fmt.Errorf("dashboard_aggregation.lookback_seconds must be non-negative")
		}
		if c.DashboardAgg.BackfillMaxDays < 0 {
			return fmt.Errorf("dashboard_aggregation.backfill_max_days must be non-negative")
		}
		if c.DashboardAgg.BackfillEnabled && c.DashboardAgg.BackfillMaxDays == 0 {
			return fmt.Errorf("dashboard_aggregation.backfill_max_days must be positive")
		}
		if c.DashboardAgg.Retention.UsageLogsDays <= 0 {
			return fmt.Errorf("dashboard_aggregation.retention.usage_logs_days must be positive")
		}
		if c.DashboardAgg.Retention.UsageBillingDedupDays <= 0 {
			return fmt.Errorf("dashboard_aggregation.retention.usage_billing_dedup_days must be positive")
		}
		if c.DashboardAgg.Retention.UsageBillingDedupDays < c.DashboardAgg.Retention.UsageLogsDays {
			return fmt.Errorf("dashboard_aggregation.retention.usage_billing_dedup_days must be greater than or equal to usage_logs_days")
		}
		if c.DashboardAgg.Retention.HourlyDays <= 0 {
			return fmt.Errorf("dashboard_aggregation.retention.hourly_days must be positive")
		}
		if c.DashboardAgg.Retention.DailyDays <= 0 {
			return fmt.Errorf("dashboard_aggregation.retention.daily_days must be positive")
		}
		if c.DashboardAgg.RecomputeDays < 0 {
			return fmt.Errorf("dashboard_aggregation.recompute_days must be non-negative")
		}
		if c.DashboardAgg.RollupStalenessSeconds <= 0 {
			return fmt.Errorf("dashboard_aggregation.rollup_staleness_seconds must be positive")
		}
		if c.DashboardAgg.BackfillBatchHours <= 0 {
			return fmt.Errorf("dashboard_aggregation.backfill_batch_hours must be positive")
		}
		if c.DashboardAgg.RollupStorageBudgetGB <= 0 {
			return fmt.Errorf("dashboard_aggregation.rollup_storage_budget_gb must be positive")
		}
	} else {
		if c.DashboardAgg.IntervalSeconds < 0 {
			return fmt.Errorf("dashboard_aggregation.interval_seconds must be non-negative")
		}
		if c.DashboardAgg.LookbackSeconds < 0 {
			return fmt.Errorf("dashboard_aggregation.lookback_seconds must be non-negative")
		}
		if c.DashboardAgg.BackfillMaxDays < 0 {
			return fmt.Errorf("dashboard_aggregation.backfill_max_days must be non-negative")
		}
		if c.DashboardAgg.Retention.UsageLogsDays < 0 {
			return fmt.Errorf("dashboard_aggregation.retention.usage_logs_days must be non-negative")
		}
		if c.DashboardAgg.Retention.UsageBillingDedupDays < 0 {
			return fmt.Errorf("dashboard_aggregation.retention.usage_billing_dedup_days must be non-negative")
		}
		if c.DashboardAgg.Retention.UsageBillingDedupDays > 0 &&
			c.DashboardAgg.Retention.UsageLogsDays > 0 &&
			c.DashboardAgg.Retention.UsageBillingDedupDays < c.DashboardAgg.Retention.UsageLogsDays {
			return fmt.Errorf("dashboard_aggregation.retention.usage_billing_dedup_days must be greater than or equal to usage_logs_days")
		}
		if c.DashboardAgg.Retention.HourlyDays < 0 {
			return fmt.Errorf("dashboard_aggregation.retention.hourly_days must be non-negative")
		}
		if c.DashboardAgg.Retention.DailyDays < 0 {
			return fmt.Errorf("dashboard_aggregation.retention.daily_days must be non-negative")
		}
		if c.DashboardAgg.RecomputeDays < 0 {
			return fmt.Errorf("dashboard_aggregation.recompute_days must be non-negative")
		}
		if c.DashboardAgg.RollupStalenessSeconds < 0 {
			return fmt.Errorf("dashboard_aggregation.rollup_staleness_seconds must be non-negative")
		}
		if c.DashboardAgg.BackfillBatchHours < 0 {
			return fmt.Errorf("dashboard_aggregation.backfill_batch_hours must be non-negative")
		}
		if c.DashboardAgg.RollupStorageBudgetGB < 0 {
			return fmt.Errorf("dashboard_aggregation.rollup_storage_budget_gb must be non-negative")
		}
	}
	if c.UsageCleanup.Enabled {
		if c.UsageCleanup.MaxRangeDays <= 0 {
			return fmt.Errorf("usage_cleanup.max_range_days must be positive")
		}
		if c.UsageCleanup.BatchSize <= 0 {
			return fmt.Errorf("usage_cleanup.batch_size must be positive")
		}
		if c.UsageCleanup.WorkerIntervalSeconds <= 0 {
			return fmt.Errorf("usage_cleanup.worker_interval_seconds must be positive")
		}
		if c.UsageCleanup.TaskTimeoutSeconds <= 0 {
			return fmt.Errorf("usage_cleanup.task_timeout_seconds must be positive")
		}
	} else {
		if c.UsageCleanup.MaxRangeDays < 0 {
			return fmt.Errorf("usage_cleanup.max_range_days must be non-negative")
		}
		if c.UsageCleanup.BatchSize < 0 {
			return fmt.Errorf("usage_cleanup.batch_size must be non-negative")
		}
		if c.UsageCleanup.WorkerIntervalSeconds < 0 {
			return fmt.Errorf("usage_cleanup.worker_interval_seconds must be non-negative")
		}
		if c.UsageCleanup.TaskTimeoutSeconds < 0 {
			return fmt.Errorf("usage_cleanup.task_timeout_seconds must be non-negative")
		}
	}
	if c.Idempotency.DefaultTTLSeconds <= 0 {
		return fmt.Errorf("idempotency.default_ttl_seconds must be positive")
	}
	if c.Idempotency.SystemOperationTTLSeconds <= 0 {
		return fmt.Errorf("idempotency.system_operation_ttl_seconds must be positive")
	}
	if c.Idempotency.ProcessingTimeoutSeconds <= 0 {
		return fmt.Errorf("idempotency.processing_timeout_seconds must be positive")
	}
	if c.Idempotency.FailedRetryBackoffSeconds <= 0 {
		return fmt.Errorf("idempotency.failed_retry_backoff_seconds must be positive")
	}
	if c.Idempotency.MaxStoredResponseLen <= 0 {
		return fmt.Errorf("idempotency.max_stored_response_len must be positive")
	}
	if c.Idempotency.CleanupIntervalSeconds <= 0 {
		return fmt.Errorf("idempotency.cleanup_interval_seconds must be positive")
	}
	if c.Idempotency.CleanupBatchSize <= 0 {
		return fmt.Errorf("idempotency.cleanup_batch_size must be positive")
	}
	if c.Gateway.MinOutputTokens < 0 {
		return fmt.Errorf("gateway.min_output_tokens must not be negative")
	}
	if c.Gateway.MinOutputTokens > 0 && c.Gateway.MinOutputTokens < 16 {
		return fmt.Errorf("gateway.min_output_tokens must be at least 16 when set")
	}
	if c.Gateway.MaxSameAccountRetriesGemini < 0 || c.Gateway.MaxSameAccountRetriesGemini > 10 {
		return fmt.Errorf("gateway.max_same_account_retries_gemini must be between 0-10")
	}
	if c.Gateway.MaxBodySize <= 0 {
		return fmt.Errorf("gateway.max_body_size must be positive")
	}
	if c.Gateway.TextMaxBodySize <= 0 || c.Gateway.TextMaxBodySize > c.Gateway.MaxBodySize {
		return fmt.Errorf("gateway.text_max_body_size must be positive and no greater than gateway.max_body_size")
	}
	if c.Gateway.UpstreamResponseReadMaxBytes <= 0 {
		return fmt.Errorf("gateway.upstream_response_read_max_bytes must be positive")
	}
	if c.Gateway.ModelsListReadMaxBytes <= 0 {
		return fmt.Errorf("gateway.models_list_read_max_bytes must be positive")
	}
	if c.Gateway.ProxyProbeResponseReadMaxBytes <= 0 {
		return fmt.Errorf("gateway.proxy_probe_response_read_max_bytes must be positive")
	}
	if c.Gateway.ResponseHeaderTimeout < 0 {
		return fmt.Errorf("gateway.response_header_timeout must be non-negative")
	}
	if c.Gateway.OpenAIResponseHeaderTimeout < 0 {
		return fmt.Errorf("gateway.openai_response_header_timeout must be non-negative")
	}
	if c.Gateway.GrokResponseHeaderTimeout < 0 || c.Gateway.GrokResponseHeaderTimeout > 1800 {
		return fmt.Errorf("gateway.grok_response_header_timeout must be between 0-1800 seconds")
	}
	if c.Gateway.OpenAIFirstOutputTimeoutSeconds < 0 || c.Gateway.OpenAIFirstOutputTimeoutSeconds > 600 ||
		(c.Gateway.OpenAIFirstOutputTimeoutSeconds > 0 && c.Gateway.OpenAIFirstOutputTimeoutSeconds < 30) {
		return fmt.Errorf("gateway.openai_first_output_timeout_seconds must be 0 or between 30-600 seconds")
	}
	if c.Gateway.OpenAIHighEffortFirstOutputTimeoutSeconds < 0 || c.Gateway.OpenAIHighEffortFirstOutputTimeoutSeconds > 1800 ||
		(c.Gateway.OpenAIHighEffortFirstOutputTimeoutSeconds > 0 && c.Gateway.OpenAIHighEffortFirstOutputTimeoutSeconds < 30) {
		return fmt.Errorf("gateway.openai_high_effort_first_output_timeout_seconds must be 0 or between 30-1800 seconds")
	}
	if c.Gateway.Live.MaxSessionDurationSeconds <= 0 {
		c.Gateway.Live.MaxSessionDurationSeconds = 3600
	}
	if strings.TrimSpace(c.Gateway.ConnectionPoolIsolation) != "" {
		switch c.Gateway.ConnectionPoolIsolation {
		case ConnectionPoolIsolationProxy, ConnectionPoolIsolationAccount, ConnectionPoolIsolationAccountProxy:
		default:
			return fmt.Errorf("gateway.connection_pool_isolation must be one of: %s/%s/%s",
				ConnectionPoolIsolationProxy, ConnectionPoolIsolationAccount, ConnectionPoolIsolationAccountProxy)
		}
	}
	if c.Gateway.ImageConcurrency.MaxConcurrentRequests < 0 {
		return fmt.Errorf("gateway.image_concurrency.max_concurrent_requests must be non-negative")
	}
	switch strings.TrimSpace(c.Gateway.ImageConcurrency.OverflowMode) {
	case "", ImageConcurrencyOverflowModeReject, ImageConcurrencyOverflowModeWait:
	default:
		return fmt.Errorf("gateway.image_concurrency.overflow_mode must be one of: %s/%s",
			ImageConcurrencyOverflowModeReject, ImageConcurrencyOverflowModeWait)
	}
	if c.Gateway.ImageConcurrency.WaitTimeoutSeconds < 0 {
		return fmt.Errorf("gateway.image_concurrency.wait_timeout_seconds must be non-negative")
	}
	if c.Gateway.ImageConcurrency.MaxWaitingRequests < 0 {
		return fmt.Errorf("gateway.image_concurrency.max_waiting_requests must be non-negative")
	}
	if c.Gateway.MaxIdleConns <= 0 {
		return fmt.Errorf("gateway.max_idle_conns must be positive")
	}
	if c.Gateway.MaxIdleConnsPerHost <= 0 {
		return fmt.Errorf("gateway.max_idle_conns_per_host must be positive")
	}
	if c.Gateway.MaxConnsPerHost < 0 {
		return fmt.Errorf("gateway.max_conns_per_host must be non-negative")
	}
	if c.Gateway.IdleConnTimeoutSeconds <= 0 {
		return fmt.Errorf("gateway.idle_conn_timeout_seconds must be positive")
	}
	if c.Gateway.IdleConnTimeoutSeconds > 180 {
		slog.Warn("gateway.idle_conn_timeout_seconds is high; consider 60-120 seconds for better connection reuse", "idle_conn_timeout_seconds", c.Gateway.IdleConnTimeoutSeconds)
	}
	if c.Gateway.MaxUpstreamClients <= 0 {
		return fmt.Errorf("gateway.max_upstream_clients must be positive")
	}
	if c.Gateway.ClientIdleTTLSeconds <= 0 {
		return fmt.Errorf("gateway.client_idle_ttl_seconds must be positive")
	}
	if c.Gateway.ConcurrencySlotTTLMinutes <= 0 {
		return fmt.Errorf("gateway.concurrency_slot_ttl_minutes must be positive")
	}
	if c.Gateway.StreamDataIntervalTimeout < 0 {
		return fmt.Errorf("gateway.stream_data_interval_timeout must be non-negative")
	}
	if c.Gateway.StreamDataIntervalTimeout != 0 &&
		(c.Gateway.StreamDataIntervalTimeout < 30 || c.Gateway.StreamDataIntervalTimeout > 3600) {
		return fmt.Errorf("gateway.stream_data_interval_timeout must be 0 or between 30-3600 seconds")
	}
	if c.Gateway.StreamKeepaliveInterval < 0 {
		return fmt.Errorf("gateway.stream_keepalive_interval must be non-negative")
	}
	if c.Gateway.StreamKeepaliveInterval != 0 &&
		(c.Gateway.StreamKeepaliveInterval < 5 || c.Gateway.StreamKeepaliveInterval > 30) {
		return fmt.Errorf("gateway.stream_keepalive_interval must be 0 or between 5-30 seconds")
	}
	if c.Gateway.ImageStreamDataIntervalTimeout < 0 {
		return fmt.Errorf("gateway.image_stream_data_interval_timeout must be non-negative")
	}
	if c.Gateway.ImageStreamDataIntervalTimeout != 0 &&
		(c.Gateway.ImageStreamDataIntervalTimeout < 60 || c.Gateway.ImageStreamDataIntervalTimeout > 1800) {
		return fmt.Errorf("gateway.image_stream_data_interval_timeout must be 0 or between 60-1800 seconds")
	}
	if c.Gateway.ImageStreamKeepaliveInterval < 0 {
		return fmt.Errorf("gateway.image_stream_keepalive_interval must be non-negative")
	}
	if c.Gateway.ImageStreamKeepaliveInterval != 0 &&
		(c.Gateway.ImageStreamKeepaliveInterval < 5 || c.Gateway.ImageStreamKeepaliveInterval > 60) {
		return fmt.Errorf("gateway.image_stream_keepalive_interval must be 0 or between 5-60 seconds")
	}
	if c.Gateway.ImageNonstreamKeepaliveInterval < 0 {
		return fmt.Errorf("gateway.image_nonstream_keepalive_interval must be non-negative")
	}
	if c.Gateway.ImageNonstreamKeepaliveInterval != 0 &&
		(c.Gateway.ImageNonstreamKeepaliveInterval < 5 || c.Gateway.ImageNonstreamKeepaliveInterval > 60) {
		return fmt.Errorf("gateway.image_nonstream_keepalive_interval must be 0 or between 5-60 seconds")
	}
	// 兼容旧键 sticky_previous_response_ttl_seconds
	if c.Gateway.OpenAIWS.StickyResponseIDTTLSeconds <= 0 && c.Gateway.OpenAIWS.StickyPreviousResponseTTLSeconds > 0 {
		c.Gateway.OpenAIWS.StickyResponseIDTTLSeconds = c.Gateway.OpenAIWS.StickyPreviousResponseTTLSeconds
	}
	if c.Gateway.OpenAIWS.MaxConnsPerAccount <= 0 {
		return fmt.Errorf("gateway.openai_ws.max_conns_per_account must be positive")
	}
	if c.Gateway.OpenAIWS.ClientFirstMessageTimeoutSeconds <= 0 {
		return fmt.Errorf("gateway.openai_ws.client_first_message_timeout_seconds must be positive")
	}
	if c.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds < 0 {
		return fmt.Errorf("gateway.openai_ws.ingress_inter_turn_idle_timeout_seconds must be non-negative")
	}
	if c.Gateway.OpenAIWS.MaxIngressConnectionsPerAPIKey < 0 {
		return fmt.Errorf("gateway.openai_ws.max_ingress_connections_per_api_key must be non-negative")
	}
	if c.Gateway.OpenAIWS.MinIdlePerAccount < 0 {
		return fmt.Errorf("gateway.openai_ws.min_idle_per_account must be non-negative")
	}
	if c.Gateway.OpenAIWS.MaxIdlePerAccount < 0 {
		return fmt.Errorf("gateway.openai_ws.max_idle_per_account must be non-negative")
	}
	if c.Gateway.OpenAIWS.MinIdlePerAccount > c.Gateway.OpenAIWS.MaxIdlePerAccount {
		return fmt.Errorf("gateway.openai_ws.min_idle_per_account must be <= max_idle_per_account")
	}
	if c.Gateway.OpenAIWS.MaxIdlePerAccount > c.Gateway.OpenAIWS.MaxConnsPerAccount {
		return fmt.Errorf("gateway.openai_ws.max_idle_per_account must be <= max_conns_per_account")
	}
	if c.Gateway.OpenAIWS.OAuthMaxConnsFactor <= 0 {
		return fmt.Errorf("gateway.openai_ws.oauth_max_conns_factor must be positive")
	}
	if c.Gateway.OpenAIWS.APIKeyMaxConnsFactor <= 0 {
		return fmt.Errorf("gateway.openai_ws.apikey_max_conns_factor must be positive")
	}
	if c.Gateway.OpenAIWS.DialTimeoutSeconds <= 0 {
		return fmt.Errorf("gateway.openai_ws.dial_timeout_seconds must be positive")
	}
	if c.Gateway.OpenAIWS.ReadTimeoutSeconds <= 0 {
		return fmt.Errorf("gateway.openai_ws.read_timeout_seconds must be positive")
	}
	if c.Gateway.OpenAIWS.WriteTimeoutSeconds <= 0 {
		return fmt.Errorf("gateway.openai_ws.write_timeout_seconds must be positive")
	}
	if c.Gateway.OpenAIWS.PoolTargetUtilization <= 0 || c.Gateway.OpenAIWS.PoolTargetUtilization > 1 {
		return fmt.Errorf("gateway.openai_ws.pool_target_utilization must be within (0,1]")
	}
	if c.Gateway.OpenAIWS.QueueLimitPerConn <= 0 {
		return fmt.Errorf("gateway.openai_ws.queue_limit_per_conn must be positive")
	}
	if c.Gateway.OpenAIWS.EventFlushBatchSize <= 0 {
		return fmt.Errorf("gateway.openai_ws.event_flush_batch_size must be positive")
	}
	if c.Gateway.OpenAIWS.EventFlushIntervalMS < 0 {
		return fmt.Errorf("gateway.openai_ws.event_flush_interval_ms must be non-negative")
	}
	if c.Gateway.OpenAIWS.PrewarmCooldownMS < 0 {
		return fmt.Errorf("gateway.openai_ws.prewarm_cooldown_ms must be non-negative")
	}
	if c.Gateway.OpenAIWS.ClientReadLimitBytes <= 0 {
		return fmt.Errorf("gateway.openai_ws.client_read_limit_bytes must be positive")
	}
	if c.Gateway.OpenAIWS.HTTPBridgeThresholdBytes < 0 {
		return fmt.Errorf("gateway.openai_ws.http_bridge_threshold_bytes must be non-negative")
	}
	if c.Gateway.OpenAIWS.HTTPBridgeEnabled && c.Gateway.OpenAIWS.HTTPBridgeThresholdBytes == 0 {
		return fmt.Errorf("gateway.openai_ws.http_bridge_threshold_bytes must be positive when http_bridge_enabled is true")
	}
	if c.Gateway.OpenAIWS.FallbackCooldownSeconds < 0 {
		return fmt.Errorf("gateway.openai_ws.fallback_cooldown_seconds must be non-negative")
	}
	if c.Gateway.OpenAIWS.RetryBackoffInitialMS < 0 {
		return fmt.Errorf("gateway.openai_ws.retry_backoff_initial_ms must be non-negative")
	}
	if c.Gateway.OpenAIWS.RetryBackoffMaxMS < 0 {
		return fmt.Errorf("gateway.openai_ws.retry_backoff_max_ms must be non-negative")
	}
	if c.Gateway.OpenAIWS.RetryBackoffInitialMS > 0 && c.Gateway.OpenAIWS.RetryBackoffMaxMS > 0 &&
		c.Gateway.OpenAIWS.RetryBackoffMaxMS < c.Gateway.OpenAIWS.RetryBackoffInitialMS {
		return fmt.Errorf("gateway.openai_ws.retry_backoff_max_ms must be >= retry_backoff_initial_ms")
	}
	if c.Gateway.OpenAIWS.RetryJitterRatio < 0 || c.Gateway.OpenAIWS.RetryJitterRatio > 1 {
		return fmt.Errorf("gateway.openai_ws.retry_jitter_ratio must be within [0,1]")
	}
	if c.Gateway.OpenAIWS.RetryTotalBudgetMS < 0 {
		return fmt.Errorf("gateway.openai_ws.retry_total_budget_ms must be non-negative")
	}
	if mode := strings.ToLower(strings.TrimSpace(c.Gateway.OpenAIWS.IngressModeDefault)); mode != "" {
		switch mode {
		case "off", "ctx_pool", "passthrough", "http_bridge":
		case "shared", "dedicated":
			slog.Warn("gateway.openai_ws.ingress_mode_default is deprecated, treating as ctx_pool; please update to off|ctx_pool|passthrough|http_bridge", "value", mode)
		default:
			return fmt.Errorf("gateway.openai_ws.ingress_mode_default must be one of off|ctx_pool|passthrough|http_bridge")
		}
	}
	if mode := strings.ToLower(strings.TrimSpace(c.Gateway.OpenAIWS.StoreDisabledConnMode)); mode != "" {
		switch mode {
		case "strict", "adaptive", "off":
		default:
			return fmt.Errorf("gateway.openai_ws.store_disabled_conn_mode must be one of strict|adaptive|off")
		}
	}
	if c.Gateway.OpenAIWS.PayloadLogSampleRate < 0 || c.Gateway.OpenAIWS.PayloadLogSampleRate > 1 {
		return fmt.Errorf("gateway.openai_ws.payload_log_sample_rate must be within [0,1]")
	}
	if c.Gateway.OpenAIWS.LBTopK <= 0 {
		return fmt.Errorf("gateway.openai_ws.lb_top_k must be positive")
	}
	if c.Gateway.OpenAIWS.StickySessionTTLSeconds <= 0 {
		return fmt.Errorf("gateway.openai_ws.sticky_session_ttl_seconds must be positive")
	}
	if c.Gateway.OpenAIWS.StickyResponseIDTTLSeconds <= 0 {
		return fmt.Errorf("gateway.openai_ws.sticky_response_id_ttl_seconds must be positive")
	}
	if c.Gateway.OpenAIWS.StickyPreviousResponseTTLSeconds < 0 {
		return fmt.Errorf("gateway.openai_ws.sticky_previous_response_ttl_seconds must be non-negative")
	}
	if c.Gateway.OpenAIHTTP2.FallbackErrorThreshold < 0 {
		return fmt.Errorf("gateway.openai_http2.fallback_error_threshold must be non-negative")
	}
	if c.Gateway.OpenAIHTTP2.FallbackWindowSeconds < 0 {
		return fmt.Errorf("gateway.openai_http2.fallback_window_seconds must be non-negative")
	}
	if c.Gateway.OpenAIHTTP2.FallbackTTLSeconds < 0 {
		return fmt.Errorf("gateway.openai_http2.fallback_ttl_seconds must be non-negative")
	}
	if c.Gateway.OpenAIProxyStreamCircuit.FailureThreshold < 0 {
		return fmt.Errorf("gateway.openai_proxy_stream_circuit.failure_threshold must be non-negative")
	}
	if c.Gateway.OpenAIProxyStreamCircuit.WindowSeconds < 0 {
		return fmt.Errorf("gateway.openai_proxy_stream_circuit.window_seconds must be non-negative")
	}
	if c.Gateway.OpenAIProxyStreamCircuit.TTLSeconds < 0 {
		return fmt.Errorf("gateway.openai_proxy_stream_circuit.ttl_seconds must be non-negative")
	}
	weights := c.Gateway.OpenAIWS.SchedulerScoreWeights
	for _, weight := range []float64{
		weights.Priority, weights.Load, weights.Queue, weights.ErrorRate, weights.TTFT,
		weights.Reset, weights.QuotaHeadroom, weights.UpstreamCost,
		weights.PreviousResponse, weights.SessionSticky,
	} {
		if weight < 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
			return fmt.Errorf("gateway.openai_ws.scheduler_score_weights.* must be non-negative and finite")
		}
	}
	weightSum := weights.BaseWeightSum()
	if weightSum <= 0 {
		return fmt.Errorf("gateway.openai_ws.scheduler_score_weights must not all be zero")
	}
	if math.IsNaN(weightSum) || math.IsInf(weightSum, 0) {
		return fmt.Errorf("gateway.openai_ws.scheduler_score_weights base-weight sum must be finite")
	}
	if totalWeightSum := weights.TotalWeightSum(); math.IsNaN(totalWeightSum) || math.IsInf(totalWeightSum, 0) {
		return fmt.Errorf("gateway.openai_ws.scheduler_score_weights total-weight sum must be finite")
	}
	if c.Gateway.OpenAIScheduler.StickyEscapeTTFTMs <= 0 {
		return fmt.Errorf("gateway.openai_scheduler.sticky_escape_ttft_ms must be positive")
	}
	if c.Gateway.OpenAIScheduler.StickyEscapeErrorRate < 0 || c.Gateway.OpenAIScheduler.StickyEscapeErrorRate > 1 {
		return fmt.Errorf("gateway.openai_scheduler.sticky_escape_error_rate must be between 0 and 1")
	}
	if c.Gateway.MaxLineSize < 0 {
		return fmt.Errorf("gateway.max_line_size must be non-negative")
	}
	if c.Gateway.MaxLineSize != 0 && c.Gateway.MaxLineSize < 1024*1024 {
		return fmt.Errorf("gateway.max_line_size must be at least 1MB")
	}
	if c.Gateway.UsageRecord.WorkerCount <= 0 {
		return fmt.Errorf("gateway.usage_record.worker_count must be positive")
	}
	if c.Gateway.UsageRecord.QueueSize <= 0 {
		return fmt.Errorf("gateway.usage_record.queue_size must be positive")
	}
	if c.Gateway.UsageRecord.TaskTimeoutSeconds <= 0 {
		return fmt.Errorf("gateway.usage_record.task_timeout_seconds must be positive")
	}
	switch strings.ToLower(strings.TrimSpace(c.Gateway.UsageRecord.OverflowPolicy)) {
	case UsageRecordOverflowPolicyDrop, UsageRecordOverflowPolicySample, UsageRecordOverflowPolicySync:
	default:
		return fmt.Errorf("gateway.usage_record.overflow_policy must be one of: %s/%s/%s",
			UsageRecordOverflowPolicyDrop, UsageRecordOverflowPolicySample, UsageRecordOverflowPolicySync)
	}
	if c.Gateway.UsageRecord.OverflowSamplePercent < 0 || c.Gateway.UsageRecord.OverflowSamplePercent > 100 {
		return fmt.Errorf("gateway.usage_record.overflow_sample_percent must be between 0-100")
	}
	if strings.EqualFold(strings.TrimSpace(c.Gateway.UsageRecord.OverflowPolicy), UsageRecordOverflowPolicySample) &&
		c.Gateway.UsageRecord.OverflowSamplePercent <= 0 {
		return fmt.Errorf("gateway.usage_record.overflow_sample_percent must be positive when overflow_policy=sample")
	}
	if c.Gateway.UsageRecord.AutoScaleEnabled {
		if c.Gateway.UsageRecord.AutoScaleMinWorkers <= 0 {
			return fmt.Errorf("gateway.usage_record.auto_scale_min_workers must be positive")
		}
		if c.Gateway.UsageRecord.AutoScaleMaxWorkers <= 0 {
			return fmt.Errorf("gateway.usage_record.auto_scale_max_workers must be positive")
		}
		if c.Gateway.UsageRecord.AutoScaleMaxWorkers < c.Gateway.UsageRecord.AutoScaleMinWorkers {
			return fmt.Errorf("gateway.usage_record.auto_scale_max_workers must be >= auto_scale_min_workers")
		}
		if c.Gateway.UsageRecord.WorkerCount < c.Gateway.UsageRecord.AutoScaleMinWorkers ||
			c.Gateway.UsageRecord.WorkerCount > c.Gateway.UsageRecord.AutoScaleMaxWorkers {
			return fmt.Errorf("gateway.usage_record.worker_count must be between auto_scale_min_workers and auto_scale_max_workers")
		}
		if c.Gateway.UsageRecord.AutoScaleUpQueuePercent <= 0 || c.Gateway.UsageRecord.AutoScaleUpQueuePercent > 100 {
			return fmt.Errorf("gateway.usage_record.auto_scale_up_queue_percent must be between 1-100")
		}
		if c.Gateway.UsageRecord.AutoScaleDownQueuePercent < 0 || c.Gateway.UsageRecord.AutoScaleDownQueuePercent >= 100 {
			return fmt.Errorf("gateway.usage_record.auto_scale_down_queue_percent must be between 0-99")
		}
		if c.Gateway.UsageRecord.AutoScaleDownQueuePercent >= c.Gateway.UsageRecord.AutoScaleUpQueuePercent {
			return fmt.Errorf("gateway.usage_record.auto_scale_down_queue_percent must be less than auto_scale_up_queue_percent")
		}
		if c.Gateway.UsageRecord.AutoScaleUpStep <= 0 {
			return fmt.Errorf("gateway.usage_record.auto_scale_up_step must be positive")
		}
		if c.Gateway.UsageRecord.AutoScaleDownStep <= 0 {
			return fmt.Errorf("gateway.usage_record.auto_scale_down_step must be positive")
		}
		if c.Gateway.UsageRecord.AutoScaleCheckIntervalSeconds <= 0 {
			return fmt.Errorf("gateway.usage_record.auto_scale_check_interval_seconds must be positive")
		}
		if c.Gateway.UsageRecord.AutoScaleCooldownSeconds < 0 {
			return fmt.Errorf("gateway.usage_record.auto_scale_cooldown_seconds must be non-negative")
		}
	}
	if c.Gateway.UserGroupRateCacheTTLSeconds <= 0 {
		return fmt.Errorf("gateway.user_group_rate_cache_ttl_seconds must be positive")
	}
	if c.Gateway.ModelsListCacheTTLSeconds < 10 || c.Gateway.ModelsListCacheTTLSeconds > 30 {
		return fmt.Errorf("gateway.models_list_cache_ttl_seconds must be between 10-30")
	}
	if c.Gateway.Scheduling.StickySessionMaxWaiting <= 0 {
		return fmt.Errorf("gateway.scheduling.sticky_session_max_waiting must be positive")
	}
	if c.Gateway.Scheduling.StickySessionWaitTimeout <= 0 {
		return fmt.Errorf("gateway.scheduling.sticky_session_wait_timeout must be positive")
	}
	if c.Gateway.Scheduling.FallbackWaitTimeout <= 0 {
		return fmt.Errorf("gateway.scheduling.fallback_wait_timeout must be positive")
	}
	if c.Gateway.Scheduling.FallbackMaxWaiting <= 0 {
		return fmt.Errorf("gateway.scheduling.fallback_max_waiting must be positive")
	}
	if c.Gateway.Scheduling.LoadBatchCacheTTLMS < 0 {
		return fmt.Errorf("gateway.scheduling.load_batch_cache_ttl_ms must be non-negative")
	}
	if c.Gateway.Scheduling.SnapshotMGetChunkSize <= 0 {
		return fmt.Errorf("gateway.scheduling.snapshot_mget_chunk_size must be positive")
	}
	if c.Gateway.Scheduling.SnapshotWriteChunkSize <= 0 {
		return fmt.Errorf("gateway.scheduling.snapshot_write_chunk_size must be positive")
	}
	if c.Gateway.Scheduling.SlotCleanupInterval < 0 {
		return fmt.Errorf("gateway.scheduling.slot_cleanup_interval must be non-negative")
	}
	if c.Gateway.Scheduling.DbFallbackTimeoutSeconds < 0 {
		return fmt.Errorf("gateway.scheduling.db_fallback_timeout_seconds must be non-negative")
	}
	if c.Gateway.Scheduling.DbFallbackMaxQPS < 0 {
		return fmt.Errorf("gateway.scheduling.db_fallback_max_qps must be non-negative")
	}
	if c.Gateway.Scheduling.OutboxPollIntervalSeconds <= 0 {
		return fmt.Errorf("gateway.scheduling.outbox_poll_interval_seconds must be positive")
	}
	if c.Gateway.Scheduling.OutboxLagWarnSeconds < 0 {
		return fmt.Errorf("gateway.scheduling.outbox_lag_warn_seconds must be non-negative")
	}
	if c.Gateway.Scheduling.OutboxLagRebuildSeconds < 0 {
		return fmt.Errorf("gateway.scheduling.outbox_lag_rebuild_seconds must be non-negative")
	}
	if c.Gateway.Scheduling.OutboxLagRebuildFailures <= 0 {
		return fmt.Errorf("gateway.scheduling.outbox_lag_rebuild_failures must be positive")
	}
	if c.Gateway.Scheduling.OutboxBacklogRebuildRows < 0 {
		return fmt.Errorf("gateway.scheduling.outbox_backlog_rebuild_rows must be non-negative")
	}
	if c.Gateway.Scheduling.FullRebuildIntervalSeconds < 0 {
		return fmt.Errorf("gateway.scheduling.full_rebuild_interval_seconds must be non-negative")
	}
	if c.Gateway.Scheduling.OutboxLagWarnSeconds > 0 &&
		c.Gateway.Scheduling.OutboxLagRebuildSeconds > 0 &&
		c.Gateway.Scheduling.OutboxLagRebuildSeconds < c.Gateway.Scheduling.OutboxLagWarnSeconds {
		return fmt.Errorf("gateway.scheduling.outbox_lag_rebuild_seconds must be >= outbox_lag_warn_seconds")
	}
	if c.Ops.MetricsCollectorCache.TTL < 0 {
		return fmt.Errorf("ops.metrics_collector_cache.ttl must be non-negative")
	}
	if c.UsageStatsCache.TTL < 0 {
		return fmt.Errorf("usage_stats_cache.ttl must be non-negative")
	}
	if c.UsageStatsCache.MaxConcurrentLoads < 0 {
		return fmt.Errorf("usage_stats_cache.max_concurrent_loads must be non-negative")
	}
	if c.UsageStatsCache.LoadQueueTimeout < 0 {
		return fmt.Errorf("usage_stats_cache.load_queue_timeout must be non-negative")
	}
	if c.Ops.Cleanup.ErrorLogRetentionDays < 0 {
		return fmt.Errorf("ops.cleanup.error_log_retention_days must be non-negative")
	}
	if c.Ops.Cleanup.Enabled && c.Ops.Cleanup.SystemLogRetentionDays <= 0 {
		return fmt.Errorf("ops.cleanup.system_log_retention_days must be positive when ops cleanup is enabled")
	}
	if c.Ops.Cleanup.MinuteMetricsRetentionDays < 0 {
		return fmt.Errorf("ops.cleanup.minute_metrics_retention_days must be non-negative")
	}
	if c.Ops.Cleanup.HourlyMetricsRetentionDays < 0 {
		return fmt.Errorf("ops.cleanup.hourly_metrics_retention_days must be non-negative")
	}
	if c.Ops.Cleanup.Enabled && strings.TrimSpace(c.Ops.Cleanup.Schedule) == "" {
		return fmt.Errorf("ops.cleanup.schedule is required when ops.cleanup.enabled=true")
	}
	if c.Concurrency.PingInterval < 5 || c.Concurrency.PingInterval > 30 {
		return fmt.Errorf("concurrency.ping_interval must be between 5-30 seconds")
	}
	if c.Gateway.Grok.FreeQuotaSoftGateEnabled {
		if c.Gateway.Grok.FreeQuotaTokenLimit <= 0 {
			return fmt.Errorf("gateway.grok.free_quota_token_limit must be positive")
		}
		if c.Gateway.Grok.FreeQuotaSoftGatePercent < 1 || c.Gateway.Grok.FreeQuotaSoftGatePercent > 100 {
			return fmt.Errorf("gateway.grok.free_quota_soft_gate_percent must be between 1 and 100")
		}
		if c.Gateway.Grok.FreeQuotaWindowHours <= 0 {
			return fmt.Errorf("gateway.grok.free_quota_window_hours must be positive")
		}
	}
	if c.Gateway.Grok.FreeQuotaStatsCacheSeconds < 0 {
		return fmt.Errorf("gateway.grok.free_quota_stats_cache_seconds must be non-negative")
	}
	if err := ValidateDingTalkConfig(c.DingTalk); err != nil {
		return fmt.Errorf("dingtalk_connect: %w", err)
	}
	return nil
}

func normalizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return values
	}
	normalized := make([]string, 0, len(values))
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			continue
		}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func isWeakJWTSecret(secret string) bool {
	lower := strings.ToLower(strings.TrimSpace(secret))
	if lower == "" {
		return true
	}
	weak := map[string]struct{}{
		"change-me-in-production": {},
		"changeme":                {},
		"secret":                  {},
		"password":                {},
		"123456":                  {},
		"12345678":                {},
		"admin":                   {},
		"jwt-secret":              {},
	}
	_, exists := weak[lower]
	return exists
}

func generateJWTSecret(byteLength int) (string, error) {
	if byteLength <= 0 {
		byteLength = 32
	}
	buf := make([]byte, byteLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// GetServerAddress returns the server address (host:port) from config file or environment variable.
// This is a lightweight function that can be used before full config validation,
// such as during setup wizard startup.
// Priority: environment variables > config file > defaults.
func GetServerAddress() string {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	configureConfigSource(v.SetConfigFile, v.AddConfigPath)

	// Support SERVER_HOST and SERVER_PORT environment variables
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)

	// Try to read config file (ignore errors if not found)
	_ = v.ReadInConfig()

	host := v.GetString("server.host")
	port := v.GetInt("server.port")
	return fmt.Sprintf("%s:%d", host, port)
}

// ValidateAbsoluteHTTPURL 验证是否为有效的绝对 HTTP(S) URL
func ValidateAbsoluteHTTPURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("empty url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if !u.IsAbs() {
		return fmt.Errorf("must be absolute")
	}
	if !isHTTPScheme(u.Scheme) {
		return fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
	if strings.TrimSpace(u.Host) == "" {
		return fmt.Errorf("missing host")
	}
	if u.Fragment != "" {
		return fmt.Errorf("must not include fragment")
	}
	return nil
}

// ValidateFrontendRedirectURL 验证前端重定向 URL（可以是绝对 URL 或相对路径）
func ValidateFrontendRedirectURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("empty url")
	}
	if strings.ContainsAny(raw, "\r\n") {
		return fmt.Errorf("contains invalid characters")
	}
	if strings.HasPrefix(raw, "/") {
		if strings.HasPrefix(raw, "//") {
			return fmt.Errorf("must not start with //")
		}
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if !u.IsAbs() {
		return fmt.Errorf("must be absolute http(s) url or relative path")
	}
	if !isHTTPScheme(u.Scheme) {
		return fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
	if strings.TrimSpace(u.Host) == "" {
		return fmt.Errorf("missing host")
	}
	if u.Fragment != "" {
		return fmt.Errorf("must not include fragment")
	}
	return nil
}

func scopeContainsOpenID(scopes string) bool {
	for _, scope := range strings.Fields(strings.ToLower(strings.TrimSpace(scopes))) {
		if scope == "openid" {
			return true
		}
	}
	return false
}

// isHTTPScheme 检查是否为 HTTP 或 HTTPS 协议
func isHTTPScheme(scheme string) bool {
	return strings.EqualFold(scheme, "http") || strings.EqualFold(scheme, "https")
}

func warnIfInsecureURL(field, raw string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return
	}
	if strings.EqualFold(u.Scheme, "http") {
		slog.Warn("url uses http scheme; use https in production to avoid token leakage", "field", field)
	}
}
