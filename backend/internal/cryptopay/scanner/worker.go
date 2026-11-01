package scanner

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sort"
	"strings"
	"sync"
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
	onReorg       func(chain, txHash string)
	beforePass    func(context.Context) error
	securityRange func(context.Context, *EVMScanner, *rpc.Pool, ChainConfig, uint64, uint64) error
	// Live workers replay a bounded recent range to recover late RPC log
	// indexing and recipient assignments racing the ordinary cursor snapshot.
	recentReplay bool
	replayMu     sync.Mutex
	replayedAt   map[string]time.Time
	failureAt    time.Time
	// Per-chain report limiter: one slow chain must not hide another's errors.
	chainFailureAt map[string]time.Time
	// liveProgress enables the head observation window and the confirmation
	// counter writes. Production workers (positive interval) turn it on.
	liveProgress bool
	// coverage is the per-pass record of security-scanned blocks. Pending
	// credit is allowed only for blocks this pass proved were security-scanned.
	coverage map[string]*chainCoverage
}

// chainCoverage records, for one chain and one pass, the durable cursor seen
// (every block at or below it already passed securityRange) and the ranges
// whose securityRange call succeeded during this pass.
type chainCoverage struct {
	cursor    uint64
	hasCursor bool
	ranges    [][2]uint64
	head      uint64
	hasHead   bool
}

func (w *Worker) chainCoverageLocked(chain string) *chainCoverage {
	if w.coverage == nil {
		w.coverage = make(map[string]*chainCoverage)
	}
	c := w.coverage[chain]
	if c == nil {
		c = &chainCoverage{}
		w.coverage[chain] = c
	}
	return c
}

func (w *Worker) markSecurityCovered(chain string, from, to uint64) {
	w.replayMu.Lock()
	defer w.replayMu.Unlock()
	c := w.chainCoverageLocked(chain)
	c.ranges = append(c.ranges, [2]uint64{from, to})
}

func (w *Worker) noteCursor(chain string, cursor uint64) {
	w.replayMu.Lock()
	defer w.replayMu.Unlock()
	c := w.chainCoverageLocked(chain)
	c.cursor, c.hasCursor = cursor, true
}

func (w *Worker) noteHead(chain string, head uint64) {
	w.replayMu.Lock()
	defer w.replayMu.Unlock()
	c := w.chainCoverageLocked(chain)
	c.head, c.hasHead = head, true
}

func (w *Worker) passHead(chain string) (uint64, bool) {
	w.replayMu.Lock()
	defer w.replayMu.Unlock()
	if c := w.coverage[chain]; c != nil && c.hasHead {
		return c.head, true
	}
	return 0, false
}

// securityCovered reports whether block has passed the security range scan:
// it is at or below the durable cursor, or inside a range this pass scanned
// successfully. Head observation records pending rows above the cursor before
// any security scan reached them; those must wait here. Without a security
// hook nothing is gated.
func (w *Worker) securityCovered(chain string, block uint64) bool {
	if w.securityRange == nil {
		return true
	}
	w.replayMu.Lock()
	defer w.replayMu.Unlock()
	c := w.coverage[chain]
	if c == nil {
		return false
	}
	if c.hasCursor && block <= c.cursor {
		return true
	}
	for _, r := range c.ranges {
		if block >= r[0] && block <= r[1] {
			return true
		}
	}
	return false
}

// confirmationsAt is the user-facing counter: the inclusion block counts as
// one, capped at the chain's requirement. A lagging head reads as zero.
func confirmationsAt(head, block, required uint64) uint64 {
	if head < block {
		return 0
	}
	return min(head-block+1, required)
}

// updatePendingConfirmations only touches a pending row and only when the
// value changed. It is display state: no balance or status depends on it.
func (w *Worker) updatePendingConfirmations(ctx context.Context, chain, txHash string, logIndex int, confirmations uint64) error {
	_, err := w.db.ExecContext(ctx, `UPDATE deposit_transactions SET confirmations = $1
		WHERE chain = $2 AND tx_hash = $3 AND log_index = $4 AND status = 'pending' AND confirmations <> $1`,
		int64(confirmations), chain, strings.ToLower(txHash), logIndex)
	if err != nil {
		return fmt.Errorf("update pending confirmations: %w", err)
	}
	return nil
}

