//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type cryptoGrantRefreshCache struct {
	RefreshTokenCache
	data      *RefreshTokenData
	getErr    error
	revokeErr error
	families  []string
	deleted   []string
}

func (c *cryptoGrantRefreshCache) GetRefreshToken(context.Context, string) (*RefreshTokenData, error) {
	return c.data, c.getErr
}

func (c *cryptoGrantRefreshCache) DeleteTokenFamily(_ context.Context, family string) error {
	c.families = append(c.families, family)
	return c.revokeErr
}

func (c *cryptoGrantRefreshCache) DeleteRefreshToken(_ context.Context, hash string) error {
	c.deleted = append(c.deleted, hash)
	return nil
}

func TestCryptoGrantLogoutRefreshRevokesFamilyBeforeDeletingToken(t *testing.T) {
	cache := &cryptoGrantRefreshCache{data: &RefreshTokenData{UserID: 7, FamilyID: "same-session"}}
	auth := &AuthService{refreshTokenCache: cache}
	require.NoError(t, auth.RevokeRefreshToken(t.Context(), "rt_fixture-only"))
	require.Equal(t, []string{"same-session"}, cache.families)
	require.Equal(t, []string{hashToken("rt_fixture-only")}, cache.deleted)
	cache.deleted = nil
	cache.revokeErr = errors.New("store unavailable")
	require.Error(t, auth.RevokeRefreshToken(t.Context(), "rt_fixture-only"))
	require.Empty(t, cache.deleted, "a failed payment revocation cannot be reported as completed logout")
}

func TestCryptoGrantAccessLogoutValidatesSignatureAndSupportsLegacySession(t *testing.T) {
	cache := &cryptoGrantRefreshCache{}
	auth := &AuthService{refreshTokenCache: cache, cfg: &config.Config{JWT: config.JWTConfig{
		Secret: "test-only-logout-signing-key", ExpireHour: 1,
	}}}
	user := &User{ID: 7, Email: "admin@example.test", PasswordHash: "fixture-password-hash", Role: RoleAdmin, Status: StatusActive}
	for _, session := range []string{"test-session", ""} {
		token, err := auth.generateAccessToken(user, session, "")
		require.NoError(t, err)
		require.NoError(t, auth.RevokeCryptoGrantSessionForAccessToken(t.Context(), token))
		want := session
		if want == "" {
			want = "jwt:" + hashToken(token)
		}
		require.Equal(t, want, cache.families[len(cache.families)-1])
	}
	count := len(cache.families)
	require.Error(t, auth.RevokeCryptoGrantSessionForAccessToken(t.Context(), "forged-token"))
	require.Len(t, cache.families, count, "unverified logout input cannot revoke a chosen session")
}
