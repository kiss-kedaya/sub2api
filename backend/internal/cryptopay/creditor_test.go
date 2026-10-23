package cryptopay

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/ledger"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func newCreditTest(t *testing.T) (*DepositCreditor, ledger.CreditRequest) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	t.Cleanup(func() { _ = client.Close() })
	_, err = db.Exec(`CREATE TABLE deposit_transactions (
		chain TEXT, tx_hash TEXT, log_index INTEGER, order_id INTEGER,
		from_address TEXT, to_address TEXT, token TEXT, amount NUMERIC,
		status TEXT, block_hash TEXT DEFAULT 'original-block', UNIQUE(chain, tx_hash, log_index))`)
	require.NoError(t, err)
	u, err := client.User.Create().SetEmail("credit@example.invalid").SetPasswordHash("test-only").Save(t.Context())
	require.NoError(t, err)
	req := ledger.CreditRequest{UserID: u.ID, Chain: "bsc", TxHash: "0xfixture", LogIndex: 2,
		BlockHash: "original-block", Token: "0xtoken", Amount: big.NewInt(10_000_000), Decimals: 6, CreditUSDT: 67,
		FromAddr: "0xfrom", ToAddr: "0xto"}
	_, err = db.Exec(`INSERT INTO deposit_transactions(chain,tx_hash,log_index,from_address,to_address,token,amount,status,block_hash)
		VALUES($1,$2,$3,$4,$5,$6,$7,'pending',$8)`, req.Chain, req.TxHash, req.LogIndex,
		req.FromAddr, req.ToAddr, req.Token, "10", req.BlockHash)
	require.NoError(t, err)
	c := &DepositCreditor{entClient: client, db: db}
	c.fulfill = func(ctx context.Context, id int64) error {
		o, e := client.PaymentOrder.Get(ctx, id)
		if e != nil || o.Status == payment.OrderStatusCompleted {
			return e
		}
		if _, e = client.User.UpdateOneID(o.UserID).AddBalance(o.Amount).Save(ctx); e != nil {
			return e
		}
		return client.PaymentOrder.UpdateOneID(id).SetStatus(payment.OrderStatusCompleted).Exec(ctx)
	}
	return c, req
}

// logIndex is block-global. The same transaction can acquire a different
// logIndex when mined again after a reorg; that must not mint a second balance.
func TestReminedTransactionWithDifferentLogIndexCannotCreditAgain(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    string
		clearLink bool
	}{
		{name: "reverted linked", status: "reverted"},
		{name: "reverted link lost", status: "reverted", clearLink: true},
		{name: "pending order link lost", status: "pending", clearLink: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, req := newCreditTest(t)
			require.NoError(t, c.CreditDeposit(t.Context(), req))
			_, err := c.db.ExecContext(t.Context(), `UPDATE deposit_transactions SET status=$1 WHERE log_index=$2`, tc.status, req.LogIndex)
			require.NoError(t, err) // retain the linked completed order across a reorg / failed status write
			if tc.clearLink {
				_, err = c.db.ExecContext(t.Context(), `UPDATE deposit_transactions SET order_id=NULL WHERE log_index=$1`, req.LogIndex)
				require.NoError(t, err) // order committed but order-link persistence was interrupted
			}
			before, err := c.entClient.User.Get(t.Context(), req.UserID)
			require.NoError(t, err)
			req.LogIndex++
			_, err = c.db.ExecContext(t.Context(), `INSERT INTO deposit_transactions
				(chain,tx_hash,log_index,from_address,to_address,token,amount,status,block_hash)
				VALUES($1,$2,$3,$4,$5,$6,$7,'pending','remined-block')`, req.Chain, req.TxHash, req.LogIndex,
				req.FromAddr, req.ToAddr, req.Token, "12")
			require.NoError(t, err)
			req.BlockHash = "remined-block"
			req.Amount = big.NewInt(12_000_000) // same tx hash, changed execution output after reorg
			require.ErrorIs(t, c.CreditDeposit(t.Context(), req), ledger.ErrReorgedDuplicateTransfer)
			after, err := c.entClient.User.Get(t.Context(), req.UserID)
			require.NoError(t, err)
			require.Equal(t, before.Balance, after.Balance, "one reminted transaction must not fulfill twice")
		})
	}
}

