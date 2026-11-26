package repository

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// refresh token 存储的原子能力（service.RefreshTokenAtomicStore）：消费旧令牌与占宽限重发名额
// 都是单个 Lua 脚本，撤销之后的写入直接失败，不会把墓碑写回去。

func newAtomicRefreshStore(t *testing.T) (*miniredis.Miniredis, service.RefreshTokenCache, service.RefreshTokenAtomicStore) {
	t.Helper()
	mini := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cache := NewRefreshTokenCache(rdb)
	atomic, ok := cache.(service.RefreshTokenAtomicStore)
	require.True(t, ok, "the Redis implementation must provide the atomic store")
	return mini, cache, atomic
}

// 接近 2^63 的 token_version：任何经过 float64 的解析/重写都会让它失真。
const precisionTokenVersion = int64(4302900343878336793)

func storeFixtureToken(t *testing.T, cache service.RefreshTokenCache, hash string) *service.RefreshTokenData {
	t.Helper()
	now := time.Now()
	data := &service.RefreshTokenData{
		UserID: 7, TokenVersion: precisionTokenVersion, FamilyID: "fam-1",
		BindingHash: "bh", BindingCoarse: "chrome|windows|4|abcd1234",
		SessionStartedAt: now, CreatedAt: now, ExpiresAt: now.Add(14 * 24 * time.Hour),
	}
	require.NoError(t, cache.StoreRefreshToken(t.Context(), hash, data, 14*24*time.Hour))
	return data
}

func tombstoneOf(data *service.RefreshTokenData) *service.RefreshTokenData {
	tomb := *data
	tomb.Consumed = true
	tomb.ConsumedAt = time.Now()
	tomb.BindingHash = "consumer-hash"
	return &tomb
}

func readRecord(t *testing.T, mini *miniredis.Miniredis, hash string) service.RefreshTokenData {
	t.Helper()
	raw, err := mini.Get(refreshTokenKey(hash))
	require.NoError(t, err)
	var data service.RefreshTokenData
	require.NoError(t, json.Unmarshal([]byte(raw), &data))
	return data
}

func TestRefreshTokenAtomicConsumeOutcomes(t *testing.T) {
	mini, cache, atomic := newAtomicRefreshStore(t)
	ctx := t.Context()

	// 记录不存在（撤销之后 / 已过期）：失败，什么都不写。
	outcome, err := atomic.ConsumeRefreshToken(ctx, "ghost", tombstoneOf(&service.RefreshTokenData{UserID: 7}), time.Hour)
	require.NoError(t, err)
	require.Equal(t, service.RefreshConsumeMissing, outcome)
	require.False(t, mini.Exists(refreshTokenKey("ghost")), "a consume after revocation must not write the tombstone back")

	data := storeFixtureToken(t, cache, "tok")
	outcome, err = atomic.ConsumeRefreshToken(ctx, "tok", tombstoneOf(data), time.Hour)
	require.NoError(t, err)
	require.Equal(t, service.RefreshConsumeWon, outcome)
	rec := readRecord(t, mini, "tok")
	require.True(t, rec.Consumed)
	require.Equal(t, "consumer-hash", rec.BindingHash, "the tombstone carries the consumer's fingerprint")
	require.Equal(t, precisionTokenVersion, rec.TokenVersion, "the token version must survive exactly")
	require.Equal(t, time.Hour, mini.TTL(refreshTokenKey("tok")), "the tombstone TTL is the one that was asked for")

	// 第二个并发请求：已经被消费。
	outcome, err = atomic.ConsumeRefreshToken(ctx, "tok", tombstoneOf(data), time.Hour)
	require.NoError(t, err)
	require.Equal(t, service.RefreshConsumeAlready, outcome)

	// 撤销（删除记录）之后：失败。
	require.NoError(t, cache.DeleteRefreshToken(ctx, "tok"))
	outcome, err = atomic.ConsumeRefreshToken(ctx, "tok", tombstoneOf(data), time.Hour)
	require.NoError(t, err)
	require.Equal(t, service.RefreshConsumeMissing, outcome)
	require.False(t, mini.Exists(refreshTokenKey("tok")))
}

