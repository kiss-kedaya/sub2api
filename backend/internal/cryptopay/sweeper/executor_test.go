package sweeper

import (
	"math/big"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
)

func TestAmountString(t *testing.T) {
	tests := []struct {
		raw      int64
		decimals int
		want     string
	}{
		{1_000_000, 6, "1"},
		{1_234_567, 6, "1.234567"},
		{1_000_000_000_000_000_000, 18, "1"},
		{1, 6, "0.000001"},
		{0, 6, "0"},
		{150, 0, "150"},
	}
	for _, tt := range tests {
		if got := amountString(big.NewInt(tt.raw), tt.decimals); got != tt.want {
			t.Errorf("amountString(%d, %d) = %q, want %q", tt.raw, tt.decimals, got, tt.want)
		}
	}
}

func TestDecimalToRawUnits(t *testing.T) {
	if got := decimalToRawUnits(0.002, 18); got.String() != "2000000000000000" {
		t.Fatalf("0.002 @18 = %s, want 2000000000000000", got)
	}
	if got := decimalToRawUnits(1, 6); got.String() != "1000000" {
		t.Fatalf("1 @6 = %s, want 1000000", got)
	}
	if got := decimalToRawUnits(0, 18); got.Sign() != 0 {
		t.Fatalf("0 should be zero, got %s", got)
	}
}

func TestGasTopUpAmountCoversShortfall(t *testing.T) {
	exec := &Executor{config: Config{GasTopUp: map[string]float64{"bsc": 0.002}}}
	// Shortfall of 1000 wei, configured 0.002 BNB is far larger: use config.
	got := exec.gasTopUpAmount("bsc", big.NewInt(5000), big.NewInt(4000))
	want := decimalToRawUnits(0.002, 18)
	if got.Cmp(want) != 0 {
		t.Fatalf("expected configured top-up %s, got %s", want, got)
	}
	// Shortfall larger than the configured value: must cover the need.
	hugeBudget := new(big.Int).Exp(big.NewInt(10), big.NewInt(20), nil) // 100 BNB
	got = exec.gasTopUpAmount("bsc", hugeBudget, big.NewInt(0))
	if got.Cmp(hugeBudget) < 0 {
		t.Fatalf("top-up %s must cover the %s shortfall", got, hugeBudget)
	}
}

func TestChainIDsCoverSweepChains(t *testing.T) {
	for _, chain := range []string{"bsc", "polygon", "plasma"} {
		if ChainIDs[chain] == 0 {
			t.Fatalf("missing chain id for %s", chain)
		}
	}
}

func TestSweepEntrySkipsChainWithoutToken(t *testing.T) {
	exec := &Executor{
		tokens: map[string]TokenSpec{"plasma": {Contract: "", Decimals: 6}},
		pools:  map[string]*rpc.Pool{},
	}
	out := exec.sweepOne(t.Context(), PlanEntry{Chain: "plasma", Address: "0x1", RawAmount: big.NewInt(1)}, false)
	if out.Status != "failed" {
		t.Fatalf("expected failed for tokenless chain, got %q", out.Status)
	}
}
