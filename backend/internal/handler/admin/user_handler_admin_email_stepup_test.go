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

const adminEmailStepUpTargetEmail = "admin-target@example.com"

// newUpdateEmailStepUpRouter 构造一条「管理员改指定账号邮箱」的路径：
// 操作者是 admin 42（已启用 TOTP），带 session 与 auth_method。
func newUpdateEmailStepUpRouter(t *testing.T, target service.User, stepUpEnabled bool, authMode string) (*gin.Engine, *globalTotpGrantCache) {
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
	adminSvc := newStubAdminService()
	adminSvc.users = []service.User{target}
	h := NewUserHandler(adminSvc, nil, nil, nil, totp, users, settings)

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

func newAdminEmailStepUpRouter(t *testing.T, stepUpEnabled bool, authMode string) (*gin.Engine, *globalTotpGrantCache) {
	return newUpdateEmailStepUpRouter(t, service.User{
		ID: 1, Email: adminEmailStepUpTargetEmail, Role: service.RoleAdmin, Status: service.StatusActive,
	}, stepUpEnabled, authMode)
}

func updateUserEmail(t *testing.T, r *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/users/1", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	return rec
}

// 改「管理员」账号的邮箱会转移登录身份与密码找回目的地：未 step-up 必须被拦。
func TestUpdateAdminUserEmailRequiresStepUp(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		granted  bool
		authMode string
		wantCode int
		wantText string
	}{
		{
			name: "admin_email_change_without_grant", body: `{"email":"attacker@example.com"}`,
			granted: false, authMode: service.AuditAuthMethodJWT, wantCode: http.StatusForbidden, wantText: "STEP_UP_REQUIRED",
		},
		{
			name: "admin_email_change_with_grant", body: `{"email":"new-admin@example.com"}`,
			granted: true, authMode: service.AuditAuthMethodJWT, wantCode: http.StatusOK,
		},
		{
			name: "admin_email_change_admin_api_key_even_with_grant", body: `{"email":"attacker@example.com"}`,
			granted: true, authMode: service.AuditAuthMethodAdminAPIKey, wantCode: http.StatusForbidden, wantText: "STEP_UP_ADMIN_API_KEY_FORBIDDEN",
		},
		{
			name: "admin_same_email_not_gated", body: `{"email":"` + adminEmailStepUpTargetEmail + `"}`,
			granted: false, authMode: service.AuditAuthMethodJWT, wantCode: http.StatusOK,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, cache := newAdminEmailStepUpRouter(t, true, tc.authMode)
			if tc.granted {
				require.NoError(t, cache.SetStepUpGrant(context.Background(), 42, "stepup-session", service.FactorTOTP, service.StepUpGrantTTL))
			}
			rec := updateUserEmail(t, r, tc.body)
			require.Equal(t, tc.wantCode, rec.Code, rec.Body.String())
			if tc.wantText != "" {
				require.Contains(t, rec.Body.String(), tc.wantText)
			}
		})
	}
}

// 普通真人账号改邮箱同样可以「改邮箱 → 忘记密码」接管：未 step-up 必须被拦。
func TestUpdateNormalUserEmailRequiresStepUp(t *testing.T) {
	r, _ := newUpdateEmailStepUpRouter(t, service.User{
		ID: 1, Email: "plain-user@example.com", Role: service.RoleUser, Status: service.StatusActive,
	}, true, service.AuditAuthMethodJWT)

	rec := updateUserEmail(t, r, `{"email":"attacker@example.com"}`)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "STEP_UP_REQUIRED")
}

// 有 grant 时普通用户邮箱变更放行。
func TestUpdateNormalUserEmailWithGrantSucceeds(t *testing.T) {
	r, cache := newUpdateEmailStepUpRouter(t, service.User{
		ID: 1, Email: "plain-user@example.com", Role: service.RoleUser, Status: service.StatusActive,
	}, true, service.AuditAuthMethodJWT)
	require.NoError(t, cache.SetStepUpGrant(context.Background(), 42, "stepup-session", service.FactorTOTP, service.StepUpGrantTTL))

	rec := updateUserEmail(t, r, `{"email":"new-plain@example.com"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// 第三方 OAuth 合成邮箱没有真实收件箱：首次补全真实邮箱不触发 step-up。
func TestUpdateSyntheticEmailUserEmailNotGated(t *testing.T) {
	r, _ := newUpdateEmailStepUpRouter(t, service.User{
		ID: 1, Email: "legacy-user" + service.LinuxDoConnectSyntheticEmailDomain, Role: service.RoleUser, Status: service.StatusActive,
	}, true, service.AuditAuthMethodJWT)

	rec := updateUserEmail(t, r, `{"email":"real@example.com"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// step_up 开关关闭时保持既有行为（不开启新门控）。
func TestUpdateAdminUserEmailStepUpDisabledSkipsGate(t *testing.T) {
	r, _ := newAdminEmailStepUpRouter(t, false, service.AuditAuthMethodJWT)
	rec := updateUserEmail(t, r, `{"email":"new-admin@example.com"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// 普通用户改邮箱在开关关闭时同样保持旧行为。
func TestUpdateNormalUserEmailStepUpDisabledSkipsGate(t *testing.T) {
	r, _ := newUpdateEmailStepUpRouter(t, service.User{
		ID: 1, Email: "plain-user@example.com", Role: service.RoleUser, Status: service.StatusActive,
	}, false, service.AuditAuthMethodJWT)
	rec := updateUserEmail(t, r, `{"email":"new-plain@example.com"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}
