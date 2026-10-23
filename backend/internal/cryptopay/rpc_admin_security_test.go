package cryptopay

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

func TestAdminRPCProbeDoesNotReturnUpstreamSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": -32005, "message": "fixture-upstream-private-message"}})
	}))
	defer server.Close()
	_, err := rpcCallStringWithClient(t.Context(), server.Client(), server.URL+"/fixture-private-path?key=fixture-private-query", "eth_chainId")
	if err == nil || strings.Contains(err.Error(), "fixture-") || strings.Contains(err.Error(), server.URL) {
		t.Fatal("admin probe leaked URL or upstream error details")
	}
	var failure *rpc.Failure
	if !errors.As(err, &failure) || failure.RPCCode != -32005 {
		t.Fatal("admin probe did not preserve numeric protocol failure")
	}
	// A rejected endpoint is not echoed back, even before any network call.
	result := ProbeRPCEndpoint(t.Context(), "bsc", "http://127.0.0.1/fixture-private-path?key=fixture-private-query")
	encoded, marshalErr := json.Marshal(result)
	if marshalErr != nil || strings.Contains(string(encoded), "fixture-") {
		t.Fatal("rejected probe response echoed endpoint credentials")
	}
}
