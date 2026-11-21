//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"
)

type verificationBudgetTestCache struct {
	TotpCache
	attempts   int
	reads      int
	increments int
	clears     int
}

func (c *verificationBudgetTestCache) GetVerifyAttempts(context.Context, int64) (int, error) {
	c.reads++
	return c.attempts, nil
}

func (c *verificationBudgetTestCache) IncrementVerifyAttempts(context.Context, int64) (int, error) {
	c.increments++
	c.attempts++
	return c.attempts, nil
}

func (c *verificationBudgetTestCache) ClearVerifyAttempts(context.Context, int64) error {
	c.clears++
	c.attempts = 0
	return nil
}

type verificationBudgetTestUsers struct {
	UserRepository
	user *User
}

func (u verificationBudgetTestUsers) GetByID(context.Context, int64) (*User, error) {
	return u.user, nil
}

func TestLoginTOTPUsesOnlyReservedAccountBudget(t *testing.T) {
	const userID = 41
	secret := "JBSWY3DPEHPK3PXP" // local test fixture
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	cache := &verificationBudgetTestCache{attempts: maxTotpAttempts}
	encryptor := cryptoSecurityTestEncryptor{}
	encrypted, err := encryptor.Encrypt(secret)
	require.NoError(t, err)
	users := verificationBudgetTestUsers{user: &User{ID: userID, TotpEnabled: true, TotpSecretEncrypted: &encrypted}}
	svc := &TotpService{cache: cache, userRepo: users, encryptor: encryptor}
	attempts := 0
	ctx, admitted, _, err := ReserveAccountTOTPAttempt(t.Context(), userID, func(_ context.Context, id int64) (bool, time.Duration, error) {
		require.EqualValues(t, userID, id)
		attempts++
		return true, 0, nil
	})
	require.NoError(t, err)
	require.True(t, admitted)
	_, err = svc.VerifyLoginCode(ctx, userID, code)
	require.NoError(t, err)
	require.Equal(t, 1, attempts)
	require.Zero(t, cache.reads)
	require.Zero(t, cache.increments)
	require.Zero(t, cache.clears)
	require.Equal(t, maxTotpAttempts, cache.attempts)
	_, err = svc.VerifyLoginCode(ctx, userID, code)
	require.ErrorIs(t, err, ErrTotpUnavailable)
	_, err = svc.VerifyLoginCode(t.Context(), userID, code)
	require.ErrorIs(t, err, ErrTotpTooManyAttempts)
	require.Zero(t, cache.clears)
}

func TestAccountTOTPAttemptCheckFailureKeepsContextUnreserved(t *testing.T) {
	base := t.Context()
	unavailable := errors.New("verification fixture unavailable")
	ctx, allowed, _, err := ReserveAccountTOTPAttempt(base, 41, func(context.Context, int64) (bool, time.Duration, error) {
		return true, 0, unavailable
	})
	require.ErrorIs(t, err, unavailable)
	require.False(t, allowed)
	require.Equal(t, base, ctx)
}
