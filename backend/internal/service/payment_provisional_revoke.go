package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

// ProvisionalDepositRevocation identifies one instant (0-confirmation) crypto
// deposit credit that the chain invalidated and that must be clawed back.
type ProvisionalDepositRevocation struct {
	Chain        string
	TxHash       string
	LogIndex     int
	RevokeReason string
}

// ProvisionalRevocationResult reports what a revocation attempt did. Revoked is
// true when this call owns the claw-back (or the identity was already revoked);
// false means nothing was owed back and the legacy manual path must handle it.
type ProvisionalRevocationResult struct {
	Revoked bool
	UserID  int64
	// Amount is the credited amount that was deducted (the frozen payment-order
	// amount, i.e. exactly what the fulfillment pipeline added to the balance).
	Amount float64
	// BalanceAfter is the user's balance after the deduction; BalanceAfterKnown
	// is false when the user row no longer exists (deleted account).
	BalanceAfter      float64
	BalanceAfterKnown bool
	// Negative records that the claw-back pushed the balance below zero. The
	// deposit honestly cannot be recovered from a user who already spent it;
	// the deficit is recorded instead of retried forever.
	Negative bool
	// LedgerID is the redeem_codes row id of the negative, user-visible balance
	// ledger entry written in the same transaction (0 when nothing was deducted,
	// e.g. the account was deleted).
	LedgerID int64
}

// auditDepositProvisionalRevoked is the payment audit action written for every
// automatic claw-back, so the reversal is traceable next to the original
// recharge order.
const auditDepositProvisionalRevoked = "DEPOSIT_PROVISIONAL_REVOKED"

// provisionalRevokeLedgerNotes is the user-visible reason on the balance ledger
// row. The machine reason (reorg_noncanonical_block/...) stays on the deposit
// row and in payment_audit_logs; the ledger only has to be understandable.
const provisionalRevokeLedgerNotes = "链上充值未最终确认已回滚"

// provisionalRevokeSourceRef builds the structured pointer from the ledger row
// back to the chain deposit it reverses. It is indexed for audit lookups, not
// shown to users (the notes field carries the readable sentence).
func provisionalRevokeSourceRef(depositID int64, chain, txHash string, logIndex int) string {
	return fmt.Sprintf("crypto_deposit:%d:%s:%s:%d", depositID, chain, txHash, logIndex)
}

