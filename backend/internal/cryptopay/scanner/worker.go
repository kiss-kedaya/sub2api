package scanner

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// ChainConfig describes one chain's scan parameters.
type ChainConfig struct {
	Chain         string
	Tokens        []string // contract addresses to watch
	Confirmations uint64
	Decimals      map[string]int // token contract -> decimals
	// MaxRange caps the block span of one eth_getLogs call. Providers differ
	// wildly (public X Layer caps at 100 blocks, others allow thousands), so
	// a chain that needs smaller chunks must be able to say so. 0 = use the
	// worker default.
	MaxRange uint64
}

// DepositHandler receives observed transfers (the ledger).
type DepositHandler interface {
	Process(ctx context.Context, t Transfer, decimals int) (bool, error)
	// ProcessPending records a not-yet-confirmed transfer so a later pass
	// can credit it after finality. Must be idempotent.
	ProcessPending(ctx context.Context, t Transfer) error
}

// Worker scans all configured EVM chains on an interval.
type Worker struct {
	db        *sql.DB
	pools     map[string]*rpc.Pool
	chains    []ChainConfig
	cursor    *CursorStore
	handler   DepositHandler
	addresses func(ctx context.Context, chain string) ([]string, error)
	interval  time.Duration
	maxRange  uint64
	cancel    context.CancelFunc
	done      chan struct{}
	// onReorg reports a credited deposit whose block was reorged away. The
	// money already moved in our ledger, so a human must reconcile it.
	onReorg func(chain, txHash string)
}

// SetReorgNotifier wires the alert for reorged credited deposits.
func (w *Worker) SetReorgNotifier(fn func(chain, txHash string)) {
	if w != nil {
		w.onReorg = fn
	}
}

// NewWorker builds the scan loop. addresses returns the watched recipient
// list per chain (cached by the caller).
func NewWorker(
	db *sql.DB,
	pools map[string]*rpc.Pool,
	chains []ChainConfig,
	cursor *CursorStore,
	handler DepositHandler,
	addresses func(ctx context.Context, chain string) ([]string, error),
	interval time.Duration,
	maxRange uint64,
) *Worker {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	if maxRange == 0 {
		maxRange = 2000
	}
	return &Worker{
		db:    db,
		pools: pools, chains: chains, cursor: cursor, handler: handler,
		addresses: addresses, interval: interval, maxRange: maxRange,
		done: make(chan struct{}),
	}
}

// decimalToRawUnits converts a NUMERIC string into raw token units.
func decimalToRawUnits(value string, decimals int) (*big.Int, error) {
	value = strings.TrimSpace(value)
	parts := strings.SplitN(value, ".", 2)
	whole := parts[0]
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if len(frac) > decimals {
		frac = frac[:decimals]
	}
	for len(frac) < decimals {
		frac += "0"
	}
	raw, ok := new(big.Int).SetString(whole+frac, 10)
	if !ok {
		return nil, fmt.Errorf("invalid decimal %q", value)
	}
	return raw, nil
}

// Start begins the loop in the background.
func (w *Worker) Start() {
	if w == nil || w.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	go w.run(ctx)
}

// Stop halts the loop and waits for it to finish.
func (w *Worker) Stop() {
	if w == nil || w.cancel == nil {
		return
	}
	w.cancel()
	<-w.done
}

func (w *Worker) run(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.scanAll(ctx)
		}
	}
}

func (w *Worker) scanAll(ctx context.Context) {
	// Retry deposits recorded before finality: their confirmations may have
	// landed since the last pass.
	if err := w.retryPending(ctx); err != nil {
		slog.Warn("[cryptopay] pending retry failed", "error", err)
	}
	// Re-verify recently credited deposits: a deep reorg can erase a block we
	// already paid out on, and nothing else would notice.
	if err := w.recheckCredited(ctx); err != nil {
		slog.Warn("[cryptopay] credited recheck failed", "error", err)
	}
	for _, chain := range w.chains {
		if err := w.scanChain(ctx, chain); err != nil {
			slog.Warn("[cryptopay] scan failed", "chain", chain.Chain, "error", err)
		}
	}
}

