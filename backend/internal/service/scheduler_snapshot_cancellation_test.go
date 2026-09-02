//go:build unit

package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type schedulerCancellationCache struct {
	SchedulerCache
	cancel        context.CancelFunc
	tokenCaptures int
}

func (c *schedulerCancellationCache) GetSnapshot(ctx context.Context, _ SchedulerBucket) ([]*Account, bool, error) {
	c.cancel()
	return nil, false, ctx.Err()
}

func (c *schedulerCancellationCache) CaptureBucketWriteToken(ctx context.Context, _ SchedulerBucket) (SchedulerBucketWriteToken, error) {
	c.tokenCaptures++
	return SchedulerBucketWriteToken{}, ctx.Err()
}

func (c *schedulerCancellationCache) GetAccount(ctx context.Context, _ int64) (*Account, error) {
	c.cancel()
	return nil, ctx.Err()
}

type schedulerCancellationAccountRepo struct {
	AccountRepository
	listCalls    int
	getByIDCalls int
}

func (r *schedulerCancellationAccountRepo) ListSchedulableUngroupedByPlatform(ctx context.Context, _ string) ([]Account, error) {
	r.listCalls++
	return nil, ctx.Err()
}

func (r *schedulerCancellationAccountRepo) GetByID(ctx context.Context, _ int64) (*Account, error) {
	r.getByIDCalls++
	return nil, ctx.Err()
}

func TestSchedulerSnapshotListStopsAfterRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cache := &schedulerCancellationCache{cancel: cancel}
	repo := &schedulerCancellationAccountRepo{}
	svc := NewSchedulerSnapshotService(cache, nil, repo, nil, nil)

	accounts, useMixed, err := svc.ListSchedulableAccounts(ctx, nil, PlatformOpenAI, false)

	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, accounts)
	require.False(t, useMixed)
	require.Zero(t, cache.tokenCaptures, "canceled requests must not capture a cache publish token")
	require.Zero(t, repo.listCalls, "canceled requests must not fall back to the database")
}

func TestSchedulerSnapshotGetAccountStopsAfterRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cache := &schedulerCancellationCache{cancel: cancel}
	repo := &schedulerCancellationAccountRepo{}
	svc := NewSchedulerSnapshotService(cache, nil, repo, nil, nil)

	account, err := svc.GetAccount(ctx, 42)

	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, account)
	require.Zero(t, repo.getByIDCalls, "canceled requests must not fall back to the database")
}

func TestSchedulerSnapshotFallbackQueryContextDoesNotInheritRequestDeadline(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.DbFallbackTimeoutSeconds = 1
	svc := NewSchedulerSnapshotService(nil, nil, nil, nil, cfg)

	requestCtx, requestCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer requestCancel()
	fallbackCtx, fallbackCancel := svc.fallbackQueryContext(requestCtx)
	defer fallbackCancel()

	<-requestCtx.Done()
	require.ErrorIs(t, requestCtx.Err(), context.DeadlineExceeded)
	require.NoError(t, fallbackCtx.Err(), "the first waiter's deadline must not cancel shared fallback work")

	deadline, ok := fallbackCtx.Deadline()
	require.True(t, ok, "shared fallback work must remain bounded")
	require.Greater(t, time.Until(deadline), 500*time.Millisecond)
	require.LessOrEqual(t, time.Until(deadline), time.Second)
}

