package cryptopay

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
)

func within(got, want time.Duration) bool { return got >= want-want/5 && got <= want+want/5 }

func TestIdleGuardPacerBacksOffExponentiallyAndCaps(t *testing.T) {
	var p idleGuardPacer
	now := time.Date(2026, 11, 24, 4, 0, 0, 0, time.UTC)
	if !p.due(now) {
		t.Fatal("a fresh pacer runs at once")
	}
	failure := errors.New("every node refused us")
	for i, want := range []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 160 * time.Second, 5 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
		p.note(failure, now)
		if got := p.next.Sub(now); !within(got, want) {
			t.Fatalf("failure %d: next attempt in %v, want about %v", i+1, got, want)
		}
		if p.due(now) {
			t.Fatalf("failure %d: must not be due immediately", i+1)
		}
		if !p.due(p.next) {
			t.Fatalf("failure %d: must be due once the delay elapsed", i+1)
		}
	}
}

func TestIdleGuardPacerFirstSuccessResumesFullCadence(t *testing.T) {
	var p idleGuardPacer
	now := time.Now()
	for i := 0; i < 8; i++ {
		p.note(errors.New("down"), now)
	}
	if p.due(now) {
		t.Fatal("setup: backed off")
	}
	p.note(nil, now.Add(time.Hour))
	if !p.due(now) || p.failures != 0 {
		t.Fatalf("one success must clear the backoff, got failures=%d next=%v", p.failures, p.next)
	}
	// and the next failure starts from the short delay again
	p.note(errors.New("down again"), now)
	if got := p.next.Sub(now); !within(got, 5*time.Second) {
		t.Fatalf("restarted at %v, want about 5s", got)
	}
}

// Another instance holding the guard lock says nothing about the chains.
func TestIdleGuardPacerIgnoresAnotherInstanceHoldingTheLock(t *testing.T) {
	var p idleGuardPacer
	now := time.Now()
	p.note(scanner.ErrScanBusy, now)
	p.note(fmt.Errorf("wrapped: %w", scanner.ErrScanBusy), now)
	p.note(errors.Join(scanner.ErrScanBusy, scanner.ErrScanBusy), now)
	if p.failures != 0 || !p.due(now) {
		t.Fatalf("a busy lock must not count as a failure: %+v", p)
	}
	// ...and must not erase a backoff that is already running
	p.note(errors.New("down"), now)
	before := p.next
	p.note(scanner.ErrScanBusy, now.Add(time.Second))
	if p.failures != 1 || !p.next.Equal(before) {
		t.Fatalf("a busy lock must leave a running backoff alone: %+v", p)
	}
}

// The wiring the incident was about: the idle guard's worker takes the guard's own
// lock, not the deposit scan's. Without this the guard (failing on a dead chain
// every tick) held the shared lock 96% of the time and starved every deposit scan.
func TestIdleGuardWorkerTakesItsOwnAdvisoryLock(t *testing.T) {
	worker := newIdleGuardWorker(nil, nil, nil)
	if got := worker.LockKey(); got != scanner.GuardScanLockKey {
		t.Fatalf("the idle guard must use scanner.GuardScanLockKey, got %#x", got)
	}
	deposit := scanner.NewWorker(nil, nil, nil, nil, nil, nil, 5*time.Second, 2000)
	if deposit.LockKey() == worker.LockKey() {
		t.Fatal("the deposit scan and the idle guard must hold different locks")
	}
}
