package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
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

type scheduledQualityConditionalCache struct {
	TempUnschedCache
	reason string
	ids    []int64
	err    error
}

func (c *scheduledQualityConditionalCache) DeleteTempUnsched(context.Context, int64) error {
	panic("quality recovery must never unconditionally delete a runtime ban")
}

func (c *scheduledQualityConditionalCache) DeleteTempUnschedIfReason(_ context.Context, id int64, reason string) (bool, error) {
	c.ids = append(c.ids, id)
	if c.err != nil {
		return false, c.err
	}
	if c.reason != reason {
		return false, nil
	}
	c.reason = ""
	return true, nil
}

func TestScheduledTestRunnerService_QualityRecoveryPreservesNewCacheBan(t *testing.T) {
	for _, mode := range []string{"matching", "new_ban", "cache_error", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			reason := scheduledQualityReasonPrefix + " plan=1"
			account := &Account{ID: 2, Status: StatusDisabled, Schedulable: false, TempUnschedulableReason: reason}
			runner, repo, _ := scheduledQualityRunner(account, &scheduledQualityResultRepo{})
			cache := &scheduledQualityConditionalCache{reason: reason}
			if mode == "new_ban" {
				cache.reason = "upstream quota exhausted"
			}
			if mode == "cache_error" {
				cache.err = errors.New("Redis unavailable")
			}
			runner.rateLimitSvc.tempUnschedCache = cache
			if mode == "unsupported" {
				runner.rateLimitSvc.tempUnschedCache = &struct{ TempUnschedCache }{}
			}
			runner.completePlanRun(context.Background(), &ScheduledTestPlan{ID: 1, AccountID: 2, CronExpression: "* * * * *"}, &ScheduledTestResult{Status: "success"})
			if mode == "cache_error" || mode == "unsupported" {
				require.False(t, account.Schedulable)
				require.Zero(t, repo.recoveries)
			} else {
				require.True(t, account.Schedulable)
				require.Equal(t, []int64{2}, cache.ids)
			}
			switch mode {
			case "matching":
				require.Empty(t, cache.reason)
			case "new_ban":
				require.Equal(t, "upstream quota exhausted", cache.reason)
			}
		})
	}
}

type scheduledQualityAccountRepo struct {
	AccountRepository
	account                           *Account
	reads, pauses, recoveries, clears int
	readErr, pauseErr, recoveryErr    error
	ids                               []int64
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
	results                                                         []*ScheduledTestResult
	nextID                                                          int64
	listCalls, pruneCalls, keepCount, clearCalls                    int
	createErr, listErr, pruneErr, clearErr                          error
	invalidCreate, duplicateRead, nilRead, staleRead, changedReason bool
	account                                                         *Account
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

func (r *scheduledQualityResultRepo) ClearScheduledQualityPause(_ context.Context, id int64, reason string) (bool, error) {
	r.clearCalls++
	if r.clearErr != nil {
		return false, r.clearErr
	}
	if r.changedReason {
		r.account.TempUnschedulableReason = "new-unrelated-ban"
		return false, nil
	}
	if r.account.ID != id || r.account.TempUnschedulableReason != reason {
		return false, nil
	}
	r.account.TempUnschedulableReason = ""
	r.account.TempUnschedulableUntil = nil
	return true, nil
}

func scheduledQualityRunner(account *Account, results *scheduledQualityResultRepo) (*ScheduledTestRunnerService, *scheduledQualityAccountRepo, *scheduledTestPlanRepoStub) {
	accounts := &scheduledQualityAccountRepo{account: account}
	plans := &scheduledTestPlanRepoStub{}
	results.account = account
	return &ScheduledTestRunnerService{
		planRepo:       plans,
		scheduledSvc:   NewScheduledTestService(plans, results),
		accountTestSvc: &AccountTestService{accountRepo: accounts},
		qualityCheck: func(string, string) (string, string) {
			return "success", "test evaluator: explicitly approved quality"
		},
		// Populating this ensures success cannot accidentally invoke the broad
		// recovery path (its unrelated methods on the stub deliberately panic).
		rateLimitSvc: &RateLimitService{accountRepo: accounts},
	}, accounts, plans
}

func TestScheduledTestRunnerService_UnapprovedOutputCannotRecover(t *testing.T) {
	for _, tc := range []struct{ name, response, status string }{
		{"empty", "", "degraded"},
		{"unknown", `<html><svg></svg><script>requestAnimationFrame(()=>{})</script></html>`, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{ID: 2, Status: StatusActive, Schedulable: false}
			results := &scheduledQualityResultRepo{}
			runner, repo, _ := scheduledQualityRunner(account, results)
			runner.qualityCheck = nil
			result := &ScheduledTestResult{Status: "success", ResponseText: tc.response}
			runner.completePlanRun(context.Background(), &ScheduledTestPlan{ID: 1, AccountID: 2, MaxResults: 2, CronExpression: "* * * * *"}, result)
			require.Equal(t, tc.status, result.Status)
			require.False(t, account.Schedulable)
			require.Zero(t, repo.recoveries)
			require.Zero(t, repo.clears)
		})
	}
}

