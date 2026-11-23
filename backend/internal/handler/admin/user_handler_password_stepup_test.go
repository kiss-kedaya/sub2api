//go:build unit

package admin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const passwordStepUpBody = `{"password":"new-strong-password"}`

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// newPasswordStepUpRouter 构造带认证上下文的用户更新路由：
// auth_context → UserHandler.Update（密码修改走 handler 内的 step-up 门控）。
func newPasswordStepUpRouter(t *testing.T, stepUpEnabled bool, authMode string) (*gin.Engine, *globalTotpGrantCache) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := &settingHandlerRepoStub{values: map[string]string{
		service.SettingKeyTotpEnabled:   "true",
		service.SettingKeyStepUpEnabled: boolString(stepUpEnabled),
	}}
	settings := service.NewSettingService(repo, &config.Config{})
	actorRepo := &globalTotpActorRepo{actor: &service.User{
		ID: 42, Role: service.RoleAdmin, Status: service.StatusActive, TotpEnabled: true,
	}}
	cache := &globalTotpGrantCache{grants: map[globalTotpGrantKey]bool{}}
	totp := service.NewTotpService(actorRepo, nil, cache, settings, nil, nil)
	users := service.NewUserService(actorRepo, repo, nil, nil)
	h := NewUserHandler(newStubAdminService(), nil, nil, nil, totp, users, settings)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Set(middleware.ContextKeySessionID, "stepup-session")
		c.Set("auth_method", authMode)
		c.Next()
	})
	r.PUT("/api/v1/admin/users/:id", h.Update)
	return r, cache
}

// 修改他人密码等同于账号接管：无认证上下文时门控直接中止（401）；
// 普通资料编辑不触发门控（继续走业务层 200）。
func TestUpdateUserPasswordRequiresStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewUserHandler(newStubAdminService(), nil, nil, nil, nil, nil, nil)
	r := gin.New()
	r.PUT("/api/v1/admin/users/:id", h.Update)

	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"password_change_triggers_gate", passwordStepUpBody, http.StatusUnauthorized},
		{"profile_edit_skips_gate", `{"username":"renamed"}`, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/users/1", bytes.NewReader([]byte(tc.body)))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, req)
			require.Equal(t, tc.want, rec.Code, rec.Body.String())
		})
	}
}

// 未验证被拦、验证通过放行、机器凭据一律 403 三种结果。
func TestUpdateUserPasswordStepUpGrantMatrix(t *testing.T) {
	for _, tc := range []struct {
		name     string
		granted  bool
		authMode string
		wantCode int
		wantText string
	}{
		{name: "no_grant", granted: false, authMode: service.AuditAuthMethodJWT, wantCode: http.StatusForbidden, wantText: "STEP_UP_REQUIRED"},
		{name: "with_grant", granted: true, authMode: service.AuditAuthMethodJWT, wantCode: http.StatusOK},
		{name: "admin_api_key_even_with_grant", granted: true, authMode: service.AuditAuthMethodAdminAPIKey, wantCode: http.StatusForbidden, wantText: "STEP_UP_ADMIN_API_KEY_FORBIDDEN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, cache := newPasswordStepUpRouter(t, true, tc.authMode)
			if tc.granted {
				require.NoError(t, cache.SetStepUpGrant(context.Background(), 42, "stepup-session", service.FactorTOTP, service.StepUpGrantTTL))
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/users/1", bytes.NewReader([]byte(passwordStepUpBody)))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, req)
			require.Equal(t, tc.wantCode, rec.Code, rec.Body.String())
			if tc.wantText != "" {
				require.Contains(t, rec.Body.String(), tc.wantText)
			}
		})
	}
}

// step_up 功能关闭时保持既有语义：API Key 也被拒绝（与其它 step-up 路由一致）。
func TestUpdateUserPasswordStepUpDisabledSkipsGate(t *testing.T) {
	r, _ := newPasswordStepUpRouter(t, false, service.AuditAuthMethodAdminAPIKey)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/users/1", bytes.NewReader([]byte(passwordStepUpBody)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}
