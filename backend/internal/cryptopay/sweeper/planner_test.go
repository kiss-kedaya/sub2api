package sweeper

import (
	"context"
	"math/big"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

type fakeReader struct {
	balances map[string][]AddressBalance
}

func (f *fakeReader) UnsweptBalances(_ context.Context, chain string) ([]AddressBalance, error) {
	return f.balances[chain], nil
}

func TestPlannerThresholds(t *testing.T) {
	reader := &fakeReader{balances: map[string][]AddressBalance{
		"bsc": {
			{Chain: "bsc", Address: "0xaaa", Token: "0xusdt", Raw: big.NewInt(5_000_000), Decimals: 6}, // 5 USDT >= 3
			{Chain: "bsc", Address: "0xbbb", Token: "0xusdt", Raw: big.NewInt(1_000_000), Decimals: 6}, // 1 USDT < 3
		},
	}}
	planner := NewPlanner(nil, reader, Config{
		GlobalThreshold: 1000,
		MinPerAddress:   map[string]float64{"bsc": 3},
	}, nil)

	plan, err := planner.BuildPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 5 USDT total is below the 1000 global trigger: entries must be empty.
	if len(plan.Entries) != 0 {
		t.Fatalf("expected empty plan below global threshold, got %d entries", len(plan.Entries))
	}
}

func TestPlannerAboveGlobalThreshold(t *testing.T) {
	reader := &fakeReader{balances: map[string][]AddressBalance{
		"bsc": {
			{Chain: "bsc", Address: "0xaaa", Token: "0xusdt", Raw: big.NewInt(600_000_000), Decimals: 6}, // 600
			{Chain: "bsc", Address: "0xbbb", Token: "0xusdt", Raw: big.NewInt(500_000_000), Decimals: 6}, // 500
			{Chain: "bsc", Address: "0xccc", Token: "0xusdt", Raw: big.NewInt(1_000_000), Decimals: 6},   // 1 < 3
		},
	}}
	planner := NewPlanner(nil, reader, Config{
		GlobalThreshold: 1000,
		MinPerAddress:   map[string]float64{"bsc": 3},
	}, nil)

	plan, err := planner.BuildPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) != 2 {
		t.Fatalf("expected 2 entries above per-address minimum, got %d", len(plan.Entries))
	}
	if plan.TotalUSD != 1100 {
		t.Fatalf("total = %v, want 1100", plan.TotalUSD)
	}
}

func TestPlannerSkipsZeroBalances(t *testing.T) {
	reader := &fakeReader{balances: map[string][]AddressBalance{
		"bsc": {
			{Chain: "bsc", Address: "0xaaa", Token: "0xusdt", Raw: big.NewInt(0), Decimals: 6},
			{Chain: "bsc", Address: "0xbbb", Token: "0xusdt", Raw: nil, Decimals: 6},
		},
	}}
	planner := NewPlanner(nil, reader, Config{
		GlobalThreshold: 1,
		MinPerAddress:   map[string]float64{"bsc": 0.01},
	}, nil)
	plan, err := planner.BuildPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) != 0 {
		t.Fatalf("expected no entries for zero balances, got %d", len(plan.Entries))
	}
}

// The plan shown in the console must say whether a child address still needs
// native gas before the transfer can run. Without this the operator sees
// NeedsGasTopUp=false right up to the failed execution.
type fakeNode struct {
	native   *big.Int
	gasPrice *big.Int
}

func (f *fakeNode) Balance(context.Context, string) (*big.Int, error) { return f.native, nil }
func (f *fakeNode) GasPrice(context.Context) (*big.Int, error)        { return f.gasPrice, nil }

