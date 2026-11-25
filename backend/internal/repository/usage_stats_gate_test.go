package repository

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newGatedUsageStatsTestCache(t *testing.T, slots int, queueTimeout time.Duration) *usageStatsCache {
	t.Helper()
	cache, _ := newUsageStatsTestCache(t)
	cache.gate = newUsageStatsGate(slots, queueTimeout)
	return cache
}

// holdUsageStatsSlot runs a user-scoped load that keeps its slot until the returned function is called
// (also called on cleanup). After it returns the slot is free again.
func holdUsageStatsSlot(t *testing.T, cache *usageStatsCache, userID int64) func() {
	t.Helper()
	holding, done, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = loadUsageStatsCached(cache, context.Background(), usageStatsCacheKey{Query: "hold", UserID: userID},
			func(context.Context) (int, error) {
				close(holding)
				<-done
				return 0, nil
			})
	}()
	<-holding
	var once sync.Once
	release := func() {
		once.Do(func() { close(done) })
		<-finished
	}
	t.Cleanup(release)
	return release
}

func bumpPeak(peak *atomic.Int64, n int64) {
	for {
		p := peak.Load()
		if n <= p || peak.CompareAndSwap(p, n) {
			return
		}
	}
}

func TestUsageStatsGateCapsConcurrentUserAggregates(t *testing.T) {
	const slots, callers = 3, 12
	cache := newGatedUsageStatsTestCache(t, slots, 10*time.Second)
	var running, peak atomic.Int64
	started := make(chan struct{}, callers)
	release := make(chan struct{})
	load := func(context.Context) (int, error) {
		bumpPeak(&peak, running.Add(1))
		started <- struct{}{}
		<-release
		running.Add(-1)
		return 1, nil
	}
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := loadUsageStatsCached(cache, context.Background(), usageStatsCacheKey{Query: "cap", UserID: int64(i + 1)}, load)
			errs <- err
		}()
	}
	for range slots {
		<-started
	}
	select {
	case <-started:
		t.Fatal("a load started although every slot was taken")
	case <-time.After(100 * time.Millisecond):
	}
	require.EqualValues(t, slots, running.Load())
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err, "queued aggregates must still be answered once a slot frees up")
	}
	require.EqualValues(t, slots, peak.Load(), "never more than the configured number on the database at once")
}

func TestUsageStatsGateTurnsAwayAfterQueueTimeoutAndForgetsIt(t *testing.T) {
	cache := newGatedUsageStatsTestCache(t, 1, 40*time.Millisecond)
	release := holdUsageStatsSlot(t, cache, 1)

	key := usageStatsCacheKey{Query: "turned_away", UserID: 2}
	var calls atomic.Int64
	load := func(context.Context) (int, error) {
		calls.Add(1)
		return 7, nil
	}
	started := time.Now()
	_, err := loadUsageStatsCached(cache, context.Background(), key, load)
	require.ErrorIs(t, err, ErrUsageStatsBusy)
	require.GreaterOrEqual(t, time.Since(started), 30*time.Millisecond, "the request queues for the configured time before giving up")
	require.Zero(t, calls.Load(), "a turned-away aggregate must never reach the database")
	require.Empty(t, cache.failures.Items(), "being turned away is not a database failure and must not be remembered")

	// The service wraps repository errors; the response must still be a plain 503 with a stable reason.
	status, body := infraerrors.ToHTTP(fmt.Errorf("get usage trend with filters: %w", err))
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Equal(t, "USAGE_STATS_BUSY", body.Reason)
	require.NotEmpty(t, body.Message)

	release()
	value, err := loadUsageStatsCached(cache, context.Background(), key, load)
	require.NoError(t, err)
	require.Equal(t, 7, value, "once a slot is free the same aggregate runs normally")
	require.EqualValues(t, 1, calls.Load())
}

func TestUsageStatsGateWithoutQueueTurnsAwayImmediately(t *testing.T) {
	cache := newGatedUsageStatsTestCache(t, 1, 0)
	holdUsageStatsSlot(t, cache, 1)
	started := time.Now()
	_, err := loadUsageStatsCached(cache, context.Background(), usageStatsCacheKey{Query: "no_queue", UserID: 2},
		func(context.Context) (int, error) { return 1, nil })
	require.ErrorIs(t, err, ErrUsageStatsBusy)
	require.Less(t, time.Since(started), 500*time.Millisecond)
}

