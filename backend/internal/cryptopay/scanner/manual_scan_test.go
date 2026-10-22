package scanner

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
)

func TestManualScanRunsFullPassUnderSharedLock(t *testing.T) {
	pg := &scannerMockPG{}
	w, _ := scannerLockFixture(t, pg)
	if err := w.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	scannerAssertSteps(t, pg, "acquire", "pending", "recheck", "load cursor", "save cursor", "release")
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
	scannerAssertSteps(t, pg, "acquire", "pending", "recheck", "load cursor", "save cursor", "release")
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
