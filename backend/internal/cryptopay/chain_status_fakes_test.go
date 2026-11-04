package cryptopay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

// fakeNodeOpts fixes a fake node's behaviour before its server starts, so the
// handler goroutines only ever read it.
type fakeNodeOpts struct {
	chainID  uint64
	head     uint64
	delay    time.Duration
	status   int    // forced HTTP status; the body carries a marker secret
	body     string // forced raw 200 body
	rpcError bool   // JSON-RPC error object whose message carries a marker secret
	badChain string // forced eth_chainId result string
}

// fakeNode is an httptest JSON-RPC node. Nothing in the tests touches a real
// RPC provider or a real database.
type fakeNode struct {
	srv        *httptest.Server
	head       atomic.Uint64
	chainCalls atomic.Int64
	blockCalls atomic.Int64
	otherCalls atomic.Int64
}

func newFakeNode(t *testing.T, o fakeNodeOpts) *fakeNode {
	t.Helper()
	n := &fakeNode{}
	n.head.Store(o.head)
	n.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Method {
		case "eth_chainId":
			n.chainCalls.Add(1)
		case "eth_blockNumber":
			n.blockCalls.Add(1)
		default:
			n.otherCalls.Add(1)
		}
		if o.delay > 0 {
			select {
			case <-time.After(o.delay):
			case <-r.Context().Done():
				return
			}
		}
		switch {
		case o.status != 0:
			w.WriteHeader(o.status)
			_, _ = w.Write([]byte(`{"error":"SECRETBODY"}`))
		case o.body != "":
			_, _ = w.Write([]byte(o.body))
		case o.rpcError:
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32005,"message":"limit reached SECRETRPC"}}`))
		default:
			result := hexutil.EncodeUint64(n.head.Load())
			if req.Method == "eth_chainId" {
				result = hexutil.EncodeUint64(o.chainID)
				if o.badChain != "" {
					result = o.badChain
				}
			}
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":%q}`, result)
		}
	}))
	t.Cleanup(n.srv.Close)
	return n
}

// poolFor maps a configured endpoint to its fake node. The pool talks to the
// httptest server directly; configured endpoints never use this constructor.
func poolFor(nodes map[string]*fakeNode) func(string) *rpc.Pool {
	return func(endpoint string) *rpc.Pool {
		if n := nodes[endpoint]; n != nil {
			return rpc.NewPoolWithHTTPClient(n.srv.URL, n.srv.Client())
		}
		return rpc.NewPool("") // no endpoint: every call fails
	}
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fakeTicker hands the monitor channels the test fires by hand.
type fakeTicker struct {
	mu      sync.Mutex
	chans   []chan time.Time
	stopped int
}

func (f *fakeTicker) New(time.Duration) (<-chan time.Time, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 8)
	f.chans = append(f.chans, ch)
	return ch, func() {
		f.mu.Lock()
		f.stopped++
		f.mu.Unlock()
	}
}

func (f *fakeTicker) Created() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.chans)
}

func (f *fakeTicker) Stopped() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopped
}

func (f *fakeTicker) Fire(i int) {
	f.mu.Lock()
	ch := f.chans[i]
	f.mu.Unlock()
	ch <- time.Now()
}

// newTestMonitor builds a standalone monitor whose clock, ticker and pools are fakes.
func newTestMonitor(t *testing.T, nodes map[string]*fakeNode) (*chainMonitor, *fakeClock, *fakeTicker) {
	t.Helper()
	clock, tick := newFakeClock(), &fakeTicker{}
	m := &chainMonitor{now: clock.Now, newPool: poolFor(nodes), newTicker: tick.New}
	t.Cleanup(m.stop)
	return m, clock, tick
}
