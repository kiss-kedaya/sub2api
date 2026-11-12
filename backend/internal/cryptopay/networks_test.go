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
			if token.Decimals != 6 && !(chain == ChainBSC && token.Decimals == 18) {
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
