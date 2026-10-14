package cryptopay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/ledger"
	"github.com/Wei-Shaw/sub2api/internal/notify"
	"github.com/Wei-Shaw/sub2api/internal/notify/bark"
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
	// bark is optional: deployments without user notifications leave it nil
	// and every send is a no-op (the client is nil-safe).
	bark *notify.UserBarkService
}

func NewDepositCreditor(entClient *dbent.Client, payments *service.PaymentService) *DepositCreditor {
	return &DepositCreditor{entClient: entClient, payments: payments}
}

// SetBarkNotifier wires the per-user push channel used when a deposit is
// credited. Optional; safe to skip in tests and minimal deployments.
func (c *DepositCreditor) SetBarkNotifier(svc *notify.UserBarkService) {
	c.bark = svc
}

// CreditDeposit creates the order and runs fulfillment.
func (c *DepositCreditor) CreditDeposit(ctx context.Context, req ledger.CreditRequest) error {
	if req.CreditUSDT <= 0 {
		return fmt.Errorf("cryptopay credit: non-positive amount")
	}
	// recharge_code is reused as a redeem code, and redeem_codes.code caps at
	// 32 characters — the raw 77-char "crypto-0x…-logIndex" form made every
	// fulfillment fail. A deterministic 24-hex-char digest of the deposit
	// identity keeps it stable across retries, unique with overwhelming
	// probability, and comfortably inside the limit.
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", req.Chain, req.TxHash, req.LogIndex)))
	rechargeCode := "crypto-" + hex.EncodeToString(digest[:12])

	// out_trade_no caps at 64 characters; a 0x-prefixed hash is 66. Store the
	// bare hash (exactly 64) and keep the canonical 0x form in the snapshot.
	outTradeNo := strings.TrimPrefix(req.TxHash, "0x")

	// user_email/user_name are required columns (redundant copies so order
	// history survives user deletion); without them every insert fails and
	// the deposit loops in "pending" forever.
	user, err := c.entClient.User.Get(ctx, req.UserID)
	if err != nil {
		return fmt.Errorf("cryptopay credit: load user: %w", err)
	}
	paidAt := time.Now()

	order, err := c.entClient.PaymentOrder.Create().
		SetUserID(req.UserID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(req.CreditUSDT).
		SetPayAmount(req.CreditUSDT).
		SetFeeRate(0).
		SetRechargeCode(rechargeCode).
		SetOutTradeNo(outTradeNo).
		SetPaymentType(payment.TypeCrypto).
		SetPaymentTradeNo(req.TxHash).
		SetOrderType(payment.OrderTypeBalance).
		SetProviderKey(payment.TypeCrypto).
		SetStatus(payment.OrderStatusPaid).
		SetPaidAt(paidAt).
		// expires_at is required; the deposit already happened, so the
		// expiry is moot — anchor it at the paid time.
		SetExpiresAt(paidAt).
		// client_ip / src_host are required origin fields. A chain deposit
		// has no HTTP client, so mark the source as the crypto watcher.
		SetClientIP("crypto").
		SetSrcHost("crypto").
		// client_ip / src_host are required origin fields. A chain deposit
		// has no HTTP client, so mark the source as the crypto watcher.
		SetClientIP("crypto").
		SetSrcHost("crypto").
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
	// Notify after the balance landed: a push that arrives before the credit
	// would send users looking at a balance that has not changed yet.
	if c.bark != nil {
		if err := c.bark.SendToUser(ctx, req.UserID, bark.Notification{
			Title: "充值到账",
			Body:  fmt.Sprintf("余额 +%.2f（%s）", req.CreditUSDT, req.TxHash),
			Group: "deposit",
		}); err != nil {
			// A failed push must never fail the credit that already landed.
			slog.Warn("[cryptopay] deposit push failed", "user", req.UserID, "error", err)
		}
	}
	return nil
}

// orderExists guards against re-creating an order for a deposit that was
// already credited (defense in depth on top of the ledger gate).
func (c *DepositCreditor) orderExists(ctx context.Context, txHash string) (bool, error) {
	count, err := c.entClient.PaymentOrder.Query().
		Where(paymentorder.OutTradeNoEQ(strings.TrimPrefix(txHash, "0x"))).
		Where(paymentorder.PaymentTypeEQ(payment.TypeCrypto)).
		Count(ctx)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
