package cryptopay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/nativeprice"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func depositInfoConfig() CryptoConfig {
	cfg := DefaultCryptoConfig()
	cfg.Enabled = true
	cfg.Chains = []string{ChainEthereum, ChainBSC}
	cfg.CreditMultiplier = 6.7
	cfg.CreditMinAmount = 0.001
	cfg.CreditMaxAmount = 10000
	return cfg
}

func depositInfoAssigned() map[string]string {
	return map[string]string{
		addresses.FamilyEVM: "0x1111111111111111111111111111111111111111",
	}
}

func depositInfoAsset(t *testing.T, info *DepositInfo, chain, symbol string) DepositAsset {
	t.Helper()
	for _, c := range info.Chains {
		if c.Chain != chain {
			continue
		}
		for _, a := range c.Assets {
			if a.Symbol == symbol {
				return a
			}
		}
	}
	t.Fatalf("asset %s on %s not found in %+v", symbol, chain, info.Chains)
	return DepositAsset{}
}

func TestDisabledDepositInfoSerializesEmptyChains(t *testing.T) {
	s := &Service{}
	info, err := s.DepositInfo(t.Context(), 42)
	require.NoError(t, err)
	require.False(t, info.Enabled)
	raw, err := json.Marshal(info)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	require.Equal(t, "[]", string(fields["chains"]))
}

// The deposit page must be able to tell "this asset is settled at the live
// market price" from "this asset has a fixed rate". A native coin carrying the
// stablecoin multiplier is exactly the bug this guards.
func TestDepositInfoMarksNativeCoinAsMarketPricedWithoutRate(t *testing.T) {
	info := buildDepositInfo(depositInfoConfig(), depositInfoAssigned())
	require.True(t, info.Enabled)

	for _, chain := range []string{ChainEthereum, ChainBSC} {
		var native DepositAsset
		found := false
		for _, c := range info.Chains {
			if c.Chain != chain {
				continue
			}
			for _, a := range c.Assets {
				if a.Native {
					native, found = a, true
				}
			}
		}
		require.True(t, found, "%s must expose its native coin", chain)
		require.True(t, native.MarketPriced, "%s native coin must be market priced", chain)
		require.Nil(t, native.Rate, "%s native coin must not expose a fixed rate", chain)
	}

	ether := depositInfoAsset(t, info, ChainEthereum, "ETH")
	require.True(t, ether.MarketPriced)
	require.Nil(t, ether.Rate)
}

// Stablecoins are unchanged: the configured multiplier still applies to a
// token amount that is already a USD amount.
func TestDepositInfoKeepsStablecoinFixedRate(t *testing.T) {
	info := buildDepositInfo(depositInfoConfig(), depositInfoAssigned())
	for _, symbol := range []string{"USDT", "USDC"} {
		asset := depositInfoAsset(t, info, ChainEthereum, symbol)
		require.False(t, asset.MarketPriced, "%s must stay fixed-rate", symbol)
		require.NotNil(t, asset.Rate, "%s must keep its rate", symbol)
		require.Equal(t, 6.7, *asset.Rate)
		require.Nil(t, asset.ReferencePriceUSD)
	}
	// Thresholds keep their existing values for stablecoins (token units).
	require.Equal(t, 0.001, info.MinAmount)
	require.Equal(t, 10000.0, info.MaxAmount)
}

