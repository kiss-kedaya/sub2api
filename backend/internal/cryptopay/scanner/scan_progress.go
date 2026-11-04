package scanner

import (
	"context"
	"fmt"
)

// ChainScanProgress is a read-only view of one chain's catch-up position.
//
// Cursor is the durable boundary: every block at or below it was scanned and
// saved. ConfirmedHead is the newest block the scanner will treat as final
// (head minus the configured confirmations) according to the most recent
// head this worker sampled. HeadKnown is false until a pass has sampled a
// head for the chain, so a caller can tell "caught up" from "not measured".
type ChainScanProgress struct {
	Chain         string
	Cursor        uint64
	ConfirmedHead uint64
	HeadKnown     bool
}

// Remaining is the number of confirmed blocks the cursor has not reached yet.
func (p ChainScanProgress) Remaining() uint64 {
	if !p.HeadKnown || p.Cursor >= p.ConfirmedHead {
		return 0
	}
	return p.ConfirmedHead - p.Cursor
}

// CaughtUp reports a measured chain whose cursor has reached the confirmed
// head. An unmeasured chain is never reported as caught up.
func (p ChainScanProgress) CaughtUp() bool {
	return p.HeadKnown && p.Cursor >= p.ConfirmedHead
}

// ScanProgress reports every scanned chain's durable cursor and the head the
// latest pass observed. It performs no RPC call and never writes: heads come
// from the per-pass record that noteHead already maintains, cursors from the
// same CursorStore the scan itself uses. Chains without an RPC pool are
// skipped because a pass skips them too, so they could never advance.
func (w *Worker) ScanProgress(ctx context.Context) ([]ChainScanProgress, error) {
	if w == nil || w.cursor == nil {
		return nil, fmt.Errorf("scanner progress is unavailable")
	}
	out := make([]ChainScanProgress, 0, len(w.chains))
	for _, chain := range w.chains {
		if w.pools[chain.Chain] == nil {
			continue
		}
		cursor, err := w.cursor.Load(ctx, chain.Chain)
		if err != nil {
			return nil, err
		}
		progress := ChainScanProgress{Chain: chain.Chain, Cursor: cursor}
		if head, ok := w.passHead(chain.Chain); ok {
			progress.HeadKnown = true
			// A head at or below the confirmation depth leaves nothing final to
			// scan; the pass itself returns early in that case.
			if head > chain.Confirmations {
				progress.ConfirmedHead = head - chain.Confirmations
			}
		}
		out = append(out, progress)
	}
	return out, nil
}
