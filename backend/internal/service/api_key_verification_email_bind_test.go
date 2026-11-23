//go:build unit

package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// 审计 F16 的真实流程验证：第三方登录注册的账号，之后在资料页绑定邮箱并设置本地密码，
// 密码因子随之可用。这里不用任何替身——真实的 AuthService.BindEmailIdentity + 真实的
// UserRepository（sqlite），证明「绑定邮箱留下的证据」确实是 passwordKnownByUser 读到的那个，
// 也顺手钉住它依赖的 source 字面量（auth_email_binding.go 里改了，这个测试会红）。

func newEmailBindVerificationFixture(t *testing.T) (*service.AuthService, service.UserRepository, *service.APIKeyVerificationService, func(source string) int64) {
	t.Helper()
	cache := &emailBindCacheStub{
		data: &service.VerificationCodeData{
			Code:      "123456",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
	}
	authSvc, repo, client := newAuthServiceForEmailBind(t, map[string]string{}, cache, nil)
	verification := service.NewAPIKeyVerificationService(repo, nil, nil, nil, nil, nil)
	createUser := func(source string) int64 {
		user, err := client.User.Create().
			SetEmail("third-party-user" + service.LinuxDoConnectSyntheticEmailDomain).
			SetUsername("third-party-user").
			SetPasswordHash("random-placeholder-hash").
			SetSignupSource(source).
			SetBalance(0).
			SetConcurrency(1).
			SetRole(service.RoleUser).
			SetStatus(service.StatusActive).
			Save(context.Background())
		require.NoError(t, err)
		return int64(user.ID)
	}
	return authSvc, repo, verification, createUser
}

func TestAPIKeyPasswordFactorBecomesAvailableAfterARealEmailBind(t *testing.T) {
	ctx := context.Background()
	authSvc, _, verification, createUser := newEmailBindVerificationFixture(t)
	userID := createUser("linuxdo")

	// 绑定前：第三方注册 + 占位邮箱，密码是系统塞的随机口令——密码因子不可用，
	// 也没有任何其它因子，所以创建仍然放行（只能一次性显示）。
	before, err := verification.Factors(ctx, userID)
	require.NoError(t, err)
	require.False(t, before.PasswordAvailable)
	require.False(t, before.SealAvailable)
	require.False(t, before.Available())
	require.ErrorIs(t, verification.VerifyForSeal(ctx, userID, "new-password"), service.ErrAPIKeyFactorUnavailable)
	allowed, err := verification.VerifyForCreate(ctx, userID, nil)
	require.NoError(t, err)
	require.Empty(t, allowed.Factor)

	// 用户在资料页绑定邮箱并设置本地密码（BindEmailIdentity 必须提交新密码）。
	_, err = authSvc.BindEmailIdentity(ctx, userID, "bound@example.com", "123456", "new-password")
	require.NoError(t, err)

	// 绑定后：密码因子可用，可以「以后可查看」，创建要求校验，且认的是用户刚设的密码。
	after, err := verification.Factors(ctx, userID)
	require.NoError(t, err)
	require.True(t, after.PasswordAvailable)
	require.True(t, after.SealAvailable)
	require.Equal(t, []string{service.APIKeyFactorPassword}, after.AvailableMethods)
	require.Equal(t, service.APIKeyFactorPassword, after.Preferred)
	require.NoError(t, verification.VerifyForSeal(ctx, userID, "new-password"))
	require.ErrorIs(t, verification.VerifyForSeal(ctx, userID, "random-placeholder-hash"), service.ErrAPIKeyPasswordInvalid)
	_, err = verification.VerifyForCreate(ctx, userID, nil)
	require.ErrorIs(t, err, service.ErrAPIKeyVerificationRequired)
	verified, err := verification.VerifyForCreate(ctx, userID, &service.APIKeyVerificationInput{
		Method: service.APIKeyFactorPassword, Password: "new-password",
	})
	require.NoError(t, err)
	require.Equal(t, service.APIKeyFactorPassword, verified.Factor)
}

// 反面：第三方注册时用户从没设过密码（系统塞的随机口令）——仓库在创建账号时就会写一条
// 来源为 user_repo_create 的邮箱身份。它必须不算「设过本地密码」的证据，否则这类账号会被要求输入
// 一个根本不存在的口令，永远建不了 Key。
func TestAPIKeyPasswordFactorIsNotInferredFromCreationTimeIdentities(t *testing.T) {
	ctx := context.Background()
	_, repo, verification, _ := newEmailBindVerificationFixture(t)

	// OIDC 已验证邮箱注册：真实邮箱 + 系统塞的随机口令（仓库的 Create 只原样保留 linuxdo / wechat /
	// oidc / dingtalk 这几个来源；github / google 创建时先落成 email，之后由登录流程改写来源）。
	oauthUser := &service.User{
		Email:        "oidc-user@example.com",
		Username:     "oidc-user",
		PasswordHash: "random-placeholder-hash",
		Role:         service.RoleUser,
		Status:       service.StatusActive,
		SignupSource: "oidc",
	}
	require.NoError(t, repo.Create(ctx, oauthUser))

	// 前提：创建账号确实留下了一条邮箱身份，来源不是「绑定邮箱」。
	records, err := repo.ListUserAuthIdentities(ctx, oauthUser.ID)
	require.NoError(t, err)
	var sources []string
	for _, record := range records {
		if record.ProviderType == "email" {
			source, _ := record.Metadata["source"].(string)
			sources = append(sources, source)
		}
	}
	require.Equal(t, []string{"user_repo_create"}, sources)

	factors, err := verification.Factors(ctx, oauthUser.ID)
	require.NoError(t, err)
	require.False(t, factors.PasswordAvailable)
	require.False(t, factors.Available())
	result, err := verification.VerifyForCreate(ctx, oauthUser.ID, nil)
	require.NoError(t, err, "a third-party account with no usable password must still be able to create a key")
	require.Empty(t, result.Factor)
}
