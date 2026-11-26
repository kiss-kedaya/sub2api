package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type seedNode struct {
	mu   sync.Mutex
	hits map[string]int
	srv  *httptest.Server
}

// newMethodNode answers blocks always, and eth_getLogs either with an empty (valid)
// result or, when refuseLogs is set, with the -32005 "limit exceeded" the public BNB
// Chain seeds give for every log query.
func newMethodNode(t *testing.T, refuseLogs bool) *seedNode {
	t.Helper()
	node := &seedNode{hits: map[string]int{}}
	node.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := requestMethod(r)
		node.mu.Lock()
		node.hits[method]++
		node.mu.Unlock()
		switch {
		case method == "eth_getLogs" && refuseLogs:
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1,
				"error": map[string]any{"code": -32005, "message": "limit exceeded"}})
		case method == "eth_getLogs":
			rpcResult(w, `[]`)
		default:
			rpcResult(w, `{"number":"0x10"}`)
		}
	}))
	t.Cleanup(node.srv.Close)
	return node
}

func (n *seedNode) hitsOf(method string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.hits[method]
}

func logsFilter() []any {
	return []any{map[string]any{"fromBlock": "0x1", "toBlock": "0x10"}}
}

// A node that refuses every log query is left out of log queries after a few
// refusals in a row, keeps serving blocks, and is asked again after the cooldown.
func TestNodeThatRefusesLogsIsLeftOutOfLogsButStillServesBlocks(t *testing.T) {
	clock := useFakeClock(t)
	seed := newMethodNode(t, true)
	good := newMethodNode(t, false)
	pool := NewPoolWithHTTPClient(seed.srv.URL+","+good.srv.URL, seed.srv.Client())

	for i := 0; i < 20; i++ {
		if _, err := pool.Call(t.Context(), "eth_getLogs", logsFilter()); err != nil {
			t.Fatalf("log query %d failed although a node answers them: %v", i, err)
		}
	}
	if got := seed.hitsOf("eth_getLogs"); got != methodRefusalThreshold {
		t.Fatalf("the refusing node was asked %d log queries, want %d and then none", got, methodRefusalThreshold)
	}
	for i := 0; i < 20; i++ {
		if _, err := pool.Call(t.Context(), "eth_getBlockByNumber", []any{"0x10", true}); err != nil {
			t.Fatalf("block read %d: %v", i, err)
		}
	}
	if got := seed.hitsOf("eth_getBlockByNumber"); got < 5 {
		t.Fatalf("the node that refuses logs served %d of 20 block reads: it must keep serving blocks", got)
	}

	clock.Advance(9 * time.Minute)
	for i := 0; i < 10; i++ {
		_, _ = pool.Call(t.Context(), "eth_getLogs", logsFilter())
	}
	if got := seed.hitsOf("eth_getLogs"); got != methodRefusalThreshold {
		t.Fatalf("asked again after 9 minutes (%d log queries)", got)
	}
	clock.Advance(2 * time.Minute)
	for i := 0; i < 4; i++ {
		_, _ = pool.Call(t.Context(), "eth_getLogs", logsFilter())
	}
	if got := seed.hitsOf("eth_getLogs"); got != methodRefusalThreshold+1 {
		t.Fatalf("after the cooldown the node was asked %d log queries in total, want %d (one probe, then left out again)", got, methodRefusalThreshold+1)
	}
}

func TestOnlyConsecutiveRefusalsLeaveANodeOut(t *testing.T) {
	useFakeClock(t)
	tracker := newEndpointHealthTracker()
	refusal := &Failure{Kind: "remote", RPCCode: -32005}
	endpoints := []string{"https://a.example", "https://b.example"}

	tracker.noteMethodFailure(endpoints[0], "eth_getLogs", refusal)
	tracker.noteMethodFailure(endpoints[0], "eth_getLogs", refusal)
	tracker.noteMethodSuccess(endpoints[0], "eth_getLogs") // it did serve one
	tracker.noteMethodFailure(endpoints[0], "eth_getLogs", refusal)
	tracker.noteMethodFailure(endpoints[0], "eth_getLogs", refusal)
	if got := tracker.servingMethod(endpoints, "eth_getLogs"); len(got) != 2 {
		t.Fatalf("serving = %v: a success in between must reset the count", got)
	}
	tracker.noteMethodFailure(endpoints[0], "eth_getLogs", refusal)
	if got := tracker.servingMethod(endpoints, "eth_getLogs"); len(got) != 1 || got[0] != endpoints[1] {
		t.Fatalf("serving = %v, want only the node that does not refuse", got)
	}
	// Other methods are untouched, and so is a success of another method.
	if got := tracker.servingMethod(endpoints, "eth_blockNumber"); len(got) != 2 {
		t.Fatalf("serving for another method = %v", got)
	}
	tracker.succeed(endpoints[0])
	if got := tracker.servingMethod(endpoints, "eth_getLogs"); len(got) != 1 {
		t.Fatalf("a success of the node's general health forgave its log refusals: %v", got)
	}
	// When every node refuses, all of them are asked rather than none.
	tracker.noteMethodFailure(endpoints[1], "eth_getLogs", refusal)
	tracker.noteMethodFailure(endpoints[1], "eth_getLogs", refusal)
	tracker.noteMethodFailure(endpoints[1], "eth_getLogs", refusal)
	if got := tracker.servingMethod(endpoints, "eth_getLogs"); len(got) != 2 {
		t.Fatalf("serving = %v: when all refuse, probe all", got)
	}
}

func TestIsMethodRefusal(t *testing.T) {
	for name, tc := range map[string]struct {
		method string
		err    error
		want   bool
	}{
		"limit exceeded":          {"eth_getLogs", &Failure{Kind: "remote", RPCCode: -32005}, true},
		"method not found":        {"eth_getLogs", &Failure{Kind: "remote", RPCCode: -32601}, true},
		"wrapped":                 {"eth_getLogs", wrapFailure(&Failure{Kind: "remote", RPCCode: -32005}), true},
		"a recognised rate limit": {"eth_getLogs", &Failure{Kind: "remote", RPCCode: -32005, Reason: "rate limited"}, false},
		"a range limit":           {"eth_getLogs", &Failure{Kind: "remote", RPCCode: -32005, Reason: ReasonRangeLimit}, false},
		"a stated span":           {"eth_getLogs", &Failure{Kind: "remote", RPCCode: -32005, SpanLimit: 100}, false},
		"another code":            {"eth_getLogs", &Failure{Kind: "remote", RPCCode: -32000}, false},
		"an http failure":         {"eth_getLogs", &Failure{Kind: "http", HTTPStatus: 429}, false},
		"a timeout":               {"eth_getLogs", NewTransportFailure(errTimeoutForTest{}), false},
		"another method":          {"eth_getBlockByNumber", &Failure{Kind: "remote", RPCCode: -32005}, false},
		"nil":                     {"eth_getLogs", nil, false},
	} {
		if got := isMethodRefusal(tc.method, tc.err); got != tc.want {
			t.Errorf("%s: isMethodRefusal = %v, want %v", name, got, tc.want)
		}
	}
}

type errTimeoutForTest struct{}

func (errTimeoutForTest) Error() string { return "timeout" }

func wrapFailure(f *Failure) error { return &wrappedFailure{f} }

type wrappedFailure struct{ inner *Failure }

func (w *wrappedFailure) Error() string { return "all rpc endpoints failed: " + w.inner.Error() }
func (w *wrappedFailure) Unwrap() error { return w.inner }
