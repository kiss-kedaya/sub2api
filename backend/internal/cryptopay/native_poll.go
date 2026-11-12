package cryptopay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/explorer"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
)

// NativeScanBlocks is the default native-coin scan source: the original
// per-block eth_getBlockByNumber scan. NativeScanTxList keeps the account-API
// path (one request per watched address per poll interval) as an explicit
// operator choice.
const (
	NativeScanTxList = scanner.NativeScanTxList
	NativeScanBlocks = scanner.NativeScanBlocks
)

// DefaultNativePollSeconds paces the native poll. It is a per-address
// interval, not a block cursor: in txlist mode the call count is
// addresses × (86400 / interval) and does not depend on the chain's block rate;
// in the default blocks mode it paces the hot-wallet native-outflow guard.
const DefaultNativePollSeconds = 10

// txListPageLimit is the account API's maximum rows per answer. A full page may
// have truncated the range, so the cursor re-reads the last returned block
// instead of jumping to the range end.
const txListPageLimit = 10000

// nativeTxLister is the account-API seam (an *explorer.Client in production).
type nativeTxLister interface {
	TxList(ctx context.Context, chain, address string, startBlock, endBlock uint64) ([]explorer.Tx, error)
}

// nativeCursorStore is the per-address progress seam. The production
// implementation is scanner.CursorStore with a native-txlist: namespace, so the
// block cursor is untouched.
type nativeCursorStore interface {
	Load(ctx context.Context, key string) (uint64, error)
	Save(ctx context.Context, key string, lastBlock uint64) error
}

// nativePollSink is where observed transfers go. Deposits must reuse the
// scanner's existing verification and ledger path; outflows reuse the wallet
// security guard.
type nativePollSink interface {
	ProcessNativeDeposit(ctx context.Context, chain scanner.ChainConfig, t scanner.Transfer) error
	ScanNativeOutflow(ctx context.Context, chain scanner.ChainConfig, t scanner.Transfer) error
}

// nativePoll is the address-based native-coin scan. It owns one durable cursor
// per (direction, chain, address) and never moves a cursor over a range it
// failed to read.
type nativePoll struct {
	chains    []scanner.ChainConfig // only chains with Native enabled
	lister    nativeTxLister
	cursor    nativeCursorStore
	sink      nativePollSink
	addresses func(ctx context.Context, chain string) ([]string, error)
	hotWallet func(ctx context.Context) (string, error)
	head      func(ctx context.Context, chain string) (uint64, error)
	// report is called once per failing chain per poll so a provider outage is
	// visible instead of silently dropping deposits.
	report func(chain string, cause error)
	now    func() time.Time
	// seen suppresses repeated receipt verification for a row that a retried
	// query returns again. The ledger's (chain, tx_hash, log_index) key stays
	// the authority on double crediting.
	seen    map[string]time.Time
	seenTTL time.Duration
	// firstRunLookback bounds the first query of a new address so a fresh
	// deployment does not backfill the address's entire history.
	firstRunLookback uint64
	// outflowOnly skips incoming deposit polling: the block-scan fallback is
	// already reading every block, so the address poll only watches the hot
	// wallet's native spend.
	outflowOnly bool
}

func newNativePoll(chainConfigs []scanner.ChainConfig) *nativePoll {
	chains := make([]scanner.ChainConfig, 0, len(chainConfigs))
	for _, chain := range chainConfigs {
		if chain.Native {
			chains = append(chains, chain)
		}
	}
	return &nativePoll{
		chains:           chains,
		report:           func(string, error) {},
		now:              time.Now,
		seen:             map[string]time.Time{},
		seenTTL:          30 * time.Minute,
		firstRunLookback: nativeFirstRunLookback,
	}
}

// nativeFirstRunLookback mirrors the block scanner's first-run window: a new
// address starts a bounded distance behind the confirmed head instead of at
// genesis, so enabling a chain never credits long-past transfers.
const nativeFirstRunLookback = 200

// nativePollOverlap re-reads the last blocks of every window. An account API can
// index a transaction later than the chain mined it; without the overlap a
// cursor that already passed that height would never see the row again. The
// overlap costs nothing extra (one call per address per poll) and duplicate rows
// are dropped by the seen cache and the ledger's unique key.
const nativePollOverlap = 64

