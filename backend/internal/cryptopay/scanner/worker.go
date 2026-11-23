package scanner

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"math/rand"
	"net/http"
	"regexp"
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

// A fixed 30s pass could not finish the head security window plus four batches
// on a chain that was far behind, so every pass timed out before the deposit
// cursor advanced and the chain never caught up (BSC stayed frozen for hours
// while ETH/Polygon, which were only tens of blocks behind, scanned normally).
// The budget is per chain and the pass still yields between batches, so a
// longer ceiling does not starve other chains or other instances.
const maxScanTimePerChain = 90 * time.Second

var ErrScanBusy = errors.New("another payment scan is running")

// ErrInvalidScanChain rejects a scope that includes a chain this worker does not scan.
var ErrInvalidScanChain = errors.New("scan chain must be an enabled EVM chain")

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
	// ScanConcurrency overrides both log-batch and native-block concurrency.
	// Zero retains the automatic per-endpoint limit.
	ScanConcurrency int
	// Native enables mainnet-coin (ETH/BNB) scanning for this chain, which
	// cannot ride the eth_getLogs path: native value transfers exist only in
	// transactions. NativeDecimals is that coin's exponent (18 on both
	// supported chains) and is never guessed.
	Native         bool
	NativeDecimals int
	// NativeSource is how the chain's native coin is discovered. Empty or
	// "blocks" reads every block with full transactions; "txlist" means an
	// address-based poller owns discovery, so this pass must not pay the
	// per-block cost while Native stays on for the allowlist and decimals.
	NativeSource string
}

// NativeBlockScan reports whether a worker pass must read blocks for native
// value transfers. In txlist mode discovery belongs to the address poller, so
// the expensive per-block read is skipped without disabling native crediting.
func (c ChainConfig) NativeBlockScan() bool {
	return c.Native && c.NativeSource != NativeScanTxList
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
	// onProvisionalRevoke is tried before onReorg for a credited->reverted
	// transition: an instant credit is clawed back automatically (once), while
	// a confirmation-gated credit still needs manual reconciliation.
	onProvisionalRevoke func(ctx context.Context, chain, txHash string, logIndex int, reason string) (bool, error)
	beforePass          func(context.Context) error
	securityRange       func(context.Context, *EVMScanner, *rpc.Pool, ChainConfig, uint64, uint64) error
	// Live workers replay a bounded recent range to recover late RPC log
	// indexing and recipient assignments racing the ordinary cursor snapshot.
	recentReplay bool
	replayMu     sync.Mutex
	replayedAt   map[string]time.Time
	failureAt    time.Time
	// Per-chain report limiter: one slow chain must not hide another's errors.
	chainFailureAt map[string]time.Time
	// chainPause paces the retries of a chain whose forward scan keeps failing
	// without moving its cursor. Guarded by replayMu.
	chainPause map[string]*chainPause
	// liveProgress enables the head observation window and the confirmation
	// counter writes. Production workers (positive interval) turn it on.
	liveProgress        bool
	depositScanObserver func(DepositScanObservation)
	depositScanRound    uint64 // Periodic worker goroutine only.
	// coverage is the per-pass record of security-scanned blocks. Pending
	// credit is allowed only for blocks this pass proved were security-scanned.
	coverage map[string]*chainCoverage
	// ownWallets returns the operator's own EVM wallet addresses (hot wallet and
	// the gas payer). Native transfers sent from one of them are our own gas
	// top-up, not a user deposit: crediting them would hand a user BNB/ETH for
	// money we moved ourselves, and the deposit list would show a phantom
	// "未入账" row for every sweep we fund.
	ownWallets func(ctx context.Context, chain string) ([]string, error)
}

// chainCoverage records, for one chain and one pass, the durable cursor seen
// (every block at or below it already passed securityRange) and the ranges
// whose securityRange call succeeded during this pass.
type chainCoverage struct {
	cursor    uint64
	hasCursor bool
	// startCursor is the first durable cursor this pass saw, so the pass can tell
	// afterwards whether the chain moved forward at all.
	startCursor uint64
	hasStart    bool
	ranges      [][2]uint64
	head        uint64
	hasHead     bool
}

type chainHeadSnapshot struct {
	pool *rpc.Pool
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
	if !c.hasStart {
		c.startCursor, c.hasStart = cursor, true
	}
}

