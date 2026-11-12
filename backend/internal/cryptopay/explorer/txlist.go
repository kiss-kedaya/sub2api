// Package explorer talks to Etherscan-compatible account APIs (Etherscan v2,
// BscScan v2, and any host exposing the same module/action contract).
//
// It exists because a native-coin (ETH/BNB) transfer is only visible in a
// transaction, never in a log. Reading every block with full transaction bodies
// costs one request per block — 28800 a day on BSC — which is what a provider
// package counts. One txlist call per address returns every ordinary
// transaction that touched that address, so the request count scales with the
// number of watched addresses and the polling interval instead of with the
// chain's block rate.
package explorer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Tx is one ordinary transaction as the account API reports it. Value is the
// native amount in wei: the account API serialises it as a decimal string.
type Tx struct {
	Hash        string
	From        string
	To          *string // nil for contract creation
	Value       *big.Int
	BlockNumber uint64
}

// Endpoint is one Etherscan-compatible API. BaseURL is the API root without a
// query string (for example https://api.bscscan.com/v2/api). ChainID is sent as
// the `chainid` parameter that the v2 APIs require; 0 omits it for a legacy v1
// host.
type Endpoint struct {
	BaseURL string
	ChainID int64
}

// Config builds a Client. Zero values select the production defaults.
type Config struct {
	Endpoints  map[string]Endpoint
	APIKey     string
	HTTPClient *http.Client
	// Retries bounds the attempts for a retryable failure (HTTP 429/5xx,
	// transport error, provider rate-limit answer). Default 3.
	Retries int
	// Backoff is the first retry delay; it doubles per attempt. Default 1s.
	Backoff time.Duration
	// Sleep is injectable so tests assert the backoff without waiting.
	Sleep func(context.Context, time.Duration) error
}

// Errors callers distinguish: a missing key must never be retried forever, and
// it must never be treated as "no deposits".
var (
	ErrNoAPIKey     = errors.New("explorer: no API key configured")
	ErrNoEndpoint   = errors.New("explorer: no endpoint configured for chain")
	ErrUnauthorized = errors.New("explorer: API key rejected")
)

// APIError is a provider answer that is not a transaction list.
type APIError struct {
	Message string
	Detail  string
	// Retryable marks a transient provider/transport failure.
	Retryable bool
}

func (e *APIError) Error() string {
	return fmt.Sprintf("explorer: %s: %s", e.Message, e.Detail)
}

// Client is safe for concurrent use.
type Client struct {
	endpoints map[string]Endpoint
	apiKey    string
	http      *http.Client
	retries   int
	backoff   time.Duration
	sleep     func(context.Context, time.Duration) error
}

// NewClient validates the configuration. An empty API key is allowed so the
// caller can start the poller and report a clear, non-silent failure instead of
// crashing at boot; TxList returns ErrNoAPIKey.
func NewClient(cfg Config) (*Client, error) {
	endpoints := make(map[string]Endpoint, len(cfg.Endpoints))
	for chain, endpoint := range cfg.Endpoints {
		root := strings.TrimSpace(endpoint.BaseURL)
		if root == "" {
			continue
		}
		parsed, err := url.Parse(root)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, fmt.Errorf("explorer: invalid endpoint for chain %q", chain)
		}
		if parsed.RawQuery != "" {
			return nil, fmt.Errorf("explorer: endpoint for chain %q must not carry a query string", chain)
		}
		endpoints[chain] = Endpoint{BaseURL: strings.TrimSuffix(root, "/"), ChainID: endpoint.ChainID}
	}
	retries := cfg.Retries
	if retries <= 0 {
		retries = 3
	}
	backoff := cfg.Backoff
	if backoff <= 0 {
		backoff = time.Second
	}
	sleep := cfg.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{
		endpoints: endpoints,
		apiKey:    strings.TrimSpace(cfg.APIKey),
		http:      client,
		retries:   retries,
		backoff:   backoff,
		sleep:     sleep,
	}, nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// HasChain reports whether a chain has a usable endpoint.
func (c *Client) HasChain(chain string) bool {
	_, ok := c.endpoints[chain]
	return ok
}

