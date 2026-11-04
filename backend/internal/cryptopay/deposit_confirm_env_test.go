package cryptopay

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/enttest"
	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/ledger"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// countingProcessor wraps the real ledger so a test can prove how often the
// ledger was reached, and can stand in for outcomes that need PostgreSQL-only
// SQL (the ledger's ignore path uses NOW()).
type countingProcessor struct {
	inner   depositProcessor
	mu      sync.Mutex
	calls   int
	before  func()
	replace func(context.Context, scanner.Transfer, int) (bool, error)
}

func (p *countingProcessor) Process(ctx context.Context, tr scanner.Transfer, decimals int) (bool, error) {
	p.mu.Lock()
	p.calls++
	before, replace := p.before, p.replace
	p.mu.Unlock()
	if before != nil {
		before()
	}
	if replace != nil {
		return replace(ctx, tr, decimals)
	}
	return p.inner.Process(ctx, tr, decimals)
}

func (p *countingProcessor) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type memoryDepositLocker struct {
	mu   sync.Mutex
	held map[int64]bool
	err  error
}

func (l *memoryDepositLocker) TryLock(_ context.Context, id int64) (func() error, error) {
	if l.err != nil {
		return nil, l.err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held == nil {
		l.held = map[int64]bool{}
	}
	if l.held[id] {
		return nil, nil
	}
	l.held[id] = true
	return func() error {
		l.mu.Lock()
		defer l.mu.Unlock()
		delete(l.held, id)
		return nil
	}, nil
}

// grantAllLocker proves that exactly-once crediting does not depend on the lock.
type grantAllLocker struct{}

func (grantAllLocker) TryLock(context.Context, int64) (func() error, error) {
	return func() error { return nil }, nil
}

type confirmResolver struct {
	userID  int64
	deposit map[string]bool
	system  map[string]bool
}

func (r *confirmResolver) IsOwnAddress(_ context.Context, _, address string) (bool, error) {
	a := strings.ToLower(address)
	return r.system[a] || r.deposit[a], nil
}

func (r *confirmResolver) ResolveDeposit(_ context.Context, _, address string) (int64, bool, error) {
	if r.deposit[strings.ToLower(address)] {
		return r.userID, true, nil
	}
	return 0, false, nil
}

type depositSpec struct {
	chain, token, from, to string
	tx, blockHash          string
	index                  int
	amount                 string // NUMERIC text as PostgreSQL renders it
	block                  uint64
	status, reason         string
}

type confirmEnv struct {
	db        *sql.DB
	client    *dbent.Client
	userID    int64
	chain     *fakeChain
	resolver  *confirmResolver
	processor *countingProcessor
	locker    *memoryDepositLocker
	confirmer *depositConfirmer
}

func newConfirmEnv(t *testing.T) *confirmEnv {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	// One connection keeps the in-memory database alive and serialises
	// statements, like the existing creditor tests.
	db.SetMaxOpenConns(1)
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	t.Cleanup(func() { _ = client.Close() })
	_, err = db.Exec(`CREATE TABLE deposit_transactions (
		id INTEGER PRIMARY KEY AUTOINCREMENT, chain TEXT NOT NULL, tx_hash TEXT NOT NULL,
		log_index INTEGER NOT NULL DEFAULT 0, from_address TEXT NOT NULL, to_address TEXT NOT NULL,
		token TEXT NOT NULL, amount TEXT NOT NULL, block_number INTEGER NOT NULL,
		block_hash TEXT NOT NULL DEFAULT '', confirmations INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'pending', reason TEXT NOT NULL DEFAULT '', order_id INTEGER,
		last_attempt_at TEXT, UNIQUE(chain, tx_hash, log_index))`)
	require.NoError(t, err)
	user, err := client.User.Create().SetEmail("manual-confirm@example.invalid").SetPasswordHash("test-only").Save(t.Context())
	require.NoError(t, err)

	creditor := &DepositCreditor{entClient: client, db: db}
	// Models the real fulfillment lease: the PAID -> COMPLETED transition and the
	// balance credit commit together or not at all, and only one caller wins it.
	creditor.fulfill = func(ctx context.Context, id int64) error {
		tx, err := client.Tx(ctx)
		if err != nil {
			return err
		}
		order, err := tx.PaymentOrder.Get(ctx, id)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		won, err := tx.PaymentOrder.Update().
			Where(paymentorder.IDEQ(id), paymentorder.StatusEQ(payment.OrderStatusPaid)).
			SetStatus(payment.OrderStatusCompleted).Save(ctx)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if won == 1 {
			if _, err := tx.User.UpdateOneID(order.UserID).AddBalance(order.Amount).Save(ctx); err != nil {
				_ = tx.Rollback()
				return err
			}
		} else if order.Status != payment.OrderStatusCompleted {
			_ = tx.Rollback()
			return fmt.Errorf("order %d is %s", id, order.Status)
		}
		return tx.Commit()
	}

	resolver := &confirmResolver{userID: user.ID, deposit: map[string]bool{strings.ToLower(confirmTo): true}, system: map[string]bool{}}
	fc := newFakeChain(t)
	processor := &countingProcessor{inner: ledger.New(db, creditor, resolver, 0.001, 10000, 6.7)}
	locker := &memoryDepositLocker{}
	return &confirmEnv{db: db, client: client, userID: user.ID, chain: fc, resolver: resolver,
		processor: processor, locker: locker,
		confirmer: &depositConfirmer{db: db, processor: processor, locker: locker, adminID: 9,
			minCredit: 0.001, maxCredit: 10000,
			pool: func(chain string) (*rpc.Pool, error) {
				if chain != "bsc" {
					return nil, errDepositNoRPC
				}
				return fc.pool(), nil
			}}}
}

func (e *confirmEnv) spec(n int) depositSpec {
	return depositSpec{chain: "bsc", token: confirmUSDT, from: confirmFrom, to: confirmTo,
		tx: fmt.Sprintf("0x%064x", 0xabc000+n), blockHash: fmt.Sprintf("0x%064x", 0xdef000+n),
		index: 7, amount: "10.000000000000000000", block: uint64(1000 + n), status: "pending"}
}

func (e *confirmEnv) insert(t *testing.T, s depositSpec) int64 {
	t.Helper()
	res, err := e.db.ExecContext(t.Context(), `INSERT INTO deposit_transactions
		(chain, tx_hash, log_index, from_address, to_address, token, amount, block_number, block_hash, status, reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		s.chain, s.tx, s.index, s.from, s.to, s.token, s.amount, s.block, s.blockHash, s.status, s.reason)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

// publish makes the chain agree with a spec: a successful receipt carrying one
// Transfer log worth ten tokens, in a canonical block.
func (e *confirmEnv) publish(s depositSpec) {
	e.chain.mu.Lock()
	defer e.chain.mu.Unlock()
	e.chain.receipts[s.tx] = &chainReceipt{status: "0x1", block: s.block, blockHash: s.blockHash,
		logs: []map[string]any{chainTransferLog(s.token, s.from, s.to, tenUSDT, s.block, s.blockHash, s.tx, s.index)}}
	e.chain.canonical[s.block] = s.blockHash
}

func (e *confirmEnv) seed(t *testing.T, n int) (int64, depositSpec) {
	t.Helper()
	s := e.spec(n)
	e.publish(s)
	return e.insert(t, s), s
}

func (e *confirmEnv) confirm(id int64) DepositConfirmResult {
	return e.confirmer.confirmOne(context.Background(), map[string]depositPoolEntry{}, id)
}

func (e *confirmEnv) balance(t *testing.T) float64 {
	t.Helper()
	return e.client.User.GetX(t.Context(), e.userID).Balance
}

func (e *confirmEnv) orders(t *testing.T) int {
	t.Helper()
	return e.client.PaymentOrder.Query().CountX(t.Context())
}

// snapshot captures everything a rejected confirmation must leave untouched.
func (e *confirmEnv) snapshot(t *testing.T) string {
	t.Helper()
	rows, err := e.db.QueryContext(t.Context(), `SELECT id, status, reason, COALESCE(order_id, 0), tx_hash,
		log_index, block_number, block_hash, COALESCE(last_attempt_at, '') FROM deposit_transactions ORDER BY id`)
	require.NoError(t, err)
	var lines []string
	for rows.Next() {
		var id, order, block int64
		var index int
		var status, reason, tx, hash, attempt string
		require.NoError(t, rows.Scan(&id, &status, &reason, &order, &tx, &index, &block, &hash, &attempt))
		lines = append(lines, fmt.Sprintf("%d|%s|%s|%d|%s|%d|%d|%s|%s", id, status, reason, order, tx, index, block, hash, attempt))
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	return strings.Join(lines, "\n") + fmt.Sprintf("\nbalance=%v orders=%d", e.balance(t), e.orders(t))
}

func (e *confirmEnv) status(t *testing.T, id int64) string {
	t.Helper()
	var status string
	require.NoError(t, e.db.QueryRowContext(t.Context(), `SELECT status FROM deposit_transactions WHERE id = $1`, id).Scan(&status))
	return status
}
