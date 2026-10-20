package sweeper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// MnemonicExport is returned only to the in-memory API caller and is never
// logged or persisted by the gateway.
type MnemonicExport struct {
	DepositMnemonic string `json:"deposit_mnemonic"`
	HotMnemonic     string `json:"hot_mnemonic"`
}

var (
	ErrMnemonicExportRejected = errors.New("signer refused mnemonic export")
	ErrMnemonicExportLimited  = errors.New("signer mnemonic export is rate limited")
)

// ExportMnemonics asks the isolated signer to decrypt the existing vault for
// this request only. Neither request nor response bodies are included in errors.
func (c *SignerClient) ExportMnemonics(ctx context.Context, passphrase string) (*MnemonicExport, error) {
	if strings.TrimSpace(passphrase) == "" || len(passphrase) > 1024 {
		return nil, ErrMnemonicExportRejected
	}
	if c == nil || c.http == nil {
		return nil, errors.New("signer unavailable")
	}
	body, err := json.Marshal(struct {
		Passphrase string `json:"passphrase"`
	}{Passphrase: passphrase})
	if err != nil {
		return nil, errors.New("could not encode signer request")
	}
	defer func() { clear(body) }()
	if len(body) > 4<<10 {
		return nil, ErrMnemonicExportRejected
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://signer/backup/mnemonics", bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("could not create signer request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cache-Control", "no-store, no-cache")
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("signer mnemonic export failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden {
		return nil, ErrMnemonicExportRejected
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, ErrMnemonicExportLimited
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("signer mnemonic export unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (4<<10)+1))
	defer func() { clear(raw) }()
	if err != nil || len(raw) > 4<<10 {
		return nil, errors.New("signer returned an invalid mnemonic response")
	}
	var out MnemonicExport
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&out) != nil || strings.TrimSpace(out.DepositMnemonic) == "" ||
		strings.TrimSpace(out.HotMnemonic) == "" || decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("signer returned an invalid mnemonic response")
	}
	return &out, nil
}
