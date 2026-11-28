//go:build unit

package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/servertiming"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type stubPersonalKeyAuth struct {
	records   map[string]*service.UserPersonalAPIKey
	callCount int
}

func (s *stubPersonalKeyAuth) Authenticate(_ context.Context, rawKey, _ string) (*service.UserPersonalAPIKey, error) {
	s.callCount++
	if rec, ok := s.records[strings.TrimSpace(rawKey)]; ok {
		return rec, nil
	}
	return nil, service.ErrUserPersonalAPIKeyInvalid
}

type stubJWTUserReader struct {
	users map[int64]*service.User
}

func (r *stubJWTUserReader) GetByID(_ context.Context, id int64) (*service.User, error) {
	if u, ok := r.users[id]; ok {
		return u, nil
	}
	return nil, service.ErrUserNotFound
}

func testValidPersonalKey() string {
	return service.UserPersonalAPIKeyPrefix + strings.Repeat("ab", 32)
}

func TestPersonalAPIKeyAuth_HeadersAndCrossIPNoSessionBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rawKey := testValidPersonalKey()
	authStub := &stubPersonalKeyAuth{
		records: map[string]*service.UserPersonalAPIKey{
			rawKey: {
				UserID:    7,
				KeyPrefix: service.MaskUserPersonalAPIKey(rawKey),
				KeyHash:   service.HashUserPersonalAPIKey(rawKey),
			},
		},
	}
	users := &stubJWTUserReader{
		users: map[int64]*service.User{
			7: {ID: 7, Email: "ops@example.com", Role: service.RoleUser, Status: service.StatusActive, Concurrency: 5},
		},
	}

	r := gin.New()
	r.Use(jwtAuthWithPersonalKey(nil, users, nil, nil, nil, authStub, nil))
	r.GET("/api/v1/keys", func(c *gin.Context) {
		subj, ok := GetAuthSubjectFromContext(c)
		require.True(t, ok)
		c.JSON(http.StatusOK, gin.H{
			"user_id":     subj.UserID,
			"auth_method": c.GetString("auth_method"),
			"prefix":      c.GetString(ContextKeyPersonalAPIKeyPrefix),
		})
	})

	// 1. Authorization: Bearer uak_...（更换 IP 与 UA 仍应直接通过，不受会话指纹限制）。
	for i, tc := range []struct {
		headerName  string
		headerValue string
		remoteIP    string
		ua          string
	}{
		{"Authorization", "Bearer " + rawKey, "198.51.100.10:12345", "ChannelManager/1.0"},
		{"X-API-Key", rawKey, "203.0.113.88:54321", "curl/8.7.1"},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/keys", nil)
		req.RemoteAddr = tc.remoteIP
		req.Header.Set("User-Agent", tc.ua)
		req.Header.Set(tc.headerName, tc.headerValue)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code, "case %d", i)
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Equal(t, float64(7), body["user_id"])
		require.Equal(t, service.AuditAuthMethodUserAPIKey, body["auth_method"])
		require.Equal(t, service.MaskUserPersonalAPIKey(rawKey), body["prefix"])
	}
}

