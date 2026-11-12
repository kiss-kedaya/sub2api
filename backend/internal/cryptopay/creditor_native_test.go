package cryptopay

import (
	"math/big"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/ent/paymentorder"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/stretchr/testify/require"
)

// A native order must carry the settlement basis: which price, observed when,
// and from which feed. The order snapshot is what an operator reads later.
func TestNativeCreditFreezesSettlementPriceInOrderSnapshot(t *testing.T) {
	c, req := newCreditTest(t)
	req.Token = scanner.NativeToken
	req.Amount = big.NewInt(1_000_000_000_000_000_000)
	req.Decimals = 18
	req.CreditUSDT = 13400
	req.NativePriceUSD = "2000"
	req.NativePriceAt = time.Date(2026, 11, 12, 9, 30, 0, 0, time.UTC)
	req.NativePriceSource = "api.binance.com:ETHUSDT"

	require.NoError(t, c.CreditDeposit(t.Context(), req))

	_, outTradeNo := depositOrderIdentity(req.Chain, req.TxHash, req.LogIndex)
	order, err := c.entClient.PaymentOrder.Query().Where(paymentorder.OutTradeNoEQ(outTradeNo)).Only(t.Context())
	require.NoError(t, err)
	require.Equal(t, float64(13400), order.Amount)
	require.Equal(t, "2000", order.ProviderSnapshot["native_price_usd"])
	require.Equal(t, "2026-11-12T09:30:00Z", order.ProviderSnapshot["native_price_at"])
	require.Equal(t, "api.binance.com:ETHUSDT", order.ProviderSnapshot["native_price_source"])
	require.Equal(t, scanner.NativeToken, order.ProviderSnapshot["token"])
}

// Retrying a native deposit whose order already exists must resume the frozen
// amount without asking for a new quote. A price-less retry is exactly what a
// replay after a crash looks like.
func TestNativeResumeUsesFrozenOrderWithoutRepricing(t *testing.T) {
	c, req := newCreditTest(t)
	req.Token = scanner.NativeToken
	req.Amount = big.NewInt(1_000_000_000_000_000_000)
	req.Decimals = 18
	req.CreditUSDT = 13400
	req.NativePriceUSD = "2000"
	req.NativePriceAt = time.Now().UTC()
	req.NativePriceSource = "stub"
	require.NoError(t, c.CreditDeposit(t.Context(), req))

	retry := req
	retry.CreditUSDT = 1 // a changed multiplier/price must not be applied
	retry.NativePriceUSD = ""
	retry.NativePriceAt = time.Time{}
	retry.NativePriceSource = ""
	require.NoError(t, c.CreditDeposit(t.Context(), retry))

	_, outTradeNo := depositOrderIdentity(req.Chain, req.TxHash, req.LogIndex)
	order, err := c.entClient.PaymentOrder.Query().Where(paymentorder.OutTradeNoEQ(outTradeNo)).Only(t.Context())
	require.NoError(t, err)
	require.Equal(t, float64(13400), order.Amount, "the frozen order amount must survive a price-less replay")
	require.Equal(t, "2000", order.ProviderSnapshot["native_price_usd"])
}

// No settlement price means no settlement: the ledger must never fall back to
// the asset-independent multiplier for a native deposit.
func TestNativeCreditWithoutSettlementPriceFailsClosed(t *testing.T) {
	c, req := newCreditTest(t)
	req.Token = scanner.NativeToken
	req.Amount = big.NewInt(1_000_000_000_000_000_000)
	req.Decimals = 18
	req.CreditUSDT = 67

	err := c.CreditDeposit(t.Context(), req)
	require.Error(t, err)

	_, outTradeNo := depositOrderIdentity(req.Chain, req.TxHash, req.LogIndex)
	count, err := c.entClient.PaymentOrder.Query().Where(paymentorder.OutTradeNoEQ(outTradeNo)).Count(t.Context())
	require.NoError(t, err)
	require.Zero(t, count, "a native deposit without a price must not create an order")
}