// runOnce polls every native chain once. Individual chain failures are reported
// and do not stop the other chains.
func (p *nativePoll) runOnce(ctx context.Context) error {
	if p == nil || len(p.chains) == 0 {
		return nil
	}
	var errs []error
	for _, chain := range p.chains {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		if err := p.pollChain(ctx, chain); err != nil {
			p.report(chain.Chain, err)
			errs = append(errs, fmt.Errorf("native txlist scan %s: %w", chain.Chain, err))
		}
	}
	return errors.Join(errs...)
}

func (p *nativePoll) pollChain(ctx context.Context, chain scanner.ChainConfig) error {
	head, err := p.head(ctx, chain.Chain)
	if err != nil {
		return err
	}
	if head <= chain.Confirmations {
		return nil
	}
	confirmed := head - chain.Confirmations

	if !p.outflowOnly {
		addresses, err := p.addresses(ctx, chain.Chain)
		if err != nil {
			return err
		}
		// One account-API answer carries both directions of an address, so the
		// deposit poll also protects that address's native spend at no extra
		// request. The hot wallet is excluded here because its own query below
		// owns that cursor.
		hot, err := p.hotWallet(ctx)
		if err != nil {
			return err
		}
		hot = strings.ToLower(strings.TrimSpace(hot))
		unique := make(map[string]struct{}, len(addresses))
		for _, address := range addresses {
			lowered := strings.ToLower(strings.TrimSpace(address))
			if lowered == "" {
				continue
			}
			if _, ok := unique[lowered]; ok {
				continue
			}
			unique[lowered] = struct{}{}
			if err := p.pollAddress(ctx, chain, lowered, hot == lowered, confirmed, head); err != nil {
				return err
			}
		}
	}

	return p.pollOutflows(ctx, chain, confirmed, head)
}

// pollOutflows watches the hot wallet's native spend. It runs from the deposit
// poller and, when the channel is disabled, from the idle security guard, so a
// drained gas wallet is never silent.
func (p *nativePoll) pollOutflows(ctx context.Context, chain scanner.ChainConfig, confirmed, head uint64) error {
	// The hot wallet's own native spend is a security signal, not a deposit:
	// it uses its own cursor so an address that is also a deposit recipient
	// keeps two independent, non-interfering checkpoints.
	hot, err := p.hotWallet(ctx)
	if err != nil {
		return err
	}
	if hot = strings.ToLower(strings.TrimSpace(hot)); hot == "" {
		return nil
	}
	return p.pollOutgoing(ctx, chain, hot, confirmed, head)
}