// markCreditedConfirmations writes the chain's requirement once a row is
// credited, so history shows a full counter instead of the stale pending one.
func (w *Worker) markCreditedConfirmations(ctx context.Context, chain ChainConfig, t Transfer) {
	if !w.liveProgress {
		return
	}
	if _, err := w.db.ExecContext(ctx, `UPDATE deposit_transactions SET confirmations = $1
		WHERE chain = $2 AND tx_hash = $3 AND log_index = $4 AND status = 'credited' AND confirmations <> $1`,
		int64(chain.Confirmations), t.Chain, strings.ToLower(t.TxHash), t.LogIndex); err != nil {
		slog.Warn("[cryptopay] credited confirmations not recorded", "chain", t.Chain, "tx", t.TxHash, "cause", rpc.SafeError(err))
	}
}

// scanStageError attributes a per-chain failure to the scan step that produced
// it (security, head, deposit, live or replay). Error() is the unchanged inner
// message, so callers and stored errors see exactly what they saw before.
type scanStageError struct {
	chain    string
	stage    string
	from, to uint64
	err      error
}

func (e *scanStageError) Error() string { return e.err.Error() }
func (e *scanStageError) Unwrap() error { return e.err }

// stageErr tags one error unless an inner step already identified itself.
func stageErr(chain, stage string, from, to uint64, err error) error {
	if err == nil {
		return nil
	}
	var tagged *scanStageError
	if errors.As(err, &tagged) {
		return err
	}
	return &scanStageError{chain: chain, stage: stage, from: from, to: to, err: err}
}

// tagStage labels every untagged leaf of a joined error; tagged parts keep
// their own stage. The rebuilt join has the same message and error chain.
func tagStage(chain, stage string, err error) error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		parts := joined.Unwrap()
		tagged := make([]error, 0, len(parts))
		for _, part := range parts {
			tagged = append(tagged, tagStage(chain, stage, part))
		}
		return errors.Join(tagged...)
	}
	return stageErr(chain, stage, 0, 0, err)
}

// logAttrs exposes only the chain name, block numbers, batch counters and the
// rpc.Failure category with numeric statuses. RPC URLs, node error text and
// keys never reach the log; the cause string is rpc.SafeError.
func (e *scanStageError) logAttrs() []any {
	attrs := []any{"chain", e.chain, "stage", e.stage}
	if e.from != 0 || e.to != 0 {
		attrs = append(attrs, "from", e.from, "to", e.to)
	}
	var logsErr *LogsError
	if errors.As(e.err, &logsErr) {
		attrs = append(attrs, "query_from", logsErr.FromBlock, "query_to", logsErr.ToBlock,
			"batch", logsErr.Batch, "addresses", logsErr.Addresses)
	}
	var failure *rpc.Failure
	switch {
	case errors.As(e.err, &failure):
		attrs = append(attrs, "kind", failure.Kind)
		if failure.HTTPStatus != 0 {
			attrs = append(attrs, "http_status", failure.HTTPStatus)
		}
		if failure.RPCCode != 0 {
			attrs = append(attrs, "rpc_code", failure.RPCCode)
		}
		if errors.Is(failure, context.DeadlineExceeded) {
			attrs = append(attrs, "timeout", true)
		}
	case errors.Is(e.err, context.DeadlineExceeded):
		attrs = append(attrs, "kind", "timeout")
	case errors.Is(e.err, context.Canceled):
		attrs = append(attrs, "kind", "canceled")
	case errors.Is(e.err, rpc.ErrIndependentEvidence):
		attrs = append(attrs, "kind", "evidence")
	default:
		attrs = append(attrs, "kind", "other")
	}
	return append(attrs, "cause", rpc.SafeError(e.err))
}

// collectScanFailures splits a pass error into tagged per-chain failures and
// everything else (pending retry, recheck, lock), keeping each subtree whole.
func collectScanFailures(err error, byChain map[string][]*scanStageError, other *[]error) {
	switch e := err.(type) {
	case nil:
		return
	case *scanStageError:
		byChain[e.chain] = append(byChain[e.chain], e)
		return
	case interface{ Unwrap() []error }:
		for _, part := range e.Unwrap() {
			collectScanFailures(part, byChain, other)
		}
		return
	}
	var tagged *scanStageError
	if !errors.As(err, &tagged) {
		*other = append(*other, err)
		return
	}
	collectScanFailures(errors.Unwrap(err), byChain, other)
}

