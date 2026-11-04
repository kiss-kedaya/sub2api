package cryptopay

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/stretchr/testify/require"
)

// The BSC incident: RPC nodes configured, the chain missing from
// crypto.config.chains, the cursor frozen for hours, 17 deposits pending and not
// one line in the logs. The page has to show it instead of hiding it.
func TestChainStatusShowsAChainThatIsConfiguredButNotEnabled(t *testing.T) {
	ethA := "https://rpc.ankr.com/eth/SECRETKEY"
	polyA, polyB := "https://polygon.drpc.org/SECRETKEY", "https://polygon-bor-rpc.publicnode.com"
	bscA, bscB := "https://bsc-dataseed.binance.org", "https://bsc.drpc.org/SECRETKEY"
	cfg := DefaultCryptoConfig()
	cfg.Chains = []string{ChainEthereum, ChainPolygon, ChainArbitrum}
	cfg.RPC = map[string]string{ChainEthereum: ethA, ChainPolygon: polyA + "," + polyB, ChainBSC: bscA + "," + bscB}
	pools := map[string]*rpc.Pool{ChainEthereum: poolOf(ethA), ChainPolygon: poolOf(polyA, polyB), ChainBSC: poolOf(bscA, bscB)}
	fake := func(chainID, head uint64) *fakeNode {
		return newFakeNode(t, fakeNodeOpts{chainID: chainID, head: head})
	}
	h := newStatusHarness(t, cfg, pools, map[string]*fakeNode{
		ethA: fake(1, 20_000_000), polyA: fake(137, 60_000_000), polyB: fake(137, 59_999_960),
		bscA: fake(56, 40_000_000), bscB: fake(56, 40_000_000),
	})
	now := h.clock.Now()
	h.expectCursors(
		cursorRow{ChainEthereum, 19_999_929, now.Add(-20 * time.Second)},
		cursorRow{ChainPolygon, 59_990_000, now.Add(-10 * time.Minute)},
		cursorRow{ChainBSC, 39_000_000, now.Add(-14 * time.Hour)},
	)
	h.expectPending(map[string]int64{ChainBSC: 17, ChainEthereum: 2})

	rep, err := h.svc.ChainStatus(context.Background())
	require.NoError(t, err)
	require.NoError(t, h.mock.ExpectationsWereMet())
	require.Equal(t, []string{ChainEthereum, ChainPolygon, ChainArbitrum, ChainBSC}, chainNames(rep),
		"enabled chains in configured order, then the disabled one")
	require.Equal(t, now.UTC().Format(time.RFC3339), rep.GeneratedAt)
	require.Equal(t, ChainStatusSummary{Enabled: 3, Lagging: 0, Stalled: 1, Error: 1}, rep.Summary,
		"polygon is stalled, arbitrum has no RPC; the disabled chain is not in the enabled count")

	got := byChain(rep)
	eth := got[ChainEthereum]
	require.True(t, eth.Enabled)
	require.Equal(t, ChainStateSynced, eth.State)
	require.Equal(t, 64, eth.Confirmations)
	require.Equal(t, 2000, eth.ScanRange)
	require.Equal(t, uint64(20_000_000), *eth.Head)
	require.Equal(t, uint64(19_999_936), *eth.ConfirmedHead)
	require.Equal(t, uint64(19_999_929), *eth.Cursor)
	require.Equal(t, uint64(7), *eth.Unscanned)
	require.Equal(t, 100.0, *eth.ProgressPct)
	require.Equal(t, int64(0), *eth.ETASeconds)
	require.Equal(t, int64(20), *eth.StalledSeconds)
	require.Equal(t, int64(2), *eth.PendingDeposits)
	require.Nil(t, eth.ScanSpeedBPM, "one sample is not a speed")
	require.Nil(t, eth.LastError)
	require.Equal(t, "ankr#0", eth.Nodes[0].Label)
	require.Equal(t, NodeStatusOK, eth.Nodes[0].Status)

	poly := got[ChainPolygon]
	require.Equal(t, ChainStateStalled, poly.State)
	require.Equal(t, uint64(59_999_995), *poly.ConfirmedHead)
	require.Equal(t, uint64(9995), *poly.Unscanned)
	require.Equal(t, int64(600), *poly.StalledSeconds)
	require.Equal(t, 0.0, *poly.ProgressPct)
	require.Nil(t, poly.ETASeconds)
	require.Equal(t, int64(0), *poly.PendingDeposits)
	require.Equal(t, NodeStatusOK, poly.Nodes[0].Status)
	require.Equal(t, NodeStatusBehind, poly.Nodes[1].Status)
	require.Equal(t, uint64(40), *poly.Nodes[1].Behind)

	arb := got[ChainArbitrum]
	require.True(t, arb.Enabled)
	require.Equal(t, ChainStateNoRPC, arb.State)
	require.NotNil(t, arb.Nodes)
	require.Empty(t, arb.Nodes)
	require.Nil(t, arb.Head)
	require.Nil(t, arb.Cursor)
	require.Nil(t, arb.ProgressPct)
	require.Nil(t, arb.LastError)

	bsc := got[ChainBSC]
	require.False(t, bsc.Enabled)
	require.Equal(t, ChainStateDisabled, bsc.State)
	require.Equal(t, 3, bsc.Confirmations)
	require.Equal(t, uint64(39_999_997), *bsc.ConfirmedHead)
	require.Equal(t, uint64(999_997), *bsc.Unscanned, "how far behind it fell is still shown")
	require.Equal(t, int64(14*3600), *bsc.StalledSeconds)
	require.Equal(t, int64(17), *bsc.PendingDeposits)
	require.Nil(t, bsc.ETASeconds)
	require.Equal(t, []string{"binance#0", "drpc#1"}, []string{bsc.Nodes[0].Label, bsc.Nodes[1].Label})
}
