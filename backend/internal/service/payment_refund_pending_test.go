//go:build unit

package service

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestPendingRefundRetainsReservedFundsUntilSuccess(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "pending-reservation")
	order, err := client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusRefunding).Save(ctx)
	require.NoError(t, err)

	available := 0.0
	rollbackCalls := 0
	finalDeductionCalls := 0
	userRepo := &mockUserRepo{}
	userRepo.updateBalanceFn = func(_ context.Context, _ int64, amount float64) error {
		rollbackCalls++
		available += amount
		return nil
	}
	userRepo.deductAvailableBalanceFn = func(_ context.Context, _ int64, amount float64) (float64, error) {
		finalDeductionCalls++
		deducted := math.Min(available, amount)
		available -= deducted
		return deducted, nil
	}
	svc := &PaymentService{entClient: client, userRepo: userRepo, loadBalancer: &captureLoadBalancer{}}
	plan := &RefundPlan{
		OrderID: order.ID, Order: order, RefundAmount: 100, GatewayAmount: 100,
		Reason: "pending reservation", Force: false, DeductBalance: true,
		DeductionType: payment.DeductionTypeBalance, BalanceToDeduct: 100,
	}

	result, err := svc.finishRefund(ctx, plan, &payment.RefundResponse{
		Status: payment.ProviderStatusPending, RefundID: "refund-retained",
	})
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Zero(t, available)
	require.Zero(t, rollbackCalls)

	restore := replacePaymentProviderFactoryForTest(t, &refundQueryProviderTestDouble{
		refundResponse: &payment.RefundResponse{Status: payment.ProviderStatusSuccess, RefundID: "refund-retained"},
	})
	defer restore()
	result, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 100.0, result.BalanceDeducted)
	require.Zero(t, available)
	require.Zero(t, rollbackCalls)
	require.Zero(t, finalDeductionCalls)
}

func TestPendingRefundFailureReleasesRetainedFundsOnce(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "pending-failure-release")
	order, err := client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusRefunding).Save(ctx)
	require.NoError(t, err)

	available := 0.0
	releaseCalls := 0
	svc := &PaymentService{
		entClient: client, loadBalancer: &captureLoadBalancer{},
		userRepo: &refundUserRepo{mockUserRepo: &mockUserRepo{}, adjustBalanceFn: func(txCtx context.Context, _ int64, amount float64) (BalanceChange, error) {
			require.NotNil(t, dbent.TxFromContext(txCtx))
			releaseCalls++
			available += amount
			return BalanceChange{Old: available - amount, New: available}, nil
		}},
	}
	plan := &RefundPlan{
		OrderID: order.ID, Order: order, RefundAmount: 100, GatewayAmount: 100,
		Reason: "provider eventually failed", DeductBalance: true,
		DeductionType: payment.DeductionTypeBalance, BalanceToDeduct: 100,
	}
	result, err := svc.finishRefund(ctx, plan, &payment.RefundResponse{
		Status: payment.ProviderStatusPending, RefundID: "refund-failed",
	})
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Zero(t, releaseCalls)

	restore := replacePaymentProviderFactoryForTest(t, &refundQueryProviderTestDouble{
		refundResponse: &payment.RefundResponse{Status: payment.ProviderStatusFailed, RefundID: "refund-failed"},
	})
	defer restore()
	result, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Equal(t, 1, releaseCalls)
	require.Equal(t, 100.0, available)

	result, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.Nil(t, result)
	require.Equal(t, "INVALID_STATUS", infraerrors.Reason(err))
	require.Equal(t, 1, releaseCalls)
}

