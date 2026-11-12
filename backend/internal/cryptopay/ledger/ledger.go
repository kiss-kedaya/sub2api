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
	"log/slog"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/nativeprice"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/shopspring/decimal"
)

// Creditor is the fulfillment surface the ledger needs. Implemented by an
// adapter over the existing payment service; tests substitute a fake.
type Creditor interface {
	// CreditDeposit creates a balance order for userID and runs the existing
	// fulfillment pipeline. Must be idempotent on (chain, txHash, logIndex)
	// via the deposit record status.
	CreditDeposit(ctx context.Context, req CreditRequest) error
}

// NativePricer quotes one chain's native coin in USD. Implemented by
// nativeprice.Provider; tests substitute a fake. A nil pricer fails closed:
// no native deposit is ever credited on a default price.
type NativePricer interface {
	Quote(ctx context.Context, chain string) (nativeprice.Quote, error)
}

// CreditRequest carries the resolved deposit for fulfillment.
type CreditRequest struct {
	UserID    int64
	Chain     string
	TxHash    string
	LogIndex  int
	BlockHash string
	Token     string
	Amount    *big.Int
	Decimals  int
	// CreditUSDT is the decimal amount credited to the balance (after
	// multiplier/fee). Existing linked orders use their frozen amount.
	CreditUSDT float64
	FromAddr   string
	ToAddr     string
	BlockNum   uint64
	// NativePriceUSD/At/Source freeze the market price a native deposit was
	// settled at. Empty for stablecoins. They are copied into the order
	// snapshot so the settlement can always be explained after the fact.
	NativePriceUSD    string
	NativePriceAt     time.Time
	NativePriceSource string
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
	pricer     NativePricer
	// onUnmatched reports a transfer that reached one of our addresses but
	// could not be credited (too small, or the address is unassigned).
	// Operators need this: it is usually a mistyped deposit or a probe.
	onUnmatched func(ctx context.Context, t scanner.Transfer, reason string)
}

// balanceScale is the internal balance precision (payment orders and the
// fulfillment pipeline round to 3 decimals). Native credits truncate to it so
// a fractional unit is never rounded up into free balance; stablecoins keep
// their existing rounding path untouched.
const balanceScale = 3

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

