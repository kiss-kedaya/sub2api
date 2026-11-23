package scanner

import (
	"context"
	"errors"
	"sync/atomic"
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
		t.Fatal("a worker without an interval still paces failing chains")
	}
}

func TestForwardStageFailedOnlyCountsStagesThatHoldTheCursor(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"deposit", stageErr("polygon", "deposit", 1, 2, boom), true},
		{"head", stageErr("polygon", "head", 0, 0, boom), true},
		{"untagged error is a deposit-stage failure after tagStage", tagStage("polygon", "deposit", boom), true},
		{"security head window only", stageErr("polygon", "security", 1, 2, boom), false},
		{"observe only", stageErr("polygon", "observe", 1, 2, boom), false},
		{"replay only", stageErr("polygon", "replay", 1, 2, boom), false},
		{"live window only", stageErr("polygon", "live", 1, 2, boom), false},
		{"security plus deposit", errors.Join(stageErr("polygon", "security", 1, 2, boom), stageErr("polygon", "deposit", 3, 4, boom)), true},
	} {
		if got := forwardStageFailed(tc.err); got != tc.want {
			t.Errorf("%s: forwardStageFailed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestNoteChainPassPausesOnlyAfterRepeatedStalls(t *testing.T) {
	w := &Worker{interval: 5 * time.Second}
	now := time.Date(2026, 11, 24, 2, 0, 0, 0, time.UTC)
	stalled := stageErr("polygon", "deposit", 1, 2, errors.New("rpc down"))

	w.noteChainPass("polygon", stalled, now)
	if w.chainPaused("polygon", now) {
		t.Fatal("one failure must not pause the chain")
	}
	w.noteChainPass("polygon", stalled, now)
	if !w.chainPaused("polygon", now) {
		t.Fatal("the second consecutive stall must pause it")
	}
	if w.chainPaused("polygon", now.Add(2*time.Minute)) {
		t.Fatal("a pause is bounded by the cap and must lapse")
	}
	if w.chainPaused("bsc", now) {
		t.Fatal("pausing one chain must not touch another")
	}
	// recovery clears the streak completely, so the next failure starts from zero again
	w.noteChainPass("polygon", nil, now)
	w.noteChainPass("polygon", stalled, now)
	if w.chainPaused("polygon", now) {
		t.Fatal("a successful pass must reset the failure streak")
	}
}

func TestNoteChainPassIgnoresFailuresThatLeftTheCursorFree(t *testing.T) {
	w := &Worker{interval: 5 * time.Second}
	now := time.Now()
	for i := 0; i < 6; i++ {
		w.noteChainPass("polygon", stageErr("polygon", "replay", 1, 2, errors.New("late replay failed")), now)
		w.noteChainPass("polygon", stageErr("polygon", "security", 1, 2, errors.New("head window failed")), now)
	}
	if w.chainPaused("polygon", now) {
		t.Fatal("failures outside the forward stages must never pause a chain")
	}
	// a pass that fails late but already moved the cursor is progress, not a stall
	w.noteCursor("polygon", 100)
	w.noteCursor("polygon", 150)
	for i := 0; i < 6; i++ {
		w.noteChainPass("polygon", stageErr("polygon", "deposit", 151, 200, errors.New("timed out after progress")), now)
	}
	if w.chainPaused("polygon", now) {
		t.Fatal("a pass that advanced the cursor must not be treated as a stall")
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

// End to end through the real pass loop: timer-driven passes of a chain that
// cannot move its cursor back off, manual scans never do, recovery resets it.
func TestTimerPassesBackOffAStalledChainButManualScansAlwaysRun(t *testing.T) {
	var failCursor atomic.Bool
	failCursor.Store(true)
	pg := &scannerMockPG{beforeWork: func(_ context.Context, step string) error {
		if step == "load cursor" && failCursor.Load() {
			return errors.New("injected cursor failure")
		}
		return nil
	}}
	w, _ := scannerLockFixture(t, pg)
	timer := context.WithValue(t.Context(), periodicPassKey{}, true)

	for i := 0; i < 2; i++ {
		if err := w.RunOnce(timer); err == nil {
			t.Fatal("the injected failure must surface")
		}
	}
	if got := countSteps(pg, "load cursor"); got != 2 {
		t.Fatalf("both early passes must try the chain, cursor loads=%d", got)
	}
	if !w.chainPaused("bsc", time.Now()) {
		t.Fatal("two stalled passes must pause the chain")
	}

	// A timer pass now skips the chain entirely: no cursor read, no RPC.
	if err := w.RunOnce(timer); err != nil {
		t.Fatalf("a pass that skips the paused chain has nothing to fail: %v", err)
	}
	if got := countSteps(pg, "load cursor"); got != 2 {
		t.Fatalf("a paused chain must not be scanned by timer passes, cursor loads=%d", got)
	}

	// A manual scan is an operator asking to try now.
	if err := w.RunOnce(t.Context()); err == nil {
		t.Fatal("the manual scan runs and hits the injected failure")
	}
	if got := countSteps(pg, "load cursor"); got != 3 {
		t.Fatalf("manual scans must bypass the pause, cursor loads=%d", got)
	}

	// Once the pause lapses and the chain is healthy again, one pass clears everything.
	failCursor.Store(false)
	w.replayMu.Lock()
	w.chainPause["bsc"].next = time.Now().Add(-time.Second)
	w.replayMu.Unlock()
	if err := w.RunOnce(timer); err != nil {
		t.Fatalf("healthy pass: %v", err)
	}
	if w.chainPaused("bsc", time.Now()) {
		t.Fatal("a successful pass must clear the pause")
	}
	w.replayMu.Lock()
	_, stillTracked := w.chainPause["bsc"]
	w.replayMu.Unlock()
	if stillTracked {
		t.Fatal("a successful pass must drop the chain's failure record")
	}
}
