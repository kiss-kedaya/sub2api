package ledger

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/nativeprice"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// failOnceCreditor fails the first fulfillment, then succeeds. It lets a test
// observe the window in which the price was persisted but the balance was not
// yet credited.
type failOnceCreditor struct {
	calls    int
	requests []CreditRequest
}

func (c *failOnceCreditor) CreditDeposit(_ context.Context, req CreditRequest) error {
	c.calls++
	c.requests = append(c.requests, req)
	if c.calls == 1 {
		return errors.New("fulfillment temporarily unavailable")
	}
	return nil
}

func TestNativeDepositCreditsAtMarketPrice(t *testing.T) {
	db := nativeDepositDB(t)
	creditor := &countingCreditor{}
	l := New(db, creditor, fixedResolver{userID: 42, ours: true}, 0.001, 10000, 6.7)
	pricer := &stubPricer{price: decimal.NewFromInt(2000)}
	l.SetNativePricer(pricer)

	// 1 ETH at $2000 with the existing 6.7 multiplier => 13400 balance units.
	credited, err := l.Process(t.Context(), nativeLedgerTransfer(big.NewInt(1_000_000_000_000_000_000)), 18)
	require.NoError(t, err)
	require.True(t, credited)
	require.Equal(t, 1, creditor.calls)
	require.InDelta(t, 13400, creditor.requests[0].CreditUSDT, 1e-9)
	require.Equal(t, "2000", creditor.requests[0].NativePriceUSD)
	require.Equal(t, "stub", creditor.requests[0].NativePriceSource)
	require.False(t, creditor.requests[0].NativePriceAt.IsZero())

	var price, source string
	var pricedAt time.Time
	require.NoError(t, db.QueryRow(`SELECT CAST(native_price_usd AS TEXT), native_price_at, native_price_source
		FROM deposit_transactions`).Scan(&price, &pricedAt, &source))
	require.Equal(t, "2000", price)
	require.Equal(t, "stub", source)
	require.False(t, pricedAt.IsZero(), "the deposit row must answer when the price was observed")
}

// A feed outage must leave the deposit pending and never call the creditor.
// The next pass, once the feed answers, must credit it.
func TestNativePriceFailureLeavesDepositRetryable(t *testing.T) {
	db := nativeDepositDB(t)
	creditor := &countingCreditor{}
	l := New(db, creditor, fixedResolver{userID: 42, ours: true}, 0.001, 10000, 6.7)
	pricer := &stubPricer{err: errTestPrice}
	l.SetNativePricer(pricer)
	transfer := nativeLedgerTransfer(big.NewInt(1_000_000_000_000_000_000))

	credited, err := l.Process(t.Context(), transfer, 18)
	require.ErrorIs(t, err, errTestPrice)
	require.False(t, credited)
	require.Zero(t, creditor.calls, "no balance may be credited without a price")

	var status string
	var price sql.NullString
	require.NoError(t, db.QueryRow(`SELECT status, native_price_usd FROM deposit_transactions`).Scan(&status, &price))
	require.Equal(t, "pending", status)
	require.False(t, price.Valid, "a failed fetch must not record a price")

	// The next pass gets a quote and settles the same deposit.
	pricer.err = nil
	pricer.price = decimal.NewFromInt(2000)
	credited, err = l.Process(t.Context(), transfer, 18)
	require.NoError(t, err)
	require.True(t, credited)
	require.Equal(t, 1, creditor.calls)
	require.InDelta(t, 13400, creditor.requests[0].CreditUSDT, 1e-9)
}

// A quote older than the freshness ceiling is as unusable as no quote.
func TestStaleNativePriceIsNotCredited(t *testing.T) {
	db := nativeDepositDB(t)
	creditor := &countingCreditor{}
	l := New(db, creditor, fixedResolver{userID: 42, ours: true}, 0.001, 10000, 6.7)
	l.SetNativePricer(&stubPricer{price: decimal.NewFromInt(2000), at: time.Now().Add(-nativeprice.MaxQuoteAge - time.Minute)})

	credited, err := l.Process(t.Context(), nativeLedgerTransfer(big.NewInt(1_000_000_000_000_000_000)), 18)
	require.ErrorIs(t, err, nativeprice.ErrStale)
	require.False(t, credited)
	require.Zero(t, creditor.calls)

	var status string
	require.NoError(t, db.QueryRow(`SELECT status FROM deposit_transactions`).Scan(&status))
	require.Equal(t, "pending", status)
}

