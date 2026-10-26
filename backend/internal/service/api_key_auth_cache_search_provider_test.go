//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Serialize like Redis: an in-memory pointer cache would hide fields missing
// from either side of the auth snapshot projection.
type searchProviderAuthCache struct {
	authCacheStub
	payload []byte
	reads   int
	writes  int
}

func (c *searchProviderAuthCache) GetAuthCache(context.Context, string) (*APIKeyAuthCacheEntry, error) {
	c.reads++
	if c.payload == nil {
		return nil, redis.Nil
	}
	var entry APIKeyAuthCacheEntry
	if err := json.Unmarshal(c.payload, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

func (c *searchProviderAuthCache) SetAuthCache(_ context.Context, _ string, entry *APIKeyAuthCacheEntry, _ time.Duration) error {
	var err error
	c.payload, err = json.Marshal(entry)
	c.writes++
	return err
}

func searchProviderAuthKey(configured bool) *APIKey {
	groupID := int64(36)
	price := 5.0
	group := &Group{
		ID: groupID, Platform: PlatformDeepseek, Status: StatusActive,
		SearchPricePer1k: &price,
	}
	if configured {
		baseURL, credential := "https://search.example.test", "search-only-fixture"
		group.SearchProviderBaseURL, group.SearchProviderAPIKey = &baseURL, &credential
	}
	return &APIKey{
		ID: 101, UserID: 202, Key: "auth-search-fixture", GroupID: &groupID,
		Status: StatusActive, User: &User{ID: 202, Status: StatusActive}, Group: group,
	}
}

func requireSearchProvider(t *testing.T, want, got *APIKey) {
	t.Helper()
	require.NotNil(t, got.Group)
	require.Equal(t, want.Group.SearchPricePer1k, got.Group.SearchPricePer1k)
	require.Equal(t, want.Group.SearchProviderBaseURL, got.Group.SearchProviderBaseURL)
	require.Equal(t, want.Group.SearchProviderAPIKey, got.Group.SearchProviderAPIKey)
}

func TestAPIKeySearchProviderSurvivesColdL2AndL1Authentication(t *testing.T) {
	for _, configured := range []bool{true, false} {
		name := "configured"
		if !configured {
			name = "known empty"
		}
		t.Run(name, func(t *testing.T) {
			original := searchProviderAuthKey(configured)
			lookups := 0
			repo := &authRepoStub{getByKeyForAuth: func(context.Context, string) (*APIKey, error) {
				lookups++
				return original, nil
			}}
			cache := &searchProviderAuthCache{}
			cfg := &config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{L2TTLSeconds: 60}}
			cold := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)
			loaded, err := cold.GetByKey(t.Context(), original.Key)
			require.NoError(t, err)
			requireSearchProvider(t, original, loaded)
			require.Equal(t, 1, lookups)
			require.Equal(t, 1, cache.writes)

			// A fresh instance reads the JSON L2 entry, then serves its own L1.
			cfg.APIKeyAuth.L1Size, cfg.APIKeyAuth.L1TTLSeconds = 1000, 60
			warm := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)
			require.NotNil(t, warm.authCacheL1)
			t.Cleanup(warm.authCacheL1.Close)
			loaded, err = warm.GetByKey(t.Context(), original.Key)
			require.NoError(t, err)
			requireSearchProvider(t, original, loaded)
			require.Equal(t, 1, lookups, "L2 hit must not repeat the database lookup")
			warm.authCacheL1.Wait()
			reads := cache.reads
			loaded, err = warm.GetByKey(t.Context(), original.Key)
			require.NoError(t, err)
			requireSearchProvider(t, original, loaded)
			require.Equal(t, reads, cache.reads, "L1 hit must not repeat the Redis lookup")
			require.Equal(t, 1, lookups, "known-empty provider configuration is cacheable too")
		})
	}
}

func TestAPIKeyLegacyDeepSeekProviderSnapshotReloadsOnce(t *testing.T) {
	for _, tier := range []string{"L1", "L2"} {
		t.Run(tier, func(t *testing.T) {
			original := searchProviderAuthKey(true)
			cache := &searchProviderAuthCache{}
			lookups := 0
			repo := &authRepoStub{getByKeyForAuth: func(context.Context, string) (*APIKey, error) {
				lookups++
				return original, nil
			}}
			cfg := &config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{L2TTLSeconds: 60}}
			if tier == "L1" {
				cfg.APIKeyAuth.L1Size, cfg.APIKeyAuth.L1TTLSeconds = 1000, 60
			}
			svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)
			legacy := &APIKeyAuthCacheEntry{Snapshot: svc.snapshotFromAPIKey(t.Context(), original)}
			legacy.Snapshot.Group.SearchProviderConfigLoaded = false
			legacy.Snapshot.Group.SearchProviderBaseURL = nil
			legacy.Snapshot.Group.SearchProviderAPIKey = nil
			var err error
			cache.payload, err = json.Marshal(legacy)
			require.NoError(t, err)
			if svc.authCacheL1 != nil {
				t.Cleanup(svc.authCacheL1.Close)
				svc.setAuthCacheL1(svc.authCacheKey(original.Key), legacy)
				svc.authCacheL1.Wait()
			}
			for range 2 {
				loaded, err := svc.GetByKey(t.Context(), original.Key)
				require.NoError(t, err)
				requireSearchProvider(t, original, loaded)
				if svc.authCacheL1 != nil {
					svc.authCacheL1.Wait()
				}
			}
			require.Equal(t, 1, lookups, "legacy configuration is refreshed once, then cached")
		})
	}
}

func TestAPIKeyLegacyOtherPlatformSnapshotDoesNotInvalidate(t *testing.T) {
	svc := &APIKeyService{}
	original := searchProviderAuthKey(false)
	original.Group.Platform = PlatformOpenAI
	snapshot := svc.snapshotFromAPIKey(t.Context(), original)
	snapshot.Group.SearchProviderConfigLoaded = false
	loaded, used, err := svc.applyAuthCacheEntry(original.Key, &APIKeyAuthCacheEntry{Snapshot: snapshot})
	require.NoError(t, err)
	require.True(t, used, "rolling deployment must not invalidate unrelated platforms")
	requireSearchProvider(t, original, loaded)
}
