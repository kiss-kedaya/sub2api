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

func TestAdminAuthAccountPoolOwnerUsesStableID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, email, role, status string
		id, ownerID               int64
		wantAll                   bool
		wantStatus                int
	}{
		{"owner original email", "first@example.test", service.RoleAdmin, service.StatusActive, 7, 7, true, 200},
		{"owner changed email", "renamed@example.test", service.RoleAdmin, service.StatusActive, 7, 7, true, 200},
		{"other admin claims legacy email", "admin@sub2api.local", service.RoleAdmin, service.StatusActive, 8, 7, false, 200},
		{"other admin", "other@example.test", service.RoleAdmin, service.StatusActive, 8, 7, false, 200},
		{"unconfigured owner leaves scope untouched", "admin@sub2api.local", service.RoleAdmin, service.StatusActive, 7, 0, true, 200},
		{"configured non-admin", "user@example.test", service.RoleUser, service.StatusActive, 7, 7, false, 403},
		{"disabled configured admin", "owner@example.test", service.RoleAdmin, service.StatusDisabled, 7, 7, false, 401},
	} {
		for _, websocket := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/jwt", true: "/websocket"}[websocket], func(t *testing.T) {
				cfg := &config.Config{JWT: config.JWTConfig{Secret: "account-owner-test-signing-key", ExpireHour: 1}, Security: config.SecurityConfig{AccountPoolOwnerUserID: tc.ownerID}}
				u := &service.User{ID: tc.id, Email: tc.email, Role: tc.role, Status: tc.status}
				repo := &stubUserRepo{getByID: func(context.Context, int64) (*service.User, error) { return u, nil }}
				auth := service.NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
				users := service.NewUserService(repo, nil, nil, nil)
				token, err := auth.GenerateToken(context.Background(), u)
				require.NoError(t, err)
				router := gin.New()
				router.Use(gin.HandlerFunc(NewAdminAuthMiddleware(auth, users, nil, nil, cfg)))
				called := false
				router.GET("/accounts", func(c *gin.Context) {
					called = true
					// The middleware installs a scope only when an owner is configured.
					id, all, ok := service.AccountOwnerScopeDetail(c.Request.Context())
					require.Equal(t, tc.ownerID > 0, ok)
					if ok {
						require.Equal(t, tc.id, id)
						require.Equal(t, tc.wantAll, all)
					}
					c.Status(http.StatusOK)
				})
				req := httptest.NewRequest(http.MethodGet, "/accounts?account_pool_owner_user_id=8&see_all=true", nil)
				req.Header.Set("X-Account-Pool-Owner-User-ID", "8")
				if websocket {
					req.Header.Set("Connection", "Upgrade")
					req.Header.Set("Upgrade", "websocket")
					req.Header.Set("Sec-WebSocket-Protocol", "sub2api-admin, jwt."+token)
				} else {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
				require.Equal(t, tc.wantStatus == 200, called)
			})
		}
	}
}
