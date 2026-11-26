//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// factorPolicyUserRepo 只实现策略判定用到的 GetByID。
type factorPolicyUserRepo struct {
	UserRepository
	user *User
	err  error
}

func (r *factorPolicyUserRepo) GetByID(context.Context, int64) (*User, error) {
	return r.user, r.err
}

// factorPolicyPasskeyRepo 只实现判定绑定状态用到的 ListByUserID。
type factorPolicyPasskeyRepo struct {
	PasskeyRepository
	records []PasskeyCredentialRecord
	err     error
}

func (r *factorPolicyPasskeyRepo) ListByUserID(context.Context, int64) ([]PasskeyCredentialRecord, error) {
	return r.records, r.err
}

// factorPolicySettingRepo 只实现 GetValue（全局 TOTP 登录开关）。
type factorPolicySettingRepo struct {
	SettingRepository
	values map[string]string
	err    error
}

func (r *factorPolicySettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}

func newFactorPolicyService(
	user *User,
	userErr error,
	records []PasskeyCredentialRecord,
	passkeyErr error,
	settings map[string]string,
) *TotpService {
	repo := &factorPolicyUserRepo{user: user, err: userErr}
	var passkeys PasskeyRepository
	if passkeyErr != nil || records != nil {
		passkeys = &factorPolicyPasskeyRepo{records: records, err: passkeyErr}
	}
	settingSvc := NewSettingService(&factorPolicySettingRepo{values: settings}, nil)
	svc := NewTotpService(repo, nil, newTwoFactorGrantTestCache(), settingSvc, nil, nil)
	if passkeys != nil {
		svc.SetPasskeyRepository(passkeys)
	}
	return svc
}

// 因子矩阵：只绑 TOTP / 只绑 Passkey / 两者都绑 / 都没有。
func TestRequiredFactorsMatrix(t *testing.T) {
	ctx := context.Background()

	t.Run("none", func(t *testing.T) {
		svc := newFactorPolicyService(&User{ID: 1}, nil, nil, nil, map[string]string{SettingKeyTotpEnabled: "true"})
		factors, err := svc.RequiredFactors(ctx, 1)
		require.NoError(t, err)
		require.Empty(t, factors)
	})

	t.Run("totp only", func(t *testing.T) {
		svc := newFactorPolicyService(&User{ID: 1, TotpEnabled: true}, nil, nil, nil, nil)
		factors, err := svc.RequiredFactors(ctx, 1)
		require.NoError(t, err)
		require.Equal(t, []Factor{FactorTOTP}, factors)
	})

	t.Run("passkey only", func(t *testing.T) {
		svc := newFactorPolicyService(&User{ID: 1}, nil, []PasskeyCredentialRecord{{ID: 9, UserID: 1}}, nil, nil)
		factors, err := svc.RequiredFactors(ctx, 1)
		require.NoError(t, err)
		require.Equal(t, []Factor{FactorPasskey}, factors)
	})

	t.Run("both keeps totp first", func(t *testing.T) {
		svc := newFactorPolicyService(&User{ID: 1, TotpEnabled: true}, nil, []PasskeyCredentialRecord{{ID: 9, UserID: 1}}, nil, nil)
		factors, err := svc.RequiredFactors(ctx, 1)
		require.NoError(t, err)
		require.Equal(t, []Factor{FactorTOTP, FactorPasskey}, factors)
	})

	// fail-closed：用户 / 凭据存储故障时返回错误，调用方必须拒绝放行。
	t.Run("user repo error", func(t *testing.T) {
		svc := newFactorPolicyService(nil, errors.New("db down"), nil, nil, nil)
		factors, err := svc.RequiredFactors(ctx, 1)
		require.Error(t, err)
		require.Nil(t, factors)
	})

	t.Run("passkey repo error", func(t *testing.T) {
		svc := newFactorPolicyService(&User{ID: 1, TotpEnabled: true}, nil, nil, errors.New("db down"), nil)
		factors, err := svc.RequiredFactors(ctx, 1)
		require.Error(t, err)
		require.Nil(t, factors)
	})
}

