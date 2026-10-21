package ledger

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
)

type fixedResolver struct {
	userID int64
	ours   bool
}

func (r fixedResolver) IsOwnAddress(context.Context, string, string) (bool, error) { return false, nil }
func (r fixedResolver) ResolveDeposit(context.Context, string, string) (int64, bool, error) {
	return r.userID, r.ours, nil
}

type countingCreditor struct {
	calls    int
	requests []CreditRequest
}

func (c *countingCreditor) CreditDeposit(_ context.Context, req CreditRequest) error {
	c.calls++
	c.requests = append(c.requests, req)
	return nil
}

func depositTransfer(raw int64) scanner.Transfer {
	return scanner.Transfer{Chain: "bsc", TxHash: "test-transaction", LogIndex: 9,
		From: "sender", To: "deposit", Token: "token", Amount: big.NewInt(raw), BlockNumber: 101, BlockHash: "test-block"}
}

func TestRejectedDepositPersistenceErrorsReachCaller(t *testing.T) {
	tests := []struct {
		name           string
		raw            int64
		userID         int64
		reason, amount string
	}{
		{"below minimum", 100_000, 42, ReasonBelowMin, "0.1"},
		{"above maximum", 11_000_000, 42, ReasonAboveMax, "11"},
		{"unassigned or disabled", 1_000_000, 0, ReasonUnassigned, "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				mock.ExpectClose()
				if err := db.Close(); err != nil {
					t.Errorf("close persistence db: %v", err)
				}
			}()
			creditor := &countingCreditor{}
			l := New(db, creditor, fixedResolver{userID: tt.userID, ours: true}, 1, 10, 1)
			tr := depositTransfer(tt.raw)
			writeErr := errors.New("ignored evidence cannot be persisted")
			expectDepositState(mock, tr, stateRows())
			mock.ExpectExec(`INSERT INTO deposit_transactions`).WithArgs(tr.Chain, tr.TxHash, tr.LogIndex,
				tr.From, tr.To, tr.Token, tt.amount, tr.BlockNumber, tr.BlockHash, "ignored", tt.reason).WillReturnError(writeErr)
			notifications := 0
			l.SetUnmatchedNotifier(func(context.Context, scanner.Transfer, string) { notifications++ })
			credited, err := l.Process(t.Context(), tr, 6)
			if credited || !errors.Is(err, writeErr) {
				t.Fatalf("credited=%v error=%v", credited, err)
			}
			if creditor.calls != 0 || notifications != 0 {
				t.Fatalf("credit calls=%d notifications=%d before persistence", creditor.calls, notifications)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestIgnoredNotificationFollowsSuccessfulPersistence(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		mock.ExpectClose()
		if err := db.Close(); err != nil {
			t.Errorf("close notification db: %v", err)
		}
	}()
	l := New(db, &countingCreditor{}, fixedResolver{userID: 42, ours: true}, 1, 10, 1)
	expectDepositState(mock, depositTransfer(100_000), stateRows())
	mock.ExpectExec(`INSERT INTO deposit_transactions`).WillReturnResult(sqlmock.NewResult(1, 1))
	notifications := 0
	l.SetUnmatchedNotifier(func(context.Context, scanner.Transfer, string) {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("notification preceded persistence: %v", err)
		}
		notifications++
	})
	credited, err := l.Process(t.Context(), depositTransfer(100_000), 6)
	if credited || err != nil || notifications != 1 {
		t.Fatalf("credited=%v err=%v notifications=%d", credited, err, notifications)
	}
}

func TestPendingUnassignedAddressRemainsDurableForLaterReconciliation(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "persisted", true: "write fails"}[fail], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				mock.ExpectClose()
				if err := db.Close(); err != nil {
					t.Errorf("close pending db: %v", err)
				}
			}()
			creditor := &countingCreditor{}
			l := New(db, creditor, fixedResolver{ours: true}, 1, 10, 1)
			expectation := mock.ExpectExec(`INSERT INTO deposit_transactions`)
			writeErr := errors.New("pending write failed")
			if fail {
				expectation.WillReturnError(writeErr)
			} else {
				expectation.WillReturnResult(sqlmock.NewResult(1, 1))
			}
			err = l.ProcessPending(t.Context(), depositTransfer(1_000_000), 6)
			if fail && !errors.Is(err, writeErr) || !fail && err != nil {
				t.Fatalf("got %v", err)
			}
			if creditor.calls != 0 {
				t.Fatal("unconfirmed deposit must not credit funds")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
