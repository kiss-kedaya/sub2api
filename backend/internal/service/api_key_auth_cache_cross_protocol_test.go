package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func crossProtocolSnapshotPayload(t *testing.T, enabled bool) []byte {
	t.Helper()
	groupID := int64(51)
	apiKey := &APIKey{
		ID: 83, UserID: 41, GroupID: &groupID, Key: "sk-cross-roundtrip", Status: StatusActive,
		AnthropicCacheTTLMode: AnthropicCacheTTLMode1h,
		User:                  &User{ID: 41, Status: StatusActive},
		Group: &Group{
			ID: groupID, Name: "cross-roundtrip", Platform: PlatformAnthropic, Status: StatusActive,
			Hydrated: true, CrossProtocolConversionEnabled: enabled,
		},
	}
	payload, err := json.Marshal(&APIKeyAuthCacheEntry{Snapshot: (&APIKeyService{}).snapshotFromAPIKey(context.Background(), apiKey)})
	require.NoError(t, err)
	return payload
}

func materializeAuthSnapshot(t *testing.T, payload []byte) *APIKey {
	t.Helper()
	var cached APIKeyAuthCacheEntry
	require.NoError(t, json.Unmarshal(payload, &cached))
	materialized, used, err := (&APIKeyService{}).applyAuthCacheEntry("sk-cross-roundtrip", &cached)
	require.NoError(t, err)
	require.True(t, used, "same-version snapshots must be served from cache, not evicted")
	require.NotNil(t, materialized.Group)
	return materialized
}

func TestAPIKeyAuthSnapshotCrossProtocolRoundtrip(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		materialized := materializeAuthSnapshot(t, crossProtocolSnapshotPayload(t, enabled))
		require.Equal(t, enabled, materialized.Group.CrossProtocolConversionEnabled)
		require.Equal(t, AnthropicCacheTTLMode1h, materialized.AnthropicCacheTTLMode)
	}
}

// 灰度期间 Redis 里的快照可能由不认识新字段的旧版进程写入。这类快照必须按"允许"
// 解析（与迁移 251 把存量分组回填为 true 一致），否则新版会把原本可用的跨协议
// 请求误判为 400；TTL 字段缺失时按 inherit 解析。
func TestAPIKeyAuthSnapshotWrittenByOlderBinaryAllowsCrossProtocol(t *testing.T) {
	payload := crossProtocolSnapshotPayload(t, false)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(payload, &raw))
	snapshot, ok := raw["snapshot"].(map[string]any)
	require.True(t, ok)
	group, ok := snapshot["group"].(map[string]any)
	require.True(t, ok)
	delete(snapshot, "anthropic_cache_ttl_mode")
	delete(group, "cross_protocol_conversion_enabled")
	legacy, err := json.Marshal(raw)
	require.NoError(t, err)

	materialized := materializeAuthSnapshot(t, legacy)
	require.True(t, materialized.Group.CrossProtocolConversionEnabled)
	require.True(t, CrossProtocolConversionAllowed(materialized))
	require.Equal(t, AnthropicCacheTTLModeInherit, materialized.AnthropicCacheTTLMode)
}
