package service

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

type tokenVersionRepoStub struct {
	AccountRepository
	calls atomic.Int64
}

func (r *tokenVersionRepoStub) GetByID(context.Context, int64) (*Account, error) {
	r.calls.Add(1)
	return &Account{ID: 1}, nil
}

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
	snapshot := &SchedulerSnapshotService{cfg: &config.Config{
		Gateway: config.GatewayConfig{Scheduling: config.GatewaySchedulingConfig{RequestFreshnessEnabled: true}},
	}}
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

func TestPrepareSchedulerRequestContextSnapshotOnlySkipsFreshnessProjection(t *testing.T) {
	repo := &schedulerFreshnessRepoStub{projection: map[int64]SchedulerFreshness{
		61: schedulerFreshnessTestValue(61, nil),
	}}
	svc := &GatewayService{
		accountRepo: repo,
		schedulerSnapshot: &SchedulerSnapshotService{cfg: &config.Config{
			Gateway: config.GatewayConfig{Scheduling: config.GatewaySchedulingConfig{RequestFreshnessEnabled: false}},
		}},
	}
	accounts := []Account{{ID: 61, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}}

	ctx := svc.PrepareSchedulerRequestContext(context.Background())
	if schedulerFreshnessFromContext(ctx) != nil {
		t.Fatal("snapshot-only request must not install a durable freshness state")
	}
	ctx = withSchedulerFreshnessAccounts(ctx, repo, svc.schedulerSnapshot, accounts)
	got := applySchedulerFreshnessAccounts(ctx, accounts)
	if len(got) != 1 || got[0].ID != accounts[0].ID {
		t.Fatalf("snapshot-only candidates = %#v, want original snapshot account", got)
	}

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.projectionCalls != 0 {
		t.Fatalf("snapshot-only scheduling issued %d freshness queries", repo.projectionCalls)
	}
}

func TestSchedulerFreshnessFallbackRemainsExplicit(t *testing.T) {
	repo := &schedulerFreshnessRepoStub{projection: map[int64]SchedulerFreshness{
		62: schedulerFreshnessTestValue(62, nil),
	}}
	ctx := withSchedulerSnapshotOnly(context.Background())
	ctx = withSchedulerFreshnessFallback(ctx, repo, &SchedulerSnapshotService{}, 62)
	state := schedulerFreshnessFromContext(ctx)
	if state == nil || !state.enabled() {
		t.Fatal("explicit fallback must install a durable freshness state")
	}
	state.prime(ctx)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.projectionCalls != 1 {
		t.Fatalf("explicit fallback projection calls = %d, want 1", repo.projectionCalls)
	}
}

func TestWithSchedulerSnapshotContextPreservesZeroDBMarkerForDetachedBilling(t *testing.T) {
	repo := &tokenVersionRepoStub{}
	account := &Account{ID: 1, Credentials: map[string]any{"_token_version": int64(1)}}
	parent := withSchedulerSnapshotOnly(context.Background())
	workerCtx := WithSchedulerSnapshotContext(parent, context.Background())

	latest, stale := CheckTokenVersion(workerCtx, account, repo)
	if latest != nil || stale {
		t.Fatalf("detached snapshot-only token check = (%#v, %v), want (nil, false)", latest, stale)
	}
	if got := repo.calls.Load(); got != 0 {
		t.Fatalf("detached billing token check issued %d PostgreSQL reads", got)
	}
}

func TestSchedulerHydratedAccount_ReusesPrivateSnapshotCopyWithinRequest(t *testing.T) {
	repo := &schedulerFreshnessRepoStub{}
	snapshot := &SchedulerSnapshotService{}
	ctx := withSchedulerFreshness(context.Background(), repo, snapshot)

	original := &Account{
		ID:          51,
		Credentials: map[string]any{"token": "secret"},
		Extra:       map[string]any{"nested": map[string]any{"enabled": true}},
	}
	rememberSchedulerHydratedAccount(ctx, original)
	original.Credentials["token"] = "mutated-after-store"
	original.Extra["nested"].(map[string]any)["enabled"] = false

	first, ok := schedulerHydratedAccount(ctx, original.ID)
	if !ok || first == nil {
		t.Fatal("scheduler hydration cache miss")
	}
	if got := first.Credentials["token"]; got != "secret" {
		t.Fatalf("cached credentials token = %v, want secret", got)
	}
	nested := first.Extra["nested"].(map[string]any)
	if got := nested["enabled"]; got != true {
		t.Fatalf("cached nested extra = %v, want true", got)
	}

	first.Credentials["token"] = "mutated-after-read"
	second, ok := schedulerHydratedAccount(ctx, original.ID)
	if !ok || second == nil || second.Credentials["token"] != "secret" {
		t.Fatalf("hydration cache returned shared mutable state: %#v", second)
	}
}