func TestPersonalAPIKeyAuth_InactiveUserRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rawKey := testValidPersonalKey()
	authStub := &stubPersonalKeyAuth{
		records: map[string]*service.UserPersonalAPIKey{
			rawKey: {UserID: 9, KeyPrefix: service.MaskUserPersonalAPIKey(rawKey)},
		},
	}
	users := &stubJWTUserReader{
		users: map[int64]*service.User{
			9: {ID: 9, Email: "disabled@example.com", Role: service.RoleUser, Status: service.StatusDisabled},
		},
	}

	r := gin.New()
	r.Use(jwtAuthWithPersonalKey(nil, users, nil, nil, nil, authStub, nil))
	r.GET("/api/v1/keys", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/keys", nil)
	req.Header.Set("X-API-Key", rawKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Contains(t, w.Body.String(), "USER_INACTIVE")
}

func TestPersonalAPIKeyAuth_ForbiddenSecurityRoutesAndAllowedUserRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rawKey := testValidPersonalKey()
	authStub := &stubPersonalKeyAuth{
		records: map[string]*service.UserPersonalAPIKey{
			rawKey: {UserID: 11, KeyPrefix: service.MaskUserPersonalAPIKey(rawKey)},
		},
	}
	users := &stubJWTUserReader{
		users: map[int64]*service.User{
			11: {ID: 11, Email: "u11@example.com", Role: service.RoleUser, Status: service.StatusActive},
		},
	}

	r := gin.New()
	r.Use(jwtAuthWithPersonalKey(nil, users, nil, nil, nil, authStub, nil))
	okHandler := func(c *gin.Context) { c.Status(http.StatusOK) }

	forbiddenRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/user/personal-api-key"},
		{http.MethodPost, "/api/v1/user/personal-api-key"},
		{http.MethodDelete, "/api/v1/user/personal-api-key"},
		{http.MethodPut, "/api/v1/user/password"},
		{http.MethodPut, "/api/v1/user"},
		{http.MethodPost, "/api/v1/user/account-bindings/email"},
		{http.MethodDelete, "/api/v1/user/account-bindings/github"},
		{http.MethodPost, "/api/v1/user/totp/disable"},
		{http.MethodPost, "/api/v1/user/passkeys/register/begin"},
		{http.MethodPost, "/api/v1/user/step-up/passkey/begin"},
		{http.MethodPut, "/api/v1/user/security/api-key-create-verification"},
		{http.MethodGet, "/api/v1/user/security/verification-factors"},
		{http.MethodPost, "/api/v1/user/security/passkey/verify/begin"},
		{http.MethodPost, "/api/v1/user/security/key-rotation-risk-ack"},
		{http.MethodPost, "/api/v1/user/aff/transfer"},
		{http.MethodPost, "/api/v1/payment/orders"},
		{http.MethodPost, "/api/v1/payment/orders/123/cancel"},
		{http.MethodGet, "/api/v1/user/api-keys/99/secret-seal"},
		{http.MethodPost, "/api/v1/user/api-keys/99/reveal-authorize"},
		{http.MethodPost, "/api/v1/auth/revoke-all-sessions"},
		{http.MethodPost, "/api/v1/auth/oauth/bind-token"},
		{http.MethodPut, "/api/v1/user/bark"},
		{http.MethodPost, "/api/v1/user/notify-email/verify"},
	}
	for _, rt := range forbiddenRoutes {
		r.Handle(rt.method, rt.path, okHandler)
	}

	allowedRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/auth/me"},
		{http.MethodGet, "/api/v1/user/profile"},
		{http.MethodGet, "/api/v1/user/security/summary"},
		{http.MethodGet, "/api/v1/keys"},
		{http.MethodPost, "/api/v1/keys"},
		{http.MethodPut, "/api/v1/keys/12"},
		{http.MethodPost, "/api/v1/keys/batch-rotate"},
		{http.MethodDelete, "/api/v1/keys/12"},
		{http.MethodGet, "/api/v1/groups/available"},
		{http.MethodGet, "/api/v1/channels/available"},
		{http.MethodGet, "/api/v1/usage"},
		{http.MethodGet, "/api/v1/subscriptions"},
		{http.MethodGet, "/api/v1/payment/orders/my"},
		{http.MethodGet, "/api/v1/user/api-keys/12/usage"},
	}
	for _, rt := range allowedRoutes {
		r.Handle(rt.method, rt.path, okHandler)
	}

	for _, rt := range forbiddenRoutes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		req.Header.Set("Authorization", "Bearer "+rawKey)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusForbidden, w.Code, "expected 403 for %s %s", rt.method, rt.path)
		require.Contains(t, w.Body.String(), "PERSONAL_API_KEY_FORBIDDEN", "route %s %s", rt.method, rt.path)
	}

	for _, rt := range allowedRoutes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		req.Header.Set("X-API-Key", rawKey)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "expected 200 for %s %s", rt.method, rt.path)
	}
}

