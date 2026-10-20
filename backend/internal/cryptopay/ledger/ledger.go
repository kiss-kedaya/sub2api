// Package ledger turns observed on-chain transfers into credited balances.
//
// Safety rails:
//   - unique (chain, tx_hash, log_index) makes crediting idempotent;
//   - transfers FROM our own addresses (sweeps, gas top-ups) are excluded so
//     the system never credits itself.
package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
)

// Creditor is the fulfillment surface the ledger needs. Implemented by an
// adapter over the existing payment service; tests substitute a fake.
type Creditor interface {
	// CreditDeposit creates a balance order for userID and runs the existing
	// fulfillment pipeline. Must be idempotent on (chain, txHash, logIndex)
	// via the deposit record status.
	CreditDeposit(ctx context.Context, req CreditRequest) error
}

// CreditRequest carries the resolved deposit for fulfillment.
type CreditRequest struct {
	UserID   int64
	Chain    string
	TxHash   string
	LogIndex int
	Token    string
	Amount   *big.Int
	Decimals int
	// CreditUSDT is the decimal amount credited to the balance (after
	// multiplier/fee). Existing linked orders use their frozen amount.
	CreditUSDT float64
	FromAddr   string
	ToAddr     string
	BlockNum   uint64
}

// AddressResolver maps a deposit address back to its owner.
type AddressResolver interface {
	// ResolveDeposit returns (userID, isOurs) for a deposit address.
	// isOurs is true when the address belongs to us even if unassigned
	// (pool). Owned-but-disabled addresses still resolve so funds are
	// recorded for manual handling.
	ResolveDeposit(ctx context.Context, chain, address string) (userID int64, isOurs bool, err error)
	// IsOwnAddress reports whether address is one of our addresses (any
	// status). Used to exclude sweeps and gas top-ups from crediting.
	IsOwnAddress(ctx context.Context, chain, address string) (bool, error)
}

// Ledger persists deposit observations and drives fulfillment.
type Ledger struct {
	db         *sql.DB
	creditor   Creditor
	resolver   AddressResolver
	minAmount  float64
	maxAmount  float64
	multiplier float64
	// onUnmatched reports a transfer that reached one of our addresses but
	// could not be credited (too small, or the address is unassigned).
	// Operators need this: it is usually a mistyped deposit or a probe.
	onUnmatched func(ctx context.Context, t scanner.Transfer, reason string)
}

// Reason codes stored on ignored rows and echoed to the depositor, so a
// rejected transfer never looks like a random failure.
const (
	ReasonBelowMin       = "below_min"
	ReasonAboveMax       = "above_max"
	ReasonUnassigned     = "address_unassigned"
	ReasonUnknownAddress = "address_unknown"
)

// ReasonText renders a stored reason code for the user-facing history.
func ReasonText(code string, minAmount, maxAmount float64) string {
	switch code {
	case ReasonBelowMin:
		return fmt.Sprintf("below the minimum of %g USDT", minAmount)
	case ReasonAboveMax:
		return fmt.Sprintf("above the maximum of %g USDT for one deposit", maxAmount)
	case ReasonUnassigned:
		return "this address is not assigned to a user"
	case ReasonUnknownAddress:
		return "this address is not a deposit address"
	}
	return code
}

func New(db *sql.DB, creditor Creditor, resolver AddressResolver, minAmount, maxAmount, multiplier float64) *Ledger {
	return &Ledger{
		db: db, creditor: creditor, resolver: resolver,
		minAmount: minAmount, maxAmount: maxAmount, multiplier: multiplier,
	}
}

// SetUnmatchedNotifier wires the alert for funds that arrived but were not
// credited. Optional: nil keeps the ledger silent.
func (l *Ledger) SetUnmatchedNotifier(fn func(ctx context.Context, t scanner.Transfer, reason string)) {
	l.onUnmatched = fn
}

