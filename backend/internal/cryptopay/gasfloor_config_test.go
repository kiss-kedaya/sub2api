package cryptopay

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
)

// TestDefaultGasBudgetsCoverEveryChainFloor pins the relationship the deferral
// rule depends on: one transfer at a chain's minimum gas price must fit inside
// that chain's default native budget. When it does not, every sweep job on the
// chain defers with "gas quote above the configured budget" on every run and
// nothing says the budget itself is wrong.
func TestDefaultGasBudgetsCoverEveryChainFloor(t *testing.T) {
	cfg := DefaultCryptoConfig()
	config := sweeper.Config{GasTopUp: cfg.GasTopUp}
	for _, chain := range evmChains {
		id, ok := chainlistIDs[chain]
		if !ok || id <= 0 {
			t.Fatalf("chain %s has no EIP-155 id, so it has no gas price floor", chain)
		}
		if floor := rpc.GasPriceFloor(uint64(id)); floor.Sign() <= 0 {
			t.Fatalf("chain %s has no positive gas price floor", chain)
		}
		if _, ok := cfg.GasTopUp[chain]; !ok {
			t.Fatalf("chain %s has no default gas_topup to check", chain)
		}
		if err := sweeper.GasBudgetCoversGasFloor(config, chain); err != nil {
			t.Errorf("%s: %v", chain, err)
		}
	}
}
