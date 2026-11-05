package cryptopay

import (
	"errors"
	"testing"
	"time"
)

// A sweep run holds sweepMu for its whole length. A reconcile asked for meanwhile
// has to answer at once; waiting left the admin's browser hanging until it timed out.
func TestReconcileSweepAnswersAtOnceWhileASweepRuns(t *testing.T) {
	s := &Service{}
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := s.ReconcileSweep(t.Context(), 38)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrScanBusy) {
			t.Fatalf("want ErrScanBusy, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reconcile waited for the running sweep instead of answering")
	}
}

func TestReconcileSweepReleasesTheLockWhenThereIsNothingToDo(t *testing.T) {
	s := &Service{}
	if _, err := s.ReconcileSweep(t.Context(), 38); err == nil || errors.Is(err, ErrScanBusy) {
		t.Fatalf("a disabled service should say so, got %v", err)
	}
	if !s.sweepMu.TryLock() {
		t.Fatal("the sweep lock was left held after the call")
	}
	s.sweepMu.Unlock()
	if _, err := s.ReconcileSweep(t.Context(), 0); err == nil {
		t.Fatal("an invalid id must be refused")
	}
	if !s.sweepMu.TryLock() {
		t.Fatal("the sweep lock was taken for an invalid id")
	}
	s.sweepMu.Unlock()
}
