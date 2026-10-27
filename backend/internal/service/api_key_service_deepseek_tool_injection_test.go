//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type deepSeekToolInjectionInvalidationCache struct {
	authCacheStub
	published []string
}

func (s *deepSeekToolInjectionInvalidationCache) PublishAuthCacheInvalidation(_ context.Context, key string) error {
	s.published = append(s.published, key)
	return nil
}

func TestAPIKeyServiceDeepSeekToolInjectionCreateDefaultAndOverride(t *testing.T) {
	disabled, enabled := false, true
	for _, setting := range []*bool{nil, &disabled, &enabled} {
		repo, cache := newCreateLimitStubs()
		svc := newCreateLimitService(repo, cache, 0, 0)
		key, err := svc.Create(context.Background(), 7, CreateAPIKeyRequest{
			Name: "tool-setting", DeepSeekToolInjectionEnabled: setting,
		})
		require.NoError(t, err)
		require.Len(t, repo.created, 1)
		want := setting == nil || *setting
		require.Equal(t, want, key.IsDeepSeekToolInjectionEnabled())
		require.Equal(t, want, repo.created[0].IsDeepSeekToolInjectionEnabled())
	}
}

func TestAPIKeyServiceDeepSeekToolInjectionUpdateChangesOnlyRequestedField(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		svc, repo := newUpdateFieldsAPIKeyService(&APIKey{
			ID: 1, UserID: 7, Key: "tool-setting", Status: StatusActive,
			Quota: 100, QuotaUsed: 30, Usage5h: 12,
		})
		key, err := svc.Update(context.Background(), 1, 7, UpdateAPIKeyRequest{
			DeepSeekToolInjectionEnabled: &enabled,
		})
		require.NoError(t, err)
		require.Equal(t, enabled, key.IsDeepSeekToolInjectionEnabled())
		require.Equal(t, []APIKeyUpdateFields{{DeepSeekToolInjectionEnabled: true}}, repo.updateFields)
		require.Equal(t, 30.0, key.QuotaUsed)
		require.Equal(t, 12.0, key.Usage5h)
	}
}

func TestAPIKeyServiceDeepSeekToolInjectionUpdateOmittedPreservesDisabled(t *testing.T) {
	disabled := false
	svc, repo := newUpdateFieldsAPIKeyService(&APIKey{
		ID: 1, UserID: 7, Key: "tool-setting", Status: StatusActive,
		DeepSeekToolInjectionEnabled: &disabled,
	})
	name := "renamed"
	key, err := svc.Update(context.Background(), 1, 7, UpdateAPIKeyRequest{Name: &name})
	require.NoError(t, err)
	require.False(t, key.IsDeepSeekToolInjectionEnabled())
	require.Equal(t, []APIKeyUpdateFields{{Name: true}}, repo.updateFields)
}

func TestAPIKeyServiceDeepSeekToolInjectionUpdateInvalidatesSharedAuthCache(t *testing.T) {
	svc, _ := newUpdateFieldsAPIKeyService(&APIKey{
		ID: 1, UserID: 7, Key: "tool-setting", Status: StatusActive,
	})
	cache := &deepSeekToolInjectionInvalidationCache{}
	svc.cache = cache
	disabled := false
	_, err := svc.Update(context.Background(), 1, 7, UpdateAPIKeyRequest{
		DeepSeekToolInjectionEnabled: &disabled,
	})
	require.NoError(t, err)
	expectedKey := svc.authCacheKey("tool-setting")
	require.Equal(t, []string{expectedKey}, cache.deleteAuthKeys)
	require.Equal(t, []string{expectedKey}, cache.published)
}
