package scanner

import (
	"sync/atomic"
	"testing"
	"time"
)

// The chain proof cache is process-wide and keyed by endpoint address, which is
// right in production (an address serves one network) and wrong in a test binary:
// every test starts servers on throwaway addresses, and the next test can be handed
// the address of one that was just closed. A proof earned there then vouched for
// the new server, so a "wrong chain" fixture on a reused address was accepted and
// TestPendingRejectsChangedOrNoncanonicalTransfer/wrong_chain failed now and then
// (err=<nil> wantErr=true).
//
// In tests the cache is therefore inert unless a test opts in by installing its own
// clock: every reading of this one is further from the previous reading than
// chainProofTTL, so nothing stored is ever still valid when it is looked up.
func init() {
	var ticks atomic.Int64
	proofClock = func() time.Time {
		return time.Unix(0, 0).Add(time.Duration(ticks.Add(1)) * 2 * chainProofTTL)
	}
}

// withProofClock installs clock for one test and puts the previous one back.
func withProofClock(t *testing.T, clock func() time.Time) {
	t.Helper()
	previous := proofClock
	proofClock = clock
	t.Cleanup(func() { proofClock = previous })
}

// What an earlier test that was handed this very address would have left behind
// must not vouch for a server that serves another chain.
func TestStaleChainProofDoesNotVouchForALaterServerOnTheSameAddress(t *testing.T) {
	fixture := newRPCFixture(testTransfer())
	fixture.chainID = "0x1" // an Ethereum node answering for BSC
	pool := fixture.pool(t)
	storeChainProof("bsc", pool.Endpoints()[0], nil)
	if _, err := verifiedChainPool(t.Context(), pool, "bsc"); err == nil {
		t.Fatal("a proof left behind by an earlier test vouched for a server on another chain")
	}
	if fixture.callCount("eth_chainId") == 0 {
		t.Fatal("the endpoint was not probed: a cached proof was trusted")
	}
}

// verifiedChainPool runs on every scan pass. An endpoint that already proved it
// serves the chain is not asked again for a few minutes: the answer never
// changes, and each probe counts against the provider's daily allowance.
func TestChainProofIsRememberedAndExpires(t *testing.T) {
	now := time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)
	withProofClock(t, func() time.Time { return now })

	fixture := newRPCFixture(testTransfer())
	fixture.chainID = "0x38" // BSC
	pool := fixture.pool(t)

	if _, err := verifiedChainPool(t.Context(), pool, "bsc"); err != nil {
		t.Fatal(err)
	}
	first := fixture.callCount("eth_chainId")
	if first == 0 {
		t.Fatal("the first pass must probe the endpoint")
	}
	for i := 0; i < 5; i++ {
		now = now.Add(5 * time.Second) // five more passes
		if _, err := verifiedChainPool(t.Context(), pool, "bsc"); err != nil {
			t.Fatal(err)
		}
	}
	if got := fixture.callCount("eth_chainId"); got != first {
		t.Fatalf("a proven endpoint must not be probed again within %v: %d -> %d requests", chainProofTTL, first, got)
	}
	now = now.Add(chainProofTTL)
	if _, err := verifiedChainPool(t.Context(), pool, "bsc"); err != nil {
		t.Fatal(err)
	}
	if got := fixture.callCount("eth_chainId"); got <= first {
		t.Fatal("the proof must expire and be renewed")
	}
}

// A wrong-chain endpoint is rejected every time, and is not hammered meanwhile.
func TestWrongChainEndpointIsRejectedAndNotReprobedEveryPass(t *testing.T) {
	now := time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)
	withProofClock(t, func() time.Time { return now })

	fixture := newRPCFixture(testTransfer())
	fixture.chainID = "0x1" // Ethereum answering for BSC
	pool := fixture.pool(t)

	if _, err := verifiedChainPool(t.Context(), pool, "bsc"); err == nil {
		t.Fatal("an endpoint serving another chain must never be accepted")
	}
	first := fixture.callCount("eth_chainId")
	now = now.Add(2 * time.Second)
	if _, err := verifiedChainPool(t.Context(), pool, "bsc"); err == nil {
		t.Fatal("still rejected from the remembered failure")
	}
	if got := fixture.callCount("eth_chainId"); got != first {
		t.Fatalf("a freshly failed endpoint must not be probed on every pass: %d -> %d", first, got)
	}
	now = now.Add(chainProofFailTTL)
	if _, err := verifiedChainPool(t.Context(), pool, "bsc"); err == nil {
		t.Fatal("still the wrong chain after the failure memory lapsed")
	}
	if got := fixture.callCount("eth_chainId"); got <= first {
		t.Fatal("after the short failure memory lapses the endpoint is probed again")
	}
}
