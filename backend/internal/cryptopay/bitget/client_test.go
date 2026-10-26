package bitget

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrepareRequiresVendorSignaturesAndPreservesRawBytes(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	valid := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, e := io.ReadAll(r.Body)
		require.NoError(t, e)
		want := sha256.Sum256([]byte(r.Method + r.URL.RequestURI() + string(raw) + r.Header.Get("X-TIMESTAMP")))
		require.Equal(t, "0x"+hex.EncodeToString(want[:]), r.Header.Get("X-SIGN"))
		body := []byte("{ \"status\": 0, \"data\": {\"orderId\":\"fixture-order\",\"chain\":\"bnb\"} }")
		sign := func(data []byte) string {
			h := sha256.Sum256(data)
			s, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
			require.NoError(t, e)
			return "0x" + hex.EncodeToString(s)
		}
		a, b := sign(body), sign(raw)
		if valid {
			w.Header().Set("security-check", a)
			w.Header().Set("security-request-check", b)
			w.Header().Set("security-double-check", sign([]byte(a+b)))
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()
	c := &Client{http: server.Client(), base: server.URL, key: &key.PublicKey}
	o, err := c.Prepare(t.Context(), Request{Chain: "bnb", NoGas: true})
	require.NoError(t, err)
	encoded, err := json.Marshal(o)
	require.NoError(t, err)
	var restored Order
	require.NoError(t, json.Unmarshal(encoded, &restored))
	require.Equal(t, string(o.Attestation.Response), string(restored.Attestation.Response))
	require.True(t, c.verify(restored.Attestation.ResponseSignature, restored.Attestation.Response))
	valid = false
	_, err = c.Prepare(t.Context(), Request{Chain: "bnb"})
	require.ErrorIs(t, err, ErrUnsafeOrder)
}

func TestClientErrorsNeverExposeResponseOrTransportSecrets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte("private-provider-secret"))
	}))
	defer server.Close()
	c := NewClient()
	c.base = server.URL
	c.http = server.Client()
	_, err := c.Prepare(context.Background(), Request{})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private-provider-secret")
	require.Contains(t, err.Error(), "HTTP 403")
}

func TestTransferMessagesRetainVendorExtensionsAfterJSONRoundTrip(t *testing.T) {
	var m Message
	require.NoError(t, json.Unmarshal([]byte(`{"msgType":"call","hash":"0x00","nonce":"9","chainId":56,"payload":{"extra":true}}`), &m))
	m.Signature = "0xfixture"
	data, err := json.Marshal(m)
	require.NoError(t, err)
	for _, want := range []string{`"nonce":"9"`, `"chainId":56`, `"extra":true`, `"sig":"0xfixture"`} {
		require.Contains(t, string(data), want)
	}
}

func TestRawAmountRejectsHugeScientificAndFractionalInputs(t *testing.T) {
	for _, s := range []string{"1e2147483647", "1e-999999999", "-1", "NaN", "0.0000001", strings.Repeat("9", 90)} {
		_, err := RawAmount(s, 6)
		require.Error(t, err, s)
	}
	v, err := RawAmount("12.345678", 6)
	require.NoError(t, err)
	require.Equal(t, "12345678", v.String())
}
