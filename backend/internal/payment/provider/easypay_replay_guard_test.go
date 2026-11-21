package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestEasyPayNotificationRejectsDuplicateFields(t *testing.T) {
	for _, field := range []string{"sign", "money", "pid", "trade_no", "trade_status", "out_trade_no", "sign_type"} {
		t.Run(field, func(t *testing.T) {
			p := &EasyPay{config: map[string]string{"pid": "fixture-merchant", "pkey": "fixture-secret"}}
			params := map[string]string{"pid": "fixture-merchant", "money": "80.00", "trade_no": "fixture-trade", "out_trade_no": "fixture-order", "trade_status": "TRADE_SUCCESS", "sign_type": "MD5"}
			params["sign"] = easyPaySign(params, "fixture-secret")
			values := url.Values{}
			for k, v := range params {
				values.Set(k, v)
			}
			_, err := p.VerifyNotification(context.Background(), values.Encode(), nil)
			require.NoError(t, err)
			values.Add(field, params[field])
			_, err = p.VerifyNotification(context.Background(), values.Encode(), nil)
			require.ErrorContains(t, err, "duplicate notify parameter")
		})
	}
}

func TestEasyPayQueryRejectsFalseSettlementEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"error HTTP with paid fields", `{"code":1,"status":1,"money":"80.00","trade_no":"t"}`, 500},
		{"failed API with paid fields", `{"code":0,"status":1,"money":"80.00","trade_no":"t"}`, 200},
		{"missing code with paid fields", `{"status":1,"money":"80.00","trade_no":"t"}`, 200},
		{"wrong order", `{"code":1,"out_trade_no":"different-order","status":1,"money":"80.00","trade_no":"t"}`, 200},
		{"wrong nested order", `{"code":1,"data":{"out_trade_no":"different-order","status":1,"money":"80.00","trade_no":"t"}}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			p := newTestEasyPay(t, server.URL)
			resp, err := p.QueryOrder(context.Background(), "fixture-order")
			require.Error(t, err)
			require.Nil(t, resp)
		})
	}
}

func TestEasyPayMissingGatewayTradeNumberStaysEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":1,"out_trade_no":"fixture-order","status":1,"money":"80.00"}`))
	}))
	defer server.Close()
	p := newTestEasyPay(t, server.URL)
	resp, err := p.QueryOrder(context.Background(), "fixture-order")
	require.NoError(t, err)
	require.Equal(t, payment.ProviderStatusPaid, resp.Status)
	require.Empty(t, resp.TradeNo, "merchant order number is not a gateway transaction number")
}

// 真实服务商变体（ylyun.net / 彩虹易支付衍生）会多带支付方生成的字段：
// 这些字段参与签名，必须被接受，否则合法回调 400、订单永远 PENDING。
func TestEasyPayNotificationAcceptsVariantFields(t *testing.T) {
	for _, extra := range []string{"bill_trade_no", "api_trade_no", "buyer", "subject", "trade_time"} {
		t.Run(extra, func(t *testing.T) {
			p := &EasyPay{config: map[string]string{"pid": "fixture-merchant", "pkey": "fixture-secret"}}
			params := map[string]string{"pid": "fixture-merchant", "money": "80.00", "trade_no": "fixture-trade", "out_trade_no": "fixture-order", "trade_status": "TRADE_SUCCESS", "sign_type": "MD5"}
			params[extra] = "provider-generated"
			params["sign"] = easyPaySign(params, "fixture-secret")
			values := url.Values{}
			for k, v := range params {
				values.Set(k, v)
			}
			got, err := p.VerifyNotification(context.Background(), values.Encode(), nil)
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Equal(t, payment.ProviderStatusSuccess, got.Status)
		})
	}
}

// 未参与签名的追加/篡改参数必须因签名失效被拒（覆盖 #7881 参数夹带面）。
func TestEasyPayNotificationRejectsUnsignedAppendedParams(t *testing.T) {
	p := &EasyPay{config: map[string]string{"pid": "fixture-merchant", "pkey": "fixture-secret"}}
	params := map[string]string{"pid": "fixture-merchant", "money": "80.00", "trade_no": "fixture-trade", "out_trade_no": "fixture-order", "trade_status": "TRADE_SUCCESS", "sign_type": "MD5"}
	params["sign"] = easyPaySign(params, "fixture-secret")

	// 攻击者无法重算签名：追加任意参数或改写金额都应失败。
	for name, mutate := range map[string]func(url.Values){
		"append return_url": func(v url.Values) { v.Set("return_url", "x&trade_status=TRADE_SUCCESS") },
		"append notify_url": func(v url.Values) { v.Set("notify_url", "https://evil.example/cb") },
		"raise money":       func(v url.Values) { v.Set("money", "0.01") },
		"downgrade status":  func(v url.Values) { v.Set("trade_status", "WAIT_BUYER_PAY") },
	} {
		t.Run(name, func(t *testing.T) {
			values := url.Values{}
			for k, v := range params {
				values.Set(k, v)
			}
			mutate(values)
			_, err := p.VerifyNotification(context.Background(), values.Encode(), nil)
			require.ErrorContains(t, err, "invalid signature")
		})
	}
}

// 必需字段缺失必须拒绝，不能把不完整回调当成成功。
func TestEasyPayNotificationRequiresSettlementFields(t *testing.T) {
	for _, missing := range []string{"pid", "trade_no", "out_trade_no", "money", "trade_status", "sign"} {
		t.Run(missing, func(t *testing.T) {
			p := &EasyPay{config: map[string]string{"pid": "fixture-merchant", "pkey": "fixture-secret"}}
			params := map[string]string{"pid": "fixture-merchant", "money": "80.00", "trade_no": "fixture-trade", "out_trade_no": "fixture-order", "trade_status": "TRADE_SUCCESS", "sign_type": "MD5"}
			params["sign"] = easyPaySign(params, "fixture-secret")
			delete(params, missing)
			values := url.Values{}
			for k, v := range params {
				values.Set(k, v)
			}
			_, err := p.VerifyNotification(context.Background(), values.Encode(), nil)
			require.Error(t, err)
		})
	}
}

func TestEasyPayNotificationAcceptsGenuineCallback(t *testing.T) {
	p := &EasyPay{config: map[string]string{"pid": "fixture-merchant", "pkey": "fixture-secret"}}
	params := map[string]string{"pid": "fixture-merchant", "money": "80.00", "trade_no": "fixture-trade", "out_trade_no": "fixture-order", "trade_status": "TRADE_SUCCESS", "sign_type": "MD5", "name": "fixture", "type": "alipay", "param": ""}
	params["sign"] = easyPaySign(params, "fixture-secret")
	values := url.Values{}
	for k, v := range params {
		values.Set(k, v)
	}
	got, err := p.VerifyNotification(context.Background(), values.Encode(), nil)
	require.NoError(t, err)
	require.NotNil(t, got)
}
