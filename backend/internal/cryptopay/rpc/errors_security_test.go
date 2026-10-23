package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestRPCFailuresNeverExposeEndpointOrNodeControlledText(t *testing.T) {
	const pathSecret = "fixture-rpc-path-secret"
	const querySecret = "fixture-rpc-query-secret"
	const nodeSecret = "fixture-node-error-secret"
	for _, scenario := range []string{"http", "node error", "invalid JSON", "invalid quantity"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				switch scenario {
				case "http":
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(nodeSecret))
				case "node error":
					_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": -32005, "message": nodeSecret}})
				case "invalid JSON":
					_, _ = w.Write([]byte(nodeSecret))
				case "invalid quantity":
					_ = json.NewEncoder(w).Encode(map[string]any{"result": nodeSecret})
				}
			}))
			defer server.Close()
			pool := NewPoolWithHTTPClient(server.URL+"/"+pathSecret+"?api_key="+querySecret, server.Client())
			_, err := pool.BlockNumber(t.Context())
			if err == nil {
				t.Fatal("expected RPC failure")
			}
			var log bytes.Buffer
			slog.New(slog.NewJSONHandler(&log, nil)).Warn("payment scan failed", "error", err)
			for _, text := range []string{err.Error(), SafeError(err), log.String()} {
				for _, secret := range []string{pathSecret, querySecret, nodeSecret, server.URL} {
					if strings.Contains(text, secret) {
						t.Fatal("RPC failure leaked fixture credential or upstream message")
					}
				}
			}
			var failure *Failure
			if !errors.As(err, &failure) {
				t.Fatal("missing stable failure classification")
			}
			if scenario == "http" && failure.HTTPStatus != http.StatusForbidden {
				t.Fatal("upstream HTTP status was not preserved")
			}
			if scenario == "node error" && failure.RPCCode != -32005 {
				t.Fatal("upstream numeric RPC code was not preserved")
			}
		})
	}
}

type failureRoundTripper func(*http.Request) (*http.Response, error)

func (f failureRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRPCTransportFailuresKeepOnlySafeContextSentinels(t *testing.T) {
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			client := &http.Client{Transport: failureRoundTripper(func(*http.Request) (*http.Response, error) {
				return nil, &url.Error{Op: "POST", URL: "https://example.test/fixture-private-path?api_key=fixture-private-key", Err: fmt.Errorf("fixture-upstream-secret: %w", sentinel)}
			})}
			_, err := NewPoolWithHTTPClient("https://example.test/fixture-private-path?api_key=fixture-private-key", client).BlockNumber(t.Context())
			if !errors.Is(err, sentinel) {
				t.Fatal("context cancellation/timeout semantics changed")
			}
			var unsafe *url.Error
			if errors.As(err, &unsafe) || strings.Contains(err.Error(), "fixture-") {
				t.Fatal("unsafe transport details remain in returned error chain")
			}
		})
	}
}

func TestHistoricalErrorTextOnlyRetainsTrustedMessages(t *testing.T) {
	for _, text := range []string{
		"rpc https://example.test/fixture-private-path?key=fixture-private-key: http 403",
		"RPC response is invalid fixture-upstream-secret",
		"RPC node rejected request (code 403) fixture-upstream-secret",
	} {
		got := SafeRecordedError(text)
		if strings.Contains(got, "fixture-") || strings.Contains(got, "https://") {
			t.Fatal("legacy record leaked endpoint credentials")
		}
	}
	for _, text := range []string{"", "RPC HTTP request failed (status 403)", "RPC node rejected request (code -32005)", "awaiting canonical receipt and required confirmations"} {
		if SafeRecordedError(text) != text {
			t.Fatal("stable status/category was lost")
		}
	}
}
