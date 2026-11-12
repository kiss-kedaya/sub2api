package cryptopay

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
	"github.com/ethereum/go-ethereum/common"
)

func TestDepositNetworksHaveVerifiedTokenAndAddressDefinitions(t *testing.T) {
	for _, chain := range []string{ChainEthereum, ChainBSC, ChainPolygon, ChainPlasma, ChainBase, ChainXLayer} {
		family, err := addresses.FamilyForChain(chain)
		if err != nil {
			t.Fatal(err)
		}
		if len(watchedTokens[chain]) == 0 {
			t.Fatalf("%s has no allowed assets", chain)
		}
		for _, token := range watchedTokens[chain] {
			if token.Symbol == "" {
				t.Fatalf("asset without a symbol: %+v", token)
			}
			if family != addresses.FamilyEVM || sweeper.ChainIDs[chain] <= 0 || chainlistIDs[chain] <= 0 {
				t.Fatalf("%s has inconsistent scanner/probe/signing settings", chain)
			}
			if token.Native {
				// A mainnet coin has no contract, so it must never be mistaken
				// for an ERC-20: the sentinel, not a hex address, identifies it.
				if token.Contract != scanner.NativeToken {
					t.Fatalf("%s native asset must use the native sentinel: %+v", chain, token)
				}
				if token.Decimals != 18 {
					t.Fatalf("%s native asset must have 18 decimals: %+v", chain, token)
				}
				if common.IsHexAddress(token.Contract) {
					t.Fatalf("%s native asset must not carry a contract address", chain)
				}
				continue
			}
			// BSC's BEP-20 stablecoins are 18-decimal; every other watched
			// token uses 6. Both are asserted explicitly because a wrong
			// exponent moves a deposit by twelve orders of magnitude.
			plausibleDecimals := token.Decimals == 6 || (chain == ChainBSC && token.Decimals == 18)
			if !plausibleDecimals {
				t.Fatalf("incorrect stablecoin precision: %+v", token)
			}
			if !common.IsHexAddress(token.Contract) || common.HexToAddress(token.Contract) == (common.Address{}) {
				t.Fatalf("%s token has an invalid contract: %+v", chain, token)
			}
		}
	}
	if tokenSymbol(ChainXLayer, "0xb6ceceab302e2e4948951ee7843fc24e92933061") != "USDC" || tokenSymbol(ChainXLayer, "0x74b7f16337b8972027f6196a17a631ac6de26d22") != "USDC.e" {
		t.Fatal("native and bridged USDC were confused")
	}
}

func TestNativeAssetsAreOnlyTheMainnetCoins(t *testing.T) {
	cases := map[string]string{
		ChainEthereum: "ETH",
		ChainBSC:      "BNB",
	}
	for chain, symbol := range cases {
		token, ok := nativeToken(chain)
		if !ok {
			t.Fatalf("%s must support its mainnet coin", chain)
		}
		if token.Symbol != symbol {
			t.Fatalf("%s native symbol = %q, want %q", chain, token.Symbol, symbol)
		}
		if tokenSymbol(chain, scanner.NativeToken) != symbol {
			t.Fatalf("%s tokenSymbol(native) = %q, want %q", chain, tokenSymbol(chain, scanner.NativeToken), symbol)
		}
		if decimals := tokenDecimals(chain, scanner.NativeToken); decimals != 18 {
			t.Fatalf("%s native decimals = %d, want 18", chain, decimals)
		}
	}
	// MATIC/POL is deliberately out of scope until the operator asks for it,
	// and every other chain must keep its previous token-only asset list.
	for _, chain := range []string{ChainPolygon, ChainPlasma, ChainArbitrum, ChainBase, ChainXLayer} {
		if _, ok := nativeToken(chain); ok {
			t.Fatalf("%s must not expose a native coin yet", chain)
		}
	}
}

func TestNativeCoinNeverReachesEthGetLogsFilters(t *testing.T) {
	for _, chain := range evmChains {
		contracts := evmContracts(chain)
		if len(contracts) == 0 {
			t.Fatalf("%s has no ERC-20 contracts", chain)
		}
		// The scanner probes and getLogs filters index this list directly; a
		// non-hex sentinel there would make every query invalid.
		for _, contract := range contracts {
			if contract == scanner.NativeToken || !common.IsHexAddress(contract) {
				t.Fatalf("%s leaked a non-ERC-20 entry into the contract list: %q", chain, contract)
			}
		}
		if len(contracts) != len(watchedTokens[chain])-nativeCount(chain) {
			t.Fatalf("%s contract list and asset table disagree", chain)
		}
	}
}

