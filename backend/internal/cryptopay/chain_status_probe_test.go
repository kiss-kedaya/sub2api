package cryptopay

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/stretchr/testify/require"
)

func TestProbeNodeClassifiesEachFailureAndLeaksNothing(t *testing.T) {
	nodes := map[string]*fakeNode{
		"healthy": newFakeNode(t, fakeNodeOpts{chainID: 56, head: 1000}),
		"429":     newFakeNode(t, fakeNodeOpts{status: 429}),
		"500":     newFakeNode(t, fakeNodeOpts{status: 500}),
		"403":     newFakeNode(t, fakeNodeOpts{status: 403}),
		"slow":    newFakeNode(t, fakeNodeOpts{chainID: 56, head: 1, delay: 3 * time.Second}),
		"wrong":   newFakeNode(t, fakeNodeOpts{chainID: 1, head: 77}),
		"garbage": newFakeNode(t, fakeNodeOpts{body: "<html>SECRETBODY</html>"}),
		"rpcerr":  newFakeNode(t, fakeNodeOpts{rpcError: true}),
		"badhex":  newFakeNode(t, fakeNodeOpts{chainID: 56, badChain: "0xzz"}),
	}
	m, _, _ := newTestMonitor(t, nodes)
	m.probeTimeout = 300 * time.Millisecond

	for name, want := range map[string]struct {
		kind       string
		answered   bool
		wrongChain bool
		head       uint64
	}{
		"healthy":                     {"", true, false, 1000},
		"429":                         {NodeErrHTTP429, false, false, 0},
		"500":                         {NodeErrHTTP5xx, false, false, 0},
		"403":                         {NodeErrHTTP4xx, false, false, 0},
		"slow":                        {NodeErrTimeout, false, false, 0},
		"wrong":                       {NodeErrWrongChain, true, true, 0},
		"garbage":                     {NodeErrBadResp, false, false, 0},
		"rpcerr":                      {NodeErrRPC, false, false, 0},
		"badhex":                      {NodeErrBadResp, false, false, 0},
		"unmapped endpoint (no pool)": {NodeErrConnection, false, false, 0},
	} {
		t.Run(name, func(t *testing.T) {
			endpoint := name
			if name == "unmapped endpoint (no pool)" {
				endpoint = "https://nowhere.invalid/SECRETKEY"
			}
			p := m.probeNode(endpoint, 56)
			require.Equal(t, want.kind, p.ErrorKind)
			require.Equal(t, want.answered, p.Answered)
			require.Equal(t, want.wrongChain, p.WrongChain)
			require.Equal(t, want.head, p.Head)
			require.Equal(t, want.head != 0, p.HasHead)
			require.NotContains(t, fmt.Sprintf("%+v", p), "SECRET", "a probe result carries categories only")
		})
	}
}

func TestProbeNodeToAClosedServerIsAConnectionError(t *testing.T) {
	dead := newFakeNode(t, fakeNodeOpts{chainID: 56, head: 1})
	dead.srv.Close()
	// Default timeout: on Windows a refused loopback connect can take about a second.
	m, _, _ := newTestMonitor(t, map[string]*fakeNode{"dead": dead})
	p := m.probeNode("dead", 56)
	require.Equal(t, NodeErrConnection, p.ErrorKind)
	require.False(t, p.HasHead)
}

func TestProbeLatencyCoversTheRoundTrip(t *testing.T) {
	node := newFakeNode(t, fakeNodeOpts{chainID: 56, head: 5, delay: 120 * time.Millisecond})
	m, _, _ := newTestMonitor(t, map[string]*fakeNode{"n": node})
	p := m.probeNode("n", 56)
	require.True(t, p.HasHead && p.HasLatency)
	require.GreaterOrEqual(t, p.Latency, 120*time.Millisecond)
	require.Less(t, p.Latency, 3*time.Second)
}

