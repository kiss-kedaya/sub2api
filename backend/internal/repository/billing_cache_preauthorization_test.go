package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

const (
	LiveBalanceOutcomeInsufficient = service.LiveBalanceOutcomeInsufficient
	LiveBalanceOutcomeApplied      = service.LiveBalanceOutcomeApplied
	LiveBalanceOutcomeIdempotent   = service.LiveBalanceOutcomeIdempotent
	LiveBalanceOutcomeConflict     = service.LiveBalanceOutcomeConflict
	LiveBalanceOutcomeNotFound     = service.LiveBalanceOutcomeNotFound
	LiveBalanceAttemptNone         = service.LiveBalanceAttemptNone
	LiveBalanceAttemptAuthorized   = service.LiveBalanceAttemptAuthorized
	LiveBalanceAttemptFinalized    = service.LiveBalanceAttemptFinalized
	LiveBalanceAttemptRefunded     = service.LiveBalanceAttemptRefunded
)

func newLiveBalanceTestCache(t *testing.T) (*billingCache, *redis.Client) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	return &billingCache{rdb: client}, client
}

func TestLiveBalanceKeysUseOneRedisSlotAndHashedAttemptID(t *testing.T) {
	digest := sha256.Sum256([]byte("request:with arbitrary spaces/and?symbols"))
	require.Equal(t, "billing:live_wallet:{42}", liveBalanceWalletKey(42))
	require.Equal(t, "billing:live_wallet:{42}:watermark", liveBalanceWatermarkKey(42))
	require.Equal(t,
		"billing:live_wallet:{42}:attempt:"+hex.EncodeToString(digest[:]),
		liveBalanceAttemptKey(42, "request:with arbitrary spaces/and?symbols"))
	require.Equal(t,
		"billing:live_wallet:{42}:adjust:"+hex.EncodeToString(digest[:]),
		liveBalanceAdjustmentKey(42, "request:with arbitrary spaces/and?symbols"))
}

func TestAuthorizeExistingLiveBalanceNeverInitializesMissingWallet(t *testing.T) {
	cache, client := newLiveBalanceTestCache(t)
	ctx := context.Background()

	missing, err := cache.AuthorizeExistingLiveBalance(ctx, 57, "request", 1)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeNotFound, missing.Outcome)
	require.Equal(t, int64(0), client.Exists(ctx,
		liveBalanceWalletKey(57),
		liveBalanceWatermarkKey(57),
		liveBalanceAttemptKey(57, "request"),
	).Val())

	unsafe, err := cache.AuthorizeLiveBalanceAtWatermarkIfSafe(ctx, 57, "request", 10, 9, 1, false)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeNotFound, unsafe.Outcome)
	require.Equal(t, int64(0), client.Exists(ctx, liveBalanceWalletKey(57)).Val())
}

func TestAuthorizeExistingLiveBalanceUsesRedisFastPath(t *testing.T) {
	cache, client := newLiveBalanceTestCache(t)
	ctx := context.Background()

	_, err := cache.AuthorizeLiveBalanceAtWatermark(ctx, 58, "first", 10, 21, 1)
	require.NoError(t, err)
	result, err := cache.AuthorizeExistingLiveBalance(ctx, 58, "second", 2)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeApplied, result.Outcome)
	require.Equal(t, 7.0, result.AvailableBalance)
	require.Equal(t, "21", client.Get(ctx, liveBalanceWatermarkKey(58)).Val())
}

