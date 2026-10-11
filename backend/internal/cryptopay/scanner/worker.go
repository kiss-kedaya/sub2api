package scanner

import (
	"context"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// ChainConfig describes one chain's scan parameters.
type ChainConfig struct {
	Chain         string
	Tokens        []string // contract addresses to watch
	Confirmations uint64
	Decimals      map[string]int // token contract -> decimals
}

// DepositHandler receives observed transfers (the ledger).
type DepositHandler interface {
	Process(ctx context.Context, t Transfer, decimals int) (bool, error)
}

// Worker scans all configured EVM chains on an interval.
type Worker struct {
	pools       map[string]*rpc.Pool
	chains      []ChainConfig
	cursor      *CursorStore
	handler     DepositHandler
	addresses   func(ctx context.Context, chain string) ([]string, error)
	interval    time.Duration
	maxRange    uint64
	cancel      context.CancelFunc
	done        chan struct{}
}

// NewWorker builds the scan loop. addresses returns the watched recipient
// list per chain (cached by the caller).
func NewWorker(
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
		pools: pools, chains: chains, cursor: cursor, handler: handler,
		addresses: addresses, interval: interval, maxRange: maxRange,
		done: make(chan struct{}),
	}
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
	for from := last + 1; from <= confirmed; {
		to := from + w.maxRange - 1
		if to > confirmed {
			to = confirmed
		}
		transfers, err := scanner.ScanRange(ctx, chain.Chain, from, to, chain.Tokens, recipients)
		if err != nil {
			return err
		}
		for _, transfer := range transfers {
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
