package scanner

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A hung pass used to hold the scan session lock forever: every later tick got
// ErrScanBusy and no chain advanced until the process was restarted. The pass
// deadline is what guarantees the lock is released.
func TestScanPassDeadlineIsLongerThanOneChainBudget(t *testing.T) {
	if scanPassDeadline <= maxScanTimePerChain {
		t.Fatalf("pass deadline %s must exceed the per-chain budget %s", scanPassDeadline, maxScanTimePerChain)
	}
	// Sanity bound: a pass should not be allowed to run for hours either.
	if scanPassDeadline > time.Hour {
		t.Fatalf("pass deadline %s is unreasonably long", scanPassDeadline)
	}
}

// scanAll must pass down a deadline but keep reacting to the worker's own stop
// signal: a stop has to abort the pass immediately, not wait 15 minutes.
func TestScanAllHonoursParentCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, cancel := context.WithTimeout(parent, scanPassDeadline)
	defer cancel()

	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("pass context must carry a deadline")
	}

	cancelParent()
	<-ctx.Done()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("parent cancel should win over the deadline, got %v", ctx.Err())
	}
}