func TestScheduledTestRunnerService_ConsecutiveQualityResults(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		statuses                   []string
		wantScheduling             []bool
		wantPauses, wantRecoveries int
	}{
		{"first_degraded", []string{"degraded"}, []bool{true}, 0, 0},
		{"two_degraded", []string{"degraded", "degraded"}, []bool{true, false}, 1, 0},
		{"unknown_breaks_streak", []string{"degraded", "unknown", "degraded"}, []bool{true, true, true}, 0, 0},
		{"failed_breaks_streak", []string{"degraded", "failed", "degraded"}, []bool{true, true, true}, 0, 0},
		{"success_breaks_streak", []string{"degraded", "success", "degraded"}, []bool{true, true, true}, 0, 0},
		{"two_after_unknown", []string{"degraded", "unknown", "degraded", "degraded"}, []bool{true, true, true, false}, 1, 0},
		{"paused_until_success", []string{"degraded", "degraded", "unknown", "failed", "degraded", "success", "degraded"}, []bool{true, false, false, false, false, true, true}, 1, 1},
		{"repeated_degraded_idempotent", []string{"degraded", "degraded", "degraded"}, []bool{true, false, false}, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{ID: 2, Status: StatusActive, Schedulable: true}
			results := &scheduledQualityResultRepo{}
			runner, repo, plans := scheduledQualityRunner(account, results)
			plan := &ScheduledTestPlan{ID: 1, AccountID: 2, CronExpression: "* * * * *", MaxResults: 1}
			for i, status := range tc.statuses {
				runner.completePlanRun(context.Background(), plan, &ScheduledTestResult{Status: status})
				require.Equal(t, tc.wantScheduling[i], account.Schedulable, "step %d: %s", i, status)
				require.Nil(t, account.TempUnschedulableUntil, "quality pause must not expire at the next cron tick")
			}
			require.Equal(t, tc.wantPauses, repo.pauses)
			require.Equal(t, tc.wantRecoveries, repo.recoveries)
			require.Equal(t, len(tc.statuses), plans.updateCalls)
			require.Equal(t, 2, results.keepCount)
			require.LessOrEqual(t, len(results.results), 2)
		})
	}
}

func TestScheduledTestRunnerService_QualityPersistenceFailures(t *testing.T) {
	for _, failure := range []string{"create", "prune", "invalid_create", "list", "duplicate", "nil", "stale"} {
		t.Run(failure, func(t *testing.T) {
			account := &Account{ID: 2, Status: StatusActive, Schedulable: true}
			results := &scheduledQualityResultRepo{nextID: 2, results: []*ScheduledTestResult{
				{ID: 1, PlanID: 1, Status: "degraded"}, {ID: 2, PlanID: 1, Status: "degraded"},
			}}
			switch failure {
			case "create":
				results.createErr = errors.New("insert failed")
			case "prune":
				results.pruneErr = errors.New("prune failed")
			case "invalid_create":
				results.invalidCreate = true
			case "list":
				results.listErr = errors.New("list failed")
			case "duplicate":
				results.duplicateRead = true
			case "nil":
				results.nilRead = true
			case "stale":
				results.staleRead = true
			}
			runner, repo, plans := scheduledQualityRunner(account, results)
			current := &ScheduledTestResult{ID: 2, PlanID: 1, Status: "degraded"}
			runner.completePlanRun(context.Background(), &ScheduledTestPlan{ID: 1, AccountID: 2, MaxResults: 1, CronExpression: "* * * * *"}, current)
			require.True(t, account.Schedulable)
			require.Zero(t, repo.reads)
			require.Zero(t, repo.pauses)
			require.Equal(t, 1, plans.updateCalls, "storage failure must not spin on the same due plan")
			if failure == "create" || failure == "invalid_create" {
				require.Zero(t, current.ID)
			}
			if failure == "create" || failure == "invalid_create" || failure == "prune" {
				require.Zero(t, results.listCalls)
			}
		})
	}
}

