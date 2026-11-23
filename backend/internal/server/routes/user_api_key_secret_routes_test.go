package routes

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 三个封存接口必须挂在 /api/v1/user/api-keys/:id/secret-seal 这组固定路径上
// （前端按此契约对接），并且都在登录用户路由组内。
func TestUserAPIKeySecretSealRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)

	settings := service.NewSettingService(&cryptoRouteSettingsRepo{values: map[string]string{}}, nil)
	jwt := middleware.JWTAuthMiddleware(func(c *gin.Context) {
		c.Set("auth_method", service.AuditAuthMethodJWT)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Next()
	})
	noopAudit := middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })

	h := &handler.Handlers{APIKey: handler.NewAPIKeyHandler(nil, nil)}
	router := gin.New()
	v1 := router.Group("/api/v1")
	RegisterUserRoutes(v1, h, jwt, noopAudit, nil, settings, nil)

	registered := map[string]struct{}{}
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = struct{}{}
	}
	for _, want := range []string{
		http.MethodPut + " /api/v1/user/api-keys/:id/secret-seal",
		http.MethodGet + " /api/v1/user/api-keys/:id/secret-seal",
		http.MethodDelete + " /api/v1/user/api-keys/:id/secret-seal",
		// 三选一身份校验（密码 / TOTP / Passkey）：
		// 因子列表、Passkey 校验挑战、二次查看授权。
		http.MethodGet + " /api/v1/user/security/verification-factors",
		http.MethodPost + " /api/v1/user/security/passkey/verify/begin",
		http.MethodPost + " /api/v1/user/security/passkey/verify/finish",
		http.MethodPost + " /api/v1/user/api-keys/:id/reveal-authorize",
		http.MethodPost + " /api/v1/keys",
	} {
		require.Contains(t, registered, want)
	}
}
