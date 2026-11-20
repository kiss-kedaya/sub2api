//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// scopedAuditRouter 按生产顺序装配认证与审计中间件：
// adminAuth → auditLog → handler。
func scopedAuditRouter(t *testing.T, auditRepo *auditCaptureRepository, settings *service.SettingService, users *service.UserService) (*gin.Engine, *service.AuditLogService) {
	t.Helper()
	audit := service.NewAuditLogService(auditRepo, nil)
	audit.Start()
	r := gin.New()
	admin := r.Group("/api/v1/admin")
	admin.Use(gin.HandlerFunc(NewAdminAuthMiddleware(nil, users, settings, audit, nil)))
	admin.Use(gin.HandlerFunc(NewAuditLogMiddleware(audit)))
	admin.POST("/users/:id/balance", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	admin.PUT("/settings", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	admin.PUT("/accounts/:id", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return r, audit
}

func scopedAuditSettings(t *testing.T, permissions ...string) (*service.SettingService, string) {
	t.Helper()
	repo := &adminAuthKeyRepo{}
	settings := service.NewSettingService(nil, nil)
	settings.SetScopedAdminKeyRepository(repo)
	key, _, err := settings.CreateScopedAdminKey(context.Background(), service.ScopedAdminKeyInput{
		Name:    "审计用受限密钥",
		Enabled: true,
		Policy:  adminaccess.Policy{GlobalPermissions: permissions},
	}, 9)
	require.NoError(t, err)
	return settings, key
}

func scopedAuditUsers() *service.UserService {
	return service.NewUserService(&stubUserRepo{getByID: func(_ context.Context, id int64) (*service.User, error) {
		return &service.User{ID: id, Role: service.RoleAdmin, Status: service.StatusActive}, nil
	}}, nil, nil, nil)
}

// 受限密钥的变更类请求必须留下与旧版 key 同级的完整审计记录：
// 真实动作名、脱敏后的请求体、路径参数与密钥身份，且只落一条（不能重复）。
func TestScopedAdminKeyWriteAuditKeepsFullDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &auditCaptureRepository{}
	settings, key := scopedAuditSettings(t, "users.balance.write")
	r, audit := scopedAuditRouter(t, repo, settings, scopedAuditUsers())

	body := `{"balance":12.5,"operation":"add","notes":"audit-probe"}`
	req := httptest.NewRequest("POST", "/api/v1/admin/users/321/balance", strings.NewReader(body))
	req.Header.Set("x-api-key", key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	audit.Stop()

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.logs, 1, "受限密钥请求不能重复记账")
	entry := repo.logs[0]
	require.Equal(t, "admin_api_key", entry.AuthMethod)
	require.Equal(t, "POST", entry.Method)
	require.Equal(t, http.StatusNoContent, entry.StatusCode)
	// 1) 动作必须由真实路由推导，而不是统一的 admin.api_key.request。
	require.NotEqual(t, "admin.api_key.request", entry.Action)
	require.Contains(t, entry.Action, "admin.users.balance")
	// 2) 请求体必须经脱敏后入审计，否则事后无法还原改了什么。
	require.Contains(t, entry.RequestBody, "audit-probe")
	require.NotContains(t, entry.RequestBody, key)
	// 3) 密钥身份必须随主记录保留。
	require.NotNil(t, entry.Extra["admin_key_id"])
	require.Equal(t, "审计用受限密钥", entry.Extra["admin_key_name"])
}

// 鉴权拒绝发生在审计中间件之前，必须由认证层单独补记，且不得回显密钥。
func TestScopedAdminKeyDeniedRequestStillAudited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &auditCaptureRepository{}
	settings, key := scopedAuditSettings(t, "users.read")
	r, audit := scopedAuditRouter(t, repo, settings, scopedAuditUsers())

	req := httptest.NewRequest("PUT", "/api/v1/admin/settings", strings.NewReader(`{"step_up_enabled":false}`))
	req.Header.Set("x-api-key", key)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
	audit.Stop()

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.logs, 1)
	entry := repo.logs[0]
	require.Equal(t, http.StatusForbidden, entry.StatusCode)
	require.Equal(t, "permission_denied", entry.Extra["denial_reason"])
	require.NotNil(t, entry.Extra["admin_key_id"])
	require.NotContains(t, entry.Path, key)
	require.NotContains(t, entry.RequestBody, key)
}

// 旧版全权 key 的审计路径不能被本次修复改变。
func TestLegacyAdminKeyAuditUnchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &auditCaptureRepository{}
	settings := service.NewSettingService(fakeSettingRepo{values: map[string]string{
		service.SettingKeyAdminAPIKey: "admin-legacy",
	}}, nil)
	r, audit := scopedAuditRouter(t, repo, settings, scopedAuditUsers())

	req := httptest.NewRequest("PUT", "/api/v1/admin/accounts/7", strings.NewReader(`{"name":"legacy-probe"}`))
	req.Header.Set("x-api-key", "admin-legacy")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	audit.Stop()

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.logs, 1)
	entry := repo.logs[0]
	require.Equal(t, "admin_api_key", entry.AuthMethod)
	require.Equal(t, "admin.accounts.update", entry.Action)
	require.Contains(t, entry.RequestBody, "legacy-probe")
}

// 非法受限密钥的认证失败仍要留痕，且不含任何密钥材料。
func TestScopedAdminKeyInvalidKeyAudited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &auditCaptureRepository{}
	settings := service.NewSettingService(nil, nil)
	settings.SetScopedAdminKeyRepository(&adminAuthKeyRepo{})
	r, audit := scopedAuditRouter(t, repo, settings, scopedAuditUsers())

	req := httptest.NewRequest("PUT", "/api/v1/admin/accounts/7", strings.NewReader(`{}`))
	req.Header.Set("x-api-key", "adm_sc_not-a-real-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	audit.Stop()

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Len(t, repo.logs, 1)
	require.Equal(t, http.StatusUnauthorized, repo.logs[0].StatusCode)
	require.NotContains(t, repo.logs[0].Path, "adm_sc_")
}
