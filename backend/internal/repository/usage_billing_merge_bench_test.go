//go:build benchmerge

// Billing hot-path benchmark for the balance merge settlement feature.
//
// Run against a throwaway PostgreSQL instance:
//
//	BENCH_DATABASE_URL='postgres://<user>:<password>@<host>:5432/postgres?sslmode=disable' \
//	  go test -tags benchmerge -run TestBillingMergeSettlementBenchmark -v ./internal/repository
//
// The benchmark compares the legacy per-request `UPDATE users SET balance` path
// (billing.balance_merge_settlement_enabled=false) with the durable coalesced
// settlement queue (true) for a single hot user at 50 and 200 concurrent
// billing transactions. It also verifies that both paths converge on the exact
// same balance and that queued charges survive a worker restart.
//
// This file is not part of the normal build or test suite; it requires a real
// PostgreSQL because the whole point is measuring row-lock contention.
package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	_ "github.com/lib/pq"
)

const benchWarmupRequests = 100

// benchSettlementWorker 是测试内启动的结算消费者；停止它即可验证「无消费者时
// 退回同步扣费」的安全兜底。
var benchSettlementWorker *service.UsageBalanceSettlementWorker

func TestBillingMergeSettlementBenchmark(t *testing.T) {
	dsn := os.Getenv("BENCH_DATABASE_URL")
	if dsn == "" {
		t.Skip("set BENCH_DATABASE_URL to a throwaway PostgreSQL to run the billing merge benchmark")
	}
	ctx := context.Background()
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	// 200 concurrent billing transactions need a pool at least that large,
	// otherwise the benchmark would measure sql.DB queueing instead of the
	// users.balance hot row.
	db.SetMaxOpenConns(240)
	db.SetMaxIdleConns(240)

	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()

	workerRepo := NewUsageBalanceSettlementRepository(db)
	benchSettlementWorker = service.NewUsageBalanceSettlementWorker(workerRepo)
	defer benchSettlementWorker.Stop()
	if !service.UsageBalanceSettlementConsumerRunning() {
		t.Fatal("settlement worker did not start; benchmark would measure the synchronous fallback")
	}

	mergeRepo := ProvideUsageBillingRepository(nil, db, &config.Config{Billing: config.BillingConfig{
		BalanceMergeSettlementEnabled: true,
	}})
	legacyRepo := ProvideUsageBillingRepository(nil, db, &config.Config{Billing: config.BillingConfig{
		BalanceMergeSettlementEnabled: false,
	}})

	const amount = 0.001
	scenarios := []struct {
		name        string
		concurrency int
		total       int
		merge       bool
		apiKeyQuota bool
	}{
		{name: "legacy-50", concurrency: 50, total: 2000},
		{name: "merge-50", concurrency: 50, total: 2000, merge: true},
		{name: "legacy-200", concurrency: 200, total: 4000},
		{name: "merge-200", concurrency: 200, total: 4000, merge: true},
		{name: "legacy-200-api-key-quota", concurrency: 200, total: 4000, apiKeyQuota: true},
		{name: "merge-200-api-key-quota", concurrency: 200, total: 4000, merge: true, apiKeyQuota: true},
	}

	fmt.Printf("\n=== billing merge settlement benchmark (single user, amount=%.4f USD) ===\n", amount)
	fmt.Printf("%-28s %7s %9s %9s %9s %14s\n", "scenario", "req", "p50_ms", "p99_ms", "rps", "final_balance")
	for _, sc := range scenarios {
		repo := legacyRepo
		if sc.merge {
			repo = mergeRepo
		}
		initialBalance := 100000.0
		userID, keyID := createBenchBillingFixtures(t, ctx, client, initialBalance, sc.apiKeyQuota)

		before := benchSettlementWorker.Stats()
		res := runApplyLoad(t, ctx, repo, userID, keyID, sc.concurrency, sc.total, amount, sc.apiKeyQuota)
		if sc.merge {
			waitPendingSettlements(t, ctx, db, userID, 60*time.Second)
		}
		after := benchSettlementWorker.Stats()
		want := initialBalance - float64(sc.total+benchWarmupRequests)*amount
		balance, balanceText := readBalance(t, ctx, db, userID)
		if balance < want-1e-6 || balance > want+1e-6 {
			t.Fatalf("%s: final balance %.10f != expected %.10f", sc.name, balance, want)
		}
		fmt.Printf("%-28s %7d %9.2f %9.2f %9.0f %14s\n",
			sc.name, sc.total,
			percentileMS(res.latencies, 0.50), percentileMS(res.latencies, 0.99),
			res.rps, balanceText)
		if sc.merge {
			flushes := after.Flushes - before.Flushes
			events := after.Events - before.Events
			users := after.Users - before.Users
			ratio := 0.0
			if flushes > 0 {
				ratio = float64(events) / float64(flushes)
			}
			fmt.Printf("    worker: flushes=%d events=%d users_updated=%d events_per_flush=%.2f\n",
				flushes, events, users, ratio)
			reportSettlementLag(t, ctx, db, userID)
		}
	}

	verifyDuplicateRequestChargedOnce(t, ctx, mergeRepo, client)
	verifyApplyCommitDurability(t, ctx, db, client, mergeRepo)
	verifyPendingRowsDrainAfterRestart(t, ctx, db, client, workerRepo, amount)
	verifySynchronousFallbackWithoutConsumer(t, ctx, db, mergeRepo, client, amount)
}

