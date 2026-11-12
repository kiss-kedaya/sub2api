// Package scanner watches configured chains for incoming deposits.
package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// Transfer is one observed token transfer.
type Transfer struct {
	Chain    string
	TxHash   string
	LogIndex int
	From     string
	To       string
	Token    string // contract address (lowercase), or NativeToken
	// Native marks a mainnet-coin transfer (ETH/BNB). It carries no ERC-20
	// log, so it is verified against the transaction's to/value instead of a
	// Transfer event; see verifyNativeTransfer.
	Native      bool
	Amount      *big.Int
	Decimals    int
	BlockNumber uint64
	BlockHash   string
}

// ERC20 Transfer(address,address,uint256) topic0.
const transferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

// batchSize bounds how many addresses go into one eth_getLogs filter.
const batchSize = 200

// scanBatchConcurrency bounds how many address batches of one scanRange have an
// eth_getLogs request in flight at once. A pool of thousands of addresses is
// dozens of batches, and issuing them one after another made a single 2000
// block range take most of maxScanTimePerChain. The value is a small ceiling,
// not a throughput target: per-endpoint pacing (rpc.EndpointThrottle) still
// spaces request starts, and a var only so tests can pin it.
var scanBatchConcurrency = 8

// scanBatchesPerEndpoint caps the in-flight requests per node the pool can rotate
// over. A chain served by a single public node must not get eight simultaneous
// getLogs calls: a rate limit there fails the batch, the cursor stays put, and the
// next pass repeats the same burst. Chains with several nodes still run at
// scanBatchConcurrency.
var scanBatchesPerEndpoint = 4

// scanWorkers is how many address batches of one scanRange may be in flight.
func (s *EVMScanner) scanWorkers() int {
	workers := scanBatchConcurrency
	if s.pool != nil {
		if nodes := len(s.pool.Endpoints()); nodes > 0 {
			workers = min(workers, nodes*scanBatchesPerEndpoint)
		}
	}
	return max(workers, 1)
}

// evmFirstRunLookback is how far back the first scan reaches so deposits
// landing just before boot are still caught. Bounded: full-history backfill
// is the reconciliation job's concern, not the live scanner's.
const evmFirstRunLookback = 200

// EVMScanner scans ERC20 transfers for a set of watched addresses.
type EVMScanner struct {
	pool *rpc.Pool
}

func NewEVMScanner(pool *rpc.Pool) *EVMScanner {
	return &EVMScanner{pool: pool}
}

type getLogsParams struct {
	FromBlock string   `json:"fromBlock"`
	ToBlock   string   `json:"toBlock"`
	Address   []string `json:"address"` // token contracts
	Topics    [][]any  `json:"topics"`
}

type evmLog struct {
	Address     string   `json:"address"`
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	BlockNumber string   `json:"blockNumber"`
	TxHash      string   `json:"transactionHash"`
	LogIndex    string   `json:"logIndex"`
	BlockHash   string   `json:"blockHash"`
	Removed     bool     `json:"removed"`
}

// ScanRange fetches transfers in [fromBlock, toBlock] for the given token
// contracts and watched recipients, batching recipients by the filter limit.
// Batches run concurrently (scanBatchConcurrency) but the result is assembled
// in batch order, so it is identical to a sequential scan.
// On a partial failure it returns transfers from successful batches together
// with the error; cursor callers must not advance, while best-effort observers
// may persist the matches without treating the range as fully scanned.
func (s *EVMScanner) ScanRange(ctx context.Context, chain string, fromBlock, toBlock uint64, tokens []string, recipients []string) ([]Transfer, error) {
	return s.scanRange(ctx, chain, fromBlock, toBlock, tokens, recipients, false)
}

// ScanOutgoingRange watches the sender topic. A to-address-only scan misses
// unauthorized transfers out of deposit and gas wallets entirely.
func (s *EVMScanner) ScanOutgoingRange(ctx context.Context, chain string, fromBlock, toBlock uint64, tokens, senders []string) ([]Transfer, error) {
	return s.scanRange(ctx, chain, fromBlock, toBlock, tokens, senders, true)
}