// cursorAdvanced reports whether the durable cursor of chain moved during this pass.
func (w *Worker) cursorAdvanced(chain string) bool {
	w.replayMu.Lock()
	defer w.replayMu.Unlock()
	c := w.coverage[chain]
	return c != nil && c.hasStart && c.hasCursor && c.cursor > c.startCursor
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

// confirmationsAt reports blocks built on top of the inclusion block. This
// matches transactionIsFinalForChain, which credits after head-block >= depth.
func confirmationsAt(head, block, required uint64) uint64 {
	if head <= block {
		return 0
	}
	return min(head-block, required)
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
		attrs = append(attrs, "kind", failure.Kind, "category", failure.Category())
		if failure.HTTPStatus != 0 {
			attrs = append(attrs, "http_status", failure.HTTPStatus)
		}
		if failure.RPCCode != 0 {
			attrs = append(attrs, "rpc_code", failure.RPCCode)
		}
		if failure.Quota {
			attrs = append(attrs, "quota_exhausted", true)
		}
		if errors.Is(failure, context.DeadlineExceeded) {
			attrs = append(attrs, "timeout", true)
		}
	case errors.Is(e.err, context.DeadlineExceeded):
		attrs = append(attrs, "kind", "timeout", "category", rpc.CategoryTimeout)
	case errors.Is(e.err, context.Canceled):
		attrs = append(attrs, "kind", "canceled", "category", "canceled")
	case errors.Is(e.err, rpc.ErrIndependentEvidence):
		attrs = append(attrs, "kind", "evidence", "category", rpc.CategoryUnknown)
	default:
		attrs = append(attrs, "kind", "other", "category", rpc.CategoryUnknown)
	}
	// The endpoint hostname (never the URL) plus a sanitised node sentence make a
	// transport failure actionable: which node timed out, and what it said.
	if host := rpc.HostOf(e.err); host != "" {
		attrs = append(attrs, "endpoint_host", host)
	}
	if detail := rpc.DetailOf(e.err); detail != "" {
		attrs = append(attrs, "detail", detail)
	}
	// SafeError collapses unknown causes into one generic sentence, which hid a
	// real scan failure for hours. Keep the sanitized sentence for anything that
	// reads it, and carry the underlying text for diagnosis.
	return append(attrs, "cause", rpc.SafeError(e.err), "raw_cause", rawScanCause(e.err))
}

// rawScanCause keeps the underlying error text so a stuck chain can be
// diagnosed. Only endpoint credentials are removed: an RPC key lives in the URL
// path, and journald may travel farther than the machine it was written on.
var scanURLKeyPattern = regexp.MustCompile(`(?i)(/rpc/)[A-Za-z0-9_-]{8,}`)
var scanQueryKeyPattern = regexp.MustCompile(`(?i)(apikey=)[A-Za-z0-9_-]+`)

func rawScanCause(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	text = scanURLKeyPattern.ReplaceAllString(text, "${1}<redacted>")
	text = scanQueryKeyPattern.ReplaceAllString(text, "${1}<redacted>")
	if len(text) > 600 {
		text = text[:600] + "…"
	}
	return text
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

// Security hooks run before every live/backfill range. A wallet-security
// incident still blocks the pass in RunOnce; a transient RPC failure in the
// independent outflow scan must not make an inbound deposit disappear. In the
// latter case the transfer is persisted as pending and only becomes credited
// after the normal pending/recheck path observes the security window.
func (w *Worker) SetSecurityGuards(before func(context.Context) error, scan func(context.Context, *EVMScanner, *rpc.Pool, ChainConfig, uint64, uint64) error) {
	w.beforePass, w.securityRange = before, scan
}

// SetOwnWallets wires the operator's own wallet addresses so a gas top-up we
// send to a deposit address is not read back as that user's deposit. Without it
// every funded sweep leaves a phantom pending deposit in the user's list.
func (w *Worker) SetOwnWallets(fn func(ctx context.Context, chain string) ([]string, error)) {
	if w != nil {
		w.ownWallets = fn
	}
}

// ownAddressSet resolves the operator wallet set once per scanned range. A
// lookup failure is not fatal: the scan still reports what it found, and the
// next pass retries. Treating it as fatal would stall the cursor on a problem
// that has nothing to do with the blocks being read.
func (w *Worker) ownAddressSet(ctx context.Context, chain string) map[string]struct{} {
	if w == nil || w.ownWallets == nil {
		return nil
	}
	addrs, err := w.ownWallets(ctx, chain)
	if err != nil {
		slog.Warn("[cryptopay] own wallet list unavailable; native transfers credited as deposits",
			"chain", chain, "cause", rpc.SafeError(err))
		return nil
	}
	if len(addrs) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(addrs))
	for _, addr := range addrs {
		if trimmed := strings.ToLower(strings.TrimSpace(addr)); trimmed != "" {
			set[trimmed] = struct{}{}
		}
	}
	return set
}

// isOwnTransfer reports whether a transfer is our own native movement, which is
// the gas top-up funding a sweep rather than a user deposit.
func isOwnTransfer(own map[string]struct{}, transfer Transfer) bool {
	if len(own) == 0 || !transfer.Native {
		return false
	}
	_, self := own[strings.ToLower(transfer.From)]
	return self
}

// SetReorgNotifier wires the alert for reorged credited deposits.
func (w *Worker) SetReorgNotifier(fn func(chain, txHash string)) {
	if w != nil {
		w.onReorg = fn
	}
}

// SetProvisionalRevokeNotifier wires the automatic claw-back performed when an
// instant (0-confirmation) credit is invalidated by a reorg or a failed /
// replaced transaction. The callback is idempotent and owns the only balance
// movement: returning true means the credit was revoked (or already had been)
// and the legacy admin-only alert must not fire again.
func (w *Worker) SetProvisionalRevokeNotifier(fn func(ctx context.Context, chain, txHash string, logIndex int, reason string) (bool, error)) {
	if w != nil {
		w.onProvisionalRevoke = fn
	}
}

