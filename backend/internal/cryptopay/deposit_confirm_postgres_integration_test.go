//go:build integration

package cryptopay

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/stretchr/testify/require"
)

// These run only against the disposable cluster that isolatedSweepAlertPostgres
// insists on (CRYPTO_SWEEP_TEST_DSN, 127.0.0.1:15439). They cover what sqlite
// and sqlmock cannot: PostgreSQL's two-key advisory locks through lib/pq, and
// NUMERIC(38,18) rendering.

func applyDepositMigrations(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, name := range []string{"261_crypto_deposit.sql", "264_crypto_deposit_reason.sql", "265_crypto_deposit_retry_clock.sql"} {
		migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		require.NoError(t, err)
		_, err = db.ExecContext(t.Context(), string(migration))
		require.NoError(t, err)
	}
}

func TestPostgresDepositLockIsExclusivePerDepositAndSeparateFromTheScanLock(t *testing.T) {
	db := isolatedSweepAlertPostgres(t)
	locker := pgDepositLocker{db: db}

	first, err := locker.TryLock(t.Context(), 302)
	require.NoError(t, err)
	require.NotNil(t, first)

	// A second session asking for the same deposit is told it is busy.
	second, err := locker.TryLock(t.Context(), 302)
	require.NoError(t, err)
	require.Nil(t, second, "the same deposit cannot be confirmed twice at once")

	// A different deposit is independent.
	other, err := locker.TryLock(t.Context(), 321)
	require.NoError(t, err)
	require.NotNil(t, other)
	require.NoError(t, other())

	// The scanner's single-bigint lock lives in another key space, so a manual
	// confirmation never blocks or is blocked by a scan pass.
	conn, err := db.Conn(t.Context())
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	var scanLocked bool
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT pg_try_advisory_lock($1)`, int64(0x43525950544f5343)).Scan(&scanLocked))
	require.True(t, scanLocked)
	var scanUnlocked bool
	require.NoError(t, conn.QueryRowContext(t.Context(), `SELECT pg_advisory_unlock($1)`, int64(0x43525950544f5343)).Scan(&scanUnlocked))
	require.True(t, scanUnlocked)

	require.NoError(t, first())
	again, err := locker.TryLock(t.Context(), 302)
	require.NoError(t, err)
	require.NotNil(t, again, "the lock is free again once released")
	require.NoError(t, again())
}

type recordingProcessor struct {
	db       *sql.DB
	mu       sync.Mutex
	transfer scanner.Transfer
	decimals int
	calls    int
}

// Process stands in for the ledger, which needs payment_orders and users. The
// direct UPDATE exists only in this test double.
func (p *recordingProcessor) Process(ctx context.Context, tr scanner.Transfer, decimals int) (bool, error) {
	p.mu.Lock()
	p.transfer, p.decimals = tr, decimals
	p.calls++
	p.mu.Unlock()
	_, err := p.db.ExecContext(ctx, `UPDATE deposit_transactions SET status = 'credited'
		WHERE chain = $1 AND tx_hash = $2 AND log_index = $3 AND status = 'pending'`, tr.Chain, tr.TxHash, tr.LogIndex)
	return err == nil, err
}

func TestPostgresManualConfirmReadsNumericAmountAndHoldsTheRealLock(t *testing.T) {
	db := isolatedSweepAlertPostgres(t)
	applyDepositMigrations(t, db)
	chain := newFakeChain(t)
	processor := &recordingProcessor{db: db}
	confirmer := &depositConfirmer{db: db, processor: processor, locker: pgDepositLocker{db: db}, adminID: 9,
		minCredit: 0.001, maxCredit: 10000,
		pool: func(string) (*rpc.Pool, error) { return chain.pool(), nil }}

	tx := fmt.Sprintf("0x%064x", 0xab12cd34)
	blockHash := fmt.Sprintf("0x%064x", 0xef5600ab)
	const block = uint64(4242)
	var id int64
	// Integer-valued NUMERIC(38,18): PostgreSQL renders it as 10.000000000000000000.
	require.NoError(t, db.QueryRowContext(t.Context(), `INSERT INTO deposit_transactions
		(chain, tx_hash, log_index, from_address, to_address, token, amount, block_number, block_hash)
		VALUES ('bsc', $1, 7, $2, $3, $4, 10, $5, $6) RETURNING id`,
		tx, confirmFrom, confirmTo, confirmUSDT, block, blockHash).Scan(&id))
	var rendered string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT CAST(amount AS TEXT) FROM deposit_transactions WHERE id = $1`, id).Scan(&rendered))
	require.Equal(t, "10.000000000000000000", rendered)

	chain.receipts[tx] = &chainReceipt{status: "0x1", block: block, blockHash: blockHash,
		logs: []map[string]any{chainTransferLog(confirmUSDT, confirmFrom, confirmTo, tenUSDT, block, blockHash, tx, 7)}}
	chain.canonical[block] = blockHash

	// Hold the deposit's real advisory lock from another session: the
	// confirmation must report busy and leave the row alone.
	held, err := pgDepositLocker{db: db}.TryLock(t.Context(), id)
	require.NoError(t, err)
	require.NotNil(t, held)
	busy := confirmer.confirmOne(t.Context(), map[string]depositPoolEntry{}, id)
	require.Equal(t, DepositConfirmError, busy.Status)
	require.Contains(t, busy.Message, "正在被另一次确认处理")
	require.Zero(t, processor.calls)
	require.NoError(t, held())

	result := confirmer.confirmOne(t.Context(), map[string]depositPoolEntry{}, id)
	require.Equal(t, DepositConfirmCredited, result.Status, result.Message)
	require.Equal(t, 1, processor.calls)
	require.Equal(t, 0, processor.transfer.Amount.Cmp(tenUSDT), "10 tokens at 18 decimals, rebuilt from the NUMERIC text")
	require.Equal(t, 18, processor.decimals)
	require.Equal(t, uint64(4242), processor.transfer.BlockNumber)

	again := confirmer.confirmOne(t.Context(), map[string]depositPoolEntry{}, id)
	require.Equal(t, DepositConfirmAlreadyCredited, again.Status)
	require.Equal(t, 1, processor.calls)
}
