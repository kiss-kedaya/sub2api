package server

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type budgetWiringUsers struct {
	service.UserRepository
	user *service.User
}

func (r *budgetWiringUsers) GetByID(context.Context, int64) (*service.User, error) {
	return r.user, nil
}

// 登录密码因子的按用户失败预算必须真的接到 APIKey handler 的校验服务上：
// 装配漏了一行，所有走密码因子的入口（创建 / 轮换 / 封存 / 二次查看）就会悄悄退回「不限次数的密码 oracle」。
func TestConfigureAccountVerificationBudgetsWiresTheAPIKeyPasswordBudget(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	user := &service.User{ID: 7, Email: "owner@example.com", Status: service.StatusActive, SignupSource: "email"}
	require.NoError(t, user.SetPassword("correct-password"))
	verification := service.NewAPIKeyVerificationService(&budgetWiringUsers{user: user}, nil, nil, nil, nil, nil)
	apiKeys := handler.NewAPIKeyHandler(nil, nil)
	apiKeys.SetVerificationService(verification)
	handlers := &handler.Handlers{APIKey: apiKeys}

	configureAccountVerificationBudgets(handlers, client)

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
