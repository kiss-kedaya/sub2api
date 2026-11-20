package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// 用脚本执行替身验证 AllowSliding 的计数/放行/RetryAfter 映射，
// 不依赖真实 Redis（真实滑动窗口语义由 integration 测试覆盖）。
func TestRateLimiterAllowSlidingMapping(t *testing.T) {
	originalRun := rateLimitSlidingRun
	t.Cleanup(func() { rateLimitSlidingRun = originalRun })

	counts := map[string]int64{}
	rateLimitSlidingRun = func(_ context.Context, _ *redis.Client, key string, _ int64, limit int64, _ string) (int64, int64, bool, error) {
		counts[key]++
		if counts[key] < limit {
			return counts[key] + 1, 0, true, nil
		}
		if counts[key] == limit {
			return counts[key], 0, true, nil
		}
		return counts[key], 1000, false, nil
	}

	limiter := NewRateLimiter(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}))
	for i := 1; i <= 5; i++ {
		result, err := limiter.AllowSliding(context.Background(), "2fa:verify:user:41", 5, time.Minute)
		require.NoError(t, err)
		require.True(t, result.Allowed, "attempt %d must be allowed", i)
	}
	result, err := limiter.AllowSliding(context.Background(), "2fa:verify:user:41", 5, time.Minute)
	require.NoError(t, err)
	require.False(t, result.Allowed, "6th attempt must be blocked")
	require.Equal(t, time.Second, result.RetryAfter)
}

func TestRateLimiterAllowSlidingDisabledLimitBypasses(t *testing.T) {
	limiter := NewRateLimiter(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}))
	result, err := limiter.AllowSliding(context.Background(), "k", 0, time.Minute)
	require.NoError(t, err)
	require.True(t, result.Allowed)
}
