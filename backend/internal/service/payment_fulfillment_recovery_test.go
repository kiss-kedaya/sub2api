//go:build unit

package service

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

type paymentRecoveryFailingRedeemRepo struct {
	paymentOrderLifecycleRedeemRepo
}

func (r *paymentRecoveryFailingRedeemRepo) GetByCode(context.Context, string) (*RedeemCode, error) {
	return nil, errors.New("payment redeem code lookup unavailable")
}

func createPaymentFulfillmentRecoveryBalanceOrder(t *testing.T, ctx context.Context, client *dbent.Client, status string, updatedAt time.Time) *dbent.PaymentOrder {
	t.Helper()

	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, status, updatedAt)
	updated, err := client.PaymentOrder.UpdateOneID(order.ID).
		SetOrderType(payment.OrderTypeBalance).
		ClearPlanID().
		ClearSubscriptionGroupID().
		ClearSubscriptionDays().
		SetUpdatedAt(updatedAt).
		Save(ctx)
	require.NoError(t, err)
	return updated
}

func newPaymentRecoveryTestService(t *testing.T, client *dbent.Client, userID int64, redeemRepo RedeemCodeRepository) (*PaymentService, *float64) {
	t.Helper()

	credited := 0.0
	userRepo := &mockUserRepo{getByIDUser: &User{ID: userID, Balance: 0}}
	userRepo.updateBalanceFn = func(_ context.Context, id int64, amount float64) error {
		require.Equal(t, userID, id)
		credited += amount
		return nil
	}
	redeemService := NewRedeemService(redeemRepo, userRepo, nil, nil, nil, client, nil, nil)
	return &PaymentService{entClient: client, redeemService: redeemService, userRepo: userRepo}, &credited
}

// TestRecoverStuckFulfillmentsCompletesRechargingOrder covers the core rescue:
// an order stuck in RECHARGING longer than the threshold is picked up by the
// sweep, credited exactly once, marked COMPLETED and audited with the original
// status and timings. A second sweep must neither re-credit nor re-audit.
func TestRecoverStuckFulfillmentsCompletesRechargingOrder(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)

	staleAt := time.Now().Add(-10 * time.Minute)
	order := createPaymentFulfillmentRecoveryBalanceOrder(t, ctx, client, OrderStatusRecharging, staleAt)

	redeemRepo := &paymentFulfillmentRedeemRepo{}
	svc, credited := newPaymentRecoveryTestService(t, client, order.UserID, redeemRepo)

	stats, err := svc.RecoverStuckFulfillments(ctx, DefaultPaymentFulfillmentRecoveryThreshold, DefaultPaymentFulfillmentRecoveryMaxAttempts)
	require.NoError(t, err)
	require.Equal(t, 1, stats.Scanned)
	require.Equal(t, 1, stats.Recovered)
	require.Zero(t, stats.Failed)
	require.InDelta(t, order.Amount, *credited, 1e-8)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)
	require.NotNil(t, reloaded.CompletedAt)
	require.Equal(t, 1, redeemRepo.createCalls, "the deterministic paycheck code is issued exactly once")

	recoveredAudits, err := client.PaymentAuditLog.Query().
		Where(
			paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
			paymentauditlog.ActionEQ(paymentFulfillmentRecoveredAuditAction),
		).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, recoveredAudits, 1)
	require.Contains(t, recoveredAudits[0].Detail, `"originalStatus":"RECHARGING"`)
	require.Contains(t, recoveredAudits[0].Detail, `"stuckSeconds"`)
	require.Contains(t, recoveredAudits[0].Detail, `"recoveryDurationMs"`)

	// Idempotency: a completed order is out of the scan window, so a second sweep
	// must not credit again, re-issue the code, or duplicate the recovery audit.
	stats, err = svc.RecoverStuckFulfillments(ctx, DefaultPaymentFulfillmentRecoveryThreshold, DefaultPaymentFulfillmentRecoveryMaxAttempts)
	require.NoError(t, err)
	require.Zero(t, stats.Scanned)
	require.Zero(t, stats.Recovered)
	require.InDelta(t, order.Amount, *credited, 1e-8)
	require.Equal(t, 1, redeemRepo.createCalls)

	recoveredAuditCount, err := client.PaymentAuditLog.Query().
		Where(
			paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)),
			paymentauditlog.ActionEQ(paymentFulfillmentRecoveredAuditAction),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, recoveredAuditCount)
}

