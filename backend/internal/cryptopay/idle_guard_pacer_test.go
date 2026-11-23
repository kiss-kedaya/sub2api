package cryptopay

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
)

func within(got, want time.Duration) bool { return got >= want-want/5 && got <= want+want/5 }

func stateOf(p *idleGuardPacer, chain string) idleGuardState {
	if st := p.chains[chain]; st != nil {
		return *st
	}
	return idleGuardState{}
}

func TestIdleGuardPacerBacksOffExponentiallyAndCaps(t *testing.T) {
	var p idleGuardPacer
	now := time.Date(2026, 11, 24, 4, 0, 0, 0, time.UTC)
	if !p.due("plasma", now) {
		t.Fatal("a fresh pacer runs at once")
	}
	failure := errors.New("every node refused us")
	for i, want := range []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 160 * time.Second, 5 * time.Minute, 5 * time.Minute, 5 * time.Minute} {
		p.note("plasma", failure, now)
		st := stateOf(&p, "plasma")
		if got := st.next.Sub(now); !within(got, want) {
			t.Fatalf("failure %d: next attempt in %v, want about %v", i+1, got, want)
		}
		if p.due("plasma", now) {
			t.Fatalf("failure %d: must not be due immediately", i+1)
		}
		if !p.due("plasma", st.next) {
			t.Fatalf("failure %d: must be due once the delay elapsed", i+1)
		}
	}
}

// The jitter is random, so a cap that is off by a doubling (320s instead of 300s) only
// shows on some draws. Draw many times, including absurd failure counts: the delay
// must stay inside five minutes +-20% and must never overflow into a negative or
// zero wait (which would make the guard hammer a dead provider again).
func TestIdleGuardPacerDelayNeverExceedsTheCap(t *testing.T) {
	now := time.Date(2026, 11, 24, 4, 0, 0, 0, time.UTC)
	for _, failures := range []int{7, 8, 30, 100000} {
		for draw := 0; draw < 300; draw++ {
			var p idleGuardPacer
			for i := 1; i < failures && i < 12; i++ { // walk up to the cap cheaply
				p.note("plasma", errors.New("down"), now)
			}
			if failures > 12 {
				p.chains["plasma"].failures = failures - 1 // as if it had failed that many times
			}
			p.note("plasma", errors.New("down"), now)
			got := p.chains["plasma"].next.Sub(now)
			if got < idleGuardMaxDelay-idleGuardMaxDelay/5 || got > idleGuardMaxDelay+idleGuardMaxDelay/5 {
				t.Fatalf("failures=%d draw=%d: delay %v is outside 5min +-20%%", failures, draw, got)
			}
		}
	}
}

func TestIdleGuardPacerFirstSuccessResumesFullCadence(t *testing.T) {
	var p idleGuardPacer
	now := time.Now()
	for i := 0; i < 8; i++ {
		p.note("plasma", errors.New("down"), now)
	}
	if p.due("plasma", now) {
		t.Fatal("setup: backed off")
	}
	p.note("plasma", nil, now.Add(time.Hour))
	if !p.due("plasma", now) || p.failuresOf("plasma") != 0 {
		t.Fatalf("one success must clear the backoff, got %+v", stateOf(&p, "plasma"))
	}
	p.note("plasma", errors.New("down again"), now)
	if got := stateOf(&p, "plasma").next.Sub(now); !within(got, 5*time.Second) {
		t.Fatalf("restarted at %v, want about 5s", got)
	}
}

// Another instance holding the guard lock says nothing about the chain.
func TestIdleGuardPacerIgnoresAnotherInstanceHoldingTheLock(t *testing.T) {
	var p idleGuardPacer
	now := time.Now()
	p.note("plasma", scanner.ErrScanBusy, now)
	p.note("plasma", fmt.Errorf("wrapped: %w", scanner.ErrScanBusy), now)
	p.note("plasma", errors.Join(scanner.ErrScanBusy, scanner.ErrScanBusy), now)
	if p.failuresOf("plasma") != 0 || !p.due("plasma", now) {
		t.Fatalf("a busy lock must not count as a failure: %+v", stateOf(&p, "plasma"))
	}
	p.note("plasma", errors.New("down"), now)
	before := stateOf(&p, "plasma")
	p.note("plasma", scanner.ErrScanBusy, now.Add(time.Second))
	if after := stateOf(&p, "plasma"); after != before {
		t.Fatalf("a busy lock must leave a running backoff alone: %+v -> %+v", before, after)
	}
}

// The point of the design: a dead chain must not delay the others. A guard exists
// to notice an unauthorised outflow from a hot wallet; one chain's outage cannot
// push another chain's detection out to five minutes.
func TestIdleGuardPacerIsPerChain(t *testing.T) {
	var p idleGuardPacer
	now := time.Date(2026, 11, 24, 4, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		p.note("plasma", errors.New("403 from every node"), now)
	}
	if p.due("plasma", now) {
		t.Fatal("setup: plasma is backed off")
	}
	for _, healthy := range []string{"arbitrum", "base", "xlayer"} {
		if !p.due(healthy, now) {
			t.Fatalf("%s has never failed and must stay due while plasma is backed off", healthy)
		}
		p.note(healthy, nil, now)
	}
	if p.due("plasma", now) || p.failuresOf("plasma") != 10 {
		t.Fatalf("other chains' successes must not touch plasma's backoff: %+v", stateOf(&p, "plasma"))
	}
	// and a failure of one healthy chain starts ITS backoff from the short delay
	p.note("base", errors.New("blip"), now)
	if got := stateOf(&p, "base").next.Sub(now); !within(got, 5*time.Second) {
		t.Fatalf("base failed once: next in %v, want about 5s", got)
	}
	if !p.due("arbitrum", now) || !p.due("xlayer", now) {
		t.Fatal("base's failure must not back off its neighbours")
	}
}

func TestIdleGuardPacerDueChainsSkipsBackedOffAndOrdersHealthiestFirst(t *testing.T) {
	var p idleGuardPacer
	now := time.Date(2026, 11, 24, 4, 0, 0, 0, time.UTC)
	chains := []scanner.ChainConfig{{Chain: "plasma"}, {Chain: "base"}, {Chain: "xlayer"}, {Chain: "arbitrum"}}
	// plasma: one failure, backed off until later; base: failed twice but its delay has passed;
	// xlayer, arbitrum: never failed.
	p.note("plasma", errors.New("down"), now)
	p.note("base", errors.New("down"), now.Add(-time.Hour))
	p.note("base", errors.New("down"), now.Add(-time.Hour))
	got := p.dueChains(chains, now)
	var names []string
	for _, c := range got {
		names = append(names, c.Chain)
	}
	want := []string{"xlayer", "arbitrum", "base"} // plasma skipped; the chain with failures runs last; ties keep input order
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("dueChains = %v, want %v", names, want)
	}
	if len(p.dueChains(nil, now)) != 0 {
		t.Fatal("no chains, nothing due")
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
