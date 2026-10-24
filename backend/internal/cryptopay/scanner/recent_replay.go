package scanner

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// replayRecentRange is independent of the durable, forward-only cursor. An RPC
// node can expose a block before its log index is complete, and an address can
// become assigned after a pass took its recipient snapshot. Rechecking at most
// one provider-sized batch every 20 seconds recovers both cases. Ledger and
// fulfillment identities still prevent duplicate credit on every replay.
func (w *Worker) replayRecentRange(ctx context.Context, scanner *EVMScanner, pool *rpc.Pool, chain ChainConfig, through, maxRange uint64, recipients []string) error {
	if through == 0 || len(recipients) == 0 {
		return nil
	}
	w.replayMu.Lock()
	if time.Since(w.replayedAt[chain.Chain]) < 20*time.Second {
		w.replayMu.Unlock()
		return nil
	}
	if w.replayedAt == nil {
		w.replayedAt = make(map[string]time.Time)
	}
	w.replayedAt[chain.Chain] = time.Now()
	w.replayMu.Unlock()
	span := min(through, min(maxRange, uint64(2000)))
	replayCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := w.processScanRange(replayCtx, scanner, pool, chain, through-span+1, through, recipients); err != nil {
		return fmt.Errorf("recent deposit replay %s: %w", chain.Chain, err)
	}
	return nil
}
