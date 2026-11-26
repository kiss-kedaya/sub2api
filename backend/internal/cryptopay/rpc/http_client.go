package rpc

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

type lookupIPAddrFunc func(context.Context, string) ([]net.IPAddr, error)
type dialContextFunc func(context.Context, string, string) (net.Conn, error)

const (
	defaultRPCTimeout = 15 * time.Second
	dialTimeout       = 10 * time.Second
	tcpKeepAlive      = 30 * time.Second
	// The handshake bound is the old one. The header bound of the shared transport
	// is the longest client timeout in the process (the admin probe's 20 s); each
	// client's own Timeout is the bound that applies.
	tlsHandshakeTimeout = 10 * time.Second
	sharedHeaderTimeout = 20 * time.Second

	// maxConnsPerHost bounds the connections one endpoint host can hold open (and
	// so the calls it can have in flight on HTTP/1.1). Before, nothing did: every
	// call opened its own.
	maxConnsPerHost = 16
	// maxIdleConns bounds the connections kept warm across all hosts.
	maxIdleConns = 128
	// idleConnTimeout closes a connection that has been quiet this long. It is
	// shorter than the idle timeouts providers, load balancers and NAT gateways
	// commonly use (60 s is the usual one, and a client that waits exactly as long
	// as the other side races it), so the pool drops a connection before the other
	// side does and a "stale" connection stays the rare race that the one retry in
	// Pool covers.
	idleConnTimeout       = 45 * time.Second
	expectContinueTimeout = time.Second
	maxResponseHeaderSize = 16 << 10

	// h2SendPingTimeout and h2PingTimeout are the HTTP/2 health check of a pooled
	// connection. Go's default is none: a connection that goes silent without a FIN
	// or an RST (a path black hole, a NAT that dropped the flow) is then reused by
	// every call, each waiting out its full timeout, for as long as traffic keeps
	// coming - an HTTP/1.1 connection is dropped after its first timeout, a
	// multiplexed one is not - until the kernel gives up on the TCP retransmits
	// (about 15 minutes on Linux). With the check, a connection that has received no
	// frame for h2SendPingTimeout gets a ping and is closed if h2PingTimeout later
	// nothing came back, so a dead connection costs one or two calls, not a stalled
	// chain. A healthy server answers a ping at once, however slow its handlers are.
	h2SendPingTimeout = 15 * time.Second
	h2PingTimeout     = 10 * time.Second
)

// callHint travels in the request context from Pool.callOne to the transport.
type callHint struct {
	// fresh sends the request on a new connection that is closed afterwards.
	// Used for methods that may change state and for the one retry after a stale
	// connection.
	fresh bool
	// staleRetry marks that retry, for the traffic summary only.
	staleRetry bool
	// maxWait bounds how long the endpoint's pacing may hold this call. Zero means
	// the default (limiterMaxWait). Pool sets a short one while another endpoint is
	// left to try, so a busy endpoint is skipped instead of waited for.
	maxWait time.Duration
}

type callHintKey struct{}

// publicRPCNetwork turns a host name into a connection, and refuses anything that
// is not a public address. Every connection the transport ever opens - pooled or
// one-shot - comes from dialContext, so the check cannot be skipped by reuse: a
// pooled connection is, by construction, one that already passed it, and a TCP
// connection cannot change the address it is connected to.
type publicRPCNetwork struct {
	dns  *dnsCache
	dial dialContextFunc
}