func TestAdjustLiveBalanceAtWatermarkAppliesOnlyExactSuccessor(t *testing.T) {
	cache, client := newLiveBalanceTestCache(t)
	ctx := context.Background()

	_, err := cache.AuthorizeLiveBalance(ctx, 51, "request", 10, 2)
	require.NoError(t, err)

	first, err := cache.AdjustLiveBalanceAtWatermark(ctx, 51, "outbox:17", 17, 0, 500000000)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeApplied, first.Outcome)
	require.Equal(t, 13.0, first.AvailableBalance)
	require.Equal(t, "17", client.Get(ctx, liveBalanceWatermarkKey(51)).Val())

	replay, err := cache.AdjustLiveBalanceAtWatermark(ctx, 51, "outbox:17", 17, 0, 500000000)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeIdempotent, replay.Outcome)
	require.Equal(t, 13.0, replay.AvailableBalance)

	gap, err := cache.AdjustLiveBalanceAtWatermark(ctx, 51, "outbox:30", 30, 11, -100000000)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeConflict, gap.Outcome)
	require.Equal(t, 13.0, gap.AvailableBalance)

	second, err := cache.AdjustLiveBalanceAtWatermark(ctx, 51, "outbox:23", 23, 17, -100000000)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeApplied, second.Outcome)
	require.Equal(t, 12.0, second.AvailableBalance)
	require.Equal(t, "23", client.Get(ctx, liveBalanceWatermarkKey(51)).Val())
}

func TestAdjustLiveBalanceAtWatermarkSkipsSnapshotIncludedEvent(t *testing.T) {
	cache, client := newLiveBalanceTestCache(t)
	ctx := context.Background()

	_, err := cache.AuthorizeLiveBalanceAtWatermark(ctx, 52, "request", 8, 42, 1)
	require.NoError(t, err)

	result, err := cache.AdjustLiveBalanceAtWatermark(ctx, 52, "outbox:42", 42, 17, 300000000)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeIdempotent, result.Outcome)
	require.Equal(t, 7.0, result.AvailableBalance)
	require.Zero(t, client.Exists(ctx, liveBalanceAdjustmentKey(52, "outbox:42")).Val(),
		"the wallet watermark is sufficient replay evidence; snapshot-included events need no marker key")
}

func TestAdjustLiveBalanceAtWatermarkFailsClosedWithoutInitializationWatermark(t *testing.T) {
	cache, client := newLiveBalanceTestCache(t)
	ctx := context.Background()

	_, err := cache.AuthorizeLiveBalance(ctx, 53, "request", 8, 1)
	require.NoError(t, err)
	require.NoError(t, client.Del(ctx, liveBalanceWatermarkKey(53)).Err())
	result, err := cache.AdjustLiveBalanceAtWatermark(ctx, 53, "outbox:7", 7, 0, 300000000)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeConflict, result.Outcome)
	require.Equal(t, 7.0, result.AvailableBalance)
}

func TestAdjustLiveBalanceAtWatermarkMissingWalletWaitsForInitialization(t *testing.T) {
	cache, _ := newLiveBalanceTestCache(t)
	ctx := context.Background()

	result, err := cache.AdjustLiveBalanceAtWatermark(ctx, 54, "outbox:9", 9, 0, 200000000)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeNotFound, result.Outcome)

	// If the DB snapshot preceded event 9, initialization starts at predecessor
	// 0 and the still-pending event is applied exactly once afterwards.
	_, err = cache.AuthorizeLiveBalanceAtWatermark(ctx, 54, "request-old-snapshot", 10, 0, 1)
	require.NoError(t, err)
	replay, err := cache.AdjustLiveBalanceAtWatermark(ctx, 54, "outbox:9", 9, 0, 200000000)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeApplied, replay.Outcome)
	require.Equal(t, 11.0, replay.AvailableBalance)
}

func TestAdjustLiveBalanceAtWatermarkMissingWalletSnapshotCanIncludePendingEvent(t *testing.T) {
	cache, _ := newLiveBalanceTestCache(t)
	ctx := context.Background()

	missing, err := cache.AdjustLiveBalanceAtWatermark(ctx, 56, "outbox:9", 9, 0, 200000000)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeNotFound, missing.Outcome)

	_, err = cache.AuthorizeLiveBalanceAtWatermark(ctx, 56, "request-new-snapshot", 12, 9, 1)
	require.NoError(t, err)
	replay, err := cache.AdjustLiveBalanceAtWatermark(ctx, 56, "outbox:9", 9, 0, 200000000)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeIdempotent, replay.Outcome)
	require.Equal(t, 11.0, replay.AvailableBalance)
}

