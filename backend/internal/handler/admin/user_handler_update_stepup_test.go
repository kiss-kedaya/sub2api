package admin

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// updateStepUpAdminStub 提供固定的目标用户，并记录是否真正进入更新逻辑。
type updateStepUpAdminStub struct {
	service.AdminService
	target      *service.User
	getErr      error
	updateCalls int
	lastInput   *service.UpdateUserInput
}

func (s *updateStepUpAdminStub) GetUser(context.Context, int64) (*service.User, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	user := *s.target
	return &user, nil
}

func (s *updateStepUpAdminStub) UpdateUser(_ context.Context, _ int64, input *service.UpdateUserInput) (*service.User, error) {
	s.updateCalls++
	s.lastInput = input
	user := *s.target
	return &user, nil
}

type updateStepUpHarness struct {
	stepUpEnabled bool
	actorTotp     bool
	grant         bool
	adminAPIKey   bool
}

func setupUpdateStepUpRouter(t *testing.T, h updateStepUpHarness) (*gin.Engine, *updateStepUpAdminStub) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	target := &service.User{
		ID:                   42,
		Email:                "target@example.com",
		Role:                 service.RoleUser,
		Status:               service.StatusActive,
		Concurrency:          5,
		RPMLimit:             60,
		AllowedGroups:        []int64{2, 3},
		RestrictPublicGroups: true,
		GroupRates:           map[int64]float64{2: 1.5},
	}
	adminSvc := &updateStepUpAdminStub{target: target}

	actorRepo := &totpDemotionActorRepo{actor: &service.User{ID: 41, Role: service.RoleAdmin, Status: service.StatusActive, TotpEnabled: h.actorTotp}}
	cache := &totpDemotionGrantCache{}
	if h.grant {
		cache.grantUserID = 41
		cache.grantSession = "admin-session"
	}
	totpSvc := service.NewTotpService(actorRepo, nil, cache, nil, nil, nil)
	userSvc := service.NewUserService(actorRepo, nil, nil, nil)
	enabled := "false"
	if h.stepUpEnabled {
		enabled = "true"
	}
	settings := service.NewSettingService(&settingHandlerRepoStub{values: map[string]string{service.SettingKeyStepUpEnabled: enabled}}, nil)
	handler := NewUserHandler(adminSvc, nil, nil, nil, totpSvc, userSvc, settings)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 41})
		c.Set(middleware.ContextKeySessionID, "admin-session")
		if h.adminAPIKey {
			c.Set("auth_method", service.AuditAuthMethodAdminAPIKey)
		} else {
			c.Set("auth_method", "jwt")
		}
	})
	router.PUT("/api/v1/admin/users/:id", handler.Update)
	return router, adminSvc
}

