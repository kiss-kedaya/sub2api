//go:build integration

package service

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"

	_ "github.com/lib/pq"
)

// TestPostgresCreateOrderPendingLimitIsSerializedPerUser proves the audit
// finding's scenario against a real PostgreSQL server: with 10 concurrent
// order creations for one user, exactly max_pending_orders succeed and every
// other request gets TOO_MANY_PENDING (429), so the pending count can never
// overshoot the configured cap.
//
// It needs a disposable database (DDL is applied by ent after the connection is
// opened), e.g.:
//
//	PAYMENT_ORDER_TEST_DSN=postgres://postgres:postgres@127.0.0.1:5432/sub2api_test?sslmode=disable \
//	  go test -tags=integration ./internal/service -run TestPostgresCreateOrderPendingLimitIsSerializedPerUser -count=1
func TestPostgresCreateOrderPendingLimitIsSerializedPerUser(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("PAYMENT_ORDER_TEST_DSN"))
	if dsn == "" {
		t.Skip("PAYMENT_ORDER_TEST_DSN not set; a disposable PostgreSQL database is required")
	}

	ctx := t.Context()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(16)
	t.Cleanup(func() { _ = db.Close() })

	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(ctx))

	suffix := time.Now().UnixNano()
	user, err := client.User.Create().
		SetEmail(fmt.Sprintf("pending-race-%d@example.com", suffix)).
		SetPasswordHash("hash").
		SetUsername(fmt.Sprintf("pending-race-%d", suffix)).
		Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = client.PaymentOrder.Delete().Where(paymentorder.UserIDEQ(user.ID)).Exec(cleanupCtx)
		_ = client.User.DeleteOneID(user.ID).Exec(cleanupCtx)
	})

	const (
		maxPending = 5
		attempts   = 10
	)
	svc := &PaymentService{entClient: client}
	actor := &User{ID: user.ID, Email: user.Email, Username: user.Username}
	cfg := &PaymentConfig{MaxPendingOrders: maxPending, DailyLimit: 0, OrderTimeoutMin: 30}
	req := CreateOrderRequest{
		UserID:      user.ID,
		PaymentType: payment.TypeAlipay,
		OrderType:   payment.OrderTypeBalance,
	}

	start := make(chan struct{})
	errs := make([]error, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = svc.createOrderInTx(ctx, req, actor, nil, cfg, 10, 10, 0, 10, nil)
		}(i)
	}
	close(start)
	wg.Wait()

	accepted := 0
	rejected := 0
	for _, createErr := range errs {
		if createErr == nil {
			accepted++
			continue
		}
		require.Equal(t, http.StatusTooManyRequests, infraerrors.Code(createErr), "unexpected error: %v", createErr)
		require.Equal(t, "TOO_MANY_PENDING", infraerrors.Reason(createErr))
		rejected++
	}
	require.Equal(t, maxPending, accepted, "the per-user advisory lock must serialize the count-then-insert window")
	require.Equal(t, attempts-maxPending, rejected)

	pending, err := client.PaymentOrder.Query().
		Where(paymentorder.UserIDEQ(user.ID), paymentorder.StatusEQ(payment.OrderStatusPending)).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, maxPending, pending)
}