func nativeCount(chain string) int {
	count := 0
	for _, token := range watchedTokens[chain] {
		if token.Native {
			count++
		}
	}
	return count
}

// The executor decides "value transfer or ERC-20 transfer" from this map, so a
// missing Native flag would silently disable native sweeping.
func TestSweepSpecsCarryTheNativeFlag(t *testing.T) {
	specs := sweepTokenSpecs()
	for chain, symbol := range map[string]string{ChainEthereum: "ETH", ChainBSC: "BNB"} {
		spec, ok := specs[tokenDecimalsKey(chain, scanner.NativeToken)]
		if !ok {
			t.Fatalf("%s native asset is missing from the sweep specs", chain)
		}
		if !spec.Native || spec.Decimals != 18 || spec.Contract != scanner.NativeToken {
			t.Fatalf("%s native spec = %+v (want Native/18/%s)", chain, spec, symbol)
		}
	}
	// Stablecoins must stay non-native, and must keep their own decimals.
	for _, chain := range []string{ChainEthereum, ChainBSC, ChainPolygon} {
		for _, token := range watchedTokens[chain] {
			if token.Native {
				continue
			}
			spec, ok := specs[tokenDecimalsKey(chain, token.Contract)]
			if !ok || spec.Native || spec.Decimals != token.Decimals || spec.Contract != token.Contract {
				t.Fatalf("%s stablecoin spec regressed: %+v", chain, spec)
			}
		}
	}
	if len(specs) != lenAllWatchedAssets() {
		t.Fatalf("sweep specs = %d entries, want %d", len(specs), lenAllWatchedAssets())
	}
}

// The balance reader rebuilds raw amounts with this map: a native row resolved
// through the stablecoin fallback would be 1e12 too small.
func TestSweepDecimalsIncludeNativeAt18(t *testing.T) {
	decimals := sweepDecimalsByAsset()
	for _, chain := range []string{ChainEthereum, ChainBSC} {
		if got := decimals[tokenDecimalsKey(chain, scanner.NativeToken)]; got != 18 {
			t.Fatalf("%s native decimals = %d, want 18", chain, got)
		}
	}
	if got := decimals[tokenDecimalsKey(ChainEthereum, "0xdac17f958d2ee523a2206206994597c13d831ec7")]; got != 6 {
		t.Fatalf("ethereum USDT decimals = %d, want 6", got)
	}
	if got := decimals[tokenDecimalsKey(ChainBSC, "0x55d398326f99059ff775485246999027b3197955")]; got != 18 {
		t.Fatalf("bsc USDT decimals = %d, want 18", got)
	}
}

func lenAllWatchedAssets() int {
	total := 0
	for _, tokens := range watchedTokens {
		total += len(tokens)
	}
	return total
}

// The scanner only sees a native deposit when Native/NativeDecimals are set,
// and an eth_getLogs filter breaks if the sentinel leaks into Tokens.
func TestChainScanConfigWiresNativeScanning(t *testing.T) {
	for chain, want := range map[string]struct {
		native   bool
		symbol   string
		contract int
	}{
		ChainEthereum: {native: true, symbol: "ETH", contract: 2},
		ChainBSC:      {native: true, symbol: "BNB", contract: 2},
		ChainPolygon:  {native: false, contract: 2},
	} {
		config, ok := chainScanConfig(chain, 7, 1234)
		if !ok {
			t.Fatalf("%s produced no scan config", chain)
		}
		if config.Native != want.native {
			t.Fatalf("%s Native = %v, want %v", chain, config.Native, want.native)
		}
		if config.Confirmations != 7 || config.MaxRange != 1234 {
			t.Fatalf("%s scan config dropped chain settings: %+v", chain, config)
		}
		if len(config.Tokens) != want.contract {
			t.Fatalf("%s watched %d contracts, want %d", chain, len(config.Tokens), want.contract)
		}
		for _, contract := range config.Tokens {
			if contract == scanner.NativeToken {
				t.Fatalf("%s put the native sentinel into the eth_getLogs filter", chain)
			}
		}
		if !want.native {
			if config.NativeDecimals != 0 {
				t.Fatalf("%s set native decimals without a native asset", chain)
			}
			continue
		}
		if config.NativeDecimals != 18 {
			t.Fatalf("%s NativeDecimals = %d, want 18", chain, config.NativeDecimals)
		}
		if config.Decimals[scanner.NativeToken] != 18 {
			t.Fatalf("%s scanner cannot resolve the native exponent", chain)
		}
		if token, ok := nativeToken(chain); !ok || token.Symbol != want.symbol {
			t.Fatalf("%s native symbol = %q, want %q", chain, token.Symbol, want.symbol)
		}
	}
}
