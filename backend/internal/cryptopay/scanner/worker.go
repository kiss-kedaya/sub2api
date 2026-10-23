package scanner

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// Shared by every scanner instance; distinct from the sweeper's CRYPTOSW key.
const scanLockKey int64 = 0x43525950544f5343

// A lagging chain yields between bounded batches so other chains and other
// instances get regular turns. Durable progress always ends at a completed
// batch; the next pass starts at precisely the next unscanned block.
const maxScanBatchesPerChain = 4
const maxScanTimePerChain = 30 * time.Second

var ErrScanBusy = errors.New("another payment scan is running")

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
	ProcessPending(ctx context.Context, t Transfer, decimals int) error
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
	if decimals < 0 || decimals > 255 {
		return nil, fmt.Errorf("invalid token decimals %d", decimals)
	}
	parts := strings.SplitN(value, ".", 2)
	whole := parts[0]
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	if len(frac) > decimals {
		if strings.Trim(frac[decimals:], "0") != "" {
			return nil, fmt.Errorf("amount %q exceeds token precision", value)
		}
		frac = frac[:decimals]
	}
	for len(frac) < decimals {
		frac += "0"
	}
	raw, ok := new(big.Int).SetString(whole+frac, 10)
	if !ok || raw.Sign() < 0 {
		return nil, fmt.Errorf("invalid decimal %q", value)
	}
	return raw, nil
}

// Start begins the loop in the background.
func (w *Worker) Start() {
	if w == nil || w.cancel != nil || mode.ReadOnly() {
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
	w.scanAll(ctx)
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
	if err := w.RunOnce(ctx); err != nil && !errors.Is(err, ErrScanBusy) && !errors.Is(err, mode.ErrReadOnly) {
		slog.Warn("[cryptopay] scan pass failed", "error", err)
	}
}

// RunOnce executes the complete background pass, including pending confirmations
// and reorg checks, under the same shared session lock. Manual callers receive
// failures rather than a false completion response.
func (w *Worker) RunOnce(ctx context.Context) (err error) {
	return w.RunOnceFrom(ctx, 0)
}

// RunOnceFrom permits a bounded, explicitly authorized backfill for a single
// configured chain. Rewinding reuses the ledger's unique transfer identity.
func (w *Worker) RunOnceFrom(ctx context.Context, fromBlock uint64) (err error) {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	release, err := w.tryScanLock(ctx)
	if err != nil {
		return err
	}
	if release == nil {
		return ErrScanBusy
	}
	defer func() { err = errors.Join(err, release()) }()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if fromBlock != 0 {
		if len(w.chains) != 1 {
			return fmt.Errorf("historical scan requires exactly one configured chain")
		}
		chain := w.chains[0]
		pool := w.pools[chain.Chain]
		if pool == nil {
			return fmt.Errorf("historical scan has no RPC pool")
		}
		pool, verifyErr := verifiedChainPool(ctx, pool, chain.Chain)
		if verifyErr != nil {
			return verifyErr
		}
		head, headErr := pool.BlockNumber(ctx)
		if headErr != nil {
			return headErr
		}
		if head <= chain.Confirmations || fromBlock > head-chain.Confirmations || head-fromBlock > 100000 {
			return fmt.Errorf("historical scan must start within the last 100000 confirmed blocks")
		}
		last, loadErr := w.cursor.Load(ctx, chain.Chain)
		if loadErr != nil {
			return loadErr
		}
		// Never skip an earlier unfinished range, even when the caller supplies
		// a more recent block. A zero cursor is the only uninitialized case.
		if last == 0 || fromBlock-1 < last {
			if saveErr := w.cursor.Save(ctx, chain.Chain, fromBlock-1); saveErr != nil {
				return saveErr
			}
		}
	}
	if pendingErr := w.retryPending(ctx); pendingErr != nil {
		err = errors.Join(err, fmt.Errorf("pending retry: %w", pendingErr))
	}
	if recheckErr := w.recheckCredited(ctx); recheckErr != nil {
		err = errors.Join(err, fmt.Errorf("credited recheck: %w", recheckErr))
	}
	for _, chain := range w.chains {
		if scanErr := w.scanChainFrom(ctx, chain, fromBlock); scanErr != nil {
			err = errors.Join(err, fmt.Errorf("scan %s: %w", chain.Chain, scanErr))
		}
	}
	return err
}

// tryScanLock pins a dedicated session for the entire pass. Business queries
// still use w.db, so a bounded pool must leave at least one other connection.
// A nil release with no error means contention: the caller must skip all work.
func (w *Worker) tryScanLock(ctx context.Context) (func() error, error) {
	if w == nil || w.db == nil {
		return nil, fmt.Errorf("scanner database is not configured")
	}
	if w.db.Stats().MaxOpenConnections == 1 {
		return nil, fmt.Errorf("scanner requires at least two database connections (lock and scan queries)")
	}
	conn, err := w.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("scanner lock connection: %w", err)
	}
	// Raw with ErrBadConn closes the physical session rather than pooling it.
	// This also releases a lock acquired before a response was lost or invalid.
	discard := func() { _ = conn.Raw(func(any) error { return driver.ErrBadConn }) }
	var locked bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, scanLockKey).Scan(&locked); err != nil {
		discard()
		return nil, fmt.Errorf("acquire scanner lock: %w", err)
	}
	if !locked {
		return nil, conn.Close()
	}
	return func() error {
		// Stop cancels the scan context; unlocking must still reach PostgreSQL.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var unlocked bool
		err := conn.QueryRowContext(cleanup, `SELECT pg_advisory_unlock($1)`, scanLockKey).Scan(&unlocked)
		if err != nil || !unlocked {
			discard() // Never pool a session whose lock state is uncertain.
			if err == nil {
				err = fmt.Errorf("scanner advisory lock was not held")
			}
			return fmt.Errorf("release scanner lock: %w", err)
		}
		return conn.Close()
	}, nil
}

