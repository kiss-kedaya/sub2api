package provider

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

// easyPayFixturePkey replaces the real merchant pkey in the production sample.
// Signatures below are recomputed with this test-only key.
const easyPayFixturePkey = "fixture-test-pkey"

func easyPayFixtureProvider() *EasyPay {
	return &EasyPay{config: map[string]string{"pid": "2617", "pkey": easyPayFixturePkey}}
}

func easyPayEncodeSigned(t *testing.T, p *EasyPay, params map[string]string) string {
	t.Helper()
	params["sign"] = easyPaySign(params, p.config["pkey"])
	values := url.Values{}
	for k, v := range params {
		values.Set(k, v)
	}
	return values.Encode()
}

// TestEasyPayNotificationAcceptsProductionBillTradeNoSample replays the
// 2026-11-21 ylyun.net callback (provider instance id=8, pid=2617) that used to
// be rejected with "unexpected notify param: bill_trade_no" -> HTTP 400 ->
// order stuck PENDING. Only the merchant pkey is replaced with a test value and
// the MD5 signature is recomputed.
func TestEasyPayNotificationAcceptsProductionBillTradeNoSample(t *testing.T) {
	p := easyPayFixtureProvider()
	params := map[string]string{
		"pid":           "2617",
		"trade_no":      "2026112117115422801",
		"out_trade_no":  "sub2_20261121mW6OsKVJ",
		"type":          "alipay",
		"name":          "product",
		"money":         "10",
		"trade_status":  "TRADE_SUCCESS",
		"bill_trade_no": "752026112123001417571454642716",
		"sign_type":     "MD5",
	}
	raw := easyPayEncodeSigned(t, p, params)

	got, err := p.VerifyNotification(context.Background(), raw, nil)
	require.NoError(t, err, "the real variant callback must verify; a 400 here strands the order as PENDING")
	require.Equal(t, payment.ProviderStatusSuccess, got.Status)
	require.Equal(t, "sub2_20261121mW6OsKVJ", got.OrderID)
	require.Equal(t, "2026112117115422801", got.TradeNo)
	require.Equal(t, 10.0, got.Amount)
	require.Equal(t, "2617", got.Metadata["pid"])
}

// TestEasyPayNotificationAcceptsProviderVariantFields keeps future
// EasyPay-compatible variants working without a code change: any extra field is
// accepted as long as it participates in the signature.
func TestEasyPayNotificationAcceptsProviderVariantFields(t *testing.T) {
	for _, extra := range []string{
		"bill_trade_no", "api_trade_no", "buyer", "subject", "trade_time",
		"addtime", "endtime", "timestamp", "custom_data", "future_variant_field",
	} {
		t.Run(extra, func(t *testing.T) {
			p := easyPayFixtureProvider()
			params := map[string]string{
				"pid": "2617", "trade_no": "trade-1", "out_trade_no": "order-1",
				"money": "10", "trade_status": "TRADE_SUCCESS", "sign_type": "MD5",
			}
			params[extra] = "provider-generated"
			raw := easyPayEncodeSigned(t, p, params)

			got, err := p.VerifyNotification(context.Background(), raw, nil)
			require.NoError(t, err)
			require.Equal(t, payment.ProviderStatusSuccess, got.Status)
		})
	}
}

func TestEasyPayNotificationRejectsTamperedSignature(t *testing.T) {
	p := easyPayFixtureProvider()
	params := map[string]string{
		"pid": "2617", "trade_no": "trade-1", "out_trade_no": "order-1",
		"money": "10", "trade_status": "TRADE_SUCCESS", "sign_type": "MD5",
	}
	raw := easyPayEncodeSigned(t, p, params)
	parsed, err := url.ParseQuery(raw)
	require.NoError(t, err)
	parsed.Set("sign", strings.Repeat("0", len(parsed.Get("sign"))))
	_, err = p.VerifyNotification(context.Background(), parsed.Encode(), nil)
	require.ErrorContains(t, err, "invalid signature")
}

func TestEasyPayNotificationRequiresBusinessParams(t *testing.T) {
	for _, missing := range []string{"pid", "trade_no", "out_trade_no", "money", "trade_status", "sign"} {
		t.Run(missing, func(t *testing.T) {
			p := easyPayFixtureProvider()
			params := map[string]string{
				"pid": "2617", "trade_no": "trade-1", "out_trade_no": "order-1",
				"money": "10", "trade_status": "TRADE_SUCCESS", "sign_type": "MD5",
			}
			delete(params, missing)
			if missing != "sign" {
				params["sign"] = easyPaySign(params, p.config["pkey"])
			}
			values := url.Values{}
			for k, v := range params {
				values.Set(k, v)
			}
			_, err := p.VerifyNotification(context.Background(), values.Encode(), nil)
			require.Error(t, err)
		})
	}
}