func TestPersonalAPIKeyAuth_InvalidKeyBruteForceRateLimitedBeforeDB(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	authStub := &stubPersonalKeyAuth{records: map[string]*service.UserPersonalAPIKey{}}
	users := &stubJWTUserReader{users: map[int64]*service.User{}}
	guard := newPersonalAPIKeyInvalidGuard(rdb)

	r := gin.New()
	r.Use(jwtAuthWithPersonalKey(nil, users, nil, nil, nil, authStub, guard))
	r.GET("/api/v1/keys", func(c *gin.Context) { c.Status(http.StatusOK) })

	badKey := service.UserPersonalAPIKeyPrefix + strings.Repeat("00", 32)
	for i := 0; i < service.UserPersonalAPIKeyInvalidIPMaxAttempts; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/keys", nil)
		req.RemoteAddr = "198.51.100.77:1000"
		req.Header.Set("X-API-Key", badKey)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusUnauthorized, w.Code, "attempt %d", i+1)
	}
	require.Equal(t, service.UserPersonalAPIKeyInvalidIPMaxAttempts, authStub.callCount)

	// 第 21 次触发滑动窗口上限，返回 429 并写入封禁标记。
	req21 := httptest.NewRequest(http.MethodGet, "/api/v1/keys", nil)
	req21.RemoteAddr = "198.51.100.77:1000"
	req21.Header.Set("X-API-Key", badKey)
	w21 := httptest.NewRecorder()
	r.ServeHTTP(w21, req21)
	require.Equal(t, http.StatusTooManyRequests, w21.Code)
	require.NotEmpty(t, w21.Header().Get("Retry-After"))
	callsAfter21 := authStub.callCount

	// 第 22 次被封禁标记直接挡在鉴权/查库之前，authStub 调用次数不再增加！
	req22 := httptest.NewRequest(http.MethodGet, "/api/v1/keys", nil)
	req22.RemoteAddr = "198.51.100.77:1000"
	req22.Header.Set("X-API-Key", badKey)
	w22 := httptest.NewRecorder()
	r.ServeHTTP(w22, req22)
	require.Equal(t, http.StatusTooManyRequests, w22.Code)
	require.Equal(t, callsAfter21, authStub.callCount)
}

func TestPersonalAPIKeyPanelRateLimit_AlwaysEnforcedAndNoAdminExempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	// 即使未传 settingService（等价于全局限流关闭），个人接口密钥仍必须受保底限流保护，
	// 且管理员账号使用个人接口密钥也不享受 ExemptAdmin 豁免。
	limiter := NewPanelRateLimiter(rdb, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 1})
		c.Set(string(ContextKeyUserRole), service.RoleAdmin)
		c.Set("auth_method", service.AuditAuthMethodUserAPIKey)
		c.Next()
	})
	r.Use(limiter.Global())
	r.POST("/api/v1/keys", func(c *gin.Context) { c.Status(http.StatusOK) })

	for i := 0; i < service.UserPersonalAPIKeyFallbackWriteRPM; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/keys", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "write %d", i+1)
	}
	reqOver := httptest.NewRequest(http.MethodPost, "/api/v1/keys", nil)
	wOver := httptest.NewRecorder()
	r.ServeHTTP(wOver, reqOver)
	require.Equal(t, http.StatusTooManyRequests, wOver.Code)
	require.NotEmpty(t, wOver.Header().Get("Retry-After"))
}

func TestPersonalAPIKey_RejectedByAdminAuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rawKey := testValidPersonalKey()
	cfg := &config.Config{}
	settingSvc := service.NewSettingService(&emptySettingRepo{}, cfg)
	authSvc := service.NewAuthService(nil, nil, nil, nil, cfg, settingSvc, nil, nil, nil, nil, nil, nil, nil)

	r := gin.New()
	r.Use(gin.HandlerFunc(NewAdminAuthMiddleware(authSvc, nil, settingSvc, nil, cfg)))
	r.GET("/api/v1/admin/users", func(c *gin.Context) { c.Status(http.StatusOK) })

	// 无论是 X-API-Key 还是 Authorization: Bearer，个人接口密钥都不能通过管理端鉴权。
	for _, tc := range []struct {
		header string
		value  string
	}{
		{"x-api-key", rawKey},
		{"Authorization", "Bearer " + rawKey},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
		req.Header.Set(tc.header, tc.value)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusUnauthorized, w.Code, "header=%s", tc.header)
	}
}

type emptySettingRepo struct{}

