package cryptopay

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
	"github.com/ethereum/go-ethereum/common"
)

func TestDepositNetworksHaveVerifiedTokenAndAddressDefinitions(t *testing.T) {
	for _, chain := range []string{ChainEthereum, ChainPolygon, ChainPlasma, ChainBase, ChainXLayer} {
		family, err := addresses.FamilyForChain(chain)
		if err != nil {
			t.Fatal(err)
		}
		if len(watchedTokens[chain]) == 0 {
			t.Fatalf("%s has no allowed assets", chain)
		}
		for _, token := range watchedTokens[chain] {
			if token.Symbol == "" || token.Decimals != 6 {
				t.Fatalf("incorrect stablecoin precision: %+v", token)
			}
			if family != addresses.FamilyEVM || !common.IsHexAddress(token.Contract) || sweeper.ChainIDs[chain] <= 0 || chainlistIDs[chain] <= 0 {
				t.Fatalf("%s has inconsistent scanner/probe/signing settings", chain)
			}
		}
	}
	if tokenSymbol(ChainXLayer, "0xb6ceceab302e2e4948951ee7843fc24e92933061") != "USDC" || tokenSymbol(ChainXLayer, "0x74b7f16337b8972027f6196a17a631ac6de26d22") != "USDC.e" {
		t.Fatal("native and bridged USDC were confused")
	}
}
