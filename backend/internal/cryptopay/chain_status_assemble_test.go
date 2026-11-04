package cryptopay

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var assembleNow = time.Unix(1_700_000_000, 0)

func baseAssemble() chainAssembleInput {
	return chainAssembleInput{
		Plan: chainPlan{Chain: ChainBSC, Enabled: true, Confirmations: 3, ScanRange: 2000,
			Endpoints: []string{"https://rpc.ankr.com/x", "https://bsc.drpc.org/y"}},
		Probes:    []nodeProbe{okProbe(1000, 50*time.Millisecond), okProbe(998, 60*time.Millisecond)},
		SlowAfter: nodeSlowAfter,
		Cursor:    &chainCursor{Block: 990, UpdatedAt: assembleNow.Add(-20 * time.Second)},
		Pending:   4, PendingKnown: true, Now: assembleNow,
	}
}

func TestAssembleSynced(t *testing.T) {
	st := assembleChainStatus(baseAssemble())
	require.Equal(t, ChainStateSynced, st.State)
	require.Equal(t, uint64(1000), *st.Head)
	require.Equal(t, uint64(997), *st.ConfirmedHead)
	require.Equal(t, uint64(990), *st.Cursor)
	require.Equal(t, uint64(7), *st.Unscanned)
	require.Equal(t, 100.0, *st.ProgressPct)
	require.Equal(t, int64(0), *st.ETASeconds)
	require.Equal(t, int64(20), *st.StalledSeconds)
	require.Equal(t, int64(4), *st.PendingDeposits)
	require.Equal(t, assembleNow.Add(-20*time.Second).UTC().Format(time.RFC3339), *st.CursorUpdatedAt)
	require.Len(t, st.Nodes, 2)
	require.Nil(t, st.LastError)
}

func TestAssembleDisabledChainKeepsItsFrozenCursorVisible(t *testing.T) {
	in := baseAssemble()
	in.Plan.Enabled = false
	in.Cursor = &chainCursor{Block: 100, UpdatedAt: assembleNow.Add(-14 * time.Hour)}
	st := assembleChainStatus(in)
	require.Equal(t, ChainStateDisabled, st.State)
	require.False(t, st.Enabled)
	require.Equal(t, uint64(897), *st.Unscanned)
	require.Equal(t, int64(14*3600), *st.StalledSeconds)
	require.Equal(t, int64(4), *st.PendingDeposits)
	require.Nil(t, st.ETASeconds, "a disabled chain is not catching up")
}

func TestAssembleNoRPCAndAllNodesFailing(t *testing.T) {
	in := baseAssemble()
	in.Plan.Endpoints, in.Probes = nil, nil
	st := assembleChainStatus(in)
	require.Equal(t, ChainStateNoRPC, st.State)
	require.NotNil(t, st.Nodes)
	require.Empty(t, st.Nodes)
	require.Nil(t, st.Head)
	require.Nil(t, st.LastError, "there is no node to blame")
	require.Equal(t, uint64(990), *st.Cursor, "the cursor is independent of the nodes")

	in = baseAssemble()
	in.Probes = []nodeProbe{{ErrorKind: NodeErrHTTP5xx}, {ErrorKind: NodeErrHTTP5xx}}
	st = assembleChainStatus(in)
	require.Equal(t, ChainStateError, st.State)
	require.Equal(t, &ChainLastError{Kind: NodeErrHTTP5xx, Stage: ErrStageHead}, st.LastError)
	require.Nil(t, st.Head)
	require.Nil(t, st.Unscanned)
	require.Nil(t, st.ProgressPct)
	require.Nil(t, st.ETASeconds)

	in.Probes = []nodeProbe{{WrongChain: true, HasHead: true, Head: 5}, {WrongChain: true, HasHead: true, Head: 6}}
	st = assembleChainStatus(in)
	require.Equal(t, ChainStateError, st.State, "nodes on the wrong chain are not usable")
	require.Equal(t, &ChainLastError{Kind: NodeErrWrongChain, Stage: ErrStageHead}, st.LastError)
}

func TestAssembleCursorFailureAndErrorPrecedence(t *testing.T) {
	in := baseAssemble()
	in.Cursor, in.CursorFailed = nil, true
	st := assembleChainStatus(in)
	require.Equal(t, ChainStateError, st.State)
	require.Equal(t, &ChainLastError{Kind: ErrKindDB, Stage: ErrStageCursor}, st.LastError)
	require.Nil(t, st.Cursor)
	require.Nil(t, st.StalledSeconds)
	require.NotNil(t, st.Head)

	in.Probes = []nodeProbe{{ErrorKind: NodeErrTimeout}, {ErrorKind: NodeErrTimeout}}
	st = assembleChainStatus(in)
	require.Equal(t, &ChainLastError{Kind: NodeErrTimeout, Stage: ErrStageHead}, st.LastError, "the node failure is reported first")
}

