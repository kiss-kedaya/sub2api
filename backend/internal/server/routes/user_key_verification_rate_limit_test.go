package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// 走身份校验的入口必须全部挂上「校验档」限流（审计 A-P1-03 / A-P1-06）：
// 创建、单条轮换（regenerate_key）、批量轮换、二次查看授权共用每用户 10 次/分钟；
// 封存 PUT 要验登录密码，单独一档 60 次/分钟。这里用真实路由表 + 真实限流器（miniredis）
// 验证：少挂任何一条，对应用例就会拿到 200 而不是 429。

type keyVerificationRouteEnv struct {
	router *gin.Engine
}

func newKeyVerificationRouteEnv(t *testing.T) *keyVerificationRouteEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	settings := service.NewSettingService(&cryptoRouteSettingsRepo{values: map[string]string{}}, nil)
	jwt := middleware.JWTAuthMiddleware(func(c *gin.Context) {
		c.Set("auth_method", service.AuditAuthMethodJWT)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Next()
	})
	noopAudit := middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })

	owner := &service.User{ID: 1, Email: "owner@example.com", Status: service.StatusActive, SignupSource: "email"}
	apiKeyService := service.NewAPIKeyService(
		&stepUpProbeAPIKeyRepo{}, &stepUpProbeUserRepo{user: owner}, nil, nil, nil, nil, &config.Config{})
	h := &handler.Handlers{APIKey: handler.NewAPIKeyHandler(apiKeyService, nil)}

	router := gin.New()
	RegisterUserRoutes(router.Group("/api/v1"), h, jwt, noopAudit, nil, settings, middleware.NewPanelRateLimiter(client, settings))
	return &keyVerificationRouteEnv{router: router}
}

func (e *keyVerificationRouteEnv) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// verificationRoutes 是全部走「校验档」的入口（创建 / 单条轮换 / 批量轮换 / 二次查看授权 / Passkey 校验）。
var verificationRoutes = []struct{ name, method, path, body string }{
	{"create", http.MethodPost, "/api/v1/keys", `{"name":"fixture-key"}`},
	{"rotate", http.MethodPut, "/api/v1/keys/1", `{"regenerate_key":true}`},
	{"batch-rotate", http.MethodPost, "/api/v1/keys/batch-rotate", `{"ids":[1]}`},
	{"reveal-authorize", http.MethodPost, "/api/v1/user/api-keys/1/reveal-authorize", `{"method":"password","password":"x"}`},
	{"passkey-begin", http.MethodPost, "/api/v1/user/security/passkey/verify/begin", ``},
}

func TestEveryIdentityVerificationRouteSharesTheVerificationRateLimit(t *testing.T) {
	for _, target := range verificationRoutes {
		t.Run(target.name, func(t *testing.T) {
			env := newKeyVerificationRouteEnv(t)

			// 先用其余入口把共用的 10 次额度打满：任意入口都在同一个桶里。
			used := 0
			for used < 10 {
				for _, other := range verificationRoutes {
					if other.name == target.name || used >= 10 {
						continue
					}
					rec := env.do(other.method, other.path, other.body)
					require.NotEqual(t, http.StatusTooManyRequests, rec.Code, "%s request %d", other.name, used+1)
					used++
				}
			}

			rec := env.do(target.method, target.path, target.body)
			require.Equal(t, http.StatusTooManyRequests, rec.Code, "%s must be limited once the shared budget is spent: %s", target.name, rec.Body.String())
			require.NotEmpty(t, rec.Header().Get("Retry-After"))
		})
	}
}

func TestKeyRotationLimitDoesNotThrottleOrdinaryEdits(t *testing.T) {
	env := newKeyVerificationRouteEnv(t)

	for i := 0; i < 12; i++ {
		env.do(http.MethodPut, "/api/v1/keys/1", `{"regenerate_key":true}`)
	}
	rec := env.do(http.MethodPut, "/api/v1/keys/1", `{"regenerate_key":true}`)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)

	// 改名 / 启停 / 配额 不下发明文：校验档耗尽后依旧畅通，也不会把自己算进去。
	for i := 0; i < 30; i++ {
		rec = env.do(http.MethodPut, "/api/v1/keys/1", `{"name":"renamed","status":"inactive"}`)
		require.NotEqual(t, http.StatusTooManyRequests, rec.Code, "plain edit %d must not be throttled", i+1)
	}
}

func TestSecretSealWriteHasItsOwnRateLimit(t *testing.T) {
	env := newKeyVerificationRouteEnv(t)

	// 校验档耗尽不影响封存（批量轮换后要连续封存最多 50 把）。
	for i := 0; i < 11; i++ {
		env.do(http.MethodPost, "/api/v1/keys/batch-rotate", `{"ids":[1]}`)
	}
	require.Equal(t, http.StatusTooManyRequests, env.do(http.MethodPost, "/api/v1/keys/batch-rotate", `{"ids":[1]}`).Code)

	for i := 0; i < 60; i++ {
		rec := env.do(http.MethodPut, "/api/v1/user/api-keys/1/secret-seal", `{}`)
		require.NotEqual(t, http.StatusTooManyRequests, rec.Code, "seal %d must pass", i+1)
	}
	rec := env.do(http.MethodPut, "/api/v1/user/api-keys/1/secret-seal", `{}`)
	require.Equal(t, http.StatusTooManyRequests, rec.Code, "the 61st seal in a minute must be limited: %s", rec.Body.String())
	require.NotEmpty(t, rec.Header().Get("Retry-After"))
}
