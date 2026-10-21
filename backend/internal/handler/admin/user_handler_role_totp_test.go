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

type totpDemotionAdminStub struct {
	service.AdminService
	target      *service.User
	getErr      error
	updateCalls int
	lastInput   *service.UpdateUserInput
}

func (s *totpDemotionAdminStub) GetUser(context.Context, int64) (*service.User, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	user := *s.target
	return &user, nil
}

func (s *totpDemotionAdminStub) UpdateUser(_ context.Context, _ int64, input *service.UpdateUserInput) (*service.User, error) {
	s.updateCalls++
	s.lastInput = input
	user := *s.target
	if input.Role != "" {
		user.Role = input.Role
	}
	return &user, nil
}

type totpDemotionActorRepo struct {
	service.UserRepository
	actor *service.User
}

func (r *totpDemotionActorRepo) GetByID(context.Context, int64) (*service.User, error) {
	user := *r.actor
	return &user, nil
}

func (r *totpDemotionActorRepo) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

type totpDemotionGrantCache struct {
	service.TotpCache
	grantUserID      int64
	grantSession     string
	err              error
	checks           int
	checkedUserID    int64
	checkedSessionID string
}

func (c *totpDemotionGrantCache) HasStepUpGrant(_ context.Context, userID int64, sessionKey string) (bool, error) {
	c.checks++
	c.checkedUserID = userID
	c.checkedSessionID = sessionKey
	return c.grantUserID != 0 && c.grantUserID == userID && c.grantSession == sessionKey, c.err
}

func TestUpdateUserAdminDemotionRequiresActorTotpRegardlessOfGlobalSwitch(t *testing.T) {
	for _, tc := range []struct {
		name             string
		authenticated    bool
		actorTotpEnabled bool
		targetTotp       bool
		adminAPIKey      bool
		grantUserID      int64
		grantSession     string
		unavailable      bool
		status           int
		code             string
	}{
		{name: "no_authenticated_actor", status: http.StatusUnauthorized, code: "UNAUTHORIZED"},
		{name: "actor_without_totp", authenticated: true, status: http.StatusForbidden, code: "STEP_UP_TOTP_NOT_ENABLED"},
		{name: "missing_grant_cannot_spoof_approval", authenticated: true, actorTotpEnabled: true, status: http.StatusForbidden, code: "STEP_UP_REQUIRED"},
		{name: "target_totp_does_not_authorize_actor", authenticated: true, targetTotp: true, status: http.StatusForbidden, code: "STEP_UP_TOTP_NOT_ENABLED"},
		{name: "different_actor_grant", authenticated: true, actorTotpEnabled: true, grantUserID: 42, grantSession: "admin-session", status: http.StatusForbidden, code: "STEP_UP_REQUIRED"},
		{name: "different_session_grant", authenticated: true, actorTotpEnabled: true, grantUserID: 41, grantSession: "other-session", status: http.StatusForbidden, code: "STEP_UP_REQUIRED"},
		{name: "admin_api_key", authenticated: true, actorTotpEnabled: true, adminAPIKey: true, grantUserID: 41, grantSession: "admin-session", status: http.StatusForbidden, code: "STEP_UP_ADMIN_API_KEY_FORBIDDEN"},
		{name: "grant_service_unavailable", authenticated: true, actorTotpEnabled: true, unavailable: true, status: http.StatusServiceUnavailable, code: "STEP_UP_UNAVAILABLE"},
		{name: "valid_actor_grant_target_without_totp", authenticated: true, actorTotpEnabled: true, grantUserID: 41, grantSession: "admin-session", status: http.StatusOK},
		{name: "valid_actor_grant_target_with_totp", authenticated: true, actorTotpEnabled: true, targetTotp: true, grantUserID: 41, grantSession: "admin-session", status: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			cache := &totpDemotionGrantCache{grantUserID: tc.grantUserID, grantSession: tc.grantSession}
			if tc.unavailable {
				cache.err = errors.New("grant service unavailable")
			}
			actorRepo := &totpDemotionActorRepo{actor: &service.User{ID: 41, Role: service.RoleAdmin, Status: service.StatusActive, TotpEnabled: tc.actorTotpEnabled}}
			adminSvc := &totpDemotionAdminStub{target: &service.User{ID: 42, Role: service.RoleAdmin, Status: service.StatusActive, TotpEnabled: tc.targetTotp}}
			totpSvc := service.NewTotpService(actorRepo, nil, cache, nil, nil, nil)
			userSvc := service.NewUserService(actorRepo, nil, nil, nil)
			settings := service.NewSettingService(&settingHandlerRepoStub{values: map[string]string{service.SettingKeyStepUpEnabled: "false"}}, nil)
			h := NewUserHandler(adminSvc, nil, nil, nil, totpSvc, userSvc, settings)
			router := gin.New()
			router.Use(func(c *gin.Context) {
				if tc.authenticated {
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 41})
					c.Set(middleware.ContextKeySessionID, "admin-session")
				}
				if tc.adminAPIKey {
					c.Set("auth_method", service.AuditAuthMethodAdminAPIKey)
				} else {
					c.Set("auth_method", "jwt")
				}
			})
			router.PUT("/api/v1/admin/users/:id", h.Update)
			rec := doJSON(t, router, http.MethodPut, "/api/v1/admin/users/42", map[string]any{
				"role": "user", "totp_demotion_approved": true, "TotpDemotionApproved": true,
			})
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			if tc.authenticated && tc.actorTotpEnabled && !tc.adminAPIKey {
				require.Equal(t, 1, cache.checks)
				require.EqualValues(t, 41, cache.checkedUserID)
				require.Equal(t, "admin-session", cache.checkedSessionID)
			} else {
				require.Zero(t, cache.checks)
			}
			if tc.status == http.StatusOK {
				require.Equal(t, 1, adminSvc.updateCalls)
				require.True(t, adminSvc.lastInput.TotpDemotionApproved)
				require.EqualValues(t, 41, adminSvc.lastInput.ActorAdminID)
			} else {
				require.Zero(t, adminSvc.updateCalls)
				require.Contains(t, rec.Body.String(), tc.code)
			}
		})
	}
}

func TestUpdateUserAdminDemotionTargetReadFailureDoesNotWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminSvc := &totpDemotionAdminStub{getErr: service.ErrUserNotFound}
	h := NewUserHandler(adminSvc, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.PUT("/api/v1/admin/users/:id", h.Update)
	rec := doJSON(t, router, http.MethodPut, "/api/v1/admin/users/42", map[string]any{"role": "user"})
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	require.Zero(t, adminSvc.updateCalls)
}

func TestUpdateUserAdminSelfDemotionStillRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminSvc := &totpDemotionAdminStub{}
	h := NewUserHandler(adminSvc, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 41})
	})
	router.PUT("/api/v1/admin/users/:id", h.Update)
	rec := doJSON(t, router, http.MethodPut, "/api/v1/admin/users/41", map[string]any{"role": "user"})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Zero(t, adminSvc.updateCalls)
}
