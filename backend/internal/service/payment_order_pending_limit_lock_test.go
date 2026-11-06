//go:build unit

package service

import (
	"context"
	"net/http"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func newPaymentOrderPostgresMockService(t *testing.T) (*PaymentService, sqlmock.Sqlmock) {
	t.Helper()

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	return &PaymentService{entClient: client}, mock
}

// expectPaymentOrderCreateUserLock asserts the transaction takes the per-user
// advisory lock before it reads the pending order count.
func expectPaymentOrderCreateUserLock(mock sqlmock.Sqlmock, userID int64) {
	mock.ExpectQuery(regexp.QuoteMeta(paymentOrderCreateLockQuery)).
		WithArgs(paymentOrderCreateLockKey(userID)).
		WillReturnRows(sqlmock.NewRows([]string{"pg_advisory_xact_lock"}).AddRow(nil))
}

func expectPendingOrderCount(mock sqlmock.Sqlmock, userID int64, count int) {
	mock.ExpectQuery(`SELECT COUNT\(.*\) FROM .*payment_orders`).
		WithArgs(userID, payment.OrderStatusPending).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(count))
}

func TestCreateOrderInTx_RejectsPendingOrdersBeyondLimitAfterTakingUserLock(t *testing.T) {
	svc, mock := newPaymentOrderPostgresMockService(t)
	ctx := context.Background()

	mock.ExpectBegin()
	expectPaymentOrderCreateUserLock(mock, 7)
	expectPendingOrderCount(mock, 7, 2)
	mock.ExpectRollback()

	_, err := svc.createOrderInTx(
		ctx,
		CreateOrderRequest{
			UserID:      7,
			PaymentType: payment.TypeAlipay,
			OrderType:   payment.OrderTypeBalance,
		},
		&User{ID: 7, Email: "limit@example.com", Username: "limit"},
		nil,
		&PaymentConfig{MaxPendingOrders: 2, DailyLimit: 0, OrderTimeoutMin: 30},
		10, 10, 0, 10,
		nil,
	)

	require.Error(t, err)
	require.Equal(t, http.StatusTooManyRequests, infraerrors.Code(err))
	require.Equal(t, "TOO_MANY_PENDING", infraerrors.Reason(err))
	require.NoError(t, mock.ExpectationsWereMet(), "the lock query must run before the pending count")
}

func TestCreateOrderTx_LockAndPendingCheckAllowOrderUnderCap(t *testing.T) {
	svc, mock := newPaymentOrderPostgresMockService(t)
	ctx := context.Background()

	mock.ExpectBegin()
	expectPaymentOrderCreateUserLock(mock, 7)
	expectPendingOrderCount(mock, 7, 1)
	mock.ExpectRollback()

	tx, err := svc.entClient.Tx(ctx)
	require.NoError(t, err)
	require.NoError(t, lockPaymentOrderCreateForUser(ctx, svc.entClient, tx, 7))
	require.NoError(t, svc.checkPendingLimit(ctx, tx, 7, 2))
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateOrderInTx_EnforcesPendingLimitInRealTransaction(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	user, err := client.User.Create().
		SetEmail("pending-limit@example.com").
		SetPasswordHash("hash").
		SetUsername("pending-limit").
		Save(ctx)
	require.NoError(t, err)

	svc := &PaymentService{entClient: client}
	req := CreateOrderRequest{
		UserID:      user.ID,
		PaymentType: payment.TypeAlipay,
		OrderType:   payment.OrderTypeBalance,
	}
	actor := &User{ID: user.ID, Email: user.Email, Username: user.Username}
	cfg := &PaymentConfig{MaxPendingOrders: 2, DailyLimit: 0, OrderTimeoutMin: 30}

	for i := 0; i < 2; i++ {
		order, createErr := svc.createOrderInTx(ctx, req, actor, nil, cfg, 10, 10, 0, 10, nil)
		require.NoError(t, createErr)
		require.Equal(t, payment.OrderStatusPending, order.Status)
	}

	_, err = svc.createOrderInTx(ctx, req, actor, nil, cfg, 10, 10, 0, 10, nil)
	require.Error(t, err)
	require.Equal(t, http.StatusTooManyRequests, infraerrors.Code(err))
	require.Equal(t, "TOO_MANY_PENDING", infraerrors.Reason(err))

	pending, err := client.PaymentOrder.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, pending, "the rejected order must not be inserted")
}

func TestPaymentOrderCreateLockKeyIsNamespacedPerUser(t *testing.T) {
	require.NotEqual(t, paymentOrderCreateLockKey(7), paymentOrderCreateLockKey(8))
	require.Equal(t, "payment-order-create:7", paymentOrderCreateLockKey(7))
	// The namespace keeps the key from colliding with locks hashed from bare ids.
	require.NotEqual(t, "7", paymentOrderCreateLockKey(7))
}