func TestInitializeLiveBalanceAtWatermarkIsAtomicAndNeverOverwrites(t *testing.T) {
	cache, client := newLiveBalanceTestCache(t)
	ctx := context.Background()

	initialized, err := cache.InitializeLiveBalanceAtWatermark(ctx, 59, 12.5, 17)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeApplied, initialized.Outcome)
	require.Equal(t, 12.5, initialized.AvailableBalance)
	require.Equal(t, "17", client.Get(ctx, liveBalanceWatermarkKey(59)).Val())

	existing, err := cache.InitializeLiveBalanceAtWatermark(ctx, 59, 99, 42)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeIdempotent, existing.Outcome)
	require.Equal(t, 12.5, existing.AvailableBalance)
	require.Equal(t, "17", client.Get(ctx, liveBalanceWatermarkKey(59)).Val())
}

func TestInitializeLiveBalanceAtWatermarkRejectsPartialState(t *testing.T) {
	cache, client := newLiveBalanceTestCache(t)
	ctx := context.Background()

	require.NoError(t, client.Set(ctx, liveBalanceWalletKey(60), 500000000, 0).Err())
	walletOnly, err := cache.InitializeLiveBalanceAtWatermark(ctx, 60, 9, 3)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeConflict, walletOnly.Outcome)
	require.Equal(t, "500000000", client.Get(ctx, liveBalanceWalletKey(60)).Val())

	require.NoError(t, client.Del(ctx, liveBalanceWalletKey(60)).Err())
	require.NoError(t, client.Set(ctx, liveBalanceWatermarkKey(60), 2, 0).Err())
	watermarkOnly, err := cache.InitializeLiveBalanceAtWatermark(ctx, 60, 9, 3)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeConflict, watermarkOnly.Outcome)
	require.Equal(t, "2", client.Get(ctx, liveBalanceWatermarkKey(60)).Val())
}

func TestAdjustLiveBalanceAtWatermarkValidatesSequence(t *testing.T) {
	cache, _ := newLiveBalanceTestCache(t)
	ctx := context.Background()

	_, err := cache.AdjustLiveBalanceAtWatermark(ctx, 55, "outbox", 0, 0, 100000000)
	require.ErrorContains(t, err, "watermark is invalid")
	_, err = cache.AdjustLiveBalanceAtWatermark(ctx, 55, "outbox", 5, 5, 100000000)
	require.ErrorContains(t, err, "watermark is invalid")
	_, err = cache.AdjustLiveBalanceAtWatermark(ctx, 55, "outbox", 5, 0, 0)
	require.ErrorContains(t, err, "balance adjustment")
}

func TestAdjustLiveBalancePreservesActiveHoldAndIsIdempotent(t *testing.T) {
	cache, _ := newLiveBalanceTestCache(t)
	ctx := context.Background()

	_, err := cache.AuthorizeLiveBalance(ctx, 41, "request", 10, 2)
	require.NoError(t, err)

	adjusted, err := cache.AdjustLiveBalance(ctx, 41, "redeem:123", 5)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeApplied, adjusted.Outcome)
	require.Equal(t, 13.0, adjusted.AvailableBalance)

	replay, err := cache.AdjustLiveBalance(ctx, 41, "redeem:123", 5)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeIdempotent, replay.Outcome)
	require.Equal(t, 13.0, replay.AvailableBalance)

	conflict, err := cache.AdjustLiveBalance(ctx, 41, "redeem:123", 4)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeConflict, conflict.Outcome)
	require.Equal(t, 13.0, conflict.AvailableBalance)

	finalized, err := cache.FinalizeLiveBalance(ctx, 41, "request", 1)
	require.NoError(t, err)
	require.Equal(t, 14.0, finalized.AvailableBalance)
}

func TestAdjustLiveBalanceMissingWalletIsTerminalNoOp(t *testing.T) {
	cache, _ := newLiveBalanceTestCache(t)
	ctx := context.Background()

	missing, err := cache.AdjustLiveBalance(ctx, 42, "promo:1:42", 3)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeNotFound, missing.Outcome)
	_, exists, err := cache.GetLiveBalance(ctx, 42)
	require.NoError(t, err)
	require.False(t, exists)

	// The first authorization reads the already-adjusted database value. A
	// delayed duplicate event must not add the grant a second time.
	_, err = cache.AuthorizeLiveBalance(ctx, 42, "request", 8, 1)
	require.NoError(t, err)
	replay, err := cache.AdjustLiveBalance(ctx, 42, "promo:1:42", 3)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeIdempotent, replay.Outcome)
	require.Equal(t, 7.0, replay.AvailableBalance)
}

