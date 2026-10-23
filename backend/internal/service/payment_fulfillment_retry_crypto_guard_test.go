//go:build unit

package service

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestRetryFulfillmentRejectsCryptoOrdersForEveryRetryableOrTerminalState(t *testing.T) {
	for _, status := range []string{OrderStatusPaid, OrderStatusFailed, OrderStatusRecharging, OrderStatusCompleted} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			order := createCryptoRetryGuardOrder(t, ctx, client, status, payment.TypeCrypto, payment.TypeCrypto, map[string]any{
				"chain": "bsc", "token": "0x" + strings.Repeat("ab", 20), "raw_amount": "1000000",
				"decimals": 6, "block": uint64(100), "log_index": 7,
				"tx":   "0x" + strings.Repeat("cd", 32),
				"from": "0x" + strings.Repeat("ef", 20), "to": "0x" + strings.Repeat("12", 20),
			})
			service := &PaymentService{entClient: client}

			err := service.RetryFulfillment(ctx, order.ID)
			require.Equal(t, "CRYPTO_RETRY_REQUIRES_LEDGER", infraerrors.Reason(err))
			require.Equal(t, http.StatusForbidden, infraerrors.Code(err))

			unchanged, loadErr := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, loadErr)
			require.Equal(t, status, unchanged.Status, "generic retry must not claim or mutate the order")
			require.Zero(t, client.PaymentAuditLog.Query().CountX(ctx), "rejected retry must not write a retry audit event")
			require.Zero(t, client.RedeemCode.Query().CountX(ctx), "rejected retry must not create/consume a credit code")
			require.Zero(t, client.User.GetX(ctx, order.UserID).Balance, "rejected retry must not credit balance")
		})
	}
}

func TestCryptoRetryGuardRecognizesLegacyAndCurrentOrderMarkers(t *testing.T) {
	legacyDepositPayload := map[string]any{
		"chain": "bsc", "token": "0x" + strings.Repeat("ab", 20), "raw_amount": "1000000",
		"decimals": 6, "block": uint64(100), "log_index": float64(7),
		"tx":   "0x" + strings.Repeat("cd", 32),
		"from": "0x" + strings.Repeat("ef", 20), "to": "0x" + strings.Repeat("12", 20),
	}
	tests := []struct {
		name         string
		paymentType  string
		providerKey  string
		providerData map[string]any
		wantCrypto   bool
	}{
		{name: "current explicit type", paymentType: payment.TypeCrypto, providerKey: payment.TypeCrypto, wantCrypto: true},
		{name: "legacy crypto payment type", paymentType: payment.TypeCrypto, providerKey: payment.TypeAlipay, wantCrypto: true},
		{name: "provider column marker", paymentType: payment.TypeAlipay, providerKey: payment.TypeCrypto, wantCrypto: true},
		{name: "provider snapshot marker", paymentType: payment.TypeAlipay, providerKey: payment.TypeAlipay, providerData: map[string]any{"provider_key": payment.TypeCrypto}, wantCrypto: true},
		{name: "legacy chain deposit payload", paymentType: payment.TypeAlipay, providerData: legacyDepositPayload, wantCrypto: true},
		{name: "legacy deposit payload without transfer endpoints", paymentType: payment.TypeAlipay, providerKey: payment.TypeAlipay, providerData: map[string]any{
			"chain": "bsc", "token": "0x" + strings.Repeat("ab", 20),
			"raw_amount": "1000000", "decimals": 6, "block": uint64(100), "log_index": 7,
			"tx": "0x" + strings.Repeat("cd", 32),
		}},
		{name: "legacy deposit payload without block metadata", paymentType: payment.TypeAlipay, providerKey: payment.TypeAlipay, providerData: map[string]any{
			"chain": "bsc", "token": "0x" + strings.Repeat("ab", 20), "raw_amount": "1000000",
			"decimals": 6, "log_index": 7, "tx": "0x" + strings.Repeat("cd", 32),
			"from": "0x" + strings.Repeat("ef", 20), "to": "0x" + strings.Repeat("12", 20),
		}},
		{name: "legacy payload with overflowed integer", paymentType: payment.TypeAlipay, providerKey: payment.TypeAlipay, providerData: map[string]any{
			"chain": "bsc", "token": "0x" + strings.Repeat("ab", 20), "raw_amount": "1000000",
			"decimals": uint64(6), "block": uint64(100), "log_index": float64(18446744073709551616),
			"tx":   "0x" + strings.Repeat("cd", 32),
			"from": "0x" + strings.Repeat("ef", 20), "to": "0x" + strings.Repeat("12", 20),
		}},
		{name: "legacy payload tied to another trade number", paymentType: payment.TypeAlipay, providerKey: payment.TypeAlipay, providerData: legacyDepositPayload},
		{name: "ordinary RPC transaction payload", paymentType: payment.TypeAlipay, providerKey: payment.TypeAlipay, providerData: map[string]any{
			"provider_key": payment.TypeAlipay, "currency": "USD", "request_id": "req-123", "trade_no": "trade-456",
		}},
		{name: "ordinary payload with partial chain metadata", paymentType: payment.TypeAlipay, providerData: map[string]any{"chain": "bsc", "tx": "0xtxhash", "request_id": "req-123"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			order := &dbent.PaymentOrder{
				PaymentType:      test.paymentType,
				OrderType:        payment.OrderTypeBalance,
				ProviderKey:      &test.providerKey,
				ProviderSnapshot: test.providerData,
			}
			if test.name == "legacy chain deposit payload" {
				order.PaymentTradeNo = legacyDepositPayload["tx"].(string)
			}
			if test.providerKey == "" {
				order.ProviderKey = nil
			}
			if test.name == "legacy payload tied to another trade number" {
				order.PaymentTradeNo = "ordinary-rpc-trade"
			}
			require.Equal(t, test.wantCrypto, isCryptoDepositOrder(order))
		})
	}
}

