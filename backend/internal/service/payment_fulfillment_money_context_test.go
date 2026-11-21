//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

// TestPaymentFulfillmentSurvivesCanceledRequestContext is the regression test for
// the 2026-11-21 incident (order 11380): a paid EasyPay/wxpay callback whose HTTP
// connection was canceled used to abort the fulfillment transaction, leaving the
// order stuck in PAID/RECHARGING with the user's balance never credited.
//
// The notification is delivered with an already-canceled request context — the
// same state a webhook caller or browser sees after disconnecting — and must
// still transition the order to PAID, credit the balance, issue the redeem code
// and complete the order.
func TestPaymentFulfillmentSurvivesCanceledRequestContext(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)

	order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, OrderStatusPending, time.Now())
	order, err := client.PaymentOrder.UpdateOneID(order.ID).
		SetOrderType(payment.OrderTypeBalance).
		ClearPlanID().
		ClearSubscriptionGroupID().
		ClearSubscriptionDays().
		Save(ctx)
	require.NoError(t, err)

	redeemRepo := &paymentFulfillmentRedeemRepo{}
	credited := 0.0
	userRepo := &mockUserRepo{getByIDUser: &User{ID: order.UserID, Balance: 0}}
	userRepo.updateBalanceFn = func(_ context.Context, id int64, amount float64) error {
		require.Equal(t, order.UserID, id)
		credited += amount
		return nil
	}
	redeemService := NewRedeemService(redeemRepo, userRepo, nil, nil, nil, client, nil, nil)
	svc := &PaymentService{entClient: client, redeemService: redeemService, userRepo: userRepo}

	// Simulate the provider / browser dropping the connection mid-callback.
	requestCtx, cancelRequest := context.WithCancel(ctx)
	cancelRequest()

	err = svc.HandlePaymentNotification(requestCtx, &payment.PaymentNotification{
		TradeNo: "wx-trade-canceled-request",
		OrderID: order.OutTradeNo,
		Amount:  order.PayAmount,
		Status:  payment.NotificationStatusSuccess,
	}, payment.TypeAlipay)
	require.NoError(t, err)

	// The money movement actually happened even though the request context was dead.
	require.InDelta(t, order.Amount, credited, 1e-8)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)
	require.NotNil(t, reloaded.CompletedAt)

	code := redeemRepo.codesByCode[order.RechargeCode]
	require.NotNil(t, code)
	require.Equal(t, StatusUsed, code.Status)
	require.NotNil(t, code.UsedBy)
	require.Equal(t, order.UserID, *code.UsedBy)
}

// TestDetachPaymentMoneyContextIgnoresParentCancellation covers the helper itself:
// cancellation must be dropped while values are kept and the timeout still applies.
func TestDetachPaymentMoneyContextIgnoresParentCancellation(t *testing.T) {
	type ctxKey struct{}
	parent, cancelParent := context.WithCancel(context.WithValue(context.Background(), ctxKey{}, "kept"))
	cancelParent()

	detached, cancelDetached := detachPaymentMoneyContext(parent, time.Minute)
	defer cancelDetached()

	require.NoError(t, detached.Err(), "cancellation of the parent must not propagate")
	require.Equal(t, "kept", detached.Value(ctxKey{}), "request values must be preserved")
	require.NotNil(t, detached.Done())
	_, ok := detached.Deadline()
	require.True(t, ok, "detached context must carry its own timeout")
}

// TestDetachPaymentMoneyContextDoesNotExtendBudget makes sure an already-detached
// context keeps the outer budget: nested fulfillment entry points (e.g. the
// recovery sweep -> executeFulfillment) must not silently get another timeout.
func TestDetachPaymentMoneyContextDoesNotExtendBudget(t *testing.T) {
	outer, cancelOuter := detachPaymentMoneyContext(context.Background(), 50*time.Millisecond)
	defer cancelOuter()

	inner, cancelInner := detachPaymentMoneyContext(outer, time.Hour)
	defer cancelInner()
	require.Equal(t, outer, inner)

	remaining, ok := inner.Deadline()
	require.True(t, ok)
	require.LessOrEqual(t, time.Until(remaining), 50*time.Millisecond,
		"inner detachment must reuse the outer budget")
}

// TestDetachPaymentMoneyContextHonorsCallerDeadline keeps background sweeps bounded:
// when the caller already has a shorter deadline, the detached context inherits it.
func TestDetachPaymentMoneyContextHonorsCallerDeadline(t *testing.T) {
	bounded, cancelBounded := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelBounded()

	detached, cancelDetached := detachPaymentMoneyContext(bounded, time.Hour)
	defer cancelDetached()

	deadline, ok := detached.Deadline()
	require.True(t, ok)
	require.LessOrEqual(t, time.Until(deadline), 20*time.Millisecond)
}