func TestPendingRefundIndeterminateQueryKeepsRetainedFunds(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response *payment.RefundResponse
	}{
		{name: "missing response"},
		{name: "unknown status", response: &payment.RefundResponse{Status: "processing-unknown", RefundID: "refund-indeterminate"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			order := createPendingRefundOrderForTest(t, ctx, client, "pending-indeterminate-"+strconv.Itoa(len(tc.name)))
			order, err := client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusRefunding).Save(ctx)
			require.NoError(t, err)

			releaseCalls := 0
			svc := &PaymentService{
				entClient: client, loadBalancer: &captureLoadBalancer{},
				userRepo: &refundUserRepo{mockUserRepo: &mockUserRepo{}, adjustBalanceFn: func(context.Context, int64, float64) (BalanceChange, error) {
					releaseCalls++
					return BalanceChange{}, nil
				}},
			}
			plan := &RefundPlan{
				OrderID: order.ID, Order: order, RefundAmount: 100, GatewayAmount: 100,
				Reason: "indeterminate provider status", DeductBalance: true,
				DeductionType: payment.DeductionTypeBalance, BalanceToDeduct: 100,
			}
			_, err = svc.finishRefund(ctx, plan, &payment.RefundResponse{
				Status: payment.ProviderStatusPending, RefundID: "refund-indeterminate",
			})
			require.NoError(t, err)

			restore := replacePaymentProviderFactoryForTest(t, &refundQueryProviderTestDouble{refundResponse: tc.response})
			defer restore()
			result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
			require.Nil(t, result)
			require.Error(t, err)
			require.Zero(t, releaseCalls)
			reloaded, getErr := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, getErr)
			require.Equal(t, OrderStatusRefundPending, reloaded.Status)
		})
	}
}

func TestPendingForcedRefundRetainsOnlyActualDeduction(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "pending-force-partial")
	order, err := client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusRefunding).Save(ctx)
	require.NoError(t, err)

	finalDeductionCalls := 0
	svc := &PaymentService{
		entClient: client, loadBalancer: &captureLoadBalancer{},
		userRepo: &mockUserRepo{deductAvailableBalanceFn: func(context.Context, int64, float64) (float64, error) {
			finalDeductionCalls++
			return 0, nil
		}},
	}
	plan := &RefundPlan{
		OrderID: order.ID, Order: order, RefundAmount: 100, GatewayAmount: 100,
		Reason: "forced partial deduction", Force: true, DeductBalance: true,
		DeductionType: payment.DeductionTypeBalance, BalanceToDeduct: 35,
	}
	_, err = svc.finishRefund(ctx, plan, &payment.RefundResponse{
		Status: payment.ProviderStatusPending, RefundID: "refund-force-partial",
	})
	require.NoError(t, err)

	restore := replacePaymentProviderFactoryForTest(t, &refundQueryProviderTestDouble{
		refundResponse: &payment.RefundResponse{Status: payment.ProviderStatusSuccess, RefundID: "refund-force-partial"},
	})
	defer restore()
	result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 35.0, result.BalanceDeducted)
	require.Zero(t, finalDeductionCalls)
}

func TestLegacyPendingRefundAlreadyRolledBackDeductsAtMostOnce(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "legacy-pending-rolled-back")

	deductionCalls := 0
	svc := &PaymentService{
		entClient: client, loadBalancer: &captureLoadBalancer{},
		userRepo: &mockUserRepo{deductAvailableBalanceFn: func(_ context.Context, _ int64, amount float64) (float64, error) {
			deductionCalls++
			return amount, nil
		}},
	}
	restore := replacePaymentProviderFactoryForTest(t, &refundQueryProviderTestDouble{
		refundResponse: &payment.RefundResponse{Status: payment.ProviderStatusSuccess, RefundID: "rf_test"},
	})
	defer restore()

	result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 100.0, result.BalanceDeducted)
	require.Equal(t, 1, deductionCalls)

	result, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.Nil(t, result)
	require.Equal(t, "INVALID_STATUS", infraerrors.Reason(err))
	require.Equal(t, 1, deductionCalls)
}

func TestLegacyPendingRefundUsesRecordedRollbackAmount(t *testing.T) {
	for _, tc := range []struct {
		name          string
		rolledBack    float64
		wantRequested float64
		wantCalls     int
	}{
		{name: "forced partial rollback", rolledBack: 35, wantRequested: 35, wantCalls: 1},
		{name: "deduction disabled", rolledBack: 0, wantCalls: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			order := createPendingRefundOrderForTest(t, ctx, client, "legacy-rollback-amount-"+strconv.Itoa(tc.wantCalls))
			_, err := client.PaymentAuditLog.Update().
				Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("REFUND_PENDING")).
				SetDetail(fmt.Sprintf(`{"refundID":"rf_test","deductionRollbackOK":true,"balanceRolledBack":%v}`, tc.rolledBack)).
				Save(ctx)
			require.NoError(t, err)

			calls := 0
			requested := 0.0
			svc := &PaymentService{
				entClient: client, loadBalancer: &captureLoadBalancer{},
				userRepo: &mockUserRepo{deductAvailableBalanceFn: func(_ context.Context, _ int64, amount float64) (float64, error) {
					calls++
					requested = amount
					return amount, nil
				}},
			}
			restore := replacePaymentProviderFactoryForTest(t, &refundQueryProviderTestDouble{
				refundResponse: &payment.RefundResponse{Status: payment.ProviderStatusSuccess, RefundID: "rf_test"},
			})
			defer restore()

			result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
			require.NoError(t, err)
			require.True(t, result.Success)
			require.Equal(t, tc.wantCalls, calls)
			require.Equal(t, tc.wantRequested, requested)
			require.Equal(t, tc.wantRequested, result.BalanceDeducted)
		})
	}
}

