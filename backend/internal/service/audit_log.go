package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

// ErrAuditLogNotFound 审计日志不存在。
var ErrAuditLogNotFound = infraerrors.NotFound("AUDIT_LOG_NOT_FOUND", "audit log not found")

// 审计日志相关常量。
const (
	// AuditAuthMethodJWT / AuditAuthMethodAdminAPIKey 与 auth 中间件写入的 auth_method 对齐。
	AuditAuthMethodJWT         = "jwt"
	AuditAuthMethodAdminAPIKey = "admin_api_key"
	// AuditAuthMethodScopedAdminAPIKey 是受限管理密钥（adm_sc_）的认证方式。
	// 与旧版全权 admin_api_key 分开登记：前者是显式授权、可过期/轮换/IP 限制的
	// 自动化身份，后者是常驻全权的历史后门路径（默认拒绝）。
	AuditAuthMethodScopedAdminAPIKey = "admin_scoped_api_key"
	// AuditAuthMethodUserAPIKey 是用户个人接口密钥（uak_）的认证方式。
	// 免浏览器会话指纹限制，仅覆盖用户端非账户安全类接口，始终受独立限流与审计约束。
	AuditAuthMethodUserAPIKey = "user_api_key"
	AuditAuthMethodPasskey    = "passkey"
	// AuditAuthMethodPassword / AuditAuthMethodOAuth 用于登录事件：
	// 密码/邮箱登录与第三方（OAuth / 扫码 / OIDC / 钉钉 / 微信）登录分开检索。
	AuditAuthMethodPassword = "password"
	AuditAuthMethodOAuth    = "oauth"

	// auditRequestBodyMaxBytes 请求体脱敏后入库的最大长度（字节），超出截断。
	auditRequestBodyMaxBytes = 16 * 1024
	// AuditRequestBodyCaptureLimit 请求体参与脱敏解析的原始大小上限（字节）。
	// 审计中间件按此上限截断读取，超出的请求体仅记录占位符不解析。
	AuditRequestBodyCaptureLimit = 256 * 1024
)

