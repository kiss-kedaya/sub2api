package rpc

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicRPCClientRejectsPrivateAndMixedDNSAnswers(t *testing.T) {
	for _, tc := range []struct {
		name string
		ips  []net.IPAddr
	}{
		{name: "private", ips: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}},
		{name: "mixed", ips: []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}, {IP: net.ParseIP("10.0.0.1")}}},
		{name: "cloud metadata", ips: []net.IPAddr{{IP: net.ParseIP("100.100.100.200")}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dialCalls atomic.Int32
			client := newPublicHTTPClient(time.Second,
				func(context.Context, string) ([]net.IPAddr, error) { return tc.ips, nil },
				func(context.Context, string, string) (net.Conn, error) {
					dialCalls.Add(1)
					return nil, nil
				},
			)
			defer client.CloseIdleConnections()

			_, err := client.Get("http://rpc.example.test:8545/")
			if err == nil || !strings.Contains(err.Error(), "rpc destination is not public") {
				t.Fatalf("request error = %v, want blocked destination", err)
			}
			if got := dialCalls.Load(); got != 0 {
				t.Fatalf("dial calls = %d, want 0", got)
			}
		})
	}
}

func TestPublicRPCClientPinsValidatedAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Host, "rpc.example.test:") {
			t.Errorf("request Host = %q, want rpc.example.test with original port", r.Host)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	localAddr := server.Listener.Addr().String()
	_, port, err := net.SplitHostPort(localAddr)
	if err != nil {
		t.Fatal(err)
	}
	var dialTarget string
	client := newPublicHTTPClient(time.Second,
		func(_ context.Context, host string) ([]net.IPAddr, error) {
			if host != "rpc.example.test" {
				t.Fatalf("lookup host = %q", host)
			}
			return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}, nil
		},
		func(ctx context.Context, network, address string) (net.Conn, error) {
			dialTarget = address
			if !strings.HasPrefix(address, "1.1.1.1:") {
				t.Fatalf("dial target = %q, want validated address", address)
			}
			return (&net.Dialer{}).DialContext(ctx, network, localAddr)
		},
	)
	defer client.CloseIdleConnections()

	resp, err := client.Get("http://rpc.example.test:" + port + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !strings.HasPrefix(dialTarget, "1.1.1.1:") {
		t.Fatalf("dial target = %q, want validated address", dialTarget)
	}
}

func TestPublicRPCClientDoesNotFollowRedirects(t *testing.T) {
	var sourceCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceCalls.Add(1)
		http.Redirect(w, r, "http://internal.example.test:8080/metadata", http.StatusFound)
	}))
	defer server.Close()
	localAddr := server.Listener.Addr().String()
	_, port, err := net.SplitHostPort(localAddr)
	if err != nil {
		t.Fatal(err)
	}
	var lookedUp []string
	client := newPublicHTTPClient(time.Second,
		func(_ context.Context, host string) ([]net.IPAddr, error) {
			lookedUp = append(lookedUp, host)
			return []net.IPAddr{{IP: net.ParseIP("1.1.1.1")}}, nil
		},
		func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, localAddr)
		},
	)
	defer client.CloseIdleConnections()

	resp, err := client.Get("http://rpc.example.test:" + port + "/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 returned without following", resp.StatusCode)
	}
	if got := sourceCalls.Load(); got != 1 {
		t.Fatalf("source calls = %d, want 1", got)
	}
	if len(lookedUp) != 1 || lookedUp[0] != "rpc.example.test" {
		t.Fatalf("DNS lookups = %v, redirect target must not be resolved", lookedUp)
	}
}

func TestPoolRejectsLoopbackRPCBeforeSendingRequest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
	}))
	defer server.Close()

	pool := NewPool(server.URL)
	_, err := pool.BlockNumber(context.Background())
	if err == nil {
		t.Fatal("loopback endpoint unexpectedly succeeded")
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("loopback server calls = %d, want 0", got)
	}
}