// RevokeProvisionalDepositCredit deducts an instant deposit credit exactly once.
//
// Atomicity: the deposit row is claimed (revoked_at + status) and the balance is
// deducted in the same database transaction, so a crash can never leave a
// permanently blacklisted transaction whose money was not taken back. The
// conditional claim is the idempotency lock: a second/concurrent call affects
// zero rows and is a successful no-op.
//
// The deduction uses balance - amount (no floor): when the user already spent
// the money the balance goes negative and the reason is recorded. After the
// transaction commits, this path also synchronizes the legacy short-lived
// balance cache (which the PG outbox skips for negative deltas under merge
// settlement), evicts the user's API key auth cache, and claws back any
// affiliate invite rebate accrued for the revoked order.
func (s *PaymentService) RevokeProvisionalDepositCredit(ctx context.Context, in ProvisionalDepositRevocation) (*ProvisionalRevocationResult, error) {
	if s == nil || s.entClient == nil {
		return nil, errors.New("provisional revocation: payment service is unavailable")
	}
	txHash := strings.ToLower(strings.TrimSpace(in.TxHash))
	chain := strings.TrimSpace(in.Chain)
	if chain == "" || txHash == "" || in.LogIndex < 0 {
		return nil, errors.New("provisional revocation: invalid deposit identity")
	}
	reason := strings.TrimSpace(in.RevokeReason)
	if reason == "" {
		reason = "reorg"
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("provisional revocation: begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			slog.Warn("[cryptopay] provisional revocation rollback failed", "error", rollbackErr)
		}
	}()
	txCtx := dbent.NewTxContext(ctx, tx)
	client := tx.Client()

	// 1. Claim: only an unrevoked provisional credit is clawed back. The
	//    scanner persists "reverted" before notifying, but accepting a still
	//    credited row makes the exact ordering irrelevant.
	claim, err := client.ExecContext(txCtx, `UPDATE deposit_transactions
		    SET status = 'reverted', revoked_at = CURRENT_TIMESTAMP, revoke_reason = $4
		  WHERE chain = $1 AND tx_hash = $2 AND log_index = $3
		    AND revoked_at IS NULL AND provisional AND status IN ('credited', 'reverted')`,
		chain, txHash, in.LogIndex, reason)
	if err != nil {
		return nil, fmt.Errorf("provisional revocation: claim deposit: %w", err)
	}
	claimed, err := claim.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("provisional revocation: claim result: %w", err)
	}
	if claimed == 0 {
		// Already revoked, not a provisional credit, or no such row: nothing to
		// deduct. Roll back so a no-op never writes anything.
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			return nil, fmt.Errorf("provisional revocation: rollback no-op: %w", err)
		}
		committed = true
		return &ProvisionalRevocationResult{Revoked: false}, nil
	}

	// 2. The frozen payment order is the only trustworthy amount: it is exactly
	//    what fulfillment added to the balance, even if the chain re-priced the
	//    deposit or the multiplier changed afterwards.
	var depositID sql.NullInt64
	var orderID, userID sql.NullInt64
	var amount sql.NullFloat64
	infoRows, err := client.QueryContext(txCtx, `SELECT d.id, d.order_id, o.user_id, o.amount
		  FROM deposit_transactions d
		  LEFT JOIN payment_orders o ON o.id = d.order_id
		 WHERE d.chain = $1 AND d.tx_hash = $2 AND d.log_index = $3`,
		chain, txHash, in.LogIndex)
	if err != nil {
		return nil, fmt.Errorf("provisional revocation: read credited order: %w", err)
	}
	if infoRows.Next() {
		err = infoRows.Scan(&depositID, &orderID, &userID, &amount)
	}
	if scanErr := infoRows.Err(); err == nil {
		err = scanErr
	}
	if closeErr := infoRows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("provisional revocation: read credited order: %w", err)
	}
	if !orderID.Valid || !userID.Valid || !amount.Valid || amount.Float64 <= 0 {
		// The row was claimed but no frozen order exists to explain the amount.
		// Undo the claim and leave the credited row for manual reconciliation:
		// deducting a guessed number is worse than a support ticket.
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			return nil, fmt.Errorf("provisional revocation: rollback unlinked: %w", err)
		}
		committed = true
		slog.Error("[cryptopay] provisional credit has no frozen order; manual reconciliation required",
			"chain", chain, "tx", txHash, "log_index", in.LogIndex)
		return &ProvisionalRevocationResult{Revoked: false}, nil
	}

	// 3. Deduct. Deliberately no floor: the provisional credit is real money we
	//    paid out on a transaction that disappeared, so an already-spent credit
	//    becomes a negative balance (recorded below) instead of being ignored.
	deduct, err := client.ExecContext(txCtx, `UPDATE users
		    SET balance = balance - $1, updated_at = CURRENT_TIMESTAMP
		  WHERE id = $2 AND deleted_at IS NULL`, amount.Float64, userID.Int64)
	if err != nil {
		return nil, fmt.Errorf("provisional revocation: deduct balance: %w", err)
	}
	deducted, err := deduct.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("provisional revocation: deduct result: %w", err)
	}
	result := &ProvisionalRevocationResult{Revoked: true, UserID: userID.Int64, Amount: amount.Float64}
	if deducted > 0 {
		var balance float64
		balanceRows, err := client.QueryContext(txCtx, `SELECT balance FROM users WHERE id = $1`, userID.Int64)
		if err != nil {
			return nil, fmt.Errorf("provisional revocation: read balance: %w", err)
		}
		if balanceRows.Next() {
			err = balanceRows.Scan(&balance)
		}
		if scanErr := balanceRows.Err(); err == nil {
			err = scanErr
		}
		if closeErr := balanceRows.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, fmt.Errorf("provisional revocation: read balance: %w", err)
		}
		result.BalanceAfter, result.BalanceAfterKnown, result.Negative = balance, true, balance < 0
	} else {
		// Deleted user: there is no balance left to claw back, but the
		// revocation stays durable for the audit trail.
		slog.Warn("[cryptopay] provisional credit revoked for a missing user; nothing to deduct",
			"chain", chain, "tx", txHash, "log_index", in.LogIndex, "user_id", userID.Int64)
	}

	// 4. User-visible balance ledger entry. The balance UPDATE above is invisible
	//    in "使用记录 / 余额明细" on its own; without this row the user sees a
	//    recharge but no matching deduction. It reuses redeem_codes (the same
	//    table the user's balance history reads) with a negative value and a
	//    server-only type, so the two sides always reconcile.
	//
	//    This is a synthetic ledger row, not a redeem code: no code/hash/prefix
	//    is written (the row is not redeemable) and source_ref points back to the
	//    chain deposit for audit. Only write it when the deduction actually
	//    happened (deleted users have no balance to explain).
	if deducted > 0 {
		now := time.Now()
		ledger, err := tx.RedeemCode.Create().
			SetType(RedeemTypeCryptoRevoke).
			SetValue(-amount.Float64).
			SetStatus(StatusUsed).
			SetUsedBy(userID.Int64).
			SetUsedAt(now).
			SetNotes(provisionalRevokeLedgerNotes).
			SetSourceRef(provisionalRevokeSourceRef(depositID.Int64, chain, txHash, in.LogIndex)).
			Save(txCtx)
		if err != nil {
			return nil, fmt.Errorf("provisional revocation: write balance ledger: %w", err)
		}
		result.LedgerID = ledger.ID
	}

	// 5. Audit trail next to the original recharge order.
	detail, err := json.Marshal(map[string]any{
		"chain":            chain,
		"tx_hash":          txHash,
		"log_index":        in.LogIndex,
		"user_id":          userID.Int64,
		"amount":           amount.Float64,
		"balance_after":    result.BalanceAfter,
		"balance_known":    result.BalanceAfterKnown,
		"negative_balance": result.Negative,
		"reason":           reason,
	})
	if err != nil {
		return nil, fmt.Errorf("provisional revocation: encode audit detail: %w", err)
	}
	if _, err := tx.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(orderID.Int64, 10)).
		SetAction(auditDepositProvisionalRevoked).
		SetOperator("system").
		SetDetail(string(detail)).
		Save(txCtx); err != nil {
		return nil, fmt.Errorf("provisional revocation: write audit log: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("provisional revocation: commit: %w", err)
	}
	committed = true

	// Post-commit synchronization (best-effort; DB state is authoritative):
	//  1. Sync the legacy short-lived balance cache (billing:balance:{uid}): under
	//     merge settlement the PG outbox skips invalidating legacy cache for
	//     DeltaUnits <= 0, so negative external deductions must adjust it here.
	//  2. Evict API key auth cache so exhausted/negative balances take effect
	//     immediately rather than serving cached balance snapshots.
	//  3. Reverse any affiliate invite rebate accrued when the provisional credit
	//     completed (even if the invitee row was later soft-deleted).
	if deducted > 0 && amount.Float64 > 0 {
		syncCommittedLiveBalanceAdjustment(
			s.provisionalRevokeBillingCache(),
			userID.Int64,
			newLiveBalanceAdjustmentEventID("crypto-provisional-revoke"),
			-amount.Float64,
		)
	}
	if deducted > 0 {
		if invalidator := s.provisionalRevokeAuthCacheInvalidator(); invalidator != nil {
			cacheCtx, cancel := detachPaymentMoneyContext(ctx, liveBalanceAdjustmentTimeout)
			invalidator.InvalidateAuthCacheByUserID(cacheCtx, userID.Int64)
			cancel()
		}
	}
	if s.affiliateService != nil && orderID.Valid && orderID.Int64 > 0 {
		s.reverseAffiliateRebateForProvisionalRevoke(ctx, orderID.Int64, chain, txHash, in.LogIndex)
	}

	return result, nil
}