// TestRecoverStuckFulfillmentsSkipsFreshFulfillmentLease makes sure the sweep never
// steals an order another worker is still processing: a RECHARGING row younger than
// the fulfillment lease is skipped without consuming a recovery attempt.
func TestRecoverStuckFulfillmentsSkipsFreshFulfillmentLease(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)

	// Older than the 2m recovery threshold but younger than the 5m fulfillment lease.
	leasedAt := time.Now().Add(-3 * time.Minute)
	order := createPaymentFulfillmentRecoveryBalanceOrder(t, ctx, client, OrderStatusRecharging, leasedAt)

	redeemRepo := &paymentFulfillmentRedeemRepo{}
	svc, credited := newPaymentRecoveryTestService(t, client, order.UserID, redeemRepo)

	stats, err := svc.RecoverStuckFulfillments(ctx, DefaultPaymentFulfillmentRecoveryThreshold, DefaultPaymentFulfillmentRecoveryMaxAttempts)
	require.NoError(t, err)
	require.Equal(t, 1, stats.Scanned)
	require.Equal(t, 1, stats.SkippedLeased)
	require.Zero(t, stats.Recovered)
	require.Zero(t, stats.Failed)
	require.Zero(t, *credited)
	require.Zero(t, redeemRepo.createCalls)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRecharging, reloaded.Status)

	attempts, err := svc.paymentFulfillmentRecoveryAttempts(ctx, order.ID)
	require.NoError(t, err)
	require.Zero(t, attempts, "a lease conflict is not a failed attempt")
}

// TestRecoverStuckFulfillmentsRetriesFailureWithUpperBound covers the failure path:
// a broken fulfillment is retried on the next sweep, each failure increments the
// persisted attempt counter, and once the cap is reached the sweep stops retrying
// instead of looping forever.
func TestRecoverStuckFulfillmentsRetriesFailureWithUpperBound(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)

	staleAt := time.Now().Add(-10 * time.Minute)
	order := createPaymentFulfillmentRecoveryBalanceOrder(t, ctx, client, OrderStatusPaid, staleAt)

	svc, credited := newPaymentRecoveryTestService(t, client, order.UserID, &paymentRecoveryFailingRedeemRepo{})

	const maxAttempts = 2
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		stats, err := svc.RecoverStuckFulfillments(ctx, DefaultPaymentFulfillmentRecoveryThreshold, maxAttempts)
		require.NoError(t, err)
		require.Equal(t, 1, stats.Scanned)
		require.Equal(t, 1, stats.Failed)
		require.Zero(t, stats.Recovered)
		require.Zero(t, *credited)

		persisted, err := svc.paymentFulfillmentRecoveryAttempts(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, attempt, persisted)

		reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
		require.NoError(t, err)
		require.Equal(t, OrderStatusFailed, reloaded.Status)
		require.Nil(t, reloaded.CompletedAt)

		// Simulate the next background tick after the threshold elapsed again.
		_, err = client.PaymentOrder.UpdateOneID(order.ID).
			SetUpdatedAt(time.Now().Add(-10 * time.Minute)).
			Save(ctx)
		require.NoError(t, err)
	}

	stats, err := svc.RecoverStuckFulfillments(ctx, DefaultPaymentFulfillmentRecoveryThreshold, maxAttempts)
	require.NoError(t, err)
	require.Equal(t, 1, stats.SkippedMaxAttempts)
	require.Zero(t, stats.Failed)
	require.Zero(t, stats.Recovered)

	persisted, err := svc.paymentFulfillmentRecoveryAttempts(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, maxAttempts, persisted, "no attempt is recorded past the cap")
	require.Zero(t, *credited)
}

