//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/stretchr/testify/require"
)

// 同一会话里 TOTP 与 Passkey 的 step-up 授权按因子隔离、各自 5 分钟：
// 只完成一个因子时另一个因子必须仍为「未授权」。
func TestStepUpGrantFactorsAreIsolated(t *testing.T) {
	cache, _, server := newStepUpGrantTestCache(t)
	ctx := context.Background()

	require.NoError(t, cache.SetStepUpGrant(ctx, 42, "session-a", service.FactorTOTP, service.StepUpGrantTTL))

	granted, err := cache.HasStepUpGrant(ctx, 42, "session-a", service.FactorTOTP)
	require.NoError(t, err)
	require.True(t, granted)

	granted, err = cache.HasStepUpGrant(ctx, 42, "session-a", service.FactorPasskey)
	require.NoError(t, err)
	require.False(t, granted, "TOTP 授权不能同时满足 Passkey 因子")

	// 补上 Passkey 因子后两个都有效。
	require.NoError(t, cache.SetStepUpGrant(ctx, 42, "session-a", service.FactorPasskey, service.StepUpGrantTTL))
	for _, factor := range []service.Factor{service.FactorTOTP, service.FactorPasskey} {
		granted, err = cache.HasStepUpGrant(ctx, 42, "session-a", factor)
		require.NoError(t, err)
		require.True(t, granted, "factor=%s", factor)
		remaining, ok, err := cache.StepUpGrantRemaining(ctx, 42, "session-a", factor)
		require.NoError(t, err)
		require.True(t, ok)
		require.Greater(t, remaining, 4*time.Minute)
	}

	// 统一 5 分钟窗口过期后都失效（Redis TTL 由 miniredis 快进验证）。
	server.FastForward(5*time.Minute + time.Second)
	granted, err = cache.HasStepUpGrant(ctx, 42, "session-a", service.FactorTOTP)
	require.NoError(t, err)
	require.False(t, granted)
	granted, err = cache.HasStepUpGrant(ctx, 42, "session-a", service.FactorPasskey)
	require.NoError(t, err)
	require.False(t, granted)
}

// break-glass 重置 2FA 靠 TotpStepUpGrantKeys 枚举授权键：它必须与 SetStepUpGrant 实际
// 写入的每个因子键一一对应，并保留旧布局键（滚动发布期间旧二进制仍按旧布局读写）。
func TestTotpStepUpGrantKeysCoverEveryWrittenKeyAndLegacyLayout(t *testing.T) {
	cache, client, _ := newStepUpGrantTestCache(t)
	ctx := context.Background()

	for _, factor := range service.AllFactors() {
		require.NoError(t, cache.SetStepUpGrant(ctx, 42, "session-a", factor, service.StepUpGrantTTL))
	}
	// 旧二进制按旧布局（没有因子维度）写入的授权。
	require.NoError(t, client.Set(ctx, "totp:stepup:42:session-a", "1", service.StepUpGrantTTL).Err())
	// 别的会话 / 别的用户的授权不能被枚举进来。
	require.NoError(t, cache.SetStepUpGrant(ctx, 42, "session-b", service.FactorTOTP, service.StepUpGrantTTL))
	require.NoError(t, cache.SetStepUpGrant(ctx, 43, "session-a", service.FactorTOTP, service.StepUpGrantTTL))

	keys := TotpStepUpGrantKeys(42, "session-a")
	written, err := client.Keys(ctx, "totp:stepup:42:*session-a").Result()
	require.NoError(t, err)
	require.ElementsMatch(t, written, keys, "撤销键必须与实际写入的键一一对应")
	// 键布局是新旧二进制共享的线上契约：改动它必须同时评估 break-glass 撤销。
	require.ElementsMatch(t, []string{
		"totp:stepup:42:totp:session-a",
		"totp:stepup:42:passkey:session-a",
		"totp:stepup:42:session-a",
	}, keys)
}

// 非法因子不写也不读（fail-closed）。
func TestStepUpGrantRejectsInvalidFactor(t *testing.T) {
	cache, _, _ := newStepUpGrantTestCache(t)
	ctx := context.Background()

	require.Error(t, cache.SetStepUpGrant(ctx, 42, "session-a", service.Factor("sms"), service.StepUpGrantTTL))
	granted, err := cache.HasStepUpGrant(ctx, 42, "session-a", service.Factor("sms"))
	require.Error(t, err)
	require.False(t, granted)
}
