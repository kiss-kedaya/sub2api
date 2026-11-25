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

	// The service wraps repository errors; the response must still be a 429 (not a 5xx: nothing is broken, and
	// it must not count as a gateway failure) with a stable reason and the wait the client is asked to observe.
	status, body := infraerrors.ToHTTP(fmt.Errorf("get usage trend with filters: %w", err))
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "USAGE_STATS_BUSY", body.Reason)
	require.Equal(t, "2", body.Metadata["retry_after"])
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

func TestUsageStatsGateOwnerOfAKey(t *testing.T) {
	cases := []struct {
		name string
		key  usageStatsCacheKey
		want string
	}{
		{"admin report", usageStatsCacheKey{Query: "admin"}, ""},
		{"account scan", usageStatsCacheKey{AccountID: 9, GroupID: 3}, ""},
		{"group report", usageStatsCacheKey{Filters: &usageFiltersFingerprint{GroupID: 3}}, ""},
		{"user", usageStatsCacheKey{UserID: 7}, "u7"},
		{"user in the filters", usageStatsCacheKey{Filters: &usageFiltersFingerprint{UserID: 7}}, "u7"},
		{"api key", usageStatsCacheKey{APIKeyID: 5}, "k5"},
		{"api key in the filters", usageStatsCacheKey{Filters: &usageFiltersFingerprint{APIKeyID: 5}}, "k5"},
		{"user wins over its key", usageStatsCacheKey{UserID: 7, APIKeyID: 5}, "u7"},
		{"filters user wins over filters key", usageStatsCacheKey{Filters: &usageFiltersFingerprint{UserID: 7, APIKeyID: 5}}, "u7"},
		{"key list is charged to its lowest id", usageStatsCacheKey{APIKeyIDs: []int64{9, 4, 6}}, "k4"},
		{"a user and a key with the same number are different owners", usageStatsCacheKey{UserID: 5}, "u5"},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, tc.key.gateOwner(), tc.name)
	}
}

// gateGrab is the outcome of one background acquire.
type gateGrab struct {
	release func()
	err     error
}

// acquireAsync takes a slot for owner in the background and reports the outcome once.
func acquireAsync(gate *usageStatsGate, owner string) <-chan gateGrab {
	out := make(chan gateGrab, 1)
	go func() {
		release, err := gate.acquire(context.Background(), owner)
		out <- gateGrab{release: release, err: err}
	}()
	return out
}

func requireGrabWithin(t *testing.T, ch <-chan gateGrab, d time.Duration, why string) gateGrab {
	t.Helper()
	select {
	case grab := <-ch:
		require.NoError(t, grab.err, why)
		return grab
	case <-time.After(d):
		t.Fatalf("%s", why)
		return gateGrab{}
	}
}

// requireStillWaiting asserts the grab has not been granted after a short moment.
func requireStillWaiting(t *testing.T, ch <-chan gateGrab) {
	t.Helper()
	select {
	case grab := <-ch:
		if grab.release != nil {
			grab.release()
		}
		t.Fatal("the miss got a turn although the owner had none free")
	case <-time.After(50 * time.Millisecond):
	}
}

// waitForOwnerPending waits until n aggregates of owner are inside the gate (running or waiting).
func waitForOwnerPending(t *testing.T, gate *usageStatsGate, owner string, n int) {
	t.Helper()
	require.Eventually(t, func() bool {
		gate.mu.Lock()
		defer gate.mu.Unlock()
		o := gate.owners[owner]
		return o != nil && o.pending == n
	}, 5*time.Second, time.Millisecond)
}

func requireNoOwnersLeft(t *testing.T, gate *usageStatsGate) {
	t.Helper()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	require.Empty(t, gate.owners, "the per-owner bookkeeping must not outlive the aggregates")
}

func TestUsageStatsGateOwnerShareIsAQuarterOfTheSlotsAndAtLeastTwo(t *testing.T) {
	for slots, want := range map[int]int{1: 2, 2: 2, 3: 2, 7: 2, 8: 2, 11: 2, 12: 3, 16: 4, 40: 10} {
		require.Equal(t, want, newUsageStatsGate(slots, time.Second).ownerSlots, "slots=%d", slots)
	}
}

