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
