//go:build unit

package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

type totpSecretRaceRepo struct {
	UserRepository
	user           User
	getCalls       int
	afterFirstRead func()
	disableCalls   int
	verifiedSecret *string
	disabled       bool
}

func (r *totpSecretRaceRepo) GetByID(context.Context, int64) (*User, error) {
	r.getCalls++
	user := r.user
	if r.getCalls == 1 && r.afterFirstRead != nil {
		r.afterFirstRead()
	}
	return &user, nil
}

func (r *totpSecretRaceRepo) DisableTotpWithExpectedRole(_ context.Context, _ int64, role string, secret *string) error {
	r.disableCalls++
	r.verifiedSecret = secret
	if role != r.user.Role || r.user.Status != StatusActive || (secret == nil) != (r.user.TotpSecretEncrypted == nil) {
		return ErrUserSecurityStateChanged
	}
	if secret != nil && *secret != *r.user.TotpSecretEncrypted {
		return ErrUserSecurityStateChanged
	}
	r.disabled = true
	return nil
}

type totpSecretRaceCache struct{ TotpCache }

func (totpSecretRaceCache) GetVerifyAttempts(context.Context, int64) (int, error) { return 0, nil }
func (totpSecretRaceCache) ClearVerifyAttempts(context.Context, int64) error      { return nil }

type totpSecretRaceEncryptor struct{ onDecrypt func() }

func (totpSecretRaceEncryptor) Encrypt(string) (string, error) { panic("unexpected encryption") }
func (e totpSecretRaceEncryptor) Decrypt(encrypted string) (string, error) {
	if e.onDecrypt != nil {
		e.onDecrypt()
	}
	return strings.TrimPrefix(encrypted, "encrypted:"), nil
}

func TestTotpDisableVerifiedOldCodeCannotDisableReboundAuthenticator(t *testing.T) {
	const oldSecret = "JBSWY3DPEHPK3PXP"
	oldEncrypted := "encrypted:" + oldSecret
	newEncrypted := "encrypted:new-authenticator"
	repo := &totpSecretRaceRepo{user: User{ID: 42, Role: RoleAdmin, Status: StatusActive, TotpEnabled: true, TotpSecretEncrypted: &oldEncrypted}}
	encryptor := totpSecretRaceEncryptor{onDecrypt: func() {
		// A concurrent legitimate disable and rebind completed after this request
		// read its verification snapshot but before its conditional write.
		repo.user.TotpSecretEncrypted = &newEncrypted
	}}
	svc := NewTotpService(repo, encryptor, totpSecretRaceCache{}, nil, nil, nil)
	code, err := totp.GenerateCode(oldSecret, time.Now())
	require.NoError(t, err)
	require.ErrorIs(t, svc.Disable(context.Background(), 42, "", "", code), ErrUserSecurityStateChanged)
	require.False(t, repo.disabled)
	require.Equal(t, 1, repo.disableCalls)
	require.NotNil(t, repo.verifiedSecret)
	require.Equal(t, oldEncrypted, *repo.verifiedSecret)
	require.Equal(t, newEncrypted, *repo.user.TotpSecretEncrypted)
}

func TestTotpDisableUsesSecretActuallyVerifiedOnSecondRead(t *testing.T) {
	const currentSecret = "JBSWY3DPEHPK3PXP"
	oldEncrypted := "encrypted:old-authenticator"
	currentEncrypted := "encrypted:" + currentSecret
	repo := &totpSecretRaceRepo{user: User{ID: 42, Role: RoleAdmin, Status: StatusActive, TotpEnabled: true, TotpSecretEncrypted: &oldEncrypted}}
	repo.afterFirstRead = func() { repo.user.TotpSecretEncrypted = &currentEncrypted }
	svc := NewTotpService(repo, totpSecretRaceEncryptor{}, totpSecretRaceCache{}, nil, nil, nil)
	code, err := totp.GenerateCode(currentSecret, time.Now())
	require.NoError(t, err)
	require.NoError(t, svc.Disable(context.Background(), 42, "", "", code))
	require.True(t, repo.disabled)
	require.Equal(t, 2, repo.getCalls)
	require.NotNil(t, repo.verifiedSecret)
	require.Equal(t, currentEncrypted, *repo.verifiedSecret)
}
