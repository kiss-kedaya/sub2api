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

// 「创建 API Key 时是否需要验证身份」开关的路由契约：
//   - PUT /api/v1/user/security/api-key-create-verification 注册在登录用户路由组内；
//   - 它和创建 / 轮换 / 二次查看授权 / Passkey 校验共用同一个 10 次/分钟的按用户限流桶——
//     这条接口也会比对登录密码 / 动态码，不能成为绕开限流的另一个在线爆破入口。

// createVerificationSwitchRoute 与 verificationRoutes（user_key_verification_rate_limit_test.go）同形。
var createVerificationSwitchRoute = struct{ name, method, path, body string }{
	"create-verification-switch", http.MethodPut, "/api/v1/user/security/api-key-create-verification", `{"mode":"on"}`,
}

func TestUserAPIKeyCreateVerificationRouteRegistered(t *testing.T) {
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
	RegisterUserRoutes(router.Group("/api/v1"), h, jwt, noopAudit, nil, settings, nil)

	registered := map[string]struct{}{}
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = struct{}{}
	}
	require.Contains(t, registered, http.MethodPut+" /api/v1/user/security/api-key-create-verification")
	// 前端读「有效值」的 GET 还是原来的路径。
	require.Contains(t, registered, http.MethodGet+" /api/v1/user/security/verification-factors")
}

// 改开关与其余校验入口共用一个桶：别的入口把额度打满，改开关就是 429；反过来也一样。
func TestCreateVerificationSwitchRouteSharesTheVerificationRateLimit(t *testing.T) {
	t.Run("others exhaust the budget first", func(t *testing.T) {
		env := newKeyVerificationRouteEnv(t)
		used := 0
		for used < 10 {
			for _, other := range verificationRoutes {
				if used >= 10 {
					break
				}
				rec := env.do(other.method, other.path, other.body)
				require.NotEqual(t, http.StatusTooManyRequests, rec.Code, "%s request %d", other.name, used+1)
				used++
			}
		}
		rec := env.do(createVerificationSwitchRoute.method, createVerificationSwitchRoute.path, createVerificationSwitchRoute.body)
		require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
		require.NotEmpty(t, rec.Header().Get("Retry-After"))
	})

	t.Run("the switch exhausts the budget first", func(t *testing.T) {
		env := newKeyVerificationRouteEnv(t)
		for i := 0; i < 10; i++ {
			rec := env.do(createVerificationSwitchRoute.method, createVerificationSwitchRoute.path, createVerificationSwitchRoute.body)
			require.NotEqual(t, http.StatusTooManyRequests, rec.Code, "switch request %d: %s", i+1, rec.Body.String())
		}
		for _, other := range verificationRoutes {
			rec := env.do(other.method, other.path, other.body)
			require.Equal(t, http.StatusTooManyRequests, rec.Code, "%s must share the bucket: %s", other.name, rec.Body.String())
		}
		// 读有效值的 GET 不计入这个桶。
		rec := env.do(http.MethodGet, "/api/v1/user/security/verification-factors", "")
		require.NotEqual(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	})
}
