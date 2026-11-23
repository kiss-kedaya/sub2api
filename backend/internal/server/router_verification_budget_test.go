package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// budgetWiringUsers 给任意用户 ID 都返回一个口令为 password 的邮箱注册账号（口令哈希只算一次）。
type budgetWiringUsers struct {
	service.UserRepository
	hash string
}

func newBudgetWiringUsers(t *testing.T, password string) *budgetWiringUsers {
	t.Helper()
	seed := &service.User{}
	require.NoError(t, seed.SetPassword(password))
	return &budgetWiringUsers{hash: seed.PasswordHash}
}

func (r *budgetWiringUsers) GetByID(_ context.Context, id int64) (*service.User, error) {
	return &service.User{ID: id, Email: "owner@example.com", Status: service.StatusActive, SignupSource: "email", PasswordHash: r.hash}, nil
}

// budgetWiringFixture 用真实的 Redis 协议实现（miniredis）把预算装配到一个 APIKey handler 的校验服务上。
func budgetWiringFixture(t *testing.T) (*service.APIKeyVerificationService, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	verification := service.NewAPIKeyVerificationService(newBudgetWiringUsers(t, "correct-password"), nil, nil, nil, nil, nil)
	apiKeys := handler.NewAPIKeyHandler(nil, nil)
	apiKeys.SetVerificationService(verification)
	configureAccountVerificationBudgets(&handler.Handlers{APIKey: apiKeys}, client)
	return verification, redisServer, client
}

// 登录密码因子的按用户失败预算必须真的接到 APIKey handler 的校验服务上：
// 装配漏了一行，所有走密码因子的入口（创建 / 轮换 / 封存 / 二次查看）就会悄悄退回「不限次数的密码 oracle」。
func TestConfigureAccountVerificationBudgetsWiresTheAPIKeyPasswordBudget(t *testing.T) {
	verification, redisServer, _ := budgetWiringFixture(t)

	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		require.ErrorIs(t, verification.VerifyForSeal(ctx, 7, "wrong-password"), service.ErrAPIKeyPasswordInvalid, "guess %d", i)
	}
	require.ErrorIs(t, verification.VerifyForSeal(ctx, 7, "correct-password"), service.ErrAPIKeyPasswordRateLimited,
		"the 6th attempt must be throttled even with the right password")

	// 预算在共享的 Redis 里（多实例一致），而且用的是密码因子自己的桶，不是账号 2FA 的桶。
	var passwordBucket, twoFactorBucket bool
	for _, key := range redisServer.Keys() {
		passwordBucket = passwordBucket || strings.Contains(key, "api_key_password:verify:user:7")
		twoFactorBucket = twoFactorBucket || strings.Contains(key, "2fa:verify:user:7")
	}
	require.True(t, passwordBucket, "keys: %v", redisServer.Keys())
	require.False(t, twoFactorBucket, "the password budget must not use the account's 2FA bucket")
}

// 真实的 Redis 滑动窗口：上限 5 次、按用户、成功清零——整条链路（校验服务 + 装配 + 限速器）一起测。
func TestAPIKeyPasswordBudgetOnRedisIsPerUserAndClearsOnSuccess(t *testing.T) {
	verification, _, _ := budgetWiringFixture(t)
	ctx := context.Background()

	// 按用户：用户 7 用尽预算，用户 8 不受影响。
	for i := 1; i <= 5; i++ {
		require.ErrorIs(t, verification.VerifyForSeal(ctx, 7, "wrong-password"), service.ErrAPIKeyPasswordInvalid)
	}
	require.ErrorIs(t, verification.VerifyForSeal(ctx, 7, "correct-password"), service.ErrAPIKeyPasswordRateLimited)
	require.NoError(t, verification.VerifyForSeal(ctx, 8, "correct-password"), "another user has an independent budget")

	// 成功清零：用户 9 连续失败 4 次，一次成功后又是一整份预算。
	for i := 1; i <= 4; i++ {
		require.ErrorIs(t, verification.VerifyForSeal(ctx, 9, "wrong-password"), service.ErrAPIKeyPasswordInvalid)
	}
	require.NoError(t, verification.VerifyForSeal(ctx, 9, "correct-password"))
	for i := 1; i <= 5; i++ {
		require.ErrorIs(t, verification.VerifyForSeal(ctx, 9, "wrong-password"), service.ErrAPIKeyPasswordInvalid, "guess %d after the reset", i)
	}
	require.ErrorIs(t, verification.VerifyForSeal(ctx, 9, "wrong-password"), service.ErrAPIKeyPasswordRateLimited)

	// 带窗口信息：429 带 retry_after，且不超过一个窗口（60 秒）。
	err := verification.VerifyForSeal(ctx, 9, "correct-password")
	require.ErrorIs(t, err, service.ErrAPIKeyPasswordRateLimited)
	seconds := service.RetryAfterSecondsFromError(err)
	require.Greater(t, seconds, 0)
	require.LessOrEqual(t, seconds, 60)
}

