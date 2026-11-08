package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func nodeAnswering(t *testing.T, code int, message string) *Pool {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1,
			"error": map[string]any{"code": code, "message": message}})
	}))
	t.Cleanup(s.Close)
	return NewPoolWithHTTPClient(s.URL, s.Client())
}

func TestNodeRejectionIsReducedToOneOfOurOwnPhrases(t *testing.T) {
	const secret = "fixture-private-key-0xAbCdEf0123456789AbCdEf0123456789AbCdEf01"
	cases := []struct{ node, want string }{
		{"nonce too low: address " + secret + ", tx: 7 state: 9", "RPC node rejected request (code -32000): nonce too low"},
		{"replacement transaction underpriced", "RPC node rejected request (code -32000): replacement transaction underpriced"},
		{"transaction underpriced: " + secret, "RPC node rejected request (code -32000): transaction underpriced"},
		{"ALREADY KNOWN", "RPC node rejected request (code -32000): transaction already known"},
		{"insufficient funds for gas * price + value: address " + secret, "RPC node rejected request (code -32000): insufficient funds for gas"},
		{"execution reverted: " + secret, "RPC node rejected request (code -32000): execution reverted"},
		{"query exceeds max block range 5000", "RPC node rejected request (code -32000): query range or result limit exceeded"},
		{"something the table has never seen " + secret, "RPC node rejected request (code -32000)"},
		{"", "RPC node rejected request (code -32000)"},
	}
	for _, tc := range cases {
		t.Run(tc.node, func(t *testing.T) {
			_, err := nodeAnswering(t, -32000, tc.node).Call(t.Context(), "eth_sendRawTransaction", []any{"0x00"})
			if err == nil {
				t.Fatal("a node error must fail the call")
			}
			if got := SafeError(err); got != tc.want {
				t.Fatalf("SafeError = %q, want %q", got, tc.want)
			}
			if strings.Contains(err.Error(), "fixture-") || strings.Contains(SafeError(err), "0xAbCd") {
				t.Fatal("node text reached the error")
			}
			// What a sweep record stores must survive the next round of sanitising.
			if again := SafeRecordedError(SafeError(err)); again != tc.want {
				t.Fatalf("stored text changed on re-sanitising: %q", again)
			}
		})
	}
}

func TestOnlyAnAlreadyKnownRejectionCountsAsAlreadyKnown(t *testing.T) {
	_, err := nodeAnswering(t, -32000, "already known").Call(t.Context(), "eth_sendRawTransaction", []any{"0x00"})
	if !IsAlreadyKnown(err) || !IsAlreadyKnown(fmt.Errorf("wrapped: %w", err)) {
		t.Fatal("an already-known answer was not recognised")
	}
	for _, other := range []string{"nonce too low", "transaction underpriced", "execution reverted", ""} {
		_, err := nodeAnswering(t, -32000, other).Call(t.Context(), "eth_sendRawTransaction", []any{"0x00"})
		if IsAlreadyKnown(err) {
			t.Fatalf("%q treated as already known", other)
		}
	}
	if IsAlreadyKnown(nil) || IsAlreadyKnown(errors.New("already known")) || IsAlreadyKnown(&Failure{Kind: "http", HTTPStatus: 500}) {
		t.Fatal("only a remote failure carrying the reason qualifies")
	}
}

func TestRecordedErrorsKeepTheirReasonAndNothingElse(t *testing.T) {
	kept := []string{
		"current credited balance is below the per-address sweep minimum",
		"current credited sweep balance or address assignment is invalid",
		"live balance 9000000 is below planned amount 10000000",
		// Gas funding waits written by the sweeper. They name an operator
		// action, so the sweep record must keep the sentence instead of
		// replacing it with the generic payment failure.
		"insufficient native gas budget to fund this sweep; it will be retried when the gas budget allows",
		"gas wallet native balance cannot cover another top-up; fund the gas wallet and the sweep will resume",
		"sweep gas budget is not configured for bsc",
		`chain "bsc" is outside the requested sweep scope`,
		"RPC node rejected request (code -32000): nonce too low",
		"signed transaction fee does not match request",
		"current credited balance is below the per-address sweep minimum (gave up after 25 attempts)",
		"RPC node rejected request (code -32000): replacement transaction underpriced (gave up after 3 attempts)",
		"Payment operation failed; reconcile transaction status before retrying (gave up after 25 attempts)",
	}
	for _, text := range kept {
		if got := SafeRecordedError(text); got != text {
			t.Errorf("lost a fixed message: %q -> %q", text, got)
		}
		if got := SafeRecordedError(SafeRecordedError(text)); got != text {
			t.Errorf("not stable when applied twice: %q -> %q", text, got)
		}
	}
	const generic = "Payment operation failed; reconcile transaction status before retrying"
	hidden := map[string]string{
		"dial tcp 10.0.0.5:443: refused fixture-secret (gave up after 25 attempts)": generic + " (gave up after 25 attempts)",
		"https://rpc.example/fixture-key-path (gave up after 7 attempts)":           generic + " (gave up after 7 attempts)",
		"live balance 9000000 is below planned amount 10000000 fixture-secret":      generic,
		"RPC node rejected request (code -32000): fixture-secret":                   generic,
		"nonce too low (gave up after 25 attempts) fixture-secret":                  generic,
		" (gave up after 25 attempts)":                                              generic,
	}
	for text, want := range hidden {
		got := SafeRecordedError(text)
		if strings.Contains(got, "fixture-") || strings.Contains(got, "10.0.0.5") {
			t.Errorf("leaked: %q -> %q", text, got)
		}
		if got != want {
			t.Errorf("%q -> %q, want %q", text, got, want)
		}
	}
}

func TestConfirmedNonceAsksForMinedTransactionsOnly(t *testing.T) {
	var mu sync.Mutex
	var tags []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var tag string
		if len(req.Params) == 2 {
			_ = json.Unmarshal(req.Params[1], &tag)
		}
		mu.Lock()
		tags = append(tags, req.Method+":"+tag)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": "0x30"})
	}))
	defer s.Close()
	pool := NewPoolWithHTTPClient(s.URL, s.Client())
	if n, err := pool.ConfirmedNonce(t.Context(), "fixture"); err != nil || n != 48 {
		t.Fatalf("confirmed nonce = %d, %v", n, err)
	}
	if n, err := pool.Nonce(t.Context(), "fixture"); err != nil || n != 48 {
		t.Fatalf("pending nonce = %d, %v", n, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(tags) != 2 || tags[0] != "eth_getTransactionCount:latest" || tags[1] != "eth_getTransactionCount:pending" {
		t.Fatalf("block tags asked for: %v", tags)
	}
}
