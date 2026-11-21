package middleware

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminroutepolicy"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// 机器凭据（管理员 API Key）的统一默认拒绝层。
//
// 历史问题：step-up 是逐路由 opt-in，只有显式挂 NewStepUpAuthMiddleware 的
// 路由才拒绝机器凭据；没挂门控的只读路由（卡密列表/详情、代理列表/详情等）
// 可以让旧版全权 x-api-key 直接读到明文，导出接口的 2FA 形同虚设。
//
// 现在改为：认证中间件在建立 auth_method=admin_api_key 身份后，立刻按集中
// 注册表判定；只有 machine_allowlisted（健康检查、只读统计聚合、无敏感数据
// 的自动化触发）放行，其余一律 403 STEP_UP_ADMIN_API_KEY_FORBIDDEN 并记安全
// 事件。forbidden 判断与 step_up_enabled 开关无关——机器凭据永远不能靠关闭
// 功能开关绕过。
const (
	// ContextKeyAdminCredentialKind 标记机器凭据类型（旧版全权 / 受限密钥）。
	ContextKeyAdminCredentialKind = "admin_credential_kind"
	// ContextKeyAdminCredentialPrefix 是可安全入审计的凭据前缀（绝不含完整 key）。
	ContextKeyAdminCredentialPrefix = "admin_credential_prefix"

	// ContextKeyStepUpMachineDenied 由 step-up 门控在拒绝机器凭据时写入，
	// 审计中间件据此补记一条安全事件（GET 敏感读本来不在审计白名单里）。
	ContextKeyStepUpMachineDenied = "step_up_machine_denied"

	AdminCredentialKindLegacy = "legacy_full_power"
	AdminCredentialKindScoped = "scoped"

	// ActionAdminMachineRequest 是机器凭据成功调用的审计动作。
	ActionAdminMachineRequest = "admin.machine_credential.request"
	// ActionAdminMachineSensitiveDenied 是机器凭据访问敏感资源被拒的安全事件。
	ActionAdminMachineSensitiveDenied = "admin.machine_credential.sensitive_denied"
	// ActionAdminMachineDefaultDenied 是机器凭据访问非白名单接口被拒的安全事件。
	ActionAdminMachineDefaultDenied = "admin.machine_credential.default_denied"
)

// enforceAdminMachineRoutePolicy 对已认证的机器凭据执行默认拒绝。
// 返回 true 表示放行；false 时已写入 403 并可安全 return。
func enforceAdminMachineRoutePolicy(c *gin.Context, audit *service.AuditLogService) bool {
	method := c.Request.Method
	fullPath := c.FullPath()
	policy, classified := adminroutepolicy.Resolve(method, fullPath)
	if classified && policy.Class == adminroutepolicy.ClassMachineAllowlisted {
		return true
	}

	action := ActionAdminMachineDefaultDenied
	if classified && policy.Class == adminroutepolicy.ClassStepUpRequired {
		action = ActionAdminMachineSensitiveDenied
	}
	recordAdminMachineDenied(c, audit, action, policy, classified)

	AbortWithError(c, 403, "STEP_UP_ADMIN_API_KEY_FORBIDDEN",
		"Admin API key cannot access this endpoint; a two-factor verified admin session is required")
	return false
}

// recordAdminMachineDenied 写一条机器凭据越权安全事件（只含前缀与路由，不含 key）。
func recordAdminMachineDenied(c *gin.Context, audit *service.AuditLogService, action string, policy adminroutepolicy.Resolved, classified bool) {
	if audit == nil {
		return
	}
	policyClass := "unclassified"
	if classified {
		policyClass = string(policy.Class)
	}
	entry := &service.AuditLog{
		CreatedAt:        time.Now().UTC(),
		AuthMethod:       service.AuditAuthMethodAdminAPIKey,
		Action:           action,
		Method:           c.Request.Method,
		Path:             c.FullPath(),
		StatusCode:       403,
		ClientIP:         SecurityClientIP(c),
		UserAgent:        c.Request.UserAgent(),
		CredentialMasked: "x-api-key " + adminCredentialPrefixForAudit(c),
		Extra: map[string]any{
			"route":           c.Request.Method + " " + c.FullPath(),
			"policy_class":    policyClass,
			"credential_kind": c.GetString(ContextKeyAdminCredentialKind),
		},
	}
	if prefix := c.GetString(ContextKeyAdminCredentialPrefix); prefix != "" {
		entry.Extra["credential_prefix"] = prefix
	}
	if requestID, ok := c.Request.Context().Value(ctxkey.RequestID).(string); ok {
		entry.RequestID = requestID
	}
	if subject, ok := GetAuthSubjectFromContext(c); ok && subject.UserID > 0 {
		uid := subject.UserID
		entry.ActorUserID = &uid
	}
	audit.Record(entry)
}

