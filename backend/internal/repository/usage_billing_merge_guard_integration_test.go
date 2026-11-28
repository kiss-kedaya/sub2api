//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// stallableSettlementRepo delegates to the real settlement repository but can hold
// back flushes, which is how these tests make the queue back up (a stuck or slow
// worker) while the guard keeps reading the real queue through the real SQL.
type stallableSettlementRepo struct {
	service.UsageBalanceSettlementRepository
	reader  service.UsageBalanceSettlementBacklogReader
	stalled atomic.Bool
}

func newStallableSettlementRepo(t *testing.T) *stallableSettlementRepo {
	t.Helper()
	real := NewUsageBalanceSettlementRepository(integrationDB)
	return &stallableSettlementRepo{
		UsageBalanceSettlementRepository: real,
		reader:                           mustBacklogReader(t, real),
	}
}

func mustBacklogReader(t *testing.T, repo service.UsageBalanceSettlementRepository) service.UsageBalanceSettlementBacklogReader {
	t.Helper()
	reader, ok := repo.(service.UsageBalanceSettlementBacklogReader)
	require.True(t, ok, "the settlement repository must expose the backlog reader")
	return reader
}

func (r *stallableSettlementRepo) FlushPendingBalanceSettlements(ctx context.Context, limit int) ([]service.UsageBalanceSettlementResult, error) {
	if r.stalled.Load() {
		return nil, nil
	}
	return r.UsageBalanceSettlementRepository.FlushPendingBalanceSettlements(ctx, limit)
}

func (r *stallableSettlementRepo) BalanceSettlementBacklog(ctx context.Context, depthCap int64) (service.UsageBalanceSettlementBacklog, error) {
	return r.reader.BalanceSettlementBacklog(ctx, depthCap)
}

func integrationBacklogReader(t *testing.T) service.UsageBalanceSettlementBacklogReader {
	t.Helper()
	return mustBacklogReader(t, NewUsageBalanceSettlementRepository(integrationDB))
}

// integrationDrainAllSettlements settles whatever earlier tests left pending so the
// global queue readings below start from zero.
func integrationDrainAllSettlements(t *testing.T) {
	t.Helper()
	real := NewUsageBalanceSettlementRepository(integrationDB)
	reader := mustBacklogReader(t, real)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		backlog, err := reader.BalanceSettlementBacklog(context.Background(), 1_000_000)
		require.NoError(t, err)
		if backlog.Pending == 0 {
			return
		}
		_, err = real.FlushPendingBalanceSettlements(context.Background(), 2000)
		require.NoError(t, err)
	}
	t.Fatal("settlement queue did not drain")
}

func integrationPendingTotal(t *testing.T) int64 {
	t.Helper()
	backlog, err := integrationBacklogReader(t).BalanceSettlementBacklog(context.Background(), 1_000_000)
	require.NoError(t, err)
	return backlog.Pending
}

func integrationCharge(t *testing.T, repo service.UsageBillingRepository, userID, keyID int64, requestID string, cost float64) *service.UsageBillingApplyResult {
	t.Helper()
	result, err := repo.Apply(context.Background(), &service.UsageBillingCommand{
		RequestID:   requestID,
		APIKeyID:    keyID,
		UserID:      userID,
		AccountType: service.AccountTypeAPIKey,
		BalanceCost: cost,
	})
	require.NoError(t, err)
	require.True(t, result.Applied, requestID)
	return result
}

func TestBalanceSettlementBacklogReadsTheRealQueue(t *testing.T) {
	ctx := context.Background()
	integrationDrainAllSettlements(t)
	startIntegrationSettlementConsumerFlag(t)
	repo := integrationMergeBillingRepo(t, true)
	userID, keyID := integrationBillingFixtures(t, 100)
	reader := integrationBacklogReader(t)

	empty, err := reader.BalanceSettlementBacklog(ctx, 1000)
	require.NoError(t, err)
	require.Zero(t, empty.Pending)
	require.Zero(t, empty.OldestAge)
	require.False(t, empty.PendingCapped)

	for i := 0; i < 5; i++ {
		integrationCharge(t, repo, userID, keyID, fmt.Sprintf("backlog-read-%d-%d", userID, i), 0.5)
	}
	time.Sleep(150 * time.Millisecond)

	backlog, err := reader.BalanceSettlementBacklog(ctx, 1000)
	require.NoError(t, err)
	require.Equal(t, int64(5), backlog.Pending)
	require.False(t, backlog.PendingCapped)
	require.GreaterOrEqual(t, backlog.OldestAge, 100*time.Millisecond)
	require.Less(t, backlog.OldestAge, 30*time.Second)

	capped, err := reader.BalanceSettlementBacklog(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, int64(3), capped.Pending)
	require.True(t, capped.PendingCapped, "the count stops at the cap and says so")

	integrationFlushUntilDrained(t, userID)
	drained, err := reader.BalanceSettlementBacklog(ctx, 1000)
	require.NoError(t, err)
	require.Zero(t, drained.Pending)
	require.Zero(t, drained.OldestAge)
}