func TestPlannerEstimatesGasTopUp(t *testing.T) {
	reader := &fakeReader{balances: map[string][]AddressBalance{
		"bsc": {
			{Chain: "bsc", Address: "0xempty", Token: "0xusdt", Raw: big.NewInt(5_000_000_000), Decimals: 6},
			{Chain: "bsc", Address: "0xfunded", Token: "0xusdt", Raw: big.NewInt(5_000_000_000), Decimals: 6},
		},
	}}
	price := big.NewInt(1_000_000_000) // 1 gwei
	pools := map[string]*rpc.Pool{"bsc": nil}
	planner := NewPlanner(nil, reader, Config{
		GlobalThreshold: 1,
		MinPerAddress:   map[string]float64{"bsc": 1},
		GasTopUp:        map[string]float64{"bsc": 0.0002},
	}, pools)
	// Inject fake chain reads: the zero-balance address needs a top-up.
	planner.pools = map[string]nodeReader{"bsc": &fakeNode{native: big.NewInt(0), gasPrice: price}}
	plan, err := planner.BuildPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(plan.Entries))
	}
	if !plan.Entries[0].NeedsGasTopUp || plan.Entries[0].GasTopUpAmount == nil {
		t.Fatalf("empty address should need gas: %+v", plan.Entries[0])
	}
	// The preview and execution both fund only this transfer's buffered cost.
	want := transferGasBudget(price)
	if plan.Entries[0].GasTopUpAmount.Cmp(want) != 0 {
		t.Fatalf("top-up = %s, want %s", plan.Entries[0].GasTopUpAmount, want)
	}
}

// A chain-scoped plan must never contain an entry for another chain, even when
// that chain has plenty of sweepable balance. This is the guarantee an operator
// relies on when sweeping BSC while Ethereum has gas_pending jobs.
func TestPlannerChainScopeExcludesOtherChains(t *testing.T) {
	reader := &fakeReader{balances: map[string][]AddressBalance{
		"bsc": {
			{Chain: "bsc", Address: "0xaaa", Token: "0xusdt", Raw: big.NewInt(50_000_000), Decimals: 6},
		},
		"ethereum": {
			{Chain: "ethereum", Address: "0xbbb", Token: "0xusdt", Raw: big.NewInt(50_000_000), Decimals: 6},
		},
		"polygon": {
			{Chain: "polygon", Address: "0xccc", Token: "0xusdt", Raw: big.NewInt(50_000_000), Decimals: 6},
		},
	}}
	planner := NewPlanner(nil, reader, Config{
		MinPerAddress: map[string]float64{"bsc": 1, "ethereum": 1, "polygon": 1},
	}, nil).WithChains([]string{"BSC"})

	plan, err := planner.BuildPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("expected exactly one entry, got %d", len(plan.Entries))
	}
	if plan.Entries[0].Chain != "bsc" {
		t.Fatalf("entry chain = %q, want bsc", plan.Entries[0].Chain)
	}
}

// An empty scope must keep the historical behaviour: every configured chain.
func TestPlannerEmptyScopeKeepsGlobalBehaviour(t *testing.T) {
	reader := &fakeReader{balances: map[string][]AddressBalance{
		"bsc":      {{Chain: "bsc", Address: "0xaaa", Token: "0xusdt", Raw: big.NewInt(50_000_000), Decimals: 6}},
		"ethereum": {{Chain: "ethereum", Address: "0xbbb", Token: "0xusdt", Raw: big.NewInt(50_000_000), Decimals: 6}},
	}}
	planner := NewPlanner(nil, reader, Config{
		MinPerAddress: map[string]float64{"bsc": 1, "ethereum": 1},
	}, nil).WithChains(nil)

	plan, err := planner.BuildPlan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Entries) != 2 {
		t.Fatalf("expected both chains, got %d entries", len(plan.Entries))
	}
}

func TestNormalizeChainScope(t *testing.T) {
	got := normalizeChainScope([]string{" BSC ", "bsc", "Ethereum", ""})
	if len(got) != 2 || got[0] != "bsc" || got[1] != "ethereum" {
		t.Fatalf("normalizeChainScope = %v", got)
	}
	if normalizeChainScope([]string{" ", ""}) != nil {
		t.Fatal("blank input must collapse to nil (global behaviour)")
	}
}
