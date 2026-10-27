//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyAuthCacheRejectsPreConcurrencyWriters(t *testing.T) {
	// A rolling-upgrade peer can still publish v26 entries with no limit.
	// Ignoring them forces the new instance to reload authoritative admission
	// policy; interpreting the missing field as zero would silently disable it.
	var entry APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal([]byte(`{"snapshot":{"version":26,"api_key_id":71,"user_id":42,"status":"active","user":{"id":42,"status":"active"}}}`), &entry))
	key, accepted, err := (&APIKeyService{}).applyAuthCacheEntry("synthetic", &entry)
	require.NoError(t, err)
	require.False(t, accepted)
	require.Nil(t, key)
}

func TestAPIKeyAuthVersionedInvalidationSubscribesWithoutL1(t *testing.T) {
	ready := make(chan struct{})
	cache := &authInvalidationCacheStub{subscribeFn: func(ctx context.Context, _ func(string)) error {
		NotifyAuthCacheSubscriptionReady(ctx)
		close(ready)
		<-ctx.Done()
		return ctx.Err()
	}}
	svc := &APIKeyService{cache: cache}
	require.Nil(t, svc.authCacheL1)
	require.Nil(t, svc.authNegativeCacheL1)
	svc.StartAuthCacheInvalidationSubscriber(context.Background())
	t.Cleanup(svc.StopAuthCacheInvalidationSubscriber)
	select {
	case <-ready:
		require.True(t, svc.AuthCacheInvalidationSubscriberHealth().Connected)
	case <-time.After(time.Second):
		t.Fatal("L2-only peer did not subscribe to cross-version invalidations")
	}
}

func TestAPIKeyAuthCacheCurrentConcurrencyRoundTrip(t *testing.T) {
	for _, limit := range []int{0, 1, 500} {
		original := &APIKey{ID: 71, UserID: 42, Status: StatusActive, ConcurrencyLimit: limit,
			User: &User{ID: 42, Status: StatusActive, Role: RoleUser, Concurrency: 0}}
		disabled := false
		original.DeepSeekToolInjectionEnabled = &disabled
		svc := &APIKeyService{}
		snapshot := svc.snapshotFromAPIKey(context.Background(), original)
		require.GreaterOrEqual(t, snapshot.Version, 27)
		payload, err := json.Marshal(APIKeyAuthCacheEntry{Snapshot: snapshot})
		require.NoError(t, err)
		var decoded APIKeyAuthCacheEntry
		require.NoError(t, json.Unmarshal(payload, &decoded))
		key, accepted, err := svc.applyAuthCacheEntry("synthetic", &decoded)
		require.NoError(t, err)
		require.True(t, accepted)
		require.Equal(t, limit, key.ConcurrencyLimit)
		require.Zero(t, key.User.Concurrency, "unlimited user admission is independent from the key limit")
		require.NotNil(t, key.DeepSeekToolInjectionEnabled)
		require.False(t, *key.DeepSeekToolInjectionEnabled, "v27 must retain v26's explicit tool opt-out")
	}
}
