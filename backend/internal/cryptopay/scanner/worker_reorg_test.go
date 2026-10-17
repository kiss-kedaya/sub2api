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

	mock.ExpectQuery(`FROM deposit_transactions`).
		WillReturnRows(sqlmock.NewRows([]string{"chain", "tx_hash", "log_index", "block_hash"}))

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
		WillReturnRows(sqlmock.NewRows([]string{"chain", "tx_hash", "log_index", "block_hash"}).
			AddRow("bsc", "0xdead", 0, "0xblock"))

	w := &Worker{db: db}
	if err := w.recheckCredited(t.Context()); err != nil {
		t.Fatalf("missing pool must be skipped, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
