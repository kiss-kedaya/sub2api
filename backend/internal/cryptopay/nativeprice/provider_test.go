package nativeprice

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func requireDecimal(t *testing.T, raw string) decimal.Decimal {
	t.Helper()
	value, err := decimal.NewFromString(raw)
	require.NoError(t, err)
	return value
}

// tickerServer serves Binance-shaped ticker responses and counts hits.
func tickerServer(t *testing.T, price string, failures int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit := hits.Add(1)
		if hit <= failures {
			http.Error(w, "temporarily unavailable", http.StatusBadGateway)
			return
		}
		if got := r.URL.Query().Get("symbol"); got == "" {
			http.Error(w, "missing symbol", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"symbol":"ETHUSDT","price":"` + price + `"}`))
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

func testProvider(t *testing.T, server *httptest.Server, clock func() time.Time) *Provider {
	t.Helper()
	return New(server.URL+"/api/v3/ticker/price",
		WithHTTPClient(server.Client()),
		WithBackoff(time.Millisecond),
		WithClock(clock),
	)
}

func TestQuoteSuccessAndCache(t *testing.T) {
	server, hits := tickerServer(t, "2450.50", 0)
	p := testProvider(t, server, time.Now)

	quote, err := p.Quote(t.Context(), "ethereum")
	require.NoError(t, err)
	require.Equal(t, "2450.5", quote.USD.String())
	require.False(t, quote.At.IsZero())
	require.Contains(t, quote.Source, "ETHUSDT")

	again, err := p.Quote(t.Context(), "ethereum")
	require.NoError(t, err)
	require.Equal(t, quote.USD.String(), again.USD.String())
	require.EqualValues(t, 1, hits.Load(), "a second call inside the 60s window must use the cache")
}

func TestQuoteRetriesBeforeGivingUp(t *testing.T) {
	server, hits := tickerServer(t, "600.25", 2)
	p := testProvider(t, server, time.Now)

	quote, err := p.Quote(t.Context(), "bsc")
	require.NoError(t, err)
	require.Equal(t, "600.25", quote.USD.String())
	require.EqualValues(t, 3, hits.Load(), "two failures must be retried, then succeed")
}

func TestQuoteFailureIsUnavailable(t *testing.T) {
	server, hits := tickerServer(t, "1", 100)
	p := testProvider(t, server, time.Now)

	_, err := p.Quote(t.Context(), "ethereum")
	require.ErrorIs(t, err, ErrUnavailable)
	require.EqualValues(t, maxAttempts, hits.Load())
}

// A quote older than MaxQuoteAge is unusable even when it is the last value we
// saw. The provider must refresh, and a failed refresh must not resurrect it.
func TestStaleQuoteIsNeverServedOnFailure(t *testing.T) {
	now := time.Now()
	server, _ := tickerServer(t, "1", 100)
	p := testProvider(t, server, func() time.Time { return now })
	p.cache["ETHUSDT"] = Quote{USD: requireDecimal(t, "2000"), At: now.Add(-MaxQuoteAge - time.Minute), Source: "stale"}

	_, err := p.Quote(t.Context(), "ethereum")
	require.ErrorIs(t, err, ErrUnavailable)
	p.mu.Lock()
	_, cached := p.cache["ETHUSDT"]
	p.mu.Unlock()
	require.False(t, cached, "an expired quote must be dropped, not kept as a fallback")
}

// A quote that is merely older than CacheTTL still must not be served while
// the refresh is failing: crediting on a price the feed no longer confirms is
// exactly the failure mode this provider exists to prevent.
func TestExpiredCacheIsNotAFallback(t *testing.T) {
	now := time.Now()
	server, _ := tickerServer(t, "1", 100)
	p := testProvider(t, server, func() time.Time { return now })
	p.cache["ETHUSDT"] = Quote{USD: requireDecimal(t, "2000"), At: now.Add(-CacheTTL - time.Second), Source: "old"}

	_, err := p.Quote(t.Context(), "ethereum")
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestQuoteSymbolTemplate(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"symbol":"BNBUSDT","price":"610"}`))
	}))
	t.Cleanup(server.Close)
	p := New(server.URL+"/ticker/{symbol}", WithHTTPClient(server.Client()), WithBackoff(time.Millisecond))

	quote, err := p.Quote(t.Context(), "bsc")
	require.NoError(t, err)
	require.Equal(t, "610", quote.USD.String())
	require.Equal(t, "/ticker/BNBUSDT", path)
}

func TestQuoteRejectsNonPositivePrice(t *testing.T) {
	server, _ := tickerServer(t, "0", 0)
	p := testProvider(t, server, time.Now)

	_, err := p.Quote(t.Context(), "ethereum")
	require.ErrorIs(t, err, ErrUnavailable)
}

func TestUnsupportedChainHasNoMarket(t *testing.T) {
	p := New("http://127.0.0.1:1", WithBackoff(time.Millisecond))
	_, ok := SymbolForChain("polygon")
	require.False(t, ok)

	_, err := p.Quote(t.Context(), "polygon")
	require.ErrorIs(t, err, ErrUnavailable)
	require.False(t, errors.Is(err, ErrStale))
}

func TestDefaultProviderBlocksSSRFRedirectsAndInvalidTemplateScheme(t *testing.T) {
	server, hits := tickerServer(t, "2500", 0)

	for name, target := range map[string]string{
		"loopback httptest": server.URL + "/api/v3/ticker/price",
		"aws metadata":      "http://169.254.169.254/latest/meta-data",
		"cgnat metadata":    "http://100.100.100.200/ticker/{symbol}",
		"rfc1918":           "https://10.0.0.1/ticker",
		"ipv6 loopback":     "http://[::1]:8080/ticker",
	} {
		t.Run(name, func(t *testing.T) {
			p := New(target, WithBackoff(time.Millisecond))
			_, err := p.Quote(t.Context(), "ethereum")
			require.ErrorIs(t, err, ErrUnavailable)
		})
	}
	require.Zero(t, hits.Load(), "blocked SSRF target must never receive HTTP requests")

	t.Run("dns resolving to private ip blocked", func(t *testing.T) {
		ctx := urlvalidator.WithResolver(t.Context(), func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("10.20.30.40")}, nil
		})
		p := New("https://rebind.example.test/ticker/{symbol}", WithBackoff(time.Millisecond))
		_, err := p.Quote(ctx, "ethereum")
		require.ErrorIs(t, err, ErrUnavailable)
	})

	t.Run("template with non-http scheme rejected", func(t *testing.T) {
		p := New("file:///etc/{symbol}", WithBackoff(time.Millisecond))
		_, err := p.Quote(t.Context(), "ethereum")
		require.ErrorIs(t, err, ErrUnavailable)
	})

	t.Run("default client does not follow redirects", func(t *testing.T) {
		p := New("https://api.binance.com/api/v3/ticker/price")
		require.NotNil(t, p.client.CheckRedirect)
		err := p.client.CheckRedirect(httptest.NewRequest(http.MethodGet, "https://evil.example.test", nil), nil)
		require.ErrorIs(t, err, http.ErrUseLastResponse)
	})
}
