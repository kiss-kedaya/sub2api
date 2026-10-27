//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

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
