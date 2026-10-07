//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type ownerKeyUserRepo struct {
	service.UserRepository
	admin *service.User
}

func (r *ownerKeyUserRepo) GetFirstAdmin(context.Context) (*service.User, error) { return r.admin, nil }

func TestAdminAPIKeyEstablishesOnlyVerifiedOwnerScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name     string
		user     *service.User
		owner    int64
		key      string
		status   int
		fullPool bool
	}{
		{"renamed owner", &service.User{ID: 7, Email: "renamed@example.test", Role: service.RoleAdmin, Status: service.StatusActive}, 7, "test-admin-key", 200, true},
		{"different configured owner", &service.User{ID: 7, Role: service.RoleAdmin, Status: service.StatusActive}, 8, "test-admin-key", 200, false},
		{"unconfigured owner leaves scope untouched", &service.User{ID: 7, Role: service.RoleAdmin, Status: service.StatusActive}, 0, "test-admin-key", 200, true},
		{"inactive admin", &service.User{ID: 7, Role: service.RoleAdmin, Status: service.StatusDisabled}, 7, "test-admin-key", 403, false},
		{"non admin", &service.User{ID: 7, Role: service.RoleUser, Status: service.StatusActive}, 7, "test-admin-key", 403, false},
		{"missing admin", nil, 7, "test-admin-key", 403, false},
		{"invalid key", &service.User{ID: 7, Role: service.RoleAdmin, Status: service.StatusActive}, 7, "wrong-key", 401, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{Security: config.SecurityConfig{AccountPoolOwnerUserID: tc.owner}}
			settings := service.NewSettingService(&bmSettingRepo{values: map[string]string{service.SettingKeyAdminAPIKey: "test-admin-key"}}, cfg)
			users := service.NewUserService(&ownerKeyUserRepo{admin: tc.user}, nil, nil, nil)
			r := gin.New()
			r.Use(gin.HandlerFunc(NewAdminAuthMiddleware(nil, users, settings, nil, cfg)))
			called := false
			r.GET("/accounts", func(c *gin.Context) {
				called = true
				// The middleware installs a scope only when an owner is configured.
				id, fullPool, ok := service.AccountOwnerScopeDetail(c.Request.Context())
				require.Equal(t, tc.owner > 0, ok)
				if ok {
					require.Equal(t, tc.user.ID, id)
					require.Equal(t, tc.fullPool, fullPool)
				}
				c.Status(http.StatusOK)
			})
			req := httptest.NewRequest(http.MethodGet, "/accounts?see_all=true", nil)
			req.Header.Set("X-API-Key", tc.key)
			req.Header.Set("X-Account-Pool-Owner-User-ID", "7")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code)
			require.Equal(t, tc.status == http.StatusOK, called)
		})
	}
}
