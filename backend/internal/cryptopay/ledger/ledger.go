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
	// multiplier/fee). Computed by the ledger from config.
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
	db           *sql.DB
	creditor     Creditor
	resolver     AddressResolver
	minAmount    float64
	multiplier   float64
	ownAddresses func(ctx context.Context, chain string) ([]string, error)
}

func New(db *sql.DB, creditor Creditor, resolver AddressResolver, minAmount, multiplier float64) *Ledger {
	return &Ledger{db: db, creditor: creditor, resolver: resolver, minAmount: minAmount, multiplier: multiplier}
}

// Process handles one observed transfer. Returns credited=true when a
// balance was added.
func (l *Ledger) Process(ctx context.Context, t scanner.Transfer, decimals int) (credited bool, err error) {
	if t.Amount == nil || t.Amount.Sign() <= 0 {
		return false, nil
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
		return l.record(ctx, t, "ignored", 0), nil
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
		return l.record(ctx, t, "ignored", 0), nil
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
	if _, err := l.db.ExecContext(ctx,
		`UPDATE deposit_transactions SET status = 'credited' WHERE chain = $1 AND tx_hash = $2 AND log_index = $3`,
		t.Chain, t.TxHash, t.LogIndex); err != nil {
		return false, fmt.Errorf("mark credited: %w", err)
	}
	return true, nil
}

// ProcessPending records a transfer that is not yet final so a later pass
// can credit it. The row stays "pending" until Process succeeds; re-running
// this is a no-op thanks to the unique key.
func (l *Ledger) ProcessPending(ctx context.Context, t scanner.Transfer) error {
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
	userID, isOurs, err := l.resolver.ResolveDeposit(ctx, t.Chain, t.To)
	if err != nil {
		return fmt.Errorf("resolve deposit: %w", err)
	}
	if !isOurs || userID == 0 {
		return nil
	}
	_, err = l.db.ExecContext(ctx,
		`INSERT INTO deposit_transactions
		   (chain, tx_hash, log_index, from_address, to_address, token, amount, block_number, block_hash, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending')
		 ON CONFLICT (chain, tx_hash, log_index) DO NOTHING`,
		t.Chain, t.TxHash, t.LogIndex, t.From, t.To, t.Token,
		amountString(t.Amount, 6), t.BlockNumber, t.BlockHash)
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

// record writes an ignored/reverted deposit row for reconciliation. Returns
// credited=false always.
func (l *Ledger) record(ctx context.Context, t scanner.Transfer, status string, decimals int) bool {
	_, err := l.db.ExecContext(ctx,
		`INSERT INTO deposit_transactions
		   (chain, tx_hash, log_index, from_address, to_address, token, amount, block_number, block_hash, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		 ON CONFLICT (chain, tx_hash, log_index) DO NOTHING`,
		t.Chain, t.TxHash, t.LogIndex, t.From, t.To, t.Token,
		amountString(t.Amount, decimals), t.BlockNumber, t.BlockHash, status)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false
	}
	return false
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
