//go:build unit

package service

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/stretchr/testify/require"
)

type provisionalRevokeEnv struct {
	service *PaymentService
	db      *sql.DB
	client  *dbent.Client
	userID  int64
	orderID int64
	chain   string
	txHash  string
	logIdx  int
}

// newProvisionalRevokeEnv builds the minimum real schema: users and
// payment_orders from the ent migrations, plus the raw deposit_transactions
// ledger table this path touches.
func newProvisionalRevokeEnv(t *testing.T, balance, credited float64) *provisionalRevokeEnv {
	t.Helper()
	dsn := fmt.Sprintf("file:provisional_revoke_%d?mode=memory&cache=shared&_fk=1", time.Now().UnixNano())
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	t.Cleanup(func() { _ = client.Close() })
	_, err = db.Exec(`CREATE TABLE deposit_transactions (
		chain TEXT NOT NULL, tx_hash TEXT NOT NULL, log_index INTEGER NOT NULL,
		order_id INTEGER, status TEXT NOT NULL,
		provisional INTEGER NOT NULL DEFAULT 0,
		credited_at TIMESTAMP, revoked_at TIMESTAMP, revoke_reason TEXT,
		UNIQUE(chain, tx_hash, log_index))`)
	require.NoError(t, err)

	user, err := client.User.Create().
		SetEmail("provisional-revoke@example.invalid").
		SetPasswordHash("test-only").
		SetBalance(balance).
		Save(t.Context())
	require.NoError(t, err)
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName("fixture").
		SetAmount(credited).
		SetPayAmount(credited).
		SetFeeRate(0).
		SetRechargeCode("crypto-fixture").
		SetOutTradeNo("crypto-fixture-out").
		SetPaymentType("crypto").
		SetPaymentTradeNo("0xfixture").
		SetOrderType("balance").
		SetProviderKey("crypto").
		SetStatus("COMPLETED").
		SetPaidAt(time.Now()).
		SetExpiresAt(time.Now()).
		SetClientIP("crypto").
		SetSrcHost("crypto").
		Save(t.Context())
	require.NoError(t, err)

	env := &provisionalRevokeEnv{
		service: &PaymentService{entClient: client},
		db:      db, client: client, userID: user.ID, orderID: order.ID,
		chain: "bsc", txHash: "0xinstantcredit", logIdx: 7,
	}
	env.insertDeposit(t, order.ID, "credited", true)
	return env
}

func (e *provisionalRevokeEnv) insertDeposit(t *testing.T, orderID any, status string, provisional bool) {
	t.Helper()
	_, err := e.db.Exec(`INSERT INTO deposit_transactions
		(chain, tx_hash, log_index, order_id, status, provisional, credited_at)
		VALUES ($1,$2,$3,$4,$5,$6,CURRENT_TIMESTAMP)`,
		e.chain, e.txHash, e.logIdx, orderID, status, provisional)
	require.NoError(t, err)
}

func (e *provisionalRevokeEnv) balance(t *testing.T) float64 {
	t.Helper()
	user, err := e.client.User.Get(t.Context(), e.userID)
	require.NoError(t, err)
	return user.Balance
}

func (e *provisionalRevokeEnv) revoke() (*ProvisionalRevocationResult, error) {
	return e.service.RevokeProvisionalDepositCredit(context.Background(), ProvisionalDepositRevocation{
		Chain: e.chain, TxHash: e.txHash, LogIndex: e.logIdx, RevokeReason: "reorg_noncanonical_block",
	})
}

// The claw-back must deduct exactly the frozen credited amount once, persist the
// revocation on the deposit row, and write an audit trail.
func TestRevokeProvisionalDepositCreditDeductsExactlyOnce(t *testing.T) {
	env := newProvisionalRevokeEnv(t, 10, 6.7)

	first, err := env.revoke()
	require.NoError(t, err)
	require.True(t, first.Revoked)
	require.Equal(t, env.userID, first.UserID)
	require.InDelta(t, 6.7, first.Amount, 1e-9)
	require.True(t, first.BalanceAfterKnown)
	require.InDelta(t, 3.3, first.BalanceAfter, 1e-9)
	require.False(t, first.Negative)

	var status, reason string
	var revokedAt sql.NullTime
	var provisional bool
	require.NoError(t, env.db.QueryRow(`SELECT status, revoke_reason, revoked_at, provisional FROM deposit_transactions`).
		Scan(&status, &reason, &revokedAt, &provisional))
	require.Equal(t, "reverted", status)
	require.Equal(t, "reorg_noncanonical_block", reason)
	require.True(t, provisional, "the provisional marker stays for reconciliation")
	require.True(t, revokedAt.Valid)

	var audits int
	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM payment_audit_logs WHERE action = 'DEPOSIT_PROVISIONAL_REVOKED'`).Scan(&audits))
	require.Equal(t, 1, audits)

	// Second attempt: same identity, same pass, another instance. No money moves.
	second, err := env.revoke()
	require.NoError(t, err)
	require.False(t, second.Revoked)
	require.InDelta(t, 3.3, env.balance(t), 1e-9)
	require.NoError(t, env.db.QueryRow(`SELECT COUNT(*) FROM payment_audit_logs WHERE action = 'DEPOSIT_PROVISIONAL_REVOKED'`).Scan(&audits))
	require.Equal(t, 1, audits)
}

// A user who already spent the provisional credit may go negative: the money
// was paid out, so the deficit is recorded instead of silently ignored.
func TestRevokeProvisionalDepositCreditAllowsNegativeBalance(t *testing.T) {
	env := newProvisionalRevokeEnv(t, 2, 6.7)

	result, err := env.revoke()
	require.NoError(t, err)
	require.True(t, result.Revoked)
	require.True(t, result.Negative)
	require.True(t, result.BalanceAfterKnown)
	require.InDelta(t, -4.7, result.BalanceAfter, 1e-9)
	require.InDelta(t, -4.7, env.balance(t), 1e-9)
}

// A confirmation-gated (non-provisional) reorg must keep the manual path: the
// automatic claw-back never touches it.
func TestRevokeProvisionalDepositCreditIgnoresFinalCredits(t *testing.T) {
	env := newProvisionalRevokeEnv(t, 10, 6.7)
	_, err := env.db.Exec(`UPDATE deposit_transactions SET provisional = 0`)
	require.NoError(t, err)

	result, err := env.revoke()
	require.NoError(t, err)
	require.False(t, result.Revoked)
	require.InDelta(t, 10, env.balance(t), 1e-9)
	var status string
	require.NoError(t, env.db.QueryRow(`SELECT status FROM deposit_transactions`).Scan(&status))
	require.Equal(t, "credited", status)
}

// Without a frozen order there is no trustworthy amount to deduct; the claim is
// rolled back and the deposit stays for manual reconciliation.
func TestRevokeProvisionalDepositCreditWithoutFrozenOrderIsManual(t *testing.T) {
	env := newProvisionalRevokeEnv(t, 10, 6.7)
	_, err := env.db.Exec(`UPDATE deposit_transactions SET order_id = NULL`)
	require.NoError(t, err)

	result, err := env.revoke()
	require.NoError(t, err)
	require.False(t, result.Revoked)
	require.InDelta(t, 10, env.balance(t), 1e-9)
	var status string
	var revokedAt sql.NullTime
	require.NoError(t, env.db.QueryRow(`SELECT status, revoked_at FROM deposit_transactions`).Scan(&status, &revokedAt))
	require.Equal(t, "credited", status)
	require.False(t, revokedAt.Valid, "a declined claw-back must not leave a revoked marker")
}
