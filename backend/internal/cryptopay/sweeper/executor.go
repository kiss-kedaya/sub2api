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

// SignerClient signs sweep transactions via the isolated signing service.
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
	Index    uint32 `json:"index"`
	ChainID  int64  `json:"chain_id"`
	Token    string `json:"token"`
	To       string `json:"to"`
	Amount   string `json:"amount"`
	Nonce    uint64 `json:"nonce"`
	GasPrice string `json:"gas_price"`
	GasLimit uint64 `json:"gas_limit"`
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
