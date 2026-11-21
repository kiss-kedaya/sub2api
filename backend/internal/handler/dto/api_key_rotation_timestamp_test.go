package dto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyRotationTimestampIsOptionalAndCanonical(t *testing.T) {
	const fixture = "fixture-key-value-123456"
	key := &service.APIKey{Key: fixture}
	var before map[string]any
	require.NoError(t, json.Unmarshal([]byte(mustJSON(t, APIKeyFromService(key))), &before))
	require.NotContains(t, before, "key_rotated_at")

	rotated := time.Date(2026, 1, 2, 4, 5, 6, 123456000, time.FixedZone("fixture", 3600))
	key.KeyRotatedAt = &rotated
	var masked, first map[string]any
	require.NoError(t, json.Unmarshal([]byte(mustJSON(t, APIKeyFromService(key))), &masked))
	require.NoError(t, json.Unmarshal([]byte(mustJSON(t, APIKeySecretFromService(key))), &first))
	require.Equal(t, "2026-01-02T03:05:06.123456Z", masked["key_rotated_at"])
	require.Equal(t, masked["key_rotated_at"], first["key_rotated_at"])
	require.NotContains(t, masked, "key")
}

// 只有创建/轮换的当次回执允许带明文，且同时带脱敏字段供列表复用。