type applyLoadResult struct {
	latencies []time.Duration
	rps       float64
}

func runApplyLoad(t *testing.T, ctx context.Context, repo service.UsageBillingRepository, userID, keyID int64, concurrency, total int, amount float64, apiKeyQuota bool) applyLoadResult {
	t.Helper()
	// Warmup: pay the one-time statement-cache / pool ramp-up cost outside the
	// measured window so both modes start from the same state.
	for i := 0; i < benchWarmupRequests; i++ {
		cmd := benchBillingCommand("bench-warmup-"+strconv.FormatInt(userID, 10)+"-"+strconv.Itoa(i), userID, keyID, amount, apiKeyQuota)
		if _, err := repo.Apply(ctx, cmd); err != nil {
			t.Fatalf("warmup apply: %v", err)
		}
	}
	perWorker := total / concurrency
	if perWorker < 1 {
		perWorker = 1
	}
	latencies := make([]time.Duration, 0, perWorker*concurrency)
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			local := make([]time.Duration, 0, perWorker)
			for i := 0; i < perWorker; i++ {
				requestID := "bench-" + strconv.FormatInt(userID, 10) + "-" + strconv.Itoa(worker) + "-" + strconv.Itoa(i)
				cmd := benchBillingCommand(requestID, userID, keyID, amount, apiKeyQuota)
				t0 := time.Now()
				if _, err := repo.Apply(ctx, cmd); err != nil {
					t.Errorf("apply %s: %v", requestID, err)
					return
				}
				local = append(local, time.Since(t0))
			}
			mu.Lock()
			latencies = append(latencies, local...)
			mu.Unlock()
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(start)
	if len(latencies) != perWorker*concurrency {
		t.Fatalf("collected %d latencies, want %d", len(latencies), perWorker*concurrency)
	}
	return applyLoadResult{latencies: latencies, rps: float64(len(latencies)) / elapsed.Seconds()}
}

func benchBillingCommand(requestID string, userID, keyID int64, amount float64, apiKeyQuota bool) *service.UsageBillingCommand {
	cmd := &service.UsageBillingCommand{
		RequestID:   requestID,
		APIKeyID:    keyID,
		UserID:      userID,
		AccountType: service.AccountTypeAPIKey,
		Model:       "bench-model",
		BalanceCost: amount,
	}
	if apiKeyQuota {
		cmd.APIKeyQuotaCost = amount
		cmd.APIKeyRateLimitCost = amount
	}
	return cmd
}

func createBenchBillingFixtures(t *testing.T, ctx context.Context, client *dbent.Client, balance float64, quota bool) (userID, keyID int64) {
	t.Helper()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	user, err := client.User.Create().
		SetEmail("bench-merge-" + suffix + "@example.com").
		SetPasswordHash("bench").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		SetBalance(balance).
		SetConcurrency(1000).
		Save(ctx)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	create := client.APIKey.Create().
		SetUserID(user.ID).
		SetKey("sk-bench-merge-" + suffix).
		SetName("bench").
		SetStatus(service.StatusActive)
	if quota {
		// Large quota so the key never becomes exhausted during the load test; the
		// point is the api_keys row update contention, not the exhaustion transition.
		create.SetQuota(1e9)
	}
	key, err := create.Save(ctx)
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}
	return user.ID, key.ID
}

