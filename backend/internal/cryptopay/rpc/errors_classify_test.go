package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFailureCategoriesDriveRetryDecisions(t *testing.T) {
	cases := []struct {
		name      string
		failure   *Failure
		category  string
		transient bool
	}{
		{"deadline", NewTransportFailure(context.DeadlineExceeded), CategoryTimeout, true},
		{"dial", NewTransportFailure(errors.New("dial tcp: refused")), CategoryTransport, true},
		{"canceled", NewTransportFailure(context.Canceled), CategoryTransport, false},
		{"nonce too low", &Failure{Kind: "remote", RPCCode: -32000, Reason: "nonce too low"}, CategoryRejectedKnown, false},
		{"already known", &Failure{Kind: "remote", Reason: ReasonAlreadyKnown}, CategoryRejectedKnown, false},
		{"insufficient", &Failure{Kind: "remote", Reason: "insufficient funds for gas"}, CategoryRejectedKnown, false},
		{"invalid key", &Failure{Kind: "remote", Message: "invalid api key supplied"}, CategoryAuthConfig, false},
		{"unauthorized http", &Failure{Kind: "http", HTTPStatus: 401}, CategoryAuthConfig, false},
		{"rate limited", &Failure{Kind: "http", HTTPStatus: 429}, CategoryTransport, true},
		{"gateway", &Failure{Kind: "http", HTTPStatus: 502}, CategoryTransport, true},
		{"bad request", &Failure{Kind: "http", HTTPStatus: 400}, CategoryUnknown, false},
		{"unknown node text", &Failure{Kind: "remote", Message: "something new"}, CategoryUnknown, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.failure.Category(); got != tc.category {
				t.Fatalf("category = %q, want %q", got, tc.category)
			}
			if got := IsTransient(tc.failure); got != tc.transient {
				t.Fatalf("IsTransient = %v, want %v", got, tc.transient)
			}
			if got := CategoryOf(errors.Join(errors.New("wrapped"), tc.failure)); got != tc.category {
				t.Fatalf("wrapped category = %q, want %q", got, tc.category)
			}
		})
	}
	if IsTransient(nil) || IsTransient(errors.New("plain")) || IsRejectedKnown(nil) {
		t.Fatal("plain errors must not be classified as RPC failures")
	}
}

func TestSanitizedNodeTextKeepsShapeButNotSecrets(t *testing.T) {
	const secret = "0x" + "AbCdEf0123456789AbCdEf0123456789AbCdEf01"
	message := "execution reverted: https://rpc.example.test/v1/private?api_key=fixture-key " +
		"address " + secret + " password=hunter2"
	got := SanitizeNodeMessage(message)
	for _, leaked := range []string{"https://", "fixture-key", "AbCdEf", "hunter2", "rpc.example.test"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("sanitiser leaked %q in %q", leaked, got)
		}
	}
	if !strings.Contains(got, "execution reverted") {
		t.Fatalf("sanitiser dropped the reason: %q", got)
	}
	long := strings.Repeat("x", 400)
	if out := SanitizeNodeMessage(long); len(out) > 161 || !strings.HasSuffix(out, "…") {
		t.Fatalf("long text was not bounded: %d bytes", len(out))
	}
	if SanitizeNodeMessage("  ") != "" {
		t.Fatal("blank text must stay blank")
	}
}

// Error() is the durable-record boundary and must never grow node text; only
// Detail() carries the sanitised sentence for logs.
func TestNodeTextStaysOutOfErrorAndInDetail(t *testing.T) {
	failure := &Failure{Kind: "remote", RPCCode: -32000,
		Message: "invalid api key for https://node.example.test/key?token=secret"}
	if strings.Contains(failure.Error(), "node.example.test") || strings.Contains(failure.Error(), "secret") {
		t.Fatalf("Error leaked node text: %q", failure.Error())
	}
	detail := failure.Detail()
	if !strings.Contains(detail, "invalid api key") || strings.Contains(detail, "secret") || strings.Contains(detail, "https://") {
		t.Fatalf("Detail = %q", detail)
	}
	if DetailOf(errors.Join(errors.New("x"), failure)) != detail {
		t.Fatal("DetailOf did not unwrap the failure")
	}
	if DetailOf(errors.New("plain")) != "" {
		t.Fatal("a plain error has no node detail")
	}
}

func TestTransactionByHashDistinguishesAbsenceFromFailure(t *testing.T) {
	var mode string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "null":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": nil})
		case "present":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1,
				"result": map[string]any{"hash": "0xabc"}})
		case "mismatch":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1,
				"result": map[string]any{"hash": "0xdifferent"}})
		default:
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	defer s.Close()
	pool := NewPoolWithHTTPClient(s.URL, s.Client())

	mode = "null"
	if present, err := pool.TransactionByHash(t.Context(), "0xabc"); err != nil || present {
		t.Fatalf("null must be an error-free absence: present=%v err=%v", present, err)
	}
	mode = "present"
	if present, err := pool.TransactionByHash(t.Context(), "0xabc"); err != nil || !present {
		t.Fatalf("a returned transaction must be present: present=%v err=%v", present, err)
	}
	mode = "mismatch"
	if _, err := pool.TransactionByHash(t.Context(), "0xabc"); err == nil {
		t.Fatal("a different hash must not count as the reserved transaction")
	}
	mode = "failure"
	_, err := pool.TransactionByHash(t.Context(), "0xabc")
	if err == nil {
		t.Fatal("an HTTP failure must stay an error, never an absence")
	}
	var failure *Failure
	if !errors.As(err, &failure) || !failure.IsTransient() {
		t.Fatalf("a 502 from a node must be a transient failure, got %v", err)
	}
	if failure.Host == "" {
		t.Fatal("a failed call must name the endpoint host for the logs")
	}
}
