//go:build integration

package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 真实 Redis 上的滚动窗口语义：60s 内最多 5 次，第 6 次被拒并给出 Retry-After；
// 窗口过去后恢复；不同 key 互不影响。
func TestRateLimiterAllowSlidingWindow(t *testing.T) {
	ctx := context.Background()
	rdb := startRedis(t, ctx)
	limiter := NewRateLimiter(rdb)
	window := 2 * time.Second

	for i := 1; i <= 5; i++ {
		result, err := limiter.AllowSliding(ctx, "2fa:verify:user:41", 5, window)
		require.NoError(t, err)
		require.True(t, result.Allowed, "attempt %d must be allowed", i)
	}

	result, err := limiter.AllowSliding(ctx, "2fa:verify:user:41", 5, window)
	require.NoError(t, err)
	require.False(t, result.Allowed)
	require.Greater(t, result.RetryAfter, time.Duration(0))
	require.LessOrEqual(t, result.RetryAfter, window)

	// 另一个账号不受同一窗口影响。
	other, err := limiter.AllowSliding(ctx, "2fa:verify:user:42", 5, window)
	require.NoError(t, err)
	require.True(t, other.Allowed)

	// 窗口过期后恢复。
	time.Sleep(window + 200*time.Millisecond)
	recovered, err := limiter.AllowSliding(ctx, "2fa:verify:user:41", 5, window)
	require.NoError(t, err)
	require.True(t, recovered.Allowed)

	// Reset 清空后立即恢复。
	for i := 0; i < 5; i++ {
		_, _ = limiter.AllowSliding(ctx, "2fa:verify:user:43", 5, window)
	}
	require.NoError(t, limiter.Delete(ctx, "2fa:verify:user:43"))
	reset, err := limiter.AllowSliding(ctx, "2fa:verify:user:43", 5, window)
	require.NoError(t, err)
	require.True(t, reset.Allowed)
}
