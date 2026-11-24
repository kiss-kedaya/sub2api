//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// 审计 F16：「是否知道登录密码」原先只看注册来源——第三方登录注册的账号一律视为「密码不可用」，
// 哪怕用户之后在资料页绑定邮箱时亲手设了本地密码，也会永远失去密码因子与「以后可查看」。
// 现在按「是否真的设置过本地密码」判断：仓库里没有显式标记，唯一可靠的持久化证据是
// 绑定邮箱留下的 source=auth_service_email_bind 邮箱身份。

func emailIdentityFrom(source string) UserAuthIdentityRecord {
	return UserAuthIdentityRecord{
		ProviderType: "email",
		ProviderKey:  "email",
		Metadata:     map[string]any{"source": source},
	}
}

func TestAPIKeyPasswordKnownByUserMatrix(t *testing.T) {
	bound := emailIdentityFrom("auth_service_email_bind")
	cases := []struct {
		name        string
		signup      string
		emptyHash   bool
		identities  []UserAuthIdentityRecord
		want        bool
		wantLookups int
	}{
		// 邮箱注册（含历史账号的空来源）：知道，而且不额外查库。
		{name: "email signup", signup: "email", want: true},
		{name: "legacy empty signup source", signup: "", want: true},
		{name: "no password hash at all", signup: "email", emptyHash: true, want: false},
		{name: "oauth signup without a hash", signup: "github", emptyHash: true, identities: []UserAuthIdentityRecord{bound}, want: false},

		// 第三方登录注册：默认不知道（随机口令）。
		{name: "github without any identity", signup: "github", want: false, wantLookups: 1},
		{name: "google without any identity", signup: "google", want: false, wantLookups: 1},
		{name: "oidc without any identity", signup: "oidc", want: false, wantLookups: 1},
		{name: "linuxdo without any identity", signup: "linuxdo", want: false, wantLookups: 1},
		{name: "wechat without any identity", signup: "wechat", want: false, wantLookups: 1},
		{name: "dingtalk without any identity", signup: "dingtalk", want: false, wantLookups: 1},
		{name: "oauth alias source", signup: "oauth", want: false, wantLookups: 1},

		// 之后绑定邮箱并设置了本地密码：知道，各个第三方来源一视同仁，来源写法大小写 / 空白不敏感。
		{name: "github + email bind", signup: "github", identities: []UserAuthIdentityRecord{bound}, want: true, wantLookups: 1},
		{name: "google + email bind", signup: "google", identities: []UserAuthIdentityRecord{bound}, want: true, wantLookups: 1},
		{name: "oidc + email bind", signup: "oidc", identities: []UserAuthIdentityRecord{bound}, want: true, wantLookups: 1},
		{name: "linuxdo + email bind", signup: "linuxdo", identities: []UserAuthIdentityRecord{bound}, want: true, wantLookups: 1},
		{name: "wechat + email bind", signup: "wechat", identities: []UserAuthIdentityRecord{bound}, want: true, wantLookups: 1},
		{name: "dingtalk + email bind", signup: "dingtalk", identities: []UserAuthIdentityRecord{bound}, want: true, wantLookups: 1},
		{name: "source is trimmed and case-insensitive", signup: "  GitHub ", identities: []UserAuthIdentityRecord{bound}, want: true, wantLookups: 1},
		{name: "bind among other identities", signup: "github", want: true, wantLookups: 1, identities: []UserAuthIdentityRecord{
			emailIdentityFrom("user_repo_create"), {ProviderType: "github", ProviderKey: "github"}, bound,
		}},

		// 这些来源是任何用户创建 / 登录时都会写的，不能当「设过本地密码」的证据：
		// 否则只有随机口令的第三方账号会被要求输入一个根本不存在的口令。
		{name: "only user_repo_create", signup: "github", identities: []UserAuthIdentityRecord{emailIdentityFrom("user_repo_create")}, want: false, wantLookups: 1},
		{name: "only user_repo_update", signup: "github", identities: []UserAuthIdentityRecord{emailIdentityFrom("user_repo_update")}, want: false, wantLookups: 1},
		{name: "only auth_service_login_backfill", signup: "github", identities: []UserAuthIdentityRecord{emailIdentityFrom("auth_service_login_backfill")}, want: false, wantLookups: 1},
		{name: "only auth_service_dual_write", signup: "github", identities: []UserAuthIdentityRecord{emailIdentityFrom("auth_service_dual_write")}, want: false, wantLookups: 1},
		{name: "empty source", signup: "github", identities: []UserAuthIdentityRecord{emailIdentityFrom("")}, want: false, wantLookups: 1},
		{name: "no metadata", signup: "github", identities: []UserAuthIdentityRecord{{ProviderType: "email", ProviderKey: "email"}}, want: false, wantLookups: 1},
		// 证据必须是「邮箱」身份：别的提供方身份里写了同样的 source 也不算。
		{name: "bind source on a non-email identity", signup: "github", identities: []UserAuthIdentityRecord{{
			ProviderType: "github", ProviderKey: "github", Metadata: map[string]any{"source": "auth_service_email_bind"},
		}}, want: false, wantLookups: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user := apiKeyVerifyTestUser(t, "chosen-password")
			user.SignupSource = tc.signup
			if tc.emptyHash {
				user.PasswordHash = ""
			}
			repo := &apiKeyVerifyUserRepoStub{user: user, identities: tc.identities}
			svc := NewAPIKeyVerificationService(repo, nil, nil, nil, nil, nil)

			known, err := svc.passwordKnownByUser(context.Background(), user)
			require.NoError(t, err)
			require.Equal(t, tc.want, known)
			require.Equal(t, tc.wantLookups, repo.identityLookups, "identities must only be looked up for third-party signups with a hash")
		})
	}
}

