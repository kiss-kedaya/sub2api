package cryptopay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

func TestPaymentRPCProbeRequiresLogsAndBSCFinalized(t *testing.T) {
	for _, scenario := range []string{"ready", "moving finalized head", "logs rejected", "null logs", "finalized rejected", "safe only", "balance rejected", "receipt rejected", "invalid receipt"} {
		t.Run(scenario, func(t *testing.T) {
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Method string            `json:"method"`
					Params []json.RawMessage `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				calls = append(calls, req.Method)
				var result any = []any{}
				switch req.Method {
				case "eth_getLogs":
					if scenario == "logs rejected" {
						_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": -32005, "message": "fixture-private-message"}})
						return
					}
					if scenario == "null logs" {
						result = nil
					}
					var filter map[string]json.RawMessage
					if len(req.Params) != 1 || json.Unmarshal(req.Params[0], &filter) != nil || string(filter["fromBlock"]) != `"0x64"` || string(filter["toBlock"]) != `"0x64"` || !strings.Contains(string(filter["topics"]), "null") {
						t.Error("probe did not use a bounded recipient-filtered scan")
					}
				case "eth_getBlockByNumber":
					if string(req.Params[0]) != `"finalized"` {
						t.Error("BSC probe used a weaker block tag")
					}
					if scenario == "finalized rejected" {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					result = map[string]any{"number": "0x62", "hash": "0x" + strings.Repeat("a", 64)}
					if scenario == "moving finalized head" {
						result = map[string]any{"number": "0x65", "hash": "0x" + strings.Repeat("a", 64)}
					}
					if scenario == "safe only" {
						result = nil
					}
				case "eth_blockNumber":
					result = "0x66"
				case "eth_getBalance":
					if scenario == "balance rejected" {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					result = "0x0"
				case "eth_getTransactionReceipt":
					if scenario == "receipt rejected" {
						_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": -32601, "message": "fixture-private-message"}})
						return
					}
					result = nil
					if scenario == "invalid receipt" {
						result = "not a receipt"
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
			}))
			defer server.Close()
			pool := rpc.NewPoolWithHTTPClient(server.URL, server.Client())
			err := probeDepositCapabilities(t.Context(), pool, ChainBSC, 100)
			if (err == nil) != (scenario == "ready" || scenario == "moving finalized head") {
				t.Fatalf("err=%v", err)
			}
			if err != nil && strings.Contains(err.Error(), "fixture-private-message") {
				t.Fatal("node error text leaked")
			}
			if scenario == "ready" && len(calls) != 4 {
				t.Fatalf("missing payment capability call: %v", calls)
			}
		})
	}
}
