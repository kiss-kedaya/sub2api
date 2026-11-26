package rpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

// tlsFixture is a local TLS JSON-RPC server that counts the TCP connections it
// accepts. The client side reaches it under the name example.com (the name in
// httptest's certificate) with a fake public address, so the production
// public-address check runs unchanged and only the final dial is redirected.
type tlsFixture struct {
	server *httptest.Server
	conns  atomic.Int64
	roots  *x509.CertPool
	port   string
}

func newTLSFixture(t testing.TB, http2 bool, handler http.Handler) *tlsFixture {
	t.Helper()
	fx := &tlsFixture{}
	fx.server = httptest.NewUnstartedServer(handler)
	fx.server.EnableHTTP2 = http2
	fx.server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			fx.conns.Add(1)
		}
	}
	fx.server.StartTLS()
	t.Cleanup(fx.server.Close)
	fx.roots = x509.NewCertPool()
	fx.roots.AddCert(fx.server.Certificate())
	_, fx.port, _ = net.SplitHostPort(fx.server.Listener.Addr().String())
	return fx
}

func (fx *tlsFixture) url() string { return "https://example.com:" + fx.port }

func (fx *tlsFixture) addr() string { return fx.server.Listener.Addr().String() }

// config is a transport configuration whose DNS answer is a fake public address
// and whose dial goes to the fixture.
func (fx *tlsFixture) config() publicTransportConfig {
	return publicTransportConfig{
		lookup: func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}, nil
		},
		dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, fx.addr())
		},
		tlsConfig: &tls.Config{RootCAs: fx.roots},
		// Tests that are not about pacing must not be paced: back-to-back calls to a
		// local server are far above the production ceiling.
		ratePerSec: 1e6,
	}
}

func (fx *tlsFixture) client(timeout time.Duration, mutate ...func(*publicTransportConfig)) *http.Client {
	cfg := fx.config()
	for _, m := range mutate {
		m(&cfg)
	}
	return newPublicHTTPClientConfig(timeout, cfg)
}

func (fx *tlsFixture) pool(t testing.TB, mutate ...func(*publicTransportConfig)) *Pool {
	t.Helper()
	client := fx.client(15*time.Second, mutate...)
	t.Cleanup(client.CloseIdleConnections)
	return NewPoolWithHTTPClient(fx.url(), client)
}

// legacyClient reproduces the client as it was before connections were reused:
// no keep-alive, no compression, the same public-address check on every call.
func (fx *tlsFixture) legacyClient(dialDelay time.Duration) *http.Client {
	cfg := fx.config()
	network := &publicRPCNetwork{
		dns: newDNSCache(cfg.lookup, time.Now, 0),
		dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			time.Sleep(dialDelay)
			return cfg.dial(ctx, network, address)
		},
	}
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy:                  nil,
			DialContext:            network.dialContext,
			TLSClientConfig:        cfg.tlsConfig,
			TLSHandshakeTimeout:    10 * time.Second,
			ResponseHeaderTimeout:  15 * time.Second,
			MaxResponseHeaderBytes: 16 << 10,
			DisableKeepAlives:      true,
			DisableCompression:     true,
			ForceAttemptHTTP2:      true,
		},
	}
}

// rpcResult answers a JSON-RPC request with a fixed result.
func rpcResult(w http.ResponseWriter, result string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":` + result + `}`))
}

// requestMethod reads the JSON-RPC method of a request.
func requestMethod(r *http.Request) string {
	var req struct {
		Method string `json:"method"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	return req.Method
}

// blockNumberHandler answers every method with 0x10.
func blockNumberHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = requestMethod(r)
		rpcResult(w, `"0x10"`)
	})
}

func mustParseURL(t testing.TB, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
