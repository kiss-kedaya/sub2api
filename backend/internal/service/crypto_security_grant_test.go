//go:build unit

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type cryptoGrantTestStore struct {
	CryptoTotpStore
	lastTTL          time.Duration
	failExists       bool
	userRevokedAt    int64
	sessionRevokedAt int64
}

func (s *cryptoGrantTestStore) CryptoGrantRevocationTimes(context.Context, int64, string) (int64, int64, error) {
	if s.failExists {
		return 0, 0, errors.New("unavailable")
	}
	return s.userRevokedAt, s.sessionRevokedAt, nil
}

func (s *cryptoGrantTestStore) SetCryptoValueIfAbsent(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	s.lastTTL = ttl
	return s.CryptoTotpStore.SetCryptoValueIfAbsent(ctx, key, value, ttl)
}

func (s *cryptoGrantTestStore) CryptoValueExists(ctx context.Context, key string) (bool, error) {
	if s.failExists {
		return false, errors.New("unavailable")
	}
	return s.CryptoTotpStore.CryptoValueExists(ctx, key)
}

func TestCryptoGrantUsesFiveMinuteRegistrationWithoutRenewal(t *testing.T) {
	security, _, _, cache := newCryptoSecurityTestServices(t, true)
	store := &cryptoGrantTestStore{CryptoTotpStore: cache}
	security.store = store
	code := cryptoSecurityTestCode(t, cryptoSecurityTestSecret)
	grant, err := security.IssueRiskGrant(t.Context(), 7, "session-one", code)
	require.NoError(t, err)
	require.EqualValues(t, 300, grant.ExpiresIn)
	require.Positive(t, store.lastTTL)
	require.LessOrEqual(t, store.lastTTL, 5*time.Minute)
	require.WithinDuration(t, time.Now().Add(5*time.Minute), grant.ExpiresAt, time.Second)
	registeredTTL := store.lastTTL
	for range 3 {
		require.NoError(t, security.VerifyRiskGrant(t.Context(), 7, "session-one", grant.Credential))
	}
	require.Equal(t, registeredTTL, store.lastTTL, "grant verification never renews the shared TTL")
	_, err = security.IssueRiskGrant(t.Context(), 7, "session-one", code)
	require.ErrorIs(t, err, ErrCryptoTotpReplayed, "a grant cannot remove TOTP replay protection")
	for key, value := range cache.values {
		require.NotContains(t, key, grant.Credential)
		require.NotContains(t, value, grant.Credential)
		require.NotContains(t, key, "session-one")
	}
}

func TestCryptoGrantRejectsForgeryAndDifferentAuthenticatedSession(t *testing.T) {
	security, _, repo, _ := newCryptoSecurityTestServices(t, true)
	grant, err := security.IssueRiskGrant(t.Context(), 7, "session-one", cryptoSecurityTestCode(t, cryptoSecurityTestSecret))
	require.NoError(t, err)
	parts := strings.Split(grant.Credential, ".")
	for _, credential := range []string{
		"", "self-issued", "cpg1.invalid.invalid", strings.Repeat("a", 2049),
		grant.Credential + "," + grant.Credential,
		"cpg1." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
		"cpg2." + parts[1] + "." + parts[2],
	} {
		require.ErrorIs(t, security.VerifyRiskGrant(t.Context(), 7, "session-one", credential), ErrCryptoGrantRequired)
	}
	require.ErrorIs(t, security.VerifyRiskGrant(t.Context(), 7, "session-two", grant.Credential), ErrCryptoGrantRequired)
	require.ErrorIs(t, security.VerifyRiskGrant(t.Context(), 7, "", grant.Credential), ErrCryptoGrantRequired)
	for _, session := range []string{" ", "session\n", "session\x00", strings.Repeat("s", 513)} {
		require.ErrorIs(t, security.VerifyRiskGrant(t.Context(), 7, session, grant.Credential), ErrCryptoGrantRequired)
		_, err := security.IssueRiskGrant(t.Context(), 7, session, "123456")
		require.ErrorIs(t, err, ErrCryptoAdminRequired)
	}
	require.ErrorIs(t, security.VerifyRiskGrant(t.Context(), 8, "session-one", grant.Credential), ErrCryptoOperatorRequired)
	security.operators[8] = struct{}{}
	repo.user.ID = 8
	require.ErrorIs(t, security.VerifyRiskGrant(t.Context(), 8, "session-one", grant.Credential), ErrCryptoGrantRequired)
	repo.user.ID = 7
	_, err = security.IssueRiskGrant(t.Context(), 7, "session-one", "not-a-code")
	require.ErrorIs(t, err, ErrCryptoTotpRequired)
}

