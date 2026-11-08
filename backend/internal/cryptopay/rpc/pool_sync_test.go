package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// laggingNode answers getLogs like a node that has only the blocks it knows:
// the observed head is the only clue that its (possibly empty) answer is short.
type laggingNode struct {
	server    *httptest.Server
	head      uint64
	headCalls atomic.Int64
	logCalls  atomic.Int64
}

func newLaggingNode(t *testing.T, head uint64) *laggingNode {
	t.Helper()
	node := &laggingNode{head: head}
	node.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		w.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case "eth_blockNumber":
			node.headCalls.Add(1)
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":"0x%x"}`, node.head)
		case "eth_getLogs":
			node.logCalls.Add(1)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":[]}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(node.server.Close)
	return node
}

func TestCallEndpointNamesTheNodeThatAnswered(t *testing.T) {
	first := newLaggingNode(t, 0x10)
	second := newLaggingNode(t, 0x2a)
	pool := NewPoolWithHTTPClient(first.server.URL+","+second.server.URL, first.server.Client())

	result, endpoint, err := pool.CallEndpoint(context.Background(), "eth_blockNumber", nil)
	if err != nil {
		t.Fatalf("CallEndpoint failed: %v", err)
	}
	if string(result) != `"0x10"` || endpoint != first.server.URL {
		t.Fatalf("result=%s endpoint=%q, want the first node and its head", result, endpoint)
	}
}

func TestRequireSyncedToRejectsAndCoolsDownALaggingNode(t *testing.T) {
	lagging := newLaggingNode(t, 100)
	synced := newLaggingNode(t, 0x2a)
	pool := NewPoolWithHTTPClient(lagging.server.URL+","+synced.server.URL, lagging.server.Client())

	head, err := pool.RequireSyncedTo(context.Background(), lagging.server.URL, 200)
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != "stale" || head != 100 {
		t.Fatalf("behind node was not rejected as stale: head=%d err=%v", head, err)
	}
	if failure.Category() != CategoryStale {
		t.Fatalf("stale failure category = %q", failure.Category())
	}
	if endpointCooldown(failure) <= 0 {
		t.Fatal("a stale node must cool down so the rotation stops using it")
	}
	// The next call must skip the node that just proved it is behind.
	result, endpoint, err := pool.CallEndpoint(context.Background(), "eth_blockNumber", nil)
	if err != nil || endpoint != synced.server.URL || string(result) != `"0x2a"` {
		t.Fatalf("rotation did not move to the caught-up node: endpoint=%q result=%s err=%v", endpoint, result, err)
	}
	if calls := lagging.headCalls.Load(); calls != 1 {
		t.Fatalf("lagging node was queried %d times, want once while cooling down", calls)
	}
}

func TestRequireSyncedToAcceptsANodeAtTheRequestedHeight(t *testing.T) {
	node := newLaggingNode(t, 200)
	pool := NewPoolWithHTTPClient(node.server.URL, node.server.Client())
	head, err := pool.RequireSyncedTo(context.Background(), node.server.URL, 200)
	if err != nil || head != 200 {
		t.Fatalf("node at the requested height was rejected: head=%d err=%v", head, err)
	}
}

func TestEndpointHeadRejectsAnUnparsableAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"not-a-height"}`))
	}))
	t.Cleanup(server.Close)
	pool := NewPoolWithHTTPClient(server.URL, server.Client())
	if _, err := pool.EndpointHead(context.Background(), server.URL); err == nil {
		t.Fatal("an invalid head must not be accepted")
	}
}
