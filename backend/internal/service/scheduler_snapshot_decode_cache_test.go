//go:build unit

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type decodeCacheProbe struct {
	SchedulerCache
	snapshotCalls int
	setCalls      int
	mu            sync.Mutex
	hit           bool
	accounts      []*Account
	getBarrier    chan struct{}
	getArrivals   int
}

func (c *decodeCacheProbe) GetSnapshot(context.Context, SchedulerBucket) ([]*Account, bool, error) {
	c.mu.Lock()
	c.snapshotCalls++
	barrier := c.getBarrier
	if barrier != nil {
		c.getArrivals++
		if c.getArrivals == 2 {
			close(barrier)
		}
	}
	accounts, hit := c.accounts, c.hit
	c.mu.Unlock()
	if barrier != nil {
		<-barrier
	}
	return accounts, hit, nil
}

func (c *decodeCacheProbe) CaptureBucketWriteToken(_ context.Context, bucket SchedulerBucket) (SchedulerBucketWriteToken, error) {
	return SchedulerBucketWriteToken{Bucket: bucket, Epoch: 1}, nil
}

func (c *decodeCacheProbe) SetSnapshot(_ context.Context, _ SchedulerBucket, _ SchedulerBucketWriteToken, accounts []Account) error {
	c.mu.Lock()
	c.setCalls++
	c.accounts = accountsToPointers(accounts)
	c.hit = true
	c.mu.Unlock()
	return nil
}

func TestSchedulerSnapshotDecodedCacheAvoidsRepeatedCacheReads(t *testing.T) {
	cache := &decodeCacheProbe{
		hit: true,
		accounts: []*Account{{
			ID:       7,
			Platform: PlatformOpenAI,
			Status:   StatusActive,
		}},
	}
	svc := NewSchedulerSnapshotService(cache, nil, nil, nil, nil)

	first, _, err := svc.ListSchedulableAccounts(context.Background(), nil, PlatformOpenAI, false)
	if err != nil {
		t.Fatalf("first list: %v", err)
	}
	second, _, err := svc.ListSchedulableAccounts(context.Background(), nil, PlatformOpenAI, false)
	if err != nil {
		t.Fatalf("second list: %v", err)
	}
	if len(first) != 1 || len(second) != 1 || first[0].ID != second[0].ID {
		t.Fatalf("unexpected accounts: first=%+v second=%+v", first, second)
	}
	cache.mu.Lock()
	calls := cache.snapshotCalls
	cache.mu.Unlock()
	if calls != 1 {
		t.Fatalf("decoded cache should avoid second SchedulerCache.GetSnapshot call, got %d", calls)
	}
}

func TestSchedulerSnapshotDecodedCacheDoesNotShareMutableAccountMaps(t *testing.T) {
	cache := &decodeCacheProbe{
		hit: true,
		accounts: []*Account{{
			ID:          8,
			Platform:    PlatformOpenAI,
			Status:      StatusActive,
			Credentials: map[string]any{"nested": map[string]any{"token": "original"}},
			Extra:       map[string]any{"limits": map[string]any{"remaining": float64(3)}},
		}},
	}
	svc := NewSchedulerSnapshotService(cache, nil, nil, nil, nil)

	first, _, err := svc.ListSchedulableAccounts(context.Background(), nil, PlatformOpenAI, false)
	if err != nil {
		t.Fatalf("first list: %v", err)
	}
	first[0].Credentials["nested"].(map[string]any)["token"] = "mutated"
	first[0].Extra["limits"].(map[string]any)["remaining"] = float64(0)

	second, _, err := svc.ListSchedulableAccounts(context.Background(), nil, PlatformOpenAI, false)
	if err != nil {
		t.Fatalf("second list: %v", err)
	}
	if got := second[0].Credentials["nested"].(map[string]any)["token"]; got != "original" {
		t.Fatalf("credentials map leaked through decoded cache: %v", got)
	}
	if got := second[0].Extra["limits"].(map[string]any)["remaining"]; got != float64(3) {
		t.Fatalf("extra map leaked through decoded cache: %v", got)
	}
}

