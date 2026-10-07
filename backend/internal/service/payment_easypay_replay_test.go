//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentauditlog"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	paymentprovider "github.com/Wei-Shaw/sub2api/internal/payment/provider"
	"github.com/stretchr/testify/require"
)

func TestEasyPaySignedSuccessRequiresProviderSettlement(t *testing.T) {
	for _, tc := range []struct {
		name, queryBody, callbackTradeNo, wantReason string
		httpStatus, wantQueries                      int
		paid                                         bool
	}{
		{"unpaid order", `{"code":1,"out_trade_no":"ORDER","status":0,"money":"80.0000","trade_no":"gateway-trade"}`, "gateway-trade", "not paid", 200, 1, false},
		{"upstream amount differs", `{"code":1,"out_trade_no":"ORDER","status":1,"money":"79.0000","trade_no":"gateway-trade"}`, "gateway-trade", "amount mismatch", 200, 1, false},
		{"transaction differs", `{"code":1,"out_trade_no":"ORDER","status":1,"money":"80.0000","trade_no":"other-trade"}`, "gateway-trade", "trade_no mismatch", 200, 1, false},
		{"upstream query unavailable", `{"code":1,"out_trade_no":"ORDER","status":1,"money":"80.0000","trade_no":"gateway-trade"}`, "gateway-trade", "query failed", 500, 1, false},
		{"missing callback transaction", `{"code":1,"out_trade_no":"ORDER","status":1,"money":"80.0000","trade_no":"gateway-trade"}`, "", "missing callback trade_no", 200, 0, false},
		{"paid order", `{"code":1,"out_trade_no":"ORDER","status":1,"money":"80.0000","trade_no":"gateway-trade"}`, "gateway-trade", "", 200, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := newPaymentConfigServiceTestClient(t)
			ensurePaymentAuditOrderActionUniqueIndex(t, ctx, client)
			order := createPaymentFulfillmentSubscriptionOrder(t, ctx, client, OrderStatusPending, time.Now())
			queries := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				queries++
				require.Equal(t, http.MethodPost, r.Method)
				require.Equal(t, "/api.php", r.URL.Path)
				require.NoError(t, r.ParseForm())
				require.Equal(t, "order", r.PostForm.Get("act"))
				require.Equal(t, "fixture-merchant", r.PostForm.Get("pid"))
				require.Equal(t, "fixture-only-secret", r.PostForm.Get("key"))
				require.Equal(t, order.OutTradeNo, r.PostForm.Get("out_trade_no"))
				w.WriteHeader(tc.httpStatus)
				_, _ = fmt.Fprint(w, strings.ReplaceAll(tc.queryBody, "ORDER", order.OutTradeNo))
			}))
			t.Cleanup(server.Close)
			inst, err := client.PaymentProviderInstance.Create().
				SetProviderKey(payment.TypeEasyPay).
				SetName("original-instance").
				SetConfig(encryptWebhookProviderConfig(t, map[string]string{
					"pid": "fixture-merchant", "pkey": "fixture-only-secret", "apiBase": server.URL,
					"notifyUrl": "https://shop.invalid/webhook", "returnUrl": "https://shop.invalid/payment/result",
				})).
				SetSupportedTypes("alipay").
				SetEnabled(true).
				Save(ctx)
			require.NoError(t, err)
			instanceID := strconv.FormatInt(inst.ID, 10)
			order, err = client.PaymentOrder.UpdateOneID(order.ID).
				SetOrderType(payment.OrderTypeBalance).
				ClearPlanID().ClearSubscriptionGroupID().ClearSubscriptionDays().
				SetPaymentType(payment.TypeAlipay).
				SetPaymentTradeNo("").
				SetProviderInstanceID(instanceID).
				SetProviderKey(payment.TypeEasyPay).
				SetProviderSnapshot(map[string]any{
					"schema_version": 2, "provider_instance_id": instanceID,
					"provider_key": payment.TypeEasyPay, "merchant_id": "fixture-merchant",
				}).Save(ctx)
			require.NoError(t, err)
			credited := 0.0
			userRepo := &mockUserRepo{getByIDUser: &User{ID: order.UserID}}
			userRepo.updateBalanceFn = func(_ context.Context, id int64, amount float64) error {
				require.Equal(t, order.UserID, id)
				credited += amount
				return nil
			}
			redeemRepo := &paymentFulfillmentRedeemRepo{}
			redeem := NewRedeemService(redeemRepo, userRepo, nil, &paymentFulfillmentRedeemCacheStub{}, nil, client, nil, nil)
			svc := &PaymentService{
				entClient: client, loadBalancer: newWebhookProviderTestLoadBalancer(client),
				redeemService: redeem, userRepo: userRepo,
			}
			notification := &payment.PaymentNotification{
				TradeNo: tc.callbackTradeNo, OrderID: order.OutTradeNo, Amount: order.PayAmount,
				Status: payment.ProviderStatusSuccess, Metadata: map[string]string{"pid": "fixture-merchant"},
			}
			if tc.name == "unpaid order" {
				// Reproduce the supplied script's signature reuse against a fixture
				// checkout URL from the old path. The fixed order-creation path now
				// rejects this return_url before any checkout signature is issued.
				const fakeTradeNo = "POC-FAKE-TRADE"
				oldReturnQuery := url.Values{
					"order_id": {strconv.FormatInt(order.ID, 10)}, "out_trade_no": {order.OutTradeNo},
					"status": {"success"}, "trade_no": {fakeTradeNo}, "trade_status": {"TRADE_SUCCESS"},
				}
				oldReturnURL := "https://shop.invalid/payment/result?" + oldReturnQuery.Encode()
				_, guardErr := CanonicalizeReturnURL(oldReturnURL, "shop.invalid", "")
				require.Error(t, guardErr, "new orders must not expose a replayable checkout URL")
				checkoutProvider, createErr := paymentprovider.NewEasyPay("fixture-instance", map[string]string{
					"pid": "fixture-merchant", "pkey": "fixture-only-secret", "apiBase": server.URL,
					"notifyUrl": "https://shop.invalid/webhook", "returnUrl": "https://shop.invalid/payment/result",
					"paymentMode": "popup",
				})
				require.NoError(t, createErr)
				checkout, createErr := checkoutProvider.CreatePayment(ctx, payment.CreatePaymentRequest{
					OrderID: order.OutTradeNo, Amount: "80.00", PaymentType: payment.TypeAlipay,
					Subject: "fixture", ReturnURL: oldReturnURL,
				})
				require.NoError(t, createErr)
				checkoutURL, parseErr := url.Parse(checkout.PayURL)
				require.NoError(t, parseErr)
				signed := checkoutURL.Query()
				marker := "&trade_no=" + fakeTradeNo + "&trade_status=TRADE_SUCCESS"
				require.Contains(t, signed.Get("return_url"), marker)
				signed.Set("return_url", strings.SplitN(signed.Get("return_url"), marker, 2)[0])
				signed.Set("trade_no", fakeTradeNo)
				signed.Set("trade_status", "TRADE_SUCCESS")
				// Upstream #7881: the provider now rejects any parameter outside the async
				// notify set, so the replayed checkout signature dies before the service layer.
				_, createErr = checkoutProvider.VerifyNotification(ctx, signed.Encode(), nil)
				require.ErrorContains(t, createErr, "unexpected notify param")
			}
			err = svc.HandlePaymentNotification(ctx, notification, payment.TypeEasyPay)
			require.Equal(t, tc.wantQueries, queries)
			reloaded, getErr := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, getErr)
			if tc.paid {
				require.NoError(t, err)
				require.Equal(t, OrderStatusCompleted, reloaded.Status)
				require.InDelta(t, order.Amount, credited, 1e-8)
				require.Len(t, redeemRepo.useCalls, 1)
				// Replayed valid success is idempotent and needs no new provider query.
				require.NoError(t, svc.HandlePaymentNotification(ctx, notification, payment.TypeEasyPay))
				require.Equal(t, 1, queries)
				require.Len(t, redeemRepo.useCalls, 1)
				_, err = client.PaymentOrder.UpdateOneID(order.ID).SetStatus(OrderStatusRefunded).Save(ctx)
				require.NoError(t, err)
				require.NoError(t, svc.HandlePaymentNotification(ctx, notification, payment.TypeEasyPay))
				require.Equal(t, 1, queries, "refund replay must not call upstream or re-credit")
				require.Len(t, redeemRepo.useCalls, 1)
			} else {
				require.ErrorContains(t, err, tc.wantReason)
				require.Equal(t, OrderStatusPending, reloaded.Status)
				require.Zero(t, credited)
				require.Empty(t, redeemRepo.useCalls)
				count, auditErr := client.PaymentAuditLog.Query().Where(paymentauditlog.ActionEQ("PAYMENT_UPSTREAM_VERIFY_FAILED")).Count(ctx)
				require.NoError(t, auditErr)
				require.Equal(t, 1, count)
			}
		})
	}
}

func TestEasyPayNotificationWithoutPinnedProviderFailsClosed(t *testing.T) {
	svc := &PaymentService{}
	err := svc.verifyEasyPaySettlement(context.Background(), &dbent.PaymentOrder{
		OutTradeNo: "fixture-order", PayAmount: 80,
	}, "fixture-trade", 80)
	require.ErrorContains(t, err, "not pinned")
}
