package sweeper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const signerVaultBackupFormat = "sub2api-signer-vault"

// SignerVaultBackup is the encrypted export envelope. The web process has no
// decryption API and must never receive plaintext mnemonics from this route.
type SignerVaultBackup struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	Vault   string `json:"vault"`
}

// VaultBackup is the service-facing name for the same wire format.
type VaultBackup = SignerVaultBackup

func (c *SignerClient) ExportVaultBackup(ctx context.Context, passphrase string) (*VaultBackup, error) {
	if strings.TrimSpace(passphrase) == "" || len(passphrase) > 1024 {
		return nil, errors.New("vault passphrase required (at most 1024 bytes)")
	}
	payload, err := c.vaultBackupRequest(ctx, "/backup/vault", map[string]string{"passphrase": passphrase})
	if err != nil {
		return nil, err
	}
	var backup VaultBackup
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&backup) != nil || backup.Format != signerVaultBackupFormat || backup.Version != 1 ||
		strings.TrimSpace(backup.Vault) == "" || decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("signer returned an invalid vault backup")
	}
	return &backup, nil
}

func (c *SignerClient) ImportHotMnemonic(ctx context.Context, passphrase, mnemonic string) error {
	if strings.TrimSpace(passphrase) == "" || len(passphrase) > 1024 || strings.TrimSpace(mnemonic) == "" {
		return errors.New("vault passphrase and hot mnemonic required (passphrase at most 1024 bytes)")
	}
	payload, err := c.vaultBackupRequest(ctx, "/vault/hot-mnemonic", map[string]string{
		"passphrase": passphrase, "hot_mnemonic": mnemonic,
	})
	if err != nil {
		return err
	}
	var result struct {
		Updated  bool  `json:"updated"`
		Unlocked *bool `json:"unlocked"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if decoder.Decode(&result) != nil || !result.Updated || result.Unlocked == nil || *result.Unlocked || decoder.Decode(new(any)) != io.EOF {
		return errors.New("signer did not confirm hot mnemonic import and relock")
	}
	return nil
}

func (c *SignerClient) vaultBackupRequest(ctx context.Context, path string, body any) ([]byte, error) {
	if c == nil || c.http == nil {
		return nil, errors.New("signer client unavailable")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("could not encode signer vault request")
	}
	defer clear(raw)
	if len(raw) > 4<<10 {
		return nil, errors.New("signer vault request exceeds 4KB")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://signer"+path, bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("could not create signer vault request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cache-Control", "no-store, no-cache")
	// Never forward a passphrase/mnemonic through a redirect. Copying the
	// client preserves its unix transport, timeout and concurrent callers.
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("signer vault request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// Do not echo the response body: neither driver errors nor ciphertext
		// nor an accidental plaintext response belong in service logs.
		return nil, fmt.Errorf("signer vault request: HTTP %d", resp.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil || len(payload) > 64<<10 {
		return nil, errors.New("invalid signer vault response")
	}
	return payload, nil
}
