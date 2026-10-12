// Package sweeper plans and (later) executes address sweeps.
//
// Phase 4 scope: threshold evaluation and plan generation. Execution and
// gas top-up follow once the signer wiring lands.
package sweeper

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

// Config carries the sweep thresholds (admin-configurable).
type Config struct {
	GlobalThreshold float64            // total USD-equivalent to trigger a run
	MinPerAddress   map[string]float64 // chain -> min amount per address
	GasTopUp        map[string]float64 // chain -> native gas amount per top-up
	HotWalletEVM    string
	HotWalletSolana string
}

// PlanEntry is one address to sweep.
type PlanEntry struct {
	Chain           string
	Address         string
	DerivationIndex uint32 // signer index that owns Address
	Token           string
	RawAmount       *big.Int
	AmountDecimal   float64
	Decimals        int
	NeedsGasTopUp   bool
	GasTopUpAmount  *big.Int
}

// Plan is the full sweep plan for one run.
type Plan struct {
	Entries  []PlanEntry
	TotalUSD float64
	DryRun   bool
}

// AddressBalance is one (address, token) balance snapshot.
type AddressBalance struct {
	Chain           string
	Address         string
	DerivationIndex uint32
	Token           string
	Raw             *big.Int
	Decimals        int
}

// BalanceReader returns un-swept balances per chain. Implemented over the
// deposit ledger (credited-but-unswept totals); tests substitute a fake.
type BalanceReader interface {
	// UnsweptBalances returns (address, token, raw) balances that have been
	// credited but not yet swept, for one chain.
	UnsweptBalances(ctx context.Context, chain string) ([]AddressBalance, error)
}

// Planner builds sweep plans from balances and thresholds.
type Planner struct {
	db      *sql.DB
	reader  BalanceReader
	config  Config
	pools   map[string]*rpc.Pool
}

func NewPlanner(db *sql.DB, reader BalanceReader, config Config, pools map[string]*rpc.Pool) *Planner {
	return &Planner{db: db, reader: reader, config: config, pools: pools}
}

// BuildPlan evaluates thresholds and returns the sweep plan for one run.
// Nothing is executed here; dry-run is implicit (execution is separate).
func (p *Planner) BuildPlan(ctx context.Context) (*Plan, error) {
	plan := &Plan{DryRun: true}
	for chain, minAmount := range p.config.MinPerAddress {
		balances, err := p.reader.UnsweptBalances(ctx, chain)
		if err != nil {
			return nil, fmt.Errorf("unswept balances %s: %w", chain, err)
		}
		for _, balance := range balances {
			if balance.Raw == nil || balance.Raw.Sign() <= 0 {
				continue
			}
			amountDecimal := toDecimal(balance.Raw, balance.Decimals)
			if amountDecimal < minAmount {
				continue
			}
			plan.Entries = append(plan.Entries, PlanEntry{
				Chain:           chain,
				Address:         balance.Address,
				DerivationIndex: balance.DerivationIndex,
				Token:           balance.Token,
				RawAmount:       balance.Raw,
				AmountDecimal:   amountDecimal,
				Decimals:        balance.Decimals,
			})
			plan.TotalUSD += amountDecimal // stablecoin ~= USD
		}
	}
	if plan.TotalUSD < p.config.GlobalThreshold {
		// Below the global trigger: report the would-be plan but mark it
		// empty so nothing executes.
		plan.Entries = nil
	}
	return plan, nil
}

// toDecimal converts raw units to a float with the token's decimals.
func toDecimal(raw *big.Int, decimals int) float64 {
	value := new(big.Float).SetInt(raw)
	divisor := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	value.Quo(value, divisor)
	out, _ := value.Float64()
	return out
}
