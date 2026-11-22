//go:build unit

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// createStepUpAdminStub 只实现 CreateUser，用于验证 handler 的条件门控与请求透传。
type createStepUpAdminStub struct {
	service.AdminService
	createCalls int
	lastInput   *service.CreateUserInput
}

func (s *createStepUpAdminStub) CreateUser(_ context.Context, input *service.CreateUserInput) (*service.User, error) {
	s.createCalls++
	s.lastInput = input
	return &service.User{ID: 100, Email: input.Email, Role: service.RoleUser, Status: service.StatusActive}, nil
}

type createStepUpHarness struct {
	stepUpEnabled bool
	grant         bool
	adminAPIKey   bool
}

func setupCreateUserStepUpRouter(t *testing.T, h createStepUpHarness) (*gin.Engine, *createStepUpAdminStub) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	actorRepo := &globalTotpActorRepo{actor: &service.User{
		ID: 42, Role: service.RoleAdmin, Status: service.StatusActive, TotpEnabled: true,
	}}
	cache := &globalTotpGrantCache{grants: map[globalTotpGrantKey]bool{}}
	if h.grant {
		require.NoError(t, cache.SetStepUpGrant(context.Background(), 42, "stepup-session", service.StepUpGrantTTL))
	}
	settings := service.NewSettingService(&settingHandlerRepoStub{values: map[string]string{
		service.SettingKeyTotpEnabled:   "true",
		service.SettingKeyStepUpEnabled: boolString(h.stepUpEnabled),
	}}, &config.Config{})
	totp := service.NewTotpService(actorRepo, nil, cache, settings, nil, nil)
	users := service.NewUserService(actorRepo, nil, nil, nil)

	adminSvc := &createStepUpAdminStub{}
	handler := NewUserHandler(adminSvc, nil, nil, nil, totp, users, settings)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
		c.Set(middleware.ContextKeySessionID, "stepup-session")
		if h.adminAPIKey {
			c.Set("auth_method", service.AuditAuthMethodAdminAPIKey)
		} else {
			c.Set("auth_method", service.AuditAuthMethodJWT)
		}
		c.Next()
	})
	router.POST("/api/v1/admin/users", handler.Create)
	return router, adminSvc
}

func postCreateUser(t *testing.T, r *gin.Engine, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	return rec
}

// 建号请求带非零余额/授权分组时，即使目标是普通用户也必须 step-up：
// 否则「无 step-up 建号」就是一条绕过 step-up 直接发钱/授权的路径。
func TestCreateUserSensitivePayloadRequiresStepUp(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload map[string]any
	}{
		{
			name:    "non_zero_balance_without_grant",
			payload: map[string]any{"email": "paid@example.com", "password": "pass123", "balance": 100},
		},
		{
			name:    "allowed_groups_without_grant",
			payload: map[string]any{"email": "granted@example.com", "password": "pass123", "allowed_groups": []int64{3}},
		},
		{
			name:    "admin_target_without_grant",
			payload: map[string]any{"email": "new-admin@example.com", "password": "pass123", "role": "admin"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, adminSvc := setupCreateUserStepUpRouter(t, createStepUpHarness{stepUpEnabled: true})
			rec := postCreateUser(t, router, tc.payload)
			require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), "STEP_UP_REQUIRED")
			require.Zero(t, adminSvc.createCalls, "门控未通过时不得进入建号逻辑")
		})
	}
}

// 有 grant 的普通建号（不带余额/授权分组）应正常创建成功。
func TestCreateUserPlainPayloadWithGrantSucceeds(t *testing.T) {
	router, adminSvc := setupCreateUserStepUpRouter(t, createStepUpHarness{stepUpEnabled: true, grant: true})
	rec := postCreateUser(t, router, map[string]any{
		"email": "plain@example.com", "password": "pass123", "username": "plain",
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, adminSvc.createCalls)
	require.NotNil(t, adminSvc.lastInput)
	require.Nil(t, adminSvc.lastInput.Balance)
}

// 机器凭据（admin API key）即使持有 grant 也不能建号：建号只能由真人会话完成。
// 用带余额的请求体触发 handler 条件门控（路由级门控在 routes 包已单测）。
func TestCreateUserRejectsAdminAPIKeyMachineCredential(t *testing.T) {
	router, adminSvc := setupCreateUserStepUpRouter(t, createStepUpHarness{stepUpEnabled: true, grant: true, adminAPIKey: true})
	rec := postCreateUser(t, router, map[string]any{"email": "machine@example.com", "password": "pass123", "balance": 10})
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
	require.Zero(t, adminSvc.createCalls)
}

// step_up 功能开关关闭时保持旧行为（不新增条件门控），与其它 step-up 路由一致。
func TestCreateUserStepUpDisabledKeepsLegacyBehavior(t *testing.T) {
	router, adminSvc := setupCreateUserStepUpRouter(t, createStepUpHarness{})
	rec := postCreateUser(t, router, map[string]any{
		"email": "legacy@example.com", "password": "pass123", "balance": 50,
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, adminSvc.createCalls)
}
