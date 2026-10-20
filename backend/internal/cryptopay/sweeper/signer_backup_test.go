package sweeper

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type signerBackupRoundTrip func(*http.Request) (*http.Response, error)

func (roundTrip signerBackupRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) {
	return roundTrip(req)
}

func TestSignerClientExportsEncryptedVaultBackup(t *testing.T) {
	const ciphertext = "opaque-existing-encrypted-vault-blob"
	client := NewSignerClient("unused-test-socket")
	client.http.Transport = signerBackupRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/backup/vault" || req.Method != http.MethodPost || req.Header.Get("Cache-Control") == "" {
			t.Fatalf("unexpected backup request: %s %s", req.Method, req.URL)
		}
		var body map[string]string
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["passphrase"] != "separate test vault password" {
			t.Fatal("backup request omitted its vault password")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"format":"sub2api-signer-vault","version":1,"vault":"` + ciphertext + `"}`)),
			Request:    req,
		}, nil
	})
	backup, err := client.ExportVaultBackup(context.Background(), "separate test vault password")
	if err != nil {
		t.Fatal(err)
	}
	if backup.Format != signerVaultBackupFormat || backup.Version != 1 || backup.Vault != ciphertext {
		t.Fatal("client did not preserve the encrypted backup envelope")
	}
	encoded, _ := json.Marshal(backup)
	if strings.Contains(string(encoded), "abandon") || strings.Contains(string(encoded), "legal winner") {
		t.Fatal("client exported plaintext mnemonic fields")
	}
}

func TestSignerClientImportsHotMnemonicAndRequiresRelockedConfirmation(t *testing.T) {
	for _, body := range []string{
		`{"updated":true,"unlocked":false}`,
		`{"updated":true,"unlocked":true}`,
		`{"updated":false,"unlocked":false}`,
	} {
		client := NewSignerClient("unused-test-socket")
		client.http.Transport = signerBackupRoundTrip(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/vault/hot-mnemonic" || req.Method != http.MethodPost {
				t.Fatalf("unexpected import endpoint: %s %s", req.Method, req.URL)
			}
			var input map[string]string
			if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
				t.Fatal(err)
			}
			if input["passphrase"] == "" || input["hot_mnemonic"] == "" {
				t.Fatal("import request omitted required credentials")
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
		})
		err := client.ImportHotMnemonic(context.Background(), "vault password", "twelve test words")
		if (body == `{"updated":true,"unlocked":false}`) != (err == nil) {
			t.Fatalf("import body %s produced error %v", body, err)
		}
	}
}

func TestSignerClientDoesNotLeakErrorResponseBodiesOrFollowRedirects(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusServiceUnavailable, http.StatusTemporaryRedirect} {
		client := NewSignerClient("unused-test-socket")
		requests := 0
		client.http.Transport = signerBackupRoundTrip(func(req *http.Request) (*http.Response, error) {
			requests++
			return &http.Response{
				StatusCode: status,
				Header:     http.Header{"Location": []string{"http://attacker.invalid/collect"}},
				Body:       io.NopCloser(strings.NewReader("secret sentinel body")),
				Request:    req,
			}, nil
		})
		_, err := client.ExportVaultBackup(context.Background(), "vault test password")
		if err == nil || strings.Contains(err.Error(), "secret sentinel") {
			t.Fatal("signer error response was exposed to caller/logs")
		}
		if requests != 1 {
			t.Fatal("credential-bearing unix request followed a redirect")
		}
	}
}

func TestSignerClientValidatesBackupEnvelopeAndPassphrase(t *testing.T) {
	for _, body := range []string{
		`{"format":"different","version":1,"vault":"opaque"}`,
		`{"format":"sub2api-signer-vault","version":2,"vault":"opaque"}`,
		`{"format":"sub2api-signer-vault","version":1,"vault":""}`,
		`{"format":"sub2api-signer-vault","version":1,"vault":"opaque"}{}`,
	} {
		client := NewSignerClient("unused-test-socket")
		client.http.Transport = signerBackupRoundTrip(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
		})
		if _, err := client.ExportVaultBackup(context.Background(), "valid password"); err == nil {
			t.Fatal("invalid signer backup envelope accepted")
		}
	}
	client := NewSignerClient("unused-test-socket")
	client.http.Transport = signerBackupRoundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid passphrase reached signer")
		return nil, nil
	})
	if _, err := client.ExportVaultBackup(context.Background(), strings.Repeat("x", 1025)); err == nil {
		t.Fatal("oversized passphrase accepted")
	}
	if err := client.ImportHotMnemonic(context.Background(), "password", ""); err == nil {
		t.Fatal("empty mnemonic accepted")
	}
}
