package scanner

import "testing"

// The idle-chain guard must not share the deposit scan's lock: a guard that keeps
// failing on a dead chain held the shared lock back to back (96% of the time in
// production) and starved the deposit scan of every chain.
func TestGuardScanUsesItsOwnAdvisoryLock(t *testing.T) {
	if GuardScanLockKey == scanLockKey {
		t.Fatal("the guard scan and the deposit scan must not contend for one lock")
	}
	if GuardScanLockKey != 0x43525950544f5347 { // "CRYPTOSG"; the sweeper's is "CRYPTOSW"
		t.Fatalf("unexpected guard lock key %#x", GuardScanLockKey)
	}
	w := &Worker{}
	if got := w.advisoryLockKey(); got != scanLockKey {
		t.Fatalf("a worker with no explicit key takes the shared deposit-scan lock, got %#x", got)
	}
	w.SetScanLockKey(GuardScanLockKey)
	if got := w.advisoryLockKey(); got != GuardScanLockKey {
		t.Fatalf("SetScanLockKey was ignored: %#x", got)
	}
}

// The configured key is the one that reaches PostgreSQL, for both the acquire and
// the release.
func TestWorkerAcquiresAndReleasesTheConfiguredAdvisoryLock(t *testing.T) {
	pg := &scannerMockPG{key: GuardScanLockKey}
	w, _ := scannerLockFixture(t, pg)
	w.SetScanLockKey(GuardScanLockKey)
	if err := w.RunOnce(t.Context()); err != nil {
		t.Fatalf("the mock rejects any key but the guard's: %v", err)
	}
	scannerAssertSteps(t, pg, "acquire", "load cursor", "save cursor", "pending", "recheck", "release")
	scannerAssertIdle(t, w, pg, false)

	// and a default worker against a mock that wants the guard key is rejected,
	// which is what proves the mock really checks the key
	other := &scannerMockPG{key: GuardScanLockKey}
	w2, _ := scannerLockFixture(t, other)
	if err := w2.RunOnce(t.Context()); err == nil {
		t.Fatal("the shared key must not be accepted where the guard key is expected")
	}
}
