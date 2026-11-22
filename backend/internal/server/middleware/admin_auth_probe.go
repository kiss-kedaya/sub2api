package middleware

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// adminAuthProbePathPrefix 只统计管理面路径。中间件只在 admin group 上注册，
// 这里再兜一层前缀判断，避免以后被误挂到全局路由时把普通 401/403 也算进来。
const adminAuthProbePathPrefix = "/api/v1/admin"

// AdminAuthProbeRecorder 由 service.AdminAuthProbeMonitor 实现。
// 返回 bool 仅用于单测判断本次是否触发了告警。
type AdminAuthProbeRecorder interface {
	RecordAdminAuthFailure(ctx context.Context, input service.AdminAuthProbeInput) bool
}

// AdminAuthProbeMonitor 观察 admin 路由最终返回的 401/403。
//
// 必须挂在 adminAuth 之前：adminAuth 会 Abort，只有先 c.Next() 才能拿到最终状态码。
// 这里只读取状态码、路由模板、来源 IP、UA 前三类数据，绝不读取/记录任何
// Authorization、x-api-key 或其它 header 值。
func AdminAuthProbeMonitor(recorder AdminAuthProbeRecorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		if recorder == nil || c == nil || c.Request == nil {
			c.Next()
			return
		}
		c.Next()
		status := c.Writer.Status()
		if status != 401 && status != 403 {
			return
		}
		path := c.FullPath()
		if path == "" && c.Request.URL != nil {
			path = c.Request.URL.Path
		}
		if !strings.HasPrefix(path, adminAuthProbePathPrefix) {
			return
		}
		recorder.RecordAdminAuthFailure(c.Request.Context(), service.AdminAuthProbeInput{
			ClientIP:   SecurityClientIP(c),
			Method:     c.Request.Method,
			Path:       path,
			StatusCode: status,
			UserAgent:  c.Request.UserAgent(),
		})
	}
}
