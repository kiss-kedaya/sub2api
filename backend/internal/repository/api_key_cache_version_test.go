package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyAuthL2VersionNamespacesDoNotOverwrite(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewAPIKeyCache(client)
	ctx := context.Background()
	hash := "same-key-hash"
	legacy := apiKeyAuthCacheLegacyPrefix + hash
	current := fmt.Sprintf("apikey:auth:v%d:%s", service.APIKeyAuthCacheSchemaVersion, hash)
	old := `{"snapshot":{"version":26,"api_key_id":71}}`
	require.NoError(t, client.Set(ctx, legacy, old, time.Minute).Err())
	_, err := cache.GetAuthCache(ctx, hash)
	require.ErrorIs(t, err, redis.Nil, "new binaries never use an old writer's L2 snapshot")
	entry := &service.APIKeyAuthCacheEntry{Snapshot: &service.APIKeyAuthSnapshot{Version: service.APIKeyAuthCacheSchemaVersion, APIKeyID: 71, ConcurrencyLimit: 3}}
	require.NoError(t, cache.SetAuthCache(ctx, hash, entry, time.Minute))
	require.Equal(t, old, serverValue(t, client, legacy))
	loaded, err := cache.GetAuthCache(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, 3, loaded.Snapshot.ConcurrencyLimit)
	require.NoError(t, client.Set(ctx, legacy, old, time.Minute).Err())
	require.True(t, client.Exists(ctx, current).Val() == 1)
	require.NoError(t, cache.DeleteAuthCache(ctx, hash))
	require.Zero(t, client.Exists(ctx, legacy, current).Val(), "direct eviction clears both active namespaces")
}

func serverValue(t *testing.T, client *redis.Client, key string) string {
	t.Helper()
	value, err := client.Get(context.Background(), key).Result()
	require.NoError(t, err)
	return value
}

func TestAPIKeyAuthL2LegacyPublicationEvictsCurrentNamespace(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewAPIKeyCache(client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hash := "cross-version-hash"
	entry := &service.APIKeyAuthCacheEntry{Snapshot: &service.APIKeyAuthSnapshot{Version: service.APIKeyAuthCacheSchemaVersion, ConcurrencyLimit: 4}}
	payload, err := json.Marshal(entry)
	require.NoError(t, err)
	received := make(chan string, 4)
	returned := make(chan error, 1)
	go func() {
		returned <- cache.SubscribeAuthCacheInvalidation(ctx, func(value string) { received <- value })
	}()
	require.Eventually(t, func() bool {
		count, err := client.PubSubNumSub(ctx, authCacheInvalidateChannel).Result()
		return err == nil && count[authCacheInvalidateChannel] == 1
	}, time.Second, 5*time.Millisecond)
	current := apiKeyAuthCacheKey(hash)
	legacy := apiKeyAuthCacheLegacyPrefix + hash
	require.NoError(t, client.Set(ctx, current, payload, time.Minute).Err())
	require.NoError(t, client.Set(ctx, legacy, "old", time.Minute).Err())
	// Simulate the previous binary: the unchanged channel carries only a hash,
	// and its own DEL cannot know the current version's Redis key prefix.
	require.NoError(t, client.Del(ctx, legacy).Err())
	require.NoError(t, client.Publish(ctx, authCacheInvalidateChannel, hash).Err())
	select {
	case got := <-received:
		require.Equal(t, hash, got)
	case <-time.After(time.Second):
		t.Fatal("legacy invalidation was not delivered")
	}
	require.Zero(t, client.Exists(ctx, current).Val(), "new peer must evict L2 before acknowledging invalidation")
	cancel()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("subscriber did not stop")
	}
}
