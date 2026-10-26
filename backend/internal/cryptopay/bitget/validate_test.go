package bitget

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func fixtureOrder() (*Order, Intent) {
	in := Intent{Chain: "bsc", ChainID: 56, Index: 7, From: "0x1111111111111111111111111111111111111111", To: "0x2222222222222222222222222222222222222222", Token: "0x55d398326f99059ff775485246999027b3197955", Decimals: 18, Gross: "10000000000000000000", Net: "9950000000000000000", Fee: "50000000000000000", MaxFee: "0.5", MaxFeeBPS: 100}
	o := &Order{ID: "fixture-order", Chain: "bnb", From: in.From, To: in.To, Contract: in.Token, Amount: "9.95"}
	o.Source.Type = "evm_7702"
	o.NoGas.Available = true
	o.NoGas.PayToken = in.Token
	o.NoGas.PayAmount = "0.05"
	o.Source.EVM.Messages = []Message{{Type: "call", Hash: "0x" + repeatHashByte()}}
	return o, in
}
func repeatHashByte() string {
	return "abababababababababababababababababababababababababababababababab"
}

func TestQuoteBindsTokenDestinationAndBothFeeCaps(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Order, *Intent)
	}{
		{"valid", func(*Order, *Intent) {}},
		{"wrong destination", func(o *Order, _ *Intent) { o.To = o.From }},
		{"wrong fee token", func(o *Order, _ *Intent) { o.NoGas.PayToken = o.From }},
		{"oversize absolute fee", func(_ *Order, i *Intent) { i.MaxFee = "0.01" }},
		{"oversize relative fee", func(_ *Order, i *Intent) { i.MaxFeeBPS = 1 }},
		{"fee omitted from gross", func(_ *Order, i *Intent) { i.Gross = i.Net }},
		{"native fallback", func(o *Order, _ *Intent) { o.Source.Type = "evm_1559" }},
		{"override warning", func(o *Order, _ *Intent) { o.NoGas.Warn = "override existing wallet" }},
		{"unsupported chain", func(_ *Order, i *Intent) { i.Chain = "xlayer" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, in := fixtureOrder()
			tc.mutate(o, &in)
			err := ValidateQuote(o, in)
			if tc.name == "valid" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestOpaqueSigningHashesRequireIndependentVendorAttestation(t *testing.T) {
	o, in := fixtureOrder()
	_, err := ValidateSigning(o, in, time.Now())
	require.ErrorIs(t, err, ErrUnsafeOrder)
	// Even syntactically valid caller-supplied typed data cannot turn an
	// unsupported domain into signing authority.
	o.Source.EVM.Messages[0].Data = json.RawMessage(`{"primaryType":"Permit","domain":{},"types":{},"message":{}}`)
	_, err = ValidateSigning(o, in, time.Now())
	require.ErrorIs(t, err, ErrUnsafeOrder)
}

func TestSupportedBitgetChainsAndStablecoinDecimals(t *testing.T) {
	for name, id := range map[string]int64{"bsc": 56, "ethereum": 1, "polygon": 137, "base": 8453, "arbitrum": 42161} {
		_, got := Chain(name)
		require.Equal(t, id, got)
	}
	for _, name := range []string{"plasma", "xlayer", "solana", "morph"} {
		_, got := Chain(name)
		require.Zero(t, got)
	}
	dec, ok := SupportedTokenDecimals("bsc", "0x55d398326f99059ff775485246999027b3197955")
	require.True(t, ok)
	require.Equal(t, 18, dec)
}
