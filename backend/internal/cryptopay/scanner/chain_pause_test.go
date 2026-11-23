package scanner

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestChainPauseDelayGrowsAndIsCapped(t *testing.T) {
	interval := 5 * time.Second
	if got := chainPauseDelay(0, interval); got != 0 {
		t.Fatalf("no failures, no delay: %v", got)
	}
	if got := chainPauseDelay(1, interval); got != 0 {
		t.Fatalf("the first failure is not delayed (scanAll already retries once): %v", got)
	}
	within := func(got, want time.Duration) bool { // +-20% jitter
		return got >= want-want/5 && got <= want+want/5
	}
	for failures, want := range map[int]time.Duration{2: 5 * time.Second, 3: 10 * time.Second, 4: 20 * time.Second, 5: 40 * time.Second} {
		for i := 0; i < 50; i++ {
			if got := chainPauseDelay(failures, interval); !within(got, want) {
				t.Fatalf("failures=%d delay=%v, want about %v", failures, got, want)
			}
		}
	}
	for _, failures := range []int{6, 7, 20, 1000} {
		for i := 0; i < 50; i++ {
			if got := chainPauseDelay(failures, interval); !within(got, maxChainPause) {
				t.Fatalf("failures=%d delay=%v must stay near the %v cap", failures, got, maxChainPause)
			}
		}
	}
	if got := chainPauseDelay(3, 0); got == 0 {
		t.Fatal("a worker without an interval still paces stalled chains")
	}
}