// 登录因子策略：只绑 TOTP 时沿用全局 TOTP 开关语义（现状不变）；
// Passkey 始终按绑定判定。
func TestLoginRequiredFactors(t *testing.T) {
	ctx := context.Background()
	both := &User{ID: 1, TotpEnabled: true}
	records := []PasskeyCredentialRecord{{ID: 9, UserID: 1}}

	t.Run("totp global enabled keeps totp", func(t *testing.T) {
		svc := newFactorPolicyService(both, nil, records, nil, map[string]string{SettingKeyTotpEnabled: "true"})
		factors, err := svc.LoginRequiredFactors(ctx, both)
		require.NoError(t, err)
		require.Equal(t, []Factor{FactorTOTP, FactorPasskey}, factors)
	})

	t.Run("totp global disabled drops totp for login", func(t *testing.T) {
		svc := newFactorPolicyService(both, nil, records, nil, map[string]string{SettingKeyTotpEnabled: "false"})
		factors, err := svc.LoginRequiredFactors(ctx, both)
		require.NoError(t, err)
		require.Equal(t, []Factor{FactorPasskey}, factors)
	})

	t.Run("setting read error keeps totp fail closed", func(t *testing.T) {
		settingSvc := NewSettingService(&factorPolicySettingRepo{err: errors.New("settings down")}, nil)
		svc := NewTotpService(&factorPolicyUserRepo{user: both}, nil, newTwoFactorGrantTestCache(), settingSvc, nil, nil)
		svc.SetPasskeyRepository(&factorPolicyPasskeyRepo{records: records})
		factors, err := svc.LoginRequiredFactors(ctx, both)
		require.NoError(t, err)
		require.Contains(t, factors, FactorTOTP)
	})

	t.Run("passkey only ignores totp global flag", func(t *testing.T) {
		user := &User{ID: 2, TotpEnabled: false}
		svc := newFactorPolicyService(user, nil, records, nil, map[string]string{SettingKeyTotpEnabled: "false"})
		factors, err := svc.LoginRequiredFactors(ctx, user)
		require.NoError(t, err)
		require.Equal(t, []Factor{FactorPasskey}, factors)
	})
}

func TestFactorHelpers(t *testing.T) {
	require.True(t, FactorTOTP.Valid())
	require.True(t, FactorPasskey.Valid())
	require.False(t, Factor("sms").Valid())

	parsed, ok := FactorFromString("passkey")
	require.True(t, ok)
	require.Equal(t, FactorPasskey, parsed)
	_, ok = FactorFromString("sms")
	require.False(t, ok)

	require.Equal(t, []string{"totp", "passkey"}, FactorStrings([]Factor{FactorTOTP, FactorPasskey}))
	require.Equal(t, []string{}, FactorStrings(nil))

	list, err := FactorList([]string{"passkey", "totp"})
	require.NoError(t, err)
	require.Equal(t, []Factor{FactorPasskey, FactorTOTP}, list)
	_, err = FactorList([]string{"sms"})
	require.Error(t, err)

	require.True(t, HasFactor([]Factor{FactorTOTP, FactorPasskey}, FactorPasskey))
	require.False(t, HasFactor([]Factor{FactorTOTP}, FactorPasskey))
	require.Equal(t, []Factor{FactorPasskey}, MissingFactors([]Factor{FactorTOTP, FactorPasskey}, []Factor{FactorTOTP}))
	require.Nil(t, MissingFactors([]Factor{FactorTOTP}, []Factor{FactorTOTP}))
	// 固定挑战顺序：即使输入乱序也先 TOTP 后 Passkey。
	require.Equal(t, []Factor{FactorTOTP, FactorPasskey}, sortFactorsForChallenge([]Factor{FactorPasskey, FactorTOTP}))
}

// AllFactors 是 Valid 的唯一数据源：break-glass 重置 2FA 按它枚举 step-up 授权键，
// 清单漏一个因子就会留下撤销不掉的授权。
func TestAllFactorsIsTheSingleSourceOfValidFactors(t *testing.T) {
	require.Equal(t, []Factor{FactorTOTP, FactorPasskey}, AllFactors())
	for _, factor := range AllFactors() {
		require.True(t, factor.Valid(), "factor=%s", factor)
	}
	require.False(t, Factor("sms").Valid())
	require.False(t, Factor("").Valid())

	// 返回的是副本：调用方改动不能污染全局清单。
	mutated := AllFactors()
	mutated[0] = Factor("sms")
	require.Equal(t, []Factor{FactorTOTP, FactorPasskey}, AllFactors())
	require.True(t, FactorTOTP.Valid())
}

