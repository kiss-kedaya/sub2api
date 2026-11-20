//go:build unit

package service_test

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// GitHub/Google/OIDC 已验证邮箱的 OAuth 注册也必须遵守注册邮箱策略。
// 旧实现只查精确黑名单，registration_email_alias_block_enabled 对 OAuth 注册无效。
func TestVerifiedEmailOAuthSignupRejectsBlockedAliases(t *testing.T) {
	svc, _, client := newAuthServiceForEmailBindWithRefreshCache(t, map[string]string{
		service.SettingKeyRegistrationEnabled:                "true",
		service.SettingKeyRegistrationEmailAliasBlockEnabled: "true",
	}, nil, nil, newEmailBindRefreshTokenCacheStub())
	ctx := context.Background()

	_, _, err := svc.LoginOrRegisterVerifiedEmailOAuth(ctx, service.EmailOAuthIdentityInput{
		ProviderType:    "github",
		ProviderSubject: "gh-alias-blocked",
		Email:           "first.last@gmail.com",
		EmailVerified:   true,
		Username:        "alias",
	})
	require.ErrorIs(t, err, service.ErrEmailAliasNotAllowed)

	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count, "被别名策略拒绝的 OAuth 注册不得落库")
}

// 钉钉/OIDC 首次登录直登注册走 LoginOrRegisterOAuthWithTokenPair：
// 真实邮箱同样必须经过注册邮箱策略；合成邮箱（渠道无真实邮箱）不参与该策略。
func TestOAuthDirectRegistrationHonorsRegistrationEmailPolicy(t *testing.T) {
	svc, _, client := newAuthServiceForEmailBindWithRefreshCache(t, map[string]string{
		service.SettingKeyRegistrationEnabled:                "true",
		service.SettingKeyRegistrationEmailAliasBlockEnabled: "true",
	}, nil, nil, newEmailBindRefreshTokenCacheStub())
	ctx := context.Background()

	_, _, err := svc.LoginOrRegisterOAuthWithTokenPair(ctx,
		"first.last@gmail.com", "alias", "", "", "dingtalk")
	require.ErrorIs(t, err, service.ErrEmailAliasNotAllowed)

	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count, "被别名策略拒绝的直登注册不得落库")

	// 不误伤：普通邮箱仍可正常注册并签出会话。
	pair, user, err := svc.LoginOrRegisterOAuthWithTokenPair(ctx,
		"normal.user@example.com", "normal", "", "", "dingtalk")
	require.NoError(t, err)
	require.NotNil(t, pair)
	require.NotEmpty(t, pair.AccessToken)
	require.NotEmpty(t, pair.RefreshToken)
	require.Equal(t, "normal.user@example.com", user.Email)
}
