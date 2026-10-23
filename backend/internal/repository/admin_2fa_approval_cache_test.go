package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestAdmin2FAApprovalCacheExpiryBindingAndAtomicConsume(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &TotpCache{rdb: rdb}
	ctx := context.Background()
	require.NoError(t, cache.SetAdmin2FAApproval(ctx, 41, "digest-one", 10*time.Minute))
	consumed, err := cache.ConsumeAdmin2FAApproval(ctx, 42, "digest-one")
	require.NoError(t, err)
	require.False(t, consumed)
	consumed, err = cache.ConsumeAdmin2FAApproval(ctx, 41, "wrong-digest")
	require.NoError(t, err)
	require.False(t, consumed)
	stored, err := server.Get("totp:admin-approval:{41}")
	require.NoError(t, err)
	require.Equal(t, "digest-one", stored)
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := cache.ConsumeAdmin2FAApproval(ctx, 41, "digest-one")
			if err == nil && ok {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, winners.Load())
	require.NoError(t, cache.SetAdmin2FAApproval(ctx, 41, "digest-two", 10*time.Minute))
	server.FastForward(10 * time.Minute)
	consumed, err = cache.ConsumeAdmin2FAApproval(ctx, 41, "digest-two")
	require.NoError(t, err)
	require.False(t, consumed)
}

func TestAdmin2FAApprovalAttemptWindowDoesNotExtendWithFailures(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := &TotpCache{rdb: rdb}
	ctx := context.Background()
	key := "totp:admin-approval:{41}:attempts"
	n, err := cache.IncrementCryptoAttempt(ctx, key, 10*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	server.FastForward(5 * time.Minute)
	n, err = cache.IncrementCryptoAttempt(ctx, key, 10*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Equal(t, 5*time.Minute, server.TTL(key))
	server.FastForward(5 * time.Minute)
	n, err = cache.IncrementCryptoAttempt(ctx, key, 10*time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, n)
}
