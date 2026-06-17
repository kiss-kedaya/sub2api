//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/stretchr/testify/require"
)

type grokTokenCacheForProviderTest struct {
	token        string
	setKey       string
	setToken     string
	setTTL       time.Duration
	lockResult   bool
	releaseCalls int
}

func (c *grokTokenCacheForProviderTest) GetAccessToken(context.Context, string) (string, error) {
	if c.token == "" {
		return "", errors.New("not cached")
	}
	return c.token, nil
}

func (c *grokTokenCacheForProviderTest) SetAccessToken(_ context.Context, key string, token string, ttl time.Duration) error {
	c.setKey = key
	c.setToken = token
	c.setTTL = ttl
	return nil
}

func (c *grokTokenCacheForProviderTest) DeleteAccessToken(context.Context, string) error {
	return nil
}

func (c *grokTokenCacheForProviderTest) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return c.lockResult, nil
}

func (c *grokTokenCacheForProviderTest) ReleaseRefreshLock(context.Context, string) error {
	c.releaseCalls++
	return nil
}

func TestGrokTokenProviderRefreshesExpiredTokenOnRequestPath(t *testing.T) {
	t.Setenv(xai.EnvBaseURL, xai.DefaultCLIBaseURL)

	expiredAt := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	account := &Account{
		ID:       54,
		Platform: PlatformGrok,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "expired-access-token",
			"refresh_token": "refresh-token",
			"expires_at":    expiredAt,
			"base_url":      xai.DefaultCLIBaseURL,
			"client_id":     "client-id",
		},
	}
	repo := &tokenRefreshAccountRepo{}
	repo.accountsByID = map[int64]*Account{54: account}
	cache := &grokTokenCacheForProviderTest{lockResult: true}
	oauthSvc := NewGrokOAuthService(nil, &grokOAuthClientStub{
		refreshResponse: &xai.TokenResponse{
			AccessToken: "new-access-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
		},
	})
	defer oauthSvc.Stop()

	provider := NewGrokTokenProvider(repo, cache, oauthSvc)
	provider.SetRefreshAPI(NewOAuthRefreshAPI(repo, cache), NewGrokTokenRefresher(oauthSvc))

	token, err := provider.GetAccessToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "new-access-token", token)
	require.Equal(t, 1, repo.updateCredentialsCalls)
	require.Equal(t, "new-access-token", repo.accountsByID[54].GetGrokAccessToken())
	require.Equal(t, "refresh-token", repo.accountsByID[54].GetGrokRefreshToken())
	require.Equal(t, xai.DefaultCLIBaseURL, repo.accountsByID[54].GetGrokBaseURL())
	require.Equal(t, "grok:account:54", cache.setKey)
	require.Equal(t, "new-access-token", cache.setToken)
	require.Greater(t, cache.setTTL, time.Duration(0))
	require.Equal(t, 1, cache.releaseCalls)
}
