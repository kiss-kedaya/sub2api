package ledger

import (
	"database/sql"
	"database/sql/driver"
	"math/big"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	sqlite "modernc.org/sqlite"
)

// The ledger writes Postgres SQL. sqlite has no NOW(), so the statements under
// test would fail on a function that production provides; registering it keeps
// the production statement text (and therefore the test's fidelity) unchanged.
func init() {
	if err := sqlite.RegisterScalarFunction("NOW", 0,
		func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
			return time.Now().UTC().Format(time.RFC3339Nano), nil
		}); err != nil {
		panic(err)
	}
}

// nativeDepositDB is the smallest Postgres-compatible schema the ledger's
// statements touch, in sqlite. It deliberately keeps the production unique
// index: idempotency for a native deposit is the same constraint that protects
// a token deposit, not a second bookkeeping mechanism.
//
// amount is TEXT rather than NUMERIC because sqlite's NUMERIC affinity converts
// to a float and would silently lose the 18th decimal. Production uses
// NUMERIC(38,18), whose exactness is covered by the Postgres integration test.
func nativeDepositDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE deposit_transactions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chain TEXT, tx_hash TEXT, log_index INTEGER,
			from_address TEXT, to_address TEXT, token TEXT, amount TEXT,
			block_number INTEGER, block_hash TEXT, confirmations INTEGER DEFAULT 0,
			status TEXT DEFAULT 'pending', order_id INTEGER, reason TEXT,
			created_at TEXT DEFAULT CURRENT_TIMESTAMP, last_attempt_at TEXT,
			native_price_usd TEXT, native_price_at TIMESTAMP, native_price_source TEXT,
			UNIQUE(chain, tx_hash, log_index))`,
		`CREATE TABLE payment_orders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			out_trade_no TEXT, user_id INTEGER, amount NUMERIC)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func nativeLedgerTransfer(amount *big.Int) scanner.Transfer {
	return scanner.Transfer{
		Chain: "ethereum", TxHash: "0xnativetx", LogIndex: scanner.NativeLogIndex,
		From: "0xsender", To: "0xdeposit", Token: scanner.NativeToken, Native: true,
		Amount: amount, BlockNumber: 100, BlockHash: "0xblock",
	}
}

// One mined transaction must credit exactly once, however many scan passes
// observe it. The 18-decimal amount must survive the NUMERIC round trip too:
// a credit built from a mis-scaled amount would be off by 1e12.
func TestNativeDepositIsCreditedOncePerTransaction(t *testing.T) {
	db := nativeDepositDB(t)
	creditor := &countingCreditor{}
	l := New(db, creditor, fixedResolver{userID: 42, ours: true}, 0.001, 10000, 6.7)
	l.SetNativePricer(fixedPricer(10))

	raw, ok := new(big.Int).SetString("1234567890123456789", 10) // 1.234567890123456789 ETH
	if !ok {
		t.Fatal("bad fixture")
	}
	transfer := nativeLedgerTransfer(raw)

	credited, err := l.Process(t.Context(), transfer, 18)
	if err != nil {
		t.Fatalf("first native credit failed: %v", err)
	}
	if !credited || creditor.calls != 1 {
		t.Fatalf("first observation credited=%v calls=%d, want true/1", credited, creditor.calls)
	}
	// A later scan pass sees the same transaction again.
	credited, err = l.Process(t.Context(), transfer, 18)
	if err != nil {
		t.Fatalf("repeat native observation failed: %v", err)
	}
	if credited || creditor.calls != 1 {
		t.Fatalf("repeat observation credited twice: credited=%v calls=%d", credited, creditor.calls)
	}

	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM deposit_transactions`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("deposit rows = %d, want exactly 1", rows)
	}
	var token, amount, status string
	var logIndex int
	if err := db.QueryRow(`SELECT token, amount, status, log_index FROM deposit_transactions`).Scan(&token, &amount, &status, &logIndex); err != nil {
		t.Fatal(err)
	}
	if token != scanner.NativeToken || logIndex != scanner.NativeLogIndex || status != "credited" {
		t.Fatalf("stored native row = token:%q log_index:%d status:%q", token, logIndex, status)
	}
	if amount != "1.234567890123456789" {
		t.Fatalf("stored amount = %q, want the exact 18-decimal value", amount)
	}
	if got := creditor.requests[0].CreditUSDT; got <= 0 {
		t.Fatalf("credited balance = %v", got)
	}
	if creditor.requests[0].Decimals != 18 {
		t.Fatalf("credit request decimals = %d, want 18", creditor.requests[0].Decimals)
	}
}

// A pending observation written before finality must not duplicate the row when
// the same transfer is recorded again, and the unique index must keep a native
// deposit distinct from a token deposit in the same transaction.
func TestNativePendingObservationIsIdempotent(t *testing.T) {
	db := nativeDepositDB(t)
	creditor := &countingCreditor{}
	l := New(db, creditor, fixedResolver{userID: 42, ours: true}, 0.001, 10000, 6.7)
	l.SetNativePricer(fixedPricer(10))

	transfer := nativeLedgerTransfer(big.NewInt(1_000_000_000_000_000_000))
	for range 3 {
		if err := l.ProcessPending(t.Context(), transfer, 18); err != nil {
			t.Fatalf("record pending native observation: %v", err)
		}
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM deposit_transactions`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("pending rows = %d, want 1", rows)
	}

	// A token deposit mined in the same transaction is a different row: the
	// native sentinel owns the synthetic log index, the log owns its own.
	tokenTransfer := transfer
	tokenTransfer.Token = "0xdac17f958d2ee523a2206206994597c13d831ec7"
	tokenTransfer.Native = false
	tokenTransfer.LogIndex = 4
	tokenTransfer.Amount = big.NewInt(10_000_000)
	if err := l.ProcessPending(t.Context(), tokenTransfer, 6); err != nil {
		t.Fatalf("record token observation beside the native one: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM deposit_transactions`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("rows = %d, want the native and token deposits to coexist", rows)
	}
}

// A native deposit below the credit minimum must be recorded with the
// token-independent reason the UI renders, never silently dropped.
func TestNativeDepositBelowMinimumIsIgnoredNotDropped(t *testing.T) {
	db := nativeDepositDB(t)
	creditor := &countingCreditor{}
	l := New(db, creditor, fixedResolver{userID: 42, ours: true}, 0.001, 10000, 6.7)
	l.SetNativePricer(fixedPricer(10))

	// 1 wei is far below the 0.001 minimum.
	credited, err := l.Process(t.Context(), nativeLedgerTransfer(big.NewInt(1)), 18)
	if err != nil {
		t.Fatalf("below-minimum native deposit failed: %v", err)
	}
	if credited || creditor.calls != 0 {
		t.Fatal("a dust native deposit was credited")
	}
	var status, reason string
	if err := db.QueryRow(`SELECT status, reason FROM deposit_transactions`).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "ignored" || reason != ReasonBelowMin {
		t.Fatalf("dust native deposit = status:%q reason:%q", status, reason)
	}
}
