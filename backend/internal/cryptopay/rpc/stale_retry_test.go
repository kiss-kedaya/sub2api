package rpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// staleConn is a connection the server has closed while it sat idle, before the
// client has noticed: the client's read loop is parked in Read, a request gets
// "written" successfully, and only then does the read come back with EOF. This is
// the race the pool's one retry exists for, made deterministic.
//
// In mode "swallow" the request never reaches the server. In mode "processed" it
// does (the server runs the handler) but the connection dies before the answer.
type staleConn struct {
	net.Conn
	mode atomic.Value // "" (healthy), "swallow" or "processed"
}

func (c *staleConn) state() string {
	s, _ := c.mode.Load().(string)
	return s
}

func (c *staleConn) Write(p []byte) (int, error) {
	switch c.state() {
	case "swallow":
		_ = c.Conn.Close() // unblocks the parked Read; the request is "written"
		return len(p), nil
	case "processed":
		n, err := c.Conn.Write(p)
		time.AfterFunc(30*time.Millisecond, func() { _ = c.Conn.Close() })
		return n, err
	}
	return c.Conn.Write(p)
}

func (c *staleConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil && c.state() != "" {
		return n, io.EOF
	}
	return n, err
}

type staleDialer struct {
	fx    *tlsFixture
	mu    sync.Mutex
	conns []*staleConn
	dials atomic.Int32
}

func (d *staleDialer) dial(ctx context.Context, network, _ string) (net.Conn, error) {
	d.dials.Add(1)
	raw, err := (&net.Dialer{}).DialContext(ctx, network, d.fx.addr())
	if err != nil {
		return nil, err
	}
	conn := &staleConn{Conn: raw}
	d.mu.Lock()
	d.conns = append(d.conns, conn)
	d.mu.Unlock()
	return conn, nil
}

// goStale marks every connection opened so far as closed-by-the-server.
func (d *staleDialer) goStale(mode string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, conn := range d.conns {
		conn.mode.Store(mode)
	}
}

func (fx *tlsFixture) staleFixturePool(t *testing.T) (*Pool, *staleDialer, *http.Client) {
	t.Helper()
	d := &staleDialer{fx: fx}
	client := fx.client(15*time.Second, func(c *publicTransportConfig) { c.dial = d.dial })
	t.Cleanup(client.CloseIdleConnections)
	return NewPoolWithHTTPClient(fx.url(), client), d, client
}

// endpointOf returns the pacing and counter state behind a test client's endpoint.
func endpointOf(t *testing.T, client *http.Client, endpoint string) *endpointTraffic {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	transport, isPublic := client.Transport.(*publicTransport)
	if !isPublic {
		t.Fatal("the client does not use the public transport")
	}
	state, ok := transport.traffic.lookup(parsed)
	if !ok {
		t.Fatal("no traffic recorded for the endpoint")
	}
	return state
}

// trafficOf reads the counters of the endpoint behind a test client.
func trafficOf(t *testing.T, client *http.Client, endpoint string) EndpointTraffic {
	t.Helper()
	return endpointOf(t, client, endpoint).snapshot()
}

// dropConnection closes the client's connection without answering.
func dropConnection(w http.ResponseWriter) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	_ = conn.Close()
}

func callOneBlockNumber(pool *Pool, endpoint string) error {
	_, err := pool.callOne(context.Background(), endpoint, "eth_blockNumber", nil)
	return err
}

