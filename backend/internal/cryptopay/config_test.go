package cryptopay

import "testing"

// The setting repository interface is defined in the service package with a
// concrete *Setting return type, so the fake above cannot satisfy it directly.
// Config persistence is therefore covered at the service layer; here we only
// test the pure normalization logic.
func TestNormalizeConfigDefaults(t *testing.T) {
	cfg, err := NormalizeConfig(CryptoConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Fatal("default config must be disabled")
	}
	if cfg.SweepGlobalThreshold != 1000 {
		t.Fatalf("threshold = %v, want 1000", cfg.SweepGlobalThreshold)
	}
	if cfg.CreditMultiplier != 1.0 {
		t.Fatalf("multiplier = %v, want 1.0", cfg.CreditMultiplier)
	}
	if cfg.Confirmations[ChainBSC] != 5 {
		t.Fatalf("bsc confirmations = %d, want 5", cfg.Confirmations[ChainBSC])
	}
}

func TestNormalizeConfigRejectsUnknownChain(t *testing.T) {
	_, err := NormalizeConfig(CryptoConfig{Chains: []string{"tron"}})
	if err == nil {
		t.Fatal("expected error for unsupported chain")
	}
}

func TestNormalizeConfigRequiresHotWalletsWhenEnabled(t *testing.T) {
	_, err := NormalizeConfig(CryptoConfig{Enabled: true})
	if err == nil {
		t.Fatal("expected error when enabled without hot wallets")
	}
	cfg, err := NormalizeConfig(CryptoConfig{
		Enabled:         true,
		HotWalletEVM:    "0x1111111111111111111111111111111111111111",
		HotWalletSolana: "So11111111111111111111111111111111111111112",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled {
		t.Fatal("expected enabled config to survive normalization")
	}
}

func TestIsEVMChain(t *testing.T) {
	for _, chain := range []string{ChainBSC, ChainPolygon, ChainPlasma} {
		if !IsEVMChain(chain) {
			t.Fatalf("%s should be EVM", chain)
		}
	}
	if IsEVMChain(ChainSolana) {
		t.Fatal("solana must not be EVM")
	}
	if KnownChain("tron") || KnownChain("ethereum") {
		t.Fatal("tron/ethereum must not be supported")
	}
}
