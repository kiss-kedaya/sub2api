package sweeper

import (
	"context"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

const sweepConfirmationWait = 90 * time.Second

// RunAndWait keeps the same operation, database lock and signing authorization
// alive while its persisted gas/token transactions reach confirmation. A
// pending receipt is a normal stage, not the end of a manual sweep. There is
// no detached worker and no new authority after the caller's context expires.
// Slow chains return the durable pending result after the shared wait budget;
// a subsequent authenticated run resumes the exact persisted transactions.
func (e *Executor) RunAndWait(ctx context.Context, dryRun bool) (*Summary, error) {
	if dryRun {
		return e.Run(ctx, true)
	}
	run := *e
	run.confirmationDeadline = time.Now().Add(sweepConfirmationWait)
	run.confirmationInterval = 2 * time.Second
	return run.Run(ctx, false)
}

// Polling only observes the existing hash. It never signs, replaces a nonce
// or rebroadcasts. Receipt errors still stop progress, and the same canonical
// block, confirmation depth and revert checks apply to every observation.
func (e *Executor) waitForReceipt(ctx context.Context, pool *rpc.Pool, hash string, required uint64, state receiptState) (receiptState, error) {
	if e.confirmationDeadline.IsZero() || (state != receiptAbsent && state != receiptWaiting) {
		return state, nil
	}
	interval := e.confirmationInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	waitCtx, cancel := context.WithDeadline(ctx, e.confirmationDeadline)
	defer cancel()
	for {
		timer := time.NewTimer(interval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return state, ctx.Err()
			}
			return state, nil // wait budget exhausted; funds remain pending
		case <-timer.C:
		}
		observed, err := checkReceipt(waitCtx, pool, hash, required)
		if err != nil {
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
				return state, nil
			}
			return state, err
		}
		state = observed
		if state == receiptConfirmed || state == receiptReverted {
			return state, nil
		}
	}
}
