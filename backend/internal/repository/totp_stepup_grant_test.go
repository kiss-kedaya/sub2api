//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newStepUpGrantTestCache(t *testing.T) (service.TotpCache, *redis.Client, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewTotpCache(client), client, server
}

// 授权窗口必须是 5 分钟（与链上充值 crypto grant 对齐），而不是原来的 15 分钟。
func TestStepUpGrantTTLIsFiveMinutes(t *testing.T) {
	require.Equal(t, 5*time.Minute, service.StepUpGrantTTL)

	cache, _, _ := newStepUpGrantTestCache(t)
	ctx := context.Background()
	require.NoError(t, cache.SetStepUpGrant(ctx, 42, "session-a", service.StepUpGrantTTL))

	granted, err := cache.HasStepUpGrant(ctx, 42, "session-a")
	require.NoError(t, err)
	require.True(t, granted)

	remaining, ok, err := cache.StepUpGrantRemaining(ctx, 42, "session-a")
	require.NoError(t, err)
	require.True(t, ok, "刚签发的授权必须能读到剩余时间")
	require.Greater(t, remaining, 4*time.Minute+59*time.Second)
	require.LessOrEqual(t, remaining, 5*time.Minute)
}

// 边界：4:59 仍在窗口内（还能免验证码，剩余秒数 > 0）。
func TestStepUpGrantStillValidAtFourFiftyNine(t *testing.T) {
	cache, _, server := newStepUpGrantTestCache(t)
	ctx := context.Background()
	require.NoError(t, cache.SetStepUpGrant(ctx, 42, "session-a", service.StepUpGrantTTL))
	server.FastForward(4*time.Minute + 59*time.Second)

	granted, err := cache.HasStepUpGrant(ctx, 42, "session-a")
	require.NoError(t, err)
	require.True(t, granted, "4:59 时授权仍然有效")

	remaining, ok, err := cache.StepUpGrantRemaining(ctx, 42, "session-a")
	require.NoError(t, err)
	require.True(t, ok)
	require.Greater(t, remaining, time.Duration(0))
	require.LessOrEqual(t, remaining, 2*time.Second)
}

// 边界：5:01 已失效（必须重新输码，剩余时间读不到）。
func TestStepUpGrantInvalidAfterFiveMinutesOneSecond(t *testing.T) {
	cache, _, server := newStepUpGrantTestCache(t)
	ctx := context.Background()
	require.NoError(t, cache.SetStepUpGrant(ctx, 42, "session-a", service.StepUpGrantTTL))
	server.FastForward(5*time.Minute + time.Second)

	granted, err := cache.HasStepUpGrant(ctx, 42, "session-a")
	require.NoError(t, err)
	require.False(t, granted, "5:01 时授权必须失效")

	_, ok, err := cache.StepUpGrantRemaining(ctx, 42, "session-a")
	require.NoError(t, err)
	require.False(t, ok)
}

// 剩余时间必须随真实 TTL 递减，而不是每次都返回常量 5 分钟。
func TestStepUpGrantRemainingCountsDown(t *testing.T) {
	cache, _, server := newStepUpGrantTestCache(t)
	ctx := context.Background()
	require.NoError(t, cache.SetStepUpGrant(ctx, 42, "session-a", service.StepUpGrantTTL))

	server.FastForward(time.Minute)
	remaining, ok, err := cache.StepUpGrantRemaining(ctx, 42, "session-a")
	require.NoError(t, err)
	require.True(t, ok)
	require.Greater(t, remaining, 3*time.Minute+59*time.Second)
	require.LessOrEqual(t, remaining, 4*time.Minute)
}

// 没有授权时读不到剩余时间（前端不应显示跳过提示）。
func TestStepUpGrantRemainingMissing(t *testing.T) {
	cache, _, _ := newStepUpGrantTestCache(t)
	remaining, ok, err := cache.StepUpGrantRemaining(context.Background(), 42, "session-a")
	require.NoError(t, err)
	require.False(t, ok)
	require.Zero(t, remaining)
}

// 授权 key 绑定用户+会话：其它会话读不到剩余窗口。
func TestStepUpGrantRemainingIsSessionBound(t *testing.T) {
	cache, _, _ := newStepUpGrantTestCache(t)
	ctx := context.Background()
	require.NoError(t, cache.SetStepUpGrant(ctx, 42, "session-a", service.StepUpGrantTTL))

	_, ok, err := cache.StepUpGrantRemaining(ctx, 42, "session-b")
	require.NoError(t, err)
	require.False(t, ok, "另一个会话不能看到该授权")

	_, ok, err = cache.StepUpGrantRemaining(ctx, 43, "session-a")
	require.NoError(t, err)
	require.False(t, ok, "另一个用户不能看到该授权")
}

// 残留的无过期时间 key 属于异常状态：HasStepUpGrant 会看到 key 存在，但
// Remaining 必须按「无授权」处理，门控据此 fail-closed 拒绝，而不是放行。
func TestStepUpGrantRemainingRejectsNonExpiringKey(t *testing.T) {
	cache, client, _ := newStepUpGrantTestCache(t)
	ctx := context.Background()

	require.NoError(t, client.Set(ctx, totpStepUpKey(42, "session-a"), "1", 0).Err())

	exists, err := cache.HasStepUpGrant(ctx, 42, "session-a")
	require.NoError(t, err)
	require.True(t, exists)

	_, ok, err := cache.StepUpGrantRemaining(ctx, 42, "session-a")
	require.NoError(t, err)
	require.False(t, ok, "无 TTL 的 key 不能当作有效授权")
}
