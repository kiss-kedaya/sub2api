package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeRPCDoesNotTreatErrorOrProxyBodyAsHealthy(t *testing.T) {
	for _, body := range []string{
		`{"jsonrpc":"2.0","id":1,"result":null,"error":{"code":-32005,"message":"limit exceeded"}}`,
		`{"jsonrpc":"2.0","id":1,"result":""}`,
		`{"jsonrpc":"2.0","id":1,"result":"0xnot-a-block"}`,
		`{"jsonrpc":"2.0","id":2,"result":"0x123"}`,
		`<html>result temporarily unavailable</html>`,
		`{"jsonrpc":"2.0","id":1,"result":"0x123"} {"error":"proxy failure"}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			if err := probeRPC(t.Context(), server.Client(), server.URL); err == nil {
				t.Fatal("unusable RPC was reported healthy")
			}
		})
	}
}

func TestProbeRPCAcceptsFragmentedBlockResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,`))
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("recorder does not support streaming flush")
			return
		}
		flusher.Flush()
		_, _ = w.Write([]byte(`"result":"0x123"}`))
	}))
	defer server.Close()
	if err := probeRPC(t.Context(), server.Client(), server.URL); err != nil {
		t.Fatal(err)
	}
}
