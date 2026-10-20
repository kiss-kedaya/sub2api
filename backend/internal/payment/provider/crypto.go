package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

// Crypto identifies existing balance orders created by the standalone
// deposit scanner/ledger pipeline. It cannot create checkout payments:
// address deposits have no association with checkout or subscription orders.
type Crypto struct {
	instanceID string
	config     map[string]string
}

// NewCrypto validates config. Only display-relevant keys are accepted;
// scanning/sweep settings live in the cryptopay settings namespace.
func NewCrypto(instanceID string, config map[string]string) (*Crypto, error) {
	cfg := cloneStringMap(config)
	currency := strings.ToUpper(strings.TrimSpace(cfg["currency"]))
	if currency == "" {
		currency = "USD"
	}
	if _, err := payment.NormalizePaymentCurrency(currency); err != nil {
		return nil, fmt.Errorf("crypto config currency: %w", err)
	}
	cfg["currency"] = currency
	return &Crypto{instanceID: instanceID, config: cfg}, nil
}

func (c *Crypto) Name() string { return "Crypto" }

func (c *Crypto) ProviderKey() string { return payment.TypeCrypto }

func (c *Crypto) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeCrypto}
}

func (c *Crypto) MerchantIdentityMetadata() map[string]string {
	return map[string]string{"currency": c.config["currency"]}
}

// CreatePayment refuses orders that the deposit scanner cannot settle.
func (c *Crypto) CreatePayment(_ context.Context, _ payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	return nil, fmt.Errorf("crypto deposits use the standalone deposit page; checkout orders are unsupported")
}

// QueryOrder reports pending: crypto orders settle through the scanner,
// never through a pull query.
func (c *Crypto) QueryOrder(_ context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	return &payment.QueryOrderResponse{TradeNo: tradeNo, Status: payment.ProviderStatusPending}, nil
}

// VerifyNotification rejects callbacks: the crypto channel is settled
// internally by the ledger, so any inbound webhook claiming to be one is
// either a mistake or an attack.
func (c *Crypto) VerifyNotification(_ context.Context, _ string, _ map[string]string) (*payment.PaymentNotification, error) {
	return nil, fmt.Errorf("crypto channel does not accept webhooks")
}

// Refund is unsupported: on-chain payments cannot be pulled back.
func (c *Crypto) Refund(_ context.Context, _ payment.RefundRequest) (*payment.RefundResponse, error) {
	return nil, fmt.Errorf("crypto channel does not support refunds")
}
