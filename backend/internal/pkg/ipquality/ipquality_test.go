package ipquality

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func resetForTest() {
	mu.Lock()
	defer mu.Unlock()
	cache = map[string]cacheEntry{}
}

func TestClassifyDatacenterIPsUsesHostingAndProxy(t *testing.T) {
	resetForTest()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]batchResult{
			{Status: "success", Query: "1.1.1.1", Hosting: true},
			{Status: "success", Query: "2.2.2.2", Proxy: true},
			{Status: "success", Query: "3.3.3.3"},
		})
	}))
	defer server.Close()
	oldURL := lookupURL
	lookupURL = server.URL
	defer func() { lookupURL = oldURL }()

	got := ClassifyDatacenterIPs(context.Background(), []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"})
	if !got["1.1.1.1"] || !got["2.2.2.2"] || got["3.3.3.3"] {
		t.Fatalf("unexpected classification: %#v", got)
	}
}

func TestClassifyDatacenterIPsCacheAndFailOpen(t *testing.T) {
	resetForTest()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode([]batchResult{{Status: "success", Query: "4.4.4.4", Hosting: true}})
	}))
	url := server.URL
	oldURL := lookupURL
	lookupURL = url
	defer func() { lookupURL = oldURL; server.Close() }()

	ctx := context.Background()
	got := ClassifyDatacenterIPs(ctx, []string{"4.4.4.4"})
	if !got["4.4.4.4"] || calls != 1 {
		t.Fatalf("first lookup failed: %#v calls=%d", got, calls)
	}
	got = ClassifyDatacenterIPs(ctx, []string{"4.4.4.4"})
	if !got["4.4.4.4"] || calls != 1 {
		t.Fatalf("expected cache hit, calls=%d", calls)
	}
	// Expire cache and point at a dead endpoint: unknown IPs must be allowed.
	mu.Lock()
	cache["4.4.4.4"] = cacheEntry{datacenter: true, expiresAt: time.Now().Add(-time.Hour)}
	mu.Unlock()
	lookupURL = "http://127.0.0.1:1/batch"
	got = ClassifyDatacenterIPs(ctx, []string{"4.4.4.4"})
	if got["4.4.4.4"] {
		t.Fatalf("expected fail-open on failed lookup")
	}
}