// 内置审计动作名（认证/安全事件与特殊操作使用固定值，普通请求由路由自动推导）。
const (
	AuditActionLogin                  = "auth.login"
	AuditActionLogin2FA               = "auth.login.2fa"
	AuditActionRegister               = "auth.register"
	AuditActionTokenRefresh           = "auth.token.refresh"
	AuditActionSessionBindingMismatch = "auth.session_binding.mismatch"
	// AuditActionSessionBindingRebind 记录一次「刷新时指纹不一致、但只是 IP 变了所以放行并重绑」：
	// 只带粗粒度标签（浏览器家族/系统家族/IP 家族），不含 IP 与完整 UA。
	AuditActionSessionBindingRebind = "auth.session_binding.rebind"
	AuditActionStepUpVerify         = "auth.step_up.verify"
	AuditActionAuditLogClear        = "admin.audit_log.clear"
	// AuditActionAdminAuthProbeAlert is written when the admin 401/403 probe
	// detector crosses its threshold for one source IP. The row carries only
	// IP, user-agent prefix, hit paths and status-code counts — never header
	// values or credentials.
	AuditActionAdminAuthProbeAlert = "admin.auth_probe.alert"
	// AuditActionRechargeBlockedKeyRotation is written by the key-rotation gate
	// whenever it rejects a redeem-code, payment-order or crypto-deposit request.
	// It records user_id / client IP / request path so the operator can measure
	// the remediation rate after the key-leak incident.
	AuditActionRechargeBlockedKeyRotation = "user.recharge.blocked_key_rotation"
	// AuditActionKeyRotationRiskAck is written when a user explicitly acknowledges
	// the key-leak risk (KEY_ROTATION_RISK_ACK) instead of rotating keys or
	// adding IP allowlists. extra carries expires_at and the cutoff snapshot.
	AuditActionKeyRotationRiskAck = "KEY_ROTATION_RISK_ACK"
	// AuditActionRechargeAllowedRiskAck is written by the gate middleware when a
	// recharge/redeem request passes only because of a valid risk
	// acknowledgement, so the operator can reconstruct every "skipped" top-up.
	AuditActionRechargeAllowedRiskAck = "user.recharge.allowed_key_rotation_risk_ack"
	// AuditActionAPIKeyAutoDisableIPSharing is written by the gateway IP-sharing
	// risk control when it disables the keys it observed behind one shared
	// source. The affected user did not act, so the evidence lives in extra and
	// actor_user_id stays empty: this row records an automated system action.
	AuditActionAPIKeyAutoDisableIPSharing = "system.api_key.auto_disable.ip_sharing"
	// AuditActionAPIKeyRotate is written for both the single-key rotation
	// (PUT /keys/:id with regenerate_key) and the batch rotation
	// (POST /keys/batch-rotate). Single requests carry rotate=true; batch
	// requests carry batch=true plus requested/succeeded/failed counts.
	// The plaintext key never enters the audit row.
	AuditActionAPIKeyRotate = "api_key.rotate"
	// AuditActionAPIKeyUpdate 是编辑 Key（PUT /keys/:id 且未轮换）的动作名。
	// 名字沿用审计中间件按路由自动推导的值。
	// API Key 三选一身份校验（密码 / TOTP / Passkey）审计动作。
	// 创建 / 二次查看的校验结果写在主审计记录的 extra 里（api_key_verification /
	// api_key_auth_factor）；这里只固定「Passkey 校验」与「二次查看授权」两个
	// 独立动作名，便于按动作检索。密码、动态码、断言、grant 明文永远不进审计。
	AuditActionAPIKeyPasskeyVerify   = "api_key.verify.passkey"
	AuditActionAPIKeyRevealAuthorize = "api_key.reveal.authorize"
	AuditActionAPIKeyUpdate          = "api_key.update"
	// AuditActionAPIKeyCreateVerificationSet 是用户修改「创建 API Key 时是否需要验证身份」开关的动作名。
	// extra 只记因子名 / 结果 / 机器码，以及改前 / 改后的模式标签（auto / on / off），
	// 密码、动态码、断言、票据永远不进审计。
	AuditActionAPIKeyCreateVerificationSet = "api_key.create_verification.set"
	// 用户个人接口密钥（Personal API Key）生命周期与越权访问拦截审计动作：
	// 只记录脱敏前缀与事件类型，明文密钥永不入库。
	AuditActionUserPersonalAPIKeyCreate     = "user.personal_api_key.create"
	AuditActionUserPersonalAPIKeyRegenerate = "user.personal_api_key.regenerate"
	AuditActionUserPersonalAPIKeyDelete     = "user.personal_api_key.delete"
	AuditActionUserPersonalAPIKeyForbidden  = "user.personal_api_key.forbidden"

	// 第三方登录（OAuth / 扫码 / 设备登录）与 2FA 挑战的固定动作名。
	// 与 AuditAuthMethodOAuth / AuditAuthMethodPasskey 配套，让「哪个渠道、
	// 哪一步、成功还是失败」都能直接从 audit_logs 反查。
	AuditActionOAuthLoginStart   = "auth.login.oauth.start"
	AuditActionOAuthLogin        = "auth.login.oauth"
	AuditActionOAuthRegister     = "auth.register.oauth"
	AuditActionOAuthBind         = "auth.oauth.bind"
	AuditActionOAuthPending      = "auth.oauth.pending"
	AuditActionPasskeyLoginBegin = "auth.login.passkey.begin"
	AuditActionLogin2FAChallenge = "auth.login.2fa.challenge"
	// 五分钟 2FA 凭证（two-factor grant）审计动作：
	//   - grant_used：本次密码登录因为「5 分钟内已验证过 2FA」而跳过动态码；
	//   - grant_rejected：浏览器带了凭证但已失效/不匹配/存储不可用，本次退回动态码挑战。
	// 落库记录里只出现 user/ip_hash/ua_hash/剩余秒数/拒绝原因，绝不出现凭证 token。
	AuditActionLogin2FAGrantUsed     = "auth.login.2fa.grant_used"
	AuditActionLogin2FAGrantRejected = "auth.login.2fa.grant_rejected"

	// 认证事件结果（写入 audit_logs.extra.result）。
	AuditResultSuccess         = "success"
	AuditResultFailure         = "failure"
	AuditResultPending         = "pending"
	AuditResultChallengeIssued = "challenge_issued"
	AuditResultStarted         = "started"

	// 认证失败原因粗分类（写入 audit_logs.extra.reason_class）。
	AuditFailureClassOAuthState     = "oauth_state"
	AuditFailureClassOAuthParams    = "oauth_params"
	AuditFailureClassOAuthProvider  = "oauth_provider"
	AuditFailureClassOAuthToken     = "oauth_token_exchange"
	AuditFailureClassOAuthUserInfo  = "oauth_userinfo"
	AuditFailureClassOAuthEmail     = "oauth_email"
	AuditFailureClassAccountState   = "account_state"
	AuditFailureClassBackendMode    = "backend_mode"
	AuditFailureClassInvitation     = "invitation"
	AuditFailureClassTwoFactor      = "two_factor"
	AuditFailureClassPendingSession = "pending_session"
	AuditFailureClassPassword       = "password"
	AuditFailureClassRateLimit      = "rate_limit"
	AuditFailureClassInternal       = "internal_error"
	AuditFailureClassOther          = "other"
)

