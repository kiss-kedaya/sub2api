package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

func TestEasyPayQueryOrderStatusMapping(t *testing.T) {
	t.Parallel()

	const orderID = "order-123"
	tests := []struct {
		name        string
		body        string
		wantStatus  string
		wantTradeNo string
		wantAmount  float64
		wantError   bool
	}{
		{
			name:        "top level trade success is paid",
			body:        `{"code":1,"trade_status":"TRADE_SUCCESS","status":0,"money":"12.34","trade_no":"gateway-123"}`,
			wantStatus:  payment.ProviderStatusPaid,
			wantTradeNo: "gateway-123",
			wantAmount:  12.34,
		},
		{
			name:        "waiting trade status with paid numeric status stays pending",
			body:        `{"code":1,"trade_status":"WAITING","status":1,"money":"12.34","trade_no":"gateway-123"}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: "gateway-123",
			wantAmount:  12.34,
		},
		{
			name:        "empty trade status with paid numeric status stays pending",
			body:        `{"code":1,"trade_status":"","status":1,"money":"12.34"}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: "",
			wantAmount:  12.34,
		},
		{
			name:        "nested data trade success is paid",
			body:        `{"code":1,"data":{"trade_status":"TRADE_SUCCESS","status":0,"money":"9.99","trade_no":"data-456"}}`,
			wantStatus:  payment.ProviderStatusPaid,
			wantTradeNo: "data-456",
			wantAmount:  9.99,
		},
		{
			name:        "legacy numeric paid status remains compatible",
			body:        `{"code":1,"status":1,"money":"3.21"}`,
			wantStatus:  payment.ProviderStatusPaid,
			wantTradeNo: "",
			wantAmount:  3.21,
		},
		{
			name:        "legacy numeric non paid status is pending",
			body:        `{"code":1,"status":0,"money":"3.21"}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: "",
			wantAmount:  3.21,
		},
		{
			name:        "top level string paid status is paid",
			body:        `{"code":1,"status":"1","money":"12.34","trade_no":"gateway-str-1"}`,
			wantStatus:  payment.ProviderStatusPaid,
			wantTradeNo: "gateway-str-1",
			wantAmount:  12.34,
		},
		{
			name:        "top level string paid status with surrounding whitespace is paid",
			body:        `{"code":" 1 ","status":" 1 ","money":" 12.34 ","trade_no":" gateway-str-ws "}`,
			wantStatus:  payment.ProviderStatusPaid,
			wantTradeNo: "gateway-str-ws",
			wantAmount:  12.34,
		},
		{
			name:        "top level string unpaid status is pending",
			body:        `{"code":1,"status":"0","money":"12.34","trade_no":"gateway-str-0"}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: "gateway-str-0",
			wantAmount:  12.34,
		},
		{
			name:        "waiting trade status with string paid status stays pending",
			body:        `{"code":1,"trade_status":"WAITING","status":"1","money":"12.34","trade_no":"gateway-wait"}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: "gateway-wait",
			wantAmount:  12.34,
		},
		{
			name:        "null trade status falls back to string paid status",
			body:        `{"code":1,"trade_status":null,"status":"1","money":"12.34","trade_no":"gateway-null-ts"}`,
			wantStatus:  payment.ProviderStatusPaid,
			wantTradeNo: "gateway-null-ts",
			wantAmount:  12.34,
		},
		{
			name:        "nested data integer paid status is paid",
			body:        `{"code":1,"data":{"status":1,"money":"9.99","trade_no":"data-int-1"}}`,
			wantStatus:  payment.ProviderStatusPaid,
			wantTradeNo: "data-int-1",
			wantAmount:  9.99,
		},
		{
			name:        "nested data string paid status with numeric money is paid",
			body:        `{"code":"1","data":{"status":"1","money":9.99,"trade_no":" data-str-1 "}}`,
			wantStatus:  payment.ProviderStatusPaid,
			wantTradeNo: "data-str-1",
			wantAmount:  9.99,
		},
		{
			name:        "nested data string unpaid status is pending",
			body:        `{"code":1,"data":{"status":"0","money":"9.99","trade_no":"data-str-0"}}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: "data-str-0",
			wantAmount:  9.99,
		},
		{
			name:      "string code zero fails",
			wantError: true,
			body:      `{"code":"0","status":"1","money":"12.34"}`,
		},
		{
			name:      "non numeric string status fails closed",
			wantError: true,
			body:      `{"code":1,"status":"paid","money":"12.34"}`,
		},
		{
			name:      "empty string status fails closed",
			wantError: true,
			body:      `{"code":1,"status":"","money":"12.34"}`,
		},
		{
			name:      "fractional string status fails closed",
			wantError: true,
			body:      `{"code":1,"status":"1.5","money":"12.34"}`,
		},
		{
			name:      "fractional numeric status fails closed",
			wantError: true,
			body:      `{"code":1,"status":1.5,"money":"12.34"}`,
		},
		{
			name:      "boolean status fails closed",
			wantError: true,
			body:      `{"code":1,"status":true,"money":"12.34"}`,
		},
		{
			name:      "nested invalid string status fails closed",
			wantError: true,
			body:      `{"code":1,"data":{"status":"abc","money":"12.34"}}`,
		},
		{
			name:      "non numeric string code fails closed",
			wantError: true,
			body:      `{"code":"ok","status":"1","money":"12.34"}`,
		},
		{
			name:      "plus prefixed string status fails closed",
			wantError: true,
			body:      `{"code":1,"status":"+1","money":"12.34"}`,
		},
		{
			name:      "leading zero string status fails closed",
			wantError: true,
			body:      `{"code":1,"status":"01","money":"12.34"}`,
		},
		{
			name:      "unicode whitespace string status fails closed",
			wantError: true,
			body:      "{\"code\":1,\"status\":\"\u00a01\",\"money\":\"12.34\"}",
		},
		{
			name:      "boolean money fails closed",
			wantError: true,
			body:      `{"code":1,"status":"1","money":true}`,
		},
		{
			name:        "query failure with missing status is an error",
			wantError:   true,
			body:        `{"code":0,"msg":"订单不存在"}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: "",
		},
		{
			name:        "missing fields are an error",
			wantError:   true,
			body:        `{}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotForm url.Values
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Documented as a GET with query-string parameters; a POST was
				// silently ignored upstream and broke every paid callback.
				if r.Method != http.MethodGet {
					t.Errorf("method = %q, want %q", r.Method, http.MethodGet)
				}
				if r.URL.Path != "/api.php" {
					t.Errorf("path = %q, want /api.php", r.URL.Path)
				}
				if err := r.ParseForm(); err != nil {
					t.Errorf("ParseForm: %v", err)
				}
				gotForm = make(url.Values, len(r.URL.Query()))
				for key, values := range r.URL.Query() {
					gotForm[key] = append([]string(nil), values...)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			provider := newTestEasyPay(t, server.URL)
			resp, err := provider.QueryOrder(context.Background(), orderID)
			if tt.wantError {
				if err == nil {
					t.Fatal("expected failed query to return an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("QueryOrder returned error: %v", err)
			}
			if resp.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q (response=%+v)", resp.Status, tt.wantStatus, resp)
			}
			if resp.TradeNo != tt.wantTradeNo {
				t.Fatalf("trade_no = %q, want %q", resp.TradeNo, tt.wantTradeNo)
			}
			if resp.Amount != tt.wantAmount {
				t.Fatalf("amount = %v, want %v", resp.Amount, tt.wantAmount)
			}
			for key, want := range map[string]string{
				"act":          "order",
				"pid":          "pid-1",
				"key":          "pkey-1",
				"out_trade_no": orderID,
			} {
				if got := gotForm.Get(key); got != want {
					t.Fatalf("form[%s] = %q, want %q (form=%v)", key, got, want, gotForm)
				}
			}
		})
	}
}

func TestEasyPayCreateAPIPaymentStringCode(t *testing.T) {
	t.Parallel()

	t.Run("string code 1 succeeds", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":"1","trade_no":"tr-1","payurl":"https://pay.example.com/p/1"}`))
		}))
		defer server.Close()

		p := newTestEasyPay(t, server.URL)
		resp, err := p.CreatePayment(context.Background(), payment.CreatePaymentRequest{
			OrderID:     "order-str-code-1",
			Amount:      "10.00",
			PaymentType: payment.TypeAlipay,
			Subject:     "test",
		})
		if err != nil {
			t.Fatalf("CreatePayment returned error: %v", err)
		}
		if resp.TradeNo != "tr-1" || resp.PayURL != "https://pay.example.com/p/1" {
			t.Fatalf("unexpected response: %+v", resp)
		}
	})

	t.Run("string code 0 surfaces gateway msg", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":"0","msg":"merchant disabled"}`))
		}))
		defer server.Close()

		p := newTestEasyPay(t, server.URL)
		_, err := p.CreatePayment(context.Background(), payment.CreatePaymentRequest{
			OrderID:     "order-str-code-0",
			Amount:      "10.00",
			PaymentType: payment.TypeAlipay,
			Subject:     "test",
		})
		if err == nil || err.Error() != "easypay error: merchant disabled" {
			t.Fatalf("err = %v, want easypay error: merchant disabled", err)
		}
	})

	t.Run("non numeric string code fails closed", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":"ok","trade_no":"tr-1"}`))
		}))
		defer server.Close()

		p := newTestEasyPay(t, server.URL)
		if _, err := p.CreatePayment(context.Background(), payment.CreatePaymentRequest{
			OrderID:     "order-str-code-bad",
			Amount:      "10.00",
			PaymentType: payment.TypeAlipay,
			Subject:     "test",
		}); err == nil {
			t.Fatal("expected non-numeric string code to return parse error")
		}
	})
}
