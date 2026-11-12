// Package nativeprice resolves the USD price of a chain's native coin.
//
// Native deposits must be credited at the market price observed when the
// deposit is settled: a constant multiplier cannot tell 1 ETH from 1 USDT.
// The provider is deliberately fail-closed. It returns an error instead of a
// default, a zero or a quote older than MaxQuoteAge, and the ledger leaves the
// deposit pending for the next pass. Crediting on a guessed price is worse
// than crediting a few minutes late.
package nativeprice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
)

const (
	// DefaultBaseURL is Binance's public ticker endpoint. Operators can point
	// it at a mirror or an internal proxy without touching the code.
	DefaultBaseURL = "https://api.binance.com/api/v3/ticker/price"
	// CacheTTL keeps a scan pass from hitting the exchange once per deposit:
	// a pass can credit many native transfers in a row.
	CacheTTL = 60 * time.Second
	// MaxQuoteAge is the hard freshness ceiling. A quote older than this is
	// never used for crediting, even if it is the last value we saw.
	MaxQuoteAge = 10 * time.Minute
	// requestTimeout bounds one HTTP attempt. Three attempts with backoff
	// still finish well inside a scan pass.
	requestTimeout = 5 * time.Second
	// maxAttempts is the total number of HTTP tries per refresh (one plus two
	// retries). A failed refresh never falls back to a stale cached quote.
	maxAttempts = 3
	// defaultBackoff is the first retry delay; it doubles per attempt.
	defaultBackoff = 150 * time.Millisecond
)

var (
	// ErrUnavailable means no trustworthy price could be obtained. Callers
	// must not credit; the deposit stays pending.
	ErrUnavailable = errors.New("native price unavailable")
	// ErrStale means a quote exists but is older than MaxQuoteAge.
	ErrStale = errors.New("native price quote is stale")
)

// Quote is one USD price observation.
type Quote struct {
	// USD is the price of one whole coin in USD.
	USD decimal.Decimal
	// At is when the price was observed (the successful HTTP response time).
	At time.Time
	// Source identifies the price feed, for the audit trail.
	Source string
}

// Age returns how old the quote is at now. A zero At counts as infinitely old.
func (q Quote) Age(now time.Time) time.Duration {
	if q.At.IsZero() {
		return 1 << 62
	}
	return now.Sub(q.At)
}

// Usable reports whether the quote may be used for crediting at now.
func (q Quote) Usable(now time.Time) bool {
	return q.USD.Sign() > 0 && !q.At.IsZero() && q.Age(now) <= MaxQuoteAge
}

// SymbolForChain maps a supported chain to its exchange symbol. Chains whose
// native coin has no configured market return ok=false and never get a price.
func SymbolForChain(chain string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(chain)) {
	case "ethereum":
		return "ETHUSDT", true
	case "bsc":
		return "BNBUSDT", true
	}
	return "", false
}

// Provider fetches and caches native-coin USD quotes.
type Provider struct {
	baseURL   string
	client    *http.Client
	now       func() time.Time
	sleep     func(time.Duration)
	sleepBase time.Duration

	mu    sync.Mutex
	cache map[string]Quote
}

// Option customises a Provider. Options exist for tests and for deployments
// that need a shared HTTP client; production defaults need none.
type Option func(*Provider)

// WithHTTPClient replaces the default client.
func WithHTTPClient(client *http.Client) Option {
	return func(p *Provider) {
		if client != nil {
			p.client = client
		}
	}
}

// WithClock replaces the wall clock (tests).
func WithClock(now func() time.Time) Option {
	return func(p *Provider) {
		if now != nil {
			p.now = now
		}
	}
}

// WithBackoff replaces the first retry delay (tests).
func WithBackoff(base time.Duration) Option {
	return func(p *Provider) {
		if base > 0 {
			p.sleepBase = base
		}
	}
}

// New builds a provider. An empty baseURL uses DefaultBaseURL.
func New(baseURL string, opts ...Option) *Provider {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	p := &Provider{
		baseURL:   baseURL,
		client:    &http.Client{Timeout: requestTimeout},
		now:       time.Now,
		sleep:     time.Sleep,
		cache:     make(map[string]Quote),
		sleepBase: defaultBackoff,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Quote returns a usable USD quote for chain, serving a cached observation
// when it is younger than CacheTTL. Any refresh failure is returned as
// ErrUnavailable: there is no stale-price fallback.
func (p *Provider) Quote(ctx context.Context, chain string) (Quote, error) {
	symbol, ok := SymbolForChain(chain)
	if !ok {
		return Quote{}, fmt.Errorf("%w: no market symbol for chain %q", ErrUnavailable, chain)
	}
	now := p.now()
	if quote, ok := p.cached(symbol, now); ok {
		return quote, nil
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			p.sleep(p.sleepBase * time.Duration(1<<(attempt-1)))
		}
		quote, err := p.fetch(ctx, symbol)
		if err == nil {
			p.store(symbol, quote)
			return quote, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return Quote{}, fmt.Errorf("%w: %s: %v", ErrUnavailable, symbol, lastErr)
}

func (p *Provider) cached(symbol string, now time.Time) (Quote, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	quote, ok := p.cache[symbol]
	if !ok {
		return Quote{}, false
	}
	if !quote.Usable(now) {
		// Expired observations must not survive as a fallback: drop it so a
		// later refresh cannot accidentally resurrect it.
		delete(p.cache, symbol)
		return Quote{}, false
	}
	if quote.Age(now) < CacheTTL {
		return quote, true
	}
	return Quote{}, false
}

func (p *Provider) store(symbol string, quote Quote) {
	p.mu.Lock()
	p.cache[symbol] = quote
	p.mu.Unlock()
}

type tickerResponse struct {
	Symbol string `json:"symbol"`
	Price  string `json:"price"`
}

func (p *Provider) fetch(ctx context.Context, symbol string) (Quote, error) {
	endpoint, err := p.endpoint(symbol)
	if err != nil {
		return Quote{}, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Quote{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return Quote{}, fmt.Errorf("request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Cap the body: a misconfigured URL must not stream an unbounded response
	// into a scan loop.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return Quote{}, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Quote{}, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var decoded tickerResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return Quote{}, fmt.Errorf("decode body: %w", err)
	}
	price, err := decimal.NewFromString(strings.TrimSpace(decoded.Price))
	if err != nil || price.Sign() <= 0 {
		return Quote{}, fmt.Errorf("invalid price %q", decoded.Price)
	}
	return Quote{USD: price, At: p.now(), Source: p.sourceName(symbol)}, nil
}

// endpoint builds the ticker URL. A base containing {symbol} is expanded;
// otherwise the symbol is added as Binance's symbol query parameter, which is
// what mirrors of the public endpoint expect.
func (p *Provider) endpoint(symbol string) (string, error) {
	if strings.Contains(p.baseURL, "{symbol}") {
		return strings.ReplaceAll(p.baseURL, "{symbol}", url.QueryEscape(symbol)), nil
	}
	parsed, err := url.Parse(p.baseURL)
	if err != nil {
		return "", fmt.Errorf("parse price url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("price url must be absolute http(s)")
	}
	query := parsed.Query()
	query.Set("symbol", symbol)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// sourceName keeps the audit trail readable without storing the full URL,
// which may contain a private mirror host.
func (p *Provider) sourceName(symbol string) string {
	parsed, err := url.Parse(p.baseURL)
	if err != nil || parsed.Host == "" {
		return "ticker:" + symbol
	}
	return parsed.Host + ":" + symbol
}
