package ledger

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// newProvisionalLedger builds a ledger whose instant-credit switch is on.
func newProvisionalLedger(t *testing.T, minInstant float64) (*Ledger, sqlmock.Sqlmock, *countingCreditor) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mock.ExpectClose()
		_ = db.Close()
	})
	creditor := &countingCreditor{}
	l := New(db, creditor, fixedResolver{userID: 42, ours: true}, 1, 10_000, 1)
	l.SetInstantCredit(true, minInstant)
	return l, mock, creditor
}

// The first verified observation credits immediately; a repeated observation of
// the same identity must not mint a second balance.
func TestInstantCreditLandsOncePerIdentity(t *testing.T) {
	l, mock, creditor := newProvisionalLedger(t, 0)
	tr := depositTransfer(10_000_000) // 10 USDT, multiplier 1

	expectDepositState(mock, tr, stateRows()) // no row yet
	mock.ExpectExec(`INSERT INTO deposit_transactions`).
		WithArgs(tr.Chain, tr.TxHash, tr.LogIndex, tr.From, tr.To, tr.Token, "10", tr.BlockNumber, tr.BlockHash).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`(?s)UPDATE deposit_transactions\s+SET status = 'credited'.*AND status = 'pending'`).
		WithArgs(tr.Chain, tr.TxHash, tr.LogIndex, true).
		WillReturnResult(sqlmock.NewResult(0, 1))

	credited, err := l.ProcessProvisional(t.Context(), tr, 6)
	if err != nil || !credited {
		t.Fatalf("first observation: credited=%v err=%v", credited, err)
	}
	if creditor.calls != 1 {
		t.Fatalf("creditor calls=%d want 1", creditor.calls)
	}

	// A second pass sees the credited row and must return without crediting.
	expectDepositState(mock, tr, stateRows().AddRow("credited", 88, 42, 10.0, "", nil, ""))
	credited, err = l.ProcessProvisional(t.Context(), tr, 6)
	if err != nil || credited {
		t.Fatalf("repeat observation: credited=%v err=%v", credited, err)
	}
	if creditor.calls != 1 {
		t.Fatalf("creditor calls=%d after repeat, want 1", creditor.calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// With the switch off the ledger must behave exactly like the old
// confirmation-gated path: record pending, never credit.
func TestInstantCreditDisabledRecordsPendingOnly(t *testing.T) {
	l, mock, creditor := newProvisionalLedger(t, 0)
	l.SetInstantCredit(false, 0)
	tr := depositTransfer(10_000_000)

	mock.ExpectExec(`INSERT INTO deposit_transactions`).
		WithArgs(tr.Chain, tr.TxHash, tr.LogIndex, tr.From, tr.To, tr.Token, "10", tr.BlockNumber, tr.BlockHash).
		WillReturnResult(sqlmock.NewResult(1, 1))
	credited, err := l.ProcessProvisional(t.Context(), tr, 6)
	if err != nil || credited {
		t.Fatalf("disabled: credited=%v err=%v", credited, err)
	}
	if creditor.calls != 0 {
		t.Fatalf("disabled path credited %d times", creditor.calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A deposit below instant_credit_min_amount keeps the confirmation-gated path.
func TestInstantCreditFloorRecordsPendingOnly(t *testing.T) {
	l, mock, creditor := newProvisionalLedger(t, 100) // credited amount would be 10
	tr := depositTransfer(10_000_000)

	expectDepositState(mock, tr, stateRows())
	mock.ExpectExec(`INSERT INTO deposit_transactions`).
		WithArgs(tr.Chain, tr.TxHash, tr.LogIndex, tr.From, tr.To, tr.Token, "10", tr.BlockNumber, tr.BlockHash).
		WillReturnResult(sqlmock.NewResult(1, 1))
	credited, err := l.ProcessProvisional(t.Context(), tr, 6)
	if err != nil || credited {
		t.Fatalf("below floor: credited=%v err=%v", credited, err)
	}
	if creditor.calls != 0 {
		t.Fatalf("below-floor deposit credited %d times", creditor.calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A revoked identity is terminal: neither the instant path nor the final path
// may credit it again, so a re-observed/re-mined transaction cannot mint money.
func TestRevokedDepositIsNeverCreditedAgain(t *testing.T) {
	l, mock, creditor := newProvisionalLedger(t, 0)
	tr := depositTransfer(10_000_000)

	for _, call := range []struct {
		name string
		fn   func() (bool, error)
	}{
		{"provisional", func() (bool, error) { return l.ProcessProvisional(t.Context(), tr, 6) }},
		{"final", func() (bool, error) { return l.Process(t.Context(), tr, 6) }},
	} {
		t.Run(call.name, func(t *testing.T) {
			// revocation already wrote status='reverted' + revoked_at.
			expectDepositState(mock, tr, stateRows().AddRow("reverted", 88, 42, 10.0, "", nil, ""))
			credited, err := call.fn()
			if err != nil || credited {
				t.Fatalf("revoked row credited: credited=%v err=%v", credited, err)
			}
			if creditor.calls != 0 {
				t.Fatalf("revoked row credited %d times", creditor.calls)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A provisional row that rechecked clean and was replayed by the scanner must
// not be credited twice when the final-credit path runs later.
func TestInstantCreditThenFinalCreditAddsBalanceOnce(t *testing.T) {
	l, mock, creditor := newProvisionalLedger(t, 0)
	tr := depositTransfer(10_000_000)

	expectDepositState(mock, tr, stateRows())
	mock.ExpectExec(`INSERT INTO deposit_transactions`).
		WithArgs(tr.Chain, tr.TxHash, tr.LogIndex, tr.From, tr.To, tr.Token, "10", tr.BlockNumber, tr.BlockHash).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`(?s)UPDATE deposit_transactions\s+SET status = 'credited'.*AND status = 'pending'`).
		WithArgs(tr.Chain, tr.TxHash, tr.LogIndex, true).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if credited, err := l.ProcessProvisional(t.Context(), tr, 6); err != nil || !credited {
		t.Fatalf("instant credit: credited=%v err=%v", credited, err)
	}

	// Finality is reached: the scanner calls Process, which must be a no-op
	// because the row is already credited.
	expectDepositState(mock, tr, stateRows().AddRow("credited", 88, 42, 10.0, "", nil, ""))
	if credited, err := l.Process(t.Context(), tr, 6); err != nil || credited {
		t.Fatalf("final credit after instant: credited=%v err=%v", credited, err)
	}
	if creditor.calls != 1 {
		t.Fatalf("creditor calls=%d want exactly 1", creditor.calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
