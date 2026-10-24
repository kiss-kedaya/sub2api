package scanner

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

type independentFinalityTransport struct {
	manualFailure bool
	headCalls     atomic.Int64
}

func (f *independentFinalityTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var request struct {
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
		return nil, err
	}
	var result any
	switch request.Method {
	case "eth_chainId":
		result = "0x2105"
	case "eth_getBlockByNumber":
		var tag string
		if len(request.Params) == 0 || json.Unmarshal(request.Params[0], &tag) != nil || tag != "safe" {
			return nil, errors.New("unexpected synthetic finality block tag")
		}
		// The registry primary claims a sufficient safe height. One of two
		// independent references is unavailable, so automatic mode must fail.
		if req.URL.Host == "one.finality-reference.example" || f.manualFailure {
			return nil, errors.New("synthetic safe block unavailable")
		}
		result = map[string]any{"number": "0x6e", "hash": "0x" + strings.Repeat("ab", 32)}
	case "eth_blockNumber":
		f.headCalls.Add(1)
		result = "0x6e"
	default:
		return nil, errors.New("unexpected synthetic finality method")
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(string(body))), Request: req}, nil
}

func TestAutomaticSafeVerificationCannotFallBackToConfirmationDepth(t *testing.T) {
	transport := &independentFinalityTransport{}
	pool := rpc.NewPoolWithHTTPClient("https://registry.finality-primary.example/rpc", &http.Client{Transport: transport})
	// These are distinct registrable domains, not aliases of a single operator.
	pool.SetIndependentVerification(8453, []string{"https://one.finality-reference.example/rpc", "https://two.other-reference.example/rpc"})
	final, err := transactionIsFinalForChain(t.Context(), pool, "base", 101, 5)
	if final || !errors.Is(err, rpc.ErrIndependentEvidence) {
		t.Fatal("unconfirmed independent safe evidence was replaced with depth-only approval", final, err)
	}
	if transport.headCalls.Load() != 0 {
		t.Fatal("automatic finality attempted the legacy depth fallback after losing independent safe evidence")
	}
}

func TestExplicitManualRPCPreservesExistingDepthFallback(t *testing.T) {
	transport := &independentFinalityTransport{manualFailure: true}
	pool := rpc.NewPoolWithHTTPClient("https://operator.finality-primary.example/rpc", &http.Client{Transport: transport})
	final, err := transactionIsFinalForChain(t.Context(), pool, "base", 101, 5)
	if err != nil || !final || transport.headCalls.Load() == 0 {
		t.Fatal("explicit operator-managed RPC lost its existing finality compatibility", final, err)
	}
}
