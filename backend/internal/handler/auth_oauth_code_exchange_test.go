package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// oauthCodeExchangeCacheStub 同时实现 RefreshTokenCache（空实现）与
// OAuthLoginCodeStore（内存版，模拟 Redis 的写入 + 原子消费）。
type oauthCodeExchangeCacheStub struct {
	oauthPendingFlowRefreshTokenCacheStub
	mu    sync.Mutex
	codes map[string]*service.OAuthLoginCodeData
	ttls  map[string]time.Duration
}

func newOAuthCodeExchangeCacheStub() *oauthCodeExchangeCacheStub {
	return &oauthCodeExchangeCacheStub{
		codes: make(map[string]*service.OAuthLoginCodeData),
		ttls:  make(map[string]time.Duration),
	}
}

func (s *oauthCodeExchangeCacheStub) StoreOAuthLoginCode(_ context.Context, codeHash string, data *service.OAuthLoginCodeData, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := *data
	s.codes[codeHash] = &cloned
	s.ttls[codeHash] = ttl
	return nil
}

func (s *oauthCodeExchangeCacheStub) ConsumeOAuthLoginCode(_ context.Context, codeHash string) (*service.OAuthLoginCodeData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.codes[codeHash]
	if !ok {
		return nil, service.ErrOAuthLoginCodeNotFound
	}
	delete(s.codes, codeHash)
	cloned := *data
	return &cloned, nil
}

func newOAuthCodeExchangeTestHandler(t *testing.T) (*AuthHandler, *dbent.Client, *oauthCodeExchangeCacheStub) {
	t.Helper()
	cache := newOAuthCodeExchangeCacheStub()
	handler, client := newOAuthPendingFlowTestHandlerWithDependencies(t, oauthPendingFlowTestHandlerOptions{
		refreshTokenCache: cache,
	})
	// 生产配置默认开启 code 交换；测试里显式打开（其余用例的零值 cfg 保持旧 fragment 行为）。
	handler.cfg = &config.Config{
		Security: config.SecurityConfig{OAuthCodeExchangeEnabled: true},
	}
	return handler, client, cache
}

func createOAuthExchangeTestUser(t *testing.T, client *dbent.Client) {
	t.Helper()
	_, err := client.User.Create().
		SetEmail("code-exchange@example.com").
		SetUsername("code-exchange").
		SetPasswordHash("hash").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		Save(context.Background())
	require.NoError(t, err)
}

func runOAuthCallback(t *testing.T, handler *AuthHandler, provider string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/"+provider+"/callback", nil)
	handler.emailOAuthCallbackWithProfile(c, provider, config.EmailOAuthProviderConfig{
		Enabled:             true,
		ClientID:            provider + "-client",
		ClientSecret:        provider + "-secret",
		RedirectURL:         "https://app.example/api/v1/auth/oauth/" + provider + "/callback",
		FrontendRedirectURL: "/auth/oauth/callback",
	}, "/auth/oauth/callback", "/dashboard", &emailOAuthProfile{
		Subject:       provider + "-code-user",
		Email:         "code-exchange@example.com",
		EmailVerified: true,
		Username:      "code-exchange",
	})
	return recorder
}

func postOAuthCodeExchange(t *testing.T, handler *AuthHandler, code string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"code": code})
	require.NoError(t, err)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/exchange", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	handler.OAuthCodeExchange(c)
	return recorder
}

// 新流程：回调重定向里只有一次性 code，不含任何令牌；code 兑换成功后立即失效。
func TestEmailOAuthCallbackReturnsOneTimeCodeInsteadOfTokens(t *testing.T) {
	handler, client, cache := newOAuthCodeExchangeTestHandler(t)
	createOAuthExchangeTestUser(t, client)

	recorder := runOAuthCallback(t, handler, "github")
	require.Equal(t, http.StatusFound, recorder.Code)
	location := recorder.Header().Get("Location")
	require.NotContains(t, location, "access_token=")
	require.NotContains(t, location, "refresh_token=")
	require.Contains(t, location, "/auth/oauth/callback")

	values := parseOAuthRedirectFragment(t, location)
	code := values.Get("code")
	require.NotEmpty(t, code)
	require.Equal(t, "/dashboard", values.Get("redirect"))
	require.Len(t, cache.codes, 1)
	for _, ttl := range cache.ttls {
		require.Equal(t, service.OAuthLoginCodeTTL, ttl)
	}

	// 首次兑换：拿到令牌对。
	exchangeRecorder := postOAuthCodeExchange(t, handler, code)
	require.Equal(t, http.StatusOK, exchangeRecorder.Code)
	var body struct {
		Code int `json:"code"`
		Data struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int    `json:"expires_in"`
			TokenType    string `json:"token_type"`
			Redirect     string `json:"redirect"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(exchangeRecorder.Body.Bytes(), &body))
	require.Equal(t, 0, body.Code)
	require.NotEmpty(t, body.Data.AccessToken)
	require.NotEmpty(t, body.Data.RefreshToken)
	require.Equal(t, 3600, body.Data.ExpiresIn)
	require.Equal(t, "/dashboard", body.Data.Redirect)

	// 第二次兑换同一个 code：已消费，返回 401。
	secondRecorder := postOAuthCodeExchange(t, handler, code)
	require.Equal(t, http.StatusUnauthorized, secondRecorder.Code)
}

// 未知 code 直接拒绝，不泄漏任何信息。
func TestOAuthCodeExchangeRejectsUnknownCode(t *testing.T) {
	handler, _, _ := newOAuthCodeExchangeTestHandler(t)
	recorder := postOAuthCodeExchange(t, handler, "deadbeef")
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

// 回滚开关关闭时，仍走旧的 fragment 令牌流程（一个版本兼容窗口）。
func TestEmailOAuthCallbackKeepsLegacyFragmentWhenExchangeDisabled(t *testing.T) {
	handler, client, _ := newOAuthCodeExchangeTestHandler(t)
	handler.cfg.Security.OAuthCodeExchangeEnabled = false
	createOAuthExchangeTestUser(t, client)

	recorder := runOAuthCallback(t, handler, "google")
	require.Equal(t, http.StatusFound, recorder.Code)
	location := recorder.Header().Get("Location")
	require.Contains(t, location, "access_token=")
	require.NotContains(t, location, "code=")
}

// 缺少 code 时返回 400，不做任何兑换。
func TestOAuthCodeExchangeRejectsMissingCode(t *testing.T) {
	handler, _, _ := newOAuthCodeExchangeTestHandler(t)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/exchange", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	c.Request = req
	handler.OAuthCodeExchange(c)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}