func TestHeadStageFailedOnlyMatchesTheHeadLookup(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"head", stageErr("polygon", "head", 0, 0, boom), true},
		{"head joined with others", errors.Join(stageErr("polygon", "security", 1, 2, boom), stageErr("polygon", "head", 0, 0, boom)), true},
		{"deposit", stageErr("polygon", "deposit", 1, 2, boom), false},
		{"security", stageErr("polygon", "security", 1, 2, boom), false},
	} {
		if got := headStageFailed(tc.err); got != tc.want {
			t.Errorf("%s: headStageFailed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// chainAt builds the per-pass coverage a scan of `polygon` leaves behind: the
// cursor it started from and the head it saw.
func chainAt(w *Worker, head, startCursor uint64) ChainConfig {
	w.noteHead("polygon", head)
	w.noteCursor("polygon", startCursor)
	return ChainConfig{Chain: "polygon", Confirmations: 5}
}

func TestForwardStalledNeedsPendingWorkAndNoProgress(t *testing.T) {
	w := &Worker{}
	chain := chainAt(w, 1000, 100) // confirmed head 995, cursor 100: 895 blocks waiting
	if !w.forwardStalled(chain) {
		t.Fatal("blocks were waiting and the cursor did not move")
	}
	w.noteCursor("polygon", 150) // the pass moved it, then failed later
	if w.forwardStalled(chain) {
		t.Fatal("a pass that advanced the cursor is progress, not a stall")
	}

	caughtUp := &Worker{}
	chain = chainAt(caughtUp, 1000, 995) // nothing beyond the cursor
	if caughtUp.forwardStalled(chain) {
		t.Fatal("a caught-up chain has nothing to stall on")
	}

	blind := &Worker{} // never learned its head or cursor this pass
	if blind.forwardStalled(ChainConfig{Chain: "polygon", Confirmations: 5}) {
		t.Fatal("without evidence of waiting work there is no stall")
	}
}

func TestNoteChainPassPausesAfterRepeatedStallsOnly(t *testing.T) {
	w := &Worker{interval: 5 * time.Second}
	chain := chainAt(w, 1000, 100)
	now := time.Date(2026, 11, 24, 2, 0, 0, 0, time.UTC)
	stalled := stageErr("polygon", "deposit", 101, 200, errors.New("rpc down"))

	w.noteChainPass(chain, stalled, now)
	if _, paused := w.forwardPause("polygon", now); paused {
		t.Fatal("one stall must not pause the forward scan")
	}
	w.noteChainPass(chain, stalled, now)
	if _, paused := w.forwardPause("polygon", now); !paused {
		t.Fatal("the second consecutive stall must pause it")
	}
	if _, paused := w.forwardPause("polygon", now.Add(2*time.Minute)); paused {
		t.Fatal("a pause is bounded by the cap and must lapse")
	}
	if _, paused := w.forwardPause("bsc", now); paused {
		t.Fatal("pausing one chain must not touch another")
	}
	// A batch-level security failure that holds the cursor is a stall too, whatever its stage label says.
	other := &Worker{interval: 5 * time.Second}
	chain = chainAt(other, 1000, 100)
	for i := 0; i < 2; i++ {
		other.noteChainPass(chain, stageErr("polygon", "security", 101, 200, errors.New("guard unavailable")), now)
	}
	if _, paused := other.forwardPause("polygon", now); !paused {
		t.Fatal("a security-stage failure that froze the cursor must pause the chain")
	}
	// recovery clears the streak completely, so the next stall starts from zero again
	w.noteChainPass(chain, nil, now)
	w.noteChainPass(chain, stalled, now)
	if _, paused := w.forwardPause("polygon", now); paused {
		t.Fatal("a successful pass must reset the stall streak")
	}
}

func TestNoteChainPassIgnoresFailuresThatLeftTheCursorFree(t *testing.T) {
	now := time.Now()
	// caught up: head-window and replay failures are not a stall
	w := &Worker{interval: 5 * time.Second}
	chain := chainAt(w, 1000, 995)
	for i := 0; i < 6; i++ {
		w.noteChainPass(chain, stageErr("polygon", "replay", 1, 2, errors.New("late replay failed")), now)
		w.noteChainPass(chain, stageErr("polygon", "security", 1, 2, errors.New("head window failed")), now)
	}
	if _, paused := w.forwardPause("polygon", now); paused {
		t.Fatal("failures on a chain with nothing to move must never pause it")
	}
	// work was waiting but the pass moved the cursor before failing: progress
	progressed := &Worker{interval: 5 * time.Second}
	chain = chainAt(progressed, 1000, 100)
	progressed.noteCursor("polygon", 300)
	for i := 0; i < 6; i++ {
		progressed.noteChainPass(chain, stageErr("polygon", "deposit", 301, 400, errors.New("timed out after progress")), now)
	}
	if _, paused := progressed.forwardPause("polygon", now); paused {
		t.Fatal("a pass that advanced the cursor must not be treated as a stall")
	}
	// a failed head lookup says nothing about the forward scan: the pass never reached it
	blind := &Worker{interval: 5 * time.Second}
	for i := 0; i < 6; i++ {
		blind.noteChainPass(ChainConfig{Chain: "polygon", Confirmations: 5}, stageErr("polygon", "head", 0, 0, errors.New("no head")), now)
	}
	if _, paused := blind.forwardPause("polygon", now); paused {
		t.Fatal("head lookup failures alone must never pause the forward scan")
	}
}

// The Polygon incident: a rate limited head call about once a minute renewed the
// pause on every tick, so the pause never lapsed and the forward scan - which the
// head stage does not feed - starved for hours. A failed head lookup is neutral: it
// must leave an active pause exactly as it is and let it lapse on schedule.
func TestHeadLookupFailuresDoNotRenewAnActivePause(t *testing.T) {
	w := &Worker{interval: 5 * time.Second}
	chain := chainAt(w, 1000, 100)
	now := time.Date(2026, 11, 24, 2, 0, 0, 0, time.UTC)
	stalled := stageErr("polygon", "deposit", 101, 200, errors.New("rpc down"))
	w.noteChainPass(chain, stalled, now)
	w.noteChainPass(chain, stalled, now)
	w.replayMu.Lock()
	before := *w.chainPause["polygon"]
	w.replayMu.Unlock()

	for i := 0; i < 30; i++ {
		headFailure := stageErr("polygon", "head", 0, 0, errors.New("HTTP 429"))
		w.noteChainPass(chain, headFailure, now.Add(time.Duration(i)*time.Second))
		w.noteChainPass(chain, errors.Join(headFailure, stageErr("polygon", "observe", 0, 0, errors.New("429"))), now.Add(time.Duration(i)*time.Second))
	}
	w.replayMu.Lock()
	after := *w.chainPause["polygon"]
	w.replayMu.Unlock()
	if after != before {
		t.Fatalf("head lookup failures changed the pause: %+v -> %+v", before, after)
	}
	if _, paused := w.forwardPause("polygon", before.next.Add(time.Millisecond)); paused {
		t.Fatal("the pause must lapse on schedule however often the head lookup fails meanwhile")
	}
	// a real forward stall after the pause lapsed still counts, as before
	w.noteChainPass(chain, stalled, before.next.Add(time.Second))
	w.replayMu.Lock()
	renewed := *w.chainPause["polygon"]
	w.replayMu.Unlock()
	if renewed.failures != before.failures+1 {
		t.Fatalf("a genuine forward stall must still extend the streak: %+v -> %+v", before, renewed)
	}
}

// A pass whose forward scan was skipped on purpose made no attempt: it must not
// count as one more failure (that would run the delay up to the cap within five
// ticks) and must not clear the pause either.
func TestSkippedForwardAttemptsDoNotChangeThePause(t *testing.T) {
	w := &Worker{interval: 5 * time.Second}
	chain := chainAt(w, 1000, 100)
	now := time.Date(2026, 11, 24, 2, 0, 0, 0, time.UTC)
	stalled := stageErr("polygon", "deposit", 101, 200, errors.New("rpc down"))
	w.noteChainPass(chain, stalled, now)
	w.noteChainPass(chain, stalled, now)
	w.replayMu.Lock()
	before := *w.chainPause["polygon"]
	w.replayMu.Unlock()

	skipped := stageErr("polygon", "deposit", 101, 995, &chainPausedError{failures: 2, wait: 3 * time.Second})
	for i := 0; i < 10; i++ {
		w.noteChainPass(chain, skipped, now.Add(time.Duration(i)*time.Second))
	}
	w.replayMu.Lock()
	after := *w.chainPause["polygon"]
	w.replayMu.Unlock()
	if after != before {
		t.Fatalf("skipped attempts changed the pause: %+v -> %+v", before, after)
	}
}

func countSteps(pg *scannerMockPG, step string) int {
	_, _, events, _ := pg.snapshot()
	n := 0
	for _, event := range events {
		if event.step == step {
			n++
		}
	}
	return n
}

// End to end through the real pass loop. A chain whose forward scan keeps failing
// without moving the cursor stops spending requests on the forward batches, but
// its head stage still runs every pass and says so, manual scans always run, and
// one healthy pass resets everything.
func TestStalledForwardScanBacksOffWhileHeadStageAndManualScansKeepRunning(t *testing.T) {
	pg := &scannerMockPG{}
	w, fixture := scannerLockFixture(t, pg)
	setFailure := func(method string) {
		fixture.mu.Lock()
		fixture.httpFail = method
		fixture.mu.Unlock()
	}
	setFailure("eth_getLogs") // every forward batch is answered with HTTP 503
	timer := context.WithValue(t.Context(), periodicPassKey{}, true)

	for i := 0; i < 2; i++ {
		if err := w.RunOnce(timer); err == nil {
			t.Fatal("the failing forward scan must surface")
		}
	}
	logsWhileStalling := fixture.callCount("eth_getLogs")
	if logsWhileStalling == 0 {
		t.Fatal("the first attempts must really try the forward batches")
	}
	if _, paused := w.forwardPause("bsc", time.Now()); !paused {
		t.Fatal("two stalled passes must pause the forward scan")
	}

	// A timer pass now skips the forward batches - no more getLogs - but still
	// reads the head and reports the skipped attempt, so the chain stays visible
	// as failing instead of going quiet.
	headsBefore := fixture.callCount("eth_blockNumber")
	err := w.RunOnce(timer)
	var skipped *chainPausedError
	if !errors.As(err, &skipped) {
		t.Fatalf("a skipped forward attempt must be reported, got %v", err)
	}
	if got := fixture.callCount("eth_getLogs"); got != logsWhileStalling {
		t.Fatalf("a paused forward scan must not spend getLogs requests: %d -> %d", logsWhileStalling, got)
	}
	if fixture.callCount("eth_blockNumber") <= headsBefore {
		t.Fatal("the head stage must keep running while the forward scan waits")
	}

	// A manual scan is an operator asking to try now.
	if err := w.RunOnce(t.Context()); err == nil {
		t.Fatal("the manual scan runs and hits the failure")
	}
	if got := fixture.callCount("eth_getLogs"); got <= logsWhileStalling {
		t.Fatalf("manual scans must bypass the pause: getLogs %d -> %d", logsWhileStalling, got)
	}

	// Once the pause lapses and the node answers again, one pass clears everything.
	setFailure("")
	w.replayMu.Lock()
	w.chainPause["bsc"].next = time.Now().Add(-time.Second)
	w.replayMu.Unlock()
	if err := w.RunOnce(timer); err != nil {
		t.Fatalf("healthy pass: %v", err)
	}
	if _, paused := w.forwardPause("bsc", time.Now()); paused {
		t.Fatal("a successful pass must clear the pause")
	}
	w.replayMu.Lock()
	_, tracked := w.chainPause["bsc"]
	w.replayMu.Unlock()
	if tracked {
		t.Fatal("a successful pass must drop the chain's stall record")
	}
}
