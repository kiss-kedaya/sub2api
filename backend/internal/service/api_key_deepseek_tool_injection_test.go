package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyDeepSeekToolInjectionDefaultAndExplicitValues(t *testing.T) {
	disabled, enabled := false, true
	for _, test := range []struct {
		name string
		key  *APIKey
		want bool
	}{
		{"nil key", nil, true},
		{"legacy key", &APIKey{}, true},
		{"enabled", &APIKey{DeepSeekToolInjectionEnabled: &enabled}, true},
		{"disabled", &APIKey{DeepSeekToolInjectionEnabled: &disabled}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, test.key.IsDeepSeekToolInjectionEnabled())
		})
	}
}

func TestAPIKeyDeepSeekToolInjectionAuthSnapshotRoundtrip(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		key := &APIKey{
			ID: 1, UserID: 2, Status: StatusActive,
			DeepSeekToolInjectionEnabled: &enabled,
			User:                         &User{ID: 2, Status: StatusActive},
		}
		svc := &APIKeyService{}
		snapshot := svc.snapshotFromAPIKey(context.Background(), key)
		payload, err := json.Marshal(APIKeyAuthCacheEntry{Snapshot: snapshot})
		require.NoError(t, err)
		var cached APIKeyAuthCacheEntry
		require.NoError(t, json.Unmarshal(payload, &cached))
		materialized, used, err := svc.applyAuthCacheEntry("test-deepseek-key", &cached)
		require.NoError(t, err)
		require.True(t, used)
		require.NotNil(t, materialized.DeepSeekToolInjectionEnabled)
		require.Equal(t, enabled, materialized.IsDeepSeekToolInjectionEnabled())
	}
}

func TestAPIKeyDeepSeekToolInjectionMissingSnapshotFieldDefaultsEnabled(t *testing.T) {
	svc := &APIKeyService{}
	snapshot := svc.snapshotFromAPIKey(context.Background(), &APIKey{
		ID: 1, UserID: 2, Status: StatusActive, User: &User{ID: 2, Status: StatusActive},
	})
	payload, err := json.Marshal(APIKeyAuthCacheEntry{Snapshot: snapshot})
	require.NoError(t, err)
	require.NotContains(t, string(payload), "deepseek_tool_injection_enabled")
	var cached APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(payload, &cached))
	materialized, used, err := svc.applyAuthCacheEntry("test-legacy-deepseek-key", &cached)
	require.NoError(t, err)
	require.True(t, used)
	require.True(t, materialized.IsDeepSeekToolInjectionEnabled())
}

// An old process can write a shared snapshot after a key's setting changed.
// Reject its version instead of silently turning explicit false back on.
func TestAPIKeyDeepSeekToolInjectionRejectsOlderSnapshotWriter(t *testing.T) {
	key, used, err := (&APIKeyService{}).applyAuthCacheEntry("test-old-deepseek-writer", &APIKeyAuthCacheEntry{
		Snapshot: &APIKeyAuthSnapshot{Version: 25},
	})
	require.NoError(t, err)
	require.False(t, used)
	require.Nil(t, key)
}
