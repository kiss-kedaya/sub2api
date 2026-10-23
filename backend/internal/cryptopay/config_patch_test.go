package cryptopay

import "testing"

// A payload that omits keys must keep the stored values: an older console or
// a scripted PUT previously wiped rpc/other keys to zero values, which
// crashed the admin page on the next load.
func TestCryptoConfigPatchKeepsOmittedFields(t *testing.T) {
	base := CryptoConfig{
		Enabled:      true,
		Chains:       []string{ChainBSC, ChainPolygon},
		RPC:          map[string]string{ChainBSC: "https://rpc.example"},
		HotWalletEVM: "0x1111111111111111111111111111111111111111",
		SweepAuto:    true,
		HotIndex:     3,
	}
	// Patch only the EVM hot wallet, as the console does when clearing it.
	empty := ""
	merged := CryptoConfigPatch{HotWalletEVM: &empty}.Apply(base)

	if merged.HotWalletEVM != "" {
		t.Fatalf("hot wallet = %q, want cleared", merged.HotWalletEVM)
	}
	if len(merged.Chains) != 2 {
		t.Fatalf("chains = %v, want them preserved", merged.Chains)
	}
	if merged.RPC[ChainBSC] != "https://rpc.example" {
		t.Fatalf("rpc = %v, want it preserved", merged.RPC)
	}
	if !merged.SweepAuto || merged.HotIndex != 3 {
		t.Fatalf("sweep_auto/hot_index not preserved: %+v", merged)
	}
}

// An empty patch changes nothing.
func TestCryptoConfigPatchEmptyIsIdentity(t *testing.T) {
	base := CryptoConfig{Enabled: true, Chains: []string{ChainBSC}}
	merged := CryptoConfigPatch{}.Apply(base)
	if merged.Enabled != base.Enabled || len(merged.Chains) != 1 {
		t.Fatalf("empty patch changed the config: %+v", merged)
	}
}
