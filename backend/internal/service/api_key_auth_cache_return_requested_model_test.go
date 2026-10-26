package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyAuthSnapshotReturnRequestedModelRoundTripAndLegacyDefault(t *testing.T) {
	for _, test := range []struct {
		name    string
		enabled bool
		legacy  bool
		want    bool
	}{
		{name: "enabled", enabled: true, want: true},
		{name: "explicit false"},
		{name: "older snapshot is off", enabled: true, legacy: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			groupID := int64(17)
			apiKey := &APIKey{
				ID: 29, UserID: 31, GroupID: &groupID, Key: "sk-model-fixture", Status: StatusActive,
				User: &User{ID: 31, Status: StatusActive},
				Group: &Group{ID: groupID, Name: "model-policy", Platform: PlatformOpenAI,
					Status: StatusActive, Hydrated: true, ReturnRequestedModel: test.enabled},
			}
			svc := &APIKeyService{}
			payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: svc.snapshotFromAPIKey(t.Context(), apiKey)})
			require.NoError(t, err)
			var raw map[string]any
			require.NoError(t, json.Unmarshal(payload, &raw))
			snapshot, ok := raw["snapshot"].(map[string]any)
			require.True(t, ok)
			group, ok := snapshot["group"].(map[string]any)
			require.True(t, ok)
			require.Contains(t, group, "return_requested_model")
			if test.legacy {
				delete(group, "return_requested_model")
			}
			payload, err = json.Marshal(raw)
			require.NoError(t, err)
			var decoded APIKeyAuthCacheEntry
			require.NoError(t, json.Unmarshal(payload, &decoded))
			materialized, used, err := svc.applyAuthCacheEntry(apiKey.Key, &decoded)
			require.NoError(t, err)
			require.True(t, used)
			require.NotNil(t, materialized.Group)
			require.True(t, materialized.Group.Hydrated)
			require.Equal(t, test.want, materialized.Group.ReturnRequestedModel)
		})
	}
}

func TestSchedulerGroupCachePreservesReturnRequestedModelAndIndependentCopies(t *testing.T) {
	snapshot := &SchedulerSnapshotService{}
	group := &Group{ID: 17, Name: "model-policy", Hydrated: true, ReturnRequestedModel: true}
	snapshot.storeCachedGroup(group)
	group.ReturnRequestedModel = false

	loaded, err := snapshot.GetGroupByIDLite(t.Context(), group.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.True(t, loaded.ReturnRequestedModel)
	loaded.ReturnRequestedModel = false
	loadedAgain, err := snapshot.GetGroupByIDLite(t.Context(), group.ID)
	require.NoError(t, err)
	require.True(t, loadedAgain.ReturnRequestedModel)

	snapshot.storeCachedGroup(group)
	disabled, err := snapshot.GetGroupByIDLite(t.Context(), group.ID)
	require.NoError(t, err)
	require.False(t, disabled.ReturnRequestedModel)
}