func TestEasyPayNotificationNonSuccessStatusIsNotSuccess(t *testing.T) {
	for _, status := range []string{"TRADE_CLOSED", "WAIT_BUYER_PAY", "trade_success", ""} {
		t.Run(status, func(t *testing.T) {
			p := easyPayFixtureProvider()
			params := map[string]string{
				"pid": "2617", "trade_no": "trade-1", "out_trade_no": "order-1",
				"money": "10", "trade_status": status, "sign_type": "MD5",
			}
			raw := easyPayEncodeSigned(t, p, params)
			got, err := p.VerifyNotification(context.Background(), raw, nil)
			if status == "" {
				require.Error(t, err, "missing trade_status must be rejected")
				return
			}
			require.NoError(t, err)
			require.NotEqual(t, payment.ProviderStatusSuccess, got.Status)
		})
	}
}

func TestEasyPayNotificationRejectsInvalidMoney(t *testing.T) {
	for _, money := range []string{"abc", "NaN", "Inf"} {
		t.Run(money, func(t *testing.T) {
			p := easyPayFixtureProvider()
			params := map[string]string{
				"pid": "2617", "trade_no": "trade-1", "out_trade_no": "order-1",
				"money": money, "trade_status": "TRADE_SUCCESS", "sign_type": "MD5",
			}
			raw := easyPayEncodeSigned(t, p, params)
			_, err := p.VerifyNotification(context.Background(), raw, nil)
			require.ErrorContains(t, err, "invalid notify money")
		})
	}
}

// --- upstream #7881: signed checkout URL replayed as a notification ---

func easyPaySignedCheckoutURL(t *testing.T) (*url.URL, string) {
	t.Helper()
	p := &EasyPay{config: map[string]string{
		"pid":         "fixture-merchant",
		"pkey":        easyPayFixturePkey,
		"apiBase":     "https://pay.invalid",
		"notifyUrl":   "https://shop.invalid/webhook/easypay",
		"returnUrl":   "https://shop.invalid/payment/result",
		"paymentMode": "popup",
	}}
	const fakeTradeNo = "POC-FAKE-TRADE"
	// A legacy order-creation path signed a return_url that carries a query
	// string. Because the MD5 base string concatenates values unescaped, this
	// signature can be re-cut into a forged success notification.
	returnURL := "https://shop.invalid/payment/result?order_id=42&out_trade_no=fixture-order&trade_no=" +
		fakeTradeNo + "&trade_status=TRADE_SUCCESS"
	resp, err := p.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID:     "fixture-order",
		Amount:      "80.00",
		PaymentType: payment.TypeAlipay,
		Subject:     "fixture",
		ReturnURL:   returnURL,
	})
	require.NoError(t, err)
	parsed, err := url.Parse(resp.PayURL)
	require.NoError(t, err)
	return parsed, fakeTradeNo
}

func easyPayRegroupedReplay(t *testing.T, dropCarrierKey bool) (url.Values, string) {
	t.Helper()
	parsed, fakeTradeNo := easyPaySignedCheckoutURL(t)
	signed := parsed.Query()
	marker := "&trade_no=" + fakeTradeNo + "&trade_status=TRADE_SUCCESS"
	prefix := strings.SplitN(signed.Get("return_url"), marker, 2)[0]

	out := url.Values{}
	for k, vs := range signed {
		if k == "sign" {
			continue
		}
		if dropCarrierKey && k == "notify_url" {
			continue
		}
		out.Set(k, vs[0])
	}
	if dropCarrierKey {
		// Hide the checkout-only carrier text inside a callback field instead of
		// keeping it as a top-level key.
		out.Set("name", signed.Get("name")+"&notify_url="+signed.Get("notify_url"))
	}
	out.Set("return_url", prefix)
	out.Set("trade_no", fakeTradeNo)
	out.Set("trade_status", "TRADE_SUCCESS")
	out.Set("sign", signed.Get("sign"))
	return out, signed.Get("sign")
}

func TestEasyPayNotificationRejectsSignedCheckoutReplay(t *testing.T) {
	for _, dropCarrierKey := range []bool{false, true} {
		name := "carrier key kept"
		if dropCarrierKey {
			name = "carrier text embedded in name"
		}
		t.Run(name, func(t *testing.T) {
			values, originalSign := easyPayRegroupedReplay(t, dropCarrierKey)
			params := map[string]string{}
			for k, vs := range values {
				params[k] = vs[0]
			}
			// The regex trick keeps the signature valid: this is why the signature
			// alone cannot distinguish the replay from the signed checkout.
			require.Equal(t, originalSign, easyPaySign(params, easyPayFixturePkey))

			p := &EasyPay{config: map[string]string{"pid": "fixture-merchant", "pkey": easyPayFixturePkey}}
			_, err := p.VerifyNotification(context.Background(), values.Encode(), nil)
			require.Error(t, err, "#7881 signed-checkout replay must not verify as a notification")
		})
	}
}
