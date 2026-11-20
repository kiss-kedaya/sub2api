//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// integrationNoopSettlementRepo只用来把「本进程存在结算消费者」的进程级标志置为 true，
// 让计费仓储走合并结算入队路径，但不消费队列。需要确定性断言时由测试自己调用
// FlushPendingBalanceSettlements。
type integrationNoopSettlementRepo struct{}

func (integrationNoopSettlementRepo) FlushPendingBalanceSettlements(context.Context, int) ([]service.UsageBalanceSettlementResult, error) {
	return nil, nil
}

func (integrationNoopSettlementRepo) DeleteAppliedBalanceSettlements(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func startIntegrationSettlementConsumerFlag(t *testing.T) {
	t.Helper()
	worker := service.NewUsageBalanceSettlementWorker(integrationNoopSettlementRepo{})
	require.True(t, service.UsageBalanceSettlementConsumerRunning())
	t.Cleanup(worker.Stop)
}

func integrationMergeBillingRepo(t *testing.T, merge bool) service.UsageBillingRepository {
	t.Helper()
	return ProvideUsageBillingRepository(integrationEntClient, integrationDB, &config.Config{
		Billing: config.BillingConfig{BalanceMergeSettlementEnabled: merge},
	})
}

func integrationBillingFixtures(t *testing.T, balance float64) (int64, int64) {
	t.Helper()
	user := mustCreateUser(t, integrationEntClient, &service.User{
		Email:        fmt.Sprintf("merge-settlement-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      balance,
	})
	apiKey := mustCreateApiKey(t, integrationEntClient, &service.APIKey{
		UserID: user.ID,
		Key:    fmt.Sprintf("sk-merge-%d", time.Now().UnixNano()),
		Name:   "merge-settlement",
	})
	return user.ID, apiKey.ID
}

func integrationBalanceText(t *testing.T, userID int64) string {
	t.Helper()
	var text string
	require.NoError(t, integrationDB.QueryRow(
		`SELECT balance::text FROM users WHERE id = $1`, userID).Scan(&text))
	return text
}

func integrationPendingSettlements(t *testing.T, userID int64) int {
	t.Helper()
	var pending int
	require.NoError(t, integrationDB.QueryRow(
		`SELECT COUNT(*) FROM billing_balance_settlements WHERE user_id = $1 AND status = $2`,
		userID, service.BalanceSettlementPending).Scan(&pending))
	return pending
}

func integrationFlushUntilDrained(t *testing.T, userID int64) {
	t.Helper()
	settlementRepo := NewUsageBalanceSettlementRepository(integrationDB)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_, err := settlementRepo.FlushPendingBalanceSettlements(context.Background(), 2000)
		require.NoError(t, err)
		if integrationPendingSettlements(t, userID) == 0 {
			return
		}
	}
	t.Fatalf("pending settlements for user %d did not drain", userID)
}

func TestUsageBillingMergeSettlementQueuesAndFlushCoalescesUser(t *testing.T) {
	ctx := context.Background()
	startIntegrationSettlementConsumerFlag(t)
	repo := integrationMergeBillingRepo(t, true)
	userID, keyID := integrationBillingFixtures(t, 100)

	const charges = 5
	for i := 0; i < charges; i++ {
		result, err := repo.Apply(ctx, &service.UsageBillingCommand{
			RequestID:   fmt.Sprintf("merge-coalesce-%d-%d", userID, i),
			APIKeyID:    keyID,
			UserID:      userID,
			AccountType: service.AccountTypeAPIKey,
			BalanceCost: 0.75,
		})
		require.NoError(t, err)
		require.True(t, result.Applied)
		require.True(t, result.BalanceSettlementDeferred)
		require.Nil(t, result.NewBalance)
	}

	// Nothing may touch users.balance inside the billing transaction.
	require.Equal(t, "100.00000000", integrationBalanceText(t, userID))
	require.Equal(t, charges, integrationPendingSettlements(t, userID))

	settlementRepo := NewUsageBalanceSettlementRepository(integrationDB)
	results, err := settlementRepo.FlushPendingBalanceSettlements(ctx, 100)
	require.NoError(t, err)
	require.Len(t, results, 1, "all charges of one user must coalesce into a single balance update")
	require.Equal(t, userID, results[0].UserID)
	require.Equal(t, charges, results[0].EventCount)
	require.InDelta(t, 3.75, results[0].Amount, 1e-9)
	require.Equal(t, "96.25000000", integrationBalanceText(t, userID))
	require.Equal(t, 0, integrationPendingSettlements(t, userID))

	var applied int
	require.NoError(t, integrationDB.QueryRow(
		`SELECT COUNT(*) FROM billing_balance_settlements WHERE user_id = $1 AND status = $2`,
		userID, service.BalanceSettlementApplied).Scan(&applied))
	require.Equal(t, charges, applied)

	// Replaying the same request id must not enqueue or deduct a second time.
	replay, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:   fmt.Sprintf("merge-coalesce-%d-0", userID),
		APIKeyID:    keyID,
		UserID:      userID,
		AccountType: service.AccountTypeAPIKey,
		BalanceCost: 0.75,
	})
	require.NoError(t, err)
	require.False(t, replay.Applied)
	require.Equal(t, "96.25000000", integrationBalanceText(t, userID))
	require.Equal(t, charges, integrationSettlementRows(t, userID))
}

func integrationSettlementRows(t *testing.T, userID int64) int {
	t.Helper()
	var rows int
	require.NoError(t, integrationDB.QueryRow(
		`SELECT COUNT(*) FROM billing_balance_settlements WHERE user_id = $1`, userID).Scan(&rows))
	return rows
}

