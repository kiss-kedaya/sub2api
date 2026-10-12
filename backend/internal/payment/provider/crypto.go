package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/payment"
)

// Crypto is the built-in on-chain deposit channel. Unlike external
// providers it has no upstream API: creating a "payment" means handing the
// user their per-user deposit address, and settlement arrives through the
// scanner + ledger pipeline (internal/cryptopay) rather than a webhook.
//
// The provider exists so the channel appears in the standard payment
// method list and checkout flow; the actual deposit machinery lives in
// cryptopay and is reached through CreatePayment's DepositAddress field.
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

// CreatePayment returns the user's deposit address as the payment target.
// The cryptopay HTTP layer resolves the address and passes it through
// CreatePaymentRequest.DepositAddress; when absent the channel reports a
// configuration error instead of fabricating an address.
func (c *Crypto) CreatePayment(_ context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	address := strings.TrimSpace(req.DepositAddress)
	if address == "" {
		return nil, fmt.Errorf("crypto create payment: deposit address unavailable")
	}
	return &payment.CreatePaymentResponse{
		TradeNo:    strings.TrimSpace(req.OrderID),
		PayURL:     "",
		Currency:   c.config["currency"],
		ResultType: payment.CreatePaymentResultOrderCreated,
		QRCode:     address,
	}, nil
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