// runOutflowPhase polls only outflows: the disabled-channel guard reuses the
// same authorization path without re-reading deposits.
func (p *nativePoll) runOutflowPhase(ctx context.Context) error {
	if p == nil || len(p.chains) == 0 {
		return nil
	}
	var errs []error
	for _, chain := range p.chains {
		head, err := p.head(ctx, chain.Chain)
		if err != nil {
			p.report(chain.Chain, err)
			errs = append(errs, err)
			continue
		}
		if head <= chain.Confirmations {
			continue
		}
		if err := p.pollOutflows(ctx, chain, head-chain.Confirmations, head); err != nil {
			p.report(chain.Chain, err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// pollAddress reads one address's transactions once and uses the same answer
// for both directions: a payment to it is a deposit, a positive native spend
// from it is an outflow. The hot wallet's own cursor is owned by pollOutgoing,
// so its direction is skipped here to avoid two cursors over one address.
func (p *nativePoll) pollAddress(ctx context.Context, chain scanner.ChainConfig, address string, isHot bool, confirmed, head uint64) error {
	key := "in:" + chain.Chain + ":" + address
	start, last, skip, err := p.nextRange(ctx, key, confirmed)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	// Query up to the head so an unconfirmed payment becomes a visible pending
	// row immediately; only the cursor stops at the confirmed height.
	rows, err := p.lister.TxList(ctx, chain.Chain, address, start, head)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.BlockNumber < start || row.BlockNumber > head {
			continue
		}
		if scanner.ExplorerTxIncoming(row, address) {
			transfer, ok := scanner.NativeTransferFromExplorerTx(chain.Chain, row)
			if !ok {
				continue
			}
			identity := chain.Chain + ":" + transfer.TxHash
			if p.recentlySeen(identity) {
				continue
			}
			if err := p.sink.ProcessNativeDeposit(ctx, chain, transfer); err != nil {
				// A row that could not be verified keeps the cursor where it
				// was: the next poll re-reads it instead of losing the deposit.
				return err
			}
			p.remember(identity)
			continue
		}
		if isHot || row.To == nil || row.Value == nil || row.Value.Sign() <= 0 || !strings.EqualFold(row.From, address) {
			continue
		}
		transfer, ok := scanner.NativeTransferFromExplorerTx(chain.Chain, row)
		if !ok {
			continue
		}
		if err := p.sink.ScanNativeOutflow(ctx, chain, transfer); err != nil {
			return err
		}
	}
	return p.advance(ctx, key, last, start, confirmed, rows)
}

func (p *nativePoll) pollOutgoing(ctx context.Context, chain scanner.ChainConfig, hot string, confirmed, head uint64) error {
	key := "out:" + chain.Chain
	start, last, skip, err := p.nextRange(ctx, key, confirmed)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	rows, err := p.lister.TxList(ctx, chain.Chain, hot, start, head)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.To == nil || row.Value == nil || row.Value.Sign() <= 0 || !strings.EqualFold(row.From, hot) {
			continue
		}
		transfer, ok := scanner.NativeTransferFromExplorerTx(chain.Chain, row)
		if !ok {
			continue
		}
		if transfer.BlockNumber < start || transfer.BlockNumber > head {
			continue
		}
		if err := p.sink.ScanNativeOutflow(ctx, chain, transfer); err != nil {
			return err
		}
	}
	return p.advance(ctx, key, last, start, confirmed, rows)
}

// nextRange resolves the cursor into the inclusive start of the query window.
// A first-run address starts a bounded lookback behind the confirmed head.
func (p *nativePoll) nextRange(ctx context.Context, key string, confirmed uint64) (start, last uint64, skip bool, err error) {
	last, err = p.cursor.Load(ctx, key)
	if err != nil {
		return 0, 0, false, err
	}
	start = last + 1
	if last > nativePollOverlap {
		// Re-read the tail so a late-indexed transaction is not skipped.
		start = last + 1 - nativePollOverlap
	}
	if last == 0 {
		// "0 means unknown": either never scanned, or scanned up to genesis.
		// Both start from the bounded lookback so a new deployment never
		// backfills the whole address history.
		if confirmed > p.firstRunLookback {
			start = confirmed - p.firstRunLookback
		} else {
			start = 1
		}
	}
	if start > confirmed {
		return 0, last, true, nil
	}
	return start, last, false, nil
}

// advance moves the cursor only after every row of the window was processed.
// The cursor stops at the confirmed height, never at the queried head: an
// unconfirmed row above it must be re-read later, while its pending record is
// already durable. A full page may have truncated the window, so the next poll
// re-reads the last returned block (idempotent) instead of assuming the whole
// range was covered. The cursor never moves backwards.
func (p *nativePoll) advance(ctx context.Context, key string, last, start, confirmed uint64, rows []explorer.Tx) error {
	next := confirmed
	if len(rows) >= txListPageLimit {
		if lastRow := rows[len(rows)-1].BlockNumber; lastRow > start {
			next = min(next, lastRow-1)
		} else {
			next = min(next, lastRow)
		}
	}
	if next < last {
		next = last
	}
	return p.cursor.Save(ctx, key, next)
}

func (p *nativePoll) recentlySeen(identity string) bool {
	if p.seen == nil {
		return false
	}
	at, ok := p.seen[identity]
	if !ok {
		return false
	}
	if p.now().Sub(at) > p.seenTTL {
		delete(p.seen, identity)
		return false
	}
	return true
}

func (p *nativePoll) remember(identity string) {
	if p.seen == nil {
		return
	}
	// The map only suppresses duplicate receipt work for a retried query; it
	// must not grow without bound on a long-running process.
	if len(p.seen) > 20000 {
		cutoff := p.now().Add(-p.seenTTL)
		for key, at := range p.seen {
			if at.Before(cutoff) {
				delete(p.seen, key)
			}
		}
	}
	p.seen[identity] = p.now()
}

// startNativePoller runs the address poller until the returned context ends.
// The caller (reload) already holds s.mu, so this function does not lock; it
// records the cancel so the next reload can stop this poller before replacing
// the block worker it writes through.
func (s *Service) startNativePoller(parent context.Context, interval time.Duration, poll *nativePoll) {
	if poll == nil || len(poll.chains) == 0 || mode.ReadOnly() {
		return
	}
	if interval <= 0 {
		interval = DefaultNativePollSeconds * time.Second
	}
	ctx, cancel := context.WithCancel(parent)
	s.nativeCancel = cancel
	go func() {
		defer cancel()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		// runOnce already reports every failing chain through report(), which
		// logs at error level and leaves the cursor in place. The aggregate
		// error would only duplicate that line, so it is deliberately dropped.
		_ = poll.runOnce(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = poll.runOnce(ctx)
			}
		}
	}()
}

// nativeFailureReporter throttles the poller's failure log per chain. Deposit
// discovery never becomes silent: an operator sees a failing address-poll
// provider at error level, with the cursor explicitly not advanced.
func nativeFailureReporter() func(chain string, cause error) {
	lastByChain := map[string]time.Time{}
	return func(chain string, cause error) {
		now := time.Now()
		if now.Sub(lastByChain[chain]) < time.Minute {
			return
		}
		lastByChain[chain] = now
		slog.Error("[cryptopay] native txlist scan incomplete; cursor not advanced, retrying",
			"chain", chain, "cause", safeCause(cause))
	}
}

// safeCause keeps the provider sentence but never the request URL that carries
// the API key.
func safeCause(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *explorer.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Error()
	}
	if errors.Is(err, explorer.ErrNoAPIKey) {
		return "no explorer API key configured for the native txlist scan"
	}
	if errors.Is(err, explorer.ErrNoEndpoint) {
		return "no explorer endpoint configured for this chain"
	}
	if errors.Is(err, explorer.ErrUnauthorized) {
		return "explorer API key rejected"
	}
	return "provider call failed"
}

