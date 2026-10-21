package ledger

import (
	"database/sql"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	_ "modernc.org/sqlite"
)

func stateRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"status", "order_id", "user_id", "amount"})
}

func expectDepositState(mock sqlmock.Sqlmock, tr scanner.Transfer, rows *sqlmock.Rows) {
	_, outTradeNo := OrderIdentity(tr.Chain, tr.TxHash, tr.LogIndex)
	mock.ExpectQuery(`(?s)SELECT d.status, COALESCE\(d.order_id, o.id\), o.user_id, o.amount.*LEFT JOIN payment_orders`).
		WithArgs(tr.Chain, tr.TxHash, tr.LogIndex, outTradeNo).WillReturnRows(rows)
}

func TestPendingOrderWithFailedLinkRestoresOriginalTerms(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	for _, schema := range []string{
		`CREATE TABLE payment_orders(id INTEGER PRIMARY KEY,user_id INTEGER,amount REAL,out_trade_no TEXT UNIQUE)`,
		`CREATE TABLE deposit_transactions(chain TEXT,tx_hash TEXT,log_index INTEGER,status TEXT,order_id INTEGER)`,
	} {
		if _, err := db.ExecContext(t.Context(), schema); err != nil {
			t.Fatal(err)
		}
	}
	tr := depositTransfer(10_000_000)
	_, outTradeNo := OrderIdentity(tr.Chain, tr.TxHash, tr.LogIndex)
	if _, err := db.ExecContext(t.Context(), `INSERT INTO payment_orders VALUES(88,42,67,?)`, outTradeNo); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO deposit_transactions VALUES(?,?,?,'pending',NULL)`, tr.Chain, tr.TxHash, tr.LogIndex); err != nil {
		t.Fatal(err)
	}
	creditor := &countingCreditor{}
	// No resolver is provided: the already-created order must recover even
	// after the address is disabled and new amount policies reject this sum.
	l := New(db, creditor, nil, 100, 1, 0)
	credited, err := l.Process(t.Context(), tr, 6)
	if err != nil || !credited || creditor.calls != 1 || creditor.requests[0].UserID != 42 || creditor.requests[0].CreditUSDT != 67 {
		t.Fatalf("unlinked recovery: credited=%v err=%v requests=%+v", credited, err, creditor.requests)
	}
}

func TestLinkedPendingRestoresFrozenOrderBeforeMutablePolicies(t *testing.T) {
	for _, setting := range []string{"minimum raised", "maximum lowered", "multiplier changed", "multiplier zero", "multiplier invalid", "address disabled"} {
		t.Run(setting, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			creditor := &countingCreditor{}
			tr := depositTransfer(10_000_000)
			min, max, multiplier := 1.0, 100.0, 6.7
			switch setting {
			case "minimum raised":
				min = 100
			case "maximum lowered":
				max = 1
			case "multiplier changed":
				multiplier = 6
			case "multiplier zero":
				multiplier = 0
			case "multiplier invalid":
				multiplier = math.NaN()
			}
			// Disabled/unassigned now resolves to user zero. Only the linked
			// order's owner and frozen amount may drive recovery.
			l := New(db, creditor, fixedResolver{ours: true}, min, max, multiplier)
			expectDepositState(mock, tr, stateRows().AddRow("pending", 88, 42, 67.0))
			mock.ExpectExec(`(?s)UPDATE deposit_transactions SET status = 'credited'.*AND status = 'pending'`).
				WithArgs(tr.Chain, tr.TxHash, tr.LogIndex).WillReturnResult(sqlmock.NewResult(0, 1))
			credited, err := l.Process(t.Context(), tr, 6)
			if !credited || err != nil || creditor.calls != 1 {
				t.Fatalf("credited=%v err=%v calls=%d", credited, err, creditor.calls)
			}
			req := creditor.requests[0]
			if req.UserID != 42 || req.CreditUSDT != 67 || req.Amount.Cmp(tr.Amount) != 0 {
				t.Fatalf("original terms changed: %+v", req)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResolvedDepositDoesNotConsultPolicyOrOverwriteState(t *testing.T) {
	for _, status := range []string{"credited", "ignored", "reverted"} {
		t.Run(status, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			creditor := &countingCreditor{}
			tr := depositTransfer(10_000_000)
			l := New(db, creditor, nil, 100, 1, 0) // nil resolver must not be called.
			expectDepositState(mock, tr, stateRows().AddRow(status, 88, 42, 67.0))
			credited, err := l.Process(t.Context(), tr, 6)
			if credited || err != nil || creditor.calls != 0 {
				t.Fatalf("credited=%v err=%v calls=%d", credited, err, creditor.calls)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLinkedPendingWithMissingOrderFailsClosed(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	creditor := &countingCreditor{}
	tr := depositTransfer(10_000_000)
	l := New(db, creditor, nil, 100, 1, 0)
	expectDepositState(mock, tr, stateRows().AddRow("pending", 88, nil, nil))
	credited, err := l.Process(t.Context(), tr, 6)
	if credited || err == nil || creditor.calls != 0 {
		t.Fatalf("credited=%v err=%v calls=%d", credited, err, creditor.calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreditedCASPreservesReorgAndConcurrentCompletion(t *testing.T) {
	for _, concurrentStatus := range []string{"reverted", "ignored", "credited"} {
		t.Run(concurrentStatus, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			creditor := &countingCreditor{}
			tr := depositTransfer(10_000_000)
			l := New(db, creditor, nil, 100, 1, 0)
			expectDepositState(mock, tr, stateRows().AddRow("pending", 88, 42, 67.0))
			mock.ExpectExec(`(?s)UPDATE deposit_transactions SET status = 'credited'.*AND status = 'pending'`).
				WithArgs(tr.Chain, tr.TxHash, tr.LogIndex).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(`SELECT status FROM deposit_transactions`).WithArgs(tr.Chain, tr.TxHash, tr.LogIndex).
				WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(concurrentStatus))
			credited, err := l.Process(t.Context(), tr, 6)
			if credited || (err != nil) != (concurrentStatus != "credited") {
				t.Fatalf("credited=%v err=%v", credited, err)
			}
			if concurrentStatus != "credited" && !strings.Contains(err.Error(), "manual reconciliation") {
				t.Fatalf("missing reconciliation error: %v", err)
			}
			if creditor.calls != 1 {
				t.Fatalf("fulfillment calls=%d", creditor.calls)
			}
			// No balance debit, compensating order or unguarded status write.
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestIgnoredCASDoesNotEraseConcurrentlyLinkedOrder(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	creditor := &countingCreditor{}
	tr := depositTransfer(10_000_000)
	l := New(db, creditor, fixedResolver{userID: 42, ours: true}, 100, 1000, 1)
	expectDepositState(mock, tr, stateRows().AddRow("pending", nil, nil, nil))
	mock.ExpectExec(`(?s)INSERT INTO deposit_transactions.*WHERE deposit_transactions.status = 'pending'.*AND deposit_transactions.order_id IS NULL`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	credited, err := l.Process(t.Context(), tr, 6)
	if credited || err == nil || creditor.calls != 0 {
		t.Fatalf("credited=%v err=%v calls=%d", credited, err, creditor.calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestFailedCreditedWriteCanResumeLinkedOrder(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	creditor := &countingCreditor{}
	tr := depositTransfer(10_000_000)
	l := New(db, creditor, nil, 100, 1, 0)
	writeErr := errors.New("mark credited unavailable")
	for attempt := 0; attempt < 2; attempt++ {
		expectDepositState(mock, tr, stateRows().AddRow("pending", 88, 42, 67.0))
		expectation := mock.ExpectExec(`(?s)UPDATE deposit_transactions SET status = 'credited'.*AND status = 'pending'`)
		if attempt == 0 {
			expectation.WillReturnError(writeErr)
		} else {
			expectation.WillReturnResult(sqlmock.NewResult(0, 1))
		}
		credited, err := l.Process(t.Context(), tr, 6)
		if attempt == 0 && (credited || !errors.Is(err, writeErr)) {
			t.Fatalf("first credited=%v err=%v", credited, err)
		}
		if attempt == 1 && (!credited || err != nil) {
			t.Fatalf("retry credited=%v err=%v", credited, err)
		}
	}
	if creditor.calls != 2 || creditor.requests[0].CreditUSDT != 67 || creditor.requests[1].CreditUSDT != 67 {
		t.Fatal("retry lost frozen order amount")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
