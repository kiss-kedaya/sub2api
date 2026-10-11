package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

type scheduledTestPlanRepoStub struct {
	listDueCalls atomic.Int32
	plans        []*ScheduledTestPlan
	updateCalls  int
}

func (r *scheduledTestPlanRepoStub) Create(context.Context, *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	return nil, nil
}

func (r *scheduledTestPlanRepoStub) GetByID(context.Context, int64) (*ScheduledTestPlan, error) {
	return nil, nil
}

func (r *scheduledTestPlanRepoStub) ListByAccountID(context.Context, int64) ([]*ScheduledTestPlan, error) {
	return nil, nil
}

func (r *scheduledTestPlanRepoStub) ListDue(context.Context, time.Time) ([]*ScheduledTestPlan, error) {
	r.listDueCalls.Add(1)
	return r.plans, nil
}

func (r *scheduledTestPlanRepoStub) Update(context.Context, *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	return nil, nil
}

func (r *scheduledTestPlanRepoStub) Delete(context.Context, int64) error {
	return nil
}

func (r *scheduledTestPlanRepoStub) UpdateAfterRun(context.Context, int64, time.Time, time.Time) error {
	r.updateCalls++
	return nil
}

func TestScheduledTestRunnerService_SkipsWhenPeerIsLeader(t *testing.T) {
	cache := &fakeLeaderLockCache{}
	_, err := cache.TryAcquireLeaderLock(context.Background(), scheduledTestRunnerLeaderLockKey, "peer", time.Minute)
	require.NoError(t, err)

	repo := &scheduledTestPlanRepoStub{}
	runner := NewScheduledTestRunnerService(repo, nil, nil, nil, nil)
	runner.alignmentDelay = 0
	runner.SetLeaderLock(cache, nil)

	runner.runScheduled()

	require.Zero(t, repo.listDueCalls.Load(), "a non-leader must not scan due plans")
	require.Equal(t, "peer", cache.heldBy(scheduledTestRunnerLeaderLockKey))
}

func TestScheduledTestRunnerService_RunsAndReleasesLeaderLock(t *testing.T) {
	cache := &fakeLeaderLockCache{}
	repo := &scheduledTestPlanRepoStub{}
	runner := NewScheduledTestRunnerService(repo, nil, nil, nil, nil)
	runner.alignmentDelay = 0
	runner.SetLeaderLock(cache, nil)

	runner.runScheduled()

	require.Equal(t, int32(1), repo.listDueCalls.Load(), "the leader should scan due plans once")
	require.Empty(t, cache.heldBy(scheduledTestRunnerLeaderLockKey), "the lock must be released after the tick")
}

func TestScheduledTestRunnerService_CacheErrorDoesNotSwitchLockBackend(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &scheduledTestPlanRepoStub{}
	runner := NewScheduledTestRunnerService(repo, nil, nil, nil, nil)
	runner.alignmentDelay = 0
	runner.SetLeaderLock(&fakeLeaderLockCache{acquireErr: context.DeadlineExceeded}, db)
	runner.runScheduled()
	require.Zero(t, repo.listDueCalls.Load())
	require.NoError(t, mock.ExpectationsWereMet(), "Redis failure must not acquire an unrelated PostgreSQL lock")
	runner.SetLeaderLock(&fakeLeaderLockCache{acquireErr: context.DeadlineExceeded}, nil)
	runner.runScheduled()
	require.Zero(t, repo.listDueCalls.Load(), "Redis failure must not run ungated")
}

type scheduledQualityAccountRepo struct {
	AccountRepository
	account                           *Account
	reads, pauses, recoveries, clears int
	readErr, pauseErr, recoveryErr    error
	ids                               []int64
	pauseReasons                      []string
}

// MarkScheduledQualityPause mirrors the repository's conditional stamp: an
// unrelated runtime reason stays untouched.
func (r *scheduledQualityAccountRepo) MarkScheduledQualityPause(_ context.Context, _ int64, reason string) error {
	if r.account == nil {
		return nil
	}
	if r.account.TempUnschedulableReason == "" || strings.HasPrefix(r.account.TempUnschedulableReason, scheduledQualityReasonPrefix) {
		r.account.TempUnschedulableReason = reason
		r.pauseReasons = append(r.pauseReasons, reason)
	}
	return nil
}

func (r *scheduledQualityAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	r.reads++
	return r.account, r.readErr
}

func (r *scheduledQualityAccountRepo) SetSchedulable(_ context.Context, id int64, schedulable bool) error {
	r.ids = append(r.ids, id)
	r.pauses++
	if r.pauseErr != nil {
		return r.pauseErr
	}
	r.account.Schedulable = schedulable
	return nil
}

func (r *scheduledQualityAccountRepo) ClearError(_ context.Context, id int64) error {
	r.ids = append(r.ids, id)
	r.clears++
	if r.recoveryErr != nil {
		return r.recoveryErr
	}
	r.account.Status = StatusActive
	r.account.ErrorMessage = ""
	r.account.Schedulable = true
	return nil
}

