package rpc

import (
	"strings"
	"testing"
)

// A record that says WHERE a job stopped ("balance_read: RPC connection
// failed") must survive the read-side sanitiser, and the prefix must never make
// an unsafe remainder safe.
func TestSafeRecordedErrorKeepsAStepPrefixButJudgesTheRest(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"balance_read: RPC connection failed", "balance_read: RPC connection failed"},
		{"gas_price: RPC HTTP request failed (status 503)", "gas_price: RPC HTTP request failed (status 503)"},
		{"sign: wallet signer is locked; a recovery shard is required", "sign: wallet signer is locked; a recovery shard is required"},
		// The remainder is judged on its own, exactly like an unprefixed record.
		{"receipt: https://node.example/fixture-secret-path?key=fixture-secret-key", "receipt: Payment operation failed; reconcile transaction status before retrying"},
		{"persist: fixture-driver-message password=fixture-secret", "persist: Payment operation failed; reconcile transaction status before retrying"},
		// An unknown stage is not a stage: the whole message is unclassified.
		{"fixture_step: RPC connection failed", "Payment operation failed; reconcile transaction status before retrying"},
		// A bare prefix has no reason to keep.
		{"validate: ", "Payment operation failed; reconcile transaction status before retrying"},
		// The attempt suffix the sweeper appends still works behind a prefix.
		{"balance_read: live balance 9 is below planned amount 10 (gave up after 25 attempts)", "balance_read: live balance 9 is below planned amount 10 (gave up after 25 attempts)"},
	} {
		if got := SafeRecordedError(tc.in); got != tc.want {
			t.Errorf("SafeRecordedError(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSafeRecordedErrorNeverLeaksThroughNestedPrefixes(t *testing.T) {
	got := SafeRecordedError("sign: broadcast: nonce: https://node.example/fixture-secret-path?key=fixture-secret-key")
	if strings.Contains(got, "fixture-") || strings.Contains(got, "https://") {
		t.Fatalf("nested step prefixes leaked node text: %q", got)
	}
}

// The drain's own message is written by this repository from fixed words, so a
// run status keeps it; the same prefix with a URL, a hex value or an oversized
// body does not.
func TestSweepDrainMessageIsKeptOnlyWhenBounded(t *testing.T) {
	ok := "sweep drain stopped: no progress in 3 rounds; failing: polygon#1338 balance_read/transport"
	if got := SafeRecordedError(ok); got != ok {
		t.Fatalf("drain message was not kept: %q", got)
	}
	for _, bad := range []string{
		"sweep drain stopped: see https://node.example/fixture-secret-path",
		"sweep drain stopped: 0x" + strings.Repeat("a", 40),
		"sweep drain stopped: " + strings.Repeat("x", 200),
	} {
		if got := SafeRecordedError(bad); got != "Payment operation failed; reconcile transaction status before retrying" {
			t.Fatalf("unbounded drain message survived: %q", got)
		}
	}
}