// AuditLog 一条管理面操作审计记录。
type AuditLog struct {
	ID               int64          `json:"id"`
	CreatedAt        time.Time      `json:"created_at"`
	ActorUserID      *int64         `json:"actor_user_id,omitempty"`
	ActorEmail       string         `json:"actor_email"`
	ActorRole        string         `json:"actor_role"`
	AuthMethod       string         `json:"auth_method"`
	CredentialMasked string         `json:"credential_masked"`
	Action           string         `json:"action"`
	Method           string         `json:"method"`
	Path             string         `json:"path"`
	RequestID        string         `json:"request_id"`
	ClientIP         string         `json:"client_ip"`
	UserAgent        string         `json:"user_agent"`
	RequestBody      string         `json:"request_body,omitempty"`
	StatusCode       int            `json:"status_code"`
	LatencyMs        int64          `json:"latency_ms"`
	Extra            map[string]any `json:"extra,omitempty"`
}

// AuditLogFilter 审计日志列表查询条件。
type AuditLogFilter struct {
	Page     int
	PageSize int

	StartTime   *time.Time
	EndTime     *time.Time
	ActorUserID *int64
	ActorEmail  string
	AuthMethod  string
	Action      string
	Method      string
	ClientIP    string
	// Success: nil 全部；true 仅 2xx/3xx；false 仅 >=400。
	Success *bool
	// Query 对 path / action / actor_email 做模糊匹配。
	Query string
}

// AuditLogList 分页结果。
type AuditLogList struct {
	Logs     []*AuditLog
	Total    int
	Page     int
	PageSize int
}

// AuditLogRepository 审计日志持久化端口。
// 注意：接口刻意不提供单条删除能力——审计日志只允许追加与全量清空。
type AuditLogRepository interface {
	BatchInsert(ctx context.Context, logs []*AuditLog) (int64, error)
	// Insert 同步写入单条（用于清空留痕等必须落库的记录）。
	Insert(ctx context.Context, log *AuditLog) error
	List(ctx context.Context, filter *AuditLogFilter) (*AuditLogList, error)
	GetByID(ctx context.Context, id int64) (*AuditLog, error)
	Count(ctx context.Context) (int64, error)
	// TruncateAll 全量清空（TRUNCATE），返回前需调用方自行 Count 记录行数。
	TruncateAll(ctx context.Context) error
	// DeleteBefore 按保留期批量删除，返回本批删除行数（幂等，可多实例并发）。
	DeleteBefore(ctx context.Context, cutoff time.Time, batchSize int) (int64, error)
}

// auditNormalizeBodyKey 归一化键名：小写并去除分隔符，
// 使 private_key / privateKey / privatekey / api-v3-key 等写法共享同一判定，
// 避免子串清单假设 snake_case 而漏掉支付渠道等无分隔符风格的密钥字段。
func auditNormalizeBodyKey(key string) string {
	var b strings.Builder
	b.Grow(len(key))
	for _, r := range strings.ToLower(strings.TrimSpace(key)) {
		switch r {
		case '_', '-', '.', ' ':
			continue
		default:
			_, _ = b.WriteRune(r)
		}
	}
	return b.String()
}