func TestReadOnlyCallRetriesOnceOnAStalePooledConnection(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		for _, mode := range []string{"swallow", "processed"} {
			t.Run(fmt.Sprintf("http2=%v/%s", http2, mode), func(t *testing.T) {
				var handled atomic.Int32
				fx := newTLSFixture(t, http2, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					n := handled.Add(1)
					_ = requestMethod(r)
					if mode == "processed" && n == 2 {
						// The request that reaches the node on the stale connection is slow to
						// answer; the connection dies first (staleConn closes it after 30 ms).
						time.Sleep(300 * time.Millisecond)
					}
					rpcResult(w, `"0x10"`)
				}))
				pool, dialer, client := fx.staleFixturePool(t)
				if err := callOneBlockNumber(pool, fx.url()); err != nil {
					t.Fatalf("warm-up: %v", err)
				}
				dialer.goStale(mode)
				if err := callOneBlockNumber(pool, fx.url()); err != nil {
					t.Fatalf("a read-only call on a stale pooled connection failed instead of being retried: %v", err)
				}
				if got := dialer.dials.Load(); got != 2 {
					t.Fatalf("dials = %d, want 2 (the warm one and one new connection for the retry)", got)
				}
				traffic := trafficOf(t, client, fx.url())
				if traffic.StaleRetries != 1 || traffic.Requests != 3 {
					t.Fatalf("traffic = %+v, want 3 requests of which 1 stale retry", traffic)
				}
				// A read is safe to repeat even when the node had already seen it.
				want := int32(2)
				if mode == "processed" {
					want = 3
				}
				if got := handled.Load(); got != want {
					t.Fatalf("the node handled %d requests, want %d", got, want)
				}
			})
		}
	}
}

// The retry is for stale connections only: one retry, on a new connection, and a
// failure on that new connection is final.
func TestStaleRetryHappensAtMostOnce(t *testing.T) {
	var handled atomic.Int32
	fx := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := handled.Add(1)
		_ = requestMethod(r)
		if n >= 2 { // every request after the warm-up is dropped mid-flight
			dropConnection(w)
			return
		}
		rpcResult(w, `"0x10"`)
	}))
	pool, dialer, client := fx.staleFixturePool(t)
	if err := callOneBlockNumber(pool, fx.url()); err != nil {
		t.Fatal(err)
	}
	dialer.goStale("swallow")
	err := callOneBlockNumber(pool, fx.url())
	if err == nil {
		t.Fatal("the dropped retry must fail the call")
	}
	if CategoryOf(err) != CategoryTransport {
		t.Fatalf("category = %s, want transport", CategoryOf(err))
	}
	if got := dialer.dials.Load(); got != 2 {
		t.Fatalf("dials = %d, want 2: no third attempt", got)
	}
	if got := trafficOf(t, client, fx.url()); got.Requests != 3 || got.StaleRetries != 1 {
		t.Fatalf("traffic = %+v, want exactly one retry", got)
	}
}

// The retry must go on a NEW connection, not on whichever idle one the pool picks
// next: after a server-wide restart every idle connection is stale at once, and a
// retry that took the next pooled one would fail the same way.
func TestStaleRetryUsesANewConnectionEvenWhenSeveralPooledOnesAreStale(t *testing.T) {
	var inFlight atomic.Int32
	both := make(chan struct{})
	var once sync.Once
	fx := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = requestMethod(r)
		if inFlight.Add(1) == 2 {
			once.Do(func() { close(both) })
		}
		select { // hold the first two requests open together, so the pool opens two connections
		case <-both:
		case <-time.After(2 * time.Second):
		}
		rpcResult(w, `"0x10"`)
	}))
	pool, dialer, client := fx.staleFixturePool(t)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := callOneBlockNumber(pool, fx.url()); err != nil {
				t.Errorf("warm-up: %v", err)
			}
		}()
	}
	wg.Wait()
	if got := dialer.dials.Load(); got != 2 {
		t.Fatalf("warm-up dialled %d connections, want 2 idle ones", got)
	}
	dialer.goStale("swallow") // both idle connections are now dead, unnoticed
	if err := callOneBlockNumber(pool, fx.url()); err != nil {
		t.Fatalf("the retry must not land on the second stale connection: %v", err)
	}
	if got := dialer.dials.Load(); got != 3 {
		t.Fatalf("dials = %d, want 3 (the retry opened its own connection)", got)
	}
	if got := trafficOf(t, client, fx.url()); got.StaleRetries != 1 {
		t.Fatalf("traffic = %+v, want exactly one stale retry", got)
	}
}