// A client that reads JSON must see no rate at all for a native coin: the
// response-wide rate is gone, and the asset-level one is null.
func TestDepositInfoJSONHasNoRateForNativeCoin(t *testing.T) {
	info := buildDepositInfo(depositInfoConfig(), depositInfoAssigned())
	raw, err := json.Marshal(info)
	require.NoError(t, err)

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &top))
	_, hasTopLevelRate := top["rate"]
	require.False(t, hasTopLevelRate, "deposit-info must not expose an asset-independent rate: %s", raw)

	var decoded struct {
		Chains []struct {
			Chain  string `json:"chain"`
			Assets []struct {
				Symbol       string   `json:"symbol"`
				Native       bool     `json:"native"`
				MarketPriced bool     `json:"market_priced"`
				Rate         *float64 `json:"rate"`
			} `json:"assets"`
		} `json:"chains"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	seenNative, seenStable := false, false
	for _, chain := range decoded.Chains {
		for _, asset := range chain.Assets {
			if asset.Native {
				seenNative = true
				require.True(t, asset.MarketPriced, "%s", asset.Symbol)
				require.Nil(t, asset.Rate, "%s must serialize rate as null", asset.Symbol)
				continue
			}
			seenStable = true
			require.False(t, asset.MarketPriced, "%s", asset.Symbol)
			require.NotNil(t, asset.Rate, "%s must serialize its rate", asset.Symbol)
		}
	}
	require.True(t, seenNative && seenStable)
}

func TestNativePriceReferenceUsesOneSnapshotDuringReload(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	oldFeed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(entered) })
		<-release
		if _, err := fmt.Fprint(w, `{"price":"100"}`); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(oldFeed.Close)
	newFeed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := fmt.Fprint(w, `{"price":"200"}`); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(newFeed.Close)
	repo := &reloadConfigRepository{}
	cfg := depositInfoConfig()
	cfg.NativePriceURL = oldFeed.URL
	repo.set(t, cfg)
	s := &Service{settings: service.NewSettingService(repo, nil), wallet: wallet.New(nil)}
	t.Cleanup(s.Close)
	s.Reload(t.Context())
	info := buildDepositInfo(cfg, depositInfoAssigned())
	done := make(chan struct{})
	go func() {
		s.attachNativeReferencePrices(t.Context(), info)
		close(done)
	}()
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() { unblock(); <-done })
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("reference price request never reached local feed")
	}
	cfg.NativePriceURL = newFeed.URL
	repo.set(t, cfg)
	s.Reload(t.Context())
	// The reload must not wait for a network quote holding the read lock.
	unblock()
	<-done
	for _, chain := range []string{ChainEthereum, ChainBSC} {
		symbol := "ETH"
		if chain == ChainBSC {
			symbol = "BNB"
		}
		price := depositInfoAsset(t, info, chain, symbol).ReferencePriceUSD
		require.NotNil(t, price)
		require.Equal(t, "100", *price, "one page response must keep the same provider snapshot")
	}
}

func TestNativePriceReferenceConcurrentReload(t *testing.T) {
	t.Setenv("CRYPTO_INSTANCE_ENABLED", "false")
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "")
	t.Setenv("SUB2API_DISABLE_BACKGROUND_JOBS", "true")
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := fmt.Fprint(w, `{"price":"100"}`); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(feed.Close)
	repo := &reloadConfigRepository{}
	cfg := depositInfoConfig()
	cfg.NativePriceURL = feed.URL
	repo.set(t, cfg)
	s := &Service{settings: service.NewSettingService(repo, nil), wallet: wallet.New(nil)}
	t.Cleanup(s.Close)
	s.Reload(t.Context())
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 100 {
				info := buildDepositInfo(cfg, depositInfoAssigned())
				s.attachNativeReferencePrices(t.Context(), info)
			}
		}()
	}
	close(start)
	for range 100 {
		s.Reload(t.Context())
	}
	wg.Wait()
}

func TestAttachNativeReferencePricesAddsDisplayQuote(t *testing.T) {
	prices := map[string]string{"ETHUSDT": "3210.12000000", "BNBUSDT": "640.50000000"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		symbol := r.URL.Query().Get("symbol")
		price, ok := prices[symbol]
		if !ok {
			t.Errorf("unexpected ticker symbol %q", symbol)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"symbol":%q,"price":%q}`, symbol, price)
	}))
	defer server.Close()

	s := &Service{nativePrice: nativeprice.New(server.URL, nativeprice.WithBackoff(time.Millisecond))}
	info := buildDepositInfo(depositInfoConfig(), depositInfoAssigned())
	s.attachNativeReferencePrices(t.Context(), info)

	ether := depositInfoAsset(t, info, ChainEthereum, "ETH")
	require.NotNil(t, ether.ReferencePriceUSD)
	require.Equal(t, "3210.12", *ether.ReferencePriceUSD)
	// Every market-priced asset in the response gets its own chain's quote.
	require.Equal(t, "640.5", *depositInfoAsset(t, info, ChainBSC, "BNB").ReferencePriceUSD)
	// The display quote never becomes a credit rate: ETH stays market priced.
	require.True(t, ether.MarketPriced)
	require.Nil(t, ether.Rate)
	// Stablecoins are not quoted: they do not need a market reference.
	require.Nil(t, depositInfoAsset(t, info, ChainEthereum, "USDT").ReferencePriceUSD)
}

// A failing feed must not fail the page, must not leak a bogus price and must
// not turn the deposit page into a retry storm.
func TestAttachNativeReferencePricesIsSilentWhenFeedFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	s := &Service{nativePrice: nativeprice.New(server.URL, nativeprice.WithBackoff(time.Millisecond))}
	info := buildDepositInfo(depositInfoConfig(), depositInfoAssigned())
	started := time.Now()
	s.attachNativeReferencePrices(t.Context(), info)
	elapsed := time.Since(started)

	require.Less(t, elapsed, 3*time.Second)
	for _, chain := range info.Chains {
		for _, asset := range chain.Assets {
			require.Nil(t, asset.ReferencePriceUSD, "%s must have no quote after a feed outage", asset.Symbol)
			if asset.Native {
				require.True(t, asset.MarketPriced)
				require.Nil(t, asset.Rate)
			}
		}
	}
}

// The time budget is a hard ceiling: a hung ticker delays the page by at most
// nativeReferencePriceTimeout and then the page renders without a quote.
func TestAttachNativeReferencePricesRespectsTheTimeBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	s := &Service{nativePrice: nativeprice.New(server.URL, nativeprice.WithBackoff(time.Millisecond))}
	info := buildDepositInfo(depositInfoConfig(), depositInfoAssigned())
	started := time.Now()
	s.attachNativeReferencePrices(t.Context(), info)
	elapsed := time.Since(started)

	require.GreaterOrEqual(t, elapsed, nativeReferencePriceTimeout)
	require.Less(t, elapsed, nativeReferencePriceTimeout+2*time.Second)
	require.Nil(t, depositInfoAsset(t, info, ChainEthereum, "ETH").ReferencePriceUSD)
}

// No configured feed at all is the default for a deployment that never set
// one: the page still renders, assets still declare their pricing mode.
func TestAttachNativeReferencePricesWithoutFeed(t *testing.T) {
	s := &Service{}
	info := buildDepositInfo(depositInfoConfig(), depositInfoAssigned())
	s.attachNativeReferencePrices(t.Context(), info)
	require.Nil(t, depositInfoAsset(t, info, ChainEthereum, "ETH").ReferencePriceUSD)
	require.True(t, depositInfoAsset(t, info, ChainEthereum, "ETH").MarketPriced)
}