// TestRecoverStuckFulfillmentsIgnoresCompletedAndUnpaidOrders proves the sweep only
// touches orders that are actually stuck after a payment: completed orders are
// never re-credited and pending (unpaid) orders are never fulfilled.
func TestRecoverStuckFulfillmentsIgnoresCompletedAndUnpaidOrders(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)

	staleAt := time.Now().Add(-10 * time.Minute)
	completed := createPaymentFulfillmentRecoveryBalanceOrder(t, ctx, client, OrderStatusCompleted, staleAt)
	// The shared order factory derives out_trade_no from the wall clock; make sure
	// the second order gets a different value on coarse-resolution hosts.
	time.Sleep(2 * time.Millisecond)
	pending := createPaymentFulfillmentRecoveryBalanceOrder(t, ctx, client, OrderStatusPending, staleAt)

	redeemRepo := &paymentFulfillmentRedeemRepo{}
	svc, credited := newPaymentRecoveryTestService(t, client, completed.UserID, redeemRepo)

	stats, err := svc.RecoverStuckFulfillments(ctx, DefaultPaymentFulfillmentRecoveryThreshold, DefaultPaymentFulfillmentRecoveryMaxAttempts)
	require.NoError(t, err)
	require.Zero(t, stats.Scanned)
	require.Zero(t, stats.Recovered)
	require.Zero(t, *credited)
	require.Zero(t, redeemRepo.createCalls)

	completedReloaded, err := client.PaymentOrder.Get(ctx, completed.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, completedReloaded.Status)
	pendingReloaded, err := client.PaymentOrder.Get(ctx, pending.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPending, pendingReloaded.Status)
}

// TestRecoverStuckFulfillmentsSkipsCryptoDepositOrder keeps the generic recovery
// away from crypto deposits: those are settled only by the protected on-chain
// ledger, exactly like the admin RetryFulfillment guard.
func TestRecoverStuckFulfillmentsSkipsCryptoDepositOrder(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)

	staleAt := time.Now().Add(-10 * time.Minute)
	order := createPaymentFulfillmentRecoveryBalanceOrder(t, ctx, client, OrderStatusPaid, staleAt)
	order, err := client.PaymentOrder.UpdateOneID(order.ID).
		SetPaymentType(payment.TypeCrypto).
		SetUpdatedAt(staleAt).
		Save(ctx)
	require.NoError(t, err)

	redeemRepo := &paymentFulfillmentRedeemRepo{}
	svc, credited := newPaymentRecoveryTestService(t, client, order.UserID, redeemRepo)

	stats, err := svc.RecoverStuckFulfillments(ctx, DefaultPaymentFulfillmentRecoveryThreshold, DefaultPaymentFulfillmentRecoveryMaxAttempts)
	require.NoError(t, err)
	require.Equal(t, 1, stats.Scanned)
	require.Equal(t, 1, stats.SkippedCrypto)
	require.Zero(t, stats.Recovered)
	require.Zero(t, *credited)
	require.Zero(t, redeemRepo.createCalls)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusPaid, reloaded.Status)
}

// TestPaymentOrderExpiryServiceSweepRecoversStuckFulfillment wires the recovery into
// the existing scheduled job: one tick of PaymentOrderExpiryService must rescue a
// stuck order, proving the periodic loop (60s + leader lock) runs the sweep.
func TestPaymentOrderExpiryServiceSweepRecoversStuckFulfillment(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)

	staleAt := time.Now().Add(-10 * time.Minute)
	order := createPaymentFulfillmentRecoveryBalanceOrder(t, ctx, client, OrderStatusRecharging, staleAt)

	redeemRepo := &paymentFulfillmentRedeemRepo{}
	svc, credited := newPaymentRecoveryTestService(t, client, order.UserID, redeemRepo)

	expiryService := NewPaymentOrderExpiryService(svc, time.Minute)
	expiryService.runOnce()

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)
	require.NotNil(t, reloaded.CompletedAt)
	require.InDelta(t, order.Amount, *credited, 1e-8)
}