// Security hooks precede pending credit as well as every live/backfill range.
// They return failures before a cursor can move past unchecked outflows.
func (w *Worker) SetSecurityGuards(before func(context.Context) error, scan func(context.Context, *EVMScanner, *rpc.Pool, ChainConfig, uint64, uint64) error) {
	w.beforePass, w.securityRange = before, scan
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
	recentReplay := interval > 0
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
		done:         make(chan struct{}),
		recentReplay: recentReplay,
		liveProgress: recentReplay,
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
	w.recentReplay = true
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
	if w == nil {
		return
	}
	if err := w.RunOnce(ctx); err != nil && !errors.Is(err, ErrScanBusy) && !errors.Is(err, mode.ErrReadOnly) && ctx.Err() == nil {
		w.reportScanFailure(err)
	}
}

// reportScanFailure keeps failures visible at error-only production logging.
// Each chain has its own one-minute limiter and names the failing stage, so a
// stuck BSC cursor is distinguishable from a healthy ETH pass.
func (w *Worker) reportScanFailure(err error) {
	byChain := make(map[string][]*scanStageError)
	var other []error
	collectScanFailures(err, byChain, &other)
	now := time.Now()
	w.replayMu.Lock()
	if w.chainFailureAt == nil {
		w.chainFailureAt = make(map[string]time.Time)
	}
	reportChains := make([]string, 0, len(byChain))
	for chain := range byChain {
		if now.Sub(w.chainFailureAt[chain]) >= time.Minute {
			w.chainFailureAt[chain] = now
			reportChains = append(reportChains, chain)
		}
	}
	reportOther := len(other) > 0 && now.Sub(w.failureAt) >= time.Minute
	if reportOther {
		w.failureAt = now
	}
	w.replayMu.Unlock()
	sort.Strings(reportChains)
	for _, chain := range reportChains {
		for _, failure := range byChain[chain] {
			slog.Error("[cryptopay] payment scan incomplete; retrying without skipping blocks", failure.logAttrs()...)
		}
	}
	if reportOther {
		slog.Error("[cryptopay] payment scan incomplete; retrying without skipping blocks", "cause", rpc.SafeError(errors.Join(other...)))
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
	if w != nil && w.beforePass != nil {
		if err := w.beforePass(ctx); err != nil {
			return err
		}
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
	// Coverage is per pass: pending credit below trusts only what this pass
	// saw (durable cursor, successful security ranges, current head).
	w.replayMu.Lock()
	w.coverage = make(map[string]*chainCoverage)
	w.replayMu.Unlock()
	// Discover new payments before rotating historical receipt/reorg checks.
	// Maintenance has its own bounded budget and must not delay fresh deposits.
	for _, chain := range w.chains {
		if scanErr := w.scanChainFrom(ctx, chain, fromBlock); scanErr != nil {
			err = errors.Join(err, fmt.Errorf("scan %s: %w", chain.Chain, scanErr))
		}
	}
	if pendingErr := w.retryPending(ctx); pendingErr != nil {
		err = errors.Join(err, fmt.Errorf("pending retry: %w", pendingErr))
	}
	if recheckErr := w.recheckCredited(ctx); recheckErr != nil {
		err = errors.Join(err, fmt.Errorf("credited recheck: %w", recheckErr))
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
	if w.beforePass != nil {
		if err := w.beforePass(ctx); err != nil {
			return err
		}
	}
	for _, chain := range w.chains {
		if scanErr := w.scanChain(ctx, chain); scanErr != nil {
			err = errors.Join(err, fmt.Errorf("scan %s: %w", chain.Chain, scanErr))
		}
	}
	return err
}

// RunGuardScan reuses the global scanner lock but never retries or credits
// pending deposits. A caller with no recipients and a security range hook can
// keep watching existing wallet funds while the recharge channel is disabled.
func (w *Worker) RunGuardScan(ctx context.Context) (err error) {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	if w == nil || w.handler != nil || w.securityRange == nil {
		return errors.New("security scan requires only a security range guard and no deposit handler")
	}
	release, err := w.tryScanLock(ctx)
	if err != nil {
		return err
	}
	if release == nil {
		return ErrScanBusy
	}
	defer func() { err = errors.Join(err, release()) }()
	return w.ScanOnce(ctx)
}

func (w *Worker) scanChain(ctx context.Context, chain ChainConfig) error {
	return w.scanChainFrom(ctx, chain, 0)
}

func (w *Worker) scanChainFrom(ctx context.Context, chain ChainConfig, manualFromBlock uint64) (scanErr error) {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	// Registered first so it runs last: every failure of this chain carries a
	// stage for the per-chain report; cursor/address steps default to deposit.
	defer func() { scanErr = tagStage(chain.Chain, "deposit", scanErr) }()
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, maxScanTimePerChain)
	defer cancel()
	pool := w.pools[chain.Chain]
	if pool == nil {
		slog.Debug("[cryptopay] scan skipped: no pool", "chain", chain.Chain)
		return nil
	}
	pool, err := verifiedChainPool(ctx, pool, chain.Chain)
	if err != nil {
		return stageErr(chain.Chain, "head", 0, 0, err)
	}
	head, err := pool.BlockNumber(ctx)
	if err != nil {
		return stageErr(chain.Chain, "head", 0, 0, err)
	}
	slog.Info("[cryptopay] scan tick", "chain", chain.Chain, "head", head, "confirmations", chain.Confirmations)
	w.noteHead(chain.Chain, head)
	if w.securityRange != nil && head > 0 {
		// Security incidents do not wait for deposit finality. Examine a
		// bounded head window even when the credited-deposit cursor is caught
		// up or confirmations intentionally lag the latest mined block.
		span := uint64(evmFirstRunLookback)
		if chain.MaxRange > 0 {
			span = min(span, chain.MaxRange)
		} else if w.maxRange > 0 {
			span = min(span, w.maxRange)
		}
		span = min(span, head)
		if err := w.securityRange(ctx, NewEVMScanner(pool), pool, chain, head-span+1, head); err != nil {
			// This head window is an early warning only. A slow public node
			// failing it must not freeze deposit discovery: every deposit range
			// below still runs securityRange itself before credit or cursor
			// progress. The failure stays in the result for manual callers.
			headErr := stageErr(chain.Chain, "security", head-span+1, head, err)
			defer func() { scanErr = errors.Join(headErr, scanErr) }()
		} else {
			w.markSecurityCovered(chain.Chain, head-span+1, head)
		}
	}
	if w.liveProgress && manualFromBlock == 0 && w.handler != nil {
		// Show deposits as soon as they are mined. Only pending rows are
		// recorded here; credit still needs finality and a security-scanned
		// block in retryPending. A failure never blocks the cursor scan.
		if err := w.observeHead(ctx, pool, chain, head); err != nil {
			failure := &scanStageError{chain: chain.Chain, stage: "observe", to: head, err: err}
			slog.Warn("[cryptopay] head observation incomplete", failure.logAttrs()...)
		}
	}
	if head <= chain.Confirmations {
		return nil
	}
	confirmed := head - chain.Confirmations

	last, err := w.cursor.Load(ctx, chain.Chain)
	if err != nil {
		slog.Warn("[cryptopay] cursor load failed", "chain", chain.Chain, "error", err)
		return err
	}
	// The durable cursor is the security boundary for pending credit: every
	// block at or below it passed securityRange before the cursor moved.
	w.noteCursor(chain.Chain, last)
	hadCursor := last != 0
	if manualFromBlock != 0 {
		// A manual start may rewind the cursor, but must never skip blocks
		// that have not yet been scanned. Keep the earlier durable boundary
		// until every intervening range has been processed successfully.
		if last == 0 || manualFromBlock-1 < last {
			last = manualFromBlock - 1
			if err := w.cursor.Save(ctx, chain.Chain, last); err != nil {
				return err
			}
			w.noteCursor(chain.Chain, last)
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
	if last >= confirmed && (!w.recentReplay || manualFromBlock != 0 || w.handler == nil) {
		return nil
	}

	recipients, err := w.addresses(ctx, chain.Chain)
	if err != nil {
		return err
	}
	if len(recipients) == 0 && w.securityRange == nil {
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
	if w.recentReplay && manualFromBlock == 0 && hadCursor && w.handler != nil {
		state, err := w.prepareRecentReplay(ctx, chain.Chain, min(last, confirmed), maxRange, recipients)
		if err != nil {
			// The overlap boundary must be durable BEFORE the forward cursor
			// moves. Once saved, failures cannot move that independent boundary.
			return stageErr(chain.Chain, "replay", 0, 0, err)
		}
		if state != nil {
			defer func() {
				// A slow or dense overlap has its own budget and checkpoint. It
				// neither steals the forward budget nor blocks new deposits.
				replayErr := w.replayRecentRange(parent, scanner, pool, chain, state, maxRange, recipients)
				scanErr = errors.Join(scanErr, stageErr(chain.Chain, "replay", state.Next, state.Through, replayErr))
			}()
		}
	}
	var liveErr error
	if last >= confirmed {
		return liveErr
	}
	if (confirmed-last-1)/maxRange >= maxScanBatchesPerChain {
		// Do not make a new payment wait behind hours of historical catch-up.
		// This overlapping live window never changes the historical cursor;
		// the ledger's transfer identity handles replay when catch-up arrives.
		span := min(uint64(evmFirstRunLookback), maxRange)
		if err := w.processScanRange(ctx, scanner, pool, chain, confirmed-span+1, confirmed, recipients); err != nil {
			liveErr = errors.Join(liveErr, stageErr(chain.Chain, "live", confirmed-span+1, confirmed,
				fmt.Errorf("live window %s: %w", chain.Chain, err)))
		}
	}
	for from, batches := last+1, 0; from <= confirmed && batches < maxScanBatchesPerChain; batches++ {
		to := from + min(maxRange-1, confirmed-from)
		if err := ctx.Err(); err != nil {
			return errors.Join(liveErr, stageErr(chain.Chain, "deposit", from, to, err))
		}
		// The cursor only ever moves to the end of a fully scanned chunk, so a
		// failure here resumes from exactly this chunk on the next pass.
		if err := w.processScanRange(ctx, scanner, pool, chain, from, to, recipients); err != nil {
			return errors.Join(liveErr, stageErr(chain.Chain, "deposit", from, to, err))
		}
		if err := w.cursor.Save(ctx, chain.Chain, to); err != nil {
			return errors.Join(liveErr, stageErr(chain.Chain, "deposit", from, to, err))
		}
		w.noteCursor(chain.Chain, to)
		from = to + 1
	}
	return liveErr
}

// The live window and historical batches deliberately share every validation
// and persistence check. Only the caller decides whether a successful range
// is contiguous with the durable cursor and can advance it.
func (w *Worker) processScanRange(ctx context.Context, scanner *EVMScanner, pool *rpc.Pool, chain ChainConfig, from, to uint64, recipients []string) error {
	if w.securityRange != nil {
		if err := w.securityRange(ctx, scanner, pool, chain, from, to); err != nil {
			return stageErr(chain.Chain, "security", from, to, err)
		}
		w.markSecurityCovered(chain.Chain, from, to)
	}
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
		if err := w.processTransfer(ctx, pool, chain, transfer, decimals); err != nil {
			return err
		}
	}
	return nil
}

// An already durable observation is handled by the rotating pending/reorg
// queues. Compare its complete identity, including block hash and amount,
// before avoiding repeated network checks in an overlapping range.
type recordedTransferHandler interface {
	HasRecordedTransfer(context.Context, Transfer, int) (bool, error)
}

func (w *Worker) processTransfer(ctx context.Context, pool *rpc.Pool, chain ChainConfig, transfer Transfer, decimals int) error {
	if transfer.Amount == nil || transfer.Amount.Sign() <= 0 {
		// ERC20 permits zero-value events. They cannot create a deposit and
		// must not turn inexpensive dust into repeated receipt/finality RPCs.
		return nil
	}
	if recorded, ok := w.handler.(recordedTransferHandler); ok {
		known, err := recorded.HasRecordedTransfer(ctx, transfer, decimals)
		if err != nil {
			return err
		}
		if known {
			return nil
		}
	}
	verifyCtx := ctx
	if deadline, ok := ctx.Deadline(); ok {
		// Preserve time to durably queue an inconclusive transfer. Expiring
		// the RPC and persistence contexts together cannot make progress.
		var cancel context.CancelFunc
		verifyCtx, cancel = context.WithDeadline(ctx, deadline.Add(-500*time.Millisecond))
		defer cancel()
	}
	receipt, verifyErr := VerifyTransaction(verifyCtx, pool, transfer.TxHash)
	if verifyErr == nil {
		verifyErr = verifyCanonicalBlock(verifyCtx, pool, receipt)
	}
	if verifyErr == nil {
		verifyErr = verifyReceiptTransfer(transfer, receipt)
	}
	final := false
	if verifyErr == nil {
		final, verifyErr = transactionIsFinalForChain(verifyCtx, pool, chain.Chain, receipt.BlockNumber, chain.Confirmations)
	}
	if verifyErr != nil || !final {
		if err := w.handler.ProcessPending(ctx, transfer, decimals); err != nil {
			return fmt.Errorf("persist pending %s/%s/%d: %w", transfer.Chain, transfer.TxHash, transfer.LogIndex, err)
		}
		slog.Info("[cryptopay] deferred transfer", "chain", transfer.Chain, "tx", transfer.TxHash, "cause", rpc.SafeError(verifyErr))
		return nil
	}
	credited, err := w.handler.Process(ctx, transfer, decimals)
	if err != nil {
		return fmt.Errorf("persist deposit %s/%s/%d: %w", transfer.Chain, transfer.TxHash, transfer.LogIndex, err)
	}
	if credited {
		w.markCreditedConfirmations(ctx, chain, transfer)
	}
	return nil
}

// observeHeadCap bounds the unconfirmed window: at most the chain's own
// confirmation depth, 256 blocks and one eth_getLogs span.
func observeHeadCap(confirmations, maxRange uint64) uint64 {
	return min(confirmations, 256, maxRange)
}

// observeHead scans the not-yet-confirmed blocks [max(confirmed+1,
// head-cap+1), head] and records matching transfers as pending only. It never
// moves the cursor and never credits. Rows already recorded only get their
// confirmation counter refreshed.
func (w *Worker) observeHead(ctx context.Context, pool *rpc.Pool, chain ChainConfig, head uint64) error {
	maxRange := w.maxRange
	if chain.MaxRange > 0 {
		maxRange = chain.MaxRange
	}
	if maxRange == 0 {
		maxRange = 2000
	}
	limit := observeHeadCap(chain.Confirmations, maxRange)
	if limit == 0 || head == 0 {
		return nil
	}
	from := uint64(1)
	if head >= limit {
		from = head - limit + 1
	}
	if head > chain.Confirmations {
		from = max(from, head-chain.Confirmations+1)
	}
	if from > head {
		return nil
	}
	recipients, err := w.addresses(ctx, chain.Chain)
	if err != nil || len(recipients) == 0 {
		return err
	}
	observeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	transfers, err := NewEVMScanner(pool).ScanRange(observeCtx, chain.Chain, from, head, chain.Tokens, recipients)
	if err != nil {
		return err
	}
	recorded, _ := w.handler.(recordedTransferHandler)
	var errs []error
	for _, transfer := range transfers {
		if transfer.BlockNumber < from || transfer.BlockNumber > head || transfer.Amount == nil || transfer.Amount.Sign() <= 0 {
			continue
		}
		decimals, watched := watchedDecimals(chain, transfer.Token)
		if !watched {
			continue
		}
		known := false
		if recorded != nil {
			if known, err = recorded.HasRecordedTransfer(ctx, transfer, decimals); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		if !known {
			if err := w.handler.ProcessPending(ctx, transfer, decimals); err != nil {
				errs = append(errs, fmt.Errorf("observe pending %s/%s/%d: %w", transfer.Chain, transfer.TxHash, transfer.LogIndex, err))
				continue
			}
		}
		if err := w.updatePendingConfirmations(ctx, transfer.Chain, transfer.TxHash, transfer.LogIndex,
			confirmationsAt(head, transfer.BlockNumber, chain.Confirmations)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