// 失败退避会把 available_at 推到未来，但扣费的真实等待时间必须仍然可见：
// 否则「整批反复失败」时最老年龄会被退避周期压住，永远触发不了退回。
func TestBalanceSettlementBacklogAgeIsNotHiddenByRetryBackoff(t *testing.T) {
	ctx := context.Background()
	integrationDrainAllSettlements(t)
	startIntegrationSettlementConsumerFlag(t)
	repo := integrationMergeBillingRepo(t, true)
	userID, keyID := integrationBillingFixtures(t, 100)

	for i := 0; i < 3; i++ {
		integrationCharge(t, repo, userID, keyID, fmt.Sprintf("backlog-backoff-%d-%d", userID, i), 0.5)
	}
	// Same shape as recordBalanceSettlementFailure leaves behind for a batch that has
	// been failing for ten minutes: many attempts, retry pushed into the future.
	_, err := integrationDB.ExecContext(ctx, `
		UPDATE billing_balance_settlements
		SET created_at = NOW() - INTERVAL '10 minutes',
			available_at = NOW() + INTERVAL '30 seconds',
			attempts = 9
		WHERE user_id = $1`, userID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec(`UPDATE billing_balance_settlements SET available_at = NOW() WHERE user_id = $1`, userID)
		integrationFlushUntilDrained(t, userID)
	})

	backlog, err := integrationBacklogReader(t).BalanceSettlementBacklog(ctx, 1000)
	require.NoError(t, err)
	require.Equal(t, int64(3), backlog.Pending)
	require.GreaterOrEqual(t, backlog.OldestAge, 9*time.Minute)
	require.Less(t, backlog.OldestAge, 11*time.Minute)
}

