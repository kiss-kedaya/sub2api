package handler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A manual sweep used to be tied to the HTTP request context. Sixty addresses
// take minutes; the browser gives up long before that, the request context is
// cancelled, and every job in the queue failed with context.Canceled without a
// single transaction being sent. The run must survive the client leaving.
func TestManualSweepContextSurvivesRequestCancellation(t *testing.T) {
	requestCtx, cancelRequest := context.WithCancel(context.Background())

	// Mirrors what AdminRunSweep builds.
	sweepCtx, cancelSweep := context.WithTimeout(context.WithoutCancel(requestCtx), manualSweepDeadline)
	defer cancelSweep()

	// The operator closes the tab.
	cancelRequest()

	select {
	case <-sweepCtx.Done():
		t.Fatalf("sweep context died with the request: %v", sweepCtx.Err())
	default:
	}

	// It still carries the deadline, so a hung run is not unbounded.
	deadline, ok := sweepCtx.Deadline()
	require.True(t, ok, "sweep context must keep a deadline")
	require.WithinDuration(t, time.Now().Add(manualSweepDeadline), deadline, time.Minute)
}

// Values still flow through, so a caller-supplied trace or logger is not lost.
func TestManualSweepContextKeepsValues(t *testing.T) {
	type key struct{}
	parent := context.WithValue(context.Background(), key{}, "trace-1")
	sweepCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), time.Minute)
	defer cancel()
	require.Equal(t, "trace-1", sweepCtx.Value(key{}))
}