// nativeServiceSink routes observed native rows into the two existing paths:
// the block scanner's verification/ledger seam for deposits, and the wallet
// security guard for outflows.
type nativeServiceSink struct {
	svc    *Service
	worker *scanner.Worker
}

func (s nativeServiceSink) ProcessNativeDeposit(ctx context.Context, chain scanner.ChainConfig, t scanner.Transfer) error {
	return s.worker.ProcessObservedTransfer(ctx, chain, t)
}

func (s nativeServiceSink) ScanNativeOutflow(ctx context.Context, chain scanner.ChainConfig, t scanner.Transfer) error {
	return s.svc.scanNativeOutflow(ctx, chain, t)
}

// hotAddressResolver caches the derived gas-wallet address. Derivation audits
// a row, so it must not run once per poll per chain; an imported mnemonic
// changes it, and a reload rebuilds the resolver.
type hotAddressResolver struct {
	mu    sync.Mutex
	svc   *Service
	value string
}

func (r *hotAddressResolver) address(ctx context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.value != "" {
		return r.value, nil
	}
	if r.svc == nil || r.svc.wallet == nil {
		return "", errors.New("wallet is not available")
	}
	hot, err := r.svc.wallet.HotAddresses(ctx)
	if err != nil {
		return "", err
	}
	r.value = strings.ToLower(hot.EVM)
	return r.value, nil
}

// reset drops the cached address so a newly imported hot mnemonic is picked up
// by the next poll. The resolver is never replaced, only invalidated, so the
// poller goroutine and the reload path never race on the pointer.
func (r *hotAddressResolver) reset() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.value = ""
	r.mu.Unlock()
}

// hotAddress returns the gas-wallet address, tolerating a Service built outside
// NewService (tests). The resolver pointer is only ever written during
// construction, so reading it from a poll goroutine is race-free.
func (s *Service) hotAddress(ctx context.Context) (string, error) {
	resolver := s.hotResolver
	if resolver == nil {
		return (&hotAddressResolver{svc: s}).address(ctx)
	}
	return resolver.address(ctx)
}

// newExplorerClient builds the account-API client from the saved config. A
// chain without an endpoint simply has no txlist scan; the poller reports it
// instead of silently skipping the chain.
func newExplorerClient(cfg CryptoConfig) (*explorer.Client, error) {
	ids := explorerChainIDs()
	endpoints := make(map[string]explorer.Endpoint, len(cfg.ExplorerAPIURL))
	for chain, root := range cfg.ExplorerAPIURL {
		chainID, ok := ids[strings.ToLower(chain)]
		if !ok {
			continue
		}
		endpoints[strings.ToLower(chain)] = explorer.Endpoint{BaseURL: root, ChainID: chainID}
	}
	return explorer.NewClient(explorer.Config{Endpoints: endpoints, APIKey: cfg.ExplorerAPIKey})
}

