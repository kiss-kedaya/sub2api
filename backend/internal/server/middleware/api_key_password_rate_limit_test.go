package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// API Key 身份校验的登录密码因子用「和 2FA 同一套实现、独立计数桶」的按用户失败预算：
// 共用一个桶的话，密码校验成功会清掉 2FA 的失败计数（给 TOTP 猜码续命），
// 持会话者故意输错密码还能把账号的登录 2FA 锁住。

func newPasswordBudgetForTest(f *fakeSlidingLimiter) *TwoFactorRateLimiter {
	return &TwoFactorRateLimiter{limiter: f, keyPrefix: apiKeyPasswordRateLimitKeyPrefix}
}

func TestAPIKeyPasswordBudgetHasItsOwnBucketPerUser(t *testing.T) {
	f := newFakeSlidingLimiter()
	password := newPasswordBudgetForTest(f)
	twoFactor := newTwoFactorLimiterForTest(f)
	ctx := context.Background()

	for i := 1; i <= TwoFactorRateLimitMaxAttempts; i++ {
		allowed, _, err := password.Check(ctx, 41)
		require.NoError(t, err)
		require.True(t, allowed, "password attempt %d", i)
	}
	allowed, retryAfter, err := password.Check(ctx, 41)
	require.NoError(t, err)
	require.False(t, allowed, "the 6th password attempt inside the window must be blocked")
	require.Greater(t, retryAfter, time.Duration(0))

	// 密码预算用尽不能碰账号的 2FA 预算，也不影响别的用户。
	allowed, _, err = twoFactor.Check(ctx, 41)
	require.NoError(t, err)
	require.True(t, allowed, "an exhausted password budget must not lock the account's 2FA")
	allowed, _, err = password.Check(ctx, 42)
	require.NoError(t, err)
	require.True(t, allowed, "the budget is per account")

	require.Contains(t, f.attempts, "api_key_password:verify:user:41")
	require.Contains(t, f.attempts, "2fa:verify:user:41")
}

func TestAPIKeyPasswordBudgetResetOnlyClearsItsOwnBucket(t *testing.T) {
	f := newFakeSlidingLimiter()
	password := newPasswordBudgetForTest(f)
	twoFactor := newTwoFactorLimiterForTest(f)
	ctx := context.Background()

	// 2FA 已经失败了 4 次，密码也失败了 3 次。
	for i := 0; i < 4; i++ {
		_, _, err := twoFactor.Check(ctx, 41)
		require.NoError(t, err)
	}
	for i := 0; i < 3; i++ {
		_, _, err := password.Check(ctx, 41)
		require.NoError(t, err)
	}

	// 密码校验成功：只清密码的计数，2FA 的 4 次失败必须原样保留。
	password.Reset(ctx, 41)
	require.Equal(t, []string{"api_key_password:verify:user:41"}, f.deleted)
	require.NotContains(t, f.attempts, "api_key_password:verify:user:41")
	require.Len(t, f.attempts["2fa:verify:user:41"], 4)

	allowed, _, err := twoFactor.Check(ctx, 41)
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, _, err = twoFactor.Check(ctx, 41)
	require.NoError(t, err)
	require.False(t, allowed, "a password success must not refill the 2FA guess budget")

	// 反过来 2FA 成功清零也不会清掉密码的失败计数。
	for i := 0; i < 3; i++ {
		_, _, err = password.Check(ctx, 41)
		require.NoError(t, err)
	}
	twoFactor.Reset(ctx, 41)
	require.Len(t, f.attempts["api_key_password:verify:user:41"], 3)
}

func TestAPIKeyPasswordBudgetConstructor(t *testing.T) {
	require.Nil(t, NewAPIKeyPasswordRateLimiter(nil), "no redis, no budget (single-node / tests)")

	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	t.Cleanup(func() { _ = client.Close() })
	limiter := NewAPIKeyPasswordRateLimiter(client)
	require.NotNil(t, limiter)
	require.Equal(t, "api_key_password:verify:user:7", limiter.key(7))
	require.Equal(t, "2fa:verify:user:7", NewTwoFactorRateLimiter(client).key(7), "the 2FA bucket keeps its key")
	require.Equal(t, TwoFactorRateLimitKey(7), NewTwoFactorRateLimiter(client).key(7))
}

func TestAPIKeyPasswordBudgetFailsClosedWithoutStore(t *testing.T) {
	var limiter *TwoFactorRateLimiter
	allowed, _, err := limiter.Check(context.Background(), 41)
	require.Error(t, err)
	require.False(t, allowed)
}