func TestUsageStatsGateOneOwnerCannotTakeEverySlot(t *testing.T) {
	gate := newUsageStatsGate(8, 5*time.Second)
	require.Equal(t, 2, gate.ownerSlots)

	// A client that re-asks with a new date range every time: eight misses at once, each holding its slot until
	// the test lets go.
	var running, peak atomic.Int64
	proceed := make(chan struct{})
	results := make(chan error, 8)
	for range 8 {
		go func() {
			release, err := gate.acquire(context.Background(), "u1")
			if err != nil {
				results <- err
				return
			}
			bumpPeak(&peak, running.Add(1))
			<-proceed
			running.Add(-1)
			release()
			results <- nil
		}()
	}
	waitForOwnerPending(t, gate, "u1", 8)
	require.Eventually(t, func() bool { return running.Load() == 2 }, 5*time.Second, time.Millisecond)
	require.Never(t, func() bool { return running.Load() > 2 }, 100*time.Millisecond, 5*time.Millisecond,
		"one owner holds at most its share of the slots at once")
	require.Equal(t, 2, len(gate.slots))

	// Everybody else is untouched: they get a slot at once, not a place behind the flood.
	other := requireGrabWithin(t, acquireAsync(gate, "u2"), time.Second, "another user waited behind the flood")
	another := requireGrabWithin(t, acquireAsync(gate, "k5"), time.Second, "an api key waited behind the flood")
	require.Equal(t, 4, len(gate.slots))
	require.Zero(t, gate.waiting.Load(), "the flood's surplus waits for its owner, not in the shared queue")

	// A ninth miss from the flooding owner is turned away at once instead of queueing behind eight.
	started := time.Now()
	_, err := gate.acquire(context.Background(), "u1")
	require.ErrorIs(t, err, ErrUsageStatsBusy)
	require.Less(t, time.Since(started), time.Second, "beyond its backlog an owner is turned away immediately")

	// Draining: the waiting ones run as the owner's earlier misses finish, never more than two at a time.
	other.release()
	another.release()
	close(proceed)
	for range 8 {
		require.NoError(t, <-results)
	}
	require.EqualValues(t, 2, peak.Load())
	require.Zero(t, len(gate.slots))
	requireNoOwnersLeft(t, gate)
}

func TestUsageStatsGateOwnerSurplusGivesUpAtTheQueueTimeout(t *testing.T) {
	gate := newUsageStatsGate(8, 60*time.Millisecond)
	first := requireGrabWithin(t, acquireAsync(gate, "u1"), time.Second, "first")
	second := requireGrabWithin(t, acquireAsync(gate, "u1"), time.Second, "second")

	started := time.Now()
	_, err := gate.acquire(context.Background(), "u1")
	require.ErrorIs(t, err, ErrUsageStatsBusy)
	require.GreaterOrEqual(t, time.Since(started), 50*time.Millisecond, "the owner's surplus waits for its own turn, up to the queue timeout")
	require.Zero(t, gate.waiting.Load(), "and not in the shared queue")

	first.release()
	second.release()
	requireNoOwnersLeft(t, gate)
}

func TestUsageStatsGateOwnerSurplusTakesATurnThatComesFreeInTime(t *testing.T) {
	gate := newUsageStatsGate(8, 5*time.Second)
	first := requireGrabWithin(t, acquireAsync(gate, "u1"), time.Second, "first")
	second := requireGrabWithin(t, acquireAsync(gate, "u1"), time.Second, "second")

	third := acquireAsync(gate, "u1")
	requireStillWaiting(t, third)
	first.release()
	grab := requireGrabWithin(t, third, 2*time.Second, "a freed turn must go to the waiting miss")
	grab.release()
	second.release()
	requireNoOwnersLeft(t, gate)
}

