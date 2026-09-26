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

func TestTempUnschedCache_DeleteTempUnschedIfReason(t *testing.T) {
	const reason = "scheduled_quality_check: plan=18 legacy"
	for _, tc := range []struct {
		name    string
		value   string
		state   *service.TempUnschedState
		missing bool
		deleted bool
	}{
		{name: "matching", state: &service.TempUnschedState{UntilUnix: 2000000000, ErrorMessage: reason}, deleted: true},
		{name: "different_reason", state: &service.TempUnschedState{UntilUnix: 2000000000, ErrorMessage: "upstream quota exhausted"}},
		{name: "missing", missing: true},
		{name: "invalid_json", value: `{"error_message":`},
		{name: "null", value: `null`},
		{name: "string", value: `"scheduled_quality_check: plan=18 legacy"`},
		{name: "number", value: `42`},
		{name: "boolean", value: `true`},
		{name: "array", value: `["scheduled_quality_check: plan=18 legacy"]`},
		{name: "missing_reason", value: `{"until_unix":2000000000}`},
		{name: "null_reason", value: `{"error_message":null}`},
		{name: "non_string_reason", value: `{"error_message":42}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
			t.Cleanup(func() { _ = client.Close() })
			cache, ok := NewTempUnschedCache(client).(interface {
				DeleteTempUnschedIfReason(context.Context, int64, string) (bool, error)
			})
			require.True(t, ok, "the optional method must not require extending TempUnschedCache")
			key := fmt.Sprintf("%s%d", tempUnschedPrefix, 42)
			if tc.state != nil {
				data, err := json.Marshal(tc.state)
				require.NoError(t, err)
				tc.value = string(data)
			}
			if !tc.missing {
				require.NoError(t, server.Set(key, tc.value))
				server.SetTTL(key, time.Hour)
			}
			deleted, err := cache.DeleteTempUnschedIfReason(context.Background(), 42, reason)
			require.NoError(t, err)
			require.Equal(t, tc.deleted, deleted)
			if tc.deleted || tc.missing {
				require.False(t, server.Exists(key))
			} else {
				value, err := server.Get(key)
				require.NoError(t, err)
				require.Equal(t, tc.value, value)
				require.Equal(t, time.Hour, server.TTL(key))
			}
		})
	}
}

func TestTempUnschedCache_DeleteTempUnschedIfReasonPreservesNewReason(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(fmt.Sprintf("concurrent=%t", concurrent), func(t *testing.T) {
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
			t.Cleanup(func() { _ = client.Close() })
			cache := &tempUnschedCache{rdb: client}
			ctx := context.Background()
			const oldReason = "scheduled_quality_check: plan=18 legacy"
			until := time.Now().Add(time.Hour).Unix()
			for attempt := range 32 {
				id := int64(attempt + 1)
				require.NoError(t, cache.SetTempUnsched(ctx, id, &service.TempUnschedState{UntilUnix: until, ErrorMessage: oldReason}))
				newState := &service.TempUnschedState{UntilUnix: until + 3600, ErrorMessage: "new upstream quota ban"}
				if concurrent {
					start := make(chan struct{})
					writeDone, deleteDone := make(chan error, 1), make(chan error, 1)
					go func() {
						<-start
						writeDone <- cache.SetTempUnsched(ctx, id, newState)
					}()
					go func() {
						<-start
						_, err := cache.DeleteTempUnschedIfReason(ctx, id, oldReason)
						deleteDone <- err
					}()
					close(start)
					writeErr, deleteErr := <-writeDone, <-deleteDone
					require.NoError(t, writeErr)
					require.NoError(t, deleteErr)
				} else {
					require.NoError(t, cache.SetTempUnsched(ctx, id, newState))
					deleted, err := cache.DeleteTempUnschedIfReason(ctx, id, oldReason)
					require.NoError(t, err)
					require.False(t, deleted)
				}
				state, err := cache.GetTempUnsched(ctx, id)
				require.NoError(t, err)
				require.Equal(t, newState, state, "a stale quality recovery must preserve the newer ban")
			}
		})
	}
}

func TestTempUnschedCache_DeleteTempUnschedIfReasonRedisError(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	cache := &tempUnschedCache{rdb: client}
	ctx := context.Background()
	const reason = "scheduled_quality_check: plan=18 legacy"
	require.NoError(t, cache.SetTempUnsched(ctx, 42, &service.TempUnschedState{UntilUnix: time.Now().Add(time.Hour).Unix(), ErrorMessage: reason}))
	key := fmt.Sprintf("%s%d", tempUnschedPrefix, 42)
	before, err := server.Get(key)
	require.NoError(t, err)
	server.SetError("ERR quality cache unavailable")
	deleted, err := cache.DeleteTempUnschedIfReason(ctx, 42, reason)
	require.ErrorContains(t, err, "quality cache unavailable")
	require.False(t, deleted)
	after, err := server.Get(key)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestTempUnschedCache_QualityExtensionPreservesExistingOperations(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	cache := NewTempUnschedCache(client)
	ctx := context.Background()
	until := time.Now().Add(time.Hour).Unix()
	state := &service.TempUnschedState{UntilUnix: until, ErrorMessage: "keep longer ban"}
	require.NoError(t, cache.SetTempUnsched(ctx, 42, state))
	require.NoError(t, cache.SetTempUnsched(ctx, 42, &service.TempUnschedState{UntilUnix: until - 60, ErrorMessage: "shorter ban"}))
	stored, err := cache.GetTempUnsched(ctx, 42)
	require.NoError(t, err)
	require.Equal(t, state, stored)
	require.NoError(t, cache.DeleteTempUnsched(ctx, 42))
	stored, err = cache.GetTempUnsched(ctx, 42)
	require.NoError(t, err)
	require.Nil(t, stored)
}