func TestAdjustLiveBalanceSupportsCommittedDebit(t *testing.T) {
	cache, _ := newLiveBalanceTestCache(t)
	ctx := context.Background()

	_, err := cache.AuthorizeLiveBalance(ctx, 43, "request", 10, 1)
	require.NoError(t, err)
	adjusted, err := cache.AdjustLiveBalance(ctx, 43, "refund:7:deduct", -2.5)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeApplied, adjusted.Outcome)
	require.Equal(t, 6.5, adjusted.AvailableBalance)
}

func TestAuthorizeLiveBalanceInitializesPersistentWalletOnce(t *testing.T) {
	cache, client := newLiveBalanceTestCache(t)
	ctx := context.Background()

	first, err := cache.AuthorizeLiveBalance(ctx, 42, "request-1", 10, 2)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeApplied, first.Outcome)
	require.Equal(t, LiveBalanceAttemptAuthorized, first.State)
	require.Equal(t, 8.0, first.AvailableBalance)
	require.Equal(t, 2.0, first.ReservedAmount)
	require.Equal(t, "0", client.Get(ctx, liveBalanceWatermarkKey(42)).Val())
	activeTTL, err := client.TTL(ctx, liveBalanceAttemptKey(42, "request-1")).Result()
	require.NoError(t, err)
	require.Equal(t, time.Duration(-1), activeTTL)

	// A later, stale DB fallback is ignored because the live wallet never expires.
	second, err := cache.AuthorizeLiveBalance(ctx, 42, "request-2", 100, 1)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeApplied, second.Outcome)
	require.Equal(t, 7.0, second.AvailableBalance)

	balance, exists, err := cache.GetLiveBalance(ctx, 42)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, 7.0, balance)

	ttl, err := client.TTL(ctx, liveBalanceWalletKey(42)).Result()
	require.NoError(t, err)
	require.Equal(t, time.Duration(-1), ttl)
	watermarkTTL, err := client.TTL(ctx, liveBalanceWatermarkKey(42)).Result()
	require.NoError(t, err)
	require.Equal(t, time.Duration(-1), watermarkTTL)
}

func TestAuthorizeLiveBalanceIsIdempotentAndDetectsConflict(t *testing.T) {
	cache, client := newLiveBalanceTestCache(t)
	ctx := context.Background()

	first, err := cache.AuthorizeLiveBalance(ctx, 7, "same-attempt", 5, 1.25)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeApplied, first.Outcome)

	replay, err := cache.AuthorizeLiveBalance(ctx, 7, "same-attempt", 999, 1.25)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeIdempotent, replay.Outcome)
	require.Equal(t, 3.75, replay.AvailableBalance)
	require.Equal(t, 1.25, replay.ReservedAmount)

	conflict, err := cache.AuthorizeLiveBalance(ctx, 7, "same-attempt", 5, 1.5)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeConflict, conflict.Outcome)
	require.Equal(t, 3.75, conflict.AvailableBalance)

	// Partial Redis loss must never rehydrate a stale DB balance over an
	// existing hold. The durable ledger must reconcile this conflict.
	require.NoError(t, client.Del(ctx, liveBalanceWalletKey(7)).Err())
	inconsistent, err := cache.AuthorizeLiveBalance(ctx, 7, "same-attempt", 999, 1.25)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeConflict, inconsistent.Outcome)
	require.Equal(t, LiveBalanceAttemptAuthorized, inconsistent.State)
	require.Zero(t, inconsistent.AvailableBalance)
	_, exists, err := cache.GetLiveBalance(ctx, 7)
	require.NoError(t, err)
	require.False(t, exists)
}

