package service

import (
	"context"
	"fmt"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/pkg/servertiming"
	"github.com/shopspring/decimal"
)

// A valid EasyPay MD5 signature is not by itself proof of settlement: checkout
// requests and notifications share the same signing scheme. Query the order's
// original provider using our persisted order number before any paid/fulfillment
// mutation. Never query by a transaction identifier supplied in the callback.
func (s *PaymentService) verifyEasyPaySettlement(ctx context.Context, order *dbent.PaymentOrder, callbackTradeNo string, callbackAmount float64) error {
	if strings.TrimSpace(callbackTradeNo) == "" {
		return fmt.Errorf("easypay verification: missing callback trade_no")
	}
	if order == nil || strings.TrimSpace(order.OutTradeNo) == "" {
		return fmt.Errorf("easypay verification: missing original out_trade_no")
	}
	if !isValidProviderAmount(order.PayAmount) || !isValidProviderAmount(callbackAmount) ||
		!decimal.NewFromFloat(callbackAmount).Equal(decimal.NewFromFloat(order.PayAmount)) {
		return fmt.Errorf("easypay verification: callback amount mismatch")
	}
	if !psHasPinnedProviderInstance(order) {
		return fmt.Errorf("easypay verification: original provider is not pinned")
	}
	prov, err := s.getPinnedOrderProvider(ctx, order)
	if err != nil || prov == nil || prov.ProviderKey() != payment.TypeEasyPay {
		// Do not return provider config or transport errors: they can contain a
		// merchant secret embedded in a URL. The audit reason is intentionally fixed.
		return fmt.Errorf("easypay verification: original provider unavailable")
	}
	finish := servertiming.ObserveDependency(ctx, "payment")
	upstream, err := prov.QueryOrder(ctx, order.OutTradeNo)
	finish()
	if err != nil {
		return fmt.Errorf("easypay verification: upstream query failed")
	}
	if upstream == nil || upstream.Status != payment.ProviderStatusPaid {
		return fmt.Errorf("easypay verification: upstream order is not paid")
	}
	if !isValidProviderAmount(upstream.Amount) ||
		!decimal.NewFromFloat(upstream.Amount).Equal(decimal.NewFromFloat(order.PayAmount)) {
		return fmt.Errorf("easypay verification: upstream amount mismatch")
	}
	if tradeNo := strings.TrimSpace(upstream.TradeNo); tradeNo != "" && tradeNo != strings.TrimSpace(callbackTradeNo) {
		return fmt.Errorf("easypay verification: upstream trade_no mismatch")
	}
	if err := validateProviderNotificationMetadata(order, payment.TypeEasyPay, upstream.Metadata); err != nil {
		return fmt.Errorf("easypay verification: upstream merchant mismatch")
	}
	return nil
}
