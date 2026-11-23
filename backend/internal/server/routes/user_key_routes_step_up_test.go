package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 管理员可用性回归（#567 之后的安全审计 P1）：
//
// 审计担心「新前端创建 Key 会调 POST /api/v1/user/api-keys/:id/reveal-authorize，
// 该路由落在 step-up 检查范围内，管理员必然开 TOTP → 创建流程被 403
// STEP_UP_REQUIRED 打断」。这里用真实路由表把结论钉死：
//
//  1. step-up 门控只挂在 admin 路由组（adminStepUpRouteGuard），用户侧 Key 路由
//     不经过它，因此管理员（带 TOTP 的真人 JWT）调用创建 / 轮换 / 二次查看授权
//     不会被 403 STEP_UP_REQUIRED 打断；
//  2. 打开 step_up_enabled 也一样 —— 这两套门互不相干：用户侧由 handler 自己的
//     三选一身份校验（密码 / TOTP / Passkey）负责；
//  3. reveal-authorize 仍是用户作用域路由，不属于 adminroutepolicy 的分类范围
//     （策略表按 /api/v1/admin* 分类），所以「加入管理面免 step-up 清单」既不
//     适用也不必要；要改 policy 的前提是把它挪进 admin 前缀，而它必须留在用户侧。

// stepUpProbeUserRepo 让创建流程能走到「用户存在」这一步。
type stepUpProbeUserRepo struct {
	service.UserRepository
	user *service.User
}

func (r *stepUpProbeUserRepo) GetByID(_ context.Context, id int64) (*service.User, error) {
	if r.user == nil || r.user.ID != id {
		return nil, service.ErrUserNotFound
	}
	return r.user, nil
}

func TestUserKeyRoutesAreNotGatedByAdminStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// step_up_enabled=true：管理面会把敏感路由统统升级成 step-up 门控。
	settings := service.NewSettingService(&cryptoRouteSettingsRepo{values: map[string]string{
		service.SettingKeyStepUpEnabled: "true",
	}}, nil)
	jwt := middleware.JWTAuthMiddleware(func(c *gin.Context) {
		// 管理员真人会话：JWT + 已开 TOTP，但没有 step-up grant。
		c.Set("auth_method", service.AuditAuthMethodJWT)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
		c.Set(middleware.ContextKeySessionID, "session-admin")
		c.Next()
	})
	noopAudit := middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })

	admin := &service.User{ID: 1, Email: "admin@example.com", Role: service.RoleAdmin, Status: service.StatusActive, TotpEnabled: true, SignupSource: "email"}
	require.NoError(t, admin.SetPassword("admin-correct-password"))
	apiKeyService := service.NewAPIKeyService(
		&stepUpProbeAPIKeyRepo{}, &stepUpProbeUserRepo{user: admin}, nil, nil, nil, nil, &config.Config{})
	// 身份校验服务故意不装配：请求进到 handler 后返回 VERIFY_UNAVAILABLE。
	// 关键是绝不出现 STEP_UP 系列机器码（那才代表被管理面门控拦了）。
	h := &handler.Handlers{APIKey: handler.NewAPIKeyHandler(apiKeyService, nil)}
	router := gin.New()
	v1 := router.Group("/api/v1")
	RegisterUserRoutes(v1, h, jwt, noopAudit, nil, settings, nil)

	for _, route := range []struct{ method, path, body string }{
		// 创建流程会调的两条用户侧接口（因子列表 + 二次查看授权）。
		{http.MethodGet, "/api/v1/user/security/verification-factors", ""},
		{http.MethodPost, "/api/v1/user/api-keys/1/reveal-authorize", `{"method":"totp","totp_code":"123456"}`},
		// 三个下发 / 变更明文的入口。
		{http.MethodPost, "/api/v1/keys", `{"name":"admin-created"}`},
		{http.MethodPut, "/api/v1/keys/1", `{"regenerate_key":true}`},
		{http.MethodPost, "/api/v1/keys/batch-rotate", `{"ids":[1]}`},
	} {
		req := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.NotEqual(t, http.StatusForbidden, rec.Code,
			"%s %s must not be blocked by admin step-up (got %d: %s)",
			route.method, route.path, rec.Code, rec.Body.String())
		for _, marker := range []string{
			"STEP_UP_REQUIRED",
			"STEP_UP_TOTP_NOT_ENABLED",
			"STEP_UP_ADMIN_API_KEY_FORBIDDEN",
			"STEP_UP_UNAVAILABLE",
		} {
			require.NotContains(t, rec.Body.String(), marker,
				"%s %s must not go through the admin step-up gate", route.method, route.path)
		}
	}

	// reveal-authorize 必须留在用户作用域：管理面策略表按 /api/v1/admin* 分类，
	// 把它挪进 admin 前缀才是真的需要改 policy。这里锁定现状。
	for _, route := range router.Routes() {
		if strings.Contains(route.Path, "reveal-authorize") {
			require.Equal(t, "/api/v1/user/api-keys/:id/reveal-authorize", route.Path)
			require.NotContains(t, route.Path, "/admin/")
		}
	}
}

// stepUpProbeAPIKeyRepo 只提供「查一把属于本用户的 Key」，其余方法走嵌入接口
// 的 nil 实现（本测试不会走到）。
type stepUpProbeAPIKeyRepo struct {
	service.APIKeyRepository
}

func (r *stepUpProbeAPIKeyRepo) GetByID(_ context.Context, id int64) (*service.APIKey, error) {
	return &service.APIKey{
		ID:     id,
		UserID: 1,
		Name:   "admin-own-key",
		Key:    "fixture-admin-existing-value",
		Status: service.StatusAPIKeyActive,
	}, nil
}

func (r *stepUpProbeAPIKeyRepo) VerifyOwnership(_ context.Context, _ int64, ids []int64) ([]int64, error) {
	return ids, nil
}

func (r *stepUpProbeAPIKeyRepo) Create(_ context.Context, key *service.APIKey) error {
	key.ID = 99
	return nil
}

func (r *stepUpProbeAPIKeyRepo) DeleteWithAudit(_ context.Context, _ int64) error {
	return nil
}

func (r *stepUpProbeAPIKeyRepo) ExistsByKey(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (r *stepUpProbeAPIKeyRepo) Update(_ context.Context, _ *service.APIKey, _ service.APIKeyUpdateFields) error {
	return nil
}