func TestUsageBillingMergeSettlementConcurrentChargesAreExactAndOnce(t *testing.T) {
	ctx := context.Background()
	startIntegrationSettlementConsumerFlag(t)
	repo := integrationMergeBillingRepo(t, true)
	userID, keyID := integrationBillingFixtures(t, 100)

	const goroutines = 50
	const perGoroutine = 20
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				_, err := repo.Apply(ctx, &service.UsageBillingCommand{
					RequestID:   fmt.Sprintf("merge-concurrent-%d-%d-%d", userID, g, i),
					APIKeyID:    keyID,
					UserID:      userID,
					AccountType: service.AccountTypeAPIKey,
					BalanceCost: 0.001,
				})
				require.NoError(t, err)
			}
		}(g)
	}
	wg.Wait()
	integrationFlushUntilDrained(t, userID)

	// 1000 charges of 0.001 = exactly 1.00000000, regardless of how they were batched.
	require.Equal(t, "99.00000000", integrationBalanceText(t, userID))
	require.Equal(t, goroutines*perGoroutine, integrationSettlementRows(t, userID))

	var duplicated int
	require.NoError(t, integrationDB.QueryRow(`
		SELECT COUNT(*) FROM (
			SELECT request_id, api_key_id, COUNT(*) AS c
			FROM billing_balance_settlements
			WHERE user_id = $1
			GROUP BY request_id, api_key_id
			HAVING COUNT(*) > 1
		) AS dup`, userID).Scan(&duplicated))
	require.Zero(t, duplicated)
}

func TestUsageBillingMergeSettlementRestartDrainsPendingRows(t *testing.T) {
	ctx := context.Background()
	startIntegrationSettlementConsumerFlag(t)
	repo := integrationMergeBillingRepo(t, true)
	userID, keyID := integrationBillingFixtures(t, 50)

	const charges = 200
	for i := 0; i < charges; i++ {
		_, err := repo.Apply(ctx, &service.UsageBillingCommand{
			RequestID:   fmt.Sprintf("merge-restart-%d-%d", userID, i),
			APIKeyID:    keyID,
			UserID:      userID,
			AccountType: service.AccountTypeAPIKey,
			BalanceCost: 0.01,
		})
		require.NoError(t, err)
	}
	require.Equal(t, charges, integrationPendingSettlements(t, userID))

	// A brand new consumer (the restarted process) must drain the durable rows.
	worker := service.NewUsageBalanceSettlementWorker(NewUsageBalanceSettlementRepository(integrationDB))
	t.Cleanup(worker.Stop)
	integrationFlushUntilDrained(t, userID)
	require.Equal(t, "48.00000000", integrationBalanceText(t, userID))
}

func TestUsageBillingMergeSettlementFlagOffDeductsSynchronously(t *testing.T) {
	ctx := context.Background()
	repo := integrationMergeBillingRepo(t, false)
	userID, keyID := integrationBillingFixtures(t, 10)

	result, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:   fmt.Sprintf("merge-flag-off-%d", userID),
		APIKeyID:    keyID,
		UserID:      userID,
		AccountType: service.AccountTypeAPIKey,
		BalanceCost: 1.25,
	})
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.False(t, result.BalanceSettlementDeferred)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, 8.75, *result.NewBalance, 1e-9)
	require.Equal(t, "8.75000000", integrationBalanceText(t, userID))
	require.Zero(t, integrationSettlementRows(t, userID), "legacy path must not use the settlement queue")
}

func TestUsageBillingMergeSettlementFallsBackWithoutConsumer(t *testing.T) {
	ctx := context.Background()
	// No consumer: stopping a worker clears the process-wide flag.
	stopWorker := service.NewUsageBalanceSettlementWorker(integrationNoopSettlementRepo{})
	stopWorker.Stop()
	require.False(t, service.UsageBalanceSettlementConsumerRunning())

	repo := integrationMergeBillingRepo(t, true)
	userID, keyID := integrationBillingFixtures(t, 10)

	result, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:   fmt.Sprintf("merge-fallback-%d", userID),
		APIKeyID:    keyID,
		UserID:      userID,
		AccountType: service.AccountTypeAPIKey,
		BalanceCost: 1.5,
	})
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.False(t, result.BalanceSettlementDeferred)
	require.NotNil(t, result.NewBalance)
	require.Equal(t, "8.50000000", integrationBalanceText(t, userID))
	require.Zero(t, integrationSettlementRows(t, userID))
}

func TestUsageBillingMergeSettlementMissingUserRollsBack(t *testing.T) {
	ctx := context.Background()
	startIntegrationSettlementConsumerFlag(t)
	repo := integrationMergeBillingRepo(t, true)
	userID, keyID := integrationBillingFixtures(t, 10)

	missingUserID := userID + 1_000_000
	requestID := fmt.Sprintf("merge-missing-user-%d", missingUserID)
	_, err := repo.Apply(ctx, &service.UsageBillingCommand{
		RequestID:   requestID,
		APIKeyID:    keyID,
		UserID:      missingUserID,
		AccountType: service.AccountTypeAPIKey,
		BalanceCost: 1,
	})
	require.ErrorIs(t, err, service.ErrUserNotFound)

	// Same failure semantics as the synchronous path: the whole billing
	// transaction rolls back, including the dedup claim.
	var claimed int
	require.NoError(t, integrationDB.QueryRow(
		`SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $1 AND api_key_id = $2`,
		requestID, keyID).Scan(&claimed))
	require.Zero(t, claimed)
}