// A failure on a connection that was NOT reused says nothing about staleness: the
// server hung up on a fresh connection, so the call is not repeated.
func TestFailureOnANewConnectionIsNotRetried(t *testing.T) {
	var handled atomic.Int32
	fx := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handled.Add(1)
		dropConnection(w)
	}))
	pool, _, client := fx.staleFixturePool(t)
	if err := callOneBlockNumber(pool, fx.url()); err == nil {
		t.Fatal("expected a transport failure")
	}
	if got := handled.Load(); got != 1 {
		t.Fatalf("the server saw the request %d times, want 1", got)
	}
	if got := trafficOf(t, client, fx.url()); got.StaleRetries != 0 || got.Requests != 1 {
		t.Fatalf("traffic = %+v", got)
	}
}

// A timeout on a reused connection is not "stale": the connection may be alive and
// slow, and the caller's time is already spent.
func TestTimeoutOnAReusedConnectionIsNotRetried(t *testing.T) {
	var handled atomic.Int32
	release := make(chan struct{})
	fx := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = requestMethod(r)
		if handled.Add(1) >= 2 {
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		rpcResult(w, `"0x10"`)
	}))
	defer close(release)
	pool, _, client := fx.staleFixturePool(t)
	if err := callOneBlockNumber(pool, fx.url()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := pool.callOne(ctx, fx.url(), "eth_blockNumber", nil)
	if CategoryOf(err) != CategoryTimeout {
		t.Fatalf("error = %v (category %s), want a timeout", err, CategoryOf(err))
	}
	if got := handled.Load(); got != 2 {
		t.Fatalf("the server saw %d requests, want 2 (no repeat of the slow one)", got)
	}
	if got := trafficOf(t, client, fx.url()); got.StaleRetries != 0 || got.Timeouts != 1 {
		t.Fatalf("traffic = %+v", got)
	}
}

// eth_sendRawTransaction (and anything not on the read-only list) never rides a
// pooled connection - a connection the server closed while idle cannot turn a
// broadcast into "result unknown" - and is never sent twice by this layer.
func TestBroadcastNeverUsesAPooledConnection(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%v", http2), func(t *testing.T) { testBroadcastNeverUsesAPooledConnection(t, http2) })
	}
}

func testBroadcastNeverUsesAPooledConnection(t *testing.T, http2 bool) {
	var sendRaw atomic.Int32
	fx := newTLSFixture(t, http2, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestMethod(r) == "eth_sendRawTransaction" {
			sendRaw.Add(1)
			rpcResult(w, `"0xabc"`)
			return
		}
		rpcResult(w, `"0x10"`)
	}))
	pool, dialer, _ := fx.staleFixturePool(t)
	for i := 0; i < 3; i++ {
		if err := callOneBlockNumber(pool, fx.url()); err != nil {
			t.Fatal(err)
		}
	}
	if got := dialer.dials.Load(); got != 1 {
		t.Fatalf("read-only calls dialled %d times, want 1", got)
	}
	// The pooled connection is warm. Even stale, it must not be picked for a broadcast.
	dialer.goStale("swallow")
	if _, err := pool.callOne(context.Background(), fx.url(), "eth_sendRawTransaction", []any{"0x00"}); err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	if got := dialer.dials.Load(); got != 2 {
		t.Fatalf("dials = %d, want 2: the broadcast must dial its own connection", got)
	}
	if got := sendRaw.Load(); got != 1 {
		t.Fatalf("the node saw the broadcast %d times, want 1", got)
	}
	// And each broadcast is on its own connection, never kept afterwards.
	for i := 0; i < 2; i++ {
		if _, err := pool.callOne(context.Background(), fx.url(), "eth_sendRawTransaction", []any{"0x00"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := dialer.dials.Load(); got != 4 {
		t.Fatalf("dials = %d after three broadcasts, want 4 (one connection each, plus the pooled one)", got)
	}
}

// When the connection dies after the node received a broadcast, the result is
// unknown. This layer reports it (once) and does not repeat it: settling it is the
// receipt check's job, as before connections were reused.
func TestBroadcastWithUnknownResultIsReportedNotRepeated(t *testing.T) {
	var sendRaw atomic.Int32
	fx := newTLSFixture(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestMethod(r) == "eth_sendRawTransaction" {
			sendRaw.Add(1)
			dropConnection(w) // the node got it, then the connection died
			return
		}
		rpcResult(w, `"0x10"`)
	}))
	pool, _, client := fx.staleFixturePool(t)
	if err := callOneBlockNumber(pool, fx.url()); err != nil {
		t.Fatal(err)
	}
	_, err := pool.callOne(context.Background(), fx.url(), "eth_sendRawTransaction", []any{"0x00"})
	if err == nil || CategoryOf(err) != CategoryTransport {
		t.Fatalf("error = %v, want a transport failure", err)
	}
	if got := sendRaw.Load(); got != 1 {
		t.Fatalf("the broadcast reached the node %d times, want 1", got)
	}
	if got := trafficOf(t, client, fx.url()); got.StaleRetries != 0 {
		t.Fatalf("traffic = %+v: a broadcast must never be retried here", got)
	}
}

