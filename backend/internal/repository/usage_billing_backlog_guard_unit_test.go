//go:build unit

package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// guardBacklogRepo reports a scripted queue backlog and settles nothing; it keeps
// the worker away from the sqlmock database while the guard watches the "queue".
type guardBacklogRepo struct {
	mu      sync.Mutex
	backlog service.UsageBalanceSettlementBacklog
	calls   atomic.Int64
}

func (r *guardBacklogRepo) set(b service.UsageBalanceSettlementBacklog) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backlog = b
}

func (r *guardBacklogRepo) FlushPendingBalanceSettlements(context.Context, int) ([]service.UsageBalanceSettlementResult, error) {
	return nil, nil
}

func (r *guardBacklogRepo) DeleteAppliedBalanceSettlements(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func (r *guardBacklogRepo) BalanceSettlementBacklog(context.Context, int64) (service.UsageBalanceSettlementBacklog, error) {
	r.calls.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.backlog, nil
}

func startGuardWorkerForTest(t *testing.T, repo *guardBacklogRepo, maxBacklog int64, maxAge time.Duration) *service.UsageBalanceSettlementWorker {
	t.Helper()
	worker := service.NewUsageBalanceSettlementWorkerWithOptions(repo, service.UsageBalanceSettlementWorkerOptions{
		MergeEnabled:          true,
		MaxBacklog:            maxBacklog,
		MaxAge:                maxAge,
		BacklogSampleInterval: 5 * time.Millisecond,
	})
	require.True(t, service.UsageBalanceSettlementConsumerRunning())
	t.Cleanup(worker.Stop)
	return worker
}

func expectDedupClaim(mock sqlmock.Sqlmock, requestID string) {
	mock.ExpectBegin()
	mock.ExpectQuery(dedupClaimCTESQL).
		WithArgs(requestID, int64(7), sqlmock.AnyArg()).
		WillReturnRows(dedupClaimClaimedRows(1))
}

func guardTestCommand(requestID string, cost float64) *service.UsageBillingCommand {
	return &service.UsageBillingCommand{
		RequestID:   requestID,
		APIKeyID:    7,
		UserID:      42,
		AccountType: service.AccountTypeAPIKey,
		BalanceCost: cost,
	}
}

// 积压超过阈值时，新扣费走和开关关闭时完全一样的同步 UPDATE（同一条 SQL、同一个事务形状），
// 队列里已有的行不受影响；积压回落后重新入队。
func TestUsageBillingApply_BacklogOverLimitChargesSynchronouslyAndRecovers(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &usageBillingRepository{db: db, mergeBalanceSettlement: true}

	backlog := &guardBacklogRepo{}
	backlog.set(service.UsageBalanceSettlementBacklog{Pending: 500})
	worker := startGuardWorkerForTest(t, backlog, 100, 0)
	require.Eventually(t, service.UsageBalanceSettlementBacklogFallbackActive, 2*time.Second, 2*time.Millisecond)
	chargesBefore := worker.Health(ctx).FallbackCharges

	// Over the limit: the synchronous deduction, no queue insert.
	expectDedupClaim(mock, "req-guard-sync")
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(2.5, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(97.5))
	mock.ExpectCommit()
	result, err := repo.Apply(ctx, guardTestCommand("req-guard-sync", 2.5))
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.False(t, result.BalanceSettlementDeferred)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, 97.5, *result.NewBalance, 1e-9)
	require.Equal(t, chargesBefore+1, worker.Health(ctx).FallbackCharges, "the bypassed charge is counted")

	// Back below half of the limit: queued again.
	backlog.set(service.UsageBalanceSettlementBacklog{Pending: 10})
	require.Eventually(t, func() bool { return !service.UsageBalanceSettlementBacklogFallbackActive() }, 2*time.Second, 2*time.Millisecond)
	expectDedupClaim(mock, "req-guard-queued")
	mock.ExpectQuery(balanceQueueInsertSQL).
		WithArgs("req-guard-queued", int64(7), sqlmock.AnyArg(), int64(42), 1.5, service.BalanceSettlementPending).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(service.BalanceSettlementPending))
	mock.ExpectCommit()
	result, err = repo.Apply(ctx, guardTestCommand("req-guard-queued", 1.5))
	require.NoError(t, err)
	require.True(t, result.BalanceSettlementDeferred)
	require.Nil(t, result.NewBalance)
	require.Equal(t, chargesBefore+1, worker.Health(ctx).FallbackCharges, "queued charges are not counted as bypassed")
	require.NoError(t, mock.ExpectationsWereMet())
}

// 与开关关闭时逐字节一致：开关关闭的仓储不读退回状态、不计数，扣费 SQL 没有任何变化。
func TestUsageBillingApply_SwitchOffIgnoresTheBacklogGuardEntirely(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &usageBillingRepository{db: db, mergeBalanceSettlement: false}

	backlog := &guardBacklogRepo{}
	backlog.set(service.UsageBalanceSettlementBacklog{Pending: 500})
	worker := startGuardWorkerForTest(t, backlog, 100, 0)
	require.Eventually(t, service.UsageBalanceSettlementBacklogFallbackActive, 2*time.Second, 2*time.Millisecond)
	chargesBefore := worker.Health(ctx).FallbackCharges

	expectDedupClaim(mock, "req-switch-off")
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(2.5, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(97.5))
	mock.ExpectCommit()
	result, err := repo.Apply(ctx, guardTestCommand("req-switch-off", 2.5))
	require.NoError(t, err)
	require.False(t, result.BalanceSettlementDeferred)
	require.NotNil(t, result.NewBalance)
	require.Equal(t, chargesBefore, worker.Health(ctx).FallbackCharges, "with the switch off nothing is counted as a fallback")
	require.NoError(t, mock.ExpectationsWereMet())
}

// 积压没有超阈值时，请求路径与保护上线前完全一样：入队，不计退回。
func TestUsageBillingApply_HealthyQueueStillQueues(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &usageBillingRepository{db: db, mergeBalanceSettlement: true}

	backlog := &guardBacklogRepo{}
	backlog.set(service.UsageBalanceSettlementBacklog{Pending: 100, OldestAge: 30 * time.Second}) // exactly at both limits
	worker := startGuardWorkerForTest(t, backlog, 100, 30*time.Second)
	require.Eventually(t, func() bool { return backlog.calls.Load() >= 3 }, 2*time.Second, 2*time.Millisecond)
	require.False(t, service.UsageBalanceSettlementBacklogFallbackActive(), "at the limit is not over it")
	chargesBefore := worker.Health(ctx).FallbackCharges

	expectDedupClaim(mock, "req-healthy")
	mock.ExpectQuery(balanceQueueInsertSQL).
		WithArgs("req-healthy", int64(7), sqlmock.AnyArg(), int64(42), 2.5, service.BalanceSettlementPending).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(service.BalanceSettlementPending))
	mock.ExpectCommit()
	result, err := repo.Apply(ctx, guardTestCommand("req-healthy", 2.5))
	require.NoError(t, err)
	require.True(t, result.BalanceSettlementDeferred)
	require.Equal(t, chargesBefore, worker.Health(ctx).FallbackCharges)
	require.NoError(t, mock.ExpectationsWereMet())
}