// auditBodySensitiveExactKeys 请求体脱敏的精确匹配键（归一化后）。
// 除内置清单外，程序化并入两份权威敏感表以防清单漂移：
//   - SensitiveCredentialKeys：账号 credentials 的敏感子键（session_key / service_account_json 等）
//   - providerSensitiveConfigFields：支付渠道密钥字段（pkey / privatekey / apiv3key 等）
var auditBodySensitiveExactKeys = func() map[string]struct{} {
	builtin := []string{
		"code", "codes", "pin", "cvv",
		"authorization", "cookie", "x-api-key",
		"key",
		// OAuth 一次性凭证：授权码 / 状态 / nonce / PKCE verifier 均不得入库。
		"state", "nonce", "verifier", "code_verifier", "code_challenge", "email_code", "verify_code",
		// 字符串值内嵌完整凭证的字段：
		// proxy_key 为 protocol|host|port|username|password 拼接，
		// custom_key 为用户自设的平台 API Key 明文，
		// session 为 Ollama Cloud 用量的浏览器会话 Cookie 明文。
		"proxy_key", "custom_key", "session",
		"request", CustomUsageExtraKey,
		// 备份加密配置只应传 age 公钥，但误传私钥时绝不能落入 audit_logs。
		"age_recipients",
	}
	set := make(map[string]struct{}, len(builtin)+len(SensitiveCredentialKeys)+16)
	for _, k := range builtin {
		set[auditNormalizeBodyKey(k)] = struct{}{}
	}
	for _, k := range SensitiveCredentialKeys {
		set[auditNormalizeBodyKey(k)] = struct{}{}
	}
	for _, fields := range providerSensitiveConfigFields {
		for k := range fields {
			set[auditNormalizeBodyKey(k)] = struct{}{}
		}
	}
	return set
}()

// auditBodySensitiveSubstrings 请求体脱敏的包含匹配子串（对归一化后的键名比对）。
// 命中任一子串即整体擦除该键的值（例如 new_password / secret_access_key / temp_token）。
var auditBodySensitiveSubstrings = []string{
	"password", "passwd", "secret", "token",
	"apikey", "accesskey", "privatekey",
	"otp", "credentialvalue",
	"sessionkey", "serviceaccount",
}

func isAuditSensitiveBodyKey(key string) bool {
	k := auditNormalizeBodyKey(key)
	if _, ok := auditBodySensitiveExactKeys[k]; ok {
		return true
	}
	for _, sub := range auditBodySensitiveSubstrings {
		if strings.Contains(k, sub) {
			return true
		}
	}
	return false
}

const auditRedactedPlaceholder = "***"

// RedactAuditBody 对请求体做审计入库前的脱敏：
//   - JSON：递归擦除敏感键的值（保留结构，base_url 等非敏感字段可见以便追责）
//   - 非 JSON：返回占位说明
//   - 超长：截断并附截断标记
func RedactAuditBody(raw []byte, contentType string) string {
	if len(raw) == 0 {
		return ""
	}
	if len(raw) > AuditRequestBodyCaptureLimit {
		// raw 可能已被中间件按上限截断，实际请求体只会更大，不报具体字节数。
		return "<body omitted: exceeds " + strconv.Itoa(AuditRequestBodyCaptureLimit) + " bytes>"
	}
	ct := strings.ToLower(contentType)
	if !strings.Contains(ct, "json") || !json.Valid(raw) {
		// 表单等非 JSON 内容走文本兜底脱敏后仍可能含敏感信息，直接不入库。
		return "<non-json body omitted: " + strconv.Itoa(len(raw)) + " bytes, content-type=" + strings.TrimSpace(contentType) + ">"
	}

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "<unparsable body omitted>"
	}
	redacted := redactAuditValue(value, 0)
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return "<redacted>"
	}
	out := string(encoded)
	if len(out) > auditRequestBodyMaxBytes {
		out = out[:auditRequestBodyMaxBytes] + "...<truncated>"
	}
	return out
}

const auditRedactMaxDepth = 24

func redactAuditValue(value any, depth int) any {
	if depth > auditRedactMaxDepth {
		return "<depth limit exceeded>"
	}
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			if isAuditSensitiveBodyKey(k) {
				out[k] = auditRedactedPlaceholder
				continue
			}
			out[k] = redactAuditValue(item, depth+1)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = redactAuditValue(item, depth+1)
		}
		return out
	case string:
		if strings.Contains(strings.ToUpper(v), "AGE-SECRET-KEY-") {
			return auditRedactedPlaceholder
		}
		return v
	default:
		return value
	}
}

// MaskAuditCredential 对请求头中的凭证做首尾保留掩码：
// 保留前 6 位与后 4 位，中间以 **** 表示；过短的凭证整体掩码。
func MaskAuditCredential(credential string) string {
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return ""
	}
	if len(credential) <= 14 {
		return "****"
	}
	return credential[:6] + "****" + credential[len(credential)-4:]
}

// RedactAuditQuery 对 URL query 做轻量脱敏后返回。
// auditQuerySensitiveSubstrings 需要脱敏的 query 键名片段（对归一化键名做包含匹配）。
// 认证回调里的 code / state / nonce / verifier 与 token 同级，一律擦除；
// 归一化后再比较，兼容 api_key / apiKey / x-api-key 等写法。
var auditQuerySensitiveSubstrings = []string{
	"token", "secret", "password", "passwd",
	"apikey", "accesskey", "privatekey", "key",
	"code", "state", "nonce", "verifier", "otp", "sign",
}

