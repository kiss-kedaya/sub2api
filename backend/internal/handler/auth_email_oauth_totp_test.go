package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// totpCacheStub 只实现 2FA 登录会话所需的缓存能力，其余方法不应被调用。
type totpCacheStub struct {
	sessions map[string]*service.TotpLoginSession
}

func newTotpCacheStub() *totpCacheStub {
	return &totpCacheStub{sessions: map[string]*service.TotpLoginSession{}}
}

func (s *totpCacheStub) GetSetupSession(context.Context, int64) (*service.TotpSetupSession, error) {
	return nil, nil
}

func (s *totpCacheStub) SetSetupSession(context.Context, int64, *service.TotpSetupSession, time.Duration) error {
	return nil
}

func (s *totpCacheStub) DeleteSetupSession(context.Context, int64) error { return nil }

func (s *totpCacheStub) GetLoginSession(_ context.Context, tempToken string) (*service.TotpLoginSession, error) {
	return s.sessions[tempToken], nil
}

func (s *totpCacheStub) SetLoginSession(_ context.Context, tempToken string, session *service.TotpLoginSession, _ time.Duration) error {
	s.sessions[tempToken] = session
	return nil
}

func (s *totpCacheStub) DeleteLoginSession(_ context.Context, tempToken string) error {
	delete(s.sessions, tempToken)
	return nil
}

func (s *totpCacheStub) IncrementVerifyAttempts(context.Context, int64) (int, error) { return 0, nil }

func (s *totpCacheStub) GetVerifyAttempts(context.Context, int64) (int, error) { return 0, nil }

func (s *totpCacheStub) ClearVerifyAttempts(context.Context, int64) error { return nil }

func (s *totpCacheStub) SetStepUpGrant(context.Context, int64, string, time.Duration) error {
	return nil
}

func (s *totpCacheStub) HasStepUpGrant(context.Context, int64, string) (bool, error) {
	return true, nil
}

// 已启用 TOTP 的账号走第三方 OAuth 登录时，后端只能下发 2FA 挑战，
// 不得在重定向片段里携带任何 access/refresh token。
func TestEmailOAuthTOTPChallengeRedirectCarriesNoTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cache := newTotpCacheStub()
	totpService := service.NewTotpService(nil, nil, cache, nil, nil, nil)
	h := NewAuthHandler(&config.Config{}, nil, nil, nil, nil, nil, totpService, nil)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/github/callback", nil)

	h.redirectEmailOAuthTOTPChallenge(c, &service.User{
		ID:          7,
		Email:       "user@example.com",
		Role:        service.RoleUser,
		TotpEnabled: true,
	}, "github", "/auth/oauth/callback", "/dashboard")

	require.Equal(t, http.StatusFound, rec.Code)
	location := rec.Header().Get("Location")
	require.NotEmpty(t, location)

	parsed, err := url.Parse(location)
	require.NoError(t, err)
	fragment, err := url.ParseQuery(parsed.Fragment)
	require.NoError(t, err)

	require.Equal(t, "1", fragment.Get("requires_2fa"))
	require.NotEmpty(t, fragment.Get("temp_token"))
	require.Equal(t, "/dashboard", fragment.Get("redirect"))
	require.Equal(t, "github", fragment.Get("provider"))
	require.NotContains(t, parsed.Fragment, "access_token")
	require.NotContains(t, parsed.Fragment, "refresh_token")

	// 临时会话必须真实可查到，否则前端拿码也无法完成登录。
	session, err := cache.GetLoginSession(context.Background(), fragment.Get("temp_token"))
	require.NoError(t, err)
	require.NotNil(t, session)
	require.Equal(t, int64(7), session.UserID)
}

// 缺少 TOTP 服务时必须 fail-closed，不能退化为直接登录。
func TestEmailOAuthTOTPChallengeFailsClosedWithoutTotpService(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewAuthHandler(&config.Config{}, nil, nil, nil, nil, nil, nil, nil)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/github/callback", nil)

	h.redirectEmailOAuthTOTPChallenge(c, &service.User{ID: 7, Email: "user@example.com"}, "github", "/auth/oauth/callback", "/dashboard")

	require.Equal(t, http.StatusFound, rec.Code)
	location := rec.Header().Get("Location")
	parsed, err := url.Parse(location)
	require.NoError(t, err)
	fragment, err := url.ParseQuery(parsed.Fragment)
	require.NoError(t, err)
	require.Equal(t, "two_factor_unavailable", fragment.Get("error"))
	require.NotContains(t, location, "access_token")
}
