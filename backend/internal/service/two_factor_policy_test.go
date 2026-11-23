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