func TestUsageStatsGateWithoutQueueNeverWaitsForAnOwnerTurnEither(t *testing.T) {
	gate := newUsageStatsGate(8, 0)
	first := requireGrabWithin(t, acquireAsync(gate, "u1"), time.Second, "first")
	second := requireGrabWithin(t, acquireAsync(gate, "u1"), time.Second, "second")
	started := time.Now()
	_, err := gate.acquire(context.Background(), "u1")
	require.ErrorIs(t, err, ErrUsageStatsBusy)
	require.Less(t, time.Since(started), 500*time.Millisecond)
	first.release()
	second.release()
	requireNoOwnersLeft(t, gate)
}

func TestUsageStatsGateOwnerTurnAndSharedSlotShareOneWaitBudget(t *testing.T) {
	// One slot, held by somebody else: the miss gets its owner's turn at once and then waits for the shared
	// slot. The whole wait stays within one queue timeout, it is not paid twice.
	gate := newUsageStatsGate(1, 80*time.Millisecond)
	hold := requireGrabWithin(t, acquireAsync(gate, "u9"), time.Second, "hold")
	started := time.Now()
	_, err := gate.acquire(context.Background(), "u1")
	require.ErrorIs(t, err, ErrUsageStatsBusy)
	elapsed := time.Since(started)
	require.GreaterOrEqual(t, elapsed, 70*time.Millisecond)
	require.Less(t, elapsed, time.Second)
	hold.release()
	requireNoOwnersLeft(t, gate)
}

func TestUsageStatsGateAnEmptyOwnerOnlyCompetesForSharedSlots(t *testing.T) {
	gate := newUsageStatsGate(4, time.Second)
	var releases []func()
	for range 4 {
		grab := requireGrabWithin(t, acquireAsync(gate, ""), time.Second, "no owner, no per-owner share")
		releases = append(releases, grab.release)
	}
	require.Equal(t, 4, len(gate.slots))
	for _, release := range releases {
		release()
	}
	requireNoOwnersLeft(t, gate)
}

func TestUsageStatsGateReleaseIsIdempotentPerOwner(t *testing.T) {
	gate := newUsageStatsGate(4, 300*time.Millisecond)
	grab := requireGrabWithin(t, acquireAsync(gate, "u1"), time.Second, "grab")
	grab.release()
	grab.release() // extra calls are ignored: they must not hand back somebody else's turn or slot
	first := requireGrabWithin(t, acquireAsync(gate, "u1"), time.Second, "first")
	second := requireGrabWithin(t, acquireAsync(gate, "u1"), time.Second, "second")
	requireStillWaiting(t, acquireAsync(gate, "u1"))
	first.release()
	second.release()
}

// The usage page asks about six aggregates at once on a cold cache. One user's page must still load in full:
// the surplus waits for the user's own earlier ones instead of being turned away.
func TestUsageStatsGateLetsOneUsersWholePageLoad(t *testing.T) {
	cache := newGatedUsageStatsTestCache(t, 8, 5*time.Second)
	var running, peak atomic.Int64
	var twoRunning sync.Once
	both := make(chan struct{})
	load := func(context.Context) (int, error) {
		n := running.Add(1)
		bumpPeak(&peak, n)
		if n == 2 {
			twoRunning.Do(func() { close(both) })
		}
		select { // the first two wait for each other, so the cap is measured, not raced
		case <-both:
		case <-time.After(2 * time.Second):
		}
		running.Add(-1)
		return 1, nil
	}
	const aggregates = 6
	var wg sync.WaitGroup
	errs := make(chan error, aggregates)
	for i := range aggregates {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := loadUsageStatsCached(cache, context.Background(), usageStatsCacheKey{Query: fmt.Sprintf("page%d", i), UserID: 7}, load)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err, "a normal page load must not be turned away")
	}
	require.EqualValues(t, 2, peak.Load(), "but it never takes more than its share of the database at once")
}