func TestProbeSharesOneFlightAndCachesForTheTTL(t *testing.T) {
	node := newFakeNode(t, fakeNodeOpts{chainID: 56, head: 4242, delay: 300 * time.Millisecond})
	m, clock, _ := newTestMonitor(t, map[string]*fakeNode{"a": node})
	endpoints := []string{"a"}

	const callers = 25
	start := make(chan struct{})
	results := make([][]nodeProbe, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i], errs[i] = m.probe(context.Background(), "bsc", 56, endpoints)
		}()
	}
	close(start)
	wg.Wait()
	for i := range results {
		require.NoError(t, errs[i])
		require.Equal(t, uint64(4242), results[i][0].Head)
	}
	require.EqualValues(t, 1, node.chainCalls.Load(), "25 simultaneous viewers cost the node one eth_chainId")
	require.EqualValues(t, 1, node.blockCalls.Load(), "and one eth_blockNumber")

	_, err := m.probe(context.Background(), "bsc", 56, endpoints)
	require.NoError(t, err)
	clock.Advance(chainProbeCacheTTL - time.Millisecond)
	_, err = m.probe(context.Background(), "bsc", 56, endpoints)
	require.NoError(t, err)
	require.EqualValues(t, 1, node.chainCalls.Load(), "still inside the 5s cache")

	clock.Advance(time.Millisecond) // exactly the TTL: expired
	_, err = m.probe(context.Background(), "bsc", 56, endpoints)
	require.NoError(t, err)
	require.EqualValues(t, 2, node.chainCalls.Load())
	require.EqualValues(t, 2, node.blockCalls.Load())
}

func TestProbeCacheFollowsTheEndpointSet(t *testing.T) {
	a := newFakeNode(t, fakeNodeOpts{chainID: 56, head: 10})
	b := newFakeNode(t, fakeNodeOpts{chainID: 56, head: 20})
	m, _, _ := newTestMonitor(t, map[string]*fakeNode{"a": a, "b": b})
	ctx := context.Background()

	_, _ = m.probe(ctx, "bsc", 56, []string{"a"})
	_, _ = m.probe(ctx, "bsc", 56, []string{"a"})
	require.EqualValues(t, 1, a.chainCalls.Load())

	probes, err := m.probe(ctx, "bsc", 56, []string{"a", "b"})
	require.NoError(t, err)
	require.Len(t, probes, 2)
	require.Equal(t, uint64(20), probes[1].Head)
	require.EqualValues(t, 2, a.chainCalls.Load(), "an edited node list is probed straight away")
	require.EqualValues(t, 1, b.chainCalls.Load())

	_, _ = m.probe(ctx, "ethereum", 56, []string{"a"})
	require.EqualValues(t, 3, a.chainCalls.Load(), "chains never share a cached result")

	none, err := m.probe(ctx, "bsc", 56, nil)
	require.NoError(t, err)
	require.Nil(t, none)
}

func TestProbeCallerCancelLeavesTheSharedFlightAlone(t *testing.T) {
	node := newFakeNode(t, fakeNodeOpts{chainID: 56, head: 9, delay: 400 * time.Millisecond})
	m, _, _ := newTestMonitor(t, map[string]*fakeNode{"a": node})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := m.probe(ctx, "bsc", 56, []string{"a"})
		done <- err
	}()
	require.Eventually(t, func() bool { return node.chainCalls.Load() == 1 }, 2*time.Second, 5*time.Millisecond)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)

	probes, err := m.probe(context.Background(), "bsc", 56, []string{"a"})
	require.NoError(t, err)
	require.Equal(t, uint64(9), probes[0].Head)
	require.EqualValues(t, 1, node.chainCalls.Load(), "the second viewer reused the in-flight probe")
	require.EqualValues(t, 1, node.blockCalls.Load())
}

func TestProbeIsPacedByThePerEndpointThrottle(t *testing.T) {
	rpc.SetEndpointThrottleInterval(200 * time.Millisecond)
	t.Cleanup(func() { rpc.SetEndpointThrottleInterval(0) })
	node := newFakeNode(t, fakeNodeOpts{chainID: 56, head: 3})
	m, _, _ := newTestMonitor(t, map[string]*fakeNode{"a": node})
	started := time.Now()
	probes, err := m.probe(context.Background(), "bsc", 56, []string{"a"})
	require.NoError(t, err)
	require.True(t, probes[0].HasHead)
	// eth_chainId and eth_blockNumber go through the same endpoint throttle the
	// scanner uses, so the second call waits out the configured spacing.
	require.GreaterOrEqual(t, time.Since(started), 190*time.Millisecond)
}

func TestProductionPoolStillRefusesNonPublicDestinations(t *testing.T) {
	m := &chainMonitor{} // no seams: the real rpc.NewPool with its public-address checks
	t.Cleanup(m.stop)
	p := m.probeNode("http://127.0.0.1:1/SECRETKEY", 56)
	require.Equal(t, NodeErrConnection, p.ErrorKind)
	require.False(t, p.HasHead)
}