// newNativePoll wires the production seams for one reload. Caller holds no
// lock requirement beyond constructing it.
func (s *Service) newNativePoll(cfg CryptoConfig, chainConfigs []scanner.ChainConfig, worker *scanner.Worker,
	client nativeTxLister, addressLister func(context.Context, string) ([]string, error)) *nativePoll {
	poll := newNativePoll(chainConfigs)
	// A chain without an RPC pool cannot verify a receipt or read a head; the
	// block worker skips it silently, so the poller must not turn that known
	// state into a repeating error.
	reachable := poll.chains[:0]
	for _, chain := range poll.chains {
		if s.pools[chain.Chain] != nil {
			reachable = append(reachable, chain)
		}
	}
	poll.chains = reachable
	poll.lister = client
	poll.cursor = scanner.NewNamespacedCursorStore(s.db, "native-txlist:")
	poll.sink = nativeServiceSink{svc: s, worker: worker}
	poll.addresses = addressLister
	poll.hotWallet = s.hotAddress
	poll.head = func(ctx context.Context, chain string) (uint64, error) {
		pool := s.pools[chain]
		if pool == nil {
			return 0, fmt.Errorf("no RPC pool configured for chain %s", chain)
		}
		return pool.BlockNumber(ctx)
	}
	poll.report = nativeFailureReporter()
	// The block-scan fallback already covers deposits; the poller then only
	// guards the hot wallet's native spend, so the expensive per-block scan is
	// never doubled.
	poll.outflowOnly = cfg.NativeScan == NativeScanBlocks
	return poll
}

// stopNativePoller cancels the running address poller, if any. It is safe to
// call repeatedly and while holding s.mu.
func (s *Service) stopNativePollerLocked() {
	if s.nativeCancel != nil {
		s.nativeCancel()
		s.nativeCancel = nil
	}
}

// pollNativeOutflowsIdle keeps the hot-wallet native guard alive while the
// recharge channel is disabled, where the deposit poller is not running. It
// shares the txlist client, the security guard and the same interval and call
// budget as the enabled path, so disabling deposits does not disable theft
// detection.
func (s *Service) pollNativeOutflowsIdle(ctx context.Context, cfg CryptoConfig, pools map[string]*rpc.Pool) error {
	if s == nil || s.db == nil {
		return nil
	}
	interval := time.Duration(cfg.NativePollSeconds) * time.Second
	if interval <= 0 {
		interval = DefaultNativePollSeconds * time.Second
	}
	s.nativeGuardMu.Lock()
	due := time.Since(s.nativeGuardAt) >= interval
	if due {
		s.nativeGuardAt = time.Now()
	}
	s.nativeGuardMu.Unlock()
	if !due {
		return nil
	}
	chains := make([]scanner.ChainConfig, 0, len(pools))
	for chain := range pools {
		token, ok := nativeToken(chain)
		if !ok {
			continue
		}
		chains = append(chains, scanner.ChainConfig{
			Chain: chain, Native: true, NativeDecimals: token.Decimals,
			Confirmations: uint64(cfg.Confirmations[chain]),
		})
	}
	if len(chains) == 0 {
		return nil
	}
	client, err := newExplorerClient(cfg)
	if err != nil {
		return err
	}
	poll := &nativePoll{
		chains: chains,
		lister: client,
		cursor: scanner.NewNamespacedCursorStore(s.db, "native-txlist:"),
		sink:   nativeServiceSink{svc: s},
		addresses: func(context.Context, string) ([]string, error) {
			return nil, nil
		},
		hotWallet: s.hotAddress,
		head: func(ctx context.Context, chain string) (uint64, error) {
			pool := pools[chain]
			if pool == nil {
				return 0, fmt.Errorf("no RPC pool configured for chain %s", chain)
			}
			return pool.BlockNumber(ctx)
		},
		report:           nativeFailureReporter(),
		now:              time.Now,
		seen:             map[string]time.Time{},
		seenTTL:          30 * time.Minute,
		firstRunLookback: nativeFirstRunLookback,
	}
	return poll.runOutflowPhase(ctx)
}
