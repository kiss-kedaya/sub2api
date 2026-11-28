//go:build unit

package service

import (
	"context"
	"net/url"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

type fixedSelectionLoadBalancer struct {
	sel *payment.InstanceSelection
}

func (lb *fixedSelectionLoadBalancer) SelectInstance(context.Context, string, payment.PaymentType, payment.Strategy, float64) (*payment.InstanceSelection, error) {
	return lb.sel, nil
}

func (lb *fixedSelectionLoadBalancer) GetInstanceConfig(context.Context, int64) (map[string]string, error) {
	if lb.sel == nil {
		return nil, nil
	}
	return lb.sel.Config, nil
}

func TestPaymentServiceCreateOrder_EndToEndReturnURLAndAlipayForceQRCode(t *testing.T) {
	t.Setenv("PAYMENT_RESUME_SIGNING_KEY", "0123456789abcdef0123456789abcdef")

	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)

	user, err := client.User.Create().
		SetEmail("e2e-pay@example.com").
		SetPasswordHash("hash").
		SetUsername("e2e-pay-user").
		SetStatus(payment.EntityStatusActive).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeEasyPay).
		SetName("EasyPay E2E").
		SetConfig("{}").
		SetSupportedTypes("alipay").
		SetEnabled(true).
		SetSortOrder(1).
		Save(ctx)
	require.NoError(t, err)

	userRepo := &mockUserRepo{
		getByIDUser: &User{
			ID:       user.ID,
			Email:    user.Email,
			Username: user.Username,
			Status:   payment.EntityStatusActive,
		},
	}

	easyPaySel := &payment.InstanceSelection{
		InstanceID:  "inst-easypay-1",
		ProviderKey: payment.TypeEasyPay,
		PaymentMode: "popup",
		Config: map[string]string{
			"pid":         "merchant-1",
			"pkey":        "test-easypay-secret-value",
			"apiBase":     "https://easypay.example.com",
			"notifyUrl":   "https://shop.example.com/api/v1/payment/webhook/easypay",
			"returnUrl":   "https://provider-fallback.example.com/payment/result",
			"paymentMode": "popup",
		},
	}

	t.Run("17777 checkout canonicalizes return_url to DB frontend_url and enforces AlipayForceQRCode", func(t *testing.T) {
		settingRepo := &paymentConfigSettingRepoStub{
			values: map[string]string{
				SettingPaymentEnabled:      "true",
				SettingAlipayForceQRCode:   "true",
				SettingKeyFrontendURL:      "https://shop.example.com",
				SettingKeyAPIBaseURL:       "https://shop.example.com/v1",
				SettingOrderTimeoutMinutes: "30",
				SettingMaxPendingOrders:    "10",
			},
		}
		configSvc := NewPaymentConfigService(client, settingRepo, []byte("0123456789abcdef0123456789abcdef"))
		svc := NewPaymentService(client, payment.NewRegistry(), &fixedSelectionLoadBalancer{sel: easyPaySel}, nil, nil, configSvc, userRepo, nil, nil)
		svc.SetConfig(&config.Config{})

		resp, createErr := svc.CreateOrder(ctx, CreateOrderRequest{
			UserID:      user.ID,
			Amount:      20,
			PaymentType: payment.TypeAlipay,
			OrderType:   payment.OrderTypeBalance,
			ClientIP:    "198.51.100.7",
			IsMobile:    true,
			SrcScheme:   "http",
			SrcHost:     "203.0.113.10", // simulates Nginx proxy_set_header Host $host stripping :17777
			SrcURL:      "http://203.0.113.10:17777/purchase",
			ReturnURL:   "http://203.0.113.10:17777/payment/result",
		})
		require.NoError(t, createErr)
		require.NotNil(t, resp)
		require.NotEmpty(t, resp.PayURL)
		require.NotEmpty(t, resp.ResumeToken)

		payURL, parseErr := url.Parse(resp.PayURL)
		require.NoError(t, parseErr)
		require.Equal(t, "", payURL.Query().Get("device"), "AlipayForceQRCode must prevent mobile device=mobile flag")

		signedReturnURL, parseErr := url.Parse(payURL.Query().Get("return_url"))
		require.NoError(t, parseErr)
		require.Equal(t, "https", signedReturnURL.Scheme)
		require.Equal(t, "shop.example.com", signedReturnURL.Host)
		require.Equal(t, "/payment/result", signedReturnURL.Path)
		require.Equal(t, resp.OutTradeNo, signedReturnURL.Query().Get("out_trade_no"))
		require.Equal(t, resp.ResumeToken, signedReturnURL.Query().Get("resume_token"))
		require.Equal(t, "success", signedReturnURL.Query().Get("status"))
	})

	t.Run("forged Referer without authoritative origin rejects return_url before creating order", func(t *testing.T) {
		beforeCount, countErr := client.PaymentOrder.Query().Count(ctx)
		require.NoError(t, countErr)

		noOriginSel := &payment.InstanceSelection{
			InstanceID:  "inst-no-return-url",
			ProviderKey: payment.TypeEasyPay,
			PaymentMode: "popup",
			Config: map[string]string{
				"pid":       "merchant-1",
				"pkey":      "test-easypay-secret-value",
				"apiBase":   "https://easypay.example.com",
				"notifyUrl": "https://shop.example.com/api/v1/payment/webhook/easypay",
				// No returnUrl here and no frontend_url/api_base_url in settings/config.
			},
		}
		settingRepo := &paymentConfigSettingRepoStub{
			values: map[string]string{
				SettingPaymentEnabled:      "true",
				SettingOrderTimeoutMinutes: "30",
				SettingMaxPendingOrders:    "10",
			},
		}
		configSvc := NewPaymentConfigService(client, settingRepo, []byte("0123456789abcdef0123456789abcdef"))
		svc := NewPaymentService(client, payment.NewRegistry(), &fixedSelectionLoadBalancer{sel: noOriginSel}, nil, nil, configSvc, userRepo, nil, nil)
		svc.SetConfig(&config.Config{
			CORS: config.CORSConfig{AllowedOrigins: []string{"https://evil.example"}},
		})

		_, createErr := svc.CreateOrder(ctx, CreateOrderRequest{
			UserID:      user.ID,
			Amount:      20,
			PaymentType: payment.TypeAlipay,
			OrderType:   payment.OrderTypeBalance,
			ClientIP:    "198.51.100.7",
			SrcScheme:   "https",
			SrcHost:     "shop.example.com",
			SrcURL:      "https://evil.example/purchase",
			ReturnURL:   "https://evil.example/payment/result",
		})
		require.Error(t, createErr)
		require.Contains(t, createErr.Error(), "current site origin")

		// HTTPS -> HTTP downgrade without authoritative origin must also fail before DB insert.
		_, downgradeErr := svc.CreateOrder(ctx, CreateOrderRequest{
			UserID:      user.ID,
			Amount:      20,
			PaymentType: payment.TypeAlipay,
			OrderType:   payment.OrderTypeBalance,
			ClientIP:    "198.51.100.7",
			SrcScheme:   "https",
			SrcHost:     "shop.example.com",
			ReturnURL:   "http://shop.example.com/payment/result",
		})
		require.Error(t, downgradeErr)
		require.Contains(t, downgradeErr.Error(), "current site origin")

		afterCount, countErr := client.PaymentOrder.Query().Count(ctx)
		require.NoError(t, countErr)
		require.Equal(t, beforeCount, afterCount, "rejected return_url must not persist any PaymentOrder rows")
	})
}
