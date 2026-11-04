package cryptopay

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func okProbe(head uint64, latency time.Duration) nodeProbe {
	return nodeProbe{Answered: true, HasHead: true, Head: head, HasLatency: true, Latency: latency}
}

func TestBuildNodeStatusesGradesEachNodeAgainstTheBestHead(t *testing.T) {
	endpoints := []string{
		"https://rpc.ankr.com/bsc/SECRETKEY",
		"https://bsc.drpc.org",
		"https://bsc.blockpi.network/v1/rpc/SECRETKEY",
		"https://eth.llamarpc.com",
		"https://1rpc.io/bnb",
		"https://bsc-dataseed.binance.org",
	}
	probes := []nodeProbe{
		okProbe(1000, 100*time.Millisecond),
		okProbe(985, 50*time.Millisecond),
		okProbe(970, 60*time.Millisecond),
		// A wrong-chain node reporting a huge head must not become the yardstick.
		{Answered: true, WrongChain: true, HasHead: true, Head: 5000, HasLatency: true, Latency: 40 * time.Millisecond, ErrorKind: NodeErrWrongChain},
		{ErrorKind: NodeErrTimeout},
		okProbe(1000, 2500*time.Millisecond),
	}
	nodes, usable := buildNodeStatuses(endpoints, probes, 2000*time.Millisecond)
	require.Equal(t, 4, usable, "ok, ok, behind and slow nodes can still scan")
	require.Len(t, nodes, len(endpoints))

	type want struct {
		label, status string
		behind        *uint64
		kind          string
	}
	u := func(v uint64) *uint64 { return &v }
	for i, w := range []want{
		{"ankr#0", NodeStatusOK, u(0), ""},
		{"drpc#1", NodeStatusOK, u(15), ""},
		{"blockpi#2", NodeStatusBehind, u(30), ""},
		{"llamarpc#3", NodeStatusWrongChain, nil, NodeErrWrongChain},
		{"1rpc#4", NodeStatusError, nil, NodeErrTimeout},
		{"binance#5", NodeStatusSlow, u(0), ""},
	} {
		n := nodes[i]
		require.Equal(t, w.label, n.Label)
		require.Equal(t, w.status, n.Status, n.Label)
		require.Equal(t, w.behind, n.Behind, n.Label)
		if w.kind == "" {
			require.Nil(t, n.ErrorKind, n.Label)
		} else {
			require.Equal(t, w.kind, *n.ErrorKind, n.Label)
			require.Nil(t, n.Head, "a node without a trustworthy head reports none")
		}
		require.NotContains(t, n.Label+n.Host, "SECRETKEY")
	}
	require.Equal(t, int64(100), *nodes[0].LatencyMS)
	require.Equal(t, int64(40), *nodes[3].LatencyMS, "a wrong-chain node still answered")
	require.Nil(t, nodes[4].LatencyMS, "a timeout has no round trip to report")
}

func TestBuildNodeStatusesWithoutAnyUsableNode(t *testing.T) {
	endpoints := []string{"https://bsc.drpc.org", "https://1rpc.io/bnb", "https://eth.llamarpc.com"}
	// The third probe is missing entirely and the first has no recorded kind.
	nodes, usable := buildNodeStatuses(endpoints, []nodeProbe{{}, {ErrorKind: NodeErrHTTP429}}, nodeSlowAfter)
	require.Zero(t, usable)
	require.Len(t, nodes, 3)
	for _, n := range nodes {
		require.Equal(t, NodeStatusError, n.Status)
		require.NotNil(t, n.ErrorKind)
	}
	require.Equal(t, NodeErrConnection, *nodes[0].ErrorKind, "never an empty error kind")
	require.Equal(t, NodeErrHTTP429, *nodes[1].ErrorKind)
	require.Equal(t, NodeErrConnection, *nodes[2].ErrorKind)

	empty, none := buildNodeStatuses(nil, nil, nodeSlowAfter)
	require.NotNil(t, empty)
	require.Empty(t, empty)
	require.Zero(t, none)
}

func TestBestProbeHead(t *testing.T) {
	_, ok := bestProbeHead(nil)
	require.False(t, ok)
	_, ok = bestProbeHead([]nodeProbe{{ErrorKind: NodeErrTimeout}, {WrongChain: true, HasHead: true, Head: 9}})
	require.False(t, ok, "failed and wrong-chain nodes give no head")
	best, ok := bestProbeHead([]nodeProbe{okProbe(7, 0), {WrongChain: true, HasHead: true, Head: 99}, okProbe(12, 0), okProbe(10, 0)})
	require.True(t, ok)
	require.Equal(t, uint64(12), best)
}

func TestDominantNodeError(t *testing.T) {
	bad := func(kind string) nodeProbe { return nodeProbe{ErrorKind: kind} }
	require.Equal(t, NodeErrHTTP5xx, dominantNodeError([]nodeProbe{bad(NodeErrTimeout), bad(NodeErrHTTP5xx), bad(NodeErrHTTP5xx)}))
	require.Equal(t, NodeErrTimeout, dominantNodeError([]nodeProbe{bad(NodeErrTimeout), bad(NodeErrHTTP5xx)}), "a tie goes to the first seen")
	require.Equal(t, NodeErrWrongChain, dominantNodeError([]nodeProbe{{WrongChain: true, HasHead: true, Head: 1}, bad(NodeErrTimeout), {WrongChain: true}}))
	require.Equal(t, NodeErrTimeout, dominantNodeError([]nodeProbe{okProbe(1, 0), bad(NodeErrTimeout)}), "healthy nodes do not count")
	require.Equal(t, NodeErrConnection, dominantNodeError([]nodeProbe{{}}), "an unrecorded kind reads as connection")
	require.Empty(t, dominantNodeError([]nodeProbe{okProbe(1, 0)}))
	require.Empty(t, dominantNodeError(nil))
}
