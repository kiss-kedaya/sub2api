package ledger

import (
	"database/sql"
	"strings"
	"testing"
)

func TestTransactionHashCasingCannotCreateSecondCredit(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	for _, schema := range []string{
		`CREATE TABLE payment_orders(id INTEGER PRIMARY KEY,user_id INTEGER,amount REAL,out_trade_no TEXT UNIQUE)`,
		`CREATE TABLE deposit_transactions(chain TEXT,tx_hash TEXT,log_index INTEGER,from_address TEXT,to_address TEXT,token TEXT,
		 amount TEXT,block_number INTEGER,block_hash TEXT,status TEXT,order_id INTEGER, UNIQUE(chain,tx_hash,log_index))`,
	} {
		if _, err := db.ExecContext(t.Context(), schema); err != nil {
			t.Fatal(err)
		}
	}
	creditor := &countingCreditor{}
	l := New(db, creditor, fixedResolver{ours: true, userID: 42}, 1, 1000, 1)
	tr := depositTransfer(10_000_000)
	tr.TxHash = "0x" + strings.Repeat("ab", 32)
	code, tradeNo := OrderIdentity(tr.Chain, tr.TxHash, tr.LogIndex)
	if credited, err := l.Process(t.Context(), tr, 6); err != nil || !credited {
		t.Fatalf("first transfer not credited: %v %v", credited, err)
	}
	tr.TxHash = "0x" + strings.Repeat("AB", 32)
	code2, tradeNo2 := OrderIdentity(tr.Chain, tr.TxHash, tr.LogIndex)
	if code != code2 || tradeNo != tradeNo2 {
		t.Error("same EVM transaction produced a second fulfillment identity")
	}
	if credited, err := l.Process(t.Context(), tr, 6); err != nil || credited {
		t.Fatalf("same EVM transaction re-credited through different hash casing: %v %v", credited, err)
	}
	if creditor.calls != 1 {
		t.Fatalf("one transfer credited %d times", creditor.calls)
	}
}
