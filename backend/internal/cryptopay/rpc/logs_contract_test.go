package rpc

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGetLogsRejectsInvalidResultsAndRotatesEndpoints(t *testing.T) {
	for _, fixture := range []struct {
		name, response string
	}{
		{"null", `{"jsonrpc":"2.0","id":1,"result":null}`},
		{"missing", `{"jsonrpc":"2.0","id":1}`},
		{"object", `{"jsonrpc":"2.0","id":1,"result":{}}`},
		{"string", `{"jsonrpc":"2.0","id":1,"result":"[]"}`},
		{"number", `{"jsonrpc":"2.0","id":1,"result":0}`},
		{"boolean", `{"jsonrpc":"2.0","id":1,"result":false}`},
		{"null_member", `{"jsonrpc":"2.0","id":1,"result":[null]}`},
		{"empty_object_member", `{"jsonrpc":"2.0","id":1,"result":[{}]}`},
		{"non_object_member", `{"jsonrpc":"2.0","id":1,"result":["fixture-invalid-log"]}`},
		{"malformed_json", `{"jsonrpc":"2.0","id":1,"result":[`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var invalidCalls, healthyCalls atomic.Int64
			invalid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				invalidCalls.Add(1)
				_, _ = w.Write([]byte(fixture.response))
			}))
			defer invalid.Close()
			healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				healthyCalls.Add(1)
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":[]}`))
			}))
			defer healthy.Close()

			_, err := NewPoolWithHTTPClient(invalid.URL, invalid.Client()).Call(t.Context(), "eth_getLogs", []any{})
			var failure *Failure
			if err == nil || !errors.As(err, &failure) || failure.Kind != "response" {
				t.Fatal("invalid logs response was accepted or lost its safe classification", err)
			}
			if strings.Contains(err.Error(), invalid.URL) || strings.Contains(err.Error(), "fixture-invalid-log") {
				t.Fatal("invalid logs response leaked endpoint or upstream-controlled data")
			}

			result, err := NewPoolWithHTTPClient(invalid.URL+","+healthy.URL, healthy.Client()).Call(t.Context(), "eth_getLogs", []any{})
			if err != nil || !bytes.Equal(result, []byte("[]")) {
				t.Fatal("bad logs result prevented failover to a valid empty result", err)
			}
			if invalidCalls.Load() != 2 || healthyCalls.Load() != 1 {
				t.Fatal("unexpected endpoint selection", invalidCalls.Load(), healthyCalls.Load())
			}
		})
	}
}

func TestGetLogsEmptyArrayAndPendingReceiptNullKeepDifferentContracts(t *testing.T) {
	for _, fixture := range []struct {
		name, method, result string
	}{
		{"empty_logs", "eth_getLogs", `[]`},
		{"pending_receipt", "eth_getTransactionReceipt", `null`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":` + fixture.result + `}`))
			}))
			defer server.Close()
			result, err := NewPoolWithHTTPClient(server.URL, server.Client()).Call(t.Context(), fixture.method, []any{})
			if err != nil || string(result) != fixture.result {
				t.Fatal("legitimate method result was rejected", err)
			}
		})
	}
}
