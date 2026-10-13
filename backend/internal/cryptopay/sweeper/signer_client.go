package sweeper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// SignerClient signs sweep and gas transactions via the isolated signing
// service over its unix socket.
type SignerClient struct {
	http *http.Client
}

func NewSignerClient(socketPath string) *SignerClient {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	return &SignerClient{http: &http.Client{Transport: transport, Timeout: 60 * time.Second}}
}

// EVMSignRequest mirrors the signer's /sign/evm body.
type EVMSignRequest struct {
	Index    uint32  `json:"index"`
	Kind     string  `json:"kind,omitempty"`      // "sweep" (default) or "gas"
	GasIndex *uint32 `json:"gas_index,omitempty"` // kind=gas: index the target derives from
	ChainID  int64   `json:"chain_id"`
	Token    string  `json:"token"`
	To       string  `json:"to"`
	Amount   string  `json:"amount"`
	Nonce    uint64  `json:"nonce"`
	GasPrice string  `json:"gas_price"`
	GasLimit uint64  `json:"gas_limit"`
}

// SignEVM asks the signer for a signed transaction.
func (c *SignerClient) SignEVM(ctx context.Context, req EVMSignRequest) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://signer/sign/evm", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("signer sign: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("signer sign: status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	var out struct {
		SignedTx string `json:"signed_tx"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return "", fmt.Errorf("signer sign decode: %w", err)
	}
	if out.SignedTx == "" {
		return "", fmt.Errorf("signer returned empty transaction")
	}
	return out.SignedTx, nil
}

// Record is one sweep_records row.
type Record struct {
	ID          int64
	Chain       string
	FromAddress string
	ToAddress   string
	Token       string
	Amount      string
	GasTxHash   string
	SweepTxHash string
	Status      string
	Error       string
}

// SignerStatus is the signer's lock state, as reported by /healthz.
type SignerStatus struct {
	OK       bool `json:"ok"`
	Unlocked bool `json:"unlocked"`
}

// Status reports whether the signer holds the seeds. A locked signer refuses
// every signing request, so the console surfaces this before a sweep.
func (c *SignerClient) Status(ctx context.Context) (*SignerStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://signer/healthz", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("signer status: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("signer status: http %d", resp.StatusCode)
	}
	var out SignerStatus
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("signer status decode: %w", err)
	}
	return &out, nil
}

// Unlock sends the operator passphrase. On first run it also carries the
// seeds, creating the vault; afterwards it only opens the existing one.
func (c *SignerClient) Unlock(ctx context.Context, passphrase, depositMnemonic, hotMnemonic string) error {
	body, err := json.Marshal(map[string]string{
		"passphrase":       passphrase,
		"deposit_mnemonic": depositMnemonic,
		"hot_mnemonic":     hotMnemonic,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://signer/unlock", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("signer unlock: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(payload, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("signer unlock: http %d", resp.StatusCode)
	}
	return nil
}
