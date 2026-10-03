package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The Claude redeem route consumes an irreversible credit just like the Codex
// reset-quota route, so it must sit behind exactly the same middleware chain.
func TestClaudeResetRedeemRouteMatchesCodexResetProtection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{Account: &adminhandler.AccountHandler{}, OpenAIOAuth: &adminhandler.OpenAIOAuthHandler{}}}
	chains := map[string][]string{}
	adminAuth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) {
		names := c.HandlerNames()
		chains[c.FullPath()] = names[:len(names)-1]
		if c.GetHeader("Authorization") == "" {
			servermiddleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authorization required")
			return
		}
		servermiddleware.AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "Admin access required")
	})
	auditLog := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepUp := servermiddleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, adminAuth, auditLog, stepUp, nil, nil)

	codex := "/api/v1/admin/openai/accounts/1/reset-quota"
	claude := "/api/v1/admin/accounts/1/claude/reset-credits/redeem"
	for _, path := range []string{codex, claude} {
		for _, auth := range []string{"", "Bearer user-token"} {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, path, nil)
			if auth != "" {
				request.Header.Set("Authorization", auth)
			}
			router.ServeHTTP(recorder, request)
			if auth == "" {
				require.Equal(t, http.StatusUnauthorized, recorder.Code, path)
			} else {
				require.Equal(t, http.StatusForbidden, recorder.Code, path)
			}
		}
	}
	codexChain := chains["/api/v1/admin/openai/accounts/:id/reset-quota"]
	claudeChain := chains["/api/v1/admin/accounts/:id/claude/reset-credits/redeem"]
	require.NotEmpty(t, codexChain)
	// 两条链必须共享同一套保护性前缀（鉴权 → 限流 → 审计 → 合规守卫）。
	// claude 侧额外带 account-owner-scope 中间件（挂在 /accounts 组上），用来
	// 阻止非主管理员通过 redeem 路由操作别人上传的号池账号——这是必要的。
	// 所以只断言前缀一致、且两边长度相当（不锁定"完全相等"，否则任何加在
	// /accounts 组上的中间件都会让这条用例误报）。
	shorter, longer := codexChain, claudeChain
	if len(longer) < len(shorter) {
		shorter, longer = longer, shorter
	}
	require.Equal(t, strings.Join(shorter, "\n"), strings.Join(longer[:len(shorter)], "\n"))
}