// 密码预算与账号的 2FA 预算互相独立：密码校验成功不能给 TOTP 猜码续命，
// 密码猜错也不能把账号的登录 2FA 锁住。
func TestAPIKeyPasswordBudgetIsIndependentOfTheTwoFactorBudgetOnRedis(t *testing.T) {
	verification, _, client := budgetWiringFixture(t)
	ctx := context.Background()
	twoFactor := middleware.NewTwoFactorRateLimiter(client)

	// 滑动窗口脚本用纳秒时间戳当 ZSET 成员；Windows 开发机上系统时钟一个刻度可以长达十几毫秒，
	// 紧挨着的两次手工尝试会得到同一个成员而被去重，所以这里每次之间隔开一点（Linux 上不会发生）。
	spend := func(userID int64, n int) {
		for i := 0; i < n; i++ {
			allowed, _, err := twoFactor.Check(ctx, userID)
			require.NoError(t, err)
			require.True(t, allowed, "2FA attempt %d should still be inside the budget", i+1)
			time.Sleep(25 * time.Millisecond)
		}
	}

	// 用户 7：2FA 已失败 4 次；密码校验成功只能清密码的桶，2FA 的 4 次失败原样保留。
	spend(7, 4)
	require.NoError(t, verification.VerifyForSeal(ctx, 7, "correct-password"))
	spend(7, 1) // 第 5 次：仍在预算内
	allowed, _, err := twoFactor.Check(ctx, 7)
	require.NoError(t, err)
	require.False(t, allowed, "a password success must not refill the 2FA guess budget")

	// 用户 8：密码预算被刷满，账号的 2FA 预算照样可用。
	for i := 1; i <= 5; i++ {
		require.ErrorIs(t, verification.VerifyForSeal(ctx, 8, "wrong-password"), service.ErrAPIKeyPasswordInvalid)
	}
	require.ErrorIs(t, verification.VerifyForSeal(ctx, 8, "correct-password"), service.ErrAPIKeyPasswordRateLimited)
	allowed, _, err = twoFactor.Check(ctx, 8)
	require.NoError(t, err)
	require.True(t, allowed, "an exhausted password budget must not lock the account's 2FA")
}

// 预算存储（Redis）故障时 fail-closed：校验不通过，而不是退回「不限次数的密码 oracle」。
func TestAPIKeyPasswordBudgetFailsClosedWhenRedisIsDown(t *testing.T) {
	verification, redisServer, _ := budgetWiringFixture(t)
	redisServer.Close()

	err := verification.VerifyForSeal(context.Background(), 7, "correct-password")
	require.ErrorIs(t, err, service.ErrAPIKeyVerificationUnavailable)
}

func TestConfigureAccountVerificationBudgetsToleratesMissingPieces(t *testing.T) {
	// 没有 Redis（单机 / 单测）：什么都不装配，也不能 panic。
	configureAccountVerificationBudgets(&handler.Handlers{APIKey: handler.NewAPIKeyHandler(nil, nil)}, nil)
	configureAccountVerificationBudgets(nil, nil)

	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	configureAccountVerificationBudgets(nil, client)
	configureAccountVerificationBudgets(&handler.Handlers{}, client)

	// 没装配校验服务的 APIKey handler（旧测试 / 嵌入方）：设置预算是空操作。
	configureAccountVerificationBudgets(&handler.Handlers{APIKey: handler.NewAPIKeyHandler(nil, nil)}, client)
}