func TestLegacyPendingRefundWithoutProofOfRollbackDoesNotDeductAgain(t *testing.T) {
	for _, tc := range []struct {
		name   string
		detail *string
	}{
		{name: "rollback failed", detail: stringPointer(`{"refundID":"rf_test","deductionRollbackOK":false,"balanceDeducted":100}`)},
		{name: "missing audit"},
		{name: "malformed audit", detail: stringPointer(`{"refundID":`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			order := createPendingRefundOrderForTest(t, ctx, client, "legacy-no-rededuct-"+strconv.Itoa(len(tc.name)))
			_, err := client.PaymentAuditLog.Delete().
				Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10))).
				Exec(ctx)
			require.NoError(t, err)
			if tc.detail != nil {
				_, err = client.PaymentAuditLog.Create().
					SetOrderID(strconv.FormatInt(order.ID, 10)).
					SetAction("REFUND_PENDING").
					SetOperator("admin").
					SetDetail(*tc.detail).
					Save(ctx)
				require.NoError(t, err)
			}

			deductionCalls := 0
			svc := &PaymentService{
				entClient: client, loadBalancer: &captureLoadBalancer{},
				userRepo: &mockUserRepo{deductAvailableBalanceFn: func(context.Context, int64, float64) (float64, error) {
					deductionCalls++
					return 100, nil
				}},
			}
			restore := replacePaymentProviderFactoryForTest(t, &refundQueryProviderTestDouble{
				refundResponse: &payment.RefundResponse{Status: payment.ProviderStatusSuccess, RefundID: "rf_test"},
			})
			defer restore()

			result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
			require.NoError(t, err)
			require.True(t, result.Success)
			require.Zero(t, deductionCalls)
		})
	}
}

func TestConcurrentPendingFailureQueriesReleaseRetainedFundsOnce(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "pending-failure-race")
	order, err := client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusRefunding).Save(ctx)
	require.NoError(t, err)

	var releaseCalls atomic.Int32
	svc := &PaymentService{
		entClient: client, loadBalancer: &captureLoadBalancer{},
		userRepo: &refundUserRepo{mockUserRepo: &mockUserRepo{}, adjustBalanceFn: func(context.Context, int64, float64) (BalanceChange, error) {
			releaseCalls.Add(1)
			return BalanceChange{}, nil
		}},
	}
	plan := &RefundPlan{
		OrderID: order.ID, Order: order, RefundAmount: 100, GatewayAmount: 100,
		Reason: "failed query race", DeductBalance: true,
		DeductionType: payment.DeductionTypeBalance, BalanceToDeduct: 100,
	}
	_, err = svc.finishRefund(ctx, plan, &payment.RefundResponse{
		Status: payment.ProviderStatusPending, RefundID: "refund-race",
	})
	require.NoError(t, err)

	provider := &blockingRefundQueryProvider{
		ready: make(chan struct{}, 2), release: make(chan struct{}),
		response: &payment.RefundResponse{Status: payment.ProviderStatusFailed, RefundID: "refund-race"},
	}
	restore := replacePaymentProviderFactoryForTest(t, provider)
	defer restore()

	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			_, _ = svc.QueryAndFinalizeRefund(ctx, order.ID)
		}()
	}
	<-provider.ready
	<-provider.ready
	close(provider.release)
	wg.Wait()

	require.Equal(t, int32(1), releaseCalls.Load())
	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRefundFailed, reloaded.Status)
}