func readBalance(t *testing.T, ctx context.Context, db *sql.DB, userID int64) (float64, string) {
	t.Helper()
	var text string
	if err := db.QueryRowContext(ctx, `SELECT balance::text FROM users WHERE id = $1`, userID).Scan(&text); err != nil {
		t.Fatalf("read balance: %v", err)
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		t.Fatalf("parse balance %q: %v", text, err)
	}
	return value, text
}

func countPendingSettlements(t *testing.T, ctx context.Context, db *sql.DB, userID int64) int {
	t.Helper()
	var pending int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM billing_balance_settlements WHERE user_id = $1 AND status = $2`,
		userID, service.BalanceSettlementPending).Scan(&pending); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	return pending
}

func waitPendingSettlements(t *testing.T, ctx context.Context, db *sql.DB, userID int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if countPendingSettlements(t, ctx, db, userID) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pending balance settlements for user %d did not drain", userID)
}

func reportSettlementLag(t *testing.T, ctx context.Context, db *sql.DB, userID int64) {
	t.Helper()
	var p50, p99 sql.NullFloat64
	var events int
	if err := db.QueryRowContext(ctx, `
		SELECT
			percentile_cont(0.50) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (applied_at - created_at)) * 1000),
			percentile_cont(0.99) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (applied_at - created_at)) * 1000),
			COUNT(*)
		FROM billing_balance_settlements
		WHERE user_id = $1 AND applied_at IS NOT NULL
	`, userID).Scan(&p50, &p99, &events); err != nil {
		t.Fatalf("settlement lag: %v", err)
	}
	fmt.Printf("    settlement lag (queued -> applied): events=%d p50=%.1fms p99=%.1fms\n", events, p50.Float64, p99.Float64)
}

func percentileMS(latencies []time.Duration, q float64) float64 {
	if len(latencies) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(latencies))
	copy(sorted, latencies)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(q * float64(len(sorted)-1))
	return float64(sorted[idx].Microseconds()) / 1000.0
}

// verifyDuplicateRequestChargedOnce mirrors the retry semantics of the unique
// (request_id, api_key_id) identity: the second Apply must not deduct again and
// must not enqueue a second settlement.
func verifyDuplicateRequestChargedOnce(t *testing.T, ctx context.Context, repo service.UsageBillingRepository, client *dbent.Client) {
	t.Helper()
	userID, keyID := createBenchBillingFixtures(t, ctx, client, 100, false)
	cmd := benchBillingCommand("bench-duplicate-"+strconv.FormatInt(userID, 10), userID, keyID, 0.5, false)
	first, err := repo.Apply(ctx, cmd)
	if err != nil || first == nil || !first.Applied {
		t.Fatalf("first apply: result=%+v err=%v", first, err)
	}
	second, err := repo.Apply(ctx, cmd)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if second == nil || second.Applied {
		t.Fatalf("duplicate apply was billed twice: %+v", second)
	}
	db := repo.(*usageBillingRepository).db
	var rows int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM billing_balance_settlements WHERE request_id = $1 AND api_key_id = $2`,
		cmd.RequestID, keyID).Scan(&rows); err != nil {
		t.Fatalf("count settlements: %v", err)
	}
	if rows != 1 {
		t.Fatalf("duplicate charge created %d settlement rows, want 1", rows)
	}
	waitPendingSettlements(t, ctx, db, userID, 30*time.Second)
	balance, _ := readBalance(t, ctx, db, userID)
	if balance < 99.5-1e-6 || balance > 99.5+1e-6 {
		t.Fatalf("duplicate request balance %.10f, want 99.5", balance)
	}
	fmt.Printf("\nduplicate request: billed once (balance=%s, settlement_rows=1)\n", strconv.FormatFloat(balance, 'f', 8, 64))
}