// provisionalCreditHandler is implemented by a ledger that supports instant
// credit. A worker without it keeps the confirmation-gated behaviour.
type provisionalCreditHandler interface {
	ProcessProvisional(ctx context.Context, t Transfer, decimals int) (bool, error)
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

// periodicPassKey marks the context of a timer-driven pass. Only those passes
// are paced by a chain's failure backoff: a manual scan or backfill is an
// operator asking to try now, and always runs.
type periodicPassKey struct{}

func isPeriodicPass(ctx context.Context) bool {
	return ctx != nil && ctx.Value(periodicPassKey{}) != nil
}

// chainPause is the retry gate of one chain after consecutive stalled passes.
type chainPause struct {
	failures int
	next     time.Time
}

// maxChainPause caps how long a stalled chain's forward scan is left alone.
const maxChainPause = time.Minute

// chainPauseDelay is how long a chain's forward scan waits before the next
// timer-driven attempt after `failures` consecutive passes that needed to move
// the cursor and did not.
//
// Why it exists: a forward attempt costs a full set of eth_getLogs batches and
// every failing call is tried on the pool's nodes, one tick (5s) after the last.
// Public RPC providers meter daily requests, so a chain that cannot make
// progress retries itself out of every free allowance within hours and then
// stays down until the provider resets the counter - the half-day Polygon
// stalls. Spacing the retries keeps the allowance for the moment the nodes
// recover.
//
// The first failure is not delayed: scanAll already repeats a rate-limited pass
// once after a short wait, and a single blip should cost one retry, not a pause.
func chainPauseDelay(failures int, interval time.Duration) time.Duration {
	if failures <= 1 {
		return 0
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	// failures=2 waits one interval, then 2x, 4x, ... up to the cap.
	delay := interval
	for i := 2; i < failures && delay < maxChainPause; i++ {
		delay *= 2
	}
	if delay > maxChainPause {
		delay = maxChainPause
	}
	// +-20% so chains that failed together do not retry in lockstep.
	jitter := time.Duration(rand.Int63n(int64(delay)/5*2+1)) - delay/5
	return delay + jitter
}

// chainPausedError is what a timer pass reports for the forward stage of a chain
// whose recent attempts all failed: the attempt was skipped on purpose. Only the
// forward range and the replay behind it wait. The head security window, head
// observation and the coverage record still run every pass, so a deposit made
// during the stall is still seen as pending and can still be credited once a
// security scan covers it.
type chainPausedError struct {
	failures int
	wait     time.Duration
}

func (e *chainPausedError) Error() string {
	return fmt.Sprintf("forward scan paused after %d consecutive stalled passes; next attempt in %s", e.failures, e.wait.Round(time.Second))
}

// forwardPause reports whether the forward scan of chain is paused right now.
func (w *Worker) forwardPause(chain string, now time.Time) (*chainPausedError, bool) {
	w.replayMu.Lock()
	defer w.replayMu.Unlock()
	st := w.chainPause[chain]
	if st == nil || !now.Before(st.next) {
		return nil, false
	}
	return &chainPausedError{failures: st.failures, wait: st.next.Sub(now)}, true
}

// headStageFailed reports whether err contains a failed head lookup: the chain
// could not even learn where it stands.
func headStageFailed(err error) bool {
	byChain := make(map[string][]*scanStageError)
	var other []error
	collectScanFailures(err, byChain, &other)
	for _, failures := range byChain {
		for _, failure := range failures {
			if failure.stage == "head" {
				return true
			}
		}
	}
	return false
}

// forwardStalled reports whether this pass needed to move the durable cursor of
// chain and did not: confirmed blocks were waiting beyond the cursor it started
// from, and it ended where it began. Whatever failed - the deposit batches, a
// batch-level security scan, a timeout - is irrelevant; the cursor is what users'
// deposits wait for.
func (w *Worker) forwardStalled(chain ChainConfig) bool {
	w.replayMu.Lock()
	defer w.replayMu.Unlock()
	c := w.coverage[chain.Chain]
	if c == nil || !c.hasStart || !c.hasHead || c.head <= chain.Confirmations {
		return false
	}
	if c.startCursor >= c.head-chain.Confirmations {
		return false // nothing was waiting
	}
	return !(c.hasCursor && c.cursor > c.startCursor)
}

// noteChainPass records the outcome of one timer-driven pass of chain. A pass
// that moved the cursor, found nothing to move, or failed in stages that leave
// the cursor free clears the pause; one that needed to move it and could not
// extends the pause. A pass whose forward scan was itself skipped is neither.
func (w *Worker) noteChainPass(chain ChainConfig, scanErr error, now time.Time) {
	var skipped *chainPausedError
	if errors.As(scanErr, &skipped) {
		return
	}
	blocked := scanErr != nil && (headStageFailed(scanErr) || w.forwardStalled(chain))
	w.replayMu.Lock()
	defer w.replayMu.Unlock()
	if !blocked {
		delete(w.chainPause, chain.Chain)
		return
	}
	if w.chainPause == nil {
		w.chainPause = make(map[string]*chainPause)
	}
	st := w.chainPause[chain.Chain]
	if st == nil {
		st = &chainPause{}
		w.chainPause[chain.Chain] = st
	}
	st.failures++
	st.next = now.Add(chainPauseDelay(st.failures, w.interval))
	if st.failures == 2 || st.failures%10 == 0 {
		slog.Warn("[cryptopay] chain forward scan keeps stalling; pacing its retries to spare the RPC allowance",
			"chain", chain.Chain, "consecutive_stalled_passes", st.failures, "next_attempt_in", st.next.Sub(now).Round(time.Second).String())
	}
}

// scanPassDeadline bounds one whole pass. Without it a single hung RPC call
// keeps the pass (and therefore the scan session lock) forever, every later
// tick answers ErrScanBusy, and the chain stops advancing until the process is
// restarted. The budget is deliberately larger than the per-chain allowance so
// a normal backlog still finishes.
const scanPassDeadline = 15 * time.Minute

func (w *Worker) scanAll(parent context.Context) {
	if w == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, scanPassDeadline)
	defer cancel()
	ctx = context.WithValue(ctx, periodicPassKey{}, true)
	if err := w.runPeriodicPass(ctx); err != nil && !errors.Is(err, ErrScanBusy) && !errors.Is(err, mode.ErrReadOnly) && parent.Err() == nil {
		// A node rate limit says nothing about the block range: the same pass
		// succeeds once the burst has drained. Waiting out one backoff keeps the
		// cursor moving inside the tick instead of losing the whole interval —
		// repeated 429s across ticks are what left the BSC cursor parked for
		// hours while the other chains advanced.
		if isRateLimitedScan(err) {
			select {
			case <-parent.Done():
				return
			case <-time.After(scanRateLimitRoundBackoff):
			}
			if retried := w.runPeriodicPass(ctx); retried == nil {
				return
			} else if parent.Err() != nil {
				return
			} else {
				err = retried
			}
		}
		w.reportScanFailure(err)
	}
	if ctx.Err() != nil && parent.Err() == nil {
		slog.Error("[cryptopay] scan pass exceeded its deadline; session lock released", "deadline", scanPassDeadline.String())
	}
}

// scanRateLimitRoundBackoff is how long one pass waits before repeating a round
// that a node throttled. Short enough to stay inside the scan interval, long
// enough for a per-second budget to refill.
const scanRateLimitRoundBackoff = 3 * time.Second

// isRateLimitedScan reports whether any failure in err is an HTTP/RPC 429.
func isRateLimitedScan(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, inner := range joined.Unwrap() {
			if isRateLimitedScan(inner) {
				return true
			}
		}
		return false
	}
	var failure *rpc.Failure
	if errors.As(err, &failure) {
		return failure.HTTPStatus == http.StatusTooManyRequests || failure.RPCCode == 429
	}
	return false
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
		slog.Error("[cryptopay] payment scan incomplete; retrying without skipping blocks", "cause", rpc.SafeError(errors.Join(other...)), "raw_cause", rawScanCause(errors.Join(other...)))
	}
}

