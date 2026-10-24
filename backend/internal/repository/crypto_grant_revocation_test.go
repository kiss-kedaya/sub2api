package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestCryptoGrantRevocationTombstonesAreSessionScopedAndExpire(t *testing.T) {
	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	refresh := &refreshTokenCache{rdb: rdb}
	store := &TotpCache{rdb: rdb}
	ctx := t.Context()
	userAt, sessionAt, err := store.CryptoGrantRevocationTimes(ctx, 7, "session-one")
	require.NoError(t, err)
	require.Zero(t, userAt)
	require.Zero(t, sessionAt)
	require.NoError(t, refresh.DeleteTokenFamily(ctx, "session-one"))
	userAt, sessionAt, err = store.CryptoGrantRevocationTimes(ctx, 7, "session-one")
	require.NoError(t, err)
	require.Zero(t, userAt)
	require.Positive(t, sessionAt)
	require.Equal(t, 5*time.Minute, mini.TTL(service.CryptoGrantSessionRevocationKey("session-one")))
	_, anotherSessionAt, err := store.CryptoGrantRevocationTimes(ctx, 7, "session-two")
	require.NoError(t, err)
	require.Zero(t, anotherSessionAt)
	require.NoError(t, refresh.DeleteUserRefreshTokens(ctx, 7))
	userAt, _, err = store.CryptoGrantRevocationTimes(ctx, 7, "session-two")
	require.NoError(t, err)
	require.Positive(t, userAt)
	otherUserAt, _, err := store.CryptoGrantRevocationTimes(ctx, 8, "session-two")
	require.NoError(t, err)
	require.Zero(t, otherUserAt)
	mini.FastForward(5 * time.Minute)
	userAt, sessionAt, err = store.CryptoGrantRevocationTimes(ctx, 7, "session-one")
	require.NoError(t, err)
	require.Zero(t, userAt)
	require.Zero(t, sessionAt)
}

func TestCryptoGrantRevocationIsNotWrittenDuringRefreshRotation(t *testing.T) {
	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	refresh := &refreshTokenCache{rdb: rdb}
	ctx := t.Context()
	require.NoError(t, refresh.StoreRefreshToken(ctx, "old-token-hash", &service.RefreshTokenData{UserID: 7, FamilyID: "session-one"}, time.Hour))
	require.NoError(t, refresh.DeleteRefreshToken(ctx, "old-token-hash"))
	store := &TotpCache{rdb: rdb}
	userAt, sessionAt, err := store.CryptoGrantRevocationTimes(ctx, 7, "session-one")
	require.NoError(t, err)
	require.Zero(t, userAt)
	require.Zero(t, sessionAt)
}

func TestCryptoGrantRevocationReaderRejectsCorruptionAndRedisFailure(t *testing.T) {
	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	store := &TotpCache{rdb: rdb}
	ctx := t.Context()
	for _, corrupted := range []string{"0", "-1", "not-a-timestamp", "12 trailing", "0123"} {
		mini.Set(service.CryptoGrantUserRevocationKey(7), corrupted)
		_, _, err := store.CryptoGrantRevocationTimes(ctx, 7, "session")
		require.Error(t, err)
	}
	mini.Close()
	_, _, err := store.CryptoGrantRevocationTimes(ctx, 7, "session")
	require.Error(t, err)
}
