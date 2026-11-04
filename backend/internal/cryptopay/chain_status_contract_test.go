package cryptopay

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The JSON field names are the contract with the frontend (and with the backfill
// job's progress report). Unknown values are null, the node list is never null.
func TestChainStatusJSONContract(t *testing.T) {
	in := baseAssemble()
	in.Plan.Endpoints, in.Probes, in.Cursor, in.PendingKnown = nil, nil, nil, false
	raw, err := json.Marshal(ChainStatusReport{GeneratedAt: "t", Chains: []ChainStatus{assembleChainStatus(in)}})
	require.NoError(t, err)
	var report map[string]any
	require.NoError(t, json.Unmarshal(raw, &report))
	require.Equal(t, []string{"chains", "generated_at", "summary"}, keysOf(report))
	require.Equal(t, []string{"enabled", "error", "lagging", "stalled"}, keysOf(report["summary"].(map[string]any)))

	chain := report["chains"].([]any)[0].(map[string]any)
	require.Equal(t, []string{
		"chain", "confirmations", "confirmed_head", "cursor", "cursor_updated_at", "enabled", "eta_seconds",
		"head", "head_speed_bpm", "last_error", "nodes", "pending_deposits", "progress_pct", "scan_range",
		"scan_speed_bpm", "stalled_seconds", "state", "unscanned",
	}, keysOf(chain))
	for _, unknown := range []string{"head", "confirmed_head", "cursor", "unscanned", "progress_pct", "scan_speed_bpm",
		"head_speed_bpm", "eta_seconds", "cursor_updated_at", "stalled_seconds", "pending_deposits", "last_error"} {
		require.Nil(t, chain[unknown], unknown+" is null when unknown")
	}
	require.Equal(t, []any{}, chain["nodes"], "an empty list, never null")

	full := assembleChainStatus(baseAssemble())
	raw, err = json.Marshal(full)
	require.NoError(t, err)
	var row map[string]any
	require.NoError(t, json.Unmarshal(raw, &row))
	node := row["nodes"].([]any)[0].(map[string]any)
	require.Equal(t, []string{"behind", "error_kind", "head", "host", "label", "latency_ms", "status"}, keysOf(node))
	require.Nil(t, node["error_kind"])

	raw, err = json.Marshal(ChainProgress{Chain: ChainBSC})
	require.NoError(t, err)
	var progress map[string]any
	require.NoError(t, json.Unmarshal(raw, &progress))
	require.Equal(t, []string{"chain", "confirmed_head", "cursor", "cursor_known", "head", "unscanned"}, keysOf(progress))
}

func TestSummarizeCountsOnlyEnabledChains(t *testing.T) {
	chains := []ChainStatus{
		{Enabled: true, State: ChainStateSynced}, {Enabled: true, State: ChainStateCatchingUp},
		{Enabled: true, State: ChainStateCatchingUp}, {Enabled: true, State: ChainStateStalled},
		{Enabled: true, State: ChainStateError}, {Enabled: true, State: ChainStateNoRPC},
		{Enabled: false, State: ChainStateDisabled}, {Enabled: false, State: ChainStateStalled},
	}
	require.Equal(t, ChainStatusSummary{Enabled: 6, Lagging: 2, Stalled: 1, Error: 2}, summarizeChains(chains))
	require.Equal(t, ChainStatusSummary{}, summarizeChains(nil))
}

func TestSortKeepsEnabledFirstAndOrderWithinEachGroup(t *testing.T) {
	chains := []ChainStatus{
		{Chain: "d1"}, {Chain: "e1", Enabled: true}, {Chain: "d2"}, {Chain: "e2", Enabled: true}, {Chain: "e3", Enabled: true},
	}
	sortChainStatuses(chains)
	names := make([]string, 0, len(chains))
	for _, c := range chains {
		names = append(names, c.Chain)
	}
	require.Equal(t, []string{"e1", "e2", "e3", "d1", "d2"}, names)
}