func TestScheduledTestRunnerService_SuccessEnablesOnlyTestedAccount(t *testing.T) {
	for _, status := range []string{StatusActive, StatusError, StatusDisabled} {
		for _, autoRecover := range []bool{false, true} {
			t.Run(status+"/autoRecover="+fmt.Sprint(autoRecover), func(t *testing.T) {
				until := time.Now().Add(time.Hour)
				account := &Account{ID: 29173, Status: status, Schedulable: false, ErrorMessage: "existing error",
					TempUnschedulableUntil: &until, TempUnschedulableReason: "upstream quota exhausted",
					RateLimitResetAt: &until, OverloadUntil: &until,
					Extra:       map[string]any{"model_rate_limits": map[string]any{"model": "keep"}},
					Credentials: map[string]any{"api_key": "test-only-placeholder"},
				}
				runner, repo, _ := scheduledQualityRunner(account, &scheduledQualityResultRepo{})
				runner.completePlanRun(context.Background(), &ScheduledTestPlan{ID: 18, AccountID: 29173, AutoRecover: autoRecover, MaxResults: 1, CronExpression: "* * * * *"}, &ScheduledTestResult{Status: "success"})
				require.Equal(t, StatusActive, account.Status)
				require.True(t, account.Schedulable)
				require.Equal(t, []int64{29173}, repo.ids)
				require.Equal(t, "upstream quota exhausted", account.TempUnschedulableReason)
				require.Equal(t, &until, account.TempUnschedulableUntil)
				require.Equal(t, &until, account.RateLimitResetAt)
				require.Equal(t, &until, account.OverloadUntil)
				require.NotEmpty(t, account.Extra["model_rate_limits"])
				require.Equal(t, "test-only-placeholder", account.Credentials["api_key"])
				if status == StatusError {
					require.Equal(t, 1, repo.clears)
					require.Empty(t, account.ErrorMessage)
				} else {
					require.Equal(t, 1, repo.recoveries)
					require.Equal(t, "existing error", account.ErrorMessage)
				}
			})
		}
	}
}

func TestScheduledTestRunnerService_LegacyQualityPauseRecovery(t *testing.T) {
	for _, mode := range []string{"clear", "changed_reason", "clear_error", "unrelated", "expired_unrelated"} {
		t.Run(mode, func(t *testing.T) {
			until := time.Now().Add(time.Hour)
			account := &Account{ID: 2, Status: StatusActive, Schedulable: false, TempUnschedulableUntil: &until, TempUnschedulableReason: scheduledQualityReasonPrefix + " plan=1", RateLimitResetAt: &until}
			results := &scheduledQualityResultRepo{}
			switch mode {
			case "changed_reason":
				results.changedReason = true
			case "clear_error":
				results.clearErr = errors.New("clear failed")
			case "unrelated":
				account.TempUnschedulableReason = "keep unrelated"
			case "expired_unrelated":
				account.TempUnschedulableReason = "keep unrelated"
				expired := time.Now().Add(-time.Hour)
				account.TempUnschedulableUntil = &expired
			}
			runner, _, _ := scheduledQualityRunner(account, results)
			runner.completePlanRun(context.Background(), &ScheduledTestPlan{ID: 1, AccountID: 2, CronExpression: "* * * * *"}, &ScheduledTestResult{Status: "success"})
			switch mode {
			case "clear":
				require.True(t, account.Schedulable)
				require.Empty(t, account.TempUnschedulableReason)
				require.Nil(t, account.TempUnschedulableUntil)
			case "changed_reason":
				require.False(t, account.Schedulable)
				require.Equal(t, "new-unrelated-ban", account.TempUnschedulableReason)
			case "clear_error":
				require.False(t, account.Schedulable)
				require.NotNil(t, account.TempUnschedulableUntil)
			default:
				require.True(t, account.Schedulable)
				require.Equal(t, "keep unrelated", account.TempUnschedulableReason)
				require.Zero(t, results.clearCalls)
			}
			require.Equal(t, &until, account.RateLimitResetAt)
		})
	}
}

