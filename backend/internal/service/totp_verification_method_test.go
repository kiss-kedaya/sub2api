//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// totpVMUserRepoStub 仅实现 TOTP 验证方式测试所需方法；未桩方法调用即 panic（嵌入 nil 接口）。
type totpVMUserRepoStub struct {
	UserRepository
	user          *User
	totpDisabled  bool
	disableCalled bool
}

func (s *totpVMUserRepoStub) GetByID(ctx context.Context, id int64) (*User, error) {
	if s.user == nil {
		return nil, errors.New("user not found")
	}
	return s.user, nil
}

func (s *totpVMUserRepoStub) DisableTotp(ctx context.Context, userID int64) error {
	s.disableCalled = true
	s.totpDisabled = true
	return nil
}

func (s *totpVMUserRepoStub) DisableTotpWithExpectedRole(ctx context.Context, userID int64, expectedRole string, expectedSecret *string) error {
	if s.user.Role != expectedRole || s.user.Status != StatusActive {
		return ErrUserSecurityStateChanged
	}
	if (s.user.TotpSecretEncrypted == nil) != (expectedSecret == nil) || (expectedSecret != nil && *s.user.TotpSecretEncrypted != *expectedSecret) {
		return ErrUserSecurityStateChanged
	}
	return s.DisableTotp(ctx, userID)
}

type totpVMSettingRepoStub struct {
	SettingRepository
	values map[string]string
}

func (s *totpVMSettingRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	v, ok := s.values[key]
	if !ok {
		return "", errors.New("setting not found")
	}
	return v, nil
}

func newTotpVMService(t *testing.T, user *User, emailVerifyEnabled bool) (*TotpService, *totpVMUserRepoStub) {
	t.Helper()
	userRepo := &totpVMUserRepoStub{user: user}
	values := map[string]string{}
	if emailVerifyEnabled {
		values[SettingKeyEmailVerifyEnabled] = "true"
	}
	settingSvc := NewSettingService(&totpVMSettingRepoStub{values: values}, nil)
	return NewTotpService(userRepo, nil, nil, settingSvc, nil, nil), userRepo
}

func TestGetVerificationMethodAdminEnablePasswordDisableTotp(t *testing.T) {
	admin := &User{ID: 1, Email: "admin@example.com", Role: RoleAdmin}
	svc, _ := newTotpVMService(t, admin, true)

	method, err := svc.GetVerificationMethod(context.Background(), admin.ID)
	require.NoError(t, err)
	require.Equal(t, "password", method.Method)
	method, err = svc.GetVerificationMethod(context.Background(), admin.ID, "enable")
	require.NoError(t, err)
	require.Equal(t, "password", method.Method)
	method, err = svc.GetVerificationMethod(context.Background(), admin.ID, "disable")
	require.NoError(t, err)
	require.Equal(t, "totp", method.Method)
}

func TestGetVerificationMethodRegularUserFollowsEmailVerifySetting(t *testing.T) {
	user := &User{ID: 2, Email: "user@example.com", Role: RoleUser}

	svcEmailOn, _ := newTotpVMService(t, user, true)
	method, err := svcEmailOn.GetVerificationMethod(context.Background(), user.ID)
	require.NoError(t, err)
	require.Equal(t, "email", method.Method)

	svcEmailOff, _ := newTotpVMService(t, user, false)
	method, err = svcEmailOff.GetVerificationMethod(context.Background(), user.ID)
	require.NoError(t, err)
	require.Equal(t, "password", method.Method)
}

func TestTotpDisableAdminRejectsPasswordAndEmailWithoutTotp(t *testing.T) {
	admin := &User{ID: 1, Email: "admin@example.com", Role: RoleAdmin, TotpEnabled: true}
	require.NoError(t, admin.SetPassword("correct-password"))
	svc, userRepo := newTotpVMService(t, admin, true)

	// 登录密码与邮箱验证码均不能代替当前 TOTP。
	err := svc.Disable(context.Background(), admin.ID, "", "")
	require.ErrorIs(t, err, ErrTotpInvalidCode)

	// 密码错误 → 拒绝。
	err = svc.Disable(context.Background(), admin.ID, "", "wrong-password")
	require.ErrorIs(t, err, ErrTotpInvalidCode)

	// 密码正确也不能替代当前 TOTP；不会进入邮箱验证码分支。
	err = svc.Disable(context.Background(), admin.ID, "654321", "correct-password")
	require.ErrorIs(t, err, ErrTotpInvalidCode)
	require.False(t, userRepo.disableCalled)
}

func TestTotpDisableRegularUserStillRequiresEmailCode(t *testing.T) {
	user := &User{ID: 2, Email: "user@example.com", Role: RoleUser, TotpEnabled: true}
	require.NoError(t, user.SetPassword("whatever"))
	svc, _ := newTotpVMService(t, user, true)

	err := svc.Disable(context.Background(), user.ID, "", "whatever")
	require.ErrorIs(t, err, ErrVerifyCodeRequired)
}
