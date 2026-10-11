package sweeper

import (
	"context"
	"math/big"
	"testing"
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
			{Chain: "bsc", Address: "0xaaa", Token: "0xusdt", Raw: big.NewInt(5_000_000), Decimals: 6},  // 5 USDT >= 3
			{Chain: "bsc", Address: "0xbbb", Token: "0xusdt", Raw: big.NewInt(1_000_000), Decimals: 6},  // 1 USDT < 3
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
