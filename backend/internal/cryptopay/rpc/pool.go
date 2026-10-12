// Package rpc provides a rotating JSON-RPC client pool for the deposit
// scanners. Endpoints come from admin settings; the pool never hardcodes
// any provider.
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Pool rotates through a chain's endpoints, preferring the last healthy one.
type Pool struct {
	mu        sync.RWMutex
	endpoints []string
	next      atomic.Uint64
	client    *http.Client
}

// NewPool builds a pool from a comma-separated endpoint list.
func NewPool(endpoints string) *Pool {
	p := &Pool{client: &http.Client{Timeout: 15 * time.Second}}
	p.SetEndpoints(endpoints)
	return p
}

// SetEndpoints replaces the endpoint list (admin config change).
func (p *Pool) SetEndpoints(endpoints string) {
	var list []string
	for _, raw := range strings.Split(endpoints, ",") {
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			list = append(list, trimmed)
		}
	}
	p.mu.Lock()
	p.endpoints = list
	p.mu.Unlock()
}

// Endpoints returns the current list (for status display).
func (p *Pool) Endpoints() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]string(nil), p.endpoints...)
}

// Call runs one JSON-RPC method, rotating endpoints on transport failure.
// The first successful HTTP exchange wins even when the RPC itself returns
// an error object (that is a valid protocol response).
func (p *Pool) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	p.mu.RLock()
	endpoints := append([]string(nil), p.endpoints...)
	p.mu.RUnlock()
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no rpc endpoints configured")
	}
	start := int(p.next.Add(1)-1) % len(endpoints)
	var lastErr error
	for i := 0; i < len(endpoints); i++ {
		endpoint := endpoints[(start+i)%len(endpoints)]
		result, err := p.callOne(ctx, endpoint, method, params)
		if err == nil {
			return result, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("all rpc endpoints failed: %w", lastErr)
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (p *Pool) callOne(ctx context.Context, endpoint, method string, params any) (json.RawMessage, error) {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("rpc %s: http %d", endpoint, resp.StatusCode)
	}
	var decoded rpcResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("rpc %s: decode: %w", endpoint, err)
	}
	if decoded.Error != nil {
		return nil, fmt.Errorf("rpc %s: %s (%d)", endpoint, decoded.Error.Message, decoded.Error.Code)
	}
	return decoded.Result, nil
}

// BlockNumber returns the latest block number (EVM chains).
func (p *Pool) BlockNumber(ctx context.Context) (uint64, error) {
	result, err := p.Call(ctx, "eth_blockNumber", nil)
	if err != nil {
		return 0, err
	}
	var hex string
	if err := json.Unmarshal(result, &hex); err != nil {
		return 0, fmt.Errorf("block number decode: %w", err)
	}
	var number uint64
	if _, err := fmt.Sscanf(strings.TrimPrefix(hex, "0x"), "%x", &number); err != nil {
		return 0, fmt.Errorf("block number parse %q: %w", hex, err)
	}
	return number, nil
}

// Balance returns an address's native balance in wei.
func (p *Pool) Balance(ctx context.Context, address string) (*big.Int, error) {
	result, err := p.Call(ctx, "eth_getBalance", []any{address, "latest"})
	if err != nil {
		return nil, err
	}
	var hex string
	if err := json.Unmarshal(result, &hex); err != nil {
		return nil, fmt.Errorf("balance decode: %w", err)
	}
	value, ok := new(big.Int).SetString(strings.TrimPrefix(hex, "0x"), 16)
	if !ok {
		return nil, fmt.Errorf("balance parse %q", hex)
	}
	return value, nil
}

// Nonce returns the account's next nonce including pending transactions;
// gas top-up and sweep for one address are serial, so pending is correct.
func (p *Pool) Nonce(ctx context.Context, address string) (uint64, error) {
	result, err := p.Call(ctx, "eth_getTransactionCount", []any{address, "pending"})
	if err != nil {
		return 0, err
	}
	var hex string
	if err := json.Unmarshal(result, &hex); err != nil {
		return 0, fmt.Errorf("nonce decode: %w", err)
	}
	var nonce uint64
	if _, err := fmt.Sscanf(strings.TrimPrefix(hex, "0x"), "%x", &nonce); err != nil {
		return 0, fmt.Errorf("nonce parse %q: %w", hex, err)
	}
	return nonce, nil
}

// GasPrice returns the current gas price in wei.
func (p *Pool) GasPrice(ctx context.Context) (*big.Int, error) {
	result, err := p.Call(ctx, "eth_gasPrice", nil)
	if err != nil {
		return nil, err
	}
	var hex string
	if err := json.Unmarshal(result, &hex); err != nil {
		return nil, fmt.Errorf("gas price decode: %w", err)
	}
	value, ok := new(big.Int).SetString(strings.TrimPrefix(hex, "0x"), 16)
	if !ok || value.Sign() <= 0 {
		return nil, fmt.Errorf("gas price parse %q", hex)
	}
	return value, nil
}

// SendRawTransaction broadcasts a signed transaction and returns its hash.
// A node that already has the transaction returns the same hash, so retrying
// a broadcast is safe.
func (p *Pool) SendRawTransaction(ctx context.Context, rawTx string) (string, error) {
	result, err := p.Call(ctx, "eth_sendRawTransaction", []any{rawTx})
	if err != nil {
		return "", err
	}
	var hash string
	if err := json.Unmarshal(result, &hash); err != nil {
		return "", fmt.Errorf("send raw decode: %w", err)
	}
	if hash == "" {
		return "", fmt.Errorf("empty transaction hash")
	}
	return hash, nil
}
