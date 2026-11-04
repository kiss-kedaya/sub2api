package cryptopay

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func fp(v float64) *float64 { return &v }

func TestChainBacklogThresholdScalesWithBlockRate(t *testing.T) {
	for name, tc := range map[string]struct {
		speed *float64
		want  uint64
	}{
		"unknown rate uses the conservative default": {nil, chainBacklogDefaultBlocks},
		"zero":     {fp(0), chainBacklogDefaultBlocks},
		"negative": {fp(-3), chainBacklogDefaultBlocks},
		"NaN":      {fp(math.NaN()), chainBacklogDefaultBlocks},
		"Inf":      {fp(math.Inf(1)), chainBacklogDefaultBlocks},
		"80 blocks/min is 150s of chain time = 200 blocks": {fp(80), 200},
		"slow chain rounds up":                             {fp(5), 13},
		"very slow chain is floored":                       {fp(1), chainBacklogMinBlocks},
		"absurd rate is capped":                            {fp(1e9), chainBacklogMaxBlocks},
	} {
		t.Run(name, func(t *testing.T) { require.Equal(t, tc.want, chainBacklogThreshold(tc.speed)) })
	}
}

func TestConfirmedHeadAndUnscannedBoundaries(t *testing.T) {
	require.Equal(t, uint64(97), confirmedHeadOf(100, 3))
	require.Equal(t, uint64(0), confirmedHeadOf(3, 3), "head == confirmations: the scanner does nothing yet")
	require.Equal(t, uint64(0), confirmedHeadOf(2, 3))
	require.Equal(t, uint64(100), confirmedHeadOf(100, 0))
	require.Equal(t, uint64(10), unscannedOf(100, 90))
	require.Equal(t, uint64(0), unscannedOf(100, 100))
	require.Equal(t, uint64(0), unscannedOf(100, 150), "a cursor ahead of the confirmed head is never negative")
}

func sampleAt(base time.Time, sec int, cursor, head uint64) chainSample {
	return chainSample{At: base.Add(time.Duration(sec) * time.Second), Cursor: cursor, Head: head}
}

func TestWindowSpeedsNeedASufficientBaseline(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	for name, samples := range map[string][]chainSample{
		"no samples":            nil,
		"one sample":            {sampleAt(base, 0, 1, 1)},
		"two samples 89s apart": {sampleAt(base, 0, 1, 1), sampleAt(base, 89, 500, 500)},
	} {
		t.Run(name, func(t *testing.T) {
			scan, head := windowSpeeds(samples)
			require.Nil(t, scan)
			require.Nil(t, head)
		})
	}
	scan, head := windowSpeeds([]chainSample{sampleAt(base, 0, 0, 0), sampleAt(base, 90, 150, 270)})
	require.NotNil(t, scan, "exactly 90s is enough")
	require.InDelta(t, 100, *scan, 1e-9) // 150 blocks in 1.5 minutes
	require.InDelta(t, 180, *head, 1e-9)
}

func TestWindowSpeedsUseTheWholeWindowNotTheLastTwoPoints(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	// The cursor moves one batch at a time: it jumped at t=120 and then sat still.
	// The last two points alone would read as "0 blocks/min".
	scan, head := windowSpeeds([]chainSample{
		sampleAt(base, 0, 1000, 5000), sampleAt(base, 60, 1000, 5080),
		sampleAt(base, 120, 5000, 5160), sampleAt(base, 180, 5000, 5240),
	})
	require.InDelta(t, 4000.0/3, *scan, 1e-9)
	require.InDelta(t, 80, *head, 1e-9)
}

func TestWindowSpeedsNeverGoNegative(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	scan, head := windowSpeeds([]chainSample{sampleAt(base, 0, 900, 900), sampleAt(base, 120, 100, 800)})
	require.Equal(t, 0.0, *scan)
	require.Equal(t, 0.0, *head)
}

func TestProgressPct(t *testing.T) {
	for _, tc := range []struct {
		name      string
		lag       uint64
		window    []uint64
		threshold uint64
		want      float64
	}{
		{name: "inside the synced threshold is complete", lag: 200, window: []uint64{5000}, threshold: 200, want: 100},
		{name: "zero lag", lag: 0, threshold: 200, want: 100},
		{name: "no history: the current lag is its own peak", lag: 201, threshold: 200, want: 0},
		{name: "half of the peak", lag: 500, window: []uint64{1000, 800}, threshold: 200, want: 50},
		{name: "still at the peak", lag: 1000, window: []uint64{1000, 500}, threshold: 200, want: 0},
		{name: "worse than anything in the window", lag: 1500, window: []uint64{1000}, threshold: 200, want: 0},
		{name: "a quarter of the peak left", lag: 250, window: []uint64{1000}, threshold: 200, want: 75},
		{name: "rounds to one decimal", lag: 333, window: []uint64{1000}, threshold: 200, want: 66.7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.InDelta(t, tc.want, progressPct(tc.lag, tc.window, tc.threshold), 1e-9)
		})
	}
}

func TestETASeconds(t *testing.T) {
	require.Equal(t, int64(0), *etaSeconds(ChainStateSynced, 0, nil, nil), "synced has nothing left to catch up")
	for _, state := range []string{ChainStateStalled, ChainStateError, ChainStateDisabled, ChainStateNoRPC, ""} {
		require.Nil(t, etaSeconds(state, 1000, fp(300), fp(100)), state)
	}
	require.Nil(t, etaSeconds(ChainStateCatchingUp, 1000, nil, fp(100)), "scan speed unknown")
	require.Nil(t, etaSeconds(ChainStateCatchingUp, 1000, fp(300), nil), "head speed unknown")
	require.Equal(t, int64(300), *etaSeconds(ChainStateCatchingUp, 1000, fp(300), fp(100))) // 1000 / 200 per min
	require.Equal(t, int64(301), *etaSeconds(ChainStateCatchingUp, 1001, fp(300), fp(100)), "rounds up")
	require.Nil(t, etaSeconds(ChainStateCatchingUp, 1000, fp(100), fp(100)), "scan only keeps pace with the chain")
	require.Nil(t, etaSeconds(ChainStateCatchingUp, 1000, fp(50), fp(100)), "falling further behind")
	require.Nil(t, etaSeconds(ChainStateCatchingUp, 1000, fp(100.005), fp(100)), "net rate below the epsilon")
	require.Equal(t, int64(604800), *etaSeconds(ChainStateCatchingUp, 10080, fp(101), fp(100)), "exactly 7 days is reported")
	require.Nil(t, etaSeconds(ChainStateCatchingUp, 10081, fp(101), fp(100)), "beyond 7 days is not a useful number")
}

func TestSecondsSinceIsNeverNegative(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	require.Equal(t, int64(90), secondsSince(now.Add(-90900*time.Millisecond), now))
	require.Equal(t, int64(0), secondsSince(now.Add(time.Hour), now))
}