type snapshotFallbackProbeRepo struct {
	AccountRepository
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (r *snapshotFallbackProbeRepo) ListSchedulableUngroupedByPlatform(context.Context, string) ([]Account, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-r.release
	return []Account{{ID: 42, Platform: PlatformOpenAI, Status: StatusActive}}, nil
}

func TestSchedulerSnapshotFallbackSingleflightPerBucket(t *testing.T) {
	cache := &decodeCacheProbe{hit: false, getBarrier: make(chan struct{})}
	repo := &snapshotFallbackProbeRepo{started: make(chan struct{}, 2), release: make(chan struct{})}
	svc := NewSchedulerSnapshotService(cache, nil, repo, nil, nil)

	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, _, err := svc.ListSchedulableAccounts(context.Background(), nil, PlatformOpenAI, false)
			results <- err
		}()
	}
	select {
	case <-repo.started:
	case <-time.After(time.Second):
		t.Fatal("fallback query did not start")
	}
	close(repo.release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("fallback list: %v", err)
		}
	}
	repo.mu.Lock()
	calls := repo.calls
	repo.mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected one DB fallback for concurrent bucket misses, got %d", calls)
	}
	cache.mu.Lock()
	sets := cache.setCalls
	cache.mu.Unlock()
	if sets != 1 {
		t.Fatalf("expected one cache publication for concurrent bucket misses, got %d", sets)
	}
}

type schedulerLateSnapshotCache struct {
	SchedulerCache
	mu    sync.Mutex
	calls int
}

func (c *schedulerLateSnapshotCache) GetSnapshot(context.Context, SchedulerBucket) ([]*Account, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls == 1 {
		return nil, false, nil
	}
	return []*Account{{ID: 44, Platform: PlatformOpenAI, Status: StatusActive}}, true, nil
}

func TestSchedulerSnapshotFallbackRechecksCacheBeforeLimiter(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.DbFallbackEnabled = true
	cfg.Gateway.Scheduling.DbFallbackMaxQPS = 1
	cache := &schedulerLateSnapshotCache{}
	svc := NewSchedulerSnapshotService(cache, nil, nil, nil, cfg)
	// Exhaust the current fallback window. A cache hit observed by the
	// singleflight recheck must not need a database fallback token.
	require.True(t, svc.fallbackLimit.Allow())

	accounts, _, err := svc.ListSchedulableAccounts(context.Background(), nil, PlatformOpenAI, false)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, int64(44), accounts[0].ID)
	cache.mu.Lock()
	calls := cache.calls
	cache.mu.Unlock()
	require.Equal(t, 2, calls, "the shared fallback must recheck the cache before consuming the limiter")
}

func TestSchedulerSnapshotFallbackSingleflightDoesNotShareMutableAccountMaps(t *testing.T) {
	cache := &decodeCacheProbe{hit: false, getBarrier: make(chan struct{})}
	repo := &snapshotFallbackProbeRepoWithMaps{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	svc := NewSchedulerSnapshotService(cache, nil, repo, nil, nil)

	type result struct {
		accounts []Account
		err      error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			accounts, _, err := svc.ListSchedulableAccounts(context.Background(), nil, PlatformOpenAI, false)
			results <- result{accounts: accounts, err: err}
		}()
	}
	select {
	case <-repo.started:
	case <-time.After(time.Second):
		t.Fatal("fallback query did not start")
	}
	close(repo.release)

	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("fallback errors: first=%v second=%v", first.err, second.err)
	}
	first.accounts[0].Credentials["nested"].(map[string]any)["token"] = "mutated"
	first.accounts[0].Extra["limits"].(map[string]any)["remaining"] = float64(0)
	if got := second.accounts[0].Credentials["nested"].(map[string]any)["token"]; got != "original" {
		t.Fatalf("credentials map shared between fallback waiters: %v", got)
	}
	if got := second.accounts[0].Extra["limits"].(map[string]any)["remaining"]; got != float64(3) {
		t.Fatalf("extra map shared between fallback waiters: %v", got)
	}
}

type snapshotFallbackProbeRepoWithMaps struct {
	AccountRepository
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *snapshotFallbackProbeRepoWithMaps) ListSchedulableUngroupedByPlatform(context.Context, string) ([]Account, error) {
	r.once.Do(func() { close(r.started) })
	<-r.release
	return []Account{{
		ID:          43,
		Platform:    PlatformOpenAI,
		Status:      StatusActive,
		Credentials: map[string]any{"nested": map[string]any{"token": "original"}},
		Extra:       map[string]any{"limits": map[string]any{"remaining": float64(3)}},
	}}, nil
}