func TestUsageStatsGateLeavesHitsAndAdminReportsAlone(t *testing.T) {
	cache := newGatedUsageStatsTestCache(t, 1, 5*time.Second)
	ctx := context.Background()
	cached := usageStatsCacheKey{Query: "cached", UserID: 5}
	_, err := loadUsageStatsCached(cache, ctx, cached, func(context.Context) (int, error) { return 11, nil })
	require.NoError(t, err)

	holdUsageStatsSlot(t, cache, 1)

	// A hit never takes a slot, so it is not stuck behind the saturated gate.
	got, err := loadUsageStatsCached(cache, ctx, cached, func(context.Context) (int, error) {
		t.Error("a cached aggregate must be served from Redis")
		return 0, nil
	})
	require.NoError(t, err)
	require.Equal(t, 11, got)

	// Admin reports and account scans (rate-limit decisions) are outside the cap, however busy users keep it.
	for _, key := range []usageStatsCacheKey{
		{Query: "admin_report"},
		{Query: "account_scan", AccountID: 9},
		{Query: "group_report", GroupID: 3, Filters: &usageFiltersFingerprint{GroupID: 3}},
	} {
		done := make(chan int, 1)
		go func() {
			v, _ := loadUsageStatsCached(cache, ctx, key, func(context.Context) (int, error) { return 22, nil })
			done <- v
		}()
		select {
		case v := <-done:
			require.Equal(t, 22, v)
		case <-time.After(2 * time.Second):
			t.Fatalf("%s waited for a user slot", key.Query)
		}
	}
}

func TestUsageStatsGateScopeOfEndUserKeys(t *testing.T) {
	require.False(t, usageStatsCacheKey{Query: "admin"}.endUserScoped())
	require.False(t, usageStatsCacheKey{Query: "account", AccountID: 1, GroupID: 2}.endUserScoped())
	require.False(t, usageStatsCacheKey{Query: "filters", Filters: &usageFiltersFingerprint{AccountID: 1}}.endUserScoped())
	require.True(t, usageStatsCacheKey{UserID: 1}.endUserScoped())
	require.True(t, usageStatsCacheKey{APIKeyID: 1}.endUserScoped())
	require.True(t, usageStatsCacheKey{APIKeyIDs: []int64{1, 2}}.endUserScoped())
	require.True(t, usageStatsCacheKey{Filters: &usageFiltersFingerprint{UserID: 1}}.endUserScoped())
	require.True(t, usageStatsCacheKey{Filters: &usageFiltersFingerprint{APIKeyID: 1}}.endUserScoped())
}

func TestUsageStatsGate100ConcurrentCallsReachTheDatabaseOnce(t *testing.T) {
	cache := newGatedUsageStatsTestCache(t, 2, time.Second)
	key := usageStatsCacheKey{Query: "burst", UserID: 7, Start: "2026-11-12T00:00:00Z"}
	var calls atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	load := func(context.Context) (int64, error) {
		calls.Add(1)
		once.Do(func() { close(started) })
		<-release
		return 42, nil
	}
	const n = 100
	waiting := make(chan struct{}, n)
	ctx := usageStatsWaitingContext{Context: context.Background(), waiting: waiting}
	results := make(chan int64, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := loadUsageStatsCached(cache, ctx, key, load)
			results <- value
			errs <- err
		}()
	}
	<-started
	for range n {
		<-waiting
	}
	close(release)
	wg.Wait()
	for range n {
		require.NoError(t, <-errs)
		require.EqualValues(t, 42, <-results)
	}
	require.EqualValues(t, 1, calls.Load(), "one load, one slot, however many callers ask")

	// Within the TTL the answer now comes from Redis.
	again, err := loadUsageStatsCached(cache, context.Background(), key, load)
	require.NoError(t, err)
	require.EqualValues(t, 42, again)
	require.EqualValues(t, 1, calls.Load())
}

func TestUsageStatsGateReleasesItsSlotWhenTheLoadFails(t *testing.T) {
	cache := newGatedUsageStatsTestCache(t, 1, 50*time.Millisecond)
	boom := errors.New("boom")
	_, err := loadUsageStatsCached(cache, context.Background(), usageStatsCacheKey{Query: "fails", UserID: 1},
		func(context.Context) (int, error) { return 0, boom })
	require.ErrorIs(t, err, boom)
	value, err := loadUsageStatsCached(cache, context.Background(), usageStatsCacheKey{Query: "next", UserID: 2},
		func(context.Context) (int, error) { return 5, nil })
	require.NoError(t, err, "a failed aggregate must not keep the slot")
	require.Equal(t, 5, value)
	require.Zero(t, len(cache.gate.slots))
}