// ScanOnce runs one full pass; exposed for tests and manual triggering.
func (w *Worker) ScanOnce(ctx context.Context) (err error) {
	for _, chain := range w.chains {
		if scanErr := w.scanChain(ctx, chain); scanErr != nil {
			err = errors.Join(err, fmt.Errorf("scan %s: %w", chain.Chain, scanErr))
		}
	}
	return err
}

func (w *Worker) scanChain(ctx context.Context, chain ChainConfig) error {
	return w.scanChainFrom(ctx, chain, 0)
}

func (w *Worker) scanChainFrom(ctx context.Context, chain ChainConfig, manualFromBlock uint64) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, maxScanTimePerChain)
	defer cancel()
	pool := w.pools[chain.Chain]
	if pool == nil {
		slog.Debug("[cryptopay] scan skipped: no pool", "chain", chain.Chain)
		return nil
	}
	pool, err := verifiedChainPool(ctx, pool, chain.Chain)
	if err != nil {
		return err
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
	if manualFromBlock != 0 {
		// A manual start may rewind the cursor, but must never skip blocks
		// that have not yet been scanned. Keep the earlier durable boundary
		// until every intervening range has been processed successfully.
		if last == 0 || manualFromBlock-1 < last {
			last = manualFromBlock - 1
			if err := w.cursor.Save(ctx, chain.Chain, last); err != nil {
				return err
			}
		}
	} else if last == 0 {
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
	if maxRange == 0 {
		maxRange = 2000
	}
	var liveErr error
	if (confirmed-last-1)/maxRange >= maxScanBatchesPerChain {
		// Do not make a new payment wait behind hours of historical catch-up.
		// This overlapping live window never changes the historical cursor;
		// the ledger's transfer identity handles replay when catch-up arrives.
		span := min(uint64(evmFirstRunLookback), maxRange)
		if err := w.processScanRange(ctx, scanner, pool, chain, confirmed-span+1, confirmed, recipients); err != nil {
			liveErr = fmt.Errorf("live window %s: %w", chain.Chain, err)
		}
	}
	for from, batches := last+1, 0; from <= confirmed && batches < maxScanBatchesPerChain; batches++ {
		if err := ctx.Err(); err != nil {
			return errors.Join(liveErr, err)
		}
		to := from + min(maxRange-1, confirmed-from)
		if err := w.processScanRange(ctx, scanner, pool, chain, from, to, recipients); err != nil {
			return errors.Join(liveErr, err)
		}
		if err := w.cursor.Save(ctx, chain.Chain, to); err != nil {
			return errors.Join(liveErr, err)
		}
		from = to + 1
	}
	return liveErr
}

// The live window and historical batches deliberately share every validation
// and persistence check. Only the caller decides whether a successful range
// is contiguous with the durable cursor and can advance it.
func (w *Worker) processScanRange(ctx context.Context, scanner *EVMScanner, pool *rpc.Pool, chain ChainConfig, from, to uint64, recipients []string) error {
	transfers, err := scanner.ScanRange(ctx, chain.Chain, from, to, chain.Tokens, recipients)
	if err != nil {
		return err
	}
	for _, transfer := range transfers {
		// Ignore out-of-range results even if a faulty endpoint returns them.
		if transfer.BlockNumber < from || transfer.BlockNumber > to {
			continue
		}
		// Decimals come from the allowlist before either ledger path; a wrong
		// exponent could inflate an 18-decimal token by twelve orders.
		decimals, watched := watchedDecimals(chain, transfer.Token)
		if !watched {
			slog.Warn("[cryptopay] ignored transfer for unwatched token",
				"chain", transfer.Chain, "tx", transfer.TxHash, "token", transfer.Token)
			continue
		}
		receipt, verifyErr := VerifyTransaction(ctx, pool, transfer.TxHash)
		if verifyErr == nil {
			verifyErr = verifyCanonicalBlock(ctx, pool, receipt)
		}
		if verifyErr == nil {
			verifyErr = verifyReceiptTransfer(transfer, receipt)
		}
		final := false
		if verifyErr == nil {
			final, verifyErr = transactionIsFinalForChain(ctx, pool, chain.Chain, receipt.BlockNumber, chain.Confirmations)
		}
		if verifyErr != nil || !final {
			if err := w.handler.ProcessPending(ctx, transfer, decimals); err != nil {
				return fmt.Errorf("persist pending %s/%s/%d: %w", transfer.Chain, transfer.TxHash, transfer.LogIndex, err)
			}
			slog.Info("[cryptopay] deferred transfer", "chain", transfer.Chain, "tx", transfer.TxHash, "reason", verifyErr)
			continue
		}
		if _, err := w.handler.Process(ctx, transfer, decimals); err != nil {
			slog.Warn("[cryptopay] credit failed", "chain", transfer.Chain, "tx", transfer.TxHash, "error", err)
			// No caller can advance past a transfer not durably persisted.
			return err
		}
	}
	return nil
}