// DepositScanObservation contains the existing per-chain evidence from one
// periodic, lock-held payment scan. It is an internal observation, not a DTO.
type DepositScanObservation struct {
	Chain           string
	Round           uint64
	At              time.Time
	ConfirmedHead   uint64
	CursorBefore    uint64
	Cursor          uint64
	CanMeasure      bool
	ForwardFailed   bool
	NearHeadScanned bool
	Failed          bool
}

// SetDepositScanObserver installs a nonblocking observer before Start.
func (w *Worker) SetDepositScanObserver(observer func(DepositScanObservation)) {
	w.depositScanObserver = observer
}

func (w *Worker) runPeriodicPass(ctx context.Context) error {
	if w.depositScanObserver == nil {
		return w.RunOnce(ctx)
	}
	w.depositScanRound++
	return w.runOnceFrom(ctx, 0, w.depositScanRound)
}

// RunOnce executes the complete background pass, including pending confirmations
// and reorg checks, under the same shared session lock. Manual callers receive
// failures rather than a false completion response.
func (w *Worker) RunOnce(ctx context.Context) (err error) {
	return w.RunOnceFrom(ctx, 0)
}

// ScanChains resolves an optional allowlist without mutating the live worker.
// Unknown names fail closed, including chains that only have an RPC configured.
func (w *Worker) ScanChains(scope ...string) ([]ChainConfig, error) {
	if w == nil {
		return nil, ErrInvalidScanChain
	}
	if len(scope) == 0 {
		return append([]ChainConfig(nil), w.chains...), nil
	}
	var selected []ChainConfig
	seen := make(map[string]bool, len(scope))
	for _, name := range scope {
		chain, ok := w.chainConfig(name)
		if !ok {
			return nil, ErrInvalidScanChain
		}
		if !seen[name] {
			selected = append(selected, chain)
			seen[name] = true
		}
	}
	return selected, nil
}