func (n *publicRPCNetwork) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return nil, errors.New("invalid rpc destination")
	}

	var ips []net.IPAddr
	if literal, parseErr := netip.ParseAddr(strings.TrimSuffix(host, ".")); parseErr == nil {
		ips = []net.IPAddr{{IP: net.IP(literal.AsSlice()), Zone: literal.Zone()}}
	} else {
		ips, err = n.dns.resolve(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("rpc DNS lookup failed")
		}
	}

	// The check runs on every dial and on every answer, cached or not: the cache
	// remembers what DNS said, never that an address was acceptable.
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip.IP)
		if !ok || ip.Zone != "" || !urlvalidator.IsPublicResolvedIP(addr) {
			return nil, errors.New("rpc destination is not public")
		}
	}

	for _, ip := range ips {
		conn, dialErr := n.dial(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, errors.New("rpc connection failed")
}

type publicTransportConfig struct {
	lookup        lookupIPAddrFunc
	dial          dialContextFunc
	headerTimeout time.Duration
	now           func() time.Time

	// Seams for tests. The zero values are the production settings.
	tlsConfig     *tls.Config
	idleTimeout   time.Duration
	dnsTTL        time.Duration
	ratePerSec    float64
	h2SendPing    time.Duration
	h2PingTimeout time.Duration
	trafficSink   func(host string, traffic EndpointTraffic)
}

// publicTransport is the http.RoundTripper behind every configured RPC endpoint.
//
//   - pooled keeps connections alive and speaks HTTP/2 where the provider does, so
//     a scan pass reuses a handful of connections instead of opening one per call
//     (a TCP+TLS handshake per call on a busy host is the likely cause of the
//     timeouts and of the TIME_WAIT pile seen with the stalled BSC cursor).
//   - fresh never reuses a connection; see callHint.fresh.
//
// Both dial through publicRPCNetwork. Compression stays on (DisableCompression is
// false): the answers are JSON, mostly large, and gzip cuts them tenfold. Callers
// bound what they read from the decompressed stream, so a hostile answer cannot
// grow memory by being small on the wire.
type publicTransport struct {
	pooled  *http.Transport
	fresh   *http.Transport
	traffic *trafficRegistry
	now     func() time.Time
}

func newPublicTransport(cfg publicTransportConfig) *publicTransport {
	now := cfg.now
	if now == nil {
		now = time.Now
	}
	network := &publicRPCNetwork{dns: newDNSCache(cfg.lookup, now, cfg.dnsTTL), dial: cfg.dial}
	build := func() *http.Transport {
		transport := &http.Transport{
			Proxy:                  nil,
			DialContext:            network.dialContext,
			TLSHandshakeTimeout:    tlsHandshakeTimeout,
			ResponseHeaderTimeout:  cfg.headerTimeout,
			ExpectContinueTimeout:  expectContinueTimeout,
			MaxResponseHeaderBytes: maxResponseHeaderSize,
			MaxConnsPerHost:        maxConnsPerHost,
			ForceAttemptHTTP2:      true,
		}
		if cfg.tlsConfig != nil {
			transport.TLSClientConfig = cfg.tlsConfig.Clone()
		}
		return transport
	}
	pooled := build()
	pooled.MaxIdleConns = maxIdleConns
	pooled.MaxIdleConnsPerHost = maxConnsPerHost
	pooled.IdleConnTimeout = idleConnTimeout
	if cfg.idleTimeout > 0 {
		pooled.IdleConnTimeout = cfg.idleTimeout
	}
	// Only the pooled transport: a one-shot connection is gone after its one call.
	pooled.HTTP2 = &http.HTTP2Config{SendPingTimeout: h2SendPingTimeout, PingTimeout: h2PingTimeout}
	if cfg.h2SendPing > 0 {
		pooled.HTTP2.SendPingTimeout = cfg.h2SendPing
	}
	if cfg.h2PingTimeout > 0 {
		pooled.HTTP2.PingTimeout = cfg.h2PingTimeout
	}
	fresh := build()
	fresh.DisableKeepAlives = true
	sink := cfg.trafficSink
	if sink == nil {
		sink = logTraffic
	}
	return &publicTransport{pooled: pooled, fresh: fresh, traffic: newTrafficRegistry(now, sink, cfg.ratePerSec), now: now}
}

// RoundTrip paces the call (see endpoint_traffic.go), sends it on the pooled or
// the one-shot transport, and accounts for the outcome.
func (t *publicTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	hint, _ := ctx.Value(callHintKey{}).(callHint)
	endpoint := t.traffic.get(req.URL)
	if endpoint != nil {
		if err := endpoint.acquire(ctx, hint.maxWait); err != nil {
			return nil, err
		}
		endpoint.sent(hint.staleRetry)
	}
	base := t.pooled
	if hint.fresh {
		base = t.fresh
	}
	var newConn atomic.Bool
	traced := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { newConn.Store(!info.Reused) },
	})
	started := t.now()
	resp, err := base.RoundTrip(req.WithContext(traced))
	if endpoint == nil {
		return resp, err
	}
	if err != nil {
		endpoint.answeredWith(0, err, newConn.Load())
		endpoint.finished(t.now().Sub(started))
		return nil, err
	}
	endpoint.answeredWith(resp.StatusCode, nil, newConn.Load())
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		now := t.now()
		endpoint.rateLimited(started, now, parseRetryAfter(resp.Header, now))
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		endpoint.answered(t.now())
	}
	resp.Body = &timedBody{ReadCloser: resp.Body, done: func() { endpoint.finished(t.now().Sub(started)) }}
	return resp, nil
}

