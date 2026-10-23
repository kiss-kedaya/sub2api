//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFreshNotificationCodeReusesSharedReplayAndAttemptGuards(t *testing.T) {
	for _, role := range []string{RoleUser, RoleAdmin} {
		t.Run(role, func(t *testing.T) {
			_, totpService, repo, _ := newCryptoSecurityTestServices(t, true)
			repo.user.Role = role
			code := cryptoSecurityTestCode(t, cryptoSecurityTestSecret)
			require.NoError(t, totpService.VerifyFreshCode(context.Background(), 7, code))
			require.ErrorIs(t, totpService.VerifyFreshCode(context.Background(), 7, code), ErrTotpCodeReplayed)
		})
	}
	security, totpService, _, _ := newCryptoSecurityTestServices(t, true)
	code := cryptoSecurityTestCode(t, cryptoSecurityTestSecret)
	require.NoError(t, security.VerifyRisk(context.Background(), 7, code))
	require.ErrorIs(t, totpService.VerifyFreshCode(context.Background(), 7, code), ErrTotpCodeReplayed)
	_, totpService, _, _ = newCryptoSecurityTestServices(t, false)
	require.ErrorIs(t, totpService.VerifyFreshCode(context.Background(), 7, code), ErrTotpNotSetup)
}