// recheckCredited re-verifies deposits credited recently enough that a reorg
// could still undo them. The window is bounded (one hour of blocks) so the
// pass stays cheap; anything older is settled by the chain's finality.
//
// A credited deposit whose transaction no longer exists, or whose block hash
// changed, is marked "reverted" so reconciliation can claw it back. It is
// never deleted: the money already moved in our ledger.
func (w *Worker) recheckCredited(ctx context.Context) error {
	rows, err := w.db.QueryContext(ctx, `
		SELECT chain, tx_hash, log_index, block_hash
		  FROM deposit_transactions
		 WHERE status = 'credited'
		   AND created_at > NOW() - INTERVAL '1 hour'
		 ORDER BY id DESC
		 LIMIT 200`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	type creditedRow struct {
		chain, txHash, blockHash string
		logIndex                 int
	}
	var pending []creditedRow
	for rows.Next() {
		var r creditedRow
		if err := rows.Scan(&r.chain, &r.txHash, &r.logIndex, &r.blockHash); err != nil {
			return err
		}
		pending = append(pending, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, p := range pending {
		pool := w.pools[p.chain]
		if pool == nil || p.blockHash == "" {
			continue
		}
		receipt, err := VerifyTransaction(ctx, pool, p.txHash)
		if err != nil {
			// No receipt can mean a reorg dropped the tx; confirm on the next
			// pass before flagging, since RPC errors are also common here.
			continue
		}
		if receipt.BlockHash == "" || receipt.BlockHash == p.blockHash {
			continue
		}
		slog.Warn("[cryptopay] credited deposit was reorged",
			"chain", p.chain, "tx", p.txHash, "was", p.blockHash, "now", receipt.BlockHash)
		if _, err := w.db.ExecContext(ctx,
			`UPDATE deposit_transactions SET status = 'reverted' WHERE chain = $1 AND tx_hash = $2 AND log_index = $3`,
			p.chain, p.txHash, p.logIndex); err != nil {
			slog.Warn("[cryptopay] mark reverted failed", "tx", p.txHash, "error", err)
			continue
		}
		if w.onReorg != nil {
			w.onReorg(p.chain, p.txHash)
		}
	}
	return nil
}

// retryPending re-verifies deposits still in "pending" state and credits the
// ones that are now final. Bounded per pass so one chain cannot monopolise
// the loop.
func (w *Worker) retryPending(ctx context.Context) error {
	rows, err := w.db.QueryContext(ctx, `
		SELECT chain, tx_hash, log_index, from_address, to_address, token,
		       amount::text, block_number, block_hash
		  FROM deposit_transactions
		 WHERE status = 'pending'
		 ORDER BY id
		 LIMIT 50`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	type pendingRow struct {
		chain, txHash, from, to, token, amount, blockHash string
		logIndex                                          int
		blockNumber                                       uint64
	}
	var pending []pendingRow
	for rows.Next() {
		var p pendingRow
		var amount string
		if err := rows.Scan(&p.chain, &p.txHash, &p.logIndex, &p.from, &p.to,
			&p.token, &amount, &p.blockNumber, &p.blockHash); err != nil {
			return err
		}
		p.amount = amount
		pending = append(pending, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range pending {
		pool := w.pools[p.chain]
		if pool == nil {
			continue
		}
		receipt, err := VerifyTransaction(ctx, pool, p.txHash)
		if err != nil {
			continue // still not final; next pass
		}
		if final, err := BlockIsFinal(ctx, pool, receipt.BlockNumber); err == nil && !final {
			continue
		}
		raw, err := decimalToRawUnits(p.amount, 6)
		if err != nil {
			slog.Warn("[cryptopay] pending amount decode failed", "tx", p.txHash, "error", err)
			continue
		}
		transfer := Transfer{
			Chain: p.chain, TxHash: p.txHash, LogIndex: p.logIndex,
			From: p.from, To: p.to, Token: p.token, Amount: raw,
			BlockNumber: p.blockNumber, BlockHash: p.blockHash,
		}
		if _, err := w.handler.Process(ctx, transfer, 6); err != nil {
			slog.Warn("[cryptopay] pending credit failed", "tx", p.txHash, "error", err)
		}
	}
	return nil
}

// ScanOnce runs one full pass; exposed for tests and manual triggering.
func (w *Worker) ScanOnce(ctx context.Context) error {
	for _, chain := range w.chains {
		if err := w.scanChain(ctx, chain); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) scanChain(ctx context.Context, chain ChainConfig) error {
	pool := w.pools[chain.Chain]
	if pool == nil {
		slog.Debug("[cryptopay] scan skipped: no pool", "chain", chain.Chain)
		return nil
	}
	head, err := pool.BlockNumber(ctx)
	if err != nil {
		return err
	}
	slog.Info("[cryptopay] scan tick", "chain", chain.Chain, "head", head, "confirmations", chain.Confirmations)
	if head <= chain.Confirmations {
		return nil
	}
	confirmed := head - chain.Confirmations

	last, err := w.cursor.Load(ctx, chain.Chain)
	if err != nil {
		slog.Warn("[cryptopay] cursor load failed", "chain", chain.Chain, "error", err)
		return err
	}
	if last == 0 {
		// First run: start from a small lookback window instead of the exact
		// head. Starting AT the head leaves nothing to scan, so the cursor
		// never advances and deposits landing right after boot are missed.
		if confirmed > evmFirstRunLookback {
			last = confirmed - evmFirstRunLookback
		} else {
			last = 0
		}
		if err := w.cursor.Save(ctx, chain.Chain, last); err != nil {
			return err
		}
	}
	if last >= confirmed {
		return nil
	}

	recipients, err := w.addresses(ctx, chain.Chain)
	if err != nil {
		return err
	}
	if len(recipients) == 0 {
		// Nothing to watch: advance the cursor so we do not rescan forever.
		return w.cursor.Save(ctx, chain.Chain, confirmed)
	}

	scanner := NewEVMScanner(pool)
	// Per-chain cap: public providers enforce very different getLogs spans
	// (X Layer rejects anything over 100 blocks), so the chunk size must be
	// configurable per chain instead of one global constant.
	maxRange := w.maxRange
	if chain.MaxRange > 0 {
		maxRange = chain.MaxRange
	}
	for from := last + 1; from <= confirmed; {
		to := from + maxRange - 1
		if to > confirmed {
			to = confirmed
		}
		transfers, err := scanner.ScanRange(ctx, chain.Chain, from, to, chain.Tokens, recipients)
		if err != nil {
			return err
		}
		for _, transfer := range transfers {
			// Confirm before crediting: a broadcast-but-reverted or
			// not-yet-final transaction must never move money. Reorgs are
			// the reason this runs per transfer instead of trusting the
			// confirmation counter alone.
			receipt, err := VerifyTransaction(ctx, pool, transfer.TxHash)
			if err != nil {
				// Not final yet: remember it so a later pass credits it.
				// Without this the cursor advance would drop the deposit.
				if pendingErr := w.handler.ProcessPending(ctx, transfer); pendingErr != nil {
					slog.Warn("[cryptopay] record pending failed",
						"chain", transfer.Chain, "tx", transfer.TxHash, "error", pendingErr)
				}
				slog.Info("[cryptopay] deferred until confirmed",
					"chain", transfer.Chain, "tx", transfer.TxHash, "reason", err)
				continue
			}
			if receipt.BlockHash != "" && transfer.BlockHash != "" && receipt.BlockHash != transfer.BlockHash {
				slog.Warn("[cryptopay] block hash mismatch, skipping",
					"chain", transfer.Chain, "tx", transfer.TxHash)
				continue
			}
			if final, err := BlockIsFinal(ctx, pool, receipt.BlockNumber); err == nil && !final {
				// Chain exposes the safe tag and this block is above it:
				// wait for the next pass rather than crediting early.
				if pendingErr := w.handler.ProcessPending(ctx, transfer); pendingErr != nil {
					slog.Warn("[cryptopay] record pending failed",
						"chain", transfer.Chain, "tx", transfer.TxHash, "error", pendingErr)
				}
				slog.Info("[cryptopay] waiting for finality",
					"chain", transfer.Chain, "tx", transfer.TxHash, "block", receipt.BlockNumber)
				continue
			}
			decimals := chain.Decimals[transfer.Token]
			if decimals == 0 {
				decimals = 6 // USDT default on the EVM chains we support
			}
			if _, err := w.handler.Process(ctx, transfer, decimals); err != nil {
				slog.Warn("[cryptopay] credit failed", "chain", transfer.Chain, "tx", transfer.TxHash, "error", err)
			}
		}
		if err := w.cursor.Save(ctx, chain.Chain, to); err != nil {
			return err
		}
		from = to + 1
	}
	return nil
}