// CloseIdleConnections drops every kept-alive connection.
func (t *publicTransport) CloseIdleConnections() {
	t.pooled.CloseIdleConnections()
	t.fresh.CloseIdleConnections()
}

// timedBody tells the traffic summary how long the whole exchange took, including
// reading the body, once the caller closes it.
type timedBody struct {
	io.ReadCloser
	once sync.Once
	done func()
}

func (b *timedBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.done)
	return err
}

func newPublicClient(transport http.RoundTripper, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: transport,
	}
}

var (
	sharedTransportOnce sync.Once
	sharedTransport     *publicTransport
)

// sharedPublicTransport is the one transport of the process. Every Pool, the
// node-status probe and the admin console go through it, so connections, resolved
// addresses and per-endpoint pacing are shared instead of being rebuilt (and
// leaked, with keep-alive on) every time a Pool is derived from configuration.
func sharedPublicTransport() *publicTransport {
	sharedTransportOnce.Do(func() {
		dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: tcpKeepAlive}
		sharedTransport = newPublicTransport(publicTransportConfig{
			lookup:        net.DefaultResolver.LookupIPAddr,
			dial:          dialer.DialContext,
			headerTimeout: sharedHeaderTimeout,
		})
	})
	return sharedTransport
}

// NewPublicHTTPClient builds the direct-only HTTP client used for configured
// RPC endpoints. It rejects private DNS answers and dials the exact address
// that was checked, preventing rebinding between validation and connection.
// Clients share one connection pool; only the per-call timeout is per client.
func NewPublicHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = defaultRPCTimeout
	}
	return newPublicClient(sharedPublicTransport(), timeout)
}

func newPublicHTTPClient(timeout time.Duration, lookup lookupIPAddrFunc, dial dialContextFunc) *http.Client {
	return newPublicHTTPClientConfig(timeout, publicTransportConfig{lookup: lookup, dial: dial})
}

// newPublicHTTPClientConfig builds a client over a private transport; tests use it
// to inject DNS, dialing, trust roots and clocks.
func newPublicHTTPClientConfig(timeout time.Duration, cfg publicTransportConfig) *http.Client {
	if cfg.headerTimeout <= 0 {
		cfg.headerTimeout = timeout
	}
	return newPublicClient(newPublicTransport(cfg), timeout)
}

// SetEndpointRateCeiling sets the request-rate ceiling (calls per second) of every
// endpoint. Zero restores the default. The rate of an endpoint that is currently
// being throttled is lowered to the new ceiling if it is above it, and climbs back
// on its own.
func SetEndpointRateCeiling(perSec int) {
	rate := float64(defaultRatePerSec)
	if perSec > 0 {
		rate = float64(min(perSec, MaxRatePerSec))
	}
	sharedPublicTransport().traffic.setCeiling(rate)
}

// EndpointRateCeiling reports the configured ceiling in calls per second.
func EndpointRateCeiling() float64 {
	registry := sharedPublicTransport().traffic
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.ceiling
}

// EndpointTrafficFor returns the current traffic window of one configured
// endpoint, or false when the endpoint has not been used.
func EndpointTrafficFor(endpoint string) (EndpointTraffic, bool) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return EndpointTraffic{}, false
	}
	state, ok := sharedPublicTransport().traffic.lookup(parsed)
	if !ok {
		return EndpointTraffic{}, false
	}
	return state.snapshot(), true
}
