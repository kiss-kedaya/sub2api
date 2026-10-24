package scanner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
)

func TestManualBackfillRejectsUnconfirmedAndUnboundedRanges(t *testing.T) {
	for _, from := range []uint64{106, 111, ^uint64(0)} {
		pg := &scannerMockPG{}
		w, _ := scannerLockFixture(t, pg)
		if err := w.RunOnceFrom(t.Context(), from); err == nil {
			t.Fatalf("accepted unconfirmed block %d", from)
		}
		scannerAssertSteps(t, pg, "acquire", "release")
		scannerAssertIdle(t, w, pg, false)
	}
	pg := &scannerMockPG{}
	w, fixture := scannerLockFixture(t, pg)
	fixture.head = "0x30d40" // 200000
	if err := w.RunOnceFrom(t.Context(), 1); err == nil {
		t.Fatal("unbounded historical range accepted")
	}
	scannerAssertSteps(t, pg, "acquire", "release")
}

func TestManualBackfillRewindsAndPreservesEarlierUnfinishedCursor(t *testing.T) {
	for _, from := range []uint64{90, 103} {
		pg := &scannerMockPG{}
		w, _ := scannerLockFixture(t, pg)
		if err := w.RunOnceFrom(t.Context(), from); err != nil {
			t.Fatal(err)
		}
		_, _, events, _ := pg.snapshot()
		saves := 0
		for _, event := range events {
			if strings.Contains(event.query, "INSERT INTO crypto_scan_cursors") {
				saves++
			}
		}
		want := 1
		if from == 90 {
			want = 3 // rewind, explicit start cursor and ordinary progress
		}
		if saves != want {
			t.Fatalf("from %d saved %d cursors, want %d", from, saves, want)
		}
		scannerAssertIdle(t, w, pg, false)
	}
}

func TestManualScanRunsFullPassUnderSharedLock(t *testing.T) {
	pg := &scannerMockPG{}
	w, _ := scannerLockFixture(t, pg)
	if err := w.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	scannerAssertSteps(t, pg, "acquire", "load cursor", "save cursor", "pending", "recheck", "release")
	scannerAssertIdle(t, w, pg, false)
}

func TestManualScanReturnsContentionWithoutChainWork(t *testing.T) {
	pg := &scannerMockPG{holder: -1, depth: 1}
	w, fixture := scannerLockFixture(t, pg)
	if err := w.RunOnce(t.Context()); !errors.Is(err, ErrScanBusy) {
		t.Fatalf("want busy: %v", err)
	}
	scannerAssertSteps(t, pg, "acquire")
	if fixture.callCount("eth_chainId") != 0 {
		t.Fatal("contender reached RPC")
	}
}

func TestManualScanReturnsFailuresAndAlwaysUnlocks(t *testing.T) {
	problem := errors.New("injected pending failure")
	pg := &scannerMockPG{beforeWork: func(_ context.Context, step string) error {
		if step == "pending" {
			return problem
		}
		return nil
	}}
	w, _ := scannerLockFixture(t, pg)
	if err := w.RunOnce(t.Context()); !errors.Is(err, problem) {
		t.Fatalf("pending error hidden: %v", err)
	}
	scannerAssertSteps(t, pg, "acquire", "load cursor", "save cursor", "pending", "recheck", "release")
	scannerAssertIdle(t, w, pg, false)
}

func TestManualScanRejectsReadOnlyBeforeAcquiringLock(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "false")
	var w *Worker
	if err := w.RunOnce(t.Context()); !errors.Is(err, mode.ErrReadOnly) {
		t.Fatalf("want read-only: %v", err)
	}
}