func (s *PaymentService) provisionalRevokeBillingCache() *BillingCacheService {
	if cache := s.liveBalanceBillingCache(); cache != nil {
		return cache
	}
	if s != nil && s.affiliateService != nil {
		return s.affiliateService.billingCacheService
	}
	return nil
}

func (s *PaymentService) provisionalRevokeAuthCacheInvalidator() APIKeyAuthCacheInvalidator {
	if s == nil {
		return nil
	}
	if s.redeemService != nil && s.redeemService.authCacheInvalidator != nil {
		return s.redeemService.authCacheInvalidator
	}
	if s.affiliateService != nil && s.affiliateService.authCacheInvalidator != nil {
		return s.affiliateService.authCacheInvalidator
	}
	return nil
}

func (s *PaymentService) reverseAffiliateRebateForProvisionalRevoke(ctx context.Context, orderID int64, chain, txHash string, logIndex int) {
	if s == nil || s.affiliateService == nil || orderID <= 0 {
		return
	}
	rebateCtx, cancel := detachPaymentMoneyContext(ctx, paymentMoneyContextTimeout)
	defer cancel()
	reversed, err := s.affiliateService.ReverseInviteRebateForOrder(rebateCtx, orderID, 1.0)
	if err != nil {
		slog.Warn("[cryptopay] provisional revocation: affiliate rebate reversal failed",
			"order_id", orderID, "chain", chain, "tx", txHash, "log_index", logIndex, "error", err)
		s.writeAuditLog(rebateCtx, orderID, "AFFILIATE_REBATE_REVERSAL_FAILED", "system", map[string]any{
			"source": "crypto_provisional_revoke",
			"error":  psErrMsg(err),
			"ratio":  1.0,
		})
		return
	}
	if reversed > 0 {
		s.writeAuditLog(rebateCtx, orderID, "AFFILIATE_REBATE_REVERSED", "system", map[string]any{
			"source": "crypto_provisional_revoke",
			"amount": reversed,
			"ratio":  1.0,
		})
	}
}
