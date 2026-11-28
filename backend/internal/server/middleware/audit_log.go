package middleware

import (
	"bytes"
	"io"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AuditLogMiddleware 管理面操作审计中间件类型（用于 wire 注入区分）。
type AuditLogMiddleware gin.HandlerFunc

// 审计相关 gin context 覆写键：handler / 认证中间件可通过这些键补充审计信息。
const (
	auditCtxKeyAction     = "audit_action"
	auditCtxKeyActorID    = "audit_actor_id"
	auditCtxKeyActorEmail = "audit_actor_email"
	auditCtxKeySkip       = "audit_skip"
	// auditCtxKeyForce 让一次成功的 token 刷新也落审计（见 MarkAuditAlways）。
	auditCtxKeyForce = "audit_force"
	// auditCtxKeyRecorded 标记本请求已由审计中间件写入完整记录；
	// 认证层据此避免为同一请求再补记一条低信息量的兜底记录。
	auditCtxKeyRecorded = "audit_recorded"
	auditCtxKeyExtra    = "audit_extra"
	// ContextKeyAuthEmail 认证中间件写入的用户邮箱（审计用）。
	ContextKeyAuthEmail = "auth_email"
	// ContextKeySessionID 认证中间件写入的会话 ID（refresh token family）。
	ContextKeySessionID = "session_id"

	// 认证事件（第三方登录 / 2FA）审计标记：handler 在关键分支写入，
	// 审计中间件在请求结束后合成一条结构化记录。
	// 这样即使回调失败走的是 302 跳转，也能准确区分 success / failure / pending。
	auditCtxKeyAuthResult        = "audit_auth_result"
	auditCtxKeyAuthFailureReason = "audit_auth_failure_reason"
	auditCtxKeyAuthEmailHash     = "audit_auth_email_hash"
	auditCtxKeyAuthEmailMasked   = "audit_auth_email_masked"
	auditCtxKeyAuthProvider      = "audit_auth_provider"
	auditCtxKeyAuthFlow          = "audit_auth_flow"
	auditCtxKeyAuthRequires2FA   = "audit_auth_requires_2fa"
)

// MarkAuthAuditActor 为认证事件记录操作者：user_id 落列，邮箱只落脱敏值与指纹。
// 认证成功但尚未签发会话时（OAuth 回调）也用它把主体写进审计。
func MarkAuthAuditActor(c *gin.Context, userID int64, email string) {
	if c == nil {
		return
	}
	if userID > 0 {
		c.Set(auditCtxKeyActorID, userID)
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return
	}
	c.Set(auditCtxKeyAuthEmailHash, service.HashAuditEmail(email))
	c.Set(auditCtxKeyAuthEmailMasked, service.MaskEmail(email))
}

// MarkAuthAuditEmail 只登记被尝试账号的邮箱指纹（失败路径也要能按账号反查撞库）。
func MarkAuthAuditEmail(c *gin.Context, email string) {
	if c == nil {
		return
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return
	}
	c.Set(auditCtxKeyAuthEmailHash, service.HashAuditEmail(email))
	c.Set(auditCtxKeyAuthEmailMasked, service.MaskEmail(email))
}

// MarkAuthAuditResult 记录认证请求结果：success / failure / pending / challenge_issued / started。
func MarkAuthAuditResult(c *gin.Context, result, reason string) {
	if c == nil {
		return
	}
	if result = strings.TrimSpace(result); result != "" {
		c.Set(auditCtxKeyAuthResult, result)
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		c.Set(auditCtxKeyAuthFailureReason, reason)
	}
}

// MarkAuthAuditProvider 覆盖路由推导出的 provider（灰色渠道 / 通用 pending 兑换等场景）。
func MarkAuthAuditProvider(c *gin.Context, provider string) {
	if c == nil {
		return
	}
	if provider = strings.TrimSpace(provider); provider != "" {
		c.Set(auditCtxKeyAuthProvider, provider)
	}
}

// MarkAuthAuditRequires2FA 标记本次请求已下发 2FA 挑战（未签发会话）。
func MarkAuthAuditRequires2FA(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(auditCtxKeyAuthRequires2FA, true)
}

// SetAuditAction 允许 handler / 中间件为当前请求指定审计动作名（覆盖自动推导）。
func SetAuditAction(c *gin.Context, action string) {
	c.Set(auditCtxKeyAction, action)
}

// SetAuditActor 允许 handler 在认证上下文缺失时（如登录接口）补充操作者身份。
func SetAuditActor(c *gin.Context, userID int64, email string) {
	if userID > 0 {
		c.Set(auditCtxKeyActorID, userID)
	}
	if email != "" {
		c.Set(auditCtxKeyActorEmail, email)
	}
}

// SkipAudit 跳过当前请求的审计记录。
func SkipAudit(c *gin.Context) {
	c.Set(auditCtxKeySkip, true)
}

// MarkAuditAlways 让一次「成功的 token 刷新」也落审计：刷新成功是高频常规操作，默认只记失败；
// 但刷新时发生了指纹重绑（rebind）这种值得事后追溯的事件，handler 调这个函数把它留下。
func MarkAuditAlways(c *gin.Context) {
	c.Set(auditCtxKeyForce, true)
}

// auditExtraAllowedKeys is deliberately narrow: handlers may only attach
// scalar, non-secret operation summaries. Request bodies and arbitrary maps
// are never accepted through this channel.
var auditExtraAllowedKeys = map[string]struct{}{
	"result": {}, "error_code": {}, "enabled": {}, "blocking_enabled": {},
	"config_version": {}, "endpoint_count": {}, "scanner_count": {},
	"all_groups": {}, "group_count": {}, "guard_endpoint_id": {},
	"http_status": {}, "latency_ms": {}, "token_applied": {}, "retryable": {},
	"event_id": {}, "requested_count": {}, "deleted_events": {}, "deleted_jobs": {},
	"matched_count": {}, "snapshot_max_id": {}, "filter_hash": {}, "confirm": {},
	// 受限管理员密钥身份（认证中间件写入，用于在完整审计记录中定位具体密钥）。
	"admin_key_id": {}, "admin_key_name": {},
	// 浏览器端封存密文的操作只记录「哪把 Key」，不记录密文 / KDF 参数。
	"api_key_id": {}, "api_key_prefix": {},
	// 密钥轮换：批量请求只记计数与批标记，绝不记录任何明文 key。
	"batch": {}, "rotate": {}, "succeeded_count": {}, "failed_count": {},
	// 归集目标地址变更（仅地址，绝不含 TOTP 码或风险短语）。
	"hot_wallet_evm_old": {}, "hot_wallet_evm_new": {},
	"hot_wallet_solana_old": {}, "hot_wallet_solana_new": {},
	"changed": {}, "locked": {}, "lock_created": {},
	// 五分钟 2FA 凭证（two-factor grant）审计：只记签发/使用/拒绝、scope、
	// 拒绝原因、剩余秒数与 IP/UA 不可逆指纹；token 明文永远不进审计。
	"two_factor_grant": {}, "two_factor_grant_scope": {},
	"two_factor_grant_reason": {}, "two_factor_grant_remaining_seconds": {},
	"two_factor_grant_ip_hash": {}, "two_factor_grant_ua_hash": {},
	// 双因子策略（TOTP / Passkey）编排审计：只记录因子名与缺失因子，
	// 不记录任何凭证、动态码、断言或 challenge。
	// two_factor_satisfied_factors：step-up 放行时，是哪些已绑定因子的授权满足了它（任选其一）。
	"two_factor_grant_factor": {}, "two_factor_missing_factors": {}, "two_factor_satisfied_factors": {},
	// API Key 三选一身份校验（密码 / TOTP / Passkey）审计：只记录因子名、
	// 校验结果与失败机器码；密码 / 动态码 / 断言 / 票据 / grant 明文一律不入库。
	"api_key_auth_factor": {}, "api_key_verification": {}, "api_key_verification_reason": {},
	"two_factor_required_factors": {}, "two_factor_next_factor": {},
	// 管理面出站目标（审计节点 / 账号 base_url）变更：只记地址与显式开关，
	// 不记 token / 密码。
	"allow_private_endpoints": {}, "account_base_url_old": {}, "account_base_url_new": {},
	"account_api_base_urls_changed": {}, "account_api_base_urls_old": {}, "account_api_base_urls_new": {},
	// 用户分组配置（专属倍率 / 可用分组 / 限制公开分组）变更：只记分组 ID、
	// 倍率数字与开关的旧/新值；请求体与账号敏感信息不经过这个通道。
	"target_user_id":       {},
	"user_group_rates_old": {}, "user_group_rates_new": {}, "user_group_rates_changed": {},
	"user_allowed_groups_old": {}, "user_allowed_groups_new": {}, "user_allowed_groups_changed": {},
	"user_restrict_public_groups_old": {}, "user_restrict_public_groups_new": {}, "user_restrict_public_groups_changed": {},
	// 管理员破玻璃重置 2FA：只记「授权撤销是否成功 / 用户是否已收到通知 /
	// 是否还保留 Passkey」三个结果位；目标用户与原因写进服务层同步落库的
	// admin.user.reset_2fa 记录，totp_secret 任何形式都不入库。
	"admin_2fa_reset_revoked_stepup": {}, "admin_2fa_reset_notified": {},
	"admin_2fa_reset_passkey_bound": {},
	// 刷新时的会话指纹重绑（auth.session_binding.rebind）：只记前后两端的粗粒度标签
	// （如 chrome/windows/ipv4），绝不记 IP 原文与完整 User-Agent。
	"binding_from": {}, "binding_to": {},
	// 用户修改「创建 API Key 时是否需要验证身份」开关：只记改前 / 改后的模式标签（auto / on / off）
	// 和这次修改是否降低了保护强度（on > auto > off）；校验用到的因子名走上面的 api_key_auth_factor，凭据一律不入库。
	"api_key_create_verification_from": {}, "api_key_create_verification_to": {},
	"api_key_create_verification_relaxed": {},
	// 用户个人接口密钥生命周期只记脱敏前缀与是否重置，明文密钥永不入库。
	"personal_api_key_prefix": {}, "personal_api_key_regenerated": {},
}

// SetAuditExtra adds allowlisted, scalar details to the current audit entry.
// It is safe to call more than once; later values replace earlier ones.
func SetAuditExtra(c *gin.Context, fields map[string]any) {
	if c == nil || len(fields) == 0 {
		return
	}
	current := map[string]any{}
	if value, ok := c.Get(auditCtxKeyExtra); ok {
		if existing, ok := value.(map[string]any); ok {
			for key, item := range existing {
				current[key] = item
			}
		}
	}
	for key, value := range fields {
		if _, ok := auditExtraAllowedKeys[key]; !ok || !isAuditExtraScalar(value) {
			continue
		}
		if text, ok := value.(string); ok {
			// 512 字符：用户分组 ID / 倍率旧新值这类允许的摘要可能超过 128
			// （一次改几十个分组也是合法操作），但仍远小于任何请求体。
			value = truncateAuditExtraString(text, 512)
		}
		current[key] = value
	}
	c.Set(auditCtxKeyExtra, current)
}

func isAuditExtraScalar(value any) bool {
	switch value.(type) {
	case string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return true
	default:
		return false
	}
}

func truncateAuditExtraString(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// auditSensitiveReads 需要审计的敏感 GET 读取（method+FullPath → 动作名）。
var auditSensitiveReads = map[string]string{
	"GET /api/v1/admin/request-captures/small/:record":                        "admin.small_request_retention.content.read",
	"GET /api/v1/admin/request-captures/:task/requests/:record/content/:part": "admin.request_capture.content.read",
	"GET /api/v1/admin/request-captures/:task/export":                         "admin.request_capture.export",
	"GET /api/v1/admin/request-captures/:task/requests/:record/export":        "admin.request_capture.export",
	"GET /api/v1/admin/request-captures/:task/requests/:record":               "admin.request_capture.detail.read",
	"GET /api/v1/admin/crypto/config/private":                                 "admin.crypto.rpc_config.read",
	"GET /api/v1/admin/crypto/deposits.csv":                                   "admin.crypto.deposits.export",
	"GET /api/v1/admin/accounts/data":                                         "admin.accounts.export",
	"GET /api/v1/admin/accounts/:id/credential":                               "admin.accounts.credential.read",
	"GET /api/v1/admin/proxies/data":                                          "admin.proxies.export",
	"GET /api/v1/admin/redeem-codes/export":                                   "admin.redeem_codes.export",
	"GET /api/v1/admin/backups/:id/download-url":                              "admin.backups.download",
	"GET /api/v1/admin/settings/admin-api-key":                                "admin.admin_api_key.read",
	"GET /api/v1/admin/users/:id/api-keys":                                    "admin.users.api_keys.read",
	"GET /api/v1/admin/groups/:id/api-keys":                                   "admin.groups.api_keys.read",
	"GET /api/v1/admin/backups/s3-config":                                     "admin.backups.s3_config.read",
	"GET /api/v1/admin/data-management/s3/config":                             "admin.data_management.s3_config.read",
	// 查看 API Key 的浏览器端封存密文是一条敏感读：它把（加密后的）凭据交回前端，
	// 必须留痕，用户事后也能在审计里看到自己何时被读取过。
	"GET /api/v1/user/api-keys/:id/secret-seal": service.AuditActionAPIKeySecretFetch,
}

// auditActionOverrides 变更类请求的动作名精确映射（未命中时自动推导）。
var auditActionOverrides = map[string]string{
	"POST /api/v1/admin/crypto/scan/run":                         "admin.crypto.scan.run",
	"POST /api/v1/admin/crypto/deposits/confirm":                 "admin.crypto.deposit.manual_confirm",
	"PUT /api/v1/admin/crypto/config":                            "admin.crypto.config.update",
	"POST /api/v1/admin/crypto/addresses/:id/status":             "admin.crypto.address.status.update",
	"POST /api/v1/admin/crypto/addresses/:id/rotate":             "admin.crypto.address.rotate",
	"POST /api/v1/admin/crypto/addresses/:id/export-private-key": "admin.crypto.address.private_key.export",
	"POST /api/v1/admin/crypto/sweep/run":                        "admin.crypto.sweep.run",
	"POST /api/v1/admin/crypto/scan/runs/:id/cancel":             "admin.crypto.scan.run.cancel",
	"POST /api/v1/admin/crypto/wallet/initialize":                "admin.crypto.wallet.initialize",
	"POST /api/v1/admin/crypto/wallet/hot-mnemonic":              "admin.crypto.wallet.hot_mnemonic.import",
	"POST /api/v1/admin/crypto/wallet/export-mnemonics":          "admin.crypto.wallet.mnemonics.export",
	"POST /api/v1/admin/crypto/security/setup":                   "admin.crypto.security.setup",
	"POST /api/v1/admin/crypto/security/enable":                  "admin.crypto.security.enable",
	"POST /api/v1/admin/crypto/security/verify":                  "admin.crypto.security.verify",
	"POST /api/v1/admin/crypto/rpc/test":                         "admin.crypto.rpc.test",
	"POST /api/v1/admin/crypto/rpc/auto-pick":                    "admin.crypto.rpc.auto_pick",
	"POST /api/v1/auth/login":                                    service.AuditActionLogin,
	"POST /api/v1/auth/login/2fa":                                service.AuditActionLogin2FA,
	"POST /api/v1/auth/login/2fa/passkey/finish":                 service.AuditActionLogin2FA,
	"POST /api/v1/auth/passkey/login/finish":                     service.AuditActionLogin,
	"POST /api/v1/auth/register":                                 service.AuditActionRegister,
	"POST /api/v1/auth/refresh":                                  service.AuditActionTokenRefresh,
	"POST /api/v1/user/totp/step-up":                             service.AuditActionStepUpVerify,
	"POST /api/v1/user/step-up/passkey/finish":                   service.AuditActionStepUpVerify,
	"POST /api/v1/user/step-up/passkey/begin":                    service.AuditActionStepUpVerify,
	"POST /api/v1/admin/audit-logs/clear":                        service.AuditActionAuditLogClear,
	// 管理员破玻璃重置 2FA：固定动作名，便于按动作检索「谁在什么时候重置了
	// 谁的 2FA、原因是什么」。完整记录（目标用户、原因、重置前后状态）由
	// 服务层同步落库，这里的中间件记录提供请求级上下文。
	"POST /api/v1/admin/users/:id/reset-2fa":                  service.AuditActionAdminUserReset2FA,
	"POST /api/v1/admin/accounts/data":                        "admin.accounts.import",
	"POST /api/v1/admin/backups":                              "admin.backups.create",
	"POST /api/v1/admin/backups/:id/restore":                  "admin.backups.restore",
	"DELETE /api/v1/admin/backups/:id":                        "admin.backups.delete",
	"PUT /api/v1/admin/backups/s3-config":                     "admin.backups.s3_config.update",
	"POST /api/v1/admin/settings/admin-api-key/regenerate":    "admin.admin_api_key.regenerate",
	"DELETE /api/v1/admin/settings/admin-api-key":             "admin.admin_api_key.delete",
	"PUT /api/v1/admin/prompt-audit/config":                   "admin.prompt_audit.config.update",
	"POST /api/v1/admin/prompt-audit/endpoints/probe":         "admin.prompt_audit.endpoint.probe",
	"DELETE /api/v1/admin/prompt-audit/events/:id":            "admin.prompt_audit.event.delete",
	"POST /api/v1/admin/prompt-audit/events/batch-delete":     "admin.prompt_audit.events.batch_delete",
	"POST /api/v1/admin/prompt-audit/events/delete-preview":   "admin.prompt_audit.events.delete_preview",
	"POST /api/v1/admin/prompt-audit/events/delete-by-filter": "admin.prompt_audit.events.filter_delete",
	// 浏览器端封存密文的写操作：只留「存/删了哪把 Key」，请求体（含密文与 salt）
	// 整体不入审计库，见 auditBodyOmittedRoutes。
	"PUT /api/v1/user/api-keys/:id/secret-seal":    service.AuditActionAPIKeySecretSeal,
	"DELETE /api/v1/user/api-keys/:id/secret-seal": service.AuditActionAPIKeySecretDelete,
	// API Key 三选一身份校验：Passkey 挑战/校验与二次查看授权单独成动作，
	// 便于在审计里按动作检索（结果与因子写在 extra）。
	"POST /api/v1/user/security/passkey/verify/begin":  service.AuditActionAPIKeyPasskeyVerify,
	"POST /api/v1/user/security/passkey/verify/finish": service.AuditActionAPIKeyPasskeyVerify,
	"POST /api/v1/user/api-keys/:id/reveal-authorize":  service.AuditActionAPIKeyRevealAuthorize,
	// 用户自己决定「创建 API Key 时是否需要验证身份」：固定动作名，便于检索谁在什么时候把保护关了。
	"PUT /api/v1/user/security/api-key-create-verification": service.AuditActionAPIKeyCreateVerificationSet,
	// 用户个人接口密钥（Personal API Key）创建/重置/删除。
	"POST /api/v1/user/personal-api-key":   service.AuditActionUserPersonalAPIKeyCreate,
	"DELETE /api/v1/user/personal-api-key": service.AuditActionUserPersonalAPIKeyDelete,
}

// auditAuthEventMeta 第三方登录 / 2FA 认证事件的审计元数据。
// Action 为固定动作名；Provider/Flow 便于按渠道与阶段过滤；
// DefaultResult 是该路由在没有 handler 显式结果时的兜底判定。
type auditAuthEventMeta struct {
	Action        string
	AuthMethod    string
	Provider      string
	Flow          string
	DefaultResult string
}

// auditAuthEvents 认证事件路由表（method + FullPath）。
//
// 此前 audit_logs 只在 POST /auth/login、/auth/login/2fa、passkey finish 上留痕，
// 而 GitHub / Google / LinuxDo / OIDC / 钉钉 / 微信 的 OAuth 回调是 GET，
// 既不进敏感读白名单、也不进变更类请求，导致「OAuth 登录成功/失败」完全无法反查，
// 2FA 是否被绕过也缺少证据链。这里把全部第三方登录入口、回调与后续兑换显式登记，
// 由审计中间件统一落一条结构化记录（result / reason / provider / flow / email_hash）。
var auditAuthEvents = map[string]auditAuthEventMeta{
	// 密码 / 邮箱登录与 2FA 挑战
	"POST /api/v1/auth/login":                    {Action: service.AuditActionLogin, AuthMethod: service.AuditAuthMethodPassword, Provider: "password", Flow: "password"},
	"POST /api/v1/auth/login/2fa":                {Action: service.AuditActionLogin2FA, AuthMethod: service.AuditAuthMethodPassword, Provider: "totp", Flow: "two_factor"},
	"POST /api/v1/auth/login/2fa/passkey/finish": {Action: service.AuditActionLogin2FA, AuthMethod: service.AuditAuthMethodPassword, Provider: "passkey", Flow: "two_factor"},
	"POST /api/v1/auth/register":                 {Action: service.AuditActionRegister, AuthMethod: service.AuditAuthMethodPassword, Provider: "password", Flow: "email_register"},

	// Passkey（WebAuthn）登录
	"POST /api/v1/auth/passkey/login/begin": {
		Action: service.AuditActionPasskeyLoginBegin, AuthMethod: service.AuditAuthMethodPasskey, Provider: "passkey",
		Flow: "passkey_challenge", DefaultResult: service.AuditResultChallengeIssued,
	},
	"POST /api/v1/auth/passkey/login/finish": {
		Action: service.AuditActionLogin, AuthMethod: service.AuditAuthMethodPasskey, Provider: "passkey", Flow: "passkey_login",
	},

	// 各渠道 start（GET 重定向 / POST 取 authorize_url）
	"GET /api/v1/auth/oauth/github/start":    {Action: service.AuditActionOAuthLoginStart, AuthMethod: service.AuditAuthMethodOAuth, Provider: "github", Flow: "login_start", DefaultResult: service.AuditResultStarted},
	"POST /api/v1/auth/oauth/github/start":   {Action: service.AuditActionOAuthLoginStart, AuthMethod: service.AuditAuthMethodOAuth, Provider: "github", Flow: "login_start", DefaultResult: service.AuditResultStarted},
	"GET /api/v1/auth/oauth/google/start":    {Action: service.AuditActionOAuthLoginStart, AuthMethod: service.AuditAuthMethodOAuth, Provider: "google", Flow: "login_start", DefaultResult: service.AuditResultStarted},
	"POST /api/v1/auth/oauth/google/start":   {Action: service.AuditActionOAuthLoginStart, AuthMethod: service.AuditAuthMethodOAuth, Provider: "google", Flow: "login_start", DefaultResult: service.AuditResultStarted},
	"GET /api/v1/auth/oauth/linuxdo/start":   {Action: service.AuditActionOAuthLoginStart, AuthMethod: service.AuditAuthMethodOAuth, Provider: "linuxdo", Flow: "login_start", DefaultResult: service.AuditResultStarted},
	"POST /api/v1/auth/oauth/linuxdo/start":  {Action: service.AuditActionOAuthLoginStart, AuthMethod: service.AuditAuthMethodOAuth, Provider: "linuxdo", Flow: "login_start", DefaultResult: service.AuditResultStarted},
	"GET /api/v1/auth/oauth/wechat/start":    {Action: service.AuditActionOAuthLoginStart, AuthMethod: service.AuditAuthMethodOAuth, Provider: "wechat", Flow: "login_start", DefaultResult: service.AuditResultStarted},
	"POST /api/v1/auth/oauth/wechat/start":   {Action: service.AuditActionOAuthLoginStart, AuthMethod: service.AuditAuthMethodOAuth, Provider: "wechat", Flow: "login_start", DefaultResult: service.AuditResultStarted},
	"GET /api/v1/auth/oauth/oidc/start":      {Action: service.AuditActionOAuthLoginStart, AuthMethod: service.AuditAuthMethodOAuth, Provider: "oidc", Flow: "login_start", DefaultResult: service.AuditResultStarted},
	"POST /api/v1/auth/oauth/oidc/start":     {Action: service.AuditActionOAuthLoginStart, AuthMethod: service.AuditAuthMethodOAuth, Provider: "oidc", Flow: "login_start", DefaultResult: service.AuditResultStarted},
	"GET /api/v1/auth/oauth/dingtalk/start":  {Action: service.AuditActionOAuthLoginStart, AuthMethod: service.AuditAuthMethodOAuth, Provider: "dingtalk", Flow: "login_start", DefaultResult: service.AuditResultStarted},
	"POST /api/v1/auth/oauth/dingtalk/start": {Action: service.AuditActionOAuthLoginStart, AuthMethod: service.AuditAuthMethodOAuth, Provider: "dingtalk", Flow: "login_start", DefaultResult: service.AuditResultStarted},

	// 绑定现有账号
	"GET /api/v1/auth/oauth/linuxdo/bind/start":   {Action: service.AuditActionOAuthBind, AuthMethod: service.AuditAuthMethodOAuth, Provider: "linuxdo", Flow: "bind_start", DefaultResult: service.AuditResultStarted},
	"GET /api/v1/auth/oauth/wechat/bind/start":    {Action: service.AuditActionOAuthBind, AuthMethod: service.AuditAuthMethodOAuth, Provider: "wechat", Flow: "bind_start", DefaultResult: service.AuditResultStarted},
	"GET /api/v1/auth/oauth/oidc/bind/start":      {Action: service.AuditActionOAuthBind, AuthMethod: service.AuditAuthMethodOAuth, Provider: "oidc", Flow: "bind_start", DefaultResult: service.AuditResultStarted},
	"GET /api/v1/auth/oauth/dingtalk/bind/start":  {Action: service.AuditActionOAuthBind, AuthMethod: service.AuditAuthMethodOAuth, Provider: "dingtalk", Flow: "bind_start", DefaultResult: service.AuditResultStarted},
	"POST /api/v1/auth/oauth/linuxdo/bind-login":  {Action: service.AuditActionOAuthBind, AuthMethod: service.AuditAuthMethodOAuth, Provider: "linuxdo", Flow: "bind_login"},
	"POST /api/v1/auth/oauth/wechat/bind-login":   {Action: service.AuditActionOAuthBind, AuthMethod: service.AuditAuthMethodOAuth, Provider: "wechat", Flow: "bind_login"},
	"POST /api/v1/auth/oauth/oidc/bind-login":     {Action: service.AuditActionOAuthBind, AuthMethod: service.AuditAuthMethodOAuth, Provider: "oidc", Flow: "bind_login"},
	"POST /api/v1/auth/oauth/dingtalk/bind-login": {Action: service.AuditActionOAuthBind, AuthMethod: service.AuditAuthMethodOAuth, Provider: "dingtalk", Flow: "bind_login"},
	"POST /api/v1/auth/oauth/pending/bind-login":  {Action: service.AuditActionOAuthBind, AuthMethod: service.AuditAuthMethodOAuth, Provider: "oauth", Flow: "bind_login"},

	// OAuth 回调（GET）：成功即签发会话（或 2FA 挑战），失败由 redirectOAuthError 标记。
	// 无显式结果时按 pending（需前端继续）而非 success，避免把「差一步」误记为登录成功。
	"GET /api/v1/auth/oauth/github/callback":   {Action: service.AuditActionOAuthLogin, AuthMethod: service.AuditAuthMethodOAuth, Provider: "github", Flow: "login_callback", DefaultResult: service.AuditResultPending},
	"GET /api/v1/auth/oauth/google/callback":   {Action: service.AuditActionOAuthLogin, AuthMethod: service.AuditAuthMethodOAuth, Provider: "google", Flow: "login_callback", DefaultResult: service.AuditResultPending},
	"GET /api/v1/auth/oauth/linuxdo/callback":  {Action: service.AuditActionOAuthLogin, AuthMethod: service.AuditAuthMethodOAuth, Provider: "linuxdo", Flow: "login_callback", DefaultResult: service.AuditResultPending},
	"GET /api/v1/auth/oauth/wechat/callback":   {Action: service.AuditActionOAuthLogin, AuthMethod: service.AuditAuthMethodOAuth, Provider: "wechat", Flow: "login_callback", DefaultResult: service.AuditResultPending},
	"GET /api/v1/auth/oauth/oidc/callback":     {Action: service.AuditActionOAuthLogin, AuthMethod: service.AuditAuthMethodOAuth, Provider: "oidc", Flow: "login_callback", DefaultResult: service.AuditResultPending},
	"GET /api/v1/auth/oauth/dingtalk/callback": {Action: service.AuditActionOAuthLogin, AuthMethod: service.AuditAuthMethodOAuth, Provider: "dingtalk", Flow: "login_callback", DefaultResult: service.AuditResultPending},

	// 回调后的注册 / 补邮箱 / 建号
	"POST /api/v1/auth/oauth/github/complete-registration":   {Action: service.AuditActionOAuthRegister, AuthMethod: service.AuditAuthMethodOAuth, Provider: "github", Flow: "register"},
	"POST /api/v1/auth/oauth/google/complete-registration":   {Action: service.AuditActionOAuthRegister, AuthMethod: service.AuditAuthMethodOAuth, Provider: "google", Flow: "register"},
	"POST /api/v1/auth/oauth/linuxdo/complete-registration":  {Action: service.AuditActionOAuthRegister, AuthMethod: service.AuditAuthMethodOAuth, Provider: "linuxdo", Flow: "register"},
	"POST /api/v1/auth/oauth/wechat/complete-registration":   {Action: service.AuditActionOAuthRegister, AuthMethod: service.AuditAuthMethodOAuth, Provider: "wechat", Flow: "register"},
	"POST /api/v1/auth/oauth/oidc/complete-registration":     {Action: service.AuditActionOAuthRegister, AuthMethod: service.AuditAuthMethodOAuth, Provider: "oidc", Flow: "register"},
	"POST /api/v1/auth/oauth/dingtalk/complete-registration": {Action: service.AuditActionOAuthRegister, AuthMethod: service.AuditAuthMethodOAuth, Provider: "dingtalk", Flow: "register"},
	"POST /api/v1/auth/oauth/linuxdo/create-account":         {Action: service.AuditActionOAuthRegister, AuthMethod: service.AuditAuthMethodOAuth, Provider: "linuxdo", Flow: "create_account"},
	"POST /api/v1/auth/oauth/wechat/create-account":          {Action: service.AuditActionOAuthRegister, AuthMethod: service.AuditAuthMethodOAuth, Provider: "wechat", Flow: "create_account"},
	"POST /api/v1/auth/oauth/oidc/create-account":            {Action: service.AuditActionOAuthRegister, AuthMethod: service.AuditAuthMethodOAuth, Provider: "oidc", Flow: "create_account"},
	"POST /api/v1/auth/oauth/dingtalk/create-account":        {Action: service.AuditActionOAuthRegister, AuthMethod: service.AuditAuthMethodOAuth, Provider: "dingtalk", Flow: "create_account"},
	"POST /api/v1/auth/oauth/pending/create-account":         {Action: service.AuditActionOAuthRegister, AuthMethod: service.AuditAuthMethodOAuth, Provider: "oauth", Flow: "create_account"},
	"POST /api/v1/auth/oauth/pending/send-verify-code":       {Action: service.AuditActionOAuthRegister, AuthMethod: service.AuditAuthMethodOAuth, Provider: "oauth", Flow: "send_verify_code"},

	// pending OAuth 一次性兑换（登录终态 / 2FA 挑战下发都走这里）
	"POST /api/v1/auth/oauth/pending/exchange": {Action: service.AuditActionOAuthPending, AuthMethod: service.AuditAuthMethodOAuth, Provider: "oauth", Flow: "pending_exchange"},
}

// decorateAuthAuditEntry 把认证事件的上下文标记合成进审计记录。
func decorateAuthAuditEntry(c *gin.Context, entry *service.AuditLog, meta auditAuthEventMeta) {
	if c == nil || entry == nil {
		return
	}
	if meta.AuthMethod != "" {
		entry.AuthMethod = meta.AuthMethod
	}
	if masked := c.GetString(auditCtxKeyAuthEmailMasked); masked != "" {
		entry.ActorEmail = masked
	}

	extra := entry.Extra
	if extra == nil {
		extra = map[string]any{}
	}
	provider := meta.Provider
	if override := strings.TrimSpace(c.GetString(auditCtxKeyAuthProvider)); override != "" {
		provider = override
	}
	if provider != "" {
		extra["provider"] = provider
	}
	if meta.Flow != "" {
		extra["flow"] = meta.Flow
	}

	result := strings.TrimSpace(c.GetString(auditCtxKeyAuthResult))
	if entry.StatusCode >= 400 {
		result = service.AuditResultFailure
	} else if result == "" {
		result = meta.DefaultResult
		if result == "" {
			result = service.AuditResultSuccess
		}
	}
	extra["result"] = result

	reason := strings.TrimSpace(c.GetString(auditCtxKeyAuthFailureReason))
	if reason == "" && result == service.AuditResultFailure {
		// response.ErrorFrom 会把 infra error reason 写到 gin context，
		// 让 POST 类登录入口（pending 兑换 / bind-login）也能拿到失败分类。
		reason = strings.TrimSpace(c.GetString("response_error_reason"))
	}
	if reason != "" {
		extra["reason"] = truncateAuditExtraString(sanitizeAuditReason(reason), 64)
		extra["reason_class"] = service.ClassifyAuthFailure(reason, entry.StatusCode)
	} else if result == service.AuditResultFailure {
		extra["reason_class"] = service.ClassifyAuthFailure("", entry.StatusCode)
	}
	if emailHash := c.GetString(auditCtxKeyAuthEmailHash); emailHash != "" {
		extra["email_hash"] = emailHash
	}
	if value, ok := c.Get(auditCtxKeyAuthRequires2FA); ok {
		if required, _ := value.(bool); required {
			extra["requires_2fa"] = true
		}
	}
	entry.Extra = extra
}

// sanitizeAuditReason 只保留错误码字符集，避免把上游错误原文（可能含邮箱/链接）写进审计。
func sanitizeAuditReason(reason string) string {
	reason = strings.TrimSpace(reason)
	out := make([]rune, 0, len(reason))
	for _, r := range reason {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '-', r == '.':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// auditBodyOmittedRoutes 请求体几乎整体由凭证构成的路由（如整块粘贴 auth JSON 的导入接口）。
// 这类 body 的凭证内嵌在普通字符串值里，键级脱敏无法覆盖，整体不入库。
var auditBodyOmittedRoutes = map[string]struct{}{
	"POST /api/v1/admin/excel-grants/:id/callback":               {},
	"PUT /api/v1/admin/credential-guard/:id":                     {},
	"PUT /api/v1/admin/account-ops/config":                       {},
	"PUT /api/v1/admin/account-ops/notification-settings":        {},
	"PUT /api/v1/admin/account-ops/webhooks/:id":                 {},
	"POST /api/v1/admin/system/mihomo":                           {},
	"PUT /api/v1/admin/crypto/config":                            {},
	"POST /api/v1/admin/crypto/rpc/test":                         {},
	"POST /api/v1/admin/crypto/rpc/auto-pick":                    {},
	"POST /api/v1/admin/crypto/wallet/initialize":                {},
	"POST /api/v1/admin/crypto/wallet/export-mnemonics":          {},
	"POST /api/v1/admin/crypto/addresses/:id/export-private-key": {},
	"POST /api/v1/admin/crypto/wallet/hot-mnemonic":              {},
	"POST /api/v1/admin/crypto/security/setup":                   {},
	"POST /api/v1/admin/crypto/security/enable":                  {},
	"POST /api/v1/admin/crypto/security/verify":                  {},
	// Device keys are push credentials; omit the entire update body.
	"PUT /api/v1/user/bark":                                     {},
	"POST /api/v1/user/totp/disable":                            {},
	"POST /api/v1/tickets":                                      {},
	"POST /api/v1/tickets/:id/replies":                          {},
	"POST /api/v1/admin/tickets/:id/replies":                    {},
	"POST /api/v1/admin/tickets":                                {},
	"POST /api/v1/auth/passkey/login/finish":                    {},
	"POST /api/v1/user/passkeys/register/finish":                {},
	"POST /api/v1/admin/accounts/import/codex-session":          {},
	"PUT /api/v1/admin/accounts/:id/ollama-cloud-usage/session": {},
	"PUT /api/v1/admin/prompt-audit/config":                     {},
	"POST /api/v1/admin/prompt-audit/endpoints/probe":           {},
	"DELETE /api/v1/admin/prompt-audit/events/:id":              {},
	"POST /api/v1/admin/prompt-audit/events/batch-delete":       {},
	"POST /api/v1/admin/prompt-audit/events/delete-preview":     {},
	"POST /api/v1/admin/prompt-audit/events/delete-by-filter":   {},
	// 封存密文的请求体是「密文 + salt + 登录密码」，审计只记 Key 标识与因子，
	// 绝不落这些字节。
	"PUT /api/v1/user/api-keys/:id/secret-seal": {},
	// 二次查看授权与 Passkey 校验的请求体几乎全部是凭据（密码 / 动态码 /
	// assertion / 票据），键级脱敏之外再整体不入库，避免新增字段名漏网。
	"POST /api/v1/user/api-keys/:id/reveal-authorize":  {},
	"POST /api/v1/user/security/passkey/verify/finish": {},
	// 双因子策略（#561）新增的两个 Passkey finish 接口，当时漏登记：请求体里的
	// credential 是完整的 WebAuthn assertion（authenticatorData / clientDataJSON /
	// signature），这些字段名不在键级脱敏清单里，不整体省略就会原样落进
	// audit_logs.request_body。login/2fa/passkey/finish 同时登记在 auditAuthEvents，
	// 认证事件分支本来就会省略 body，这里是纵深防御，防止以后调整认证事件表时回退。
	"POST /api/v1/user/step-up/passkey/finish":   {},
	"POST /api/v1/auth/login/2fa/passkey/finish": {},
	// 封存密文的读回接口是 GET，本身没有请求体；显式登记为「无可记录体」，
	// 防止以后给 GET 加 body 或在 Omit 清单里被当成漏项。
	"GET /api/v1/user/api-keys/:id/secret-seal": {},
	// 修改「创建 API Key 时是否需要验证身份」的请求体可能带 verification（密码 / 动态码 / 通行密钥票据）：
	// 键级脱敏之外再整体不入库，审计只靠 extra 里的因子名与改前 / 改后标签。
	"PUT /api/v1/user/security/api-key-create-verification": {},
	// 备份计划接口允许管理员粘贴 age 公钥；若有人误把 AGE-SECRET-KEY-... 私钥粘进请求体，
	// 即使 handler 返回 400 拒绝，审计中间件也会在进入 handler 前抓包落库且 audit_logs
	// 会被关键表备份打包，因此整条路由不记录原始请求体。
	"PUT /api/v1/admin/backups/schedule": {},
	// 创建/重置用户个人接口密钥的请求体可能带三选一身份校验凭据，整体不入库。
	"POST /api/v1/user/personal-api-key": {},
}

// NewAuditLogMiddleware 创建审计中间件。
// 记录范围：变更类请求（POST/PUT/PATCH/DELETE）+ 白名单内的敏感 GET 读取。
// 挂载位置：admin / user / admin-payment 组挂在各自认证中间件之后（只审计已认证请求，
// 未过认证的 401/403 不入库）；auth 组（登录/注册/刷新）无前置认证，天然记录失败尝试。
func NewAuditLogMiddleware(auditService *service.AuditLogService) AuditLogMiddleware {
	return AuditLogMiddleware(func(c *gin.Context) {
		if service.CryptoAdminTestReadOnly() {
			c.Next()
			return
		}
		routeKey := c.Request.Method + " " + c.FullPath()
		authMeta, isAuthEvent := auditAuthEvents[routeKey]

		record := false
		action := ""
		switch c.Request.Method {
		case "POST", "PUT", "PATCH", "DELETE":
			record = true
			if v, ok := auditActionOverrides[routeKey]; ok {
				action = v
			}
		case "GET":
			if v, ok := auditSensitiveReads[routeKey]; ok {
				record = true
				action = v
			}
		}
		// 认证事件（OAuth 回调是 GET）不受变更类请求白名单限制，单独放行。
		if isAuthEvent {
			record = true
			action = authMeta.Action
		}
		if !record {
			c.Next()
			// 敏感 GET 不在审计白名单里，但机器凭据被 step-up 拒绝必须留安全事件。
			recordStepUpMachineDenial(c, auditService)
			return
		}

		// 捕获请求体（读出后回填，避免影响后续 ShouldBindJSON）。
		// 只读取脱敏解析上限内的字节，超出部分与已读部分拼接回填，
		// 避免大体积导入请求被完整复制进内存两次。
		var bodyRedacted string
		if isAuthEvent {
			// 登录/回调请求体里可能同时含邮箱、密码、临时 token，
			// 认证审计只保留结构化字段，整体不入库。
			bodyRedacted = "<credential-bearing body omitted>"
		} else if _, omit := auditBodyOmittedRoutes[routeKey]; omit {
			bodyRedacted = "<credential-bearing body omitted>"
		} else if c.Request.Body != nil && c.Request.Method != "GET" {
			orig := c.Request.Body
			raw, err := io.ReadAll(io.LimitReader(orig, service.AuditRequestBodyCaptureLimit+1))
			if err == nil {
				c.Request.Body = &restoredBody{
					Reader: io.MultiReader(bytes.NewReader(raw), orig),
					closer: orig,
				}
				bodyRedacted = service.RedactAuditBody(raw, c.GetHeader("Content-Type"))
			}
		}

		start := time.Now()
		c.Next()

		if c.GetBool(auditCtxKeySkip) {
			return
		}

		status := c.Writer.Status()
		// token 刷新成功属于高频常规操作，只记录失败（潜在攻击信号）；
		// handler 用 MarkAuditAlways 标记过的成功刷新（如指纹重绑）除外。
		if routeKey == "POST /api/v1/auth/refresh" && status < 400 && !c.GetBool(auditCtxKeyForce) {
			return
		}

		entry := &service.AuditLog{
			CreatedAt:   time.Now().UTC(),
			Action:      action,
			Method:      c.Request.Method,
			Path:        c.FullPath(),
			ClientIP:    SecurityClientIP(c),
			UserAgent:   c.Request.UserAgent(),
			RequestBody: bodyRedacted,
			StatusCode:  status,
			LatencyMs:   time.Since(start).Milliseconds(),
		}
		if entry.Path == "" {
			entry.Path = c.Request.URL.Path
		}
		if entry.Action == "" {
			entry.Action = deriveAuditAction(c.Request.Method, entry.Path)
		}
		if v, ok := c.Get(auditCtxKeyAction); ok {
			if s, ok := v.(string); ok && s != "" {
				entry.Action = s
			}
		}
		if requestID, ok := c.Request.Context().Value(ctxkey.RequestID).(string); ok {
			entry.RequestID = requestID
		}

		// 操作者身份：优先取认证中间件写入的上下文，其次取 handler 覆写（登录等场景）。
		if subject, ok := GetAuthSubjectFromContext(c); ok && subject.UserID > 0 {
			uid := subject.UserID
			entry.ActorUserID = &uid
		}
		if role, ok := GetUserRoleFromContext(c); ok {
			entry.ActorRole = role
		}
		entry.ActorEmail = c.GetString(ContextKeyAuthEmail)
		entry.AuthMethod = c.GetString("auth_method")
		if entry.AuthMethod == "" && entry.ActorUserID != nil {
			entry.AuthMethod = service.AuditAuthMethodJWT
		}
		if v, ok := c.Get(auditCtxKeyActorID); ok {
			if id, ok := v.(int64); ok && id > 0 {
				entry.ActorUserID = &id
			}
		}
		if v, ok := c.Get(auditCtxKeyActorEmail); ok {
			if s, ok := v.(string); ok && s != "" {
				entry.ActorEmail = s
			}
		}

		// 请求头凭证掩码（仅保留首尾）。
		entry.CredentialMasked = MaskedRequestCredential(c)

		extra := map[string]any{}
		if value, ok := c.Get(auditCtxKeyExtra); ok {
			if details, ok := value.(map[string]any); ok {
				for key, item := range details {
					extra[key] = item
				}
			}
		}
		if entry.AuthMethod == service.AuditAuthMethodUserAPIKey {
			if prefix := c.GetString(ContextKeyPersonalAPIKeyPrefix); prefix != "" {
				if _, exists := extra["personal_api_key_prefix"]; !exists {
					extra["personal_api_key_prefix"] = prefix
				}
			}
		}
		if len(c.Params) > 0 {
			params := make(map[string]string, len(c.Params))
			for _, p := range c.Params {
				params[p.Key] = p.Value
			}
			extra["params"] = params
		}
		if q := service.RedactAuditQuery(c.Request.URL.RawQuery); q != "" {
			extra["query"] = q
		}
		if len(extra) > 0 {
			entry.Extra = extra
		}
		if isAuthEvent {
			decorateAuthAuditEntry(c, entry, authMeta)
		}

		// 先标记再入队：认证层的兜底记录在整条中间件链返回后才执行，
		// 只有确认完整记录已产生，才不重复补记。
		c.Set(auditCtxKeyRecorded, true)
		auditService.Record(entry)
		// 变更类请求被 step-up 拒绝时，主记录之外再落一条机器凭据安全事件。
		recordStepUpMachineDenial(c, auditService)
	})
}

// restoredBody 把审计中间件按上限读出的前缀与未读完的原始 body 拼接回填，
// 保证 handler 读到完整请求体；Close 委托给原始 body。
type restoredBody struct {
	io.Reader
	closer io.Closer
}

func (b *restoredBody) Close() error { return b.closer.Close() }

// MaskedRequestCredential 提取请求头中的凭证并做首尾掩码。
func MaskedRequestCredential(c *gin.Context) string {
	if len(c.Request.Header.Values("X-Crypto-Grant")) > 0 {
		return "x-crypto-grant <redacted>"
	}
	// X-Reveal-Grant 是「二次查看 API Key 明文」的 5 分钟一次性授权票据，
	// 等价于一次性凭证：一旦落进 audit_logs 就等于长期留存一份可用票据。
	if len(c.Request.Header.Values("X-Reveal-Grant")) > 0 {
		return "x-reveal-grant <redacted>"
	}
	if len(c.Request.Header.Values("X-Bark-TOTP")) > 0 {
		return "x-bark-totp <redacted>"
	}
	if len(c.Request.Header.Values("X-Crypto-TOTP")) > 0 {
		return "x-crypto-totp <redacted>"
	}
	if apiKey := strings.TrimSpace(c.GetHeader("x-api-key")); apiKey != "" {
		// 个人接口密钥场景下若请求通过 Authorization: Bearer uak_... 认证且夹带无关 X-API-Key，
		// 跳过诱饵 X-API-Key，保证审计记录的是实际生效的凭证掩码。
		if c.GetString("auth_method") != service.AuditAuthMethodUserAPIKey || service.IsUserPersonalAPIKeyPrefix(apiKey) {
			return "x-api-key " + service.MaskAuditCredential(apiKey)
		}
	}
	authHeader := strings.TrimSpace(c.GetHeader("Authorization"))
	if authHeader == "" {
		return ""
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) == 2 {
		return parts[0] + " " + service.MaskAuditCredential(strings.TrimSpace(parts[1]))
	}
	return service.MaskAuditCredential(authHeader)
}

// deriveAuditAction 由 method + 路由模板自动推导动作名，
// 例：PUT /api/v1/admin/accounts/:id → admin.accounts.update
func deriveAuditAction(method, fullPath string) string {
	path := strings.TrimPrefix(fullPath, "/api/v1/")
	path = strings.Trim(path, "/")
	segs := strings.Split(path, "/")
	parts := make([]string, 0, len(segs))
	for _, seg := range segs {
		if seg == "" || strings.HasPrefix(seg, ":") || strings.HasPrefix(seg, "*") {
			continue
		}
		parts = append(parts, strings.ReplaceAll(seg, "-", "_"))
	}
	verb := ""
	switch method {
	case "POST":
		verb = "create"
	case "PUT", "PATCH":
		verb = "update"
	case "DELETE":
		verb = "delete"
	case "GET":
		verb = "read"
	default:
		verb = strings.ToLower(method)
	}
	if len(parts) == 0 {
		return verb
	}
	return strings.Join(parts, ".") + "." + verb
}
