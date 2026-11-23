package scanner

import (
	"testing"
	"time"
)

// verifiedChainPool runs on every scan pass. An endpoint that already proved it
// serves the chain is not asked again for a few minutes: the answer never
// changes, and each probe counts against the provider's daily allowance.
func TestChainProofIsRememberedAndExpires(t *testing.T) {
	now := time.Date(2026, 11, 24, 3, 0, 0, 0, time.UTC)
	proofClock = func() time.Time { return now }
	t.Cleanup(func() { proofClock = time.Now })

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
	proofClock = func() time.Time { return now }
	t.Cleanup(func() { proofClock = time.Now })

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
