//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func machineGuardUsers() *service.UserService {
	return service.NewUserService(&stubUserRepo{getByID: func(_ context.Context, id int64) (*service.User, error) {
		return &service.User{ID: id, Role: service.RoleAdmin, Status: service.StatusActive}, nil
	}}, nil, nil, nil)
}

func machineGuardRouter(settings *service.SettingService, register func(admin *gin.RouterGroup)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	admin := r.Group("/api/v1/admin")
	admin.Use(gin.HandlerFunc(NewAdminAuthMiddleware(nil, machineGuardUsers(), settings, nil, nil)))
	register(admin)
	return r
}

// 旧版全权 key 默认拒绝：优惠码列表（明文 code）这类敏感只读接口绝不能被机器凭据读到。
func TestLegacyAdminKeyDefaultDeniedOnSensitiveRoute(t *testing.T) {
	const legacyKey = "admin-legacy-machine-key"
	settings := service.NewSettingService(fakeSettingRepo{values: map[string]string{
		service.SettingKeyAdminAPIKey: legacyKey,
	}}, nil)
	called := false
	r := machineGuardRouter(settings, func(admin *gin.RouterGroup) {
		admin.GET("/promo-codes", func(c *gin.Context) {
			called = true
			c.JSON(http.StatusOK, gin.H{"items": []string{"PLAINTEXT-CODE"}})
		})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/promo-codes", nil)
	req.Header.Set("x-api-key", legacyKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
	require.False(t, called, "机器凭据不得进入业务 handler")
	require.NotContains(t, w.Body.String(), "PLAINTEXT-CODE")
}

// 白名单接口（只读统计聚合）允许机器凭据。
func TestLegacyAdminKeyAllowlistedOnAggregateRoute(t *testing.T) {
	const legacyKey = "admin-legacy-machine-key"
	settings := service.NewSettingService(fakeSettingRepo{values: map[string]string{
		service.SettingKeyAdminAPIKey: legacyKey,
	}}, nil)
	r := machineGuardRouter(settings, func(admin *gin.RouterGroup) {
		admin.GET("/dashboard/stats", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/dashboard/stats", nil)
	req.Header.Set("x-api-key", legacyKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
}

// 未归类路由 fail-closed：新增路由没进注册表时机器凭据拿不到访问权。
func TestLegacyAdminKeyDeniedOnUnclassifiedRoute(t *testing.T) {
	const legacyKey = "admin-legacy-machine-key"
	settings := service.NewSettingService(fakeSettingRepo{values: map[string]string{
		service.SettingKeyAdminAPIKey: legacyKey,
	}}, nil)
	r := machineGuardRouter(settings, func(admin *gin.RouterGroup) {
		admin.GET("/zz-brand-new-route", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/zz-brand-new-route", nil)
	req.Header.Set("x-api-key", legacyKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
}

// 未配置旧版 key 时：错误 key 与"没配置"返回完全一致的 401，不泄露配置状态。
func TestLegacyAdminKeyUnconfiguredAndWrongKeyLookIdentical(t *testing.T) {
	settings := service.NewSettingService(fakeSettingRepo{values: map[string]string{}}, nil)
	r := machineGuardRouter(settings, func(admin *gin.RouterGroup) {
		admin.GET("/dashboard/stats", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	})

	unconfigured := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/dashboard/stats", nil)
	req.Header.Set("x-api-key", "admin-not-configured")
	r.ServeHTTP(unconfigured, req)

	wrongConfigured := httptest.NewRecorder()
	configuredSettings := service.NewSettingService(fakeSettingRepo{values: map[string]string{
		service.SettingKeyAdminAPIKey: "admin-real-key",
	}}, nil)
	r2 := machineGuardRouter(configuredSettings, func(admin *gin.RouterGroup) {
		admin.GET("/dashboard/stats", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	})
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/admin/dashboard/stats", nil)
	req2.Header.Set("x-api-key", "admin-wrong-key")
	r2.ServeHTTP(wrongConfigured, req2)

	require.Equal(t, http.StatusUnauthorized, unconfigured.Code)
	require.Equal(t, http.StatusUnauthorized, wrongConfigured.Code)
	require.Equal(t, unconfigured.Body.String(), wrongConfigured.Body.String(),
		"不能通过错误信息区分 key 是否已配置")
}

// 机器凭据访问敏感资源被拒必须记安全事件，且审计里只能出现前缀。
func TestLegacyAdminKeySensitiveDenialRecordsSecurityEvent(t *testing.T) {
	const legacyKey = "admin-legacy-machine-key"
	repo := &auditCaptureRepository{}
	audit := service.NewAuditLogService(repo, nil)
	audit.Start()

	settings := service.NewSettingService(fakeSettingRepo{values: map[string]string{
		service.SettingKeyAdminAPIKey: legacyKey,
	}}, nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	admin := r.Group("/api/v1/admin")
	admin.Use(gin.HandlerFunc(NewAdminAuthMiddleware(nil, machineGuardUsers(), settings, audit, nil)))
	// 明文导出仍是 step-up_required：用它验证机器凭据访问敏感资源时的安全事件。
	admin.GET("/proxies/data", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/proxies/data", nil)
	req.Header.Set("x-api-key", legacyKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
	audit.Stop()

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.logs, 1)
	entry := repo.logs[0]
	require.Equal(t, service.AuditAuthMethodAdminAPIKey, entry.AuthMethod)
	require.Equal(t, ActionAdminMachineSensitiveDenied, entry.Action)
	require.Equal(t, http.StatusForbidden, entry.StatusCode)
	require.Equal(t, "/api/v1/admin/proxies/data", entry.Path)
	require.Equal(t, "step_up_required", entry.Extra["policy_class"])
	require.Contains(t, entry.CredentialMasked, "admin-")
	require.NotContains(t, entry.CredentialMasked, legacyKey)
	require.NotContains(t, entry.Extra["credential_prefix"], "machine-key")
}

// 受限管理密钥（adm_sc_）同样是机器凭据：敏感资源要求真人 step-up，
// 即使持钥者给了 TOTP 也无法通过（机器身份不能完成 step-up）。
func TestScopedAdminKeyCannotPassStepUpOnSensitiveRoute(t *testing.T) {
	settings := service.NewSettingService(fakeSettingRepo{values: map[string]string{
		service.SettingKeyStepUpEnabled: "true",
	}}, nil)
	realStepUp := NewStepUpAuthMiddleware(nil, nil, settings)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("auth_method", service.AuditAuthMethodScopedAdminAPIKey)
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 9})
		c.Next()
	})
	r.GET("/api/v1/admin/promo-codes", gin.HandlerFunc(realStepUp), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/promo-codes", nil))
	require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
	require.False(t, strings.Contains(w.Body.String(), "STEP_UP_REQUIRED"))
}

// 旧版全权 key 的成功调用也要有审计记录（只含前缀）。
func TestLegacyAdminKeyAllowedCallIsAudited(t *testing.T) {
	const legacyKey = "admin-legacy-machine-key"
	repo := &auditCaptureRepository{}
	audit := service.NewAuditLogService(repo, nil)
	audit.Start()

	settings := service.NewSettingService(fakeSettingRepo{values: map[string]string{
		service.SettingKeyAdminAPIKey: legacyKey,
	}}, nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	admin := r.Group("/api/v1/admin")
	admin.Use(gin.HandlerFunc(NewAdminAuthMiddleware(nil, machineGuardUsers(), settings, audit, nil)))
	admin.GET("/dashboard/stats", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/dashboard/stats", nil)
	req.Header.Set("x-api-key", legacyKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
	audit.Stop()

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.logs, 1)
	entry := repo.logs[0]
	require.Equal(t, ActionAdminMachineRequest, entry.Action)
	require.Equal(t, http.StatusNoContent, entry.StatusCode)
	require.NotContains(t, entry.CredentialMasked, legacyKey)
}

// 受限密钥访问敏感 GET 被 step-up 拒绝时，审计中间件要补记安全事件
// （Get 敏感读不在常规审计白名单里，不能因为这样就没有痕迹）。
func TestScopedAdminKeySensitiveDenialIsAudited(t *testing.T) {
	repo := &auditCaptureRepository{}
	audit := service.NewAuditLogService(repo, nil)
	audit.Start()
	settings := service.NewSettingService(fakeSettingRepo{values: map[string]string{
		service.SettingKeyStepUpEnabled: "true",
	}}, nil)
	realStepUp := NewStepUpAuthMiddleware(nil, nil, settings)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	admin := r.Group("/api/v1/admin")
	admin.Use(func(c *gin.Context) {
		c.Set("auth_method", service.AuditAuthMethodScopedAdminAPIKey)
		c.Set(ContextKeyAdminCredentialKind, AdminCredentialKindScoped)
		c.Set(ContextKeyAdminCredentialPrefix, "adm_sc_test")
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 9})
		c.Next()
	})
	admin.Use(gin.HandlerFunc(NewAuditLogMiddleware(audit)))
	admin.GET("/promo-codes", gin.HandlerFunc(realStepUp), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/promo-codes", nil))
	require.Equal(t, http.StatusForbidden, w.Code)
	audit.Stop()

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.logs, 1)
	entry := repo.logs[0]
	require.Equal(t, ActionAdminMachineSensitiveDenied, entry.Action)
	require.Equal(t, service.AuditAuthMethodScopedAdminAPIKey, entry.AuthMethod)
	require.Equal(t, "step_up_required", entry.Extra["policy_class"])
	require.Equal(t, "scoped", entry.Extra["credential_kind"])
	require.Equal(t, http.StatusForbidden, entry.StatusCode)
}

// 2026-11-23 放开的低风险写接口（上游模型目录同步/预览、邮件模板预览）同样
// 不允许机器凭据：放开的是「真人会话不再弹 TOTP」，不是「把写接口开给 x-api-key」。
// 这些路由没有进 machine_allowlisted，认证层必须默认拒绝且不进业务 handler。
func TestReleasedLowRiskWriteRoutesRejectMachineCredential(t *testing.T) {
	const legacyKey = "admin-legacy-machine-key"
	settings := service.NewSettingService(fakeSettingRepo{values: map[string]string{
		service.SettingKeyAdminAPIKey: legacyKey,
	}}, nil)

	called := false
	r := machineGuardRouter(settings, func(admin *gin.RouterGroup) {
		admin.POST("/accounts/:id/models/sync-upstream", func(c *gin.Context) {
			called = true
			c.Status(http.StatusNoContent)
		})
		admin.POST("/accounts/models/sync-upstream-preview", func(c *gin.Context) {
			called = true
			c.Status(http.StatusNoContent)
		})
		admin.POST("/settings/email-template-preview", func(c *gin.Context) {
			called = true
			c.Status(http.StatusNoContent)
		})
	})

	for _, path := range []string{
		"/api/v1/admin/accounts/1/models/sync-upstream",
		"/api/v1/admin/accounts/models/sync-upstream-preview",
		"/api/v1/admin/settings/email-template-preview",
	} {
		t.Run(path, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("x-api-key", legacyKey)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
			require.False(t, called, "机器凭据不得进入业务 handler")
		})
	}
}
