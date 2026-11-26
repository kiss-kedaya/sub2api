package rpc

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
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
		clock.Advance(methodRefusalEventWindow + time.Second) // one refusal per event: a window apart
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

	clock.Advance(7 * time.Minute) // (the learning above took about a minute of the ten)
	for i := 0; i < 10; i++ {
		_, _ = pool.Call(t.Context(), "eth_getLogs", logsFilter())
	}
	if got := seed.hitsOf("eth_getLogs"); got != methodRefusalThreshold {
		t.Fatalf("asked again 8 minutes after it was left out (%d log queries)", got)
	}
	clock.Advance(4 * time.Minute)
	for i := 0; i < 4; i++ {
		_, _ = pool.Call(t.Context(), "eth_getLogs", logsFilter())
	}
	if got := seed.hitsOf("eth_getLogs"); got != methodRefusalThreshold+1 {
		t.Fatalf("after the cooldown the node was asked %d log queries in total, want %d (one probe, then left out again)", got, methodRefusalThreshold+1)
	}
}

func TestOnlyConsecutiveRefusalsLeaveANodeOut(t *testing.T) {
	clock := useFakeClock(t)
	tracker := newEndpointHealthTracker()
	refusal := &Failure{Kind: "remote", RPCCode: -32005}
	endpoints := []string{"https://a.example", "https://b.example"}
	refuse := func(endpoint string) {
		clock.Advance(methodRefusalEventWindow + time.Second) // a new event each time
		tracker.noteMethodFailure(endpoint, "eth_getLogs", refusal)
	}

	refuse(endpoints[0])
	refuse(endpoints[0])
	tracker.noteMethodSuccess(endpoints[0], "eth_getLogs") // it did serve one
	refuse(endpoints[0])
	refuse(endpoints[0])
	if got := tracker.servingMethod(endpoints, "eth_getLogs"); len(got) != 2 {
		t.Fatalf("serving = %v: a success in between must reset the count", got)
	}
	refuse(endpoints[0])
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
	refuse(endpoints[1])
	refuse(endpoints[1])
	refuse(endpoints[1])
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

// When the only node that serves logs is cooling down (a 429, a timeout) a log query
// is sent to it anyway - it fails retryably - and NOT to the node known to refuse
// logs: that one answers -32005, which is final and makes the scanner split the
// query for nothing. The order of the two filters decides it.
func TestLogQueryGoesToTheOnlyLogServingNodeWhileItCoolsDown(t *testing.T) {
	clock := useFakeClock(t)
	seed := newMethodNode(t, true)
	var limited atomic.Bool
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestMethod(r) != "eth_getLogs" {
			rpcResult(w, `{"number":"0x10"}`)
			return
		}
		if limited.Load() {
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		rpcResult(w, `[]`)
	}))
	t.Cleanup(good.Close)
	pool := NewPoolWithHTTPClient(seed.srv.URL+","+good.URL, good.Client())
	for i := 0; i < 12 && seed.hitsOf("eth_getLogs") < methodRefusalThreshold; i++ {
		clock.Advance(methodRefusalEventWindow + time.Second)
		if _, err := pool.Call(t.Context(), "eth_getLogs", logsFilter()); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	if got := seed.hitsOf("eth_getLogs"); got != methodRefusalThreshold {
		t.Fatalf("setup: the refusing node was asked %d log queries, want %d", got, methodRefusalThreshold)
	}

	limited.Store(true) // from now on the good node answers 429 and cools down
	seedBefore := seed.hitsOf("eth_getLogs")
	for i := 0; i < 4; i++ {
		_, err := pool.Call(t.Context(), "eth_getLogs", logsFilter())
		var failure *Failure
		if !errors.As(err, &failure) || failure.Kind != "http" || failure.HTTPStatus != http.StatusTooManyRequests {
			t.Fatalf("call %d: error %v, want the log-serving node's retryable 429, not the refusing node's answer", i, err)
		}
	}
	if got := seed.hitsOf("eth_getLogs"); got != seedBefore {
		t.Fatalf("the refusing node was asked %d more log queries while the good one cooled down", got-seedBefore)
	}
}

// Three calls in flight at the same instant are ONE refusal event: they all met the
// same node in the same moment (a node that answers -32005 in words we do not
// recognise as a rate limit does that to all of them), and a healthy node must not be
// left out of log queries for ten minutes because of it.
func TestConcurrentRefusalsAreOneEvent(t *testing.T) {
	useFakeClock(t)
	tracker := newEndpointHealthTracker()
	refusal := &Failure{Kind: "remote", RPCCode: -32005}
	endpoints := []string{"https://a.example", "https://b.example"}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tracker.noteMethodFailure(endpoints[0], "eth_getLogs", refusal)
		}()
	}
	wg.Wait()
	if got := tracker.servingMethod(endpoints, "eth_getLogs"); len(got) != 2 {
		t.Fatalf("serving = %v: three refusals at one instant left a node out", got)
	}
}

func TestConcurrentLogQueriesDoNotExcludeAHealthyNode(t *testing.T) {
	useFakeClock(t)
	node := newMethodNode(t, true) // answers -32005 to every log query, all at once
	pool := NewPoolWithHTTPClient(node.srv.URL, node.srv.Client())
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = pool.Call(t.Context(), "eth_getLogs", logsFilter())
		}()
	}
	wg.Wait()
	endpoints := []string{node.srv.URL, "https://other.example"}
	if got := pool.healthTracker().servingMethod(endpoints, "eth_getLogs"); len(got) != 2 {
		t.Fatalf("serving = %v: six simultaneous refusals left the node out for ten minutes", got)
	}
}

// Refusals are counted once per event window (3 s): the ones inside the window of a
// counted refusal belong to its event, whatever their number, and the window does not
// slide with them.
func TestRefusalsAreCountedOncePerEventWindow(t *testing.T) {
	clock := useFakeClock(t)
	tracker := newEndpointHealthTracker()
	refusal := &Failure{Kind: "remote", RPCCode: -32005}
	endpoints := []string{"https://a.example", "https://b.example"}
	serving := func() int { return len(tracker.servingMethod(endpoints, "eth_getLogs")) }
	// A refusal every 2 s: events at 0 s, 4 s and 8 s (2 s, 6 s belong to the event before them).
	for _, at := range []int{0, 2, 4, 6} {
		clock.now = time.Date(2026, 11, 26, 12, 0, at, 0, time.UTC)
		tracker.noteMethodFailure(endpoints[0], "eth_getLogs", refusal)
	}
	if serving() != 2 {
		t.Fatal("two events (0 s and 4 s) must not leave the node out")
	}
	clock.now = time.Date(2026, 11, 26, 12, 0, 8, 0, time.UTC)
	tracker.noteMethodFailure(endpoints[0], "eth_getLogs", refusal)
	if serving() != 1 {
		t.Fatal("the third event (8 s) must leave the node out")
	}
}
