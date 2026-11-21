package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type fakeTwoFactorLimiter struct {
	allowed    bool
	retryAfter time.Duration
	err        error
	checked    []int64
	reset      []int64
}

func (f *fakeTwoFactorLimiter) Check(_ context.Context, userID int64) (bool, time.Duration, error) {
	f.checked = append(f.checked, userID)
	return f.allowed, f.retryAfter, f.err
}

func (f *fakeTwoFactorLimiter) Reset(_ context.Context, userID int64) {
	f.reset = append(f.reset, userID)
}

func TestCheckTwoFactorRateLimitBlockedWrites429(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/2fa", nil)

	limiter := &fakeTwoFactorLimiter{allowed: false, retryAfter: 42 * time.Second}
	attemptCtx, ok := checkTwoFactorRateLimit(c, limiter, 41)
	require.False(t, ok)
	require.Nil(t, attemptCtx)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, "42", recorder.Header().Get("Retry-After"))
	require.Contains(t, recorder.Body.String(), "TWO_FACTOR_RATE_LIMITED")
	// 不泄露剩余次数/账号信息。
	require.NotContains(t, recorder.Body.String(), "remaining")
	require.Equal(t, []int64{41}, limiter.checked)
}

func TestCheckTwoFactorRateLimitStoreFailureFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/user/totp/step-up", nil)

	limiter := &fakeTwoFactorLimiter{err: errors.New("redis down")}
	attemptCtx, ok := checkTwoFactorRateLimit(c, limiter, 41)
	require.False(t, ok)
	require.Nil(t, attemptCtx)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "TWO_FACTOR_RATE_LIMIT_UNAVAILABLE")
}

func TestCheckTwoFactorRateLimitAllowsAndResetsBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/2fa", nil)

	limiter := &fakeTwoFactorLimiter{allowed: true}
	originalCtx := c.Request.Context()
	attemptCtx, ok := checkTwoFactorRateLimit(c, limiter, 41)
	require.True(t, ok)
	require.NotNil(t, attemptCtx)
	require.Equal(t, originalCtx, c.Request.Context())
	require.Equal(t, []int64{41}, limiter.checked)

	resetTwoFactorRateLimit(context.Background(), limiter, 41)
	require.Equal(t, []int64{41}, limiter.reset)
}

// 2FA 登录校验入口在超限时必须在调用验证码校验前返回 429。
func TestLogin2FABlockedByAccountRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	totpCache := newTotpCacheStub()
	totpService := service.NewTotpService(nil, nil, totpCache, nil, nil, nil)
	h := NewAuthHandler(&config.Config{}, nil, nil, nil, nil, nil, totpService, nil)
	h.SetTwoFactorRateLimiter(&fakeTwoFactorLimiter{allowed: false, retryAfter: 30 * time.Second})

	tempToken, err := totpService.CreateLoginSession(context.Background(), 41, "user@example.com")
	require.NoError(t, err)

	body := `{"temp_token":"` + tempToken + `","totp_code":"123456"}`
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/2fa", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Login2FA(c)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, "30", recorder.Header().Get("Retry-After"))

	var payload map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, "TWO_FACTOR_RATE_LIMITED", payload["reason"])
}

// step-up 校验同样受账号维度限速。
func TestStepUpBlockedByAccountRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewTotpHandler(service.NewTotpService(nil, nil, newTotpCacheStub(), nil, nil, nil))
	h.SetTwoFactorRateLimiter(&fakeTwoFactorLimiter{allowed: false, retryAfter: 60 * time.Second})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/user/totp/step-up", strings.NewReader(`{"code":"123456"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 41})

	h.StepUp(c)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, "60", recorder.Header().Get("Retry-After"))
}
