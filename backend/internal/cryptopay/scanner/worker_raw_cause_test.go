package scanner

import (
	"errors"
	"strings"
	"testing"
)

// The raw cause is what finally makes a stuck chain diagnosable, but an RPC key
// sits inside the endpoint URL, so it must never reach the log verbatim.
func TestRawScanCauseRedactsEndpointKeys(t *testing.T) {
	err := errors.New(`Post "https://bsc.blockpi.network/v1/rpc/9d5a92d447e4a11a67b6ffe43e17f7fb4626b4ef": dial tcp: i/o timeout`)

	got := rawScanCause(err)

	if strings.Contains(got, "9d5a92d447e4a11a67b6ffe43e17f7fb4626b4ef") {
		t.Fatalf("RPC key leaked into the log: %s", got)
	}
	if !strings.Contains(got, "/rpc/<redacted>") {
		t.Fatalf("key was not replaced, got %s", got)
	}
	// The part that actually explains the failure must survive.
	if !strings.Contains(got, "i/o timeout") {
		t.Fatalf("cause was stripped, got %s", got)
	}
}

func TestRawScanCauseRedactsQueryKeysAndKeepsMessages(t *testing.T) {
	err := errors.New("explorer call failed: https://api.bscscan.com/v2/api?module=account&apikey=ABCDEF123456&action=txlist: status 403")

	got := rawScanCause(err)

	if strings.Contains(got, "ABCDEF123456") {
		t.Fatalf("query api key leaked: %s", got)
	}
	if !strings.Contains(got, "apikey=<redacted>") || !strings.Contains(got, "status 403") {
		t.Fatalf("unexpected raw cause: %s", got)
	}
}

func TestRawScanCauseCapsLength(t *testing.T) {
	got := rawScanCause(errors.New(strings.Repeat("x", 4000)))
	if len(got) > 700 {
		t.Fatalf("raw cause should be bounded, got %d bytes", len(got))
	}
}

func TestRawScanCauseNilIsEmpty(t *testing.T) {
	if got := rawScanCause(nil); got != "" {
		t.Fatalf("nil error should produce empty cause, got %q", got)
	}
}