func (e *emptySettingRepo) Get(_ context.Context, _ string) (*service.Setting, error) {
	return nil, service.ErrSettingNotFound
}
func (e *emptySettingRepo) GetValue(_ context.Context, _ string) (string, error) {
	return "", service.ErrSettingNotFound
}
func (e *emptySettingRepo) Set(_ context.Context, _, _ string) error { return nil }
func (e *emptySettingRepo) GetMultiple(_ context.Context, _ []string) (map[string]string, error) {
	return map[string]string{}, nil
}
func (e *emptySettingRepo) SetMultiple(_ context.Context, _ map[string]string) error { return nil }
func (e *emptySettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}
func (e *emptySettingRepo) Delete(_ context.Context, _ string) error { return nil }

func TestOptionalJWTAuth_AcceptsPersonalAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rawKey := testValidPersonalKey()
	authStub := &stubPersonalKeyAuth{
		records: map[string]*service.UserPersonalAPIKey{
			rawKey: {UserID: 55, KeyPrefix: service.MaskUserPersonalAPIKey(rawKey)},
		},
	}
	users := &stubJWTUserReader{
		users: map[int64]*service.User{
			55: {ID: 55, Email: "plaza@example.com", Role: service.RoleUser, Status: service.StatusActive},
		},
	}
	strict := jwtAuthWithPersonalKey(nil, users, nil, nil, nil, authStub, nil)

	r := gin.New()
	r.Use(gin.HandlerFunc(optionalJWTAuthFromStrict(strict)))
	r.GET("/api/v1/model-plaza", func(c *gin.Context) {
		subj, ok := GetAuthSubjectFromContext(c)
		if !ok {
			c.JSON(http.StatusOK, gin.H{"anonymous": true})
			return
		}
		c.JSON(http.StatusOK, gin.H{"anonymous": false, "user_id": subj.UserID})
	})

	// 匿名正常放行。
	wAnon := httptest.NewRecorder()
	r.ServeHTTP(wAnon, httptest.NewRequest(http.MethodGet, "/api/v1/model-plaza", nil))
	require.Equal(t, http.StatusOK, wAnon.Code)
	require.Contains(t, wAnon.Body.String(), `"anonymous":true`)

	// 带 X-API-Key: uak_... 识别用户身份。
	reqKey := httptest.NewRequest(http.MethodGet, "/api/v1/model-plaza", nil)
	reqKey.Header.Set("X-API-Key", rawKey)
	wKey := httptest.NewRecorder()
	r.ServeHTTP(wKey, reqKey)
	require.Equal(t, http.StatusOK, wKey.Code)
	require.Contains(t, wKey.Body.String(), `"user_id":55`)
}

func TestPersonalAPIKey_AdminOwnerCannotEscalatePrimaryAdminOrServerTiming(t *testing.T) {
	gin.SetMode(gin.TestMode)

	makeCtx := func(authMethod string) *gin.Context {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil)
		collector := servertiming.New(time.Now())
		req = req.WithContext(servertiming.WithCollector(req.Context(), collector))
		c.Request = req
		c.Set(string(ContextKeyUser), AuthSubject{UserID: PrimaryAdminUserID})
		c.Set(string(ContextKeyUserRole), service.RoleAdmin)
		c.Set("auth_method", authMethod)
		return c
	}

	cPersonal := makeCtx(service.AuditAuthMethodUserAPIKey)
	require.False(t, IsPrimaryAdmin(cPersonal), "user_api_key owned by admin must never pass IsPrimaryAdmin")
	require.Empty(t, ServerTimingHeaderValue(cPersonal), "user_api_key must not receive admin-scope Server-Timing")

	cJWT := makeCtx(service.AuditAuthMethodJWT)
	require.True(t, IsPrimaryAdmin(cJWT))
	require.NotEmpty(t, ServerTimingHeaderValue(cJWT))
}

