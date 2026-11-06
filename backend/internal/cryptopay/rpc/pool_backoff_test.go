package rpc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func shrinkRetryDelays(t *testing.T) {
	t.Helper()
	base, maxDelay := callRetryBaseDelay, callRetryMaxDelay
	callRetryBaseDelay = time.Millisecond
	callRetryMaxDelay = 2 * time.Millisecond
	t.Cleanup(func() {
		callRetryBaseDelay = base
		callRetryMaxDelay = maxDelay
	})
}

func jsonRPCSuccess(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
}

// scriptedTransport fails the first failN round trips at the transport layer
// (dial errors), which is the case the scanner sees as "RPC connection failed".
type scriptedTransport struct {
	calls atomic.Int32
	failN int
	base  http.RoundTripper
}

func (t *scriptedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if int(t.calls.Add(1)) <= t.failN {
		return nil, errors.New("dial tcp: connection refused")
	}
	return t.base.RoundTrip(req)
}

func TestPoolCallRetriesTransientFailureBeforeGivingUp(t *testing.T) {
	shrinkRetryDelays(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonRPCSuccess(w)
	}))
	defer srv.Close()
	transport := &scriptedTransport{failN: 2, base: srv.Client().Transport}
	pool := NewPoolWithHTTPClient(srv.URL, &http.Client{Transport: transport})
	if _, err := pool.Call(context.Background(), "eth_blockNumber", nil); err != nil {
		t.Fatalf("expected the third attempt to succeed, got %v", err)
	}
	if got := transport.calls.Load(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

func TestPoolCallStopsAfterMaxRounds(t *testing.T) {
	shrinkRetryDelays(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	transport := &scriptedTransport{failN: 99, base: srv.Client().Transport}
	pool := NewPoolWithHTTPClient(srv.URL, &http.Client{Transport: transport})
	_, err := pool.Call(context.Background(), "eth_blockNumber", nil)
	if err == nil || !strings.Contains(err.Error(), "all rpc endpoints failed") {
		t.Fatalf("expected all-endpoints-failed error, got %v", err)
	}
	if got := transport.calls.Load(); got != maxCallRounds {
		t.Fatalf("expected %d attempts, got %d", maxCallRounds, got)
	}
}

func TestPoolCallSkipsCoolingEndpoint(t *testing.T) {
	shrinkRetryDelays(t)
	var badHits atomic.Int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		badHits.Add(1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonRPCSuccess(w)
	}))
	defer good.Close()

	pool := NewPoolWithHTTPClient(bad.URL+","+good.URL, bad.Client())
	if _, err := pool.Call(context.Background(), "eth_blockNumber", nil); err != nil {
		t.Fatalf("first call should fail over to the healthy endpoint: %v", err)
	}
	if got := badHits.Load(); got != 1 {
		t.Fatalf("expected one attempt on the failing endpoint, got %d", got)
	}
	if _, err := pool.Call(context.Background(), "eth_blockNumber", nil); err != nil {
		t.Fatalf("second call should succeed: %v", err)
	}
	if got := badHits.Load(); got != 1 {
		t.Fatalf("cooling endpoint must be skipped, hits=%d", got)
	}
}

func TestPoolCallDoesNotRetryAuthFailure(t *testing.T) {
	shrinkRetryDelays(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	pool := NewPoolWithHTTPClient(srv.URL, srv.Client())
	_, err := pool.Call(context.Background(), "eth_blockNumber", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if CategoryOf(err) != CategoryAuthConfig {
		t.Fatalf("expected auth-config category, got %s", CategoryOf(err))
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("auth failures must not be retried, hits=%d", got)
	}
}

func TestEndpointCooldownByCategory(t *testing.T) {
	if got := endpointCooldown(&Failure{Kind: "http", HTTPStatus: 401}); got != authEndpointCooldown {
		t.Fatalf("auth cooldown = %v", got)
	}
	if got := endpointCooldown(&Failure{Kind: "http", HTTPStatus: 503}); got != transientEndpointCooldown {
		t.Fatalf("transport cooldown = %v", got)
	}
	if got := endpointCooldown(&Failure{Kind: "remote", Reason: ReasonAlreadyKnown}); got != 0 {
		t.Fatalf("request-specific rejection must not cool the node down, got %v", got)
	}
}
