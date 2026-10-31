// Package sweeper plans and (later) executes address sweeps.
//
// Phase 4 scope: threshold evaluation and plan generation. Execution and
// gas top-up are handled by the durable executor.
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
	// Confirmations is the required block depth (head - receipt block), per chain.
	// Execution fails closed when a chain is missing or zero. Wire from the
	// same confirmation configuration used by the deposit scanner.
	Confirmations map[string]uint64
}

// PlanEntry is one address to sweep.
type PlanEntry struct {
	Chain           string
	Address         string
	DerivationIndex uint32 // wallet derivation index that owns Address
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

// CurrentBalanceReader supplies a fresh credited-minus-swept ledger balance,
// including an address reserved by an unfinished job. It never treats extra
// on-chain funds as authorization to sweep uncredited deposits.
type CurrentBalanceReader interface {
	CurrentBalance(ctx context.Context, chain, address, token string) (*AddressBalance, error)
}

// Planner builds sweep plans from balances and thresholds.
type Planner struct {
	db     *sql.DB
	reader BalanceReader
	config Config
	pools  map[string]nodeReader
}

func NewPlanner(db *sql.DB, reader BalanceReader, config Config, pools map[string]*rpc.Pool) *Planner {
	readers := make(map[string]nodeReader, len(pools))
	for chain, pool := range pools {
		readers[chain] = pool
	}
	return &Planner{db: db, reader: reader, config: config, pools: readers}
}

// BuildManualPlan previews an operator-requested sweep. The global threshold
// controls automatic scheduling; per-address minimums still apply.
func (p *Planner) BuildManualPlan(ctx context.Context) (*Plan, error) {
	manual := *p
	manual.config.GlobalThreshold = 0
	return manual.BuildPlan(ctx)
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
		// One gas quote per chain per run: asking the node once per address
		// burns the public RPC budget and is what made whole runs fail with
		// "all rpc endpoints failed". A failed quote is not retried per address.
		var gasPrice *big.Int
		if pool := p.pools[chain]; pool != nil {
			if quote, err := pool.GasPrice(ctx); err == nil {
				gasPrice = quote
			}
		}
		for _, balance := range balances {
			if balance.Raw == nil || balance.Raw.Sign() <= 0 {
				continue
			}
			amountDecimal := toDecimal(balance.Raw, balance.Decimals)
			if amountDecimal < minAmount {
				continue
			}
			entry := PlanEntry{
				Chain:           chain,
				Address:         balance.Address,
				DerivationIndex: balance.DerivationIndex,
				Token:           balance.Token,
				RawAmount:       new(big.Int).Set(balance.Raw),
				AmountDecimal:   amountDecimal,
				Decimals:        balance.Decimals,
			}
			p.estimateGas(ctx, chain, &entry, gasPrice)
			plan.Entries = append(plan.Entries, entry)
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

// estimateGas fills NeedsGasTopUp/GasTopUpAmount without moving funds: it
// asks the chain for the child's native balance and the current gas price,
// then uses the same budget rule as the executor. A chain that cannot be
// read leaves the fields at their zero values; the executor re-checks at
// execution time (the live balance may have changed).
func (p *Planner) estimateGas(ctx context.Context, chain string, entry *PlanEntry, gasPrice *big.Int) {
	pool := p.pools[chain]
	if pool == nil || gasPrice == nil {
		return
	}
	budget := transferGasBudget(gasPrice)
	native, err := pool.Balance(ctx, entry.Address)
	if err != nil {
		return
	}
	if native.Cmp(budget) >= 0 {
		return
	}
	entry.NeedsGasTopUp = true
	entry.GasTopUpAmount = gasTopUpAmount(p.config, chain, budget, native)
}

// toDecimal converts raw units to a float with the token's decimals.
func toDecimal(raw *big.Int, decimals int) float64 {
	value := new(big.Float).SetInt(raw)
	divisor := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	value.Quo(value, divisor)
	out, _ := value.Float64()
	return out
}
