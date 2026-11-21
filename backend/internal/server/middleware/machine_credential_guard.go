package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// MachineCredentialForbiddenCode 是拒绝机器凭据访问只读敏感接口的错误码。
const MachineCredentialForbiddenCode = "MACHINE_CREDENTIAL_FORBIDDEN"

// ForbidMachineCredentials 拒绝机器凭据（旧版全权 x-api-key 与 adm_sc_ 受限密钥）
// 访问「只读但成批敏感」的管理面接口。
//
// 背景（#489 审计 B-2 第 1 条）：step-up 只挂在写操作与导出上，只读的
// GET /admin/redeem-codes（分页）和 /:id 不在门控里；机器凭据没有 TOTP，却可以
// 逐页把全部卡密读走，导出接口的 step-up 因此形同虚设。
//
// 卡密哈希化之后这些接口只返回掩码，但「机器凭据能成批读走全量业务凭据元数据」
// 本身仍是不该保留的能力：读操作不该强迫管理员每次输 TOTP，那就必须要求人。
// 因此这里只拦机器凭据，不要求 step-up：人工管理员会话（JWT）照旧直接可读。
//
// 注意：只包在「只读且成批」的路由上；写操作继续由 StepUpAuthMiddleware 负责
// （它对机器凭据返回 403 STEP_UP_ADMIN_API_KEY_FORBIDDEN，语义更强）。
func ForbidMachineCredentials() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("auth_method") == service.AuditAuthMethodAdminAPIKey {
			AbortWithError(c, 403, MachineCredentialForbiddenCode,
				"Admin API keys cannot access this endpoint; a human admin session is required")
			return
		}
		c.Next()
	}
}
