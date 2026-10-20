package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestCryptoTotpStoreUsesAtomicCountersAndOwnerBoundLocks(t *testing.T) {
	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	store := &TotpCache{rdb: rdb}
	ctx := context.Background()

	first, err := store.IncrementCryptoAttempt(ctx, "crypto:totp:{7}:attempts:verify", time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, first)
	second, err := store.IncrementCryptoAttempt(ctx, "crypto:totp:{7}:attempts:verify", time.Minute)
	require.NoError(t, err)
	require.Equal(t, 2, second)
	require.Equal(t, time.Minute, mini.TTL("crypto:totp:{7}:attempts:verify"))

	const lockKey = "crypto:totp:{7}:setup-lock"
	acquired, err := store.SetCryptoValueIfAbsent(ctx, lockKey, "owner-a", 30*time.Second)
	require.NoError(t, err)
	require.True(t, acquired)
	acquired, err = store.SetCryptoValueIfAbsent(ctx, lockKey, "owner-b", 30*time.Second)
	require.NoError(t, err)
	require.False(t, acquired)

	require.NoError(t, store.ReleaseCryptoValue(ctx, lockKey, "owner-b"))
	exists, err := store.CryptoValueExists(ctx, lockKey)
	require.NoError(t, err)
	require.True(t, exists, "a different request must not release the owner's lock")
	require.NoError(t, store.ReleaseCryptoValue(ctx, lockKey, "owner-a"))
	exists, err = store.CryptoValueExists(ctx, lockKey)
	require.NoError(t, err)
	require.False(t, exists)

	consumed, err := store.SetCryptoValueIfAbsent(ctx, "crypto:totp:{7}:used:code", "1", 90*time.Second)
	require.NoError(t, err)
	require.True(t, consumed)
	consumed, err = store.SetCryptoValueIfAbsent(ctx, "crypto:totp:{7}:used:code", "1", 90*time.Second)
	require.NoError(t, err)
	require.False(t, consumed)
}