type schedulerSharedFallbackRepo struct {
	AccountRepository

	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *schedulerSharedFallbackRepo) ListSchedulableUngroupedByPlatform(ctx context.Context, _ string) ([]Account, error) {
	r.once.Do(func() { close(r.started) })
	select {
	case <-r.release:
		return []Account{{ID: 42, Platform: PlatformOpenAI, Status: StatusActive}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestSchedulerSnapshotFallbackWaiterCanCancelIndependently(t *testing.T) {
	repo := &schedulerSharedFallbackRepo{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	svc := NewSchedulerSnapshotService(nil, nil, repo, nil, nil)

	leaderResult := make(chan error, 1)
	go func() {
		_, _, err := svc.ListSchedulableAccounts(context.Background(), nil, PlatformOpenAI, false)
		leaderResult <- err
	}()

	select {
	case <-repo.started:
	case <-time.After(time.Second):
		t.Fatal("shared fallback query did not start")
	}

	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	waiterResult := make(chan error, 1)
	waiterStarted := make(chan struct{})
	go func() {
		close(waiterStarted)
		_, _, err := svc.ListSchedulableAccounts(waiterCtx, nil, PlatformOpenAI, false)
		waiterResult <- err
	}()
	<-waiterStarted
	// Give the waiter a chance to join the already-running flight before it is
	// canceled. A blocking singleflight.Do implementation would then hang here.
	time.Sleep(10 * time.Millisecond)
	cancelWaiter()

	select {
	case err := <-waiterResult:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(250 * time.Millisecond):
		t.Fatal("canceled waiter remained blocked on the shared fallback query")
	}

	close(repo.release)
	select {
	case err := <-leaderResult:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("leader did not complete after the database query was released")
	}
}

type schedulerAccountFallbackRepo struct {
	AccountRepository
	started chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

type schedulerFallbackCachingCache struct {
	snapshotHydrationCache
	mu       sync.Mutex
	accounts map[int64]*Account
	setCalls atomic.Int32
}

type schedulerFallbackSeedRepo struct {
	AccountRepository
	calls atomic.Int32
}

func (r *schedulerFallbackSeedRepo) GetByID(context.Context, int64) (*Account, error) {
	r.calls.Add(1)
	return &Account{ID: 42, Platform: PlatformOpenAI, Status: StatusActive}, nil
}

func (c *schedulerFallbackCachingCache) GetAccount(_ context.Context, accountID int64) (*Account, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	account := c.accounts[accountID]
	if account == nil {
		return nil, nil
	}
	clone := cloneSnapshotAccount(account)
	return &clone, nil
}

func (c *schedulerFallbackCachingCache) SetAccount(_ context.Context, account *Account) error {
	if account == nil {
		return nil
	}
	c.mu.Lock()
	if c.accounts == nil {
		c.accounts = make(map[int64]*Account)
	}
	clone := cloneSnapshotAccount(account)
	c.accounts[account.ID] = &clone
	c.mu.Unlock()
	c.setCalls.Add(1)
	return nil
}

func TestSchedulerSnapshotAccountFallbackPublishesSuccessfulHydration(t *testing.T) {
	repo := &schedulerFallbackSeedRepo{}
	cache := &schedulerFallbackCachingCache{}
	svc := NewSchedulerSnapshotService(cache, nil, repo, nil, nil)

	first, err := svc.GetAccount(context.Background(), 42)
	require.NoError(t, err)
	require.NotNil(t, first)
	second, err := svc.GetAccount(context.Background(), 42)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Equal(t, int32(1), cache.setCalls.Load(), "one successful DB fallback should seed the account cache")
	require.Equal(t, int32(1), repo.calls.Load(), "the seeded account cache should prevent a second GetByID")
}

func (r *schedulerAccountFallbackRepo) GetByID(ctx context.Context, _ int64) (*Account, error) {
	r.calls.Add(1)
	r.once.Do(func() { close(r.started) })
	select {
	case <-r.release:
		return &Account{ID: 42, Platform: PlatformOpenAI, Status: StatusActive}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestSchedulerSnapshotAccountFallbackSingleflightCoalescesConcurrentMisses(t *testing.T) {
	repo := &schedulerAccountFallbackRepo{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	svc := NewSchedulerSnapshotService(&snapshotHydrationCache{}, nil, repo, nil, nil)

	firstResult := make(chan *Account, 1)
	firstErr := make(chan error, 1)
	go func() {
		account, err := svc.GetAccount(context.Background(), 42)
		firstResult <- account
		firstErr <- err
	}()

	select {
	case <-repo.started:
	case <-time.After(time.Second):
		t.Fatal("account fallback query did not start")
	}

	secondResult := make(chan *Account, 1)
	secondErr := make(chan error, 1)
	go func() {
		account, err := svc.GetAccount(context.Background(), 42)
		secondResult <- account
		secondErr <- err
	}()

	// The second caller must join the existing flight before it is released.
	time.Sleep(10 * time.Millisecond)
	close(repo.release)

	firstAccount := <-firstResult
	if err := <-firstErr; err != nil {
		t.Fatalf("first GetAccount returned error: %v", err)
	}
	secondAccount := <-secondResult
	if err := <-secondErr; err != nil {
		t.Fatalf("second GetAccount returned error: %v", err)
	}
	if got := repo.calls.Load(); got != 1 {
		t.Fatalf("expected one shared GetByID call, got %d", got)
	}
	if firstAccount == nil || secondAccount == nil {
		t.Fatal("expected both callers to receive the hydrated account")
	}
	if firstAccount == secondAccount {
		t.Fatal("shared fallback result must be cloned per caller")
	}
}
