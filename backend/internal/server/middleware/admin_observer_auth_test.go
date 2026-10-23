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
)

func TestAuthenticatedSecondaryAdminCannotBypassObservationBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "observer-unit-signing-fixture", ExpireHour: 1}}
	auth := service.NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	user := &service.User{ID: 2, Role: service.RoleAdmin, Status: service.StatusActive}
	repo := &stubUserRepo{getByID: func(context.Context, int64) (*service.User, error) { return user, nil }}
	users := service.NewUserService(repo, nil, nil, nil)
	token, err := auth.GenerateToken(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method, path string
		websocket    bool
		want         int
	}{
		{http.MethodGet, "/api/v1/admin/usage", false, 200},
		{http.MethodPost, "/api/v1/admin/users", false, 403},
		{http.MethodGet, "/api/v1/admin/users", false, 403},
		{http.MethodGet, "/api/v1/admin/accounts/export", false, 403},
		{http.MethodGet, "/api/v1/admin/usage", true, 403},
	} {
		router := gin.New()
		router.Use(gin.HandlerFunc(NewAdminAuthMiddleware(auth, users, nil, nil, cfg)))
		router.Handle(test.method, test.path, func(c *gin.Context) {
			if !IsAdminObserver(c) {
				t.Error("observer identity lost after authentication")
			}
			c.Status(http.StatusOK)
		})
		req := httptest.NewRequest(test.method, test.path+"?user_id=1&role=admin", nil)
		if test.websocket {
			req.Header.Set("Connection", "Upgrade")
			req.Header.Set("Upgrade", "websocket")
			req.Header.Set("Sec-WebSocket-Protocol", "sub2api-admin, jwt."+token)
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != test.want {
			t.Fatalf("%s %s websocket=%v: %d %s", test.method, test.path, test.websocket, w.Code, w.Body.String())
		}
	}
}
