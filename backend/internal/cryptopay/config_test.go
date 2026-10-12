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
	if cfg.CreditMultiplier != 6.7 {
		t.Fatalf("multiplier = %v, want 6.7", cfg.CreditMultiplier)
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

// Enabling the channel without a hot wallet is a valid state: deposits,
// scanning and crediting all work. Only a real sweep must refuse.
func TestNormalizeConfigAllowsEmptyHotWallets(t *testing.T) {
	cfg, err := NormalizeConfig(CryptoConfig{Enabled: true})
	if err != nil {
		t.Fatalf("empty hot wallets must normalize, got %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("expected enabled config to survive normalization")
	}
}

// The sweep gate is what protects funds: no destination, no sweep.
// A config stored before the rpc key existed reads back as null; the admin
// console indexes that map directly, so it must never stay nil.
func TestNormalizeConfigBackfillsNilRPC(t *testing.T) {
	cfg, err := NormalizeConfig(CryptoConfig{RPC: nil})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RPC == nil {
		t.Fatal("rpc must be backfilled to an empty map, not left nil")
	}
	if len(cfg.RPC) != 0 {
		t.Fatalf("rpc = %v, want empty", cfg.RPC)
	}
}

func TestValidateSweepReady(t *testing.T) {
	tests := []struct {
		name    string
		hot     string
		wantErr bool
	}{
		{"configured", "0x1111111111111111111111111111111111111111", false},
		{"empty", "", true},
		{"whitespace", "   ", true},
		{"zero address", "0x0000000000000000000000000000000000000000", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CryptoConfig{HotWalletEVM: tt.hot}.ValidateSweepReady()
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateSweepReady(%q) err = %v, wantErr %v", tt.hot, err, tt.wantErr)
			}
		})
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