func (s *EVMScanner) scanRange(ctx context.Context, chain string, fromBlock, toBlock uint64, tokens []string, recipients []string, outgoing bool) ([]Transfer, error) {
	if len(recipients) == 0 || len(tokens) == 0 || toBlock < fromBlock {
		return nil, nil
	}
	recipientSet := make(map[string]struct{}, len(recipients))
	for _, r := range recipients {
		recipientSet[strings.ToLower(r)] = struct{}{}
	}

	// One budget per scanRange, shared by every concurrent batch: a dense or slow
	// range may be subdivided, but a node failing every request can never turn
	// one pass into a request storm.
	budget := newGetLogsBudget(maxGetLogsSplitRequests)
	witness := newSyncWitness(s.pool)
	results := make([]batchResult, (len(recipients)+batchSize-1)/batchSize)
	runBatches(ctx, len(results), s.scanWorkers(), func(index int) {
		start := index * batchSize
		end := min(start+batchSize, len(recipients))
		result := batchResult{done: true}
		if logs, err := s.getLogsAdaptive(ctx, chain, fromBlock, toBlock, tokens, recipients[start:end], outgoing, index, budget, witness); err != nil {
			result.err = err
		} else {
			result.transfers = logsToTransfers(chain, logs, recipientSet, outgoing)
		}
		results[index] = result
	})

	// Assemble strictly by batch index, so neither the transfer order nor the
	// joined error order depends on which request finished first.
	var out []Transfer
	var scanErrs []error
	unscanned := -1
	for index, result := range results {
		switch {
		case !result.done:
			// Never started because ctx ended first. Skipping it silently would
			// report a partly scanned range as complete.
			if unscanned < 0 {
				unscanned = index
			}
		case result.err != nil:
			scanErrs = append(scanErrs, result.err)
		default:
			out = append(out, result.transfers...)
		}
	}
	if unscanned >= 0 {
		scanErrs = append(scanErrs, unscannedBatchError(ctx, chain, fromBlock, toBlock, unscanned, len(recipients)))
	}
	return out, errors.Join(scanErrs...)
}

// batchResult is the outcome of one address batch. Each slot is written only by
// the goroutine that ran that batch and read only after every worker returned.
// done stays false for a batch that never started.
type batchResult struct {
	done      bool
	transfers []Transfer
	err       error
}

// runBatches calls run(0) through run(n-1) with at most workers calls active.
// Indexes start in ascending order and none starts once ctx is done, so the
// batches that never ran are always a suffix. A started batch always runs to
// completion: run is expected to return promptly when ctx ends. It returns only
// after every started call has returned, so no goroutine outlives it. A panic in
// run is re-raised here, on the caller's goroutine, with the worker's stack.
func runBatches(ctx context.Context, n, workers int, run func(index int)) {
	workers = min(workers, n)
	if workers <= 1 {
		for index := 0; index < n && ctx.Err() == nil; index++ {
			run(index)
		}
		return
	}
	var (
		next     atomic.Int64
		panicked atomic.Pointer[batchPanic]
		wg       sync.WaitGroup
	)
	for range workers {
		wg.Go(func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					panicked.CompareAndSwap(nil, &batchPanic{value: recovered, stack: debug.Stack()})
				}
			}()
			for panicked.Load() == nil && ctx.Err() == nil {
				index := int(next.Add(1) - 1)
				if index >= n {
					return
				}
				run(index)
			}
		})
	}
	wg.Wait()
	if failure := panicked.Load(); failure != nil {
		panic(failure)
	}
}

// batchPanic carries a worker's panic and its original stack to the goroutine
// that called runBatches, where the stack would otherwise be lost.
type batchPanic struct {
	value any
	stack []byte
}

func (p *batchPanic) Error() string {
	return fmt.Sprintf("panic in getLogs batch worker: %v\n%s", p.value, p.stack)
}

// errBatchNotScanned marks a batch that never ran although ctx reported no
// error. runBatches cannot produce that; it exists so an unscanned batch can
// never be mistaken for a scanned one.
var errBatchNotScanned = errors.New("address batch was not scanned")

// unscannedBatchError reports the first batch that never started. The cause is
// shaped like the transport failure an interrupted request would have produced,
// so callers that classify a timeout or cancellation by error type (for example
// the outflow-security fallback) treat both the same way.
func unscannedBatchError(ctx context.Context, chain string, from, to uint64, batch, recipients int) error {
	cause := errBatchNotScanned
	if ctxErr := ctx.Err(); ctxErr != nil {
		cause = rpc.NewTransportFailure(ctxErr)
	}
	return &LogsError{Chain: chain, FromBlock: from, ToBlock: to, Batch: batch,
		Addresses: min(batchSize, recipients-batch*batchSize), Err: cause}
}