func TestCryptoGrantRejectsExpiredOrUnregisteredSignedCapabilities(t *testing.T) {
	security, _, _, cache := newCryptoSecurityTestServices(t, true)
	grant, err := security.IssueRiskGrant(t.Context(), 7, "session", cryptoSecurityTestCode(t, cryptoSecurityTestSecret))
	require.NoError(t, err)
	parts := strings.Split(grant.Credential, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var original cryptoGrantClaims
	require.NoError(t, json.Unmarshal(payload, &original))
	for _, tc := range []struct {
		name string
		edit func(*cryptoGrantClaims)
	}{
		{"expired", func(c *cryptoGrantClaims) {
			c.IssuedAt = time.Now().Unix() - 301
			c.IssuedAtMS = c.IssuedAt * 1000
			c.ExpiresAt = c.IssuedAt + 300
		}},
		{"future", func(c *cryptoGrantClaims) {
			c.IssuedAt = time.Now().Unix() + 60
			c.IssuedAtMS = c.IssuedAt * 1000
			c.ExpiresAt = c.IssuedAt + 300
		}},
		{"extended", func(c *cryptoGrantClaims) { c.ExpiresAt += 60 }},
		{"unregistered", func(c *cryptoGrantClaims) { c.Nonce = base64.RawURLEncoding.EncodeToString(make([]byte, 32)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := original
			tc.edit(&claims)
			credential, err := security.signRiskGrant(claims)
			require.NoError(t, err)
			if tc.name != "unregistered" {
				cache.values[security.grantStoreKey(7, credential)] = "1"
			}
			require.ErrorIs(t, security.VerifyRiskGrant(t.Context(), 7, "session", credential), ErrCryptoGrantRequired)
		})
	}
}

func TestCryptoGrantFailsClosedWhenSecurityStateChanges(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*User)
		want error
	}{
		{"password", func(u *User) { u.PasswordHash += "changed" }, ErrCryptoGrantRequired},
		{"email", func(u *User) { u.Email = "other@example.test" }, ErrCryptoGrantRequired},
		{"authenticator", func(u *User) { secret := "changed-ciphertext"; u.TotpSecretEncrypted = &secret }, ErrCryptoGrantRequired},
		{"2fa-reenrolled", func(u *User) { now := time.Now(); u.TotpEnabledAt = &now }, ErrCryptoGrantRequired},
		{"2fa-disabled", func(u *User) { u.TotpEnabled = false }, ErrCryptoTotpNotSetup},
		{"demoted", func(u *User) { u.Role = RoleUser }, ErrCryptoAdminRequired},
		{"blocked", func(u *User) { u.Status = "disabled" }, ErrCryptoAdminRequired},
		{"deleted", func(u *User) { now := time.Now(); u.DeletedAt = &now }, ErrCryptoAdminRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			security, _, repo, _ := newCryptoSecurityTestServices(t, true)
			grant, err := security.IssueRiskGrant(t.Context(), 7, "session", cryptoSecurityTestCode(t, cryptoSecurityTestSecret))
			require.NoError(t, err)
			tc.edit(&repo.user)
			require.ErrorIs(t, security.VerifyRiskGrant(t.Context(), 7, "session", grant.Credential), tc.want)
		})
	}
}

func TestCryptoGrantRequiresSharedStoreForEveryUse(t *testing.T) {
	security, _, _, cache := newCryptoSecurityTestServices(t, true)
	store := &cryptoGrantTestStore{CryptoTotpStore: cache}
	security.store = store
	grant, err := security.IssueRiskGrant(t.Context(), 7, "session", cryptoSecurityTestCode(t, cryptoSecurityTestSecret))
	require.NoError(t, err)
	store.failExists = true
	require.ErrorIs(t, security.VerifyRiskGrant(t.Context(), 7, "session", grant.Credential), ErrCryptoTotpUnavailable)
	store.failExists = false
	delete(cache.values, security.grantStoreKey(7, grant.Credential))
	require.ErrorIs(t, security.VerifyRiskGrant(t.Context(), 7, "session", grant.Credential), ErrCryptoGrantRequired)
}

func TestCryptoGrantLogoutRevokesExistingGrantIncludingSameMillisecond(t *testing.T) {
	security, _, _, cache := newCryptoSecurityTestServices(t, true)
	store := &cryptoGrantTestStore{CryptoTotpStore: cache}
	security.store = store
	grant, err := security.IssueRiskGrant(t.Context(), 7, "session", cryptoSecurityTestCode(t, cryptoSecurityTestSecret))
	require.NoError(t, err)
	parts := strings.Split(grant.Credential, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)
	var claims cryptoGrantClaims
	require.NoError(t, json.Unmarshal(payload, &claims))
	store.userRevokedAt = claims.IssuedAtMS - 1
	require.NoError(t, security.VerifyRiskGrant(t.Context(), 7, "session", grant.Credential), "fresh verification after logout can create a new grant")
	store.userRevokedAt = claims.IssuedAtMS
	require.ErrorIs(t, security.VerifyRiskGrant(t.Context(), 7, "session", grant.Credential), ErrCryptoGrantRequired)
	store.userRevokedAt = 0
	store.sessionRevokedAt = claims.IssuedAtMS
	require.ErrorIs(t, security.VerifyRiskGrant(t.Context(), 7, "session", grant.Credential), ErrCryptoGrantRequired)
	store.sessionRevokedAt = claims.IssuedAtMS + 1
	require.ErrorIs(t, security.VerifyRiskGrant(t.Context(), 7, "session", grant.Credential), ErrCryptoGrantRequired)
}