// TxList returns the ordinary transactions of address in [startBlock, endBlock]
// in ascending block order. A provider-side empty answer is an empty list; any
// other failure is an error so the caller keeps the cursor where it was.
func (c *Client) TxList(ctx context.Context, chain, address string, startBlock, endBlock uint64) ([]Tx, error) {
	if c.apiKey == "" {
		return nil, ErrNoAPIKey
	}
	endpoint, ok := c.endpoints[chain]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoEndpoint, chain)
	}
	if startBlock > endBlock {
		return nil, nil
	}
	target, err := url.Parse(endpoint.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNoEndpoint, chain)
	}
	query := target.Query()
	if endpoint.ChainID > 0 {
		query.Set("chainid", strconv.FormatInt(endpoint.ChainID, 10))
	}
	query.Set("module", "account")
	query.Set("action", "txlist")
	query.Set("address", address)
	query.Set("startblock", strconv.FormatUint(startBlock, 10))
	query.Set("endblock", strconv.FormatUint(endBlock, 10))
	query.Set("sort", "asc")
	query.Set("apikey", c.apiKey)
	target.RawQuery = query.Encode()

	var lastErr error
	delay := c.backoff
	for attempt := 0; attempt < c.retries; attempt++ {
		if attempt > 0 {
			if err := c.sleep(ctx, delay); err != nil {
				return nil, err
			}
			delay *= 2
		}
		transactions, err := c.fetch(ctx, target.String())
		if err == nil {
			return transactions, nil
		}
		lastErr = err
		var apiErr *APIError
		if errors.As(err, &apiErr) && !apiErr.Retryable {
			return nil, err
		}
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *Client) fetch(ctx context.Context, endpoint string) ([]Tx, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &APIError{Message: "transport", Detail: err.Error(), Retryable: true}
	}
	defer func() { _ = response.Body.Close() }()
	// The URL carries the API key, so it must never appear in an error message.
	body, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	if err != nil {
		return nil, &APIError{Message: "read", Detail: "response body unreadable", Retryable: true}
	}
	if response.StatusCode != http.StatusOK {
		retryable := response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
		return nil, &APIError{Message: "http", Detail: fmt.Sprintf("status %d", response.StatusCode), Retryable: retryable}
	}
	var payload struct {
		Status  string          `json:"status"`
		Message string          `json:"message"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, &APIError{Message: "decode", Detail: "response is not JSON", Retryable: false}
	}
	if payload.Status == "1" {
		return decodeRows(payload.Result)
	}
	detail := strings.TrimSpace(string(payload.Result))
	detail = strings.Trim(detail, `"`)
	lowered := strings.ToLower(detail)
	// The account API reports "no rows" with status 0, which is a success.
	if strings.Contains(strings.ToLower(payload.Message), "no transactions found") ||
		strings.Contains(lowered, "no transactions found") {
		return nil, nil
	}
	if strings.Contains(lowered, "invalid api key") || strings.Contains(lowered, "invalid key") {
		return nil, fmt.Errorf("%w: %s", ErrUnauthorized, detail)
	}
	retryable := strings.Contains(lowered, "rate limit") ||
		strings.Contains(lowered, "too many requests") ||
		strings.Contains(lowered, "timeout") ||
		strings.Contains(lowered, "busy")
	return nil, &APIError{Message: strings.TrimSpace(payload.Message), Detail: detail, Retryable: retryable}
}

func decodeRows(raw json.RawMessage) ([]Tx, error) {
	var rows []struct {
		BlockNumber string `json:"blockNumber"`
		Hash        string `json:"hash"`
		From        string `json:"from"`
		To          string `json:"to"`
		Value       string `json:"value"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, &APIError{Message: "decode", Detail: "transaction list is not an array", Retryable: false}
	}
	out := make([]Tx, 0, len(rows))
	for index, row := range rows {
		tx, err := decodeRow(row.BlockNumber, row.Hash, row.From, row.To, row.Value)
		if err != nil {
			return nil, &APIError{Message: "decode", Detail: fmt.Sprintf("row %d: %v", index, err), Retryable: false}
		}
		out = append(out, tx)
	}
	return out, nil
}

func decodeRow(blockNumber, hash, from, to, value string) (Tx, error) {
	block, err := strconv.ParseUint(strings.TrimSpace(blockNumber), 10, 64)
	if err != nil {
		return Tx{}, errors.New("blockNumber is not a decimal height")
	}
	if !validHex(hash, 32) {
		return Tx{}, errors.New("hash is not a 32-byte value")
	}
	if !validHex(from, 20) {
		return Tx{}, errors.New("from is not a 20-byte address")
	}
	// The account API returns an empty string for contract creation; a numeric
	// value must still be a real address.
	var recipient *string
	if trimmed := strings.TrimSpace(to); trimmed != "" {
		if !validHex(trimmed, 20) {
			return Tx{}, errors.New("to is not a 20-byte address")
		}
		lowered := strings.ToLower(trimmed)
		recipient = &lowered
	}
	amount, ok := new(big.Int).SetString(strings.TrimSpace(value), 10)
	if !ok || amount.Sign() < 0 || amount.BitLen() > 256 {
		return Tx{}, errors.New("value is not a uint256 decimal amount")
	}
	return Tx{
		Hash:        strings.ToLower(hash),
		From:        strings.ToLower(from),
		To:          recipient,
		Value:       amount,
		BlockNumber: block,
	}, nil
}

func validHex(value string, size int) bool {
	if len(value) != 2+size*2 || !strings.HasPrefix(value, "0x") {
		return false
	}
	for _, c := range value[2:] {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
