package cryptopay

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/explorer"
)

// A provider failure is reported at most once a minute per chain, as before; a
// missing explorer key (which only an operator can fix) at most once an hour, but
// still repeated: without it the native outflow monitor is not running.
func TestNativeFailureReporterThrottlesAMissingKeyHarderThanAnOutage(t *testing.T) {
	logs := captureLogs(t)
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	report := newNativeFailureReporter(func() time.Time { return now })
	count := func() int { return strings.Count(logs.String(), "native txlist scan incomplete") }

	outage := fmt.Errorf("native txlist scan bsc: %w", errors.New("boom"))
	for i := 0; i < 10; i++ { // ten polls over 50 seconds: one line
		report("bsc", outage)
		now = now.Add(5 * time.Second)
	}
	if count() != 1 {
		t.Fatalf("outage lines in 50s = %d, want 1", count())
	}
	now = now.Add(time.Minute)
	report("bsc", outage)
	if count() != 2 {
		t.Fatalf("outage lines after a minute = %d, want 2", count())
	}

	base := count()
	missing := fmt.Errorf("native txlist scan eth: %w", explorer.ErrNoAPIKey)
	report("eth", missing)
	for i := 0; i < 200; i++ { // polls every 10s for 33 minutes
		now = now.Add(10 * time.Second)
		report("eth", missing)
	}
	if got := count() - base; got != 1 {
		t.Fatalf("missing-key lines in 33 minutes = %d, want 1", got)
	}
	if !strings.Contains(logs.String(), "no explorer API key configured") {
		t.Fatalf("the line must still say why:\n%s", logs.String())
	}
	now = now.Add(time.Hour)
	report("eth", missing)
	if got := count() - base; got != 2 {
		t.Fatalf("missing-key lines after an hour = %d, want 2: the gap must stay visible", got)
	}
	// The same applies to a chain with no explorer endpoint at all.
	report("polygon", fmt.Errorf("native txlist scan polygon: %w", explorer.ErrNoEndpoint))
	now = now.Add(10 * time.Minute)
	report("polygon", fmt.Errorf("native txlist scan polygon: %w", explorer.ErrNoEndpoint))
	if got := count() - base; got != 3 {
		t.Fatalf("lines = %d, want 3", got)
	}
	// Chains are throttled independently.
	report("bsc", outage)
	if got := count() - base; got != 4 {
		t.Fatalf("lines = %d, want 4: another chain's failure must not be swallowed", got)
	}
}
