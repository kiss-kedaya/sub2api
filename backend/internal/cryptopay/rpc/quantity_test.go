package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRPCRejectsMalformedQuantitiesBeforeUse(t *testing.T) {
	for _, input := range []string{"0x1junk", "0x-1", "0x+1", "1", "0x01", "0x", "0x10000000000000000000000000000000000000000000000000000000000000000"} {
		t.Run(input, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": input})
			}))
			defer s.Close()
			pool := NewPoolWithHTTPClient(s.URL, s.Client())
			for name, call := range map[string]func() error{
				"head":           func() error { _, err := pool.BlockNumber(t.Context()); return err },
				"nonce":          func() error { _, err := pool.Nonce(t.Context(), "fixture"); return err },
				"native balance": func() error { _, err := pool.Balance(t.Context(), "fixture"); return err },
				"gas price":      func() error { _, err := pool.GasPrice(t.Context()); return err },
			} {
				t.Run(name, func(t *testing.T) {
					if err := call(); err == nil {
						t.Fatalf("malformed RPC quantity %q accepted for %s", input, name)
					}
				})
			}
		})
	}
}
