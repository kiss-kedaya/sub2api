package cryptopay

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/ledger"
	"github.com/Wei-Shaw/sub2api/internal/notify"
	"github.com/Wei-Shaw/sub2api/internal/notify/bark"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
)

// DepositCreditor turns an observed deposit into a balance top-up by
// creating a PAID order and reusing the existing fulfillment pipeline.
// The deterministic order identity and existing fulfillment lease make
// interrupted and concurrent attempts safe to resume.
type DepositCreditor struct {
	entClient *dbent.Client
	db        *sql.DB
	fulfill   func(context.Context, int64) error
	// bark is optional: deployments without user notifications leave it nil
	// and every send is a no-op (the client is nil-safe).
	bark *notify.UserBarkService
}

func NewDepositCreditor(entClient *dbent.Client, payments *service.PaymentService, db *sql.DB) *DepositCreditor {
	return &DepositCreditor{entClient: entClient, db: db, fulfill: payments.ExecuteBalanceFulfillment}
}

// SetBarkNotifier wires the per-user push channel used when a deposit is
// credited. Optional; safe to skip in tests and minimal deployments.
func (c *DepositCreditor) SetBarkNotifier(svc *notify.UserBarkService) {
	c.bark = svc
}

// CreditDeposit creates the order and runs fulfillment.
func (c *DepositCreditor) CreditDeposit(ctx context.Context, req ledger.CreditRequest) error {
	if req.CreditUSDT <= 0 || math.IsNaN(req.CreditUSDT) || math.IsInf(req.CreditUSDT, 0) || req.Amount == nil || req.Amount.Sign() <= 0 || req.Decimals < 0 || req.Decimals > 18 {
		return fmt.Errorf("cryptopay credit: non-positive amount")
	}
	// recharge_code is reused as a redeem code, and redeem_codes.code caps at
	// 32 characters — the raw 77-char "crypto-0x…-logIndex" form made every
	// fulfillment fail. A deterministic 24-hex-char digest of the deposit
	// identity keeps it stable across retries, unique with overwhelming
	// probability, and comfortably inside the limit.
	rechargeCode, outTradeNo := depositOrderIdentity(req.Chain, req.TxHash, req.LogIndex)

	// out_trade_no caps at 64 characters and is unique per order. A single
	// transaction can carry several Transfer logs (batch payouts), so the
	// bare tx hash would make every log after the first collide. Use the
	// same deterministic identity as recharge_code (40 hex chars) and keep
	// the canonical tx hash in payment_trade_no/snapshot for explorer links.
	order, err := c.entClient.PaymentOrder.Query().Where(paymentorder.OutTradeNoEQ(outTradeNo)).Only(ctx)
	if err == nil {
		return c.resume(ctx, req, order, rechargeCode)
	}
	if !dbent.IsNotFound(err) {
		return fmt.Errorf("cryptopay credit: find order: %w", err)
	}

	// user_email/user_name are required columns (redundant copies so order
	// history survives user deletion); without them every insert fails and
	// the deposit loops in "pending" forever.
	user, err := c.entClient.User.Get(ctx, req.UserID)
	if err != nil {
		return fmt.Errorf("cryptopay credit: load user: %w", err)
	}
	paidAt := time.Now()

	// Match the existing balance-order precision once, before persisting.
	// The stored order, rather than a later multiplier, is authoritative on retry.
	creditAmount := decimal.NewFromFloat(req.CreditUSDT).Round(3).InexactFloat64()
	if creditAmount <= 0 {
		return fmt.Errorf("cryptopay credit: amount below balance precision")
	}
	paidToken := decimal.NewFromBigInt(req.Amount, -int32(req.Decimals))
	order, err = c.entClient.PaymentOrder.Create().
		SetUserID(req.UserID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(creditAmount).
		SetPayAmount(paidToken.Round(3).InexactFloat64()).
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
		SetProviderSnapshot(map[string]any{
			"currency":   "USD",
			"raw_amount": req.Amount.String(),
			"decimals":   req.Decimals,
			"log_index":  req.LogIndex,
			"chain":      req.Chain,
			"token":      req.Token,
			"from":       req.FromAddr,
			"to":         req.ToAddr,
			"tx":         req.TxHash,
			"block":      req.BlockNum,
		}).
		Save(ctx)
	if err != nil {
		if !dbent.IsConstraintError(err) {
			return fmt.Errorf("cryptopay credit: create order: %w", err)
		}
		// Another scanner may have created the same order concurrently. Load
		// that exact identity; never treat an arbitrary constraint as success.
		order, err = c.entClient.PaymentOrder.Query().Where(paymentorder.OutTradeNoEQ(outTradeNo)).Only(ctx)
		if err != nil {
			return fmt.Errorf("cryptopay credit: reload concurrent order: %w", err)
		}
	}
	return c.resume(ctx, req, order, rechargeCode)
}

func depositOrderIdentity(chain, txHash string, logIndex int) (rechargeCode, outTradeNo string) {
	return ledger.OrderIdentity(chain, txHash, logIndex)
}

func (c *DepositCreditor) resume(ctx context.Context, req ledger.CreditRequest, order *dbent.PaymentOrder, rechargeCode string) error {
	if order.UserID != req.UserID || order.PaymentType != payment.TypeCrypto || order.OrderType != payment.OrderTypeBalance ||
		order.RechargeCode != rechargeCode || !strings.EqualFold(order.PaymentTradeNo, req.TxHash) ||
		order.Amount <= 0 || math.IsNaN(order.Amount) || math.IsInf(order.Amount, 0) {
		return fmt.Errorf("cryptopay credit: existing order identity mismatch")
	}
	for key, want := range map[string]string{"chain": req.Chain, "token": req.Token, "from": req.FromAddr, "to": req.ToAddr, "tx": req.TxHash} {
		got, ok := order.ProviderSnapshot[key].(string)
		if !ok || !strings.EqualFold(got, want) {
			return fmt.Errorf("cryptopay credit: existing order %s mismatch", key)
		}
	}
	if raw, exists := order.ProviderSnapshot["raw_amount"]; exists && raw != req.Amount.String() {
		return fmt.Errorf("cryptopay credit: existing order amount mismatch")
	}
	if c.db == nil {
		return fmt.Errorf("cryptopay credit: ledger database unavailable")
	}
	linked, err := c.db.ExecContext(ctx, `UPDATE deposit_transactions SET order_id = $1
		WHERE chain = $2 AND tx_hash = $3 AND log_index = $4
		  AND (order_id IS NULL OR order_id = $1) AND status IN ('pending', 'credited')`,
		order.ID, req.Chain, req.TxHash, req.LogIndex)
	if err != nil {
		return fmt.Errorf("cryptopay credit: link order: %w", err)
	}
	n, err := linked.RowsAffected()
	if err != nil || n != 1 {
		return fmt.Errorf("cryptopay credit: deposit is missing, resolved or linked to another order")
	}
	if order.Status == payment.OrderStatusCompleted {
		return nil
	}
	if c.fulfill == nil {
		return fmt.Errorf("cryptopay credit: fulfillment unavailable")
	}
	if err := c.fulfill(ctx, order.ID); err != nil {
		return fmt.Errorf("cryptopay credit: fulfillment: %w", err)
	}
	completed, err := c.entClient.PaymentOrder.Get(ctx, order.ID)
	if err != nil {
		return fmt.Errorf("cryptopay credit: verify completion: %w", err)
	}
	if completed.Status != payment.OrderStatusCompleted {
		return fmt.Errorf("cryptopay credit: order has not completed")
	}
	// Notify after the balance landed: a push that arrives before the credit
	// would send users looking at a balance that has not changed yet.
	if c.bark != nil {
		if err := c.bark.SendToUser(ctx, req.UserID, bark.Notification{
			Title: "充值到账",
			Body:  fmt.Sprintf("余额 +%.3f（%s）", order.Amount, req.TxHash),
			Group: "deposit",
		}); err != nil {
			// A failed push must never fail the credit that already landed.
			slog.Warn("[cryptopay] deposit push failed", "user", req.UserID, "error", err)
		}
	}
	return nil
}