func TestAuthorizeLiveBalanceInsufficientIsAtomicAndRetryable(t *testing.T) {
	cache, client := newLiveBalanceTestCache(t)
	ctx := context.Background()

	result, err := cache.AuthorizeLiveBalance(ctx, 8, "request-low", 0.50, 0.75)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeInsufficient, result.Outcome)
	require.Equal(t, LiveBalanceAttemptNone, result.State)
	require.Equal(t, 0.50, result.AvailableBalance)

	exists, err := client.Exists(ctx, liveBalanceAttemptKey(8, "request-low")).Result()
	require.NoError(t, err)
	require.Zero(t, exists)

	require.NoError(t, client.IncrBy(ctx, liveBalanceWalletKey(8), 50_000_000).Err())
	retry, err := cache.AuthorizeLiveBalance(ctx, 8, "request-low", 0.50, 0.75)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeApplied, retry.Outcome)
	require.Equal(t, 0.25, retry.AvailableBalance)
}

func TestTopUpLiveBalanceUsesCumulativeTargetForIdempotency(t *testing.T) {
	cache, client := newLiveBalanceTestCache(t)
	ctx := context.Background()

	_, err := cache.AuthorizeLiveBalance(ctx, 9, "request-stream", 3, 1)
	require.NoError(t, err)
	require.NoError(t, client.Expire(ctx, liveBalanceWalletKey(9), time.Minute).Err())

	topUp, err := cache.TopUpLiveBalance(ctx, 9, "request-stream", 1.50)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeApplied, topUp.Outcome)
	require.Equal(t, 1.50, topUp.AvailableBalance)
	require.Equal(t, 1.50, topUp.ReservedAmount)
	walletTTL, err := client.TTL(ctx, liveBalanceWalletKey(9)).Result()
	require.NoError(t, err)
	require.Equal(t, time.Duration(-1), walletTTL)

	replay, err := cache.TopUpLiveBalance(ctx, 9, "request-stream", 1.50)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeIdempotent, replay.Outcome)
	require.Equal(t, 1.50, replay.AvailableBalance)

	insufficient, err := cache.TopUpLiveBalance(ctx, 9, "request-stream", 4)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeInsufficient, insufficient.Outcome)
	require.Equal(t, 1.50, insufficient.AvailableBalance)
	require.Equal(t, 1.50, insufficient.ReservedAmount)

	backwards, err := cache.TopUpLiveBalance(ctx, 9, "request-stream", 1.25)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeConflict, backwards.Outcome)
}

func TestFinalizeLiveBalanceSettlesOnceAndRetainsTerminalMarker(t *testing.T) {
	cache, client := newLiveBalanceTestCache(t)
	ctx := context.Background()

	_, err := cache.AuthorizeLiveBalance(ctx, 10, "request-final", 5, 2)
	require.NoError(t, err)

	finalized, err := cache.FinalizeLiveBalance(ctx, 10, "request-final", 1.25)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeApplied, finalized.Outcome)
	require.Equal(t, LiveBalanceAttemptFinalized, finalized.State)
	require.Equal(t, 3.75, finalized.AvailableBalance)
	require.Equal(t, 2.0, finalized.ReservedAmount)
	require.Equal(t, 1.25, finalized.ActualAmount)

	replay, err := cache.FinalizeLiveBalance(ctx, 10, "request-final", 1.25)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeIdempotent, replay.Outcome)
	require.Equal(t, 3.75, replay.AvailableBalance)

	conflict, err := cache.FinalizeLiveBalance(ctx, 10, "request-final", 1.50)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeConflict, conflict.Outcome)
	require.Equal(t, 3.75, conflict.AvailableBalance)

	ttl, err := client.TTL(ctx, liveBalanceAttemptKey(10, "request-final")).Result()
	require.NoError(t, err)
	require.Greater(t, ttl, time.Duration(0))
	require.LessOrEqual(t, ttl, liveBalanceTerminalAttemptTTL)
}

func TestFinalizeLiveBalanceDebitsBoundedOverageEvenBelowZero(t *testing.T) {
	cache, _ := newLiveBalanceTestCache(t)
	ctx := context.Background()

	_, err := cache.AuthorizeLiveBalance(ctx, 11, "request-overage", 1.10, 1)
	require.NoError(t, err)
	result, err := cache.FinalizeLiveBalance(ctx, 11, "request-overage", 1.25)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeApplied, result.Outcome)
	require.Equal(t, -0.15, result.AvailableBalance)
	require.Equal(t, 1.25, result.ActualAmount)
}