func isAuditSensitiveQueryKey(key string) bool {
	normalized := auditNormalizeBodyKey(key)
	if normalized == "" {
		return false
	}
	for _, sub := range auditQuerySensitiveSubstrings {
		if strings.Contains(normalized, sub) {
			return true
		}
	}
	return false
}

// RedactAuditQuery 对 query 做逐键脱敏：
//   - 敏感键（token/code/state/nonce/verifier/key/secret/otp/sign…）整值替换为 ***；
//   - 其余键保留（截断超长值），例如 redirect / aff 这类排障需要的字段。
func RedactAuditQuery(rawQuery string) string {
	rawQuery = strings.TrimSpace(rawQuery)
	if rawQuery == "" {
		return ""
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil || len(values) == 0 {
		// 解析不了就退回整串脱敏，宁可过度脱敏也不漏凭证。
		return logredact.RedactText(rawQuery,
			"api_key", "apikey", "token", "secret", "key",
			"code", "state", "nonce", "verifier", "code_verifier", "code_challenge",
		)
	}
	out := make(url.Values, len(values))
	for key, items := range values {
		if isAuditSensitiveQueryKey(key) {
			out.Set(key, "***")
			continue
		}
		for _, item := range items {
			if len(item) > 512 {
				item = item[:512] + "...<truncated>"
			}
			out.Add(key, item)
		}
	}
	return out.Encode()
}

// HashAuditEmail 把邮箱转换成不可逆的审计指纹（sha256 前 16 字节 hex）。
// 认证审计只允许记录脱敏邮箱或指纹，禁止落全量邮箱。
func HashAuditEmail(email string) string {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(normalized))
	return "sha256:" + hex.EncodeToString(sum[:16])
}

// ClassifyAuthFailure 把原始失败码（infra error reason / OAuth 错误码）归类成
// 粗粒度原因分类，便于按类检索「绕过 2FA / 撞库 / 状态失配」等攻击面。
func ClassifyAuthFailure(reason string, statusCode int) string {
	normalized := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case normalized == "":
		if statusCode == 429 {
			return AuditFailureClassRateLimit
		}
		if statusCode >= 500 {
			return AuditFailureClassInternal
		}
		return AuditFailureClassOther
	case strings.Contains(normalized, "state"), strings.Contains(normalized, "csrf"),
		strings.Contains(normalized, "browser_session"), strings.Contains(normalized, "missing_params"):
		return AuditFailureClassOAuthState
	case strings.Contains(normalized, "two_factor"), strings.Contains(normalized, "2fa"),
		strings.Contains(normalized, "totp"), strings.Contains(normalized, "step_up"):
		return AuditFailureClassTwoFactor
	case strings.Contains(normalized, "pending_auth"), strings.Contains(normalized, "pending_oauth"):
		return AuditFailureClassPendingSession
	case strings.Contains(normalized, "invitation"):
		return AuditFailureClassInvitation
	case strings.Contains(normalized, "backend_mode"):
		return AuditFailureClassBackendMode
	case strings.Contains(normalized, "user_not_active"), strings.Contains(normalized, "disabled"),
		strings.Contains(normalized, "not_active"), strings.Contains(normalized, "frozen"):
		return AuditFailureClassAccountState
	case strings.Contains(normalized, "token_exchange"), strings.Contains(normalized, "invalid_grant"):
		return AuditFailureClassOAuthToken
	case strings.Contains(normalized, "userinfo"):
		return AuditFailureClassOAuthUserInfo
	case strings.Contains(normalized, "email_unverified"), strings.Contains(normalized, "email_not_verified"),
		strings.Contains(normalized, "missing_email"):
		return AuditFailureClassOAuthEmail
	case strings.Contains(normalized, "provider_error"), strings.Contains(normalized, "not_enabled"):
		return AuditFailureClassOAuthProvider
	case strings.Contains(normalized, "rate_limit"), strings.Contains(normalized, "too_many"):
		return AuditFailureClassRateLimit
	case strings.Contains(normalized, "password"), strings.Contains(normalized, "invalid_credential"),
		strings.Contains(normalized, "credentials"):
		return AuditFailureClassPassword
	case strings.Contains(normalized, "oauth"):
		return AuditFailureClassOAuthState
	case statusCode >= 500:
		return AuditFailureClassInternal
	default:
		return AuditFailureClassOther
	}
}