func TestReadOnlyMethodAllowList(t *testing.T) {
	for _, method := range []string{
		"eth_chainId", "eth_blockNumber", "eth_gasPrice", "eth_getBalance", "eth_getTransactionCount",
		"eth_getBlockByNumber", "eth_getLogs", "eth_getTransactionByHash", "eth_getTransactionReceipt", "eth_call",
	} {
		if !isReadOnlyMethod(method) {
			t.Errorf("%s is a read and must be on the list", method)
		}
	}
	for _, method := range []string{
		"eth_sendRawTransaction", "eth_sendTransaction", "personal_sign", "personal_sendTransaction",
		"debug_traceTransaction", "admin_addPeer", "eth_newFilter", "eth_getFilterChanges", "txpool_content", "", "ETH_BLOCKNUMBER", "eth_unknownFutureMethod",
	} {
		if isReadOnlyMethod(method) {
			t.Errorf("%q must not be on the read-only list: it is never retried here and never rides a pooled connection", method)
		}
	}
}

func TestIsStaleConnError(t *testing.T) {
	wrap := func(err error) error { return &url.Error{Op: "Post", URL: "https://rpc.example.test/key", Err: err} }
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"EOF":                               {wrap(io.EOF), true},
		"unexpected EOF":                    {wrap(io.ErrUnexpectedEOF), true},
		"closed connection":                 {wrap(net.ErrClosed), true},
		"reset":                             {wrap(&net.OpError{Op: "read", Err: syscall.ECONNRESET}), true},
		"broken pipe":                       {wrap(&net.OpError{Op: "write", Err: syscall.EPIPE}), true},
		"server closed idle":                {wrap(errors.New("http: server closed idle connection")), true},
		"h2 conn lost":                      {wrap(errors.New("http2: client connection lost")), true},
		"h2 goaway":                         {wrap(errors.New("http2: server sent GOAWAY and closed the connection; LastStreamID=1")), true},
		"reset by peer text":                {wrap(errors.New("read tcp 10.0.0.1:5->1.2.3.4:443: read: connection reset by peer")), true},
		"deadline":                          {wrap(context.DeadlineExceeded), false},
		"canceled":                          {wrap(context.Canceled), false},
		"deadline over a closed connection": {wrap(fmt.Errorf("%w: %w", context.DeadlineExceeded, net.ErrClosed)), false},
		"cancel over an EOF":                {wrap(fmt.Errorf("%w: %w", context.Canceled, io.EOF)), false},
		"refused":                           {wrap(errors.New("dial tcp 1.2.3.4:443: connect: connection refused")), false},
		"tls":                               {wrap(errors.New("tls: handshake failure")), false},
		"not public":                        {wrap(errors.New("rpc destination is not public")), false},
		"local rate limit":                  {wrap(errLocalRateLimit), false},
		"url text must not decide":          {&url.Error{Op: "Post", URL: "https://rpc.example.test/EOF/broken pipe", Err: errors.New("boom")}, false},
		"nil":                               {nil, false},
	} {
		if got := isStaleConnError(tc.err); got != tc.want {
			t.Errorf("%s: isStaleConnError = %v, want %v", name, got, tc.want)
		}
	}
}
