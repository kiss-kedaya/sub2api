//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// 审计 A-P2-03：封存 PUT 的「旧客户端兼容」分支（既没带 password 也没带 auth_factor）原先完全不校验。
// 账号存在可用因子时它必须被拒；没有任何可用因子的账号保持放行。

func TestAPIKeyVerifyForLegacySealMatrix(t *testing.T) {
	ctx := context.Background()
	bound := emailIdentityFrom("auth_service_email_bind")
	cases := []struct {
		name       string
		signup     string
		totp       bool
		identities []UserAuthIdentityRecord
		wantErr    error // nil = 放行
	}{
		{name: "email signup (login password factor)", signup: "email", wantErr: ErrAPIKeyVerificationRequired},
		{name: "third party with TOTP only", signup: "github", totp: true, wantErr: ErrAPIKeyVerificationRequired},
		{name: "third party that set a local password", signup: "linuxdo", identities: []UserAuthIdentityRecord{bound}, wantErr: ErrAPIKeyVerificationRequired},
		{name: "third party without any factor stays allowed", signup: "github"},
		{name: "third party with only creation-time identities stays allowed", signup: "github",
			identities: []UserAuthIdentityRecord{emailIdentityFrom("user_repo_create")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			user := apiKeyVerifyTestUser(t, "chosen-password")
			user.SignupSource = tc.signup
			secret := ""
			if tc.totp {
				secret = "JBSWY3DPEHPK3PXP"
			}
			svc, _, repo := newAPIKeyVerificationTestService(t, user, nil, nil, secret)
			repo.identities = tc.identities

			err := svc.VerifyForLegacySeal(ctx, user.ID)
			if tc.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestAPIKeyVerifyForLegacySealFailsClosed(t *testing.T) {
	ctx := context.Background()

	// 查不到账号 / 证据：不能被当成「没有因子，放行」。
	user := apiKeyVerifyTestUser(t, "chosen-password")
	user.SignupSource = "github"
	svc, _, repo := newAPIKeyVerificationTestService(t, user, nil, nil, "")
	repo.identitiesErr = errors.New("database is unavailable")
	err := svc.VerifyForLegacySeal(ctx, user.ID)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrAPIKeyVerificationRequired)

	repo.identitiesErr = nil
	repo.err = errors.New("database is unavailable")
	require.Error(t, svc.VerifyForLegacySeal(ctx, user.ID))

	// 服务没装配 / 用户 ID 非法：同样拒绝。
	var nilSvc *APIKeyVerificationService
	require.ErrorIs(t, nilSvc.VerifyForLegacySeal(ctx, 7), ErrAPIKeyVerificationUnavailable)
	require.ErrorIs(t, svc.VerifyForLegacySeal(ctx, 0), ErrAPIKeyVerificationUnavailable)
}
