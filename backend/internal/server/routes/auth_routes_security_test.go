package routes

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type routePersonalKeyRevokerSpy struct {
	revoked []int64
}

func (s *routePersonalKeyRevokerSpy) Delete(_ context.Context, userID int64) (bool, error) {
	s.revoked = append(s.revoked, userID)
	return true, nil
}

// Area 1: All 5 OAuth bind-login routes must share ONE unified rate-limit bucket ("oauth-bind-login")
// so an attacker cannot split brute-force attempts across pending/linuxdo/wechat/oidc/dingtalk.
func TestAuthRoutesUnifyOAuthBindLoginRateLimitBucketAndWirePersonalKeyRevoker(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:                   "test-secret",
			ExpireHour:               1,
			AccessTokenExpireMinutes: 60,
			RefreshTokenExpireDays:   7,
		},
	}
	authSvc := service.NewAuthService(nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	revoker := &routePersonalKeyRevokerSpy{}
	authSvc.SetPersonalAPIKeyRevoker(revoker)

	totpSvc := service.NewTotpService(nil, nil, nil, nil, nil, nil)
	authHandler := handler.NewAuthHandler(cfg, authSvc, nil, nil, nil, nil, totpSvc, nil)
	totpHandler := handler.NewTotpHandler(totpSvc)

	handlers := &handler.Handlers{
		Auth:    authHandler,
		Totp:    totpHandler,
		Passkey: &handler.PasskeyHandler{},
		Setting: &handler.SettingHandler{},
	}

	router := gin.New()
	v1 := router.Group("/api/v1")
	RegisterAuthRoutes(
		v1,
		handlers,
		servermiddleware.JWTAuthMiddleware(func(c *gin.Context) { c.Next() }),
		servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }),
		rdb,
		nil,
		servermiddleware.NewPanelRateLimiter(rdb, nil),
	)

	// Verify RegisterAuthRoutes wired PersonalAPIKeyRevoker onto TotpService via TotpHandler.
	_ = totpSvc.RevokeTwoFactorGrantsForUser(context.Background(), 99)
	require.Equal(t, []int64{99}, revoker.revoked)

	// Hit all 5 bind-login endpoints twice each (10 total from same IP), then the 11th must hit 429
	// even on a different provider endpoint!
	endpoints := []string{
		"/api/v1/auth/oauth/pending/bind-login",
		"/api/v1/auth/oauth/linuxdo/bind-login",
		"/api/v1/auth/oauth/wechat/bind-login",
		"/api/v1/auth/oauth/oidc/bind-login",
		"/api/v1/auth/oauth/dingtalk/bind-login",
	}
	for round := 0; round < 2; round++ {
		for _, ep := range endpoints {
			req := httptest.NewRequest(http.MethodPost, ep, bytes.NewBufferString(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = "203.0.113.55:12345"
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.NotEqual(t, http.StatusTooManyRequests, w.Code, "request within shared 10/min budget on %s should not 429", ep)
		}
	}

	// 11th request on ANY of the 5 endpoints must be rejected with 429.
	for _, ep := range endpoints {
		req := httptest.NewRequest(http.MethodPost, ep, bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "203.0.113.55:12345"
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusTooManyRequests, w.Code, "11th request on %s must hit shared oauth-bind-login bucket", ep)
	}
}