func (r *scheduledQualityAccountRepo) BulkUpdate(_ context.Context, ids []int64, update AccountBulkUpdate) (int64, error) {
	if len(ids) != 1 || ids[0] != r.account.ID || update.Status == nil || update.Schedulable == nil ||
		update.Credentials != nil || update.Extra != nil || update.Name != nil || update.ProxyID != nil ||
		update.Priority != nil || update.Concurrency != nil || update.RateMultiplier != nil {
		return 0, errors.New("expected a single-account status/scheduling-only patch")
	}
	r.ids = append(r.ids, ids...)
	r.recoveries++
	if r.recoveryErr != nil {
		return 0, r.recoveryErr
	}
	r.account.Status = *update.Status
	r.account.Schedulable = *update.Schedulable
	return 1, nil
}

type scheduledQualityResultRepo struct {
	results                                          []*ScheduledTestResult
	nextID                                           int64
	listCalls, pruneCalls, keepCount                 int
	createErr, listErr, pruneErr                     error
	invalidCreate, duplicateRead, nilRead, staleRead bool
	account                                          *Account
}

func (r *scheduledQualityResultRepo) Create(_ context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error) {
	if r.createErr != nil {
		return nil, r.createErr
	}
	if r.invalidCreate {
		return nil, nil
	}
	r.nextID++
	saved := *result
	saved.ID = r.nextID
	// Equal timestamps deliberately exercise the ID tie-breaker.
	saved.CreatedAt = time.Unix(1000, 0)
	r.results = append(r.results, &saved)
	return &saved, nil
}

func (r *scheduledQualityResultRepo) ListByPlanID(_ context.Context, planID int64, limit int) ([]*ScheduledTestResult, error) {
	r.listCalls++
	if r.listErr != nil {
		return nil, r.listErr
	}
	var results []*ScheduledTestResult
	for _, result := range r.results {
		if result.PlanID == planID {
			results = append(results, result)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].CreatedAt.Equal(results[j].CreatedAt) {
			return results[i].ID > results[j].ID
		}
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})
	if len(results) > limit {
		results = results[:limit]
	}
	if r.duplicateRead && len(results) > 0 {
		return []*ScheduledTestResult{results[0], results[0]}, nil
	}
	if r.nilRead {
		return []*ScheduledTestResult{nil, nil}, nil
	}
	if r.staleRead && len(results) > 0 {
		results = results[1:]
	}
	return results, nil
}

func (r *scheduledQualityResultRepo) PruneOldResults(ctx context.Context, planID int64, keepCount int) error {
	r.pruneCalls++
	r.keepCount = keepCount
	if r.pruneErr != nil {
		return r.pruneErr
	}
	if len(r.results) > keepCount {
		r.results = r.results[len(r.results)-keepCount:]
	}
	return nil
}

func scheduledQualityRunner(account *Account, results *scheduledQualityResultRepo) (*ScheduledTestRunnerService, *scheduledQualityAccountRepo, *scheduledTestPlanRepoStub) {
	accounts := &scheduledQualityAccountRepo{account: account}
	plans := &scheduledTestPlanRepoStub{}
	results.account = account
	return &ScheduledTestRunnerService{
		planRepo:       plans,
		scheduledSvc:   NewScheduledTestService(plans, results),
		accountTestSvc: &AccountTestService{accountRepo: accounts},
		// Populating this ensures success cannot accidentally invoke the broad
		// recovery path (its unrelated methods on the stub deliberately panic).
		rateLimitSvc: &RateLimitService{accountRepo: accounts},
	}, accounts, plans
}

func TestScheduledTestRunnerService_PlainPlanKeepsUpstreamBehavior(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      string
		autoRecover bool
		wantClears  int
	}{
		{"success_without_auto_recover", "success", false, 0},
		{"success_with_auto_recover", "success", true, 1},
		{"failed_with_auto_recover", "failed", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{ID: 2, Status: StatusError, Schedulable: true, ErrorMessage: "runtime error"}
			results := &scheduledQualityResultRepo{}
			runner, repo, _ := scheduledQualityRunner(account, results)
			plan := &ScheduledTestPlan{ID: 1, AccountID: 2, MaxResults: 2, CronExpression: "* * * * *", AutoRecover: tc.autoRecover}
			runner.completePlanRun(context.Background(), plan, &ScheduledTestResult{Status: tc.status})
			require.Zero(t, repo.pauses, "a plain plan must never pause an account")
			require.Zero(t, repo.recoveries, "a plain plan must not run the quality recovery path")
			require.Equal(t, tc.wantClears, repo.clears)
			if tc.wantClears > 0 {
				require.Equal(t, StatusActive, account.Status)
			} else {
				require.Equal(t, StatusError, account.Status)
			}
		})
	}
}