func TestScheduledTestRunnerService_PausePreservesUnrelatedState(t *testing.T) {
	until := time.Now().Add(time.Hour)
	account := &Account{ID: 2, Status: StatusError, Schedulable: true, ErrorMessage: "other error", TempUnschedulableUntil: &until, TempUnschedulableReason: "other pause"}
	runner, repo, _ := scheduledQualityRunner(account, &scheduledQualityResultRepo{})
	plan := &ScheduledTestPlan{ID: 1, AccountID: 2, MaxResults: 1, CronExpression: "* * * * *"}
	for range 2 {
		runner.completePlanRun(context.Background(), plan, &ScheduledTestResult{Status: "degraded"})
	}
	require.False(t, account.Schedulable)
	require.Equal(t, 1, repo.pauses)
	require.Equal(t, StatusError, account.Status)
	require.Equal(t, "other error", account.ErrorMessage)
	require.Equal(t, "other pause", account.TempUnschedulableReason)
	require.Equal(t, &until, account.TempUnschedulableUntil)
}

func TestScheduledTestRunnerService_RejectsUnpersistedOrForeignResult(t *testing.T) {
	for _, result := range []*ScheduledTestResult{nil, {Status: "success"}, {ID: 2, PlanID: 999, Status: "success"}, {ID: 1, PlanID: 1, Status: "unknown"}, {ID: 1, PlanID: 1, Status: "failed"}} {
		runner, repo, _ := scheduledQualityRunner(&Account{ID: 2, Status: StatusDisabled}, &scheduledQualityResultRepo{})
		runner.updateScheduledQualityState(context.Background(), &ScheduledTestPlan{ID: 1, AccountID: 2}, result)
		require.Zero(t, repo.reads)
	}
}

func TestScheduledTestService_ConsecutiveDegradedResults(t *testing.T) {
	for _, tc := range []struct {
		name    string
		history []*ScheduledTestResult
		current *ScheduledTestResult
		want    bool
	}{
		{"distinct", []*ScheduledTestResult{{ID: 1, PlanID: 1, Status: "degraded"}, {ID: 2, PlanID: 1, Status: "degraded"}}, &ScheduledTestResult{ID: 2, PlanID: 1, Status: "degraded"}, true},
		{"same_record_twice", []*ScheduledTestResult{{ID: 2, PlanID: 1, Status: "degraded"}, {ID: 2, PlanID: 1, Status: "degraded"}}, &ScheduledTestResult{ID: 2, PlanID: 1, Status: "degraded"}, false},
		{"old_current", []*ScheduledTestResult{{ID: 1, PlanID: 1, Status: "degraded"}, {ID: 2, PlanID: 1, Status: "degraded"}}, &ScheduledTestResult{ID: 1, PlanID: 1, Status: "degraded"}, false},
		{"foreign_plan", []*ScheduledTestResult{{ID: 1, PlanID: 2, Status: "degraded"}, {ID: 2, PlanID: 1, Status: "degraded"}}, &ScheduledTestResult{ID: 2, PlanID: 1, Status: "degraded"}, false},
		{"zero_id", []*ScheduledTestResult{{ID: 0, PlanID: 1, Status: "degraded"}, {ID: 2, PlanID: 1, Status: "degraded"}}, &ScheduledTestResult{ID: 2, PlanID: 1, Status: "degraded"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewScheduledTestService(nil, &scheduledQualityResultRepo{results: tc.history})
			got, err := svc.hasConsecutiveDegradedResults(context.Background(), tc.current)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