func TestRefreshTokenAtomicReserveLeewayReissueOutcomes(t *testing.T) {
	mini, cache, atomic := newAtomicRefreshStore(t)
	ctx := t.Context()

	outcome, n, err := atomic.ReserveLeewayReissue(ctx, "ghost", 3)
	require.NoError(t, err)
	require.Equal(t, service.RefreshLeewayMissing, outcome)
	require.Zero(t, n)
	require.False(t, mini.Exists(refreshTokenKey("ghost")), "reserving on a revoked token must not create anything")

	data := storeFixtureToken(t, cache, "tok")
	outcome, _, err = atomic.ReserveLeewayReissue(ctx, "tok", 3)
	require.NoError(t, err)
	require.Equal(t, service.RefreshLeewayNotConsumed, outcome, "a live token is not a tombstone")

	won, err := atomic.ConsumeRefreshToken(ctx, "tok", tombstoneOf(data), 90*time.Second)
	require.NoError(t, err)
	require.Equal(t, service.RefreshConsumeWon, won)

	for want := 1; want <= 3; want++ {
		outcome, n, err = atomic.ReserveLeewayReissue(ctx, "tok", 3)
		require.NoError(t, err)
		require.Equal(t, service.RefreshLeewayReserved, outcome)
		require.Equal(t, want, n)
		rec := readRecord(t, mini, "tok")
		require.Equal(t, want, rec.LeewayReissues)
		require.True(t, rec.Consumed)
		require.Equal(t, precisionTokenVersion, rec.TokenVersion, "string surgery must not touch other numbers")
		require.Equal(t, "consumer-hash", rec.BindingHash)
		require.LessOrEqual(t, mini.TTL(refreshTokenKey("tok")), 90*time.Second, "the tombstone TTL is never extended")
		require.Greater(t, mini.TTL(refreshTokenKey("tok")), 80*time.Second, "and never reset either")
	}

	outcome, n, err = atomic.ReserveLeewayReissue(ctx, "tok", 3)
	require.NoError(t, err)
	require.Equal(t, service.RefreshLeewayCapped, outcome)
	require.Equal(t, 3, n)
	require.Equal(t, 3, readRecord(t, mini, "tok").LeewayReissues, "a refused reservation does not move the counter")

	// 撤销之后：失败，什么都不写。
	require.NoError(t, cache.DeleteRefreshToken(ctx, "tok"))
	outcome, _, err = atomic.ReserveLeewayReissue(ctx, "tok", 3)
	require.NoError(t, err)
	require.Equal(t, service.RefreshLeewayMissing, outcome)
	require.False(t, mini.Exists(refreshTokenKey("tok")))
}

// 墓碑只剩不到 1ms（PTTL=0）时占名额：写回时仍带过期时间，不会留下永不过期的死标记；
// 带不带 TTL 的 SET 会清掉原有的 TTL，所以脚本必须 PX max(ttl,1) 写回。
func TestRefreshTokenAtomicReserveNeverLeavesAnImmortalTombstone(t *testing.T) {
	mini, cache, atomic := newAtomicRefreshStore(t)
	ctx := t.Context()
	data := storeFixtureToken(t, cache, "tok")
	won, err := atomic.ConsumeRefreshToken(ctx, "tok", tombstoneOf(data), time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.RefreshConsumeWon, won)

	key := refreshTokenKey("tok")
	mini.SetTTL(key, 500*time.Microsecond) // PTTL 取整后是 0：墓碑即将过期
	outcome, n, err := atomic.ReserveLeewayReissue(ctx, "tok", 3)
	require.NoError(t, err)
	require.Equal(t, service.RefreshLeewayReserved, outcome)
	require.Equal(t, 1, n)
	require.Positive(t, mini.TTL(key), "the tombstone must still carry an expiry after the reservation")

	mini.FastForward(10 * time.Millisecond)
	require.False(t, mini.Exists(key), "and it expires instead of living forever")
}

// 并发占名额：不论多少个请求同时来，成功的恰好是 max 个。
func TestRefreshTokenAtomicReserveIsAtomicUnderConcurrency(t *testing.T) {
	_, cache, atomic := newAtomicRefreshStore(t)
	ctx := t.Context()
	data := storeFixtureToken(t, cache, "tok")
	won, err := atomic.ConsumeRefreshToken(ctx, "tok", tombstoneOf(data), time.Minute)
	require.NoError(t, err)
	require.Equal(t, service.RefreshConsumeWon, won)

	const parallel = 32
	results := make(chan service.RefreshLeewayOutcome, parallel)
	for i := 0; i < parallel; i++ {
		go func() {
			outcome, _, reserveErr := atomic.ReserveLeewayReissue(ctx, "tok", 3)
			if reserveErr != nil {
				outcome = service.RefreshLeewayMissing
			}
			results <- outcome
		}()
	}
	reserved, capped := 0, 0
	for i := 0; i < parallel; i++ {
		switch <-results {
		case service.RefreshLeewayReserved:
			reserved++
		case service.RefreshLeewayCapped:
			capped++
		}
	}
	require.Equal(t, 3, reserved)
	require.Equal(t, parallel-3, capped)
}

// 并发消费：恰好一个赢。
func TestRefreshTokenAtomicConsumeHasExactlyOneWinner(t *testing.T) {
	_, cache, atomic := newAtomicRefreshStore(t)
	ctx := t.Context()
	data := storeFixtureToken(t, cache, "tok")

	const parallel = 32
	results := make(chan service.RefreshConsumeOutcome, parallel)
	for i := 0; i < parallel; i++ {
		go func() {
			outcome, consumeErr := atomic.ConsumeRefreshToken(ctx, "tok", tombstoneOf(data), time.Minute)
			if consumeErr != nil {
				outcome = service.RefreshConsumeMissing
			}
			results <- outcome
		}()
	}
	won, already := 0, 0
	for i := 0; i < parallel; i++ {
		switch <-results {
		case service.RefreshConsumeWon:
			won++
		case service.RefreshConsumeAlready:
			already++
		}
	}
	require.Equal(t, 1, won)
	require.Equal(t, parallel-1, already)
}