// 积压读取绝不能全表扫：队列表里保留着 15 分钟的已结算行，生产上是几十万行，
// 而待结算行只有几百。这里用同样形状的数据证明查询只走待结算部分索引、没有排序。
func TestBalanceSettlementBacklogQueryNeverScansTheWholeTable(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	userID, keyID := integrationBillingFixtures(t, 1)
	userID2, _ := integrationBillingFixtures(t, 1)
	// 200k already-applied rows (the retention window) and 300 pending ones across
	// interleaved users with chronological available_at (matching production shape so
	// idx_billing_balance_settlements_user_unsettled does not get an artificial single-user
	// correlation=1.0 + 1-page dedup advantage over idx_billing_balance_settlements_pending).
	_, err = tx.ExecContext(ctx, `
		INSERT INTO billing_balance_settlements
			(request_id, api_key_id, request_fingerprint, user_id, amount_usd, status, available_at, applied_at)
		SELECT 'explain-applied-' || g, $1, 'fp',
			CASE WHEN g % 2 = 0 THEN $2::bigint ELSE $3::bigint END,
			0.01, 4, NOW() + (g * INTERVAL '1 millisecond'), NOW()
		FROM generate_series(1, 200000) AS g`, keyID, userID, userID2)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO billing_balance_settlements
			(request_id, api_key_id, request_fingerprint, user_id, amount_usd, status, available_at)
		SELECT 'explain-pending-' || g, $1, 'fp',
			CASE WHEN g % 2 = 0 THEN $2::bigint ELSE $3::bigint END,
			0.01, 3, NOW() + ((200000 + g) * INTERVAL '1 millisecond')
		FROM generate_series(1, 300) AS g`, keyID, userID, userID2)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `ANALYZE billing_balance_settlements`)
	require.NoError(t, err)

	planOf := func() string {
		var plan string
		require.NoError(t, tx.QueryRowContext(ctx, `EXPLAIN (FORMAT JSON) `+balanceSettlementBacklogSQL,
			int64(20000), balanceSettlementOldestProbeRows).Scan(&plan))
		return plan
	}

	natural := planOf()
	require.NotContains(t, natural, "Seq Scan", "plan the production data shape would get:\n%s", natural)
	require.Contains(t, natural, "idx_billing_balance_settlements_pending")
	require.NotContains(t, natural, `"Node Type": "Sort"`, "the oldest-row probe takes the index order, it never sorts the pending set")

	// Under force_generic_plan (prepared statements), the literal status = 3 must
	// still match the partial index instead of falling back to a full table scan.
	_, err = tx.ExecContext(ctx, `SET LOCAL plan_cache_mode = force_generic_plan`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `PREPARE backlog_generic_plan(bigint, integer) AS `+balanceSettlementBacklogSQL)
	require.NoError(t, err)
	defer func() { _, _ = tx.Exec(`DEALLOCATE backlog_generic_plan`) }()
	var generic string
	require.NoError(t, tx.QueryRowContext(ctx,
		`EXPLAIN (FORMAT JSON) EXECUTE backlog_generic_plan(20000, 256)`).Scan(&generic))
	require.NotContains(t, generic, "Seq Scan", "generic plan must not scan the whole table:\n%s", generic)
	require.Contains(t, generic, "idx_billing_balance_settlements_pending")
	require.NotContains(t, generic, `"Node Type": "Sort"`)

	// With sequential scans forbidden the index must still be the way the query is
	// answered (it proves the status predicate really matches the partial index).
	_, err = tx.ExecContext(ctx, `SET LOCAL enable_seqscan = off`)
	require.NoError(t, err)
	forced := planOf()
	require.NotContains(t, forced, "Seq Scan")
	require.Contains(t, forced, "idx_billing_balance_settlements_pending")
}

func TestFlushPendingBalanceSettlementsReturnsRealAlertPreferences(t *testing.T) {
	ctx := context.Background()
	integrationDrainAllSettlements(t)
	startIntegrationSettlementConsumerFlag(t)
	repo := integrationMergeBillingRepo(t, true)

	customUser, customKey := integrationBillingFixtures(t, 100)
	_, err := integrationDB.ExecContext(ctx, `
		UPDATE users
		SET balance_notify_enabled = FALSE,
			balance_notify_threshold = 12.5,
			balance_notify_threshold_type = 'percentage',
			total_recharged = 250
		WHERE id = $1`, customUser)
	require.NoError(t, err)
	defaultUser, defaultKey := integrationBillingFixtures(t, 100)

	for i := 0; i < 3; i++ {
		integrationCharge(t, repo, customUser, customKey, fmt.Sprintf("alert-prefs-custom-%d-%d", customUser, i), 0.75)
	}
	integrationCharge(t, repo, defaultUser, defaultKey, fmt.Sprintf("alert-prefs-default-%d", defaultUser), 2)

	results, err := NewUsageBalanceSettlementRepository(integrationDB).FlushPendingBalanceSettlements(ctx, 100)
	require.NoError(t, err)
	byUser := map[int64]service.UsageBalanceSettlementResult{}
	for _, r := range results {
		byUser[r.UserID] = r
	}

	custom := byUser[customUser]
	require.Equal(t, 3, custom.EventCount)
	require.InDelta(t, 2.25, custom.Amount, 1e-9)
	require.InDelta(t, 97.75, custom.NewBalance, 1e-9)
	require.False(t, custom.NotifyEnabled)
	require.NotNil(t, custom.NotifyThreshold)
	require.InDelta(t, 12.5, *custom.NotifyThreshold, 1e-9)
	require.Equal(t, "percentage", custom.NotifyThresholdType)
	require.InDelta(t, 250.0, custom.TotalRecharged, 1e-9)

	def := byUser[defaultUser]
	require.InDelta(t, 98.0, def.NewBalance, 1e-9)
	require.True(t, def.NotifyEnabled, "alerts are on by default")
	require.Nil(t, def.NotifyThreshold, "NULL means the global threshold applies")
	require.Equal(t, "fixed", def.NotifyThresholdType)
	require.Zero(t, def.TotalRecharged)
}

// 完整链路（真 PG + 真结算 worker + 真积压读数）：
// 队列被 stall 住 → 积压超阈值 → 新请求退回同步扣费（余额立刻变化、不再进队列）→
// 队列恢复排空 → 回落到阈值一半以下后重新入队。整个过程结束后的余额必须精确等于
// 「全部逐请求同步扣」的对照用户，没有丢扣、没有重复扣。
func TestBacklogFallbackEndToEndKeepsTheFinalBalanceExact(t *testing.T) {
	ctx := context.Background()
	integrationDrainAllSettlements(t)

	stall := newStallableSettlementRepo(t)
	stall.stalled.Store(true)
	worker := service.NewUsageBalanceSettlementWorkerWithOptions(stall, service.UsageBalanceSettlementWorkerOptions{
		MergeEnabled:          true,
		MaxBacklog:            20,
		BacklogSampleInterval: 10 * time.Millisecond,
	})
	t.Cleanup(worker.Stop)

	repo := integrationMergeBillingRepo(t, true)
	control := integrationMergeBillingRepo(t, false)
	const initial = 1000.0
	const charge = 1.25
	userID, keyID := integrationBillingFixtures(t, initial)
	controlUser, controlKey := integrationBillingFixtures(t, initial)

	var requestIDs []string
	var queued, synchronous int
	do := func(label string) *service.UsageBillingApplyResult {
		requestID := fmt.Sprintf("fallback-e2e-%d-%s-%d", userID, label, len(requestIDs))
		requestIDs = append(requestIDs, requestID)
		result := integrationCharge(t, repo, userID, keyID, requestID, charge)
		if result.BalanceSettlementDeferred {
			require.Nil(t, result.NewBalance, "a queued charge never also deducts")
			queued++
		} else {
			require.NotNil(t, result.NewBalance, "a synchronous charge reports the balance it just wrote")
			synchronous++
		}
		return result
	}

	// Phase 1: the worker is stalled, so queued charges pile up until the guard trips.
	for i := 0; i < 500 && !service.UsageBalanceSettlementBacklogFallbackActive(); i++ {
		do("fill")
		time.Sleep(2 * time.Millisecond)
	}
	require.True(t, service.UsageBalanceSettlementBacklogFallbackActive(), "a backed-up queue must trip the fallback")
	require.Greater(t, queued, 20)
	health := worker.Health(ctx)
	require.True(t, health.FallbackActive)
	require.Greater(t, health.Pending, int64(20))
	chargesBefore := health.FallbackCharges

	// Phase 2: still stalled, in fallback. Every new charge hits users.balance
	// directly: visible immediately, nothing new in the queue.
	rowsBefore := integrationSettlementRows(t, userID)
	syncBefore := synchronous
	for i := 0; i < 10; i++ {
		result := do("sync")
		require.False(t, result.BalanceSettlementDeferred)
		require.InDelta(t, initial-charge*float64(synchronous), *result.NewBalance, 1e-9,
			"NewBalance is the row's balance: initial minus the synchronous charges only (queued ones are not applied yet)")
	}
	require.Equal(t, syncBefore+10, synchronous)
	require.Equal(t, rowsBefore, integrationSettlementRows(t, userID), "fallback charges bypass the queue")
	require.Equal(t, fmt.Sprintf("%.8f", initial-charge*float64(synchronous)), integrationBalanceText(t, userID),
		"queued charges are still waiting, synchronous ones are already in the balance")
	require.Equal(t, chargesBefore+10, worker.Health(ctx).FallbackCharges)

	// Phase 3: the worker recovers. The queue drains, the backlog falls below half of
	// the limit and new charges are queued again.
	stall.stalled.Store(false)
	require.Eventually(t, func() bool {
		return !service.UsageBalanceSettlementBacklogFallbackActive() && integrationPendingTotal(t) == 0
	}, 30*time.Second, 20*time.Millisecond, "the queue must drain and the fallback must release")
	require.Equal(t, fmt.Sprintf("%.8f", initial-charge*float64(queued+synchronous)), integrationBalanceText(t, userID),
		"after the drain every charge is in the balance exactly once")

	queuedBefore := queued
	for i := 0; i < 5; i++ {
		result := do("after")
		require.True(t, result.BalanceSettlementDeferred, "recovered: charges queue again")
	}
	require.Equal(t, queuedBefore+5, queued)
	require.Eventually(t, func() bool { return integrationPendingTotal(t) == 0 }, 30*time.Second, 20*time.Millisecond)

	// The control user is charged the same amounts one by one, synchronously.
	for i := range requestIDs {
		integrationCharge(t, control, controlUser, controlKey, fmt.Sprintf("fallback-e2e-control-%d-%d", controlUser, i), charge)
	}
	total := len(requestIDs)
	want := fmt.Sprintf("%.8f", initial-charge*float64(total))
	require.Equal(t, want, integrationBalanceText(t, controlUser))
	require.Equal(t, want, integrationBalanceText(t, userID), "final balance is exactly the all-synchronous result")

	// Exactly one billing claim per request, one queue row per queued charge, and
	// replaying any request charges nothing.
	var claims int
	require.NoError(t, integrationDB.QueryRow(
		`SELECT COUNT(*) FROM usage_billing_dedup WHERE api_key_id = $1`, keyID).Scan(&claims))
	require.Equal(t, total, claims)
	require.Equal(t, queued, integrationSettlementRows(t, userID))
	for _, requestID := range requestIDs {
		replay, err := repo.Apply(ctx, &service.UsageBillingCommand{
			RequestID: requestID, APIKeyID: keyID, UserID: userID, AccountType: service.AccountTypeAPIKey, BalanceCost: charge,
		})
		require.NoError(t, err)
		require.False(t, replay.Applied, "replay of %s", requestID)
	}
	require.Equal(t, want, integrationBalanceText(t, userID))
}

// 退回期间同时有新请求和 worker 结算：很多并发请求、积压阈值极小（保护反复进出）、
// worker 真实运行。不管每一笔走了队列还是同步，终值必须精确、每个请求只扣一次。
func TestBacklogFallbackWithConcurrentChargesAndALiveWorkerIsExact(t *testing.T) {
	ctx := context.Background()
	integrationDrainAllSettlements(t)

	worker := service.NewUsageBalanceSettlementWorkerWithOptions(NewUsageBalanceSettlementRepository(integrationDB),
		service.UsageBalanceSettlementWorkerOptions{
			MergeEnabled:          true,
			MaxBacklog:            3,
			BacklogSampleInterval: 2 * time.Millisecond,
		})
	t.Cleanup(worker.Stop)
	repo := integrationMergeBillingRepo(t, true)

	const goroutines = 8
	const perGoroutine = 100
	const charge = 0.01
	const initial = 500.0
	type account struct{ userID, keyID int64 }
	accounts := []account{}
	for i := 0; i < 2; i++ {
		userID, keyID := integrationBillingFixtures(t, initial)
		accounts = append(accounts, account{userID, keyID})
	}

	var wg sync.WaitGroup
	chargesBefore := worker.Health(ctx).FallbackCharges
	eventsBefore := worker.Health(ctx).Events
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			acc := accounts[g%len(accounts)]
			for i := 0; i < perGoroutine; i++ {
				_, err := repo.Apply(ctx, &service.UsageBillingCommand{
					RequestID:   fmt.Sprintf("fallback-live-%d-%d-%d", acc.userID, g, i),
					APIKeyID:    acc.keyID,
					UserID:      acc.userID,
					AccountType: service.AccountTypeAPIKey,
					BalanceCost: charge,
				})
				if err != nil {
					t.Errorf("charge failed: %v", err)
					return
				}
				// Spread the load over several settlement polls and sampling ticks so the
				// guard has time to react while charges are still arriving.
				time.Sleep(2 * time.Millisecond)
			}
		}(g)
	}
	wg.Wait()
	require.False(t, t.Failed())
	require.Eventually(t, func() bool { return integrationPendingTotal(t) == 0 }, 30*time.Second, 20*time.Millisecond)

	perAccount := (goroutines / len(accounts)) * perGoroutine
	want := fmt.Sprintf("%.8f", initial-charge*float64(perAccount))
	for _, acc := range accounts {
		require.Equal(t, want, integrationBalanceText(t, acc.userID))
		var claims int
		require.NoError(t, integrationDB.QueryRow(
			`SELECT COUNT(*) FROM usage_billing_dedup WHERE api_key_id = $1`, acc.keyID).Scan(&claims))
		require.Equal(t, perAccount, claims)
		var duplicated int
		require.NoError(t, integrationDB.QueryRow(`
			SELECT COUNT(*) FROM (
				SELECT request_id, api_key_id FROM billing_balance_settlements
				WHERE user_id = $1 GROUP BY request_id, api_key_id HAVING COUNT(*) > 1
			) AS dup`, acc.userID).Scan(&duplicated))
		require.Zero(t, duplicated)
		require.LessOrEqual(t, integrationSettlementRows(t, acc.userID), perAccount)
	}
	health := worker.Health(ctx)
	require.Greater(t, health.FallbackCharges, chargesBefore, "the guard really kicked in during the run")
	require.Greater(t, health.Events, eventsBefore, "and the worker really settled queued charges")
}

type integrationObserverSpy struct {
	mu      sync.Mutex
	results []service.UsageBalanceSettlementResult
}

func (s *integrationObserverSpy) ObserveBalanceSettlements(_ context.Context, results []service.UsageBalanceSettlementResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append(s.results, results...)
}

func (s *integrationObserverSpy) forUser(userID int64) []service.UsageBalanceSettlementResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []service.UsageBalanceSettlementResult
	for _, r := range s.results {
		if r.UserID == userID {
			out = append(out, r)
		}
	}
	return out
}

// 低余额观察者拿到的（结算前, 结算后）余额必须是真实的：第一条的「结算前」是用户原来的余额，
// 相邻两次 flush 首尾相接，最后一条的「结算后」就是最终余额。
func TestSettlementObserverSeesExactBalancesFromARealFlush(t *testing.T) {
	integrationDrainAllSettlements(t)
	spy := &integrationObserverSpy{}
	worker := service.NewUsageBalanceSettlementWorkerWithOptions(NewUsageBalanceSettlementRepository(integrationDB),
		service.UsageBalanceSettlementWorkerOptions{MergeEnabled: true, Observer: spy})
	t.Cleanup(worker.Stop)
	require.True(t, service.UsageBalanceSettlementNotifierActive())
	repo := integrationMergeBillingRepo(t, true)
	userID, keyID := integrationBillingFixtures(t, 20)

	for i := 0; i < 8; i++ {
		integrationCharge(t, repo, userID, keyID, fmt.Sprintf("observer-real-%d-%d", userID, i), 1.25)
		if i == 3 {
			time.Sleep(400 * time.Millisecond) // let at least one flush run in the middle
		}
	}
	require.Eventually(t, func() bool {
		events := 0
		for _, r := range spy.forUser(userID) {
			events += r.EventCount
		}
		return events == 8
	}, 30*time.Second, 20*time.Millisecond)

	results := spy.forUser(userID)
	require.GreaterOrEqual(t, len(results), 2, "the charges were coalesced into more than one flush")
	require.InDelta(t, 20.0, results[0].NewBalance+results[0].Amount, 1e-9, "balance before the first flush")
	for i := 1; i < len(results); i++ {
		require.InDelta(t, results[i-1].NewBalance, results[i].NewBalance+results[i].Amount, 1e-9, "flushes are contiguous")
	}
	require.InDelta(t, 10.0, results[len(results)-1].NewBalance, 1e-9)
	require.Equal(t, "10.00000000", integrationBalanceText(t, userID))
	for _, r := range results {
		require.True(t, r.NotifyEnabled)
		require.Equal(t, "fixed", r.NotifyThresholdType)
	}
}

func TestSettlementHealthReportsTheRealQueue(t *testing.T) {
	ctx := context.Background()
	integrationDrainAllSettlements(t)
	stall := newStallableSettlementRepo(t)
	stall.stalled.Store(true)
	worker := service.NewUsageBalanceSettlementWorkerWithOptions(stall, service.UsageBalanceSettlementWorkerOptions{
		MergeEnabled: true, MaxBacklog: 1000, MaxAge: time.Hour,
	})
	t.Cleanup(worker.Stop)
	repo := integrationMergeBillingRepo(t, true)
	userID, keyID := integrationBillingFixtures(t, 50)
	for i := 0; i < 4; i++ {
		integrationCharge(t, repo, userID, keyID, fmt.Sprintf("health-real-%d-%d", userID, i), 0.5)
	}

	health := worker.Health(ctx)
	require.True(t, health.MergeEnabled)
	require.True(t, health.ConsumerRunning)
	require.Equal(t, int64(4), health.Pending)
	require.False(t, health.FallbackActive)
	require.Equal(t, int64(1000), health.MaxBacklog)
	require.Equal(t, int64(3600), health.MaxAgeSeconds)
	require.Empty(t, health.StatsError)

	stall.stalled.Store(false)
	require.Eventually(t, func() bool { return integrationPendingTotal(t) == 0 }, 30*time.Second, 20*time.Millisecond)
}
