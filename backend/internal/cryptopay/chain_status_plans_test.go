package cryptopay

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/stretchr/testify/require"
)

func TestChainPlansPreferPoolsDedupeAndKeepConfiguredButDisabledChains(t *testing.T) {
	cfg := DefaultCryptoConfig()
	cfg.Chains = []string{ChainPolygon, ChainBSC, ChainPolygon, ChainSolana, ChainEthereum, ChainBase}
	cfg.RPC = map[string]string{
		ChainPolygon: "https://config.example/poly", // the live pool wins over the stored string
		ChainBSC:     " https://a.example/x , ,https://a.example/x,https://b.example/y ",
		ChainBase:    "https://base.example/z",
		ChainXLayer:  "https://xlayer.example/k", // configured, not in chains: shown as disabled
		ChainPlasma:  "  ",                       // blank is not a node
	}
	delete(cfg.Confirmations, ChainPolygon) // missing key: the default applies
	cfg.Confirmations[ChainBSC] = 0         // an explicit zero is honoured
	cfg.ScanRange[ChainBase] = 123
	pools := map[string]*rpc.Pool{
		ChainPolygon:  poolOf("https://pool.example/p1", "https://pool.example/p1", " https://pool.example/p2 "),
		ChainEthereum: poolOf(), // empty pool and empty config: enabled with no endpoints
	}
	s := &Service{config: cfg, pools: pools}

	want := []chainPlan{
		{ChainPolygon, true, 5, 2000, []string{"https://pool.example/p1", "https://pool.example/p2"}},
		{ChainBSC, true, 0, 2000, []string{"https://a.example/x", "https://b.example/y"}},
		{ChainEthereum, true, 64, 2000, []string{}},
		{ChainBase, true, 5, 123, []string{"https://base.example/z"}},
		{ChainXLayer, false, 5, 100, []string{"https://xlayer.example/k"}},
	}
	require.Equal(t, want, s.chainPlans(),
		"enabled chains first in configured order (once each, EVM only), then the other configured chains")
}

func TestChainEndpointsFallsBackToTheStoredSetting(t *testing.T) {
	cfg := map[string]string{ChainBSC: "https://one.example/a,https://two.example/b"}
	require.Equal(t, []string{"https://one.example/a", "https://two.example/b"}, chainEndpoints(nil, cfg, ChainBSC))
	require.Equal(t, []string{"https://one.example/a", "https://two.example/b"},
		chainEndpoints(map[string]*rpc.Pool{ChainBSC: poolOf()}, cfg, ChainBSC), "an empty pool does not hide the stored nodes")
	require.Empty(t, chainEndpoints(nil, cfg, ChainEthereum))
}

func TestChainStatusOnAZeroValueServiceIsAnEmptyReport(t *testing.T) {
	s := &Service{}
	tick := &fakeTicker{}
	s.chainMon.newTicker = tick.New
	t.Cleanup(s.chainMon.stop)

	rep, err := s.ChainStatus(context.Background())
	require.NoError(t, err)
	require.NotNil(t, rep.Chains)
	require.Empty(t, rep.Chains)
	require.Equal(t, ChainStatusSummary{}, rep.Summary)
	_, parseErr := time.Parse(time.RFC3339, rep.GeneratedAt)
	require.NoError(t, parseErr)
	raw, err := json.Marshal(rep)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"chains":[]`, "an empty list, never null")
	require.Equal(t, 1, tick.Created(), "a poll starts the sampling loop")
}