// RunOnceFrom permits a bounded, explicitly authorized backfill. An optional
// scope limits discovery and maintenance; a rewind requires exactly one chain.
// Rewinding reuses the ledger's unique transfer identity.
func (w *Worker) RunOnceFrom(ctx context.Context, fromBlock uint64, scope ...string) error {
	return w.runOnceFrom(ctx, fromBlock, 0, scope...)
}

func (w *Worker) runOnceFrom(ctx context.Context, fromBlock, periodicRound uint64, scope ...string) (err error) {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	chains, err := w.ScanChains(scope...)
	if err != nil {
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
		if len(chains) != 1 {
			return fmt.Errorf("historical scan requires exactly one configured chain")
		}
		chain := chains[0]
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
	var bscHead *chainHeadSnapshot
	if w.liveProgress && fromBlock == 0 && w.handler != nil {
		for _, chain := range chains {
			if chain.Chain == "bsc" {
				bscHead = w.observeConfiguredHead(ctx, chain)
				break
			}
		}
	}
	// Discover new payments before rotating historical receipt/reorg checks.
	// Maintenance has its own bounded budget and must not delay fresh deposits.
	periodic := isPeriodicPass(ctx)
	for _, chain := range chains {
		var scanErr error
		var snapshot *chainHeadSnapshot
		if chain.Chain == "bsc" {
			snapshot = bscHead
		}
		if periodicRound != 0 && w.handler != nil {
			observation := DepositScanObservation{Chain: chain.Chain, Round: periodicRound, At: time.Now()}
			scanErr = w.scanChainObserved(ctx, chain, fromBlock, snapshot, &observation)
			if !errors.Is(ctx.Err(), context.Canceled) {
				observation.Failed = scanErr != nil
				w.depositScanObserver(observation)
			}
		} else {
			scanErr = w.scanChainFromSnapshot(ctx, chain, fromBlock, snapshot)
		}
		if periodic && fromBlock == 0 {
			w.noteChainPass(chain, scanErr, time.Now())
		}
		if scanErr != nil {
			err = errors.Join(err, fmt.Errorf("scan %s: %w", chain.Chain, scanErr))
		}
	}
	if pendingErr := w.retryPending(ctx, scope...); pendingErr != nil {
		err = errors.Join(err, fmt.Errorf("pending retry: %w", pendingErr))
	}
	if recheckErr := w.recheckCredited(ctx, scope...); recheckErr != nil {
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

// effectiveMaxRange clamps the configured span to what the chain's RPC pool
// will actually accept.
//
// Configured ranges come from crypto.config and describe what the chain *can*
// be scanned at; a provider limit describes what one endpoint *will* accept.
// Taking the minimum means a strict node in the rotation no longer forces every
// request to be rejected and bisected: the split happens up front, which is the
// difference between one request per chunk and repeatedly hitting the limit.
func (w *Worker) effectiveMaxRange(chain ChainConfig) uint64 {
	maxRange := w.maxRange
	if chain.MaxRange > 0 {
		maxRange = chain.MaxRange
	}
	if maxRange == 0 {
		maxRange = 2000
	}
	if pool := w.pools[chain.Chain]; pool != nil {
		if limits, ok := pool.TightestLimits(); ok && limits.MaxBlocks > 0 && limits.MaxBlocks < maxRange {
			maxRange = limits.MaxBlocks
		}
	}
	return maxRange
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
	return w.scanChainFromSnapshot(ctx, chain, manualFromBlock, nil)
}

func (w *Worker) observeConfiguredHead(ctx context.Context, chain ChainConfig) *chainHeadSnapshot {
	observeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	pool := w.pools[chain.Chain]
	if pool == nil {
		return nil
	}
	pool, err := verifiedChainPool(observeCtx, pool, chain.Chain)
	if err != nil {
		w.reportObserveFailure(&scanStageError{chain: chain.Chain, stage: "observe", err: err})
		return nil
	}
	head, err := pool.BlockNumber(observeCtx)
	if err != nil {
		w.reportObserveFailure(&scanStageError{chain: chain.Chain, stage: "observe", err: err})
		return nil
	}
	w.noteHead(chain.Chain, head)
	if err := w.observeHead(observeCtx, pool, chain, head); err != nil {
		w.reportObserveFailure(&scanStageError{chain: chain.Chain, stage: "observe", to: head, err: err})
	}
	return &chainHeadSnapshot{pool: pool}
}

func (w *Worker) scanChainFromSnapshot(ctx context.Context, chain ChainConfig, manualFromBlock uint64, snapshot *chainHeadSnapshot) error {
	return w.scanChainObserved(ctx, chain, manualFromBlock, snapshot, nil)
}

func (w *Worker) scanChainObserved(ctx context.Context, chain ChainConfig, manualFromBlock uint64, snapshot *chainHeadSnapshot, observation *DepositScanObservation) (scanErr error) {
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
	var head uint64
	var err error
	if snapshot != nil {
		pool = snapshot.pool
		head, err = pool.BlockNumber(ctx)
		if err != nil {
			return stageErr(chain.Chain, "head", 0, 0, err)
		}
	} else {
		pool, err = verifiedChainPool(ctx, pool, chain.Chain)
		if err != nil {
			return stageErr(chain.Chain, "head", 0, 0, err)
		}
		head, err = pool.BlockNumber(ctx)
		if err != nil {
			return stageErr(chain.Chain, "head", 0, 0, err)
		}
	}
	slog.Info("[cryptopay] scan tick", "chain", chain.Chain, "head", head, "confirmations", chain.Confirmations)
	w.noteHead(chain.Chain, head)
	// The head security window runs before head observation: instant credit
	// moves the balance from a 0-confirmation block, so the block must first
	// have passed the outflow-security scan. A security failure still records
	// the deposit as pending (no funds move) and the confirmation path owns it.
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
		if err := w.securityRange(ctx, chain.evmScanner(pool), pool, chain, head-span+1, head); err != nil {
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
	if snapshot == nil && w.liveProgress && manualFromBlock == 0 && w.handler != nil {
		// Recording pending deposits does not move the cursor; the security
		// window above already decided whether a fresh observation may be
		// credited instantly.
		if err := w.observeHead(ctx, pool, chain, head); err != nil {
			w.reportObserveFailure(&scanStageError{chain: chain.Chain, stage: "observe", to: head, err: err})
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
	if observation != nil {
		observation.ConfirmedHead = confirmed
		observation.CursorBefore, observation.Cursor = last, last
		// If an explorer poller owns native deposits this scan alone cannot
		// certify the whole network. A bootstrap cursor is not progress either.
		observation.CanMeasure = hadCursor && (!chain.Native || chain.NativeBlockScan())
		observation.ForwardFailed = true
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
		if observation != nil {
			observation.ForwardFailed = false
		}
		return nil
	}

	if last < confirmed && manualFromBlock == 0 && isPeriodicPass(parent) {
		// Forward work is waiting but the last attempts at it all failed: leave it
		// to the next scheduled attempt. Everything above (head window, head
		// observation, coverage) already ran this pass.
		if paused, ok := w.forwardPause(chain.Chain, time.Now()); ok {
			return stageErr(chain.Chain, "deposit", last+1, confirmed, paused)
		}
	}

	recipients, err := w.addresses(ctx, chain.Chain)
	if err != nil {
		return err
	}
	if len(recipients) == 0 && w.securityRange == nil {
		// Nothing to watch: advance the cursor so we do not rescan forever.
		if observation != nil {
			observation.CanMeasure = false
		}
		return w.cursor.Save(ctx, chain.Chain, confirmed)
	}

	scanner := chain.evmScanner(pool)
	// Per-chain cap: public providers enforce very different getLogs spans
	// (X Layer rejects anything over 100 blocks), so the chunk size must be
	// configurable per chain instead of one global constant.
	maxRange := w.effectiveMaxRange(chain)
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
		if observation != nil {
			observation.ForwardFailed = false
		}
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
		} else if observation != nil {
			observation.NearHeadScanned = true
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
		if observation != nil && to == confirmed {
			observation.NearHeadScanned = true
		}
		if err := w.cursor.Save(ctx, chain.Chain, to); err != nil {
			return errors.Join(liveErr, stageErr(chain.Chain, "deposit", from, to, err))
		}
		w.noteCursor(chain.Chain, to)
		if observation != nil {
			observation.Cursor = to
		}
		from = to + 1
	}
	if observation != nil {
		observation.ForwardFailed = false
	}
	return liveErr
}

// The live window and historical batches deliberately share every validation
// and persistence check. Only the caller decides whether a successful range
// is contiguous with the durable cursor and can advance it.
func (w *Worker) processScanRange(ctx context.Context, scanner *EVMScanner, pool *rpc.Pool, chain ChainConfig, from, to uint64, recipients []string) error {
	securityUnavailable := false
	var securityFailure error
	if w.securityRange != nil {
		if err := w.securityRange(ctx, scanner, pool, chain, from, to); err != nil {
			var rpcFailure *rpc.Failure
			if errors.As(err, &rpcFailure) || errors.Is(err, context.DeadlineExceeded) {
				securityUnavailable = true
				securityFailure = err
				slog.Error("[cryptopay] outflow security scan unavailable; recording deposits as pending",
					"chain", chain.Chain, "from", from, "to", to, "cause", rpc.SafeError(err))
			} else {
				return stageErr(chain.Chain, "security", from, to, err)
			}
		} else {
			w.markSecurityCovered(chain.Chain, from, to)
		}
	}
	transfers, err := scanner.ScanRange(ctx, chain.Chain, from, to, chain.Tokens, recipients)
	if err != nil {
		return err
	}
	if chain.NativeBlockScan() {
		// Native coin transfers have no logs, so they are read from the blocks
		// themselves. A failure here must fail the range: the cursor may only
		// advance over blocks whose native transfers were actually inspected.
		nativeTransfers, nativeErr := scanner.ScanNativeRange(ctx, chain.Chain, from, to, recipients)
		if nativeErr != nil {
			return nativeErr
		}
		transfers = append(transfers, nativeTransfers...)
	}
	own := w.ownAddressSet(ctx, chain.Chain)
	for _, transfer := range transfers {
		// Ignore out-of-range results even if a faulty endpoint returns them.
		if transfer.BlockNumber < from || transfer.BlockNumber > to {
			continue
		}
		// A native transfer we sent ourselves is the gas top-up that funds a
		// sweep, never a deposit. Token transfers are unaffected: their sender is
		// a user, and a watched address cannot be both a depositor and our wallet.
		if isOwnTransfer(own, transfer) {
			continue
		}
		// Decimals come from the allowlist before either ledger path; a wrong
		// exponent could inflate an 18-decimal token by twelve orders.
		decimals, watched := transferDecimals(chain, transfer.Token)
		if !watched {
			slog.Warn("[cryptopay] ignored transfer for unwatched token",
				"chain", transfer.Chain, "tx", transfer.TxHash, "token", transfer.Token)
			continue
		}
		if securityUnavailable {
			if err := w.handler.ProcessPending(ctx, transfer, decimals); err != nil {
				return fmt.Errorf("persist pending %s/%s/%d: %w", transfer.Chain, transfer.TxHash, transfer.LogIndex, err)
			}
			continue
		}
		if err := w.processTransfer(ctx, pool, chain, transfer, decimals); err != nil {
			return err
		}
	}
	if securityFailure != nil {
		// Pending observations are durable, but this range is not safe to put
		// behind the cursor until outflow verification succeeds.
		return stageErr(chain.Chain, "security", from, to, securityFailure)
	}
	return nil
}

// ProcessObservedTransfer runs one transfer that was observed outside the
// block-range scan (the account-API txlist poller) through the identical
// verification, pending and crediting path as a scanned one.
//
// It is deliberately a thin seam: the receipt/proof/finality checks, the
// watched-token decimals lookup, the pending persistence and the ledger call
// are the same code the block scan uses, so a native deposit discovered by
// txlist can never take a second, differently-validated route into the ledger.
func (w *Worker) ProcessObservedTransfer(ctx context.Context, chain ChainConfig, transfer Transfer) error {
	if w == nil || w.handler == nil {
		return errors.New("scanner has no deposit handler")
	}
	pool := w.pools[chain.Chain]
	if pool == nil {
		return fmt.Errorf("no RPC pool configured for chain %s", chain.Chain)
	}
	decimals, watched := transferDecimals(chain, transfer.Token)
	if !watched {
		return fmt.Errorf("transfer for unwatched token %s", transfer.Token)
	}
	return w.processTransfer(ctx, pool, chain, transfer, decimals)
}

// creditProvisional hands an already-verified, not-yet-final transfer to the
// ledger's instant-credit entry point. handled=false means the deposit handler
// has no instant-credit support and the caller must keep the pending path.
func (w *Worker) creditProvisional(ctx context.Context, chain ChainConfig, transfer Transfer, decimals int) (credited bool, handled bool, err error) {
	handler, ok := w.handler.(provisionalCreditHandler)
	if !ok {
		return false, false, nil
	}
	credited, err = handler.ProcessProvisional(ctx, transfer, decimals)
	return credited, true, err
}

// verifyAndCreditProvisional is the head-observation path: the transfer was
// only just discovered, so it must be proven on the canonical chain before any
// balance moves. A verification failure is not evidence against the deposit;
// the row stays pending and the confirmation-gated retry path owns it.
func (w *Worker) verifyAndCreditProvisional(ctx context.Context, pool *rpc.Pool, chain ChainConfig, transfer Transfer, decimals int) (credited bool, handled bool, err error) {
	if _, ok := w.handler.(provisionalCreditHandler); !ok {
		return false, false, nil
	}
	verifyCtx := ctx
	if deadline, ok := ctx.Deadline(); ok {
		// Leave time to persist the trigger state if the RPC is slow.
		var cancel context.CancelFunc
		verifyCtx, cancel = context.WithDeadline(ctx, deadline.Add(-500*time.Millisecond))
		defer cancel()
	}
	receipt, verifyErr := VerifyTransaction(verifyCtx, pool, transfer.TxHash)
	if verifyErr == nil {
		verifyErr = verifyCanonicalBlock(verifyCtx, pool, receipt)
	}
	if verifyErr == nil {
		if transfer.Native {
			verifyErr = verifyNativeTransfer(verifyCtx, pool, transfer, receipt)
		} else {
			verifyErr = verifyReceiptTransfer(transfer, receipt)
		}
	}
	if verifyErr != nil {
		return false, true, nil
	}
	return w.creditProvisional(ctx, chain, transfer, decimals)
}

// finalizeProvisional promotes an instant credit once its transaction reached
// finality. No balance moves: the amount already landed at detection time, so
// only the risk-window recheck stops. Best effort; a failure just means the row
// is rechecked again on a later pass.
func (w *Worker) finalizeProvisional(ctx context.Context, chain ChainConfig, p depositObservation) {
	if _, err := w.db.ExecContext(ctx, `UPDATE deposit_transactions
		SET provisional = FALSE, confirmations = $4
		WHERE chain = $1 AND tx_hash = $2 AND log_index = $3
		  AND status = 'credited' AND provisional`,
		p.chain, p.txHash, p.logIndex, int64(chain.Confirmations)); err != nil {
		slog.Warn("[cryptopay] provisional credit not finalized", "chain", p.chain, "tx", p.txHash, "log_index", p.logIndex, "cause", rpc.SafeError(err))
	}
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
		if transfer.Native {
			// A native transfer has no log: prove it with the transaction's
			// own to/value instead of a Transfer event.
			verifyErr = verifyNativeTransfer(verifyCtx, pool, transfer, receipt)
		} else {
			verifyErr = verifyReceiptTransfer(transfer, receipt)
		}
	}
	final := false
	if verifyErr == nil {
		final, verifyErr = transactionIsFinalForChain(verifyCtx, pool, chain.Chain, receipt.BlockNumber, chain.Confirmations)
	}
	if verifyErr == nil && !final {
		// Instant credit: the receipt, canonical block and transfer identity
		// above are already proven, so the balance may land now. The ledger
		// decides eligibility (switch + amount floor) and records the row as
		// pending when it is not eligible; either way the observation is durable.
		credited, handled, creditErr := w.creditProvisional(ctx, chain, transfer, decimals)
		if creditErr != nil {
			return fmt.Errorf("instant credit %s/%s/%d: %w", transfer.Chain, transfer.TxHash, transfer.LogIndex, creditErr)
		}
		if handled {
			if credited {
				w.markCreditedConfirmations(ctx, chain, transfer)
			}
			return nil
		}
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

// reportObserveFailure logs at error level (production keeps only errors),
// at most once per chain per minute, and never joins the pass result: head
// observation is best effort and must not fail the scan.
func (w *Worker) reportObserveFailure(failure *scanStageError) {
	key := failure.chain + "|observe"
	now := time.Now()
	w.replayMu.Lock()
	if w.chainFailureAt == nil {
		w.chainFailureAt = make(map[string]time.Time)
	}
	report := now.Sub(w.chainFailureAt[key]) >= time.Minute
	if report {
		w.chainFailureAt[key] = now
	}
	w.replayMu.Unlock()
	if report {
		slog.Error("[cryptopay] head observation incomplete; cursor scan unaffected", failure.logAttrs()...)
	}
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
	maxRange := w.effectiveMaxRange(chain)
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
	transfers, scanErr := chain.evmScanner(pool).ScanRange(observeCtx, chain.Chain, from, head, chain.Tokens, recipients)
	if scanErr == nil && chain.NativeBlockScan() {
		// The unconfirmed window must count native deposits too, otherwise a
		// user who paid ETH/BNB sees nothing until the cursor reaches the
		// block. A failure here is best effort, like the log scan above.
		var nativeTransfers []Transfer
		nativeTransfers, scanErr = chain.evmScanner(pool).ScanNativeRange(observeCtx, chain.Chain, from, head, recipients)
		transfers = append(transfers, nativeTransfers...)
	}
	if own := w.ownAddressSet(ctx, chain.Chain); len(own) > 0 {
		// The same exclusion the durable pass applies: a gas top-up is our money
		// moving, and the observation window must not turn it into a pending
		// deposit either.
		kept := transfers[:0]
		for _, transfer := range transfers {
			if isOwnTransfer(own, transfer) {
				continue
			}
			kept = append(kept, transfer)
		}
		transfers = kept
	}
	recorded, _ := w.handler.(recordedTransferHandler)
	var errs []error
	if scanErr != nil {
		errs = append(errs, scanErr)
	}
	for _, transfer := range transfers {
		if transfer.BlockNumber < from || transfer.BlockNumber > head || transfer.Amount == nil || transfer.Amount.Sign() <= 0 {
			continue
		}
		decimals, watched := transferDecimals(chain, transfer.Token)
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
			// Instant credit needs a proven receipt and a security-scanned
			// block. The caller runs the head security window before this
			// observation pass, so coverage is already recorded when it
			// succeeded; a transient security failure leaves the row pending.
			if w.securityRange == nil || w.securityCovered(chain.Chain, transfer.BlockNumber) {
				credited, _, creditErr := w.verifyAndCreditProvisional(ctx, pool, chain, transfer, decimals)
				if creditErr != nil {
					errs = append(errs, fmt.Errorf("observe instant credit %s/%s/%d: %w", transfer.Chain, transfer.TxHash, transfer.LogIndex, creditErr))
					continue
				}
				if credited {
					w.markCreditedConfirmations(ctx, chain, transfer)
					continue
				}
			}
		}
		if err := w.updatePendingConfirmations(ctx, transfer.Chain, transfer.TxHash, transfer.LogIndex,
			confirmationsAt(head, transfer.BlockNumber, chain.Confirmations)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
