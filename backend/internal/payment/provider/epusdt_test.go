//go:build unit

package provider

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

const epusdtTestSecret = "test-epusdt-secret"

func newTestEPUSDT(t *testing.T, server *httptest.Server) *EPUSDT {
	t.Helper()
	prov, err := NewEPUSDT("test", map[string]string{
		"pid":       "1000",
		"secretKey": epusdtTestSecret,
		"apiBase":   "https://ep.example.test",
		"notifyUrl": "https://shop.example.test/api/v1/payments/callback",
		"returnUrl": "https://shop.example.test",
		"token":     "USDT",
		"network":   "bsc",
		"currency":  "CNY",
	})
	require.NoError(t, err)
	prov.config["apiBase"] = server.URL
	prov.httpClient = server.Client()
	return prov
}

func TestNewEPUSDTValidatesHTTPSConfig(t *testing.T) {
	_, err := NewEPUSDT("test", map[string]string{
		"pid":       "1000",
		"secretKey": epusdtTestSecret,
		"apiBase":   "http://ep.example.test",
		"notifyUrl": "https://shop.example.test/callback",
		"returnUrl": "https://shop.example.test",
		"token":     "USDT",
		"network":   "bsc",
		"currency":  "CNY",
	})
	require.ErrorContains(t, err, "HTTPS")
}

func TestEPUSDTCreatePaymentSignsPayloadAndReturnsRedirect(t *testing.T) {
	var received map[string]interface{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, epusdtCreatePath, r.URL.Path)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &received))
		require.Equal(t, "1000", received["pid"])
		require.Equal(t, "order-123", received["order_id"])
		require.Equal(t, "CNY", received["currency"])
		require.Equal(t, "USDT", received["token"])
		require.Equal(t, "bsc", received["network"])
		require.Equal(t, float64(12.34), received["amount"])
		require.Equal(t, expectedEPUSDTSignature(received, epusdtTestSecret), received["signature"])
		_, _ = w.Write([]byte(`{"status_code":200,"message":"success","data":{"trade_id":"trade-123","order_id":"order-123","amount":12.34,"payment_url":"/pay/trade-123"}}`))
	}))
	defer server.Close()

	prov := newTestEPUSDT(t, server)
	resp, err := prov.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID: "order-123",
		Amount:  "12.34",
		Subject: "充值",
	})
	require.NoError(t, err)
	require.Equal(t, "trade-123", resp.TradeNo)
	require.Equal(t, server.URL+"/pay/trade-123", resp.PayURL)
	require.Equal(t, "CNY", resp.Currency)
	require.Equal(t, payment.CreatePaymentResultOrderCreated, resp.ResultType)
}

func TestEPUSDTQueryOrderMapsStatuses(t *testing.T) {
	statuses := map[string]int{"pending": 1, "paid": 2, "expired": 3, "selection": 4}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := 1
		switch r.URL.Path {
		case epusdtStatusPath + "paid":
			status = 2
		case epusdtStatusPath + "expired":
			status = 3
		case epusdtStatusPath + "selection":
			status = 4
		}
		_, _ = w.Write([]byte(`{"status_code":200,"message":"success","data":{"trade_id":"` + strings.TrimPrefix(r.URL.Path, epusdtStatusPath) + `","status":` + string(rune('0'+status)) + `}}`))
	}))
	defer server.Close()
	prov := newTestEPUSDT(t, server)

	for name, wantStatus := range statuses {
		resp, err := prov.QueryOrder(context.Background(), name)
		require.NoError(t, err, name)
		switch wantStatus {
		case 1, 4:
			require.Equal(t, payment.ProviderStatusPending, resp.Status)
		case 2:
			require.Equal(t, payment.ProviderStatusPaid, resp.Status)
		case 3:
			require.Equal(t, payment.ProviderStatusFailed, resp.Status)
		}
	}
}

func TestEPUSDTVerifyNotificationBindsPIDAndSignature(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	prov := newTestEPUSDT(t, server)

	payload := map[string]interface{}{
		"pid": "1000", "trade_id": "trade-123", "order_id": "order-123",
		"amount": 12.34, "token": "USDT", "status": 2,
		"block_transaction_id": "0xabc",
	}
	payload["signature"] = expectedEPUSDTSignature(payload, epusdtTestSecret)
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	notification, err := prov.VerifyNotification(context.Background(), string(body), nil)
	require.NoError(t, err)
	require.Equal(t, "trade-123", notification.TradeNo)
	require.Equal(t, "order-123", notification.OrderID)
	require.Equal(t, payment.NotificationStatusSuccess, notification.Status)
	require.NotContains(t, notification.RawData, epusdtTestSecret)
	require.NotContains(t, notification.RawData, "0xabc")

	payload["pid"] = "9999"
	body, err = json.Marshal(payload)
	require.NoError(t, err)
	_, err = prov.VerifyNotification(context.Background(), string(body), nil)
	require.ErrorContains(t, err, "pid mismatch")

	payload["pid"] = "1000"
	payload["signature"] = strings.Repeat("0", sha256.Size*2)
	body, err = json.Marshal(payload)
	require.NoError(t, err)
	_, err = prov.VerifyNotification(context.Background(), string(body), nil)
	require.ErrorContains(t, err, "signature mismatch")
}

func TestEPUSDTRejectsUntrustedPaymentURLAndRefund(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status_code":200,"message":"success","data":{"trade_id":"trade-123","order_id":"order-123","payment_url":"https://evil.example.test/pay"}}`))
	}))
	defer server.Close()
	prov := newTestEPUSDT(t, server)
	_, err := prov.CreatePayment(context.Background(), payment.CreatePaymentRequest{OrderID: "order-123", Amount: "1"})
	require.ErrorContains(t, err, "host mismatch")
	_, err = prov.Refund(context.Background(), payment.RefundRequest{TradeNo: "trade-123"})
	require.ErrorContains(t, err, "not supported")
}

func expectedEPUSDTSignature(values map[string]interface{}, secret string) string {
	parts := make([]string, 0, len(values))
	for key, value := range values {
		if key == "signature" || value == nil {
			continue
		}
		var formatted string
		switch v := value.(type) {
		case string:
			formatted = v
		case float64:
			formatted = strconv.FormatFloat(v, 'f', -1, 64)
		case int:
			formatted = strconv.Itoa(v)
		case bool:
			formatted = strconv.FormatBool(v)
		}
		if formatted != "" {
			parts = append(parts, key+"="+formatted)
		}
	}
	sort.Strings(parts)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strings.Join(parts, "&")))
	return hex.EncodeToString(mac.Sum(nil))
}