// notifyUnmatched fires the alert without letting a notifier failure affect
// the ledger: recording the deposit matters more than the push.
func (l *Ledger) notifyUnmatched(ctx context.Context, t scanner.Transfer, reason string) {
	if l.onUnmatched == nil {
		return
	}
	l.onUnmatched(ctx, t, reason)
}

// Process handles one observed transfer. Returns credited=true when a
// balance was added.
func (l *Ledger) Process(ctx context.Context, t scanner.Transfer, decimals int) (credited bool, err error) {
	if t.Amount == nil || t.Amount.Sign() <= 0 {
		return false, nil
	}
	// Resume an existing order before consulting mutable address status or
	// thresholds. A completed fulfillment can still have a pending deposit
	// row after a failed status write; reclassifying it would hide real funds.
	state, err := l.existingDeposit(ctx, t)
	if err != nil {
		return false, err
	}
	switch state.status {
	case "credited", "ignored", "reverted":
		return false, nil
	case "pending":
		if state.orderID.Valid {
			if !state.userID.Valid || !state.credit.Valid || state.credit.Float64 <= 0 || math.IsNaN(state.credit.Float64) || math.IsInf(state.credit.Float64, 0) {
				return false, fmt.Errorf("linked deposit order is missing or invalid")
			}
			return l.credit(ctx, t, decimals, state.userID.Int64, state.credit.Float64)
		}
	case "": // New observation.
	default:
		return false, fmt.Errorf("unexpected deposit status %q", state.status)
	}
	// Exclude our own sends (sweeps / gas top-ups): a sweep from a child
	// address must never credit anyone.
	own, err := l.resolver.IsOwnAddress(ctx, t.Chain, t.From)
	if err != nil {
		return false, fmt.Errorf("own-address check: %w", err)
	}
	if own {
		return false, nil
	}

	amountDecimal := toDecimal(t.Amount, decimals)
	if amountDecimal < l.minAmount {
		reason := fmt.Sprintf("below the minimum credit amount (%g)", l.minAmount)
		return false, l.ignore(ctx, t, decimals, ReasonBelowMin, reason)
	}
	if l.maxAmount > 0 && amountDecimal > l.maxAmount {
		reason := fmt.Sprintf("above the per-deposit maximum (%g)", l.maxAmount)
		return false, l.ignore(ctx, t, decimals, ReasonAboveMax, reason)
	}

	userID, isOurs, err := l.resolver.ResolveDeposit(ctx, t.Chain, t.To)
	if err != nil {
		return false, fmt.Errorf("resolve deposit: %w", err)
	}
	if !isOurs {
		// Not our address at all: nothing to record.
		return false, nil
	}
	if userID == 0 {
		// Ours but unassigned (pool) or disabled: record for reconciliation.
		return false, l.ignore(ctx, t, decimals, ReasonUnassigned, "address is not assigned to a user")
	}

	creditUSDT := amountDecimal * l.multiplier

	status, err := l.insertPending(ctx, t, decimals)
	if err != nil {
		return false, err
	}
	switch status {
	case "credited", "ignored", "reverted":
		// Already resolved: nothing to do. "pending" falls through so a
		// transaction that was observed before finality gets credited on a
		// later pass instead of being dropped when the cursor advances.
		return false, nil
	case "pending":
	default:
		return false, fmt.Errorf("unexpected deposit status %q", status)
	}

	return l.credit(ctx, t, decimals, userID, creditUSDT)
}

type depositState struct {
	status          string
	orderID, userID sql.NullInt64
	credit          sql.NullFloat64
}