func TestUpdateUserSensitiveFieldsRequireStepUp(t *testing.T) {
	tests := []struct {
		name       string
		harness    updateStepUpHarness
		payload    map[string]any
		wantStatus int
		wantCode   string
	}{
		{
			name:       "disable_user_without_grant",
			harness:    updateStepUpHarness{stepUpEnabled: true, actorTotp: true},
			payload:    map[string]any{"status": "disabled"},
			wantStatus: http.StatusForbidden,
			wantCode:   "STEP_UP_REQUIRED",
		},
		{
			name:       "enable_user_unchanged_status_does_not_trigger",
			harness:    updateStepUpHarness{stepUpEnabled: true, actorTotp: true},
			payload:    map[string]any{"status": "active"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "allowed_groups_change_without_grant",
			harness:    updateStepUpHarness{stepUpEnabled: true, actorTotp: true},
			payload:    map[string]any{"allowed_groups": []int64{2}},
			wantStatus: http.StatusForbidden,
			wantCode:   "STEP_UP_REQUIRED",
		},
		{
			name:       "group_rates_change_without_grant",
			harness:    updateStepUpHarness{stepUpEnabled: true, actorTotp: true},
			payload:    map[string]any{"group_rates": map[string]any{"2": 2.5}},
			wantStatus: http.StatusForbidden,
			wantCode:   "STEP_UP_REQUIRED",
		},
		{
			name:       "group_rates_clear_all_without_grant",
			harness:    updateStepUpHarness{stepUpEnabled: true, actorTotp: true},
			payload:    map[string]any{"group_rates": map[string]any{}},
			wantStatus: http.StatusForbidden,
			wantCode:   "STEP_UP_REQUIRED",
		},
		{
			name:       "concurrency_change_without_grant",
			harness:    updateStepUpHarness{stepUpEnabled: true, actorTotp: true},
			payload:    map[string]any{"concurrency": 9},
			wantStatus: http.StatusForbidden,
			wantCode:   "STEP_UP_REQUIRED",
		},
		{
			name:       "rpm_change_without_grant",
			harness:    updateStepUpHarness{stepUpEnabled: true, actorTotp: true},
			payload:    map[string]any{"rpm_limit": 120},
			wantStatus: http.StatusForbidden,
			wantCode:   "STEP_UP_REQUIRED",
		},
		{
			name:       "restrict_public_groups_change_without_grant",
			harness:    updateStepUpHarness{stepUpEnabled: true, actorTotp: true},
			payload:    map[string]any{"restrict_public_groups": false},
			wantStatus: http.StatusForbidden,
			wantCode:   "STEP_UP_REQUIRED",
		},
		{
			name:       "password_change_without_grant",
			harness:    updateStepUpHarness{stepUpEnabled: true, actorTotp: true},
			payload:    map[string]any{"password": "newpass123"},
			wantStatus: http.StatusForbidden,
			wantCode:   "STEP_UP_REQUIRED",
		},
		{
			name:       "role_promotion_without_grant",
			harness:    updateStepUpHarness{stepUpEnabled: true, actorTotp: true},
			payload:    map[string]any{"role": "admin"},
			wantStatus: http.StatusForbidden,
			wantCode:   "STEP_UP_REQUIRED",
		},
		{
			name:       "ordinary_fields_do_not_trigger",
			harness:    updateStepUpHarness{stepUpEnabled: true, actorTotp: true},
			payload:    map[string]any{"username": "renamed", "notes": "hello"},
			wantStatus: http.StatusOK,
		},
		{
			name:    "echoed_unchanged_sensitive_fields_do_not_trigger",
			harness: updateStepUpHarness{stepUpEnabled: true, actorTotp: true},
			payload: map[string]any{
				"email":                  "target@example.com",
				"role":                   "user",
				"status":                 "active",
				"concurrency":            5,
				"rpm_limit":              60,
				"allowed_groups":         []int64{3, 2},
				"restrict_public_groups": true,
				"group_rates":            map[string]any{"2": 1.5},
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "granted_sensitive_field_passes",
			harness:    updateStepUpHarness{stepUpEnabled: true, actorTotp: true, grant: true},
			payload:    map[string]any{"status": "disabled"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "granted_group_rates_passes",
			harness:    updateStepUpHarness{stepUpEnabled: true, actorTotp: true, grant: true},
			payload:    map[string]any{"group_rates": map[string]any{"2": 2.5}},
			wantStatus: http.StatusOK,
		},
		{
			name:       "machine_credential_is_forbidden",
			harness:    updateStepUpHarness{stepUpEnabled: true, actorTotp: true, grant: true, adminAPIKey: true},
			payload:    map[string]any{"status": "disabled"},
			wantStatus: http.StatusForbidden,
			wantCode:   "STEP_UP_ADMIN_API_KEY_FORBIDDEN",
		},
		{
			name:       "actor_without_totp_is_forbidden",
			harness:    updateStepUpHarness{stepUpEnabled: true},
			payload:    map[string]any{"status": "disabled"},
			wantStatus: http.StatusForbidden,
			wantCode:   "STEP_UP_TOTP_NOT_ENABLED",
		},
		{
			name:       "step_up_disabled_keeps_old_behavior",
			harness:    updateStepUpHarness{},
			payload:    map[string]any{"status": "disabled"},
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router, adminSvc := setupUpdateStepUpRouter(t, tc.harness)
			rec := doJSON(t, router, http.MethodPut, "/api/v1/admin/users/42", tc.payload)
			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			if tc.wantStatus == http.StatusOK {
				require.Equal(t, 1, adminSvc.updateCalls)
			} else {
				require.Zero(t, adminSvc.updateCalls)
				require.Contains(t, rec.Body.String(), tc.wantCode)
			}
		})
	}
}

func TestUpdateUserSensitiveFieldGateSurvivesGetUserError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminSvc := &updateStepUpAdminStub{getErr: errors.New("db down")}
	handler := NewUserHandler(adminSvc, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 41})
	})
	router.PUT("/api/v1/admin/users/:id", handler.Update)

	rec := doJSON(t, router, http.MethodPut, "/api/v1/admin/users/42", map[string]any{"status": "disabled"})
	require.NotEqual(t, http.StatusOK, rec.Code)
	require.Zero(t, adminSvc.updateCalls)
}