// SetNativePricer wires the market feed native deposits are settled against.
// Without one, native deposits stay pending rather than being credited at a
// guessed price; stablecoin crediting is unaffected.
func (l *Ledger) SetNativePricer(p NativePricer) {
	l.pricer = p
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
	if err := mode.CheckWritable(); err != nil {
		return false, err
	}
	// EVM hashes are bytes, not case-sensitive text. The DB unique key and
	// fulfillment identity must use the same canonical representation.
	t.TxHash = strings.ToLower(t.TxHash)
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
	case "credited", "ignored", "reverted", "dead_letter":
		return false, nil
	case "pending":
		if state.orderID.Valid {
			if !state.userID.Valid || !state.credit.Valid || state.credit.Float64 <= 0 || math.IsNaN(state.credit.Float64) || math.IsInf(state.credit.Float64, 0) {
				return false, fmt.Errorf("linked deposit order is missing or invalid")
			}
			// The order amount was frozen at the first settlement, so a retry
			// after this point can never re-price the deposit.
			return l.credit(ctx, t, decimals, state.userID.Int64, state.credit.Float64, nil)
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

	// Native coin has no stable USD value: it is settled at the market price
	// observed at credit time, and never by the asset-independent multiplier
	// alone (1 ETH must not be booked as 1 USDT).
	if t.Token == scanner.NativeToken {
		return l.processNative(ctx, t, decimals, state)
	}

	// Stablecoin path: unchanged. A token amount is already a USD amount, so
	// the multiplier applies directly and no price feed is consulted.
	amountDecimal := decimal.NewFromBigInt(t.Amount, -int32(decimals))
	if amountDecimal.LessThan(decimal.NewFromFloat(l.minAmount)) {
		reason := fmt.Sprintf("below the minimum credit amount (%g)", l.minAmount)
		return false, l.ignore(ctx, t, decimals, ReasonBelowMin, reason)
	}
	if l.maxAmount > 0 && amountDecimal.GreaterThan(decimal.NewFromFloat(l.maxAmount)) {
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

	creditAmount := amountDecimal.Mul(decimal.NewFromFloat(l.multiplier)).Round(3)
	creditUSDT, _ := creditAmount.Float64()

	status, err := l.insertPending(ctx, t, decimals)
	if err != nil {
		return false, err
	}
	switch status {
	case "credited", "ignored", "reverted", "dead_letter":
		// Already resolved: nothing to do. "pending" falls through so a
		// transaction that was observed before finality gets credited on a
		// later pass instead of being dropped when the cursor advances.
		return false, nil
	case "pending":
	default:
		return false, fmt.Errorf("unexpected deposit status %q", status)
	}

	credited, err = l.credit(ctx, t, decimals, userID, creditUSDT, nil)
	if errors.Is(err, ErrReorgedDuplicateTransfer) {
		const reason = "reorg_duplicate_credit_manual_review"
		if recordErr := l.record(ctx, t, "reverted", decimals, reason); recordErr != nil {
			return false, errors.Join(err, recordErr)
		}
		l.notifyUnmatched(ctx, t, reason)
		return false, nil
	}
	return credited, err
}

// processNative settles one native-coin deposit at its market price.
//
// Ordering matters for the three guarantees:
//   - the pending row exists before pricing, so a feed outage leaves a
//     retryable deposit instead of a silent one;
//   - the price is persisted before the credit, so a crash between the two
//     replays with the same price while it is still fresh; an order freezes
//     the settled amount permanently;
//   - any failure returns an error and leaves the row pending, never credited.
func (l *Ledger) processNative(ctx context.Context, t scanner.Transfer, decimals int, state depositState) (bool, error) {
	status, err := l.insertPending(ctx, t, decimals)
	if err != nil {
		return false, err
	}
	switch status {
	case "credited", "ignored", "reverted", "dead_letter":
		return false, nil
	case "pending":
	default:
		return false, fmt.Errorf("unexpected deposit status %q", status)
	}

	quote, err := l.nativeQuote(ctx, t, state)
	if err != nil {
		// Rate-limit the next attempt: retryPending only picks a row whose
		// last_attempt_at is old enough, and a failed fetch must not turn the
		// scan loop into a per-pass hammer of the exchange.
		l.touchPending(ctx, t)
		// The row stays pending and the next pass retries. Crediting on an
		// unavailable/stale price is explicitly forbidden.
		return false, err
	}
	now := time.Now()
	if !quote.Usable(now) {
		return false, fmt.Errorf("%w: %s quote from %s is %s old",
			nativeprice.ErrStale, t.Chain, quote.At.UTC().Format(time.RFC3339), quote.Age(now).Round(time.Second))
	}

	amountDecimal := decimal.NewFromBigInt(t.Amount, -int32(decimals))
	// Thresholds are denominated in USD (they were written for stablecoins,
	// whose token amount is already USD). Comparing raw ETH against a USDT
	// minimum would reject a $50 deposit as dust.
	notional := amountDecimal.Mul(quote.USD)
	if notional.LessThan(decimal.NewFromFloat(l.minAmount)) {
		reason := fmt.Sprintf("below the minimum credit amount (%g USD)", l.minAmount)
		return false, l.ignore(ctx, t, decimals, ReasonBelowMin, reason)
	}
	if l.maxAmount > 0 && notional.GreaterThan(decimal.NewFromFloat(l.maxAmount)) {
		reason := fmt.Sprintf("above the per-deposit maximum (%g USD)", l.maxAmount)
		return false, l.ignore(ctx, t, decimals, ReasonAboveMax, reason)
	}

	userID, isOurs, err := l.resolver.ResolveDeposit(ctx, t.Chain, t.To)
	if err != nil {
		return false, fmt.Errorf("resolve deposit: %w", err)
	}
	if !isOurs {
		return false, nil
	}
	if userID == 0 {
		return false, l.ignore(ctx, t, decimals, ReasonUnassigned, "address is not assigned to a user")
	}

	creditDecimal := notional.Mul(decimal.NewFromFloat(l.multiplier)).Truncate(balanceScale)
	creditUSDT, _ := creditDecimal.Float64()
	if creditUSDT <= 0 || math.IsNaN(creditUSDT) || math.IsInf(creditUSDT, 0) {
		// Truncation must never mint a sub-precision credit, but it also must
		// not silently drop funds: keep the row pending for an operator.
		return false, fmt.Errorf("native credit is below balance precision")
	}
	slog.Info("[cryptopay] native deposit priced",
		"chain", t.Chain,
		"tx", t.TxHash,
		"log_index", t.LogIndex,
		"user_id", userID,
		"amount", amountDecimal.String(),
		"price_usd", quote.USD.String(),
		"priced_at", quote.At.UTC().Format(time.RFC3339),
		"price_source", quote.Source,
		"credit_usdt", creditUSDT)

	credited, err := l.credit(ctx, t, decimals, userID, creditUSDT, &quote)
	if errors.Is(err, ErrReorgedDuplicateTransfer) {
		const reason = "reorg_duplicate_credit_manual_review"
		if recordErr := l.record(ctx, t, "reverted", decimals, reason); recordErr != nil {
			return false, errors.Join(err, recordErr)
		}
		l.notifyUnmatched(ctx, t, reason)
		return false, nil
	}
	return credited, err
}

func (l *Ledger) nativeQuote(ctx context.Context, t scanner.Transfer, state depositState) (nativeprice.Quote, error) {
	// A price persisted by an earlier attempt is authoritative. There is no
	// settlement yet at this point (a linked order short-circuits above), so a
	// stale stored quote is the only case in which it may be replaced.
	if stored, ok, err := quoteFromState(state); err != nil {
		return nativeprice.Quote{}, err
	} else if ok && stored.Usable(time.Now()) {
		return stored, nil
	}
	if l.pricer == nil {
		return nativeprice.Quote{}, fmt.Errorf("%w: no native price source configured", nativeprice.ErrUnavailable)
	}
	quote, err := l.pricer.Quote(ctx, t.Chain)
	if err != nil {
		return nativeprice.Quote{}, err
	}
	if !quote.Usable(time.Now()) {
		return nativeprice.Quote{}, fmt.Errorf("%w: %s quote is older than %s",
			nativeprice.ErrStale, t.Chain, nativeprice.MaxQuoteAge)
	}
	if err := l.persistNativePrice(ctx, t, quote); err != nil {
		return nativeprice.Quote{}, err
	}
	// Read back what the row actually holds: a concurrent pass may have
	// persisted first, and the credit must match the audited row.
	stored, ok, err := l.readNativePrice(ctx, t)
	if err != nil {
		return nativeprice.Quote{}, err
	}
	if !ok {
		return nativeprice.Quote{}, fmt.Errorf("%w: price was not recorded on the deposit", nativeprice.ErrUnavailable)
	}
	return stored, nil
}

func quoteFromState(state depositState) (nativeprice.Quote, bool, error) {
	price := strings.TrimSpace(state.priceUSD.String)
	if price == "" {
		return nativeprice.Quote{}, false, nil
	}
	usd, err := decimal.NewFromString(price)
	if err != nil || usd.Sign() <= 0 {
		return nativeprice.Quote{}, false, fmt.Errorf("stored native price %q is invalid", price)
	}
	if !state.priceAt.Valid || state.priceAt.Time.IsZero() {
		return nativeprice.Quote{}, false, fmt.Errorf("stored native price has no observation time")
	}
	return nativeprice.Quote{USD: usd, At: state.priceAt.Time, Source: state.priceSource.String}, true, nil
}

// persistNativePrice writes the settlement price for a deposit. A live price
// already on the row wins: the first writer is authoritative for retries. An
// expired price may be replaced only because no settlement has happened yet
// (a linked order short-circuits before pricing), so retrying forever on a
// price the feed no longer vouches for would strand real funds.
func (l *Ledger) persistNativePrice(ctx context.Context, t scanner.Transfer, quote nativeprice.Quote) error {
	cutoff := time.Now().Add(-nativeprice.MaxQuoteAge)
	_, err := l.db.ExecContext(ctx, `UPDATE deposit_transactions
		SET native_price_usd = $4, native_price_at = $5, native_price_source = $6, last_attempt_at = NOW()
		WHERE chain = $1 AND tx_hash = $2 AND log_index = $3
		  AND status = 'pending'
		  AND (native_price_usd IS NULL OR native_price_at IS NULL OR native_price_at < $7)`,
		t.Chain, t.TxHash, t.LogIndex, quote.USD.String(), quote.At.UTC(), quote.Source, cutoff)
	if err != nil {
		return fmt.Errorf("record native price: %w", err)
	}
	return nil
}

func (l *Ledger) readNativePrice(ctx context.Context, t scanner.Transfer) (nativeprice.Quote, bool, error) {
	var state depositState
	err := l.db.QueryRowContext(ctx, `SELECT COALESCE(CAST(native_price_usd AS TEXT), ''), native_price_at,
		       COALESCE(native_price_source, '')
		FROM deposit_transactions WHERE chain = $1 AND tx_hash = $2 AND log_index = $3`,
		t.Chain, t.TxHash, t.LogIndex).
		Scan(&state.priceUSD, &state.priceAt, &state.priceSource)
	if err != nil {
		return nativeprice.Quote{}, false, fmt.Errorf("read native price: %w", err)
	}
	return quoteFromState(state)
}

// touchPending rotates a pending row's retry clock without changing its state.
// It is best effort: the price error is the important signal, and a failed
// bookkeeping update must not replace it.
func (l *Ledger) touchPending(ctx context.Context, t scanner.Transfer) {
	if _, err := l.db.ExecContext(ctx, `UPDATE deposit_transactions SET last_attempt_at = NOW()
		WHERE chain = $1 AND tx_hash = $2 AND log_index = $3 AND status = 'pending'`,
		t.Chain, t.TxHash, t.LogIndex); err != nil {
		slog.Warn("[cryptopay] failed to rotate native deposit retry clock",
			"chain", t.Chain, "tx", t.TxHash, "log_index", t.LogIndex, "error", err)
	}
}

type depositState struct {
	status          string
	orderID, userID sql.NullInt64
	credit          sql.NullFloat64
	priceUSD        sql.NullString
	priceAt         sql.NullTime
	priceSource     sql.NullString
}

func (l *Ledger) existingDeposit(ctx context.Context, t scanner.Transfer) (depositState, error) {
	var state depositState
	_, outTradeNo := OrderIdentity(t.Chain, t.TxHash, t.LogIndex)
	err := l.db.QueryRowContext(ctx, `SELECT d.status, COALESCE(d.order_id, o.id), o.user_id, o.amount,
		       COALESCE(CAST(d.native_price_usd AS TEXT), ''), d.native_price_at, COALESCE(d.native_price_source, '')
		FROM deposit_transactions d LEFT JOIN payment_orders o
		  ON o.id = d.order_id OR (d.order_id IS NULL AND o.out_trade_no = $4)
		WHERE d.chain = $1 AND d.tx_hash = $2 AND d.log_index = $3`,
		t.Chain, t.TxHash, t.LogIndex, outTradeNo).
		Scan(&state.status, &state.orderID, &state.userID, &state.credit,
			&state.priceUSD, &state.priceAt, &state.priceSource)
	if errors.Is(err, sql.ErrNoRows) {
		return depositState{}, nil
	}
	if err != nil {
		return state, fmt.Errorf("read deposit state: %w", err)
	}
	return state, nil
}

func (l *Ledger) credit(ctx context.Context, t scanner.Transfer, decimals int, userID int64, creditUSDT float64, quote *nativeprice.Quote) (bool, error) {
	req := CreditRequest{
		UserID:     userID,
		Chain:      t.Chain,
		TxHash:     t.TxHash,
		LogIndex:   t.LogIndex,
		BlockHash:  t.BlockHash,
		Token:      t.Token,
		Amount:     t.Amount,
		Decimals:   decimals,
		CreditUSDT: creditUSDT,
		FromAddr:   t.From,
		ToAddr:     t.To,
		BlockNum:   t.BlockNumber,
	}
	if quote != nil {
		req.NativePriceUSD = quote.USD.String()
		req.NativePriceAt = quote.At.UTC()
		req.NativePriceSource = quote.Source
	}
	if err := l.creditor.CreditDeposit(ctx, req); err != nil {
		// A credit failure leaves the row pending and retries on every pass, so
		// without a log the operator only sees "deposit never arrived". Log the
		// safe identifiers and the cause; amounts and hashes are already public
		// on-chain, and nothing from the RPC layer (endpoints, credentials) is
		// included because the creditor only returns its own error text.
		slog.Error("[cryptopay] deposit credit failed; row stays pending",
			"chain", t.Chain, "tx", t.TxHash, "log_index", t.LogIndex,
			"user_id", userID, "credit_usdt", creditUSDT, "cause", err.Error())
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
		slog.Error("[cryptopay] deposit status update did not apply; manual reconciliation required",
			"chain", t.Chain, "tx", t.TxHash, "log_index", t.LogIndex,
			"user_id", userID, "current_status", status)
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
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	t.TxHash = strings.ToLower(t.TxHash)
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
	value := decimal.NewFromBigInt(raw, -int32(decimals))
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
