package cryptopay

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Three polls a minute apart while the scanner catches up with a chain that keeps
// growing. Numbers: confirmations 3, head grows 80 blocks/min, cursor 4000/min.
func TestChainStatusProgressSpeedAndETAGrowWithTheSampleWindow(t *testing.T) {
	a := "https://rpc.ankr.com/bsc/SECRETKEY"
	cfg, pools := bscOnly(a)
	node := newFakeNode(t, fakeNodeOpts{chainID: 56})
	h := newStatusHarness(t, cfg, pools, map[string]*fakeNode{a: node})

	poll := func(head uint64, cursor int64) ChainStatus {
		node.head.Store(head)
		h.expectCursors(cursorRow{ChainBSC, cursor, h.clock.Now().Add(-5 * time.Second)})
		h.expectPending(nil)
		rep, err := h.svc.ChainStatus(context.Background())
		require.NoError(t, err)
		return byChain(rep)[ChainBSC]
	}

	first := poll(10_003, 3_000) // confirmed 10000, lag 7000
	require.Equal(t, ChainStateCatchingUp, first.State)
	require.Equal(t, uint64(7000), *first.Unscanned)
	require.Equal(t, 0.0, *first.ProgressPct, "no history: the current backlog is its own 100%")
	require.Nil(t, first.ETASeconds, "calculating, not a guess")
	require.Nil(t, first.ScanSpeedBPM)

	h.clock.Advance(time.Minute)
	second := poll(10_083, 7_000) // confirmed 10080, lag 3080
	require.Equal(t, uint64(3080), *second.Unscanned)
	require.Equal(t, 56.0, *second.ProgressPct) // 1 - 3080/7000
	require.Nil(t, second.ScanSpeedBPM, "60s is shorter than the 90s baseline")
	require.Nil(t, second.ETASeconds)

	require.Equal(t, 1, h.tick.Created(), "one sampling loop for the whole session")
	require.NoError(t, h.mock.ExpectationsWereMet())
}

func TestChainStatusTurnsSyncedOnceInsideTheBacklogWindow(t *testing.T) {
	a := "https://rpc.ankr.com/bsc/SECRETKEY"
	cfg, pools := bscOnly(a)
	node := newFakeNode(t, fakeNodeOpts{chainID: 56})
	h := newStatusHarness(t, cfg, pools, map[string]*fakeNode{a: node})
	poll := func(head uint64, cursor int64) ChainStatus {
		node.head.Store(head)
		h.expectCursors(cursorRow{ChainBSC, cursor, h.clock.Now().Add(-5 * time.Second)})
		h.expectPending(nil)
		rep, err := h.svc.ChainStatus(context.Background())
		require.NoError(t, err)
		return byChain(rep)[ChainBSC]
	}
	poll(10_003, 3_000)
	h.clock.Advance(time.Minute)
	poll(10_083, 7_000)
	h.clock.Advance(time.Minute)
	third := poll(10_163, 10_000) // confirmed 10160, lag 160

	require.Equal(t, uint64(160), *third.Unscanned)
	require.Equal(t, ChainStateSynced, third.State, "160 blocks is inside the 200-block window")
	require.NotNil(t, third.ScanSpeedBPM)
	require.InDelta(t, 3500, *third.ScanSpeedBPM, 0.1) // (10000-3000)/2min
	require.InDelta(t, 80, *third.HeadSpeedBPM, 0.1)   // 160/2min
	require.Equal(t, 100.0, *third.ProgressPct)
	require.Equal(t, int64(0), *third.ETASeconds)
}