func TestRetryFulfillmentKeepsNonCryptoOrderBehavior(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	completedOrder := createCryptoRetryGuardOrder(t, ctx, client, OrderStatusCompleted, payment.TypeAlipay, payment.TypeAlipay, map[string]any{
		"provider_key": payment.TypeAlipay, "schema_version": 2, "currency": "USD", "trade_no": "legacy-rpc-trade",
	})
	service := &PaymentService{entClient: client}

	err := service.RetryFulfillment(ctx, completedOrder.ID)
	require.Equal(t, "INVALID_STATUS", infraerrors.Reason(err), "ordinary completed payments retain the existing completion guard")
	require.NotEqual(t, "CRYPTO_RETRY_REQUIRES_LEDGER", infraerrors.Reason(err))
	reloaded, loadErr := client.PaymentOrder.Get(ctx, completedOrder.ID)
	require.NoError(t, loadErr)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)

	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	failedOrder := createCryptoRetryGuardOrder(t, ctx, client, OrderStatusFailed, payment.TypeAlipay, payment.TypeAlipay, map[string]any{
		"provider_key": payment.TypeAlipay, "schema_version": 2, "currency": "USD", "trade_no": "legacy-rpc-failed-trade",
	})
	var credited float64
	userRepo := &mockUserRepo{getByIDUser: &User{ID: failedOrder.UserID}}
	userRepo.updateBalanceFn = func(_ context.Context, userID int64, amount float64) error {
		require.Equal(t, failedOrder.UserID, userID)
		credited += amount
		return nil
	}
	redeemRepo := &paymentFulfillmentRedeemRepo{}
	cache := &paymentFulfillmentRedeemCacheStub{}
	redeemService := NewRedeemService(redeemRepo, userRepo, nil, cache, nil, client, nil, nil)
	service = &PaymentService{entClient: client, redeemService: redeemService, userRepo: userRepo}

	require.NoError(t, service.RetryFulfillment(ctx, failedOrder.ID))
	require.Equal(t, failedOrder.Amount, credited, "ordinary FAILED checkout retries still follow the existing fulfillment path")
	completed, loadErr := client.PaymentOrder.Get(ctx, failedOrder.ID)
	require.NoError(t, loadErr)
	require.Equal(t, OrderStatusCompleted, completed.Status)
	require.Equal(t, 1, redeemRepo.createCalls)
}

func TestCryptoOrderStillUsesInternalBalanceFulfillmentForLedgerRecovery(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)
	ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
	order := createCryptoRetryGuardOrder(t, ctx, client, OrderStatusPaid, payment.TypeCrypto, payment.TypeCrypto, nil)

	var credited float64
	userRepo := &mockUserRepo{getByIDUser: &User{ID: order.UserID}}
	userRepo.updateBalanceFn = func(_ context.Context, userID int64, amount float64) error {
		require.Equal(t, order.UserID, userID)
		credited += amount
		return nil
	}
	redeemRepo := &paymentFulfillmentRedeemRepo{}
	redeemService := NewRedeemService(redeemRepo, userRepo, nil, nil, nil, client, nil, nil)
	service := &PaymentService{entClient: client, redeemService: redeemService, userRepo: userRepo}

	require.NoError(t, service.ExecuteBalanceFulfillment(ctx, order.ID))
	require.Equal(t, order.Amount, credited)
	require.Equal(t, 1, redeemRepo.createCalls)
	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusCompleted, reloaded.Status)
}

var cryptoRetryGuardSequence atomic.Uint64

func createCryptoRetryGuardOrder(
	t *testing.T, ctx context.Context, client *dbent.Client, status, paymentType, providerKey string, snapshot map[string]any,
) *dbent.PaymentOrder {
	t.Helper()
	sequence := strconv.FormatUint(cryptoRetryGuardSequence.Add(1), 10)
	user, err := client.User.Create().
		SetEmail("retry-guard-" + sequence + "@example.invalid").
		SetPasswordHash("test-only").
		SetUsername("retry-guard-user").
		Save(ctx)
	require.NoError(t, err)

	orderBuilder := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(10).
		SetPayAmount(10).
		SetFeeRate(0).
		SetRechargeCode("retry-guard-" + sequence).
		SetOutTradeNo("retry-guard-" + sequence).
		SetPaymentType(paymentType).
		SetPaymentTradeNo("test-trade").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(status).
		SetPaidAt(time.Now().Add(-time.Minute)).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("crypto-test").
		SetSrcHost("crypto-test")
	if providerKey != "" {
		orderBuilder.SetProviderKey(providerKey)
	}
	if snapshot != nil {
		orderBuilder.SetProviderSnapshot(snapshot)
	}
	order, err := orderBuilder.Save(ctx)
	require.NoError(t, err)
	return order
}
