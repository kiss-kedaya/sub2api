package cryptopay

import (
	"math"
	"time"
)

// chainSample is one observation of a chain: when it was taken, where the
// durable scan cursor was and the best head any node reported.
type chainSample struct {
	At     time.Time
	Cursor uint64
	Head   uint64
}

func chainPtr[T any](v T) *T { return &v }

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// confirmedHeadOf mirrors the scanner: it scans up to head-confirmations and does
// nothing while head <= confirmations.
func confirmedHeadOf(head, confirmations uint64) uint64 {
	if head <= confirmations {
		return 0
	}
	return head - confirmations
}

// unscannedOf is how many confirmed blocks the cursor still has to cover.
func unscannedOf(confirmedHead, cursor uint64) uint64 {
	if cursor >= confirmedHead {
		return 0
	}
	return confirmedHead - cursor
}

// chainBacklogThreshold turns the chain's observed block rate into the number of
// unscanned blocks that still counts as synced (chainBacklogWindow of chain
// time). Without a usable rate it falls back to a conservative fixed count.
func chainBacklogThreshold(headSpeedBPM *float64) uint64 {
	if headSpeedBPM == nil {
		return chainBacklogDefaultBlocks
	}
	v := *headSpeedBPM
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return chainBacklogDefaultBlocks
	}
	blocks := math.Ceil(v * chainBacklogWindow.Minutes())
	switch {
	case blocks < chainBacklogMinBlocks:
		blocks = chainBacklogMinBlocks
	case blocks > chainBacklogMaxBlocks:
		blocks = chainBacklogMaxBlocks
	}
	return uint64(blocks)
}

// windowSpeeds returns the cursor speed and the chain growth speed in blocks per
// minute, measured from the first to the last sample of the window. The cursor
// moves one batch at a time, so only the whole window gives a stable rate; a
// window shorter than chainSpeedMinSpan or with fewer than chainSpeedMinSamples
// samples returns nil rather than a guess. samples must be chronological.
// A backwards step (a rewound cursor, a node switch) counts as zero, never negative.
func windowSpeeds(samples []chainSample) (scan, head *float64) {
	if len(samples) < chainSpeedMinSamples {
		return nil, nil
	}
	first, last := samples[0], samples[len(samples)-1]
	span := last.At.Sub(first.At)
	if span < chainSpeedMinSpan {
		return nil, nil
	}
	minutes := span.Minutes()
	var scanDelta, headDelta float64
	if last.Cursor > first.Cursor {
		scanDelta = float64(last.Cursor - first.Cursor)
	}
	if last.Head > first.Head {
		headDelta = float64(last.Head - first.Head)
	}
	s, h := scanDelta/minutes, headDelta/minutes
	return &s, &h
}

// progressPct is the downloader-style percentage. The largest backlog seen in the
// window (the current one included) is 100%: progress = 1 - lag/peak, clamped to
// 0..100. Without history the current backlog is its own peak, so the result is 0
// (the caller shows "calculating" because the ETA is nil too). A backlog inside
// the synced threshold is complete by definition.
func progressPct(lag uint64, windowLags []uint64, threshold uint64) float64 {
	if lag <= threshold {
		return 100
	}
	peak := lag
	for _, l := range windowLags {
		if l > peak {
			peak = l
		}
	}
	pct := (1 - float64(lag)/float64(peak)) * 100
	switch {
	case pct < 0:
		pct = 0
	case pct > 100:
		pct = 100
	}
	return round1(pct)
}

// etaSeconds estimates the time to catch up. It needs both speeds and a cursor
// that outruns the chain; otherwise it is unknown (nil), never a made-up number.
// A synced chain has nothing left to catch up, which is a known 0.
func etaSeconds(state string, lag uint64, scan, head *float64) *int64 {
	switch state {
	case ChainStateSynced:
		return chainPtr(int64(0))
	case ChainStateCatchingUp:
	default:
		return nil
	}
	if scan == nil || head == nil {
		return nil
	}
	net := *scan - *head
	if math.IsNaN(net) || net <= chainNetSpeedEpsilon {
		return nil
	}
	seconds := math.Ceil(float64(lag) / net * 60)
	if seconds > chainETAMaxSeconds {
		return nil
	}
	return chainPtr(int64(seconds))
}

// secondsSince is whole seconds from then to now, never negative (clock skew).
func secondsSince(then, now time.Time) int64 {
	d := now.Sub(then)
	if d < 0 {
		return 0
	}
	return int64(d / time.Second)
}

// chainStateInput carries everything the state decision depends on.
type chainStateInput struct {
	Enabled      bool
	HasEndpoints bool
	UsableNodes  int
	// CursorFailed: the cursor table could not be read at all.
	CursorFailed bool
	// CursorMissing: the table was read but this chain has no row (never scanned).
	CursorMissing bool
	Unscanned     uint64
	Threshold     uint64
	// StalledSeconds: seconds since the cursor last moved, or since its row was
	// first seen missing.
	StalledSeconds int64
}

// classifyChainState decides the row state. Precedence, first match wins:
// disabled, no_rpc, error, then the backlog test (synced / stalled / catching_up).
// Both thresholds are strict: a backlog equal to the threshold is still synced,
// and a cursor idle for exactly chainStalledAfter is still catching up.
func classifyChainState(in chainStateInput) string {
	switch {
	case !in.Enabled:
		return ChainStateDisabled
	case !in.HasEndpoints:
		return ChainStateNoRPC
	case in.UsableNodes == 0 || in.CursorFailed:
		return ChainStateError
	}
	if !in.CursorMissing && in.Unscanned <= in.Threshold {
		return ChainStateSynced
	}
	if in.StalledSeconds > int64(chainStalledAfter/time.Second) {
		return ChainStateStalled
	}
	return ChainStateCatchingUp
}