// logsToTransfers turns one batch's logs into transfers, in log order. It keeps
// the filtering the scan always had: removed or malformed logs, logs whose
// watched side is not in the set, and unparsable amounts are dropped.
func logsToTransfers(chain string, logs []evmLog, watchedSet map[string]struct{}, outgoing bool) []Transfer {
	var out []Transfer
	for _, log := range logs {
		if log.Removed || len(log.Topics) < 3 {
			continue
		}
		to := topicToAddress(log.Topics[2])
		from := topicToAddress(log.Topics[1])
		watchedAddress := to
		if outgoing {
			watchedAddress = from
		}
		if _, ok := watchedSet[strings.ToLower(watchedAddress)]; !ok {
			continue
		}
		amount, ok := new(big.Int).SetString(strings.TrimPrefix(log.Data, "0x"), 16)
		if !ok {
			continue
		}
		blockNumber := parseHexUint(log.BlockNumber)
		logIndex := int(parseHexUint(log.LogIndex))
		out = append(out, Transfer{
			Chain:       chain,
			TxHash:      strings.ToLower(log.TxHash),
			LogIndex:    logIndex,
			From:        from,
			To:          to,
			Token:       strings.ToLower(log.Address),
			Amount:      amount,
			BlockNumber: blockNumber,
			BlockHash:   log.BlockHash,
		})
	}
	return out
}

// maxGetLogsSplitRequests caps the extra eth_getLogs requests one scanRange may
// spend subdividing failed queries. Public BSC nodes time out or answer "query
// exceeds max results" on dense ranges; halving recovers those, while the cap
// keeps a node that rejects everything from being hammered every pass.
const maxGetLogsSplitRequests = 64

// getLogsBudget is the shared allowance of extra eth_getLogs requests. Batches
// run concurrently, so a plain counter would let two of them both pass the
// check and then both spend, driving it negative and exceeding the cap.
type getLogsBudget struct {
	remaining atomic.Int64
}

func newGetLogsBudget(requests int) *getLogsBudget {
	budget := &getLogsBudget{}
	budget.remaining.Store(int64(requests))
	return budget
}

// reserve takes n requests all at once or none at all, and never lets the
// balance go below zero, however many batches race for it.
func (b *getLogsBudget) reserve(n int64) bool {
	for {
		current := b.remaining.Load()
		if current < n {
			return false
		}
		if b.remaining.CompareAndSwap(current, current-n) {
			return true
		}
	}
}

// minSplitAddresses stops halving the address filter. A single-block query with
// this few topics that still fails is an endpoint problem, not query size.
const minSplitAddresses = 25

// LogsError locates the eth_getLogs query that still failed after adaptive
// splitting. Every field is numeric or a configured chain name and the wrapped
// cause comes from rpc.Pool, so it never adds an endpoint or node-supplied text.
type LogsError struct {
	Chain              string
	FromBlock, ToBlock uint64
	Batch              int // index of the address batch (batchSize each) in the scanned list
	Addresses          int // addresses in the failing, possibly halved, filter
	Err                error
}

func (e *LogsError) Error() string {
	return fmt.Sprintf("getLogs %s %d-%d batch %d (%d addresses): %v", e.Chain, e.FromBlock, e.ToBlock, e.Batch, e.Addresses, e.Err)
}

func (e *LogsError) Unwrap() error { return e.Err }

// syncWitness proves, once per scanRange, that the node which answered an
// eth_getLogs query had already reached the range's last block. An endpoint
// behind the tip answers only for the blocks it has, and that empty answer is
// indistinguishable from a complete range without transfers. Heads only grow,
// so a head observed during this range stays a valid lower bound for every
// later requirement in it; one observation per endpoint keeps the proof from
// costing more than the scan itself.
type syncWitness struct {
	pool  *rpc.Pool
	mu    sync.Mutex
	heads map[string]uint64
}

func newSyncWitness(pool *rpc.Pool) *syncWitness {
	return &syncWitness{pool: pool, heads: make(map[string]uint64)}
}

// require reports a stale-endpoint failure unless endpoint is known to have
// reached height. The first check of an endpoint costs one eth_blockNumber on
// that exact node; later checks in the same range reuse the observed head.
func (w *syncWitness) require(ctx context.Context, endpoint string, height uint64) error {
	w.mu.Lock()
	head, known := w.heads[endpoint]
	w.mu.Unlock()
	if known {
		if head >= height {
			return nil
		}
		return &rpc.Failure{Kind: "stale"}
	}
	observed, err := w.pool.RequireSyncedTo(ctx, endpoint, height)
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.heads[endpoint] = observed
	w.mu.Unlock()
	return nil
}

