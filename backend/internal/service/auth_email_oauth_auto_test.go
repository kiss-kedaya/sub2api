//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func newEmailOAuthAutoAuthService(
	userRepo UserRepository,
	settings map[string]string,
	quotaRepo UserPlatformQuotaRepository,
) *AuthService {
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:                   "test-secret",
			ExpireHour:               1,
			AccessTokenExpireMinutes: 60,
			RefreshTokenExpireDays:   7,
		},
		Default: config.DefaultConfig{
			UserBalance:     3.5,
			UserConcurrency: 2,
		},
	}

	settingService := NewSettingService(&settingRepoStub{values: settings}, cfg)

	return NewAuthService(
		nil, // entClient — nil, updateUserSignupSource early return
		userRepo,
		nil, // redeemRepo — invitationCode="" 时不触发
		&refreshTokenCacheStub{},
		cfg,
		settingService,
		nil, // emailService
		nil, // turnstileService
		nil, // emailQueueService
		nil, // promoService
		nil, // defaultSubAssigner — nil, assignSubscriptions early return
		nil, // affiliateService — nil, bindOAuthAffiliate early return
		quotaRepo,
	)
}

func TestEmailOAuthAuto_SnapshotsPlatformQuotaDefaults(t *testing.T) {
	userRepo := &userRepoStub{nextID: 88}
	quotaRepo := &userPlatformQuotaRepoStub{}

	svc := newEmailOAuthAutoAuthService(
		userRepo,
		map[string]string{
			SettingKeyRegistrationEnabled:   "true",
			SettingKeyDefaultPlatformQuotas: `{"gemini": {"monthly": 100.0}}`,
		},
		quotaRepo,
	)

	user, err := svc.createEmailOAuthUser(
		context.Background(),
		"newoauth@example.com",
		"newoauth",
		"github",
		"", // invitationCode
		"", // affiliateCode
	)
	require.NoError(t, err)
	require.NotNil(t, user)
	require.Equal(t, int64(88), user.ID)

	require.Len(t, quotaRepo.bulkInsertCalls, 1, "createEmailOAuthUser must snapshot platform quotas via BulkInsertInitial")

	records := quotaRepo.bulkInsertCalls[0]
	var geminiRecord *UserPlatformQuotaRecord
	for i := range records {
		if records[i].Platform == "gemini" {
			geminiRecord = &records[i]
			break
		}
	}
	require.NotNil(t, geminiRecord, "expected gemini platform record")
	require.NotNil(t, geminiRecord.MonthlyLimitUSD)
	require.InDelta(t, 100.0, *geminiRecord.MonthlyLimitUSD, 0.0001)
}

// 第三方登录（OAuth/passkey）不得绕过已启用账号的登录二次验证：
// 该判定必须与密码登录同口径（覆盖 TOTP / Passkey / 双因子），且功能总开关关闭/账号未开 2FA 时不能误伤正常登录。
func TestLoginRequiresTOTPSharedAcrossLoginEntrypoints(t *testing.T) {
	ctx := context.Background()

	t.Run("totp开且账号已启用：必须挑战", func(t *testing.T) {
		svc := newEmailOAuthAutoAuthService(
			&userRepoStub{nextID: 1},
			map[string]string{SettingKeyTotpEnabled: "true"},
			nil,
		)
		factors, err := svc.loginRequiredFactors(ctx, &User{ID: 7, TotpEnabled: true})
		require.NoError(t, err)
		require.Equal(t, []Factor{FactorTOTP}, factors)
	})

	t.Run("账号未启用TOTP：不误伤", func(t *testing.T) {
		svc := newEmailOAuthAutoAuthService(
			&userRepoStub{nextID: 1},
			map[string]string{SettingKeyTotpEnabled: "true"},
			nil,
		)
		factors, err := svc.loginRequiredFactors(ctx, &User{ID: 7, TotpEnabled: false})
		require.NoError(t, err)
		require.Empty(t, factors)
	})

	t.Run("功能总开关关闭：不误伤", func(t *testing.T) {
		svc := newEmailOAuthAutoAuthService(
			&userRepoStub{nextID: 1},
			map[string]string{SettingKeyTotpEnabled: "false"},
			nil,
		)
		factors, err := svc.loginRequiredFactors(ctx, &User{ID: 7, TotpEnabled: true})
		require.NoError(t, err)
		require.Empty(t, factors)
	})

	t.Run("nil用户：fail-closed 返回错误", func(t *testing.T) {
		svc := newEmailOAuthAutoAuthService(&userRepoStub{nextID: 1}, map[string]string{SettingKeyTotpEnabled: "true"}, nil)
		factors, err := svc.loginRequiredFactors(ctx, nil)
		require.ErrorIs(t, err, ErrTwoFactorPolicyUnavailable)
		require.Empty(t, factors)
	})

	t.Run("设置读取失败：fail-closed 仍要求挑战", func(t *testing.T) {
		repo := &settingRepoStub{values: map[string]string{}, err: errors.New("settings db down")}
		svc := NewSettingService(repo, &config.Config{})
		require.True(t, svc.LoginRequiresTOTP(ctx, &User{ID: 7, TotpEnabled: true}))
	})
}
