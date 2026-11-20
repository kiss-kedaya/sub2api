package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	intmw "github.com/Wei-Shaw/sub2api/internal/middleware"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// fakeSlidingLimiter 用内存滑动窗口模拟 Redis 原语，便于验证账号维度与窗口语义。
type fakeSlidingLimiter struct {
	now      time.Time
	attempts map[string][]time.Time
	deleted  []string
	err      error
}

func newFakeSlidingLimiter() *fakeSlidingLimiter {
	return &fakeSlidingLimiter{now: time.Date(2026, 11, 21, 3, 0, 0, 0, time.UTC), attempts: map[string][]time.Time{}}
}

func (f *fakeSlidingLimiter) AllowSliding(_ context.Context, key string, limit int, window time.Duration) (intmw.AllowResult, error) {
	if f.err != nil {
		return intmw.AllowResult{}, f.err
	}
	cutoff := f.now.Add(-window)
	kept := make([]time.Time, 0, len(f.attempts[key]))
	for _, at := range f.attempts[key] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) < limit {
		kept = append(kept, f.now)
		f.attempts[key] = kept
		return intmw.AllowResult{Allowed: true, Count: int64(len(kept))}, nil
	}
	retry := kept[0].Add(window).Sub(f.now)
	f.attempts[key] = kept
	return intmw.AllowResult{Allowed: false, Count: int64(len(kept)), RetryAfter: retry}, nil
}

func (f *fakeSlidingLimiter) Delete(_ context.Context, key string) error {
	f.deleted = append(f.deleted, key)
	delete(f.attempts, key)
	return nil
}

func newTwoFactorLimiterForTest(f *fakeSlidingLimiter) *TwoFactorRateLimiter {
	return &TwoFactorRateLimiter{limiter: f}
}

func TestTwoFactorRateLimiterBlocksSixthAttemptPerAccount(t *testing.T) {
	f := newFakeSlidingLimiter()
	limiter := newTwoFactorLimiterForTest(f)
	ctx := context.Background()

	for i := 1; i <= TwoFactorRateLimitMaxAttempts; i++ {
		allowed, _, err := limiter.Check(ctx, 41)
		require.NoError(t, err)
		require.True(t, allowed, "attempt %d must be allowed", i)
	}

	allowed, retryAfter, err := limiter.Check(ctx, 41)
	require.NoError(t, err)
	require.False(t, allowed, "6th attempt inside the window must be blocked")
	require.Greater(t, retryAfter, time.Duration(0))
	require.LessOrEqual(t, retryAfter, TwoFactorRateLimitWindow)
	require.Equal(t, "2fa:verify:user:41", TwoFactorRateLimitKey(41))
}

func TestTwoFactorRateLimiterIsPerAccount(t *testing.T) {
	f := newFakeSlidingLimiter()
	limiter := newTwoFactorLimiterForTest(f)
	ctx := context.Background()

	for i := 0; i < TwoFactorRateLimitMaxAttempts; i++ {
		_, _, err := limiter.Check(ctx, 41)
		require.NoError(t, err)
	}
	blocked, _, err := limiter.Check(ctx, 41)
	require.NoError(t, err)
	require.False(t, blocked)

	// 另一个账号不受影响。
	for i := 0; i < TwoFactorRateLimitMaxAttempts; i++ {
		allowed, _, err := limiter.Check(ctx, 42)
		require.NoError(t, err)
		require.True(t, allowed, "other account attempt %d must be allowed", i+1)
	}
}

func TestTwoFactorRateLimiterRecoversAfterWindow(t *testing.T) {
	f := newFakeSlidingLimiter()
	limiter := newTwoFactorLimiterForTest(f)
	ctx := context.Background()

	for i := 0; i < TwoFactorRateLimitMaxAttempts; i++ {
		_, _, _ = limiter.Check(ctx, 41)
	}
	blocked, _, _ := limiter.Check(ctx, 41)
	require.False(t, blocked)

	// 60 秒滚动窗口过去后恢复。
	f.now = f.now.Add(TwoFactorRateLimitWindow + time.Second)
	allowed, _, err := limiter.Check(ctx, 41)
	require.NoError(t, err)
	require.True(t, allowed, "attempts outside the rolling window must be allowed again")
}

func TestTwoFactorRateLimiterResetAfterSuccess(t *testing.T) {
	f := newFakeSlidingLimiter()
	limiter := newTwoFactorLimiterForTest(f)
	ctx := context.Background()

	for i := 0; i < TwoFactorRateLimitMaxAttempts; i++ {
		_, _, _ = limiter.Check(ctx, 41)
	}
	limiter.Reset(ctx, 41)
	require.Contains(t, f.deleted, "2fa:verify:user:41")

	allowed, _, err := limiter.Check(ctx, 41)
	require.NoError(t, err)
	require.True(t, allowed, "successful verification resets the account budget")
}

func TestTwoFactorRateLimiterStoreFailureIsExplicit(t *testing.T) {
	f := newFakeSlidingLimiter()
	f.err = errors.New("redis down")
	limiter := newTwoFactorLimiterForTest(f)

	allowed, _, err := limiter.Check(context.Background(), 41)
	require.Error(t, err)
	require.False(t, allowed)
}

func TestAbortTwoFactorRateLimitedSetsRetryAfterWithoutDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/2fa", nil)

	AbortTwoFactorRateLimited(c, 1500*time.Millisecond)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, "2", recorder.Header().Get("Retry-After"))
	require.Contains(t, recorder.Body.String(), "TWO_FACTOR_RATE_LIMITED")
	require.NotContains(t, recorder.Body.String(), "remaining")
}

func TestAbortTwoFactorRateLimitUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/2fa", nil)

	AbortTwoFactorRateLimitUnavailable(c)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "TWO_FACTOR_RATE_LIMIT_UNAVAILABLE")
}
