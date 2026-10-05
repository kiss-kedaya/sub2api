package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestFailedAffinityAtomicInvalidation(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	require.NoError(t, cache.SetSessionAccountID(ctx, 7, "s", 11, time.Hour))
	require.NoError(t, cache.InvalidateFailedSession(ctx, 7, 9, "s", 11))
	_, err := cache.GetSessionAccountID(ctx, 7, "s")
	require.Error(t, err)
	// A concurrent stale admission cannot resurrect the failed binding.
	require.NoError(t, cache.SetSessionAccountID(ctx, 7, "s", 11, time.Hour))
	_, err = cache.GetSessionAccountID(ctx, 7, "s")
	require.Error(t, err)
	require.NoError(t, cache.SetSessionAccountID(ctx, 7, "s", 12, time.Hour))
	// A late failure for 11 must not delete the newly selected 12.
	require.NoError(t, cache.InvalidateFailedSession(ctx, 7, 9, "s", 11))
	bound, err := cache.GetSessionAccountID(ctx, 7, "s")
	require.NoError(t, err)
	require.Equal(t, int64(12), bound)
	ids, err := cache.FailedUserAccounts(ctx, 7, 9)
	require.NoError(t, err)
	require.Equal(t, []int64{11}, ids)
	ids, err = cache.FailedUserAccounts(ctx, 7, 10)
	require.NoError(t, err)
	require.Empty(t, ids)
	ids, err = cache.FailedUserAccounts(ctx, 8, 9)
	require.NoError(t, err)
	require.Empty(t, ids)
	server.FastForward(61 * time.Second)
	require.NoError(t, cache.SetSessionAccountID(ctx, 7, "s", 11, time.Hour))
	bound, err = cache.GetSessionAccountID(ctx, 7, "s")
	require.NoError(t, err)
	require.Equal(t, int64(11), bound)
}

func TestFailedAffinityWithoutSessionStillAvoidsAccount(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	require.NoError(t, cache.InvalidateFailedSession(context.Background(), 7, 9, "", 11))
	ids, err := cache.FailedUserAccounts(context.Background(), 7, 9)
	require.NoError(t, err)
	require.Equal(t, []int64{11}, ids)
	require.False(t, server.Exists(buildSessionKey(7, "")+":failed:11"))
}

func TestFailedAffinityConcurrentLateErrorsPreserveReplacement(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &gatewayCache{rdb: client}
	ctx := context.Background()
	require.NoError(t, cache.InvalidateFailedSession(ctx, 7, 9, "s", 11))
	require.NoError(t, cache.SetSessionAccountID(ctx, 7, "s", 12, time.Hour))
	errors := make(chan error, 32)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errors <- cache.InvalidateFailedSession(ctx, 7, 9, "s", 11)
			errors <- cache.SetSessionAccountID(ctx, 7, "s", 11, time.Hour)
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	bound, err := cache.GetSessionAccountID(ctx, 7, "s")
	require.NoError(t, err)
	require.Equal(t, int64(12), bound)
}
