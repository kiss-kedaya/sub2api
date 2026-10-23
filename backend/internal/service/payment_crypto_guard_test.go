package service

import (
	"context"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestCryptoCheckoutRejectedBeforeCreatingUnsettleableOrder(t *testing.T) {
	for _, orderType := range []string{payment.OrderTypeBalance, payment.OrderTypeSubscription} {
		t.Run(orderType, func(t *testing.T) {
			s := &PaymentService{}
			_, err := s.CreateOrder(context.Background(), CreateOrderRequest{
				PaymentType: payment.TypeCrypto, OrderType: orderType, Amount: 10,
			})
			require.ErrorContains(t, err, "链上充值页面")
		})
	}
}

func TestCryptoNotAdvertisedAsCheckoutMethod(t *testing.T) {
	grouped := pcGroupByPaymentType([]*dbent.PaymentProviderInstance{
		{ID: 1, ProviderKey: payment.TypeCrypto, SupportedTypes: "crypto"},
		{ID: 2, ProviderKey: payment.TypeEpusdt, SupportedTypes: "epusdt"},
		{ID: 3, ProviderKey: payment.TypeStripe, SupportedTypes: "card"},
		{ID: 4, ProviderKey: payment.TypeEasyPay, SupportedTypes: "alipay,crypto"},
	})
	require.NotContains(t, grouped, payment.TypeCrypto)
	require.Contains(t, grouped, payment.TypeEpusdt)
	require.Contains(t, grouped, payment.TypeStripe)
	require.Contains(t, grouped, payment.TypeAlipay)
	enabled := applyVisibleMethodRoutingToEnabledTypes([]string{payment.TypeCrypto, payment.TypeEpusdt, payment.TypeStripe}, nil, nil)
	require.Equal(t, []string{payment.TypeEpusdt, payment.TypeStripe}, enabled)
}