func TestUsageStatsGateCancelledCallerLeavesTheQueuedLoadRunning(t *testing.T) {
	cache := newGatedUsageStatsTestCache(t, 1, 5*time.Second)
	release := holdUsageStatsSlot(t, cache, 1)

	key := usageStatsCacheKey{Query: "queued", UserID: 2}
	var calls atomic.Int64
	load := func(context.Context) (int, error) {
		calls.Add(1)
		return 9, nil
	}
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := loadUsageStatsCached(cache, caller, key, load)
		result <- err
	}()
	require.Eventually(t, func() bool { return cache.gate.waiting.Load() == 1 }, 2*time.Second, time.Millisecond)

	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("a cancelled caller kept waiting for the queue")
	}
	require.Empty(t, cache.failures.Items(), "a cancelled caller must not leave an error behind")

	// The slot frees up: the detached load still runs once, and its answer is what the next caller gets.
	release()
	require.Eventually(t, func() bool { return calls.Load() == 1 }, 2*time.Second, time.Millisecond)
	value, err := loadUsageStatsCached(cache, context.Background(), key, load)
	require.NoError(t, err)
	require.Equal(t, 9, value)
	require.EqualValues(t, 1, calls.Load(), "the retry reuses the answer instead of scanning again")
}

func TestUsageStatsGateDisabledWhenNoSlotsConfigured(t *testing.T) {
	cache, _ := newUsageStatsTestCache(t)
	require.Nil(t, cache.gate, "MaxConcurrentLoads 0 means no cap")

	const callers = 20
	var inflight, peak atomic.Int64
	allRunning := make(chan struct{})
	load := func(context.Context) (int, error) {
		n := inflight.Add(1)
		bumpPeak(&peak, n)
		if n == callers {
			close(allRunning)
		}
		select {
		case <-allRunning:
		case <-time.After(3 * time.Second):
		}
		inflight.Add(-1)
		return 1, nil
	}
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := loadUsageStatsCached(cache, context.Background(), usageStatsCacheKey{Query: "free", UserID: int64(i + 1)}, load)
			require.NoError(t, err)
		}()
	}
	wg.Wait()
	require.EqualValues(t, callers, peak.Load(), "without a cap every aggregate runs at once, as before")
}

func TestUsageStatsGateIsBuiltFromConfig(t *testing.T) {
	client := newMiniredisClient(t)
	cache := newUsageStatsCache(client, &config.Config{UsageStatsCache: config.UsageStatsCacheConfig{
		Enabled: true, TTL: time.Minute, MaxConcurrentLoads: 8, LoadQueueTimeout: 3 * time.Second,
	}})
	require.NotNil(t, cache.gate)
	require.Equal(t, 8, cap(cache.gate.slots))
	require.Equal(t, 3*time.Second, cache.gate.queueTimeout)

	off := newUsageStatsCache(client, &config.Config{UsageStatsCache: config.UsageStatsCacheConfig{Enabled: true, TTL: time.Minute}})
	require.Nil(t, off.gate)
}

func newMiniredisClient(t *testing.T) *redis.Client {
	t.Helper()
	_, server := newUsageStatsTestCache(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestUsageStatsCacheFailureMemory(t *testing.T) {
	cases := []struct {
		name string
		err  error
		// want is how long the failure should be remembered; 0 means not at all.
		want time.Duration
	}{
		{"cancellation is never remembered", context.Canceled, 0},
		{"deadline", context.DeadlineExceeded, usageStatsCacheTimeoutTTL},
		{"statement timeout", &pq.Error{Code: "57014"}, usageStatsCacheTimeoutTTL},
		{"anything else", errors.New("connection reset"), usageStatsCacheFailureTTL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache, _ := newUsageStatsTestCache(t)
			key := usageStatsCacheKey{Query: "failure_memory", UserID: 1}
			var calls atomic.Int64
			load := func(context.Context) (int, error) {
				calls.Add(1)
				return 0, tc.err
			}
			for range 2 {
				_, err := loadUsageStatsCached(cache, context.Background(), key, load)
				require.ErrorIs(t, err, tc.err)
			}
			item, remembered := cache.failures.Items()[usageStatsRedisKey(key)]
			if tc.want == 0 {
				require.False(t, remembered)
				require.EqualValues(t, 2, calls.Load(), "a cancelled load must be retried, not replayed")
				return
			}
			require.True(t, remembered)
			require.EqualValues(t, 1, calls.Load(), "a remembered failure must not reach the database again")
			remaining := time.Until(time.Unix(0, item.Expiration))
			require.Greater(t, remaining, tc.want-2*time.Second)
			require.LessOrEqual(t, remaining, tc.want)
		})
	}
}

func TestUsageStatsCacheKeepsASlowButSuccessfulAnswer(t *testing.T) {
	cache, server := newUsageStatsTestCache(t)
	key := usageStatsCacheKey{Query: "slow", UserID: 1}
	// The scan uses up its whole budget and still succeeds: the answer must be published anyway.
	_, err := loadUsageStatsCachedWithTimeout(cache, context.Background(), key, 30*time.Millisecond,
		func(ctx context.Context) (int, error) {
			<-ctx.Done()
			return 3, nil
		})
	require.NoError(t, err)
	require.Len(t, server.Keys(), 1)
}
