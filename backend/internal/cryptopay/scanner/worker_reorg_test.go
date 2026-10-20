package scanner

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// A reorg recheck must query only recently credited rows: older ones are
// settled by finality, and scanning them all would be unbounded work.
func TestRecheckCreditedScopeIsBounded(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`(?s)LEFT JOIN payment_orders o ON o.id = d.order_id.*WHERE d.status = 'credited'.*COALESCE\(o.completed_at, d.created_at\) > NOW\(\) - INTERVAL '1 hour'.*OR d.reason LIKE 'reorg_missing_receipt:%'.*ORDER BY COALESCE\(d.last_attempt_at, d.created_at\), d.id.*LIMIT 200`).
		WillReturnRows(observationRows())

	w := &Worker{db: db}
	if err := w.recheckCredited(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A chain with no pool configured must be skipped, not crash the pass: the
// recheck runs for every chain on every scan tick.
func TestRecheckCreditedSkipsChainsWithoutPool(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`FROM deposit_transactions`).
		WillReturnRows(observationRows().
			AddRow("bsc", "0xdead", 0, "from", "to", "token", "1", 100, "0xblock", ""))
	mock.ExpectExec(`UPDATE deposit_transactions`).
		WithArgs("credited", "", "bsc", "0xdead", 0, "credited", "").
		WillReturnResult(sqlmock.NewResult(0, 1))

	w := &Worker{db: db}
	if err := w.recheckCredited(t.Context()); err != nil {
		t.Fatalf("missing pool must be skipped, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