// getLogsAdaptive returns logs only when the complete [from,to] x addresses
// space was covered. Recoverable remote query rejections are retried as two
// halves: the block range, then the address filter. Rate limits are not split.
// Splits are disjoint, so no log repeats. The halves of one batch run one after
// the other; only the budget is shared with the batches running beside it.
func (s *EVMScanner) getLogsAdaptive(ctx context.Context, chain string, from, to uint64, tokens, addresses []string, outgoing bool, batch int, budget *getLogsBudget, witness *syncWitness) ([]evmLog, error) {
	logs, err := s.getLogs(ctx, from, to, tokens, addresses, outgoing, witness)
	if err == nil {
		return logs, nil
	}
	var failure *rpc.Failure
	canSplitFailure := errors.As(err, &failure) &&
		(failure.Kind == "response" || (failure.Kind == "remote" && failure.RPCCode != 429))
	// reserve is last: it is the only condition with a side effect, so it must
	// run only once every other reason to split already holds. It takes both
	// halves before either can spend more.
	canSplit := ctx.Err() == nil && canSplitFailure &&
		(to > from || len(addresses) > minSplitAddresses) && budget.reserve(2)
	if !canSplit {
		return nil, &LogsError{Chain: chain, FromBlock: from, ToBlock: to, Batch: batch, Addresses: len(addresses), Err: err}
	}
	if to > from {
		mid := from + (to-from)/2
		left, leftErr := s.getLogsAdaptive(ctx, chain, from, mid, tokens, addresses, outgoing, batch, budget, witness)
		if leftErr != nil {
			return nil, leftErr
		}
		right, rightErr := s.getLogsAdaptive(ctx, chain, mid+1, to, tokens, addresses, outgoing, batch, budget, witness)
		if rightErr != nil {
			return nil, rightErr
		}
		return append(left, right...), nil
	}
	half := len(addresses) / 2
	left, leftErr := s.getLogsAdaptive(ctx, chain, from, to, tokens, addresses[:half], outgoing, batch, budget, witness)
	if leftErr != nil {
		return nil, leftErr
	}
	right, rightErr := s.getLogsAdaptive(ctx, chain, from, to, tokens, addresses[half:], outgoing, batch, budget, witness)
	if rightErr != nil {
		return nil, rightErr
	}
	return append(left, right...), nil
}

// getLogs issues exactly one eth_getLogs filter for the given addresses.
func (s *EVMScanner) getLogs(ctx context.Context, from, to uint64, tokens, addresses []string, outgoing bool, witness *syncWitness) ([]evmLog, error) {
	// topic0 = Transfer, topic2 = to (any of the watched addresses); outflows
	// filter topic1 = from instead.
	topic := make([]any, 0, len(addresses))
	for _, addr := range addresses {
		topic = append(topic, padTopic(addr))
	}
	params := getLogsParams{
		FromBlock: fmt.Sprintf("0x%x", from),
		ToBlock:   fmt.Sprintf("0x%x", to),
		Address:   tokens,
		Topics:    [][]any{{transferTopic}, nil, topic},
	}
	if outgoing {
		params.Topics = [][]any{{transferTopic}, topic}
	}
	raw, endpoint, err := s.pool.CallEndpoint(ctx, "eth_getLogs", []any{params})
	if err != nil {
		return nil, err
	}
	var logs []evmLog
	if err := json.Unmarshal(raw, &logs); err != nil {
		return nil, fmt.Errorf("getLogs decode: %w", err)
	}
	if logs == nil {
		return nil, &rpc.Failure{Kind: "response"}
	}
	// An empty array is the only answer that cannot be told apart from a node
	// that has not reached toBlock yet: it would report the blocks it knows and
	// drop the rest. A caught-up node proving its head first turns that silent
	// skip into an error, so the range is retried instead of being written down
	// as scanned. A non-empty answer names real logs at their real heights and
	// needs no extra request.
	if len(logs) == 0 {
		if err := witness.require(ctx, endpoint, to); err != nil {
			return nil, err
		}
	}
	return logs, nil
}

// padTopic left-pads a 20-byte address to a 32-byte topic.
func padTopic(address string) string {
	trimmed := strings.TrimPrefix(strings.ToLower(address), "0x")
	return "0x" + strings.Repeat("0", 64-len(trimmed)) + trimmed
}

// topicToAddress extracts a 20-byte address from a 32-byte topic.
func topicToAddress(topic string) string {
	trimmed := strings.TrimPrefix(topic, "0x")
	if len(trimmed) < 40 {
		return "0x" + trimmed
	}
	return "0x" + trimmed[len(trimmed)-40:]
}

func parseHexUint(hex string) uint64 {
	hex = strings.TrimPrefix(hex, "0x")
	if hex == "" {
		return 0
	}
	var value uint64
	_, _ = fmt.Sscanf(hex, "%x", &value)
	return value
}
