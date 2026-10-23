package cryptopay

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// A credited deposit on a legacy index-0 address must stay sweeable: the
// reader may only skip rows whose assignment could not be resolved (NULL
// after the LEFT JOIN). Dropping index 0 left the sweep plan empty for the
// first customer address and the funds unmovable.
func TestUnsweptBalancesKeepsIndexZero(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	const token = "0x55d398326f99059ff775485246999027b3197955"
	mock.ExpectQuery(`FROM deposit_transactions`).
		WithArgs("bsc").
		WillReturnRows(sqlmock.NewRows([]string{"to_address", "token", "amount", "idx"}).
			AddRow("0x339ffddcc5a3cac856e41ed3793bcf375b7551f5", token, "0.010000000000000000", 0).
			AddRow("0xdead", token, "5.000000000000000000", nil))

	reader := newBalanceReader(db, map[string]int{tokenDecimalsKey("bsc", token): 18})
	out, err := reader.UnsweptBalances(context.Background(), "bsc")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 row (NULL index skipped), got %d", len(out))
	}
	if out[0].DerivationIndex != 0 {
		t.Fatalf("index-0 row must survive, got %d", out[0].DerivationIndex)
	}
	if got := out[0].Raw.String(); got != "10000000000000000" {
		t.Fatalf("raw = %s, want 1e16", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A successful sweep must not exclude an address forever: deposit addresses
// are reused, so only the credited-minus-swept remainder is eligible.
func TestUnsweptBalancesSubtractsSweptRemainder(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	const token = "0x55d398326f99059ff775485246999027b3197955"
	mock.ExpectQuery(`FROM deposit_transactions`).
		WithArgs("bsc").
		WillReturnRows(sqlmock.NewRows([]string{"to_address", "token", "remainder", "idx"}).
			AddRow("0x339ffddcc5a3cac856e41ed3793bcf375b7551f5", token, "2.000000000000000000", 0))

	reader := newBalanceReader(db, map[string]int{tokenDecimalsKey("bsc", token): 18})
	out, err := reader.UnsweptBalances(context.Background(), "bsc")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 remainder row, got %d", len(out))
	}
	if got := out[0].Raw.String(); got != "2000000000000000000" {
		t.Fatalf("raw = %s, want 2e18", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