// recordAdminMachineAllowedRequest 为机器凭据的成功调用补一条审计记录。
// 变更类请求与敏感读取已由审计中间件落库（含密钥身份）；这里只兜底那些
// 审计中间件不覆盖的调用（例如白名单里的只读统计），保证"每次调用都有记录"。
func recordAdminMachineAllowedRequest(c *gin.Context, audit *service.AuditLogService) {
	if audit == nil || c.GetBool(auditCtxKeyRecorded) {
		return
	}
	entry := &service.AuditLog{
		CreatedAt:        time.Now().UTC(),
		AuthMethod:       service.AuditAuthMethodAdminAPIKey,
		Action:           ActionAdminMachineRequest,
		Method:           c.Request.Method,
		Path:             c.FullPath(),
		StatusCode:       c.Writer.Status(),
		ClientIP:         SecurityClientIP(c),
		UserAgent:        c.Request.UserAgent(),
		CredentialMasked: "x-api-key " + adminCredentialPrefixForAudit(c),
		Extra: map[string]any{
			"route":           c.Request.Method + " " + c.FullPath(),
			"credential_kind": c.GetString(ContextKeyAdminCredentialKind),
		},
	}
	if prefix := c.GetString(ContextKeyAdminCredentialPrefix); prefix != "" {
		entry.Extra["credential_prefix"] = prefix
	}
	if requestID, ok := c.Request.Context().Value(ctxkey.RequestID).(string); ok {
		entry.RequestID = requestID
	}
	if subject, ok := GetAuthSubjectFromContext(c); ok && subject.UserID > 0 {
		uid := subject.UserID
		entry.ActorUserID = &uid
	}
	audit.Record(entry)
}

// recordStepUpMachineDenial 由审计中间件在请求结束后调用：step-up 门控拒绝了
// 机器凭据（受限密钥或旧版 key），这里补一条安全事件。审计记录只含路由、
// 身份类型与凭据前缀，不含任何密钥材料。
func recordStepUpMachineDenial(c *gin.Context, audit *service.AuditLogService) {
	if audit == nil || c == nil || !c.GetBool(ContextKeyStepUpMachineDenied) {
		return
	}
	policy, classified := adminroutepolicy.Resolve(c.Request.Method, c.FullPath())
	action := ActionAdminMachineSensitiveDenied
	policyClass := "unclassified"
	if classified {
		policyClass = string(policy.Class)
	}
	entry := &service.AuditLog{
		CreatedAt:        time.Now().UTC(),
		AuthMethod:       c.GetString("auth_method"),
		Action:           action,
		Method:           c.Request.Method,
		Path:             c.FullPath(),
		StatusCode:       403,
		ClientIP:         SecurityClientIP(c),
		UserAgent:        c.Request.UserAgent(),
		CredentialMasked: "x-api-key " + adminCredentialPrefixForAudit(c),
		Extra: map[string]any{
			"route":           c.Request.Method + " " + c.FullPath(),
			"policy_class":    policyClass,
			"credential_kind": c.GetString(ContextKeyAdminCredentialKind),
		},
	}
	if prefix := c.GetString(ContextKeyAdminCredentialPrefix); prefix != "" {
		entry.Extra["credential_prefix"] = prefix
	}
	if requestID, ok := c.Request.Context().Value(ctxkey.RequestID).(string); ok {
		entry.RequestID = requestID
	}
	if subject, ok := GetAuthSubjectFromContext(c); ok && subject.UserID > 0 {
		uid := subject.UserID
		entry.ActorUserID = &uid
	}
	audit.Record(entry)
}

func adminCredentialPrefixForAudit(c *gin.Context) string {
	if prefix := c.GetString(ContextKeyAdminCredentialPrefix); prefix != "" {
		return prefix + "..."
	}
	return "***"
}