func TestConcurrentLegacyPendingSuccessQueriesDeductOnce(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "legacy-success-race")

	var deductionCalls atomic.Int32
	svc := &PaymentService{
		entClient: client, loadBalancer: &captureLoadBalancer{},
		userRepo: &mockUserRepo{deductAvailableBalanceFn: func(context.Context, int64, float64) (float64, error) {
			deductionCalls.Add(1)
			return 100, nil
		}},
	}
	provider := &blockingRefundQueryProvider{
		ready: make(chan struct{}, 2), release: make(chan struct{}),
		response: &payment.RefundResponse{Status: payment.ProviderStatusSuccess, RefundID: "rf_test"},
	}
	restore := replacePaymentProviderFactoryForTest(t, provider)
	defer restore()

	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			_, _ = svc.QueryAndFinalizeRefund(ctx, order.ID)
		}()
	}
	<-provider.ready
	<-provider.ready
	close(provider.release)
	wg.Wait()

	require.Equal(t, int32(1), deductionCalls.Load())
	successAudits, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("REFUND_SUCCESS")).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, successAudits)
}

func TestPendingRefundQueryAndCallbackRaceFinalizesOnce(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "pending-query-callback-race")
	order, err := client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusRefunding).Save(ctx)
	require.NoError(t, err)

	deductionCalls := 0
	svc := &PaymentService{
		entClient: client, loadBalancer: &captureLoadBalancer{},
		userRepo: &mockUserRepo{deductAvailableBalanceFn: func(context.Context, int64, float64) (float64, error) {
			deductionCalls++
			return 100, nil
		}},
	}
	plan := &RefundPlan{
		OrderID: order.ID, Order: order, RefundAmount: 100, GatewayAmount: 100,
		Reason: "query callback race", DeductBalance: true,
		DeductionType: payment.DeductionTypeBalance, BalanceToDeduct: 100,
	}
	_, err = svc.finishRefund(ctx, plan, &payment.RefundResponse{
		Status: payment.ProviderStatusPending, RefundID: "refund-query-callback-race",
	})
	require.NoError(t, err)

	provider := &blockingRefundQueryProvider{
		ready: make(chan struct{}, 1), release: make(chan struct{}),
		response: &payment.RefundResponse{Status: payment.ProviderStatusSuccess, RefundID: "refund-query-callback-race"},
	}
	restore := replacePaymentProviderFactoryForTest(t, provider)
	defer restore()
	queryDone := make(chan error, 1)
	go func() {
		_, queryErr := svc.QueryAndFinalizeRefund(ctx, order.ID)
		queryDone <- queryErr
	}()
	<-provider.ready

	callbackPlan := svc.refundFinalizePlan(order)
	callbackPlan.BalanceToDeduct = 100
	callbackResult, err := svc.finalizePendingRefundSuccessWithDeduction(ctx, callbackPlan, false)
	require.NoError(t, err)
	require.True(t, callbackResult.Success)
	close(provider.release)
	require.Equal(t, "CONFLICT", infraerrors.Reason(<-queryDone))

	require.Zero(t, deductionCalls)
	successAudits, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("REFUND_SUCCESS")).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, successAudits)
}

func TestPendingRefundFailureReleaseCanRetryAfterRepositoryError(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "pending-failure-retry")
	order, err := client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusRefunding).Save(ctx)
	require.NoError(t, err)

	var attempts atomic.Int32
	svc := &PaymentService{
		entClient: client, loadBalancer: &captureLoadBalancer{},
		userRepo: &refundUserRepo{mockUserRepo: &mockUserRepo{}, adjustBalanceFn: func(context.Context, int64, float64) (BalanceChange, error) {
			if attempts.Add(1) == 1 {
				return BalanceChange{}, context.DeadlineExceeded
			}
			return BalanceChange{Old: 0, New: 100}, nil
		}},
	}
	plan := &RefundPlan{
		OrderID: order.ID, Order: order, RefundAmount: 100, GatewayAmount: 100,
		Reason: "failed release retry", DeductBalance: true,
		DeductionType: payment.DeductionTypeBalance, BalanceToDeduct: 100,
	}
	_, err = svc.finishRefund(ctx, plan, &payment.RefundResponse{
		Status: payment.ProviderStatusPending, RefundID: "refund-failure-retry",
	})
	require.NoError(t, err)
	restore := replacePaymentProviderFactoryForTest(t, &refundQueryProviderTestDouble{
		refundResponse: &payment.RefundResponse{Status: payment.ProviderStatusFailed, RefundID: "refund-failure-retry"},
	})
	defer restore()

	result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.Nil(t, result)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRefundPending, reloaded.Status)

	result, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Equal(t, int32(2), attempts.Load())
	reloaded, err = client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRefundFailed, reloaded.Status)
}

