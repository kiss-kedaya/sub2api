package scanner

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

func TestVerifiedChainPoolIsolatesStalledAndWrongChainEndpoints(t *testing.T) {
	release := make(chan struct{})
	stalled := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
		case <-release:
		}
	}))
	defer func() { close(release); stalled.Close() }()
	wrong := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": "0x1"})
	}))
	defer wrong.Close()
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": "0x38"})
	}))
	defer healthy.Close()
	configured := strings.Join([]string{stalled.URL, wrong.URL, healthy.URL}, ",")
	pool := rpc.NewPoolWithHTTPClient(configured, healthy.Client())
	started := time.Now()
	verified, err := verifiedChainPool(t.Context(), pool, "bsc")
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("a stalled endpoint delayed the healthy chain pool")
	}
	if got := verified.Endpoints(); len(got) != 1 || got[0] != healthy.URL {
		t.Fatal("unverified endpoint entered payment scanner", len(got))
	}
	if len(pool.Endpoints()) != 3 {
		t.Fatal("verification mutated the operator's configured pool")
	}
}
