package cryptopay

import "testing"

// TestDefaultNativeScanUsesBlocksEveryTenSeconds pins the operator-chosen
// defaults: the per-block scan is the default native source and the native poll
// interval is ten seconds. The address-based txlist path stays selectable.
func TestDefaultNativeScanUsesBlocksEveryTenSeconds(t *testing.T) {
	cfg := DefaultCryptoConfig()
	if cfg.NativeScan != NativeScanBlocks {
		t.Fatalf("default native_scan = %q, want %q", cfg.NativeScan, NativeScanBlocks)
	}
	if cfg.NativePollSeconds != 10 || DefaultNativePollSeconds != 10 {
		t.Fatalf("default native poll = %d (const %d), want 10", cfg.NativePollSeconds, DefaultNativePollSeconds)
	}

	// A config stored before this field existed has neither key: normalization
	// must land on the same defaults instead of keeping the old txlist/30 pair.
	stored, err := NormalizeConfig(CryptoConfig{Chains: []string{ChainBSC}})
	if err != nil {
		t.Fatal(err)
	}
	if stored.NativeScan != NativeScanBlocks || stored.NativePollSeconds != 10 {
		t.Fatalf("normalized legacy config = %q/%d, want %q/10",
			stored.NativeScan, stored.NativePollSeconds, NativeScanBlocks)
	}

	// txlist is still an accepted operator choice with an explicit interval.
	optIn := DefaultCryptoConfig()
	optIn.NativeScan = NativeScanTxList
	optIn.NativePollSeconds = 30
	normalized, err := NormalizeConfig(optIn)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.NativeScan != NativeScanTxList || normalized.NativePollSeconds != 30 {
		t.Fatalf("txlist opt-in = %q/%d, want %q/30",
			normalized.NativeScan, normalized.NativePollSeconds, NativeScanTxList)
	}
}
