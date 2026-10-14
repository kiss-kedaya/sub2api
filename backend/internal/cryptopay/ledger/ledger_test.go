package ledger

import (
	"context"
	"math/big"
	"testing"
)

type fakeCreditor struct {
	calls []CreditRequest
}

func (f *fakeCreditor) CreditDeposit(_ context.Context, req CreditRequest) error {
	f.calls = append(f.calls, req)
	return nil
}

type fakeResolver struct {
	owners  map[string]int64 // chain/address(lower) -> userID
	allOwn  map[string]bool  // chain/address(lower) -> ours
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{owners: map[string]int64{}, allOwn: map[string]bool{}}
}

func (f *fakeResolver) ResolveDeposit(_ context.Context, chain, address string) (int64, bool, error) {
	key := chain + "/" + address
	if userID, ok := f.owners[key]; ok {
		return userID, true, nil
	}
	if f.allOwn[key] {
		return 0, true, nil
	}
	return 0, false, nil
}

func (f *fakeResolver) IsOwnAddress(_ context.Context, chain, address string) (bool, error) {
	key := chain + "/" + address
	if _, ok := f.owners[key]; ok {
		return true, nil
	}
	return f.allOwn[key], nil
}

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
