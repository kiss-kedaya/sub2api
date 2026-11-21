package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/adminroutepolicy"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

// adminStepUpRouteGuard 是「敏感资源 = 敏感读」的统一落点。
//
// 集中注册表 adminroutepolicy 声明了哪些路由属于 step_up_required（卡密、上游
// 凭证、代理密码、渠道密钥、用户 API Key、钱包/私钥、导出类等，任何方法都算）。
// 该中间件在管理面路由组入口统一执行 step-up 2FA，不再依赖每个 handler 记得
// 手工挂 gin.HandlerFunc(stepUpAuth)。未归类路由按 fail-closed 同样要求 step-up。
//
// 注意：机器凭据（auth_method=admin_api_key）在认证层就已经被默认拒绝，只会
// 进入 machine_allowlisted 路由，因此这里实际只面向真人 JWT 会话。
func adminStepUpRouteGuard(stepUp middleware.StepUpAuthMiddleware) gin.HandlerFunc {
	enforce := gin.HandlerFunc(stepUp)
	return func(c *gin.Context) {
		if !adminroutepolicy.IsStepUpRequired(c.Request.Method, c.FullPath()) {
			c.Next()
			return
		}
		if enforce == nil {
			// 装配缺失时 fail-closed，绝不能因为漏注入中间件就放行敏感资源。
			middleware.AbortWithError(c, 503, "STEP_UP_UNAVAILABLE", "Step-up verification service unavailable")
			return
		}
		// stepUpAuth 内部会在通过时调用 c.Next()，因此这里不再重复推进链。
		enforce(c)
	}
}
