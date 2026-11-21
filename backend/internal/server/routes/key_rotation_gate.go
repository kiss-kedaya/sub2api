package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

// applyKeyRotationGate 在路由组上挂载密钥轮换门禁。
//
// 路由单测通常用最小参数注册 handler（门禁为 nil）；nil 时必须保持原行为而不是
// 在每次请求时 panic，生产装配由 wire 保证非 nil。
func applyKeyRotationGate(group *gin.RouterGroup, gate middleware.KeyRotationGateMiddleware) {
	if group == nil || gate == nil {
		return
	}
	group.Use(gin.HandlerFunc(gate))
}