func TestMultipleTransferLogsInSameBlockCanEachCredit(t *testing.T) {
	c, req := newCreditTest(t)
	require.NoError(t, c.CreditDeposit(t.Context(), req))
	_, err := c.db.ExecContext(t.Context(), `UPDATE deposit_transactions SET status='credited' WHERE log_index=$1`, req.LogIndex)
	require.NoError(t, err)
	req.LogIndex++
	req.Amount = big.NewInt(20_000_000)
	req.CreditUSDT = 134
	_, err = c.db.ExecContext(t.Context(), `INSERT INTO deposit_transactions
		(chain,tx_hash,log_index,from_address,to_address,token,amount,status,block_hash)
		VALUES($1,$2,$3,$4,$5,$6,$7,'pending',$8)`, req.Chain, req.TxHash, req.LogIndex,
		req.FromAddr, req.ToAddr, req.Token, "20", req.BlockHash)
	require.NoError(t, err)
	require.NoError(t, c.CreditDeposit(t.Context(), req))
	require.Equal(t, 201.0, c.entClient.User.GetX(t.Context(), req.UserID).Balance)
}

func TestCreditResumesFailedOrderAndFreezesOriginalTerms(t *testing.T) {
	c, req := newCreditTest(t)
	fulfill := c.fulfill
	c.fulfill = func(context.Context, int64) error { return errors.New("temporary fulfillment outage") }
	require.ErrorContains(t, c.CreditDeposit(t.Context(), req), "temporary fulfillment")
	require.Equal(t, 1, c.entClient.PaymentOrder.Query().CountX(t.Context()))
	c.fulfill = fulfill
	// Rates changed during the outage. The already-created 67 balance order
	// must resume, rather than create another order or reprice it to 60.
	req.CreditUSDT = 60
	require.NoError(t, c.CreditDeposit(t.Context(), req))
	require.NoError(t, c.CreditDeposit(t.Context(), req))
	require.Equal(t, 1, c.entClient.PaymentOrder.Query().CountX(t.Context()))
	u, err := c.entClient.User.Get(t.Context(), req.UserID)
	require.NoError(t, err)
	require.Equal(t, 67.0, u.Balance)
	o := c.entClient.PaymentOrder.Query().OnlyX(t.Context())
	require.Equal(t, 10.0, o.PayAmount, "token payment and credited balance are different units")
	var linked int64
	require.NoError(t, c.db.QueryRow(`SELECT order_id FROM deposit_transactions`).Scan(&linked))
	require.Equal(t, o.ID, linked)
}

func TestCreditMustPersistOrderLinkBeforeFulfillment(t *testing.T) {
	c, req := newCreditTest(t)
	_, err := c.db.Exec(`CREATE TRIGGER reject_link BEFORE UPDATE OF order_id ON deposit_transactions
		BEGIN SELECT RAISE(FAIL, 'link unavailable'); END`)
	require.NoError(t, err)
	require.ErrorContains(t, c.CreditDeposit(t.Context(), req), "link order")
	u := c.entClient.User.GetX(t.Context(), req.UserID)
	require.Zero(t, u.Balance)
	_, err = c.db.Exec(`DROP TRIGGER reject_link`)
	require.NoError(t, err)
	require.NoError(t, c.CreditDeposit(t.Context(), req))
	require.Equal(t, 1, c.entClient.PaymentOrder.Query().CountX(t.Context()))
	require.Equal(t, 67.0, c.entClient.User.GetX(t.Context(), req.UserID).Balance)
}

func TestCreditRejectsMismatchedExistingIdentity(t *testing.T) {
	c, req := newCreditTest(t)
	require.NoError(t, c.CreditDeposit(t.Context(), req))
	req.Amount = big.NewInt(11_000_000)
	require.ErrorContains(t, c.CreditDeposit(t.Context(), req), "amount mismatch")
	req.Amount = big.NewInt(10_000_000)
	req.UserID++
	require.ErrorContains(t, c.CreditDeposit(t.Context(), req), "identity mismatch")
}