// step-up 的唯一满足规则（任选其一）：已绑定因子里至少一个持有有效凭证即满足；
// 未绑定因子的凭证、未知因子名、空集合都不算。
func TestStepUpSatisfiedFactors(t *testing.T) {
	both := []Factor{FactorTOTP, FactorPasskey}
	cases := []struct {
		name     string
		bound    []Factor
		verified []Factor
		want     []Factor
	}{
		{"both bound / totp verified", both, []Factor{FactorTOTP}, []Factor{FactorTOTP}},
		{"both bound / passkey verified", both, []Factor{FactorPasskey}, []Factor{FactorPasskey}},
		{"both bound / both verified", both, both, both},
		{"both bound / nothing verified", both, nil, nil},
		{"both bound / empty verified set", both, []Factor{}, nil},
		{"totp only / totp verified", []Factor{FactorTOTP}, []Factor{FactorTOTP}, []Factor{FactorTOTP}},
		{"totp only / only the unbound passkey verified", []Factor{FactorTOTP}, []Factor{FactorPasskey}, nil},
		{"passkey only / passkey verified", []Factor{FactorPasskey}, []Factor{FactorPasskey}, []Factor{FactorPasskey}},
		{"passkey only / only the unbound totp verified", []Factor{FactorPasskey}, []Factor{FactorTOTP}, nil},
		{"no factor bound / both verified (the empty set is never satisfied)", nil, both, nil},
		{"no factor bound / nothing verified", nil, nil, nil},
		{"both bound / unknown factor verified", both, []Factor{Factor("sms")}, nil},
		{"unknown factor bound and verified", []Factor{Factor("sms")}, []Factor{Factor("sms")}, nil},
		{"result follows the bound order, not the verified order", both, []Factor{FactorPasskey, FactorTOTP}, both},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, StepUpSatisfiedFactors(tc.bound, tc.verified))
		})
	}
}

// 提示客户端「任选其一」的挑战 metadata。
func TestStepUpChallengeMetadata(t *testing.T) {
	require.Equal(t, map[string]string{"step_up_mode": "any", "factors": "totp,passkey"},
		StepUpChallengeMetadata([]Factor{FactorTOTP, FactorPasskey}))
	require.Equal(t, map[string]string{"step_up_mode": "any", "factors": "passkey"},
		StepUpChallengeMetadata([]Factor{FactorPasskey}))
	require.Equal(t, StepUpModeAny, StepUpChallengeMetadata(nil)["step_up_mode"])
}

// 任选其一只属于 step-up：同一个「两者都绑定」的账号，登录仍要求全部因子，
// 只验证了其中一个时登录没有完成，而 step-up 已经满足。
func TestStepUpAnyOneRuleDoesNotRelaxLogin(t *testing.T) {
	ctx := context.Background()
	both := &User{ID: 1, TotpEnabled: true}
	records := []PasskeyCredentialRecord{{ID: 9, UserID: 1}}
	svc := newFactorPolicyService(both, nil, records, nil, map[string]string{SettingKeyTotpEnabled: "true"})

	login, err := svc.LoginRequiredFactors(ctx, both)
	require.NoError(t, err)
	require.Equal(t, []Factor{FactorTOTP, FactorPasskey}, login)

	bound, err := svc.RequiredFactors(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []Factor{FactorTOTP, FactorPasskey}, bound, "required_factors 仍然是全部已绑定因子")

	for _, only := range []Factor{FactorTOTP, FactorPasskey} {
		verified := []Factor{only}
		require.NotEmpty(t, MissingFactors(login, verified), "登录只验证了 %s：还缺另一个因子，不得签发令牌", only)
		require.Equal(t, verified, StepUpSatisfiedFactors(bound, verified), "step-up 验证其一即满足")
	}
}