func TestAssembleMissingCursorRowTurnsStalledAfter180Seconds(t *testing.T) {
	for name, tc := range map[string]struct {
		missing time.Duration
		want    string
	}{
		"just seen":        {0, ChainStateCatchingUp},
		"180s":             {180 * time.Second, ChainStateCatchingUp},
		"180.9s truncates": {180900 * time.Millisecond, ChainStateCatchingUp},
		"181s":             {181 * time.Second, ChainStateStalled},
	} {
		t.Run(name, func(t *testing.T) {
			in := baseAssemble()
			in.Cursor, in.MissingFor = nil, tc.missing
			st := assembleChainStatus(in)
			require.Equal(t, tc.want, st.State)
			require.Nil(t, st.Cursor)
			require.Nil(t, st.Unscanned, "no cursor, no backlog figure")
			require.Nil(t, st.ProgressPct)
			require.Nil(t, st.StalledSeconds)
		})
	}
}

func TestAssembleStalledBoundaryWithACursor(t *testing.T) {
	for age, want := range map[time.Duration]string{
		180 * time.Second: ChainStateCatchingUp, 181 * time.Second: ChainStateStalled,
	} {
		in := baseAssemble()
		in.Cursor = &chainCursor{Block: 100, UpdatedAt: assembleNow.Add(-age)} // 897 blocks behind
		require.Equal(t, want, assembleChainStatus(in).State, age.String())
	}
}

func TestAssembleCatchingUpUsesTheWholeWindow(t *testing.T) {
	in := baseAssemble()
	in.Probes = []nodeProbe{okProbe(10_163, 40*time.Millisecond)}
	in.Plan.Endpoints = in.Plan.Endpoints[:1]
	in.Cursor = &chainCursor{Block: 8000, UpdatedAt: assembleNow.Add(-5 * time.Second)}
	base := assembleNow.Add(-120 * time.Second)
	in.Window = []chainSample{sampleAt(base, 0, 0, 10_003), sampleAt(base, 60, 4000, 10_083), sampleAt(base, 120, 8000, 10_163)}
	st := assembleChainStatus(in)
	require.Equal(t, ChainStateCatchingUp, st.State)
	require.Equal(t, uint64(2160), *st.Unscanned)
	require.Equal(t, 4000.0, *st.ScanSpeedBPM)
	require.Equal(t, 80.0, *st.HeadSpeedBPM)
	require.InDelta(t, 78.4, *st.ProgressPct, 1e-9) // peak backlog 10000
	require.Equal(t, int64(34), *st.ETASeconds)     // 2160 / (4000-80) per minute

	in.Window = in.Window[:2] // only 60s of history
	in.Cursor = &chainCursor{Block: 4000, UpdatedAt: assembleNow.Add(-5 * time.Second)}
	in.Probes = []nodeProbe{okProbe(10_083, 40*time.Millisecond)}
	st = assembleChainStatus(in)
	require.Equal(t, uint64(6080), *st.Unscanned)
	require.InDelta(t, 39.2, *st.ProgressPct, 1e-9)
	require.Nil(t, st.ScanSpeedBPM, "calculating")
	require.Nil(t, st.HeadSpeedBPM)
	require.Nil(t, st.ETASeconds, "calculating, not invented")
}

func TestAssembleStalledCursorReadsZeroSpeedAndNoETA(t *testing.T) {
	in := baseAssemble()
	in.Probes = []nodeProbe{okProbe(5400, 40*time.Millisecond)}
	in.Plan.Endpoints = in.Plan.Endpoints[:1]
	in.Cursor = &chainCursor{Block: 100, UpdatedAt: assembleNow.Add(-300 * time.Second)}
	base := assembleNow.Add(-300 * time.Second)
	for i := 0; i <= 5; i++ {
		in.Window = append(in.Window, sampleAt(base, i*60, 100, uint64(5000+i*80)))
	}
	st := assembleChainStatus(in)
	require.Equal(t, ChainStateStalled, st.State)
	require.Equal(t, 0.0, *st.ScanSpeedBPM)
	require.Equal(t, 80.0, *st.HeadSpeedBPM)
	require.Equal(t, 0.0, *st.ProgressPct, "the backlog only grows")
	require.Nil(t, st.ETASeconds)
}