func TestRefundLiveBalanceReleasesHoldOnce(t *testing.T) {
	cache, _ := newLiveBalanceTestCache(t)
	ctx := context.Background()

	_, err := cache.AuthorizeLiveBalance(ctx, 12, "request-refund", 2, 1.25)
	require.NoError(t, err)

	refunded, err := cache.RefundLiveBalance(ctx, 12, "request-refund")
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeApplied, refunded.Outcome)
	require.Equal(t, LiveBalanceAttemptRefunded, refunded.State)
	require.Equal(t, 2.0, refunded.AvailableBalance)

	replay, err := cache.RefundLiveBalance(ctx, 12, "request-refund")
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeIdempotent, replay.Outcome)
	require.Equal(t, 2.0, replay.AvailableBalance)

	finalize, err := cache.FinalizeLiveBalance(ctx, 12, "request-refund", 1)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeConflict, finalize.Outcome)
	require.Equal(t, LiveBalanceAttemptRefunded, finalize.State)
}

func TestLiveBalanceMoneyQuantization(t *testing.T) {
	cache, _ := newLiveBalanceTestCache(t)
	ctx := context.Background()

	authorized, err := cache.AuthorizeLiveBalance(ctx, 13, "request-round", 1, 0.000000001)
	require.NoError(t, err)
	require.Equal(t, 0.99999999, authorized.AvailableBalance)
	require.Equal(t, 0.00000001, authorized.ReservedAmount)

	finalized, err := cache.FinalizeLiveBalance(ctx, 13, "request-round", 0.000000005)
	require.NoError(t, err)
	require.Equal(t, 0.99999999, finalized.AvailableBalance)
	require.Equal(t, 0.00000001, finalized.ActualAmount)

	_, err = cache.AuthorizeLiveBalance(ctx, 14, "bad", math.NaN(), 1)
	require.ErrorIs(t, err, errInvalidLiveBalanceMoney)
	_, err = cache.AuthorizeLiveBalance(ctx, 14, "bad", 1, -1)
	require.ErrorIs(t, err, errInvalidLiveBalanceMoney)
}

func TestLiveBalanceConcurrentAuthorizationsCannotOverspend(t *testing.T) {
	cache, _ := newLiveBalanceTestCache(t)
	ctx := context.Background()

	const attempts = 100
	var applied atomic.Int64
	var insufficient atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			result, err := cache.AuthorizeLiveBalance(ctx, 15, "parallel-"+strconv.Itoa(i), 0.50, 0.01)
			require.NoError(t, err)
			switch result.Outcome {
			case LiveBalanceOutcomeApplied:
				applied.Add(1)
			case LiveBalanceOutcomeInsufficient:
				insufficient.Add(1)
			default:
				t.Errorf("unexpected outcome %v", result.Outcome)
			}
		}(i)
	}
	wg.Wait()

	require.Equal(t, int64(50), applied.Load())
	require.Equal(t, int64(50), insufficient.Load())
	balance, exists, err := cache.GetLiveBalance(ctx, 15)
	require.NoError(t, err)
	require.True(t, exists)
	require.Zero(t, balance)
}

func TestGetLiveBalanceMissingAndValidation(t *testing.T) {
	cache, _ := newLiveBalanceTestCache(t)
	ctx := context.Background()

	balance, exists, err := cache.GetLiveBalance(ctx, 99)
	require.NoError(t, err)
	require.False(t, exists)
	require.Zero(t, balance)

	_, _, err = cache.GetLiveBalance(ctx, 0)
	require.ErrorIs(t, err, errInvalidLiveBalanceUserID)
	_, err = cache.AuthorizeLiveBalance(ctx, 1, "", 1, 0.1)
	require.ErrorIs(t, err, errInvalidLiveBalanceAttemptID)

	missing, err := cache.TopUpLiveBalance(ctx, 1, "does-not-exist", 1)
	require.NoError(t, err)
	require.Equal(t, LiveBalanceOutcomeNotFound, missing.Outcome)
}
