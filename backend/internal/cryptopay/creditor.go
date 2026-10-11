package cryptopay

import (
	"context"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/ledger"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// DepositCreditor turns an observed deposit into a balance top-up by
// creating a PAID order and reusing the existing fulfillment pipeline.
// Idempotency comes from the ledger's unique (chain, tx_hash, log_index)
// gate; this layer only runs once per deposit.
type DepositCreditor struct {
	entClient *dbent.Client
	payments  *service.PaymentService
}

func NewDepositCreditor(entClient *dbent.Client, payments *service.PaymentService) *DepositCreditor {
	return &DepositCreditor{entClient: entClient, payments: payments}
}

// CreditDeposit creates the order and runs fulfillment.
func (c *DepositCreditor) CreditDeposit(ctx context.Context, req ledger.CreditRequest) error {
	if req.CreditUSDT <= 0 {
		return fmt.Errorf("cryptopay credit: non-positive amount")
	}
	rechargeCode := fmt.Sprintf("crypto-%s-%d", req.TxHash, req.LogIndex)

	order, err := c.entClient.PaymentOrder.Create().
		SetUserID(req.UserID).
		SetAmount(req.CreditUSDT).
		SetPayAmount(req.CreditUSDT).
		SetFeeRate(0).
		SetRechargeCode(rechargeCode).
		SetOutTradeNo(req.TxHash).
		SetPaymentType(payment.TypeCrypto).
		SetPaymentTradeNo(req.TxHash).
		SetOrderType(payment.OrderTypeBalance).
		SetProviderKey(payment.TypeCrypto).
		SetStatus(payment.OrderStatusPaid).
		SetPaidAt(time.Now()).
		SetProviderSnapshot(map[string]any{
			"chain":  req.Chain,
			"token":  req.Token,
			"from":   req.FromAddr,
			"to":     req.ToAddr,
			"tx":     req.TxHash,
			"block":  req.BlockNum,
		}).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("cryptopay credit: create order: %w", err)
	}
	if err := c.payments.ExecuteBalanceFulfillment(ctx, order.ID); err != nil {
		return fmt.Errorf("cryptopay credit: fulfillment: %w", err)
	}
	return nil
}

// orderExists guards against re-creating an order for a deposit that was
// already credited (defense in depth on top of the ledger gate).
func (c *DepositCreditor) orderExists(ctx context.Context, txHash string) (bool, error) {
	count, err := c.entClient.PaymentOrder.Query().
		Where(paymentorder.OutTradeNoEQ(txHash)).
		Where(paymentorder.PaymentTypeEQ(payment.TypeCrypto)).
		Count(ctx)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
