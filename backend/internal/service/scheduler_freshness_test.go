package service

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
)

type schedulerFreshnessRepoStub struct {
	AccountRepository

	mu              sync.Mutex
	projection      map[int64]SchedulerFreshness
	projectionErr   error
	fallback        map[int64]*Account
	projectionCalls int
	fallbackCalls   int
	projectionIDs   [][]int64
}

func (r *schedulerFreshnessRepoStub) ReadSchedulerFreshness(_ context.Context, ids []int64) (map[int64]SchedulerFreshness, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.projectionCalls++
	r.projectionIDs = append(r.projectionIDs, append([]int64(nil), ids...))
	if r.projectionErr != nil {
		return nil, r.projectionErr
	}
	values := make(map[int64]SchedulerFreshness, len(r.projection))
	for id, value := range r.projection {
		values[id] = value
	}
	return values, nil
}

func (r *schedulerFreshnessRepoStub) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fallbackCalls++
	accounts := make([]*Account, 0, len(ids))
	for _, id := range ids {
		if account := r.fallback[id]; account != nil {
			clone := *account
			accounts = append(accounts, &clone)
		}
	}
	return accounts, nil
}

func schedulerFreshnessTestValue(id int64, parentID *int64) SchedulerFreshness {
	return SchedulerFreshness{
		ID:              id,
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		Status:          StatusActive,
		Schedulable:     true,
		ParentAccountID: parentID,
	}
}

func TestSchedulerFreshness_PrimesCandidatesAndSharedParentInOneBatch(t *testing.T) {
	parentID := int64(91)
	repo := &schedulerFreshnessRepoStub{projection: map[int64]SchedulerFreshness{
		11: schedulerFreshnessTestValue(11, &parentID),
		12: schedulerFreshnessTestValue(12, &parentID),
		91: schedulerFreshnessTestValue(91, nil),
	}}
	accounts := []Account{
		{ID: 11, ParentAccountID: &parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		{ID: 12, ParentAccountID: &parentID, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
	}
	ctx := withSchedulerFreshness(context.Background(), repo, &SchedulerSnapshotService{})
	ctx = withSchedulerFreshnessAccounts(ctx, repo, &SchedulerSnapshotService{}, accounts)
	got := applySchedulerFreshnessAccounts(ctx, accounts)
	if len(got) != 2 {
		t.Fatalf("fresh candidate count = %d, want 2", len(got))
	}
	if parent := schedulerFreshnessLookup(ctx, parentID); parent == nil || !parent.IsOpenAIOAuth() {
		t.Fatalf("shared parent lookup = %#v, want active OpenAI OAuth parent", parent)
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.projectionCalls != 1 {
		t.Fatalf("projection calls = %d, want 1", repo.projectionCalls)
	}
	if repo.fallbackCalls != 0 {
		t.Fatalf("fallback calls = %d, want 0", repo.fallbackCalls)
	}
	ids := append([]int64(nil), repo.projectionIDs[0]...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	want := []int64{11, 12, 91}
	for i := range want {
		if i >= len(ids) || ids[i] != want[i] {
			t.Fatalf("projection ids = %v, want %v", ids, want)
		}
	}
}

func TestSchedulerFreshness_ProjectionFailureUsesOneBatchFallbackAndFailsClosed(t *testing.T) {
	repo := &schedulerFreshnessRepoStub{
		projectionErr: errors.New("projection unavailable"),
		fallback: map[int64]*Account{
			21: {ID: 21, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true},
		},
	}
	accounts := []Account{
		{ID: 21, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		{ID: 22, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
	}
	ctx := withSchedulerFreshness(context.Background(), repo, &SchedulerSnapshotService{})
	ctx = withSchedulerFreshnessAccounts(ctx, repo, &SchedulerSnapshotService{}, accounts)
	got := applySchedulerFreshnessAccounts(ctx, accounts)
	if len(got) != 1 || got[0].ID != 21 {
		t.Fatalf("fallback candidates = %#v, want only account 21", got)
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.projectionCalls != 1 || repo.fallbackCalls != 1 {
		t.Fatalf("calls projection=%d fallback=%d, want 1/1", repo.projectionCalls, repo.fallbackCalls)
	}
}

func TestSchedulerFreshnessLookupResultDoesNotAllowFallbackAfterProjectionFailure(t *testing.T) {
	repo := &schedulerFreshnessRepoStub{projectionErr: errors.New("database unavailable")}
	ctx := withSchedulerFreshness(context.Background(), repo, &SchedulerSnapshotService{}, 31)

	account, known := schedulerFreshnessLookupResult(ctx, 31)
	if account != nil || !known {
		t.Fatalf("lookup result = (%#v, %v), want (nil, true)", account, known)
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.projectionCalls != 1 || repo.fallbackCalls != 1 {
		t.Fatalf("calls projection=%d fallback=%d, want 1/1", repo.projectionCalls, repo.fallbackCalls)
	}
}

func TestPrepareSchedulerRequestContextReusesProjectionAcrossRetries(t *testing.T) {
	repo := &schedulerFreshnessRepoStub{projection: map[int64]SchedulerFreshness{
		41: schedulerFreshnessTestValue(41, nil),
	}}
	snapshot := &SchedulerSnapshotService{}
	svc := &GatewayService{accountRepo: repo, schedulerSnapshot: snapshot}
	accounts := []Account{{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeOAuth}}

	ctx := svc.PrepareSchedulerRequestContext(context.Background())
	ctx = withSchedulerFreshnessAccounts(ctx, repo, snapshot, accounts)
	// A failover attempt derives its own context from the request context. The
	// projection must remain shared so the retry cannot issue another batch.
	retryCtx := svc.PrepareSchedulerRequestContext(ctx)
	retryCtx = withSchedulerFreshnessAccounts(retryCtx, repo, snapshot, accounts)
	if got := applySchedulerFreshnessAccounts(retryCtx, accounts); len(got) != 1 {
		t.Fatalf("retry candidates = %d, want 1", len(got))
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.projectionCalls != 1 {
		t.Fatalf("projection calls = %d, want 1 across initial selection and retry", repo.projectionCalls)
	}
}