// Once a settlement price is recorded, a replay must use it — even if the
// market (and therefore the feed) has moved since.
func TestRepeatedNativeProcessingKeepsTheFirstPrice(t *testing.T) {
	db := nativeDepositDB(t)
	creditor := &failOnceCreditor{}
	l := New(db, creditor, fixedResolver{userID: 42, ours: true}, 0.001, 10000, 6.7)
	pricer := &stubPricer{price: decimal.NewFromInt(2000)}
	l.SetNativePricer(pricer)
	transfer := nativeLedgerTransfer(big.NewInt(1_000_000_000_000_000_000))

	// First attempt persists $2000, then fulfillment fails; the row stays
	// pending with the price already recorded.
	_, err := l.Process(t.Context(), transfer, 18)
	require.Error(t, err)
	require.Equal(t, 1, creditor.calls)
	require.InDelta(t, 13400, creditor.requests[0].CreditUSDT, 1e-9)

	// The market doubles. The replay must not re-price the deposit.
	pricer.price = decimal.NewFromInt(3000)
	credited, err := l.Process(t.Context(), transfer, 18)
	require.NoError(t, err)
	require.True(t, credited)
	require.Equal(t, 2, creditor.calls)
	require.InDelta(t, 13400, creditor.requests[1].CreditUSDT, 1e-9,
		"the replay used a new price instead of the first settlement price")
	require.Equal(t, 1, pricer.calls, "a stored price must not be re-fetched")
	require.Equal(t, "2000", creditor.requests[1].NativePriceUSD)
}

// A price observed but never settled (no order) may expire. Retrying must
// replace it with a fresh quote instead of stranding the deposit forever; the
// idempotency guarantee only binds once an order freezes the amount.
func TestExpiredStoredPriceIsReplacedBeforeSettlement(t *testing.T) {
	db := nativeDepositDB(t)
	creditor := &countingCreditor{}
	l := New(db, creditor, fixedResolver{userID: 42, ours: true}, 0.001, 10000, 6.7)
	transfer := nativeLedgerTransfer(big.NewInt(1_000_000_000_000_000_000))
	require.NoError(t, l.ProcessPending(t.Context(), transfer, 18))
	_, err := db.Exec(`UPDATE deposit_transactions
		SET native_price_usd = '1000', native_price_at = $1, native_price_source = 'old'`, time.Now().Add(-nativeprice.MaxQuoteAge-time.Minute))
	require.NoError(t, err)

	l.SetNativePricer(&stubPricer{price: decimal.NewFromInt(2000)})
	credited, err := l.Process(t.Context(), transfer, 18)
	require.NoError(t, err)
	require.True(t, credited)
	require.InDelta(t, 13400, creditor.requests[0].CreditUSDT, 1e-9)

	var price, source string
	require.NoError(t, db.QueryRow(`SELECT CAST(native_price_usd AS TEXT), native_price_source
		FROM deposit_transactions`).Scan(&price, &source))
	require.Equal(t, "2000", price)
	require.Equal(t, "stub", source)
}

// Balance precision is 3 decimals and native credit truncates: rounding a
// fraction up would mint balance the deposit did not pay for.
func TestNativeCreditTruncatesInsteadOfRoundingUp(t *testing.T) {
	db := nativeDepositDB(t)
	creditor := &countingCreditor{}
	l := New(db, creditor, fixedResolver{userID: 42, ours: true}, 0.001, 10000, 6.7)
	l.SetNativePricer(&stubPricer{price: decimal.NewFromInt(1)})

	// 1.0001 ETH * $1 * 6.7 = 6.70067; truncation yields 6.700, not 6.701.
	credited, err := l.Process(t.Context(), nativeLedgerTransfer(big.NewInt(1_000_100_000_000_000_000)), 18)
	require.NoError(t, err)
	require.True(t, credited)
	require.InDelta(t, 6.7, creditor.requests[0].CreditUSDT, 1e-9)
	require.Less(t, creditor.requests[0].CreditUSDT, 6.701)
}

// Stablecoins already carry a USD amount; they must take the original path and
// never touch the market feed.
func TestStablecoinCreditIgnoresMarketPrice(t *testing.T) {
	db := nativeDepositDB(t)
	creditor := &countingCreditor{}
	l := New(db, creditor, fixedResolver{userID: 42, ours: true}, 0.001, 10000, 6.7)
	pricer := &stubPricer{err: errTestPrice}
	l.SetNativePricer(pricer)

	transfer := scanner.Transfer{
		Chain: "bsc", TxHash: "0xtoken", LogIndex: 3,
		From: "0xsender", To: "0xdeposit",
		Token:  "0x55d398326f99059ff775485246999027b3197955",
		Amount: big.NewInt(10_000_000), BlockNumber: 100, BlockHash: "0xblock",
	}
	credited, err := l.Process(t.Context(), transfer, 6)
	require.NoError(t, err)
	require.True(t, credited)
	require.InDelta(t, 67, creditor.requests[0].CreditUSDT, 1e-9)
	require.Empty(t, creditor.requests[0].NativePriceUSD)
	require.Zero(t, pricer.calls, "the stablecoin path must not consult the price feed")
}
