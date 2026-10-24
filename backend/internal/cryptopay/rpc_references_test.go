package cryptopay

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"golang.org/x/net/publicsuffix"
)

func TestAutomaticRPCReferencesHaveIndependentOperatorDomains(t *testing.T) {
	for chain, endpoints := range automaticRPCReferences {
		t.Run(chain, func(t *testing.T) {
			if chainlistIDs[chain] == 0 {
				t.Fatal("reference operators are configured for an unsupported chain")
			}
			groups := make(map[string]bool)
			for _, endpoint := range endpoints {
				parsed, err := url.Parse(endpoint)
				if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
					t.Fatal("automatic reference contains insecure transport or installation credentials")
				}
				group, err := publicsuffix.EffectiveTLDPlusOne(parsed.Hostname())
				if err != nil || groups[group] {
					t.Fatal("reference aliases incorrectly count as independent operators")
				}
				groups[group] = true
			}
			if len(groups) < 2 {
				t.Fatal("automatic chain has fewer than two independent reference operators")
			}
		})
	}
}

type provenanceTransport struct {
	manualCalls    atomic.Int64
	referenceCalls atomic.Int64
}

func (f *provenanceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var request struct {
		Method string `json:"method"`
	}
	if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
		return nil, err
	}
	// Every host, including the built-in reference names, is intercepted.
	// This transport never delegates to the network or resolves those names.
	if req.URL.Hostname() == "operator.provenance.example" {
		f.manualCalls.Add(1)
	} else if req.URL.Hostname() != "registry.provenance.example" {
		f.referenceCalls.Add(1)
	}
	var result any
	switch request.Method {
	case "eth_chainId":
		result = "0x38"
	case "eth_getLogs":
		result = []any{}
	default:
		return nil, errors.New("unexpected synthetic provenance RPC method")
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "result": result})
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(string(body))), Request: req}, nil
}

func TestRPCConfigReloadKeepsDiscoveryProvenanceAndManualTrustAligned(t *testing.T) {
	previous := automaticRPC
	previousReferences := automaticRPCReferences[ChainBSC]
	automaticRPC = newRPCDiscovery()
	// Two fixture operators avoid leaving an unnecessary third reference call
	// in flight while this test switches provenance back to manual mode.
	automaticRPCReferences[ChainBSC] = []string{"https://one.provenance-anchor.example/rpc", "https://two.other-anchor.example/rpc"}
	t.Cleanup(func() {
		automaticRPC = previous
		automaticRPCReferences[ChainBSC] = previousReferences
	})
	const manual = "https://operator.provenance.example/rpc"
	const discovered = "https://registry.provenance.example/rpc"
	automaticRPC.entries[ChainBSC] = discoveredRPC{endpoints: []string{discovered}, refreshAt: time.Now().Add(time.Hour)}
	transport := &provenanceTransport{}
	shared := rpc.NewPoolWithHTTPClient(manual, &http.Client{Transport: transport})
	cfg := CryptoConfig{Chains: []string{ChainBSC}, RPC: map[string]string{ChainBSC: manual}}
	pools := poolsForConfig(cfg, map[string]*rpc.Pool{ChainBSC: shared})
	if pools[ChainBSC] != shared || shared.RequiresIndependentVerification() || !slices.Equal(shared.Endpoints(), []string{manual}) {
		t.Fatal("reload replaced the shared manual pool or changed its trust provenance")
	}
	if _, err := shared.Call(t.Context(), "eth_getLogs", []any{}); err != nil || transport.referenceCalls.Load() != 0 {
		t.Fatal("manual RPC made an unrequested public reference call", err)
	}
	cfg.RPC[ChainBSC] = ""
	pools = poolsForConfig(cfg, pools)
	if pools[ChainBSC] != shared || !shared.RequiresIndependentVerification() || !slices.Equal(shared.Endpoints(), []string{discovered}) {
		t.Fatal("automatic reload retained manual trust or discarded shared readers")
	}
	if _, err := shared.Call(t.Context(), "eth_getLogs", []any{}); err != nil || transport.referenceCalls.Load() == 0 {
		t.Fatal("automatic reload did not request independent funds evidence", err)
	}
	cfg.RPC[ChainBSC] = manual
	before := transport.referenceCalls.Load()
	pools = poolsForConfig(cfg, pools)
	if pools[ChainBSC] != shared || shared.RequiresIndependentVerification() {
		t.Fatal("switching back to a manual endpoint retained automatic-only trust policy")
	}
	if _, err := shared.Call(t.Context(), "eth_getLogs", []any{}); err != nil || transport.referenceCalls.Load() != before {
		t.Fatal("manual reload kept querying independent public references", err)
	}
}
