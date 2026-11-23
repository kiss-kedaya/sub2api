//go:build unit

package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// machineRouteUsers 让旧版 key 解析出的主管理员是 ID 1（PrimaryAdminUserID）。
type machineRouteUsers struct{ service.UserRepository }

func (*machineRouteUsers) GetByID(_ context.Context, id int64) (*service.User, error) {
	if id != middleware.PrimaryAdminUserID {
		return nil, service.ErrUserNotFound
	}
	return &service.User{ID: middleware.PrimaryAdminUserID, Role: service.RoleAdmin, Status: service.StatusActive}, nil
}

func (*machineRouteUsers) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

// buildMachineCredentialRouter 用真实 adminAuth 装配完整管理面路由树。
// handler 用零值占位即可：被拒绝的请求进不到业务层。
func buildMachineCredentialRouter(t *testing.T, settings *service.SettingService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	users := service.NewUserService(&machineRouteUsers{}, nil, nil, nil)
	auth := middleware.NewAdminAuthMiddleware(nil, users, settings, nil, nil)
	RegisterAdminRoutes(
		r.Group("/api/v1"),
		&handler.Handlers{Admin: &handler.AdminHandlers{}},
		auth,
		middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }),
		stepUpProbe(),
		settings,
		nil,
	)
	return r
}

// 复现并被堵死的原始漏洞：旧版全权机器凭据（x-api-key）可以分页读走卡密列表
// 与卡密详情里的明文 code，而导出接口却要求 2FA，等于 2FA 被绕过。
//
// 修复后：认证层对机器凭据默认拒绝，这两个 GET 直接 403
// STEP_UP_ADMIN_API_KEY_FORBIDDEN，业务 handler 不会执行。
func TestLegacyMachineCredentialCannotReadRedeemCodeReads(t *testing.T) {
	settings := stepUpRouteSettings(map[string]string{
		service.SettingKeyAdminAPIKey: "admin-legacy-machine-key",
	})
	router := buildMachineCredentialRouter(t, settings)

	for _, path := range []string{
		"/api/v1/admin/redeem-codes",
		"/api/v1/admin/redeem-codes/1",
		"/api/v1/admin/redeem-codes/stats",
		// 代理列表/详情泄露明文密码，同一模式。
		"/api/v1/admin/proxies",
		"/api/v1/admin/proxies/1",
		// 上游账号凭证与系统设置密钥。
		"/api/v1/admin/accounts",
		"/api/v1/admin/settings",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("X-API-Key", "admin-legacy-machine-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
		})
	}
}

// 管理员改 Key 分组 / 用户分组配置都是写操作：机器凭据（旧版 x-api-key 与
// 受限 adm_sc_ 密钥）不能调用，必须由真人 2FA 会话完成。认证层的默认拒绝先于
// step-up 门控生效，因此这里断言 403 STEP_UP_ADMIN_API_KEY_FORBIDDEN。
func TestLegacyMachineCredentialCannotWriteUserKeyGroups(t *testing.T) {
	settings := stepUpRouteSettings(map[string]string{
		service.SettingKeyAdminAPIKey: "admin-legacy-machine-key",
	})
	router := buildMachineCredentialRouter(t, settings)

	for _, route := range []struct{ method, path string }{
		// 管理端改单个 Key 分组
		{http.MethodPut, "/api/v1/admin/api-keys/1"},
		// 管理端改用户所属分组（会批量迁移该用户的 Key）
		{http.MethodPost, "/api/v1/admin/users/1/replace-group"},
		// 管理端改用户分组配置（专属倍率 / 可用分组）
		{http.MethodPut, "/api/v1/admin/users/1"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			req := httptest.NewRequest(route.method, route.path, nil)
			req.Header.Set("X-API-Key", "admin-legacy-machine-key")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
		})
	}
}

// 白名单（只读统计聚合）仍允许机器凭据，避免把自动化监控一刀切死。
// 这里用真实 adminAuth 验证认证层判定；dashboard stats 的 handler 为 nil，
// 放行后会在 handler 阶段 panic，因此只断言响应不是 403 门控错误。
func TestLegacyMachineCredentialAllowlistedAggregateNotGated(t *testing.T) {
	settings := stepUpRouteSettings(map[string]string{
		service.SettingKeyAdminAPIKey: "admin-legacy-machine-key",
	})
	router := buildMachineCredentialRouter(t, settings)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/dashboard/stats", nil)
	req.Header.Set("X-API-Key", "admin-legacy-machine-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.NotEqual(t, http.StatusForbidden, w.Code)
	require.NotContains(t, w.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
}
