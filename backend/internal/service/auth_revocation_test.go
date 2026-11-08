package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type revocationStoreStub struct {
	RefreshTokenCache
	before time.Time
	err    error
}

func (s *revocationStoreStub) RevokeAccessTokensBefore(context.Context, int64, time.Time, time.Duration) error {
	return s.err
}

func (s *revocationStoreStub) AccessTokensRevokedBefore(context.Context, int64) (time.Time, error) {
	return s.before, s.err
}

func TestAuthServiceIsAccessTokenRevoked(t *testing.T) {
	cutoff := time.Unix(1_700_000_000, 0)
	svc := &AuthService{refreshTokenCache: &revocationStoreStub{before: cutoff}}

	require.True(t, svc.IsAccessTokenRevoked(context.Background(), 7, cutoff.Add(-time.Minute)), "token issued before the cutoff is revoked")
	require.False(t, svc.IsAccessTokenRevoked(context.Background(), 7, cutoff.Add(time.Minute)), "token issued after the cutoff stays valid")
	require.False(t, svc.IsAccessTokenRevoked(context.Background(), 7, cutoff.Add(time.Nanosecond)), "the revocation instant itself is invalidated")
}

func TestAuthServiceIsAccessTokenRevokedWithoutStore(t *testing.T) {
	svc := &AuthService{}
	require.False(t, svc.IsAccessTokenRevoked(context.Background(), 7, time.Now()))
}

type atomicVerifyCacheStub struct {
	EmailCache
	status int
}

func (s atomicVerifyCacheStub) ConsumeVerificationCode(context.Context, string, string, int) (int, int, error) {
	return s.status, 0, nil
}

func TestEmailServiceVerifyCodeUsesAtomicConsumer(t *testing.T) {
	svc := &EmailService{cache: atomicVerifyCacheStub{status: 1}}
	require.NoError(t, svc.VerifyCode(context.Background(), "user@example.com", "123456"))

	svc = &EmailService{cache: atomicVerifyCacheStub{status: 0}}
	require.ErrorIs(t, svc.VerifyCode(context.Background(), "user@example.com", "000000"), ErrInvalidVerifyCode)

	svc = &EmailService{cache: atomicVerifyCacheStub{status: -2}}
	require.ErrorIs(t, svc.VerifyCode(context.Background(), "user@example.com", "000000"), ErrVerifyCodeMaxAttempts)
}
