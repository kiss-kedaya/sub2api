//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
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

func TestGetVerificationMethodAdminAlwaysPassword(t *testing.T) {
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
	method, err = svcEmailOn.GetVerificationMethod(context.Background(), user.ID, "disable")
	require.NoError(t, err)
	require.Equal(t, "totp", method.Method)

	svcEmailOff, _ := newTotpVMService(t, user, false)
	method, err = svcEmailOff.GetVerificationMethod(context.Background(), user.ID)
	require.NoError(t, err)
	require.Equal(t, "password", method.Method)
	method, err = svcEmailOff.GetVerificationMethod(context.Background(), user.ID, "disable")
	require.NoError(t, err)
	require.Equal(t, "totp", method.Method)
}

func TestTotpDisableAdminRequiresCurrentCodeAndNotPassword(t *testing.T) {
	admin := &User{ID: 1, Email: "admin@example.com", Role: RoleAdmin, TotpEnabled: true}
	require.NoError(t, admin.SetPassword("correct-password"))
	svc, userRepo := newTotpVMService(t, admin, true)

	// A password alone cannot remove the administrator's second factor.
	err := svc.Disable(context.Background(), admin.ID, "", "correct-password")
	require.ErrorIs(t, err, ErrCryptoTotpRequired)
	require.False(t, userRepo.disableCalled)

	// The OTP is mandatory; once supplied, the admin path does not request a password.
	err = svc.Disable(context.Background(), admin.ID, "", "", "123456")
	require.ErrorIs(t, err, ErrCryptoTotpUnavailable)
	require.False(t, userRepo.disableCalled)
}

func TestTotpDisableAdminCannotRemoveUnboundTwoFactor(t *testing.T) {
	admin := &User{ID: 1, Email: "admin@example.com", Role: RoleAdmin}
	svc, userRepo := newTotpVMService(t, admin, false)
	require.ErrorIs(t, svc.Disable(context.Background(), admin.ID, "", "password", "123456"), ErrCryptoTotpNotSetup)
	require.False(t, userRepo.disableCalled)
}

func TestTotpDisableAdminAcceptsOnlyCurrentCode(t *testing.T) {
	const secret = "JBSWY3DPEHPK3PXP"
	encrypted := "encrypted:" + secret
	admin := &User{ID: 1, Email: "admin@example.com", Role: RoleAdmin, Status: StatusActive, TotpEnabled: true, TotpSecretEncrypted: &encrypted}
	repo := &totpVMUserRepoStub{user: admin}
	cache := &totpDisableTestCache{}
	settings := NewSettingService(&totpVMSettingRepoStub{values: map[string]string{SettingKeyTotpEnabled: "true"}}, nil)
	svc := NewTotpService(repo, totpDisableTestEncryptor{}, cache, settings, nil, nil)

	require.ErrorIs(t, svc.Disable(context.Background(), admin.ID, "", "correct-password", "000000"), ErrCryptoTotpRequired)
	require.False(t, repo.disableCalled)

	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	require.NoError(t, svc.Disable(context.Background(), admin.ID, "", "", code))
	require.True(t, repo.disableCalled)
}

type totpDisableTestCache struct{ TotpCache }

func (*totpDisableTestCache) GetVerifyAttempts(context.Context, int64) (int, error) { return 0, nil }
func (*totpDisableTestCache) IncrementVerifyAttempts(context.Context, int64) (int, error) {
	return 1, nil
}
func (*totpDisableTestCache) ClearVerifyAttempts(context.Context, int64) error { return nil }

type totpDisableTestEncryptor struct{}

func (totpDisableTestEncryptor) Encrypt(value string) (string, error) {
	return "encrypted:" + value, nil
}
func (totpDisableTestEncryptor) Decrypt(value string) (string, error) {
	return strings.TrimPrefix(value, "encrypted:"), nil
}

func TestTotpDisableRegularUserRequiresCurrentCodeInsteadOfIdentityCredentials(t *testing.T) {
	for _, emailEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("email_enabled_%t", emailEnabled), func(t *testing.T) {
			const secret = "JBSWY3DPEHPK3PXP"
			encrypted := "encrypted:" + secret
			user := &User{ID: 2, Email: "user@example.com", Role: RoleUser, Status: StatusActive, TotpEnabled: true, TotpSecretEncrypted: &encrypted}
			require.NoError(t, user.SetPassword("correct-password"))
			svc, repo := newTotpVMService(t, user, emailEnabled)
			svc.encryptor, svc.cache = totpDisableTestEncryptor{}, &totpDisableTestCache{}
			for _, credentials := range [][2]string{{"", "correct-password"}, {"123456", ""}, {"123456", "correct-password"}} {
				require.ErrorIs(t, svc.Disable(t.Context(), user.ID, credentials[0], credentials[1]), ErrTotpInvalidCode)
				require.False(t, repo.disableCalled)
			}
			require.ErrorIs(t, svc.Disable(t.Context(), user.ID, "123456", "correct-password", "invalid"), ErrTotpInvalidCode)
			require.False(t, repo.disableCalled)
			code, err := totp.GenerateCode(secret, time.Now())
			require.NoError(t, err)
			require.NoError(t, svc.Disable(t.Context(), user.ID, "", "", code))
			require.True(t, repo.disableCalled)
		})
	}
}
