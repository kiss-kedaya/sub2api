package rpc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func blockNumberServer(t *testing.T, head string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":%q}`, head)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMaxBlockNumberPicksHighestHealthyEndpoint(t *testing.T) {
	low := blockNumberServer(t, "0x10")
	high := blockNumberServer(t, "0x2a")
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(broken.Close)

	pool := NewPoolWithHTTPClient(low.URL+","+high.URL+","+broken.URL, low.Client())
	head, err := pool.MaxBlockNumber(context.Background())
	if err != nil {
		t.Fatalf("MaxBlockNumber failed: %v", err)
	}
	if head != 42 {
		t.Fatalf("head = %d, want 42 (the highest healthy endpoint)", head)
	}
}

func TestMaxBlockNumberReportsFailureWhenNoEndpointAnswers(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(broken.Close)

	pool := NewPoolWithHTTPClient(broken.URL, broken.Client())
	if _, err := pool.MaxBlockNumber(context.Background()); err == nil {
		t.Fatal("expected an error when every endpoint fails")
	}
}
