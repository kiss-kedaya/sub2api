package ledger

import (
	"math/big"
	"testing"
)

func TestAmountStringRendering(t *testing.T) {
	tests := []struct {
		raw      int64
		decimals int
		want     string
	}{
		{12345678, 6, "12.345678"},
		{1000000, 6, "1"},
		{1, 6, "0.000001"},
		{150, 0, "150"},
		{0, 6, "0"},
	}
	for _, tt := range tests {
		if got := amountString(big.NewInt(tt.raw), tt.decimals); got != tt.want {
			t.Errorf("amountString(%d, %d) = %q, want %q", tt.raw, tt.decimals, got, tt.want)
		}
	}
}

func TestToDecimal(t *testing.T) {
	if got := toDecimal(big.NewInt(12_345_678), 6); got != 12.345678 {
		t.Fatalf("toDecimal = %v, want 12.345678", got)
	}
}

// A rejected transfer must carry a machine reason so the UI can explain it.
// The 2026-10-14 0.01 USDT probe landed as a bare "ignored" with a raw-unit
// amount; both halves of that bug are pinned here.
func TestAmountStringUsesTokenDecimals(t *testing.T) {
	// 0.01 USDT on an 18-decimal chain is 10^16 raw. Stored at 18 decimals it
	// renders as 0.01, not 1e16.
	if got := amountString(big.NewInt(10_000_000_000_000_000), 18); got != "0.01" {
		t.Fatalf("18-decimal amount = %q, want 0.01", got)
	}
	// The old bug: the same raw value stored at decimals=0 renders as 1e16.
	if got := amountString(big.NewInt(10_000_000_000_000_000), 0); got != "10000000000000000" {
		t.Fatalf("decimals=0 amount = %q", got)
	}
}

func TestReasonTextCoversThresholds(t *testing.T) {
	if got := ReasonText(ReasonBelowMin, 0.001, 10000); got == "" || got == ReasonBelowMin {
		t.Fatalf("below-min reason not rendered: %q", got)
	}
	if got := ReasonText(ReasonAboveMax, 0.001, 10000); got == "" || got == ReasonAboveMax {
		t.Fatalf("above-max reason not rendered: %q", got)
	}
}