func TestRefundFinalizePlanPreservesSubscriptionDeductionType(t *testing.T) {
	order := &dbent.PaymentOrder{
		ID: 7, OrderType: payment.OrderTypeSubscription, RefundAmount: 30,
	}
	plan := (&PaymentService{}).refundFinalizePlan(order)
	detail := refundPendingAuditDetail{
		DeductionState:  refundDeductionStateRetained,
		SubDaysDeducted: 30,
		SubscriptionID:  99,
	}

	require.False(t, detail.applyToFinalizationPlan(plan))
	require.Equal(t, payment.DeductionTypeSubscription, plan.DeductionType)
	require.Equal(t, 30, plan.SubDaysToDeduct)
	require.Equal(t, int64(99), plan.SubscriptionID)
}

func TestPendingSubscriptionRefundKeepsDaysUntilFinalOutcome(t *testing.T) {
	for _, tc := range []struct {
		name           string
		providerStatus string
		wantExtensions int
	}{
		{name: "success keeps original deduction", providerStatus: payment.ProviderStatusSuccess},
		{name: "failure restores original deduction", providerStatus: payment.ProviderStatusFailed, wantExtensions: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			order := createPendingRefundOrderForTest(t, ctx, client, "pending-subscription-"+strconv.Itoa(tc.wantExtensions))
			order, err := client.PaymentOrder.UpdateOneID(order.ID).
				SetOrderType(payment.OrderTypeSubscription).
				SetSubscriptionDays(30).
				SetStatus(OrderStatusRefunding).
				Save(ctx)
			require.NoError(t, err)

			subRepo := &refundSubscriptionRepo{sub: &UserSubscription{
				ID: 99, UserID: order.UserID, GroupID: 7,
				Status: SubscriptionStatusActive, ExpiresAt: time.Now().Add(60 * 24 * time.Hour),
			}}
			svc := &PaymentService{
				entClient: client, loadBalancer: &captureLoadBalancer{},
				subscriptionSvc: NewSubscriptionService(groupRepoNoop{}, subRepo, nil, nil, nil),
			}
			plan := &RefundPlan{
				OrderID: order.ID, Order: order, RefundAmount: 100, GatewayAmount: 100,
				Reason: "pending subscription", DeductBalance: true,
				DeductionType: payment.DeductionTypeSubscription, SubDaysToDeduct: 30, SubscriptionID: 99,
			}
			_, err = svc.finishRefund(ctx, plan, &payment.RefundResponse{
				Status: payment.ProviderStatusPending, RefundID: "refund-subscription",
			})
			require.NoError(t, err)
			require.Zero(t, subRepo.extendCalls)

			restore := replacePaymentProviderFactoryForTest(t, &refundQueryProviderTestDouble{
				refundResponse: &payment.RefundResponse{Status: tc.providerStatus, RefundID: "refund-subscription"},
			})
			defer restore()
			result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tc.wantExtensions, subRepo.extendCalls)
			if tc.wantExtensions > 0 {
				require.NotNil(t, dbent.TxFromContext(subRepo.extendCtx))
				require.Equal(t, subRepo.originalExpiry.AddDate(0, 0, 30), subRepo.sub.ExpiresAt)
			}
		})
	}
}

func TestExecuteRefundNonForceRequiresFullAtomicDeduction(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "non-force-concurrent-spend")
	order, err := client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusCompleted).Save(ctx)
	require.NoError(t, err)

	provider := &countingRefundProvider{response: &payment.RefundResponse{Status: payment.ProviderStatusPending}}
	restore := replacePaymentProviderFactoryForTest(t, provider)
	defer restore()
	svc := &PaymentService{
		entClient: client, loadBalancer: &captureLoadBalancer{},
		userRepo: &refundUserRepo{
			mockUserRepo: &mockUserRepo{deductAvailableBalanceFn: func(context.Context, int64, float64) (float64, error) {
				return 35, nil
			}},
			adjustBalanceFn: func(context.Context, int64, float64) (BalanceChange, error) {
				return BalanceChange{Old: 35, New: 35}, ErrBalanceNegative
			},
		},
	}
	plan := &RefundPlan{
		OrderID: order.ID, Order: order, RefundAmount: 100, GatewayAmount: 100,
		Reason: "non-force concurrent spend", Force: false, DeductBalance: true,
		DeductionType: payment.DeductionTypeBalance, BalanceToDeduct: 100,
	}

	result, err := svc.ExecuteRefund(ctx, plan)
	require.NoError(t, err)
	require.False(t, result.Success)
	require.True(t, result.RequireForce)
	require.Zero(t, provider.refundCalls.Load())
	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)
}

