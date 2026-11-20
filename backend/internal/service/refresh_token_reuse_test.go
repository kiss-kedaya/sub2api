//go:build unit

package service_test

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// newRefreshReuseTestService 用最小 UserRepository 桩 + 完整 RefreshTokenCache 桩
// 构造认证服务：Refresh Token 有效期 7 天，保证墓碑 TTL 计算与生产一致。
func newRefreshReuseTestService(t *testing.T) (*service.AuthService, *service.User) {
	t.Helper()
	user := &service.User{
		ID:           41,
		Email:        "refresh-reuse@example.com",
		PasswordHash: "hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		Concurrency:  1,
	}
	repo := newEmailBindUserRepoStub(user)
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:                   "test-refresh-reuse-secret",
			ExpireHour:               1,
			AccessTokenExpireMinutes: 60,
			RefreshTokenExpireDays:   7,
		},
	}
	svc := service.NewAuthService(nil, repo, nil, newEmailBindRefreshTokenCacheStub(), cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	return svc, user
}

// 已消费（已轮换）的 Refresh Token 再次出现时，必须撤销整个 token family：
// 攻击者即使先用掉被盗 token，合法会话也会失效，不能长期并行。
func TestRefreshTokenReuseRevokesWholeFamily(t *testing.T) {
	svc, user := newRefreshReuseTestService(t)
	ctx := context.Background()

	pair1, err := svc.GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)

	// 正常轮换两次：每次旧 token 立即失效，新 token 可用。
	pair2, err := svc.RefreshTokenPair(ctx, pair1.RefreshToken)
	require.NoError(t, err)
	pair3, err := svc.RefreshTokenPair(ctx, pair2.RefreshToken)
	require.NoError(t, err)

	// 重放最早那个已消费 token：识别为复用并撤销整个 family。
	_, err = svc.RefreshTokenPair(ctx, pair1.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenReuse)

	// 全家已撤销：当前合法 token 也不能再刷新，用户需要重新登录。
	_, err = svc.RefreshTokenPair(ctx, pair3.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid)
}

// 正常轮换（无重放）不得误伤：连续刷新任意多次都应成功，且家族保持同一条链。
func TestRefreshTokenRotationWithoutReuseKeepsSession(t *testing.T) {
	svc, user := newRefreshReuseTestService(t)
	ctx := context.Background()

	pair, err := svc.GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	refreshToken := pair.RefreshToken
	for i := 0; i < 5; i++ {
		rotated, rotateErr := svc.RefreshTokenPair(ctx, refreshToken)
		require.NoError(t, rotateErr, "rotation %d must not be treated as reuse", i)
		refreshToken = rotated.RefreshToken
	}
}

// 网络重试场景（有意接受的取舍）：客户端已消费旧 token 但没收到响应，
// 重试同一个旧 token 会被判定为复用并让整条会话失效，用户重新登录。
func TestRefreshTokenRetryAfterLostResponseRevokesFamily(t *testing.T) {
	svc, user := newRefreshReuseTestService(t)
	ctx := context.Background()

	pair1, err := svc.GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	pair2, err := svc.RefreshTokenPair(ctx, pair1.RefreshToken)
	require.NoError(t, err)

	// 客户端重试旧 token（响应丢失场景）。
	_, err = svc.RefreshTokenPair(ctx, pair1.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenReuse)

	// 重试拿到的 pair2 也会随 family 一起失效。
	_, err = svc.RefreshTokenPair(ctx, pair2.RefreshToken)
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid)
}

// 完全未知的 token 不属于重放：返回 invalid，且不影响任何真实会话。
func TestRefreshTokenUnknownTokenIsInvalidNotReuse(t *testing.T) {
	svc, user := newRefreshReuseTestService(t)
	ctx := context.Background()

	pair, err := svc.GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)

	_, err = svc.RefreshTokenPair(ctx, "rt_0000000000000000000000000000000000000000000000000000000000000000")
	require.ErrorIs(t, err, service.ErrRefreshTokenInvalid)

	// 真实会话不受影响。
	_, err = svc.RefreshTokenPair(ctx, pair.RefreshToken)
	require.NoError(t, err)
}
