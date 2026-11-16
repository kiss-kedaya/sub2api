package scanner

import "testing"

// The split-retry path must keep the good prefix and only give up when a range
// is a single block; this is the arithmetic it relies on.
func TestHalveRange(t *testing.T) {
	cases := []struct {
		from, to   uint64
		wantA      uint64
		wantB      uint64
		wantHalved bool
	}{
		{from: 1, to: 1, wantHalved: false},
		{from: 1, to: 2, wantA: 1, wantB: 2, wantHalved: true},
		{from: 100, to: 599, wantA: 349, wantB: 350, wantHalved: true},
		{from: 100, to: 101, wantA: 100, wantB: 101, wantHalved: true},
		{from: 5, to: 4, wantHalved: false},
	}
	for _, tc := range cases {
		a, b, ok := halveRange(tc.from, tc.to)
		if ok != tc.wantHalved {
			t.Fatalf("halveRange(%d,%d) ok = %v, want %v", tc.from, tc.to, ok, tc.wantHalved)
		}
		if !ok {
			continue
		}
		if a != tc.wantA || b != tc.wantB {
			t.Fatalf("halveRange(%d,%d) = (%d,%d), want (%d,%d)", tc.from, tc.to, a, b, tc.wantA, tc.wantB)
		}
		// The two halves plus the left one must cover the original range with
		// no gap and no overlap.
		if a < tc.from || b != a+1 || b > tc.to {
			t.Fatalf("halves do not tile the range: (%d,%d) inside (%d,%d)", a, b, tc.from, tc.to)
		}
	}
}