func TestPersonalAPIKeyAuth_BadFormatIsolatedFromValidKeyAndLoopbackSkipped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	rawKey := testValidPersonalKey()
	authStub := &stubPersonalKeyAuth{
		records: map[string]*service.UserPersonalAPIKey{
			rawKey: {UserID: 7, KeyPrefix: service.MaskUserPersonalAPIKey(rawKey)},
		},
	}
	users := &stubJWTUserReader{
		users: map[int64]*service.User{
			7: {ID: 7, Email: "ok@example.com", Role: service.RoleUser, Status: service.StatusActive},
		},
	}
	guard := newPersonalAPIKeyInvalidGuard(rdb)

	r := gin.New()
	r.Use(jwtAuthWithPersonalKey(nil, users, nil, nil, nil, authStub, guard))
	r.GET("/api/v1/keys", func(c *gin.Context) { c.Status(http.StatusOK) })

	// 1. 同一公网出口发 20 次畸形 X-API-Key：0 次查库，第 21 次触发 429，但不得封禁同出口合法 uak_。
	for i := 0; i < service.UserPersonalAPIKeyInvalidIPMaxAttempts; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/keys", nil)
		req.RemoteAddr = "198.51.100.88:1234"
		req.Header.Set("X-API-Key", "not-a-valid-key")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusUnauthorized, w.Code, "badfmt attempt %d", i+1)
	}
	require.Equal(t, 0, authStub.callCount)

	reqBad21 := httptest.NewRequest(http.MethodGet, "/api/v1/keys", nil)
	reqBad21.RemoteAddr = "198.51.100.88:1234"
	reqBad21.Header.Set("X-API-Key", "not-a-valid-key")
	wBad21 := httptest.NewRecorder()
	r.ServeHTTP(wBad21, reqBad21)
	require.Equal(t, http.StatusTooManyRequests, wBad21.Code)
	require.Equal(t, 0, authStub.callCount)

	reqValid := httptest.NewRequest(http.MethodGet, "/api/v1/keys", nil)
	reqValid.RemoteAddr = "198.51.100.88:1234"
	reqValid.Header.Set("X-API-Key", rawKey)
	wValid := httptest.NewRecorder()
	r.ServeHTTP(wValid, reqValid)
	require.Equal(t, http.StatusOK, wValid.Code)

	// 2. 私网/回环 IP 不受公网 IP 封禁影响，防止反代配置偏差导致全站坍缩误封。
	badValidFmt := service.UserPersonalAPIKeyPrefix + strings.Repeat("ff", 32)
	for i := 0; i < service.UserPersonalAPIKeyInvalidIPMaxAttempts+5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/keys", nil)
		req.RemoteAddr = "127.0.0.1:5000"
		req.Header.Set("X-API-Key", badValidFmt)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusUnauthorized, w.Code, "loopback attempt %d", i+1)
	}
}

func TestPersonalAPIKeyPanelRateLimit_KeyVerificationBucketIsolatedFromJWT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	limiter := NewPanelRateLimiter(rdb, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyUser), AuthSubject{UserID: 11})
		c.Set(string(ContextKeyUserRole), service.RoleUser)
		c.Set("auth_method", c.GetHeader("X-Test-Auth-Method"))
		c.Next()
	})
	r.Use(limiter.KeyVerification())
	r.POST("/api/v1/keys/batch-rotate", func(c *gin.Context) { c.Status(http.StatusOK) })

	hit429 := false
	for i := 0; i < 40; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/keys/batch-rotate", nil)
		req.Header.Set("X-Test-Auth-Method", service.AuditAuthMethodUserAPIKey)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			hit429 = true
			break
		}
		require.Equal(t, http.StatusOK, w.Code)
	}
	require.True(t, hit429, "expected personal API key to hit KeyVerification rate limit")

	// 同一用户的交互式 JWT 桶必须独立，不被个人接口密钥耗尽。
	reqJWT := httptest.NewRequest(http.MethodPost, "/api/v1/keys/batch-rotate", nil)
	reqJWT.Header.Set("X-Test-Auth-Method", service.AuditAuthMethodJWT)
	wJWT := httptest.NewRecorder()
	r.ServeHTTP(wJWT, reqJWT)
	require.Equal(t, http.StatusOK, wJWT.Code)
}

func TestPersonalAPIKey_MaskedRequestCredentialIgnoresDecoyXAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rawKey := testValidPersonalKey()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/keys", nil)
	req.Header.Set("X-API-Key", "decoy-not-uak-header")
	req.Header.Set("Authorization", "Bearer "+rawKey)
	c.Request = req
	c.Set("auth_method", service.AuditAuthMethodUserAPIKey)

	masked := MaskedRequestCredential(c)
	require.True(t, strings.HasPrefix(masked, "Bearer "))
	require.NotContains(t, masked, "decoy")
	require.NotContains(t, masked, rawKey)
}
