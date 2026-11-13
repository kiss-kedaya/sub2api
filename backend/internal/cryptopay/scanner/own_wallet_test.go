package scanner

import (
	"context"
	"errors"
	"testing"
)

// A gas top-up we send to a deposit address is our own money moving. Reading it
// back as a deposit credits the user for BNB/ETH we funded ourselves and leaves
// a phantom pending row in their deposit list.
func TestIsOwnTransfer(t *testing.T) {
	own := map[string]struct{}{"0xgas": {}}
	cases := []struct {
		name   string
		from   string
		native bool
		want   bool
	}{
		{"native from our gas wallet", "0xgas", true, true},
		{"native from our gas wallet, mixed case", "0xGAS", true, true},
		{"native from a user", "0xuser", true, false},
		{"token from our wallet is not a gas top-up", "0xgas", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transfer := Transfer{From: tc.from, Native: tc.native}
			if got := isOwnTransfer(own, transfer); got != tc.want {
				t.Fatalf("isOwnTransfer(from=%s native=%v) = %v, want %v", tc.from, tc.native, got, tc.want)
			}
		})
	}
	if isOwnTransfer(nil, Transfer{From: "0xgas", Native: true}) {
		t.Fatal("an empty wallet set must not drop transfers")
	}
}

func TestOwnAddressSetNormalizesAndDegrades(t *testing.T) {
	w := &Worker{}
	if got := w.ownAddressSet(context.Background(), "bsc"); got != nil {
		t.Fatalf("without a provider the set must be empty, got %v", got)
	}
	w.SetOwnWallets(func(context.Context, string) ([]string, error) {
		return []string{" 0xABC ", "", "0xdef"}, nil
	})
	got := w.ownAddressSet(context.Background(), "bsc")
	for _, want := range []string{"0xabc", "0xdef"} {
		if _, ok := got[want]; !ok {
			t.Fatalf("normalized set is missing %s: %v", want, got)
		}
	}
	if _, ok := got[""]; ok {
		t.Fatal("blank addresses must not enter the set")
	}

	// A failing lookup must not stop the scan: the next pass retries.
	w.SetOwnWallets(func(context.Context, string) ([]string, error) {
		return nil, errors.New("address store unavailable")
	})
	if got := w.ownAddressSet(context.Background(), "bsc"); got != nil {
		t.Fatalf("a failed lookup must degrade to no exclusion, got %v", got)
	}
}