// A user at their share must not slow anybody else's page: while one user's misses are stuck on the
// database, other users' misses run straight away.
func TestUsageStatsGateOneUsersStuckScansDoNotDelayOthers(t *testing.T) {
	cache := newGatedUsageStatsTestCache(t, 8, 5*time.Second)
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	started := make(chan struct{}, 8)
	for i := range 8 {
		go func() {
			_, _ = loadUsageStatsCached(cache, context.Background(), usageStatsCacheKey{Query: fmt.Sprintf("slow%d", i), UserID: 1},
				func(context.Context) (int, error) {
					started <- struct{}{}
					<-stuck
					return 0, nil
				})
		}()
	}
	<-started
	<-started
	require.Never(t, func() bool { return len(started) > 0 }, 100*time.Millisecond, 5*time.Millisecond,
		"the flooding user has a third scan on the database")

	for userID := int64(2); userID <= 4; userID++ {
		done := make(chan error, 1)
		go func() {
			_, err := loadUsageStatsCached(cache, context.Background(), usageStatsCacheKey{Query: "mine", UserID: userID},
				func(context.Context) (int, error) { return 1, nil })
			done <- err
		}()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatalf("user %d waited behind another user's stuck scans", userID)
		}
	}
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
	require.Equal(t, 2, cache.gate.ownerSlots)

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
		{"statement timeout", &pq.Error{Code: "57014", Message: "canceling statement due to statement timeout"}, usageStatsCacheTimeoutTTL},
		{"wrapped statement timeout", fmt.Errorf("get trend: %w", &pq.Error{Code: "57014", Message: "canceling statement due to statement timeout"}), usageStatsCacheTimeoutTTL},
		// pg_cancel_backend, the migration's reader canceller: somebody stopped it, the next attempt may be fine.
		{"cancelled by an operator", &pq.Error{Code: "57014", Message: "canceling statement due to user request"}, 0},
		{"wrapped cancel by an operator", fmt.Errorf("get trend: %w", &pq.Error{Code: "57014", Message: "canceling statement due to user request"}), 0},
		// A query_canceled the server did not explain is no evidence that the scan is too big: ordinary memory.
		{"unexplained query_canceled", &pq.Error{Code: "57014"}, usageStatsCacheFailureTTL},
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

// lib/pq relays a cancelled context to the server as a cancel request, so a scan that ran past our own load
// budget comes back as "canceling statement due to user request". That is a timeout, not an operator's cancel,
// and must be remembered like one: the load context is what tells them apart.
func TestUsageStatsCacheRemembersAScanThatRanOutOfItsOwnBudgetWhateverTheDriverSays(t *testing.T) {
	cache, _ := newUsageStatsTestCache(t)
	key := usageStatsCacheKey{Query: "own_budget", UserID: 1}
	var calls atomic.Int64
	load := func(ctx context.Context) (int, error) {
		calls.Add(1)
		<-ctx.Done()
		return 0, &pq.Error{Code: "57014", Message: "canceling statement due to user request"}
	}
	for range 2 {
		_, err := loadUsageStatsCachedWithTimeout(cache, context.Background(), key, 20*time.Millisecond, load)
		require.Error(t, err)
	}
	require.EqualValues(t, 1, calls.Load(), "the second ask must replay the timeout instead of scanning again")
	item, remembered := cache.failures.Items()[usageStatsRedisKey(key)]
	require.True(t, remembered)
	require.Greater(t, time.Until(time.Unix(0, item.Expiration)), usageStatsCacheTimeoutTTL-2*time.Second)
}

// The operator's cancel on a healthy budget is forgotten at once: once the database is back the same scan runs.
func TestUsageStatsCacheDoesNotReplayAnOperatorCancel(t *testing.T) {
	cache, _ := newUsageStatsTestCache(t)
	key := usageStatsCacheKey{Query: "operator_cancel", UserID: 1}
	var calls atomic.Int64
	load := func(context.Context) (int, error) {
		if calls.Add(1) == 1 {
			return 0, &pq.Error{Code: "57014", Message: "canceling statement due to user request"}
		}
		return 7, nil
	}
	_, err := loadUsageStatsCached(cache, context.Background(), key, load)
	require.Error(t, err)
	value, err := loadUsageStatsCached(cache, context.Background(), key, load)
	require.NoError(t, err)
	require.Equal(t, 7, value, "the database recovered: the same question must be asked again, not answered with the old error")
	require.EqualValues(t, 2, calls.Load())
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
