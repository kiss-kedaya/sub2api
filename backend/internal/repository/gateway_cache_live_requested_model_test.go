package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestLiveCallRequestedModelCachePreservesPolicyAndControllerLease(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	cache, ok := NewGatewayCache(client).(service.LiveCallStore)
	require.True(t, ok)
	presentation, ok := cache.(service.LiveCallPresentationStore)
	require.True(t, ok)
	record := &service.LiveCallRecord{CallID: "call_model", CallHash: HashLiveCallID("call_model"),
		Model: "upstream-v3", RequestedModel: "Codex-Alias[1M]", SessionRequestedModel: "Codex-Alias[1M]",
		ReturnRequestedModel: true, Controller: service.LiveControllerPending,
		CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, cache.SaveLiveCall(t.Context(), record, time.Hour))
	claimed, err := cache.ClaimLiveController(t.Context(), record.CallHash, service.LiveControllerProxy, "owner")
	require.NoError(t, err)
	require.True(t, claimed)
	require.ErrorIs(t, presentation.UpdateLiveSessionRequestedModel(t.Context(), record.CallHash, "other", "wrong"), service.ErrLiveControllerChanged)
	require.NoError(t, presentation.UpdateLiveSessionRequestedModel(t.Context(), record.CallHash, "owner", "Gemini-Cased-v2"))
	loaded, err := cache.GetLiveCall(t.Context(), record.CallHash)
	require.NoError(t, err)
	require.Equal(t, "Codex-Alias[1M]", loaded.RequestedModel)
	require.Equal(t, "Gemini-Cased-v2", loaded.SessionRequestedModel)
	require.True(t, loaded.ReturnRequestedModel)
	require.Equal(t, record.Model, loaded.Model)
	require.Equal(t, service.LiveControllerProxy, loaded.Controller)
	require.Equal(t, "owner", loaded.ControllerOwner)
	require.Positive(t, server.TTL(liveCallKey(record.CallHash)))

	// Hashes written by the old binary have no presentation fields and remain off.
	require.NoError(t, client.HDel(t.Context(), liveCallKey(record.CallHash), "requested_model", "session_requested_model", "return_requested_model").Err())
	legacy, err := cache.GetLiveCall(t.Context(), record.CallHash)
	require.NoError(t, err)
	require.False(t, legacy.ReturnRequestedModel)
	require.Equal(t, record.Model, legacy.Model)
	require.ErrorIs(t, presentation.UpdateLiveSessionRequestedModel(t.Context(), "missing", "owner", "ignored"), service.ErrLiveControllerChanged)
	require.False(t, server.Exists(liveCallKey("missing")), "updates may not recreate expired call mappings")
}