// verifyApplyCommitDurability checks that every charge committed by Apply while
// the consumer was running still exists as a billing_balance_settlements row
// after the consumer is stopped -- i.e. the charge survives a crash in the
// window between the billing commit and the balance flush.
func verifyApplyCommitDurability(t *testing.T, ctx context.Context, db *sql.DB, client *dbent.Client, repo service.UsageBillingRepository) {
	t.Helper()
	userID, keyID := createBenchBillingFixtures(t, ctx, client, 50, false)
	const burst = 200
	for i := 0; i < burst; i++ {
		cmd := benchBillingCommand("bench-durable-"+strconv.FormatInt(userID, 10)+"-"+strconv.Itoa(i), userID, keyID, 0.001, false)
		if _, err := repo.Apply(ctx, cmd); err != nil {
			t.Fatalf("apply burst %d: %v", i, err)
		}
	}
	benchSettlementWorker.Stop()
	var stored int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM billing_balance_settlements WHERE user_id = $1`, userID).Scan(&stored); err != nil {
		t.Fatalf("count durable rows: %v", err)
	}
	if stored != burst {
		t.Fatalf("durable settlement rows = %d, want %d (a charge was lost)", stored, burst)
	}
	fmt.Printf("apply durability: %d/%d committed charges persisted in billing_balance_settlements\n", stored, burst)
}

// verifyPendingRowsDrainAfterRestart simulates a process that died before
// flushing: the durable pending rows are inserted in the exact shape Apply
// commits, then a brand new settlement consumer must drain them and produce the
// exact final balance.
func verifyPendingRowsDrainAfterRestart(t *testing.T, ctx context.Context, db *sql.DB, client *dbent.Client, workerRepo service.UsageBalanceSettlementRepository, amount float64) {
	t.Helper()
	userID, keyID := createBenchBillingFixtures(t, ctx, client, 50, false)
	const pendingRows = 500
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for i := 0; i < pendingRows; i++ {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO billing_balance_settlements (request_id, api_key_id, request_fingerprint, user_id, amount_usd, status)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, "bench-pending-"+strconv.FormatInt(userID, 10)+"-"+strconv.Itoa(i), keyID,
			"bench-fingerprint", userID, amount, service.BalanceSettlementPending); err != nil {
			_ = tx.Rollback()
			t.Fatalf("insert pending %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit pending rows: %v", err)
	}
	if got := countPendingSettlements(t, ctx, db, userID); got != pendingRows {
		t.Fatalf("pending rows = %d, want %d", got, pendingRows)
	}

	restarted := service.NewUsageBalanceSettlementWorker(workerRepo)
	if !service.UsageBalanceSettlementConsumerRunning() {
		t.Fatal("restarted settlement worker is not running")
	}
	defer restarted.Stop()
	waitPendingSettlements(t, ctx, db, userID, 30*time.Second)
	balance, _ := readBalance(t, ctx, db, userID)
	want := 50.0 - float64(pendingRows)*amount
	if balance < want-1e-6 || balance > want+1e-6 {
		t.Fatalf("after restart drain balance %.10f != expected %.10f", balance, want)
	}
	fmt.Printf("restart durability: %d queued charges replayed by a new consumer, balance=%s\n",
		pendingRows, strconv.FormatFloat(balance, 'f', 8, 64))
}

// verifySynchronousFallbackWithoutConsumer proves the safety net: when the
// merge flag is on but no consumer is running, Apply must fall back to the
// synchronous deduction instead of leaving the charge stranded in the queue.
func verifySynchronousFallbackWithoutConsumer(t *testing.T, ctx context.Context, db *sql.DB, repo service.UsageBillingRepository, client *dbent.Client, amount float64) {
	t.Helper()
	userID, keyID := createBenchBillingFixtures(t, ctx, client, 10, false)
	cmd := benchBillingCommand("bench-fallback-"+strconv.FormatInt(userID, 10), userID, keyID, amount, false)
	result, err := repo.Apply(ctx, cmd)
	if err != nil {
		t.Fatalf("fallback apply: %v", err)
	}
	if service.UsageBalanceSettlementConsumerRunning() {
		t.Fatal("consumer unexpectedly running during fallback check")
	}
	balance, _ := readBalance(t, ctx, db, userID)
	if result.NewBalance == nil || *result.NewBalance != balance {
		t.Fatalf("fallback did not deduct synchronously: result=%+v db_balance=%.10f", result, balance)
	}
	if pending := countPendingSettlements(t, ctx, db, userID); pending != 0 {
		t.Fatalf("fallback left %d queued settlements", pending)
	}
	fmt.Printf("no-consumer fallback: synchronous deduction confirmed, pending_rows=0\n")
}