func TestPrepareRefundRejectsPendingResubmission(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "pending-resubmission")
	svc := &PaymentService{entClient: client}

	plan, result, err := svc.PrepareRefund(ctx, order.ID, order.RefundAmount, "retry pending", true, true)
	require.Nil(t, plan)
	require.Nil(t, result)
	require.Equal(t, "INVALID_STATUS", infraerrors.Reason(err))
}

func TestExecuteRefundRejectsStalePlanAfterOrderBecomesPending(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "pending-stale-plan")
	staleOrder := *order
	staleOrder.Status = OrderStatusCompleted

	provider := &countingRefundProvider{response: &payment.RefundResponse{Status: payment.ProviderStatusPending}}
	restore := replacePaymentProviderFactoryForTest(t, provider)
	defer restore()
	deductions := 0
	svc := &PaymentService{
		entClient: client, loadBalancer: &captureLoadBalancer{},
		userRepo: &mockUserRepo{deductAvailableBalanceFn: func(context.Context, int64, float64) (float64, error) {
			deductions++
			return 100, nil
		}},
	}
	plan := &RefundPlan{
		OrderID: order.ID, Order: &staleOrder, RefundAmount: 100, GatewayAmount: 100,
		Reason: "stale duplicate", Force: true, DeductBalance: true,
		DeductionType: payment.DeductionTypeBalance, BalanceToDeduct: 100,
	}

	result, err := svc.ExecuteRefund(ctx, plan)
	require.Nil(t, result)
	require.Equal(t, "CONFLICT", infraerrors.Reason(err))
	require.Zero(t, deductions)
	require.Zero(t, provider.refundCalls.Load())
}

func stringPointer(value string) *string { return &value }

type blockingRefundQueryProvider struct {
	refundProviderTestDouble
	ready    chan struct{}
	release  chan struct{}
	response *payment.RefundResponse
}

type refundUserRepo struct {
	*mockUserRepo
	adjustBalanceFn func(context.Context, int64, float64) (BalanceChange, error)
}

func (r *refundUserRepo) AdjustBalance(ctx context.Context, id int64, delta float64) (BalanceChange, error) {
	return r.adjustBalanceFn(ctx, id, delta)
}

func (p *blockingRefundQueryProvider) QueryRefund(context.Context, payment.RefundQueryRequest) (*payment.RefundResponse, error) {
	p.ready <- struct{}{}
	<-p.release
	return p.response, nil
}

type countingRefundProvider struct {
	refundProviderTestDouble
	refundCalls atomic.Int32
	response    *payment.RefundResponse
}

type refundSubscriptionRepo struct {
	userSubRepoNoop
	sub            *UserSubscription
	originalExpiry time.Time
	extendCalls    int
	extendCtx      context.Context
}

func (r *refundSubscriptionRepo) GetByID(context.Context, int64) (*UserSubscription, error) {
	if r.originalExpiry.IsZero() {
		r.originalExpiry = r.sub.ExpiresAt
	}
	copy := *r.sub
	return &copy, nil
}

func (r *refundSubscriptionRepo) ExtendExpiry(ctx context.Context, _ int64, expiresAt time.Time) error {
	r.extendCalls++
	r.extendCtx = ctx
	r.sub.ExpiresAt = expiresAt
	return nil
}

func (p *countingRefundProvider) Refund(context.Context, payment.RefundRequest) (*payment.RefundResponse, error) {
	p.refundCalls.Add(1)
	return p.response, nil
}

func (p *countingRefundProvider) QueryRefund(context.Context, payment.RefundQueryRequest) (*payment.RefundResponse, error) {
	return p.response, nil
}