func (l *Ledger) existingDeposit(ctx context.Context, t scanner.Transfer) (depositState, error) {
	var state depositState
	_, outTradeNo := OrderIdentity(t.Chain, t.TxHash, t.LogIndex)
	err := l.db.QueryRowContext(ctx, `SELECT d.status, COALESCE(d.order_id, o.id), o.user_id, o.amount
		FROM deposit_transactions d LEFT JOIN payment_orders o
		  ON o.id = d.order_id OR (d.order_id IS NULL AND o.out_trade_no = $4)
		WHERE d.chain = $1 AND d.tx_hash = $2 AND d.log_index = $3`,
		t.Chain, t.TxHash, t.LogIndex, outTradeNo).Scan(&state.status, &state.orderID, &state.userID, &state.credit)
	if errors.Is(err, sql.ErrNoRows) {
		return depositState{}, nil
	}
	if err != nil {
		return state, fmt.Errorf("read deposit state: %w", err)
	}
	return state, nil
}

func (l *Ledger) credit(ctx context.Context, t scanner.Transfer, decimals int, userID int64, creditUSDT float64) (bool, error) {
	if err := l.creditor.CreditDeposit(ctx, CreditRequest{
		UserID:     userID,
		Chain:      t.Chain,
		TxHash:     t.TxHash,
		LogIndex:   t.LogIndex,
		Token:      t.Token,
		Amount:     t.Amount,
		Decimals:   decimals,
		CreditUSDT: creditUSDT,
		FromAddr:   t.From,
		ToAddr:     t.To,
		BlockNum:   t.BlockNumber,
	}); err != nil {
		return false, fmt.Errorf("credit deposit: %w", err)
	}
	result, err := l.db.ExecContext(ctx,
		`UPDATE deposit_transactions SET status = 'credited'
		 WHERE chain = $1 AND tx_hash = $2 AND log_index = $3 AND status = 'pending'`,
		t.Chain, t.TxHash, t.LogIndex)
	if err != nil {
		return false, fmt.Errorf("mark credited: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("mark credited result: %w", err)
	}
	if affected == 0 {
		var status string
		if err := l.db.QueryRowContext(ctx, `SELECT status FROM deposit_transactions WHERE chain = $1 AND tx_hash = $2 AND log_index = $3`,
			t.Chain, t.TxHash, t.LogIndex).Scan(&status); err != nil {
			return false, fmt.Errorf("read fulfilled deposit: %w", err)
		}
		if status == "credited" {
			return false, nil
		}
		// Fulfillment may already have moved the balance. Keep the reorg or
		// reconciliation state; never overwrite it or automatically reverse.
		return false, fmt.Errorf("fulfilled deposit is %q; manual reconciliation required", status)
	}
	return true, nil
}

// ProcessPending records a transfer that is not yet final so a later pass
// can credit it. The row stays "pending" until Process succeeds; re-running
// this is a no-op thanks to the unique key. decimals must be the token's
// real exponent: the retry pass rebuilds the raw amount from the stored
// decimal, and the old hardcoded 6 inflated 18-decimal deposits by 1e12.
func (l *Ledger) ProcessPending(ctx context.Context, t scanner.Transfer, decimals int) error {
	if t.Amount == nil || t.Amount.Sign() <= 0 {
		return nil
	}
	own, err := l.resolver.IsOwnAddress(ctx, t.Chain, t.From)
	if err != nil {
		return fmt.Errorf("own-address check: %w", err)
	}
	if own {
		return nil
	}
	_, isOurs, err := l.resolver.ResolveDeposit(ctx, t.Chain, t.To)
	if err != nil {
		return fmt.Errorf("resolve deposit: %w", err)
	}
	if !isOurs {
		return nil
	}
	_, err = l.db.ExecContext(ctx,
		`INSERT INTO deposit_transactions
		   (chain, tx_hash, log_index, from_address, to_address, token, amount, block_number, block_hash, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending')
		 ON CONFLICT (chain, tx_hash, log_index) DO NOTHING`,
		t.Chain, t.TxHash, t.LogIndex, t.From, t.To, t.Token,
		amountString(t.Amount, decimals), t.BlockNumber, t.BlockHash)
	if err != nil {
		return fmt.Errorf("record pending: %w", err)
	}
	return nil
}

// insertPending inserts the deposit row; returns the row's status after the
// insert ("pending" when new) so callers can tell a fresh observation from
// a retry of one still waiting for confirmations.
func (l *Ledger) insertPending(ctx context.Context, t scanner.Transfer, decimals int) (string, error) {
	result, err := l.db.ExecContext(ctx,
		`INSERT INTO deposit_transactions
		   (chain, tx_hash, log_index, from_address, to_address, token, amount, block_number, block_hash, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending')
		 ON CONFLICT (chain, tx_hash, log_index) DO NOTHING`,
		t.Chain, t.TxHash, t.LogIndex, t.From, t.To, t.Token,
		amountString(t.Amount, decimals), t.BlockNumber, t.BlockHash)
	if err != nil {
		return "", fmt.Errorf("insert deposit: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected > 0 {
		return "pending", nil
	}
	var status string
	err = l.db.QueryRowContext(ctx,
		`SELECT status FROM deposit_transactions WHERE chain = $1 AND tx_hash = $2 AND log_index = $3`,
		t.Chain, t.TxHash, t.LogIndex).Scan(&status)
	if err != nil {
		return "", fmt.Errorf("read existing deposit: %w", err)
	}
	return status, nil
}

// record writes an ignored/reverted deposit row for reconciliation. The
// amount is stored in display units (raw / 10^decimals): a raw value in a
// NUMERIC(38,18) column reads as an astronomically large number, which once
// made a 0.01 USDT probe show up as 1e16. reason carries the machine code
// the user-facing history renders. Persistence failures must stop the scan.
func (l *Ledger) record(ctx context.Context, t scanner.Transfer, status string, decimals int, reason string) error {
	result, err := l.db.ExecContext(ctx,
		`INSERT INTO deposit_transactions
		   (chain, tx_hash, log_index, from_address, to_address, token, amount, block_number, block_hash, status, reason)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		-- A pending row must be allowed to become ignored (thresholds or
		-- address status changed after it was seen); a resolved row must
		-- never be overwritten, since the money already moved.
		ON CONFLICT (chain, tx_hash, log_index) DO UPDATE
		  SET status = EXCLUDED.status,
		      reason = EXCLUDED.reason,
		      last_attempt_at = NOW()
		  WHERE deposit_transactions.status = 'pending'
		    AND deposit_transactions.order_id IS NULL
		    AND EXCLUDED.status IN ('ignored', 'reverted')`,
		t.Chain, t.TxHash, t.LogIndex, t.From, t.To, t.Token,
		amountString(t.Amount, decimals), t.BlockNumber, t.BlockHash, status, reason)
	if err != nil {
		return fmt.Errorf("record %s deposit: %w", status, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("record %s deposit result: %w", status, err)
	}
	if affected == 0 {
		return fmt.Errorf("deposit state or linked order changed while recording %s; retry required", status)
	}
	return nil
}

func (l *Ledger) ignore(ctx context.Context, t scanner.Transfer, decimals int, code, message string) error {
	if err := l.record(ctx, t, "ignored", decimals, code); err != nil {
		return err
	}
	l.notifyUnmatched(ctx, t, message)
	return nil
}

// toDecimal converts raw units to a float with the token's decimals.
func toDecimal(raw *big.Int, decimals int) float64 {
	value := new(big.Float).SetInt(raw)
	divisor := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	value.Quo(value, divisor)
	out, _ := value.Float64()
	return out
}

// amountString renders raw units as a decimal string for NUMERIC storage.
func amountString(raw *big.Int, decimals int) string {
	if raw == nil {
		return "0"
	}
	sign := ""
	value := new(big.Int).Set(raw)
	if value.Sign() < 0 {
		sign = "-"
		value.Neg(value)
	}
	if decimals <= 0 {
		return sign + value.String()
	}
	digits := value.String()
	for len(digits) <= decimals {
		digits = "0" + digits
	}
	whole := digits[:len(digits)-decimals]
	frac := strings.TrimRight(digits[len(digits)-decimals:], "0")
	if frac == "" {
		return sign + whole
	}
	return sign + whole + "." + frac
}
