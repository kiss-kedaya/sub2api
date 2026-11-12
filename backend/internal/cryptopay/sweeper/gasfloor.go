package sweeper

import (
	"fmt"
	"math/big"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// chainGasPriceFloor is the lowest gas price, in wei, that the RPC pool may
// sign with on chain (rpc.GasPriceFloor). It is exported from the rpc package
// because the pool is what signs; the sweeper only needs it to prove that the
// operator's native budget can still pay for one transfer.
func chainGasPriceFloor(chain string) *big.Int {
	return rpc.GasPriceFloor(uint64(ChainIDs[chain]))
}

// GasBudgetCoversGasFloor reports the configuration defect the fee floor can
// expose: a per-chain native budget (gas_topup) that cannot pay for a single
// transfer at that chain's minimum gas price. A high quote is temporary and
// clears up; this never does. Without an early, explicit report every run
// defers the job with "gas quote above the configured budget" and nothing
// says the budget itself is the problem.
//
// A missing budget is a different, already-handled state: it is not a defect
// of the floor, so it is not reported here.
//
// It is exported because the operator configuration is validated where it is
// loaded, not only where a sweep happens to run.
func GasBudgetCoversGasFloor(config Config, chain string) error {
	budget := configuredGasBudget(config, chain)
	if budget.Sign() <= 0 {
		return nil
	}
	floor := chainGasPriceFloor(chain)
	need := transferGasBudget(floor)
	if need.Cmp(budget) <= 0 {
		return nil
	}
	return fmt.Errorf("gas budget %s wei on %s cannot pay one transfer at the chain's minimum gas price %s wei (%s wei needed): raise gas_topup for %s",
		budget.String(), chain, floor.String(), need.String(), chain)
}