func TestAPIKeyFactorsForAThirdPartyAccountThatSetALocalPassword(t *testing.T) {
	ctx := context.Background()
	user := apiKeyVerifyTestUser(t, "chosen-password")
	user.SignupSource = "linuxdo"
	svc, _, repo := newAPIKeyVerificationTestService(t, user, apiKeyVerifyTestKey(nil), apiKeyVerifyTestSecret(""), "JBSWY3DPEHPK3PXP")
	repo.identities = []UserAuthIdentityRecord{emailIdentityFrom("auth_service_email_bind")}

	factors, err := svc.Factors(ctx, user.ID)
	require.NoError(t, err)
	require.True(t, factors.PasswordAvailable)
	require.True(t, factors.SealAvailable, "a user who really set a local password can seal for later viewing")
	require.Equal(t, []string{APIKeyFactorTOTP, APIKeyFactorPassword}, factors.AvailableMethods)

	// 密码因子真的可用：创建 / 封存 / 二次查看授权都认这个密码，错的不行。
	_, err = svc.VerifyForCreate(ctx, user.ID, nil)
	require.ErrorIs(t, err, ErrAPIKeyVerificationRequired)
	result, err := svc.VerifyForCreate(ctx, user.ID, &APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "chosen-password"})
	require.NoError(t, err)
	require.Equal(t, APIKeyFactorPassword, result.Factor)
	_, err = svc.VerifyForCreate(ctx, user.ID, &APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "wrong-password"})
	require.ErrorIs(t, err, ErrAPIKeyPasswordInvalid)
	require.NoError(t, svc.VerifyForSeal(ctx, user.ID, "chosen-password"))
	require.ErrorIs(t, svc.VerifyForSeal(ctx, user.ID, "wrong-password"), ErrAPIKeyPasswordInvalid)
	grant, err := svc.AuthorizeReveal(ctx, user.ID, 42, APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "chosen-password"})
	require.NoError(t, err)
	require.Equal(t, APIKeyFactorPassword, grant.Factor)
}

// 「账号没有任何可用因子则放行」的语义不能变：第三方注册、没设过本地密码、没开 2FA 的账号，
// 即使身份表里躺着创建 / 登录时写的邮箱身份，也仍然是「没有因子」。
func TestAPIKeyThirdPartyAccountWithoutALocalPasswordKeepsTheNoFactorSemantics(t *testing.T) {
	ctx := context.Background()
	user := apiKeyVerifyTestUser(t, "random-generated-secret")
	user.SignupSource = "github"
	svc, _, repo := newAPIKeyVerificationTestService(t, user, nil, nil, "")
	repo.identities = []UserAuthIdentityRecord{
		emailIdentityFrom("user_repo_create"),
		emailIdentityFrom("auth_service_login_backfill"),
	}

	factors, err := svc.Factors(ctx, user.ID)
	require.NoError(t, err)
	require.False(t, factors.PasswordAvailable)
	require.False(t, factors.SealAvailable)
	require.False(t, factors.Available())

	result, err := svc.VerifyForCreate(ctx, user.ID, nil)
	require.NoError(t, err, "no available factor: creation is allowed (one-time display)")
	require.Empty(t, result.Factor)
	_, err = svc.VerifyForCreate(ctx, user.ID, &APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "random-generated-secret"})
	require.ErrorIs(t, err, ErrAPIKeyFactorUnavailable)
	require.ErrorIs(t, svc.VerifyForSeal(ctx, user.ID, "random-generated-secret"), ErrAPIKeyFactorUnavailable)
}

// 查不到证据时必须 fail-closed：不能退回「没有密码因子」，否则一个设过密码、又没有其它因子的账号
// 会在数据库抖动时被判成「没有任何可用因子」而放行创建。
func TestAPIKeyFactorsFailClosedWhenTheIdentityLookupFails(t *testing.T) {
	ctx := context.Background()
	user := apiKeyVerifyTestUser(t, "chosen-password")
	user.SignupSource = "github"
	// 给足 Key 与密文：保证下面每个入口失败的原因只可能是「查不到证据」。
	svc, _, repo := newAPIKeyVerificationTestService(t, user, apiKeyVerifyTestKey(nil), apiKeyVerifyTestSecret(""), "")
	repo.identitiesErr = errors.New("database is unavailable")

	_, err := svc.Factors(ctx, user.ID)
	require.Error(t, err)
	result, err := svc.VerifyForCreate(ctx, user.ID, nil)
	require.Error(t, err, "a failed lookup must never be read as 'no factor, allow'")
	require.Nil(t, result)
	_, err = svc.VerifyForCreate(ctx, user.ID, &APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "chosen-password"})
	require.Error(t, err)
	require.Error(t, svc.VerifyForSeal(ctx, user.ID, "chosen-password"))
	_, err = svc.AuthorizeReveal(ctx, user.ID, 42, APIKeyVerificationInput{Method: APIKeyFactorPassword, Password: "chosen-password"})
	require.Error(t, err)

	// 邮箱注册的账号根本不查证据：数据库抖动不影响它们。
	emailUser := apiKeyVerifyTestUser(t, "chosen-password")
	svc, _, repo = newAPIKeyVerificationTestService(t, emailUser, nil, nil, "")
	repo.identitiesErr = errors.New("database is unavailable")
	_, err = svc.Factors(ctx, emailUser.ID)
	require.NoError(t, err)
}
