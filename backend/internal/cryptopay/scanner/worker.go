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

// ScanOnce runs one full pass; exposed for tests and manual triggering.
func (w *Worker) ScanOnce(ctx context.Context) error {
	for _, chain := range w.chains {
		if err := w.scanChain(ctx, chain); err != nil {
			return err
		}
	}
	return nil
}

// chainDecimals resolves a token's decimals from the chain config, falling
// back to the stablecoin default. Lookup is case-insensitive because the
// scanner stores contract addresses lowercase while config may not be.
func (w *Worker) chainDecimals(chain, token string) int {
	for _, c := range w.chains {
		if c.Chain != chain {
			continue
		}
		for contract, decimals := range c.Decimals {
			if strings.EqualFold(contract, token) {
				return decimals
			}
		}
	}
	return 6
}

func (w *Worker) scanChain(ctx context.Context, chain ChainConfig) error {
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
			// Decimals must come from the chain config up front: the pending
			// path stores and later rebuilds the raw amount from it, and a
			// wrong exponent (the old hardcoded 6) inflated 18-decimal
			// deposits by 1e12 on the retry pass.
			decimals, watched := watchedDecimals(chain, transfer.Token)
			// A token outside the watched set must never be credited: a
			// misconfigured or hostile RPC can return any log it likes.
			if !watched {
				slog.Warn("[cryptopay] ignored transfer for unwatched token",
					"chain", transfer.Chain, "tx", transfer.TxHash, "token", transfer.Token)
				continue
			}
			// Every observation is either fully verified and processed, or
			// durably pending before the batch cursor may advance.
			receipt, verifyErr := VerifyTransaction(ctx, pool, transfer.TxHash)
			if verifyErr == nil {
				verifyErr = verifyCanonicalBlock(ctx, pool, receipt)
			}
			if verifyErr == nil {
				verifyErr = verifyReceiptTransfer(transfer, receipt)
			}
			final := false
			if verifyErr == nil {
				final, verifyErr = transactionIsFinal(ctx, pool, receipt.BlockNumber, chain.Confirmations)
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
				// Do not advance the cursor past a transfer we failed to
				// persist: the next pass would never see it again and the
				// deposit would be lost silently.
				return err
			}
		}
		if err := w.cursor.Save(ctx, chain.Chain, to); err != nil {
			return err
		}
		from = to + 1
	}
	return nil
}