func TestChainStatusCatchingUpETA(t *testing.T) {
	a := "https://rpc.ankr.com/bsc/SECRETKEY"
	cfg, pools := bscOnly(a)
	node := newFakeNode(t, fakeNodeOpts{chainID: 56})
	h := newStatusHarness(t, cfg, pools, map[string]*fakeNode{a: node})
	poll := func(head uint64, cursor int64) ChainStatus {
		node.head.Store(head)
		h.expectCursors(cursorRow{ChainBSC, cursor, h.clock.Now().Add(-5 * time.Second)})
		h.expectPending(nil)
		rep, err := h.svc.ChainStatus(context.Background())
		require.NoError(t, err)
		return byChain(rep)[ChainBSC]
	}
	poll(10_003, 0)
	h.clock.Advance(time.Minute)
	poll(10_083, 4_000)
	h.clock.Advance(time.Minute)
	third := poll(10_163, 8_000) // confirmed 10160, lag 2160

	require.Equal(t, ChainStateCatchingUp, third.State)
	require.Equal(t, uint64(2160), *third.Unscanned)
	require.InDelta(t, 4000, *third.ScanSpeedBPM, 0.1)
	require.InDelta(t, 80, *third.HeadSpeedBPM, 0.1)
	require.InDelta(t, 78.4, *third.ProgressPct, 0.05) // peak lag 10000
	require.Equal(t, int64(34), *third.ETASeconds)     // 2160 / (4000-80) per minute
}

func TestChainProgressIsTheReusableScanPosition(t *testing.T) {
	a := "https://rpc.ankr.com/bsc/SECRETKEY"
	cfg, pools := bscOnly(a)
	node := newFakeNode(t, fakeNodeOpts{chainID: 56, head: 10_003})
	h := newStatusHarness(t, cfg, pools, map[string]*fakeNode{a: node})
	ctx := context.Background()

	t.Run("an unknown or unconfigured chain", func(t *testing.T) {
		for _, chain := range []string{"solana", "nope", ChainEthereum} {
			_, err := h.svc.ChainProgress(ctx, chain)
			require.ErrorIs(t, err, ErrChainNotConfigured, chain)
		}
	})
	t.Run("cursor behind the confirmed head", func(t *testing.T) {
		h.expectCursors(cursorRow{ChainBSC, 9_000, h.clock.Now()})
		p, err := h.svc.ChainProgress(ctx, ChainBSC)
		require.NoError(t, err)
		require.Equal(t, ChainProgress{Chain: ChainBSC, Head: 10_003, ConfirmedHead: 10_000, Cursor: 9_000, CursorKnown: true, Unscanned: 1_000}, *p)
	})
	t.Run("no cursor row yet reports the whole confirmed height", func(t *testing.T) {
		h.expectCursors()
		p, err := h.svc.ChainProgress(ctx, ChainBSC)
		require.NoError(t, err)
		require.False(t, p.CursorKnown)
		require.Equal(t, uint64(10_000), p.Unscanned)
	})
	t.Run("cursor ahead of the confirmed head", func(t *testing.T) {
		h.expectCursors(cursorRow{ChainBSC, 10_050, h.clock.Now()})
		p, err := h.svc.ChainProgress(ctx, ChainBSC)
		require.NoError(t, err)
		require.Equal(t, uint64(0), p.Unscanned)
	})
	t.Run("cursor read failure is a fixed sentinel", func(t *testing.T) {
		h.expectCursorsError(errors.New("password=DBPASSWORD"))
		_, err := h.svc.ChainProgress(ctx, ChainBSC)
		require.ErrorIs(t, err, ErrChainCursorUnavailable)
		require.NotContains(t, err.Error(), "DBPASSWORD")
	})
	require.NoError(t, h.mock.ExpectationsWereMet())
	require.Zero(t, h.tick.Created(), "asking for progress must not start the page's sampling loop")
}

func TestChainProgressWithoutAUsableHeadDoesNotReadTheDatabase(t *testing.T) {
	a := "https://rpc.ankr.com/bsc/SECRETKEY"
	cfg, pools := bscOnly(a)
	h := newStatusHarness(t, cfg, pools, map[string]*fakeNode{a: newFakeNode(t, fakeNodeOpts{status: 500})})
	_, err := h.svc.ChainProgress(context.Background(), ChainBSC)
	require.ErrorIs(t, err, ErrChainHeadUnavailable)
	require.NoError(t, h.mock.ExpectationsWereMet(), "no query was issued")

	var nilSvc *Service
	_, err = nilSvc.ChainProgress(context.Background(), ChainBSC)
	require.ErrorIs(t, err, ErrChainNotConfigured)
}
