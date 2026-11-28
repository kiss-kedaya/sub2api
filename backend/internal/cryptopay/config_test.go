package cryptopay

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

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
	if cfg.Confirmations[ChainBSC] != 3 {
		t.Fatalf("bsc confirmations = %d, want 3", cfg.Confirmations[ChainBSC])
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
		{"bare prefix", "0x", true},
		{"truncated", "0x1234", true},
		{"non hex", "0xzz11111111111111111111111111111111111111", true},
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

// A negative confirmation/scan range used to wrap when cast to uint64 and
// stopped the chain from ever scanning; NormalizeConfig must clamp them.
func TestNormalizeConfigClampsRanges(t *testing.T) {
	cfg, err := NormalizeConfig(CryptoConfig{
		Chains:        []string{ChainBSC},
		Confirmations: map[string]int{ChainBSC: -5},
		ScanRange:     map[string]int{ChainBSC: -1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Confirmations[ChainBSC] != 0 {
		t.Fatalf("confirmations = %d, want 0", cfg.Confirmations[ChainBSC])
	}
	if cfg.ScanRange[ChainBSC] != 1 {
		t.Fatalf("scan range = %d, want 1", cfg.ScanRange[ChainBSC])
	}
}

// The console saves the whole config object; a malformed hot wallet must be
// rejected instead of being silently padded by common.HexToAddress.
func TestNormalizeConfigRejectsMalformedHotWallet(t *testing.T) {
	for _, hot := range []string{"0x", "0x1234", "not-an-address"} {
		if _, err := NormalizeConfig(CryptoConfig{
			Chains:       []string{ChainBSC},
			HotWalletEVM: hot,
		}); err == nil {
			t.Fatalf("hot wallet %q should be rejected", hot)
		}
	}
}

func TestIsEVMChain(t *testing.T) {
	for _, chain := range []string{ChainBSC, ChainEthereum, ChainPolygon, ChainPlasma, ChainArbitrum, ChainBase, ChainXLayer} {
		if !IsEVMChain(chain) {
			t.Fatalf("%s should be EVM", chain)
		}
	}
	if IsEVMChain(ChainSolana) {
		t.Fatal("solana must not be EVM")
	}
	if KnownChain("tron") || !KnownChain(ChainEthereum) || !KnownChain(ChainSolana) {
		t.Fatal("supported deposit networks are inconsistent")
	}
}

func TestNormalizeConfigSupportsAllRequestedNetworks(t *testing.T) {
	chains := []string{ChainEthereum, ChainPolygon, ChainPlasma, ChainBase, ChainXLayer, ChainEthereum}
	cfg, err := NormalizeConfig(CryptoConfig{Enabled: true, Chains: chains,
		Confirmations: map[string]int{}, ScanRange: map[string]int{}, GasTopUp: map[string]float64{}, SweepMinPerAddress: map[string]float64{}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Chains) != 5 {
		t.Fatal("duplicate chain was retained")
	}
	for _, chain := range cfg.Chains {
		if cfg.Confirmations[chain] <= 0 || cfg.ScanRange[chain] <= 0 || cfg.SweepMinPerAddress[chain] <= 0 {
			t.Fatalf("%s is missing operational defaults", chain)
		}
	}
	if cfg.GasTopUp[ChainEthereum] <= 0 || cfg.Confirmations[ChainEthereum] < 64 {
		t.Fatal("Ethereum requires its own gas and confirmation settings")
	}
}

func TestNormalizeConfigKeepsSolanaDepositsDisabled(t *testing.T) {
	if _, err := NormalizeConfig(CryptoConfig{Enabled: true, Chains: []string{ChainSolana}}); err == nil {
		t.Fatal("Solana deposits remain unavailable")
	}
}

func TestConfigSnapshotsDoNotShareMutableMaps(t *testing.T) {
	base := DefaultCryptoConfig()
	s := &Service{config: base}
	snapshot := s.Config()
	snapshot.Confirmations[ChainBSC] = 100
	snapshot.Chains[0] = ChainBase
	if s.Config().Confirmations[ChainBSC] != 3 || s.Config().Chains[0] != ChainBSC {
		t.Fatal("a config reader changed the live config")
	}
	_, err := NormalizeConfig(CryptoConfig{Chains: []string{ChainBSC}, Confirmations: base.Confirmations})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeConfigValidatesRPCProtectionBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  CryptoConfig
	}{
		{"negative throttle", CryptoConfig{RPCThrottleMs: -1}},
		{"oversized throttle", CryptoConfig{RPCThrottleMs: maxRPCThrottleMs + 1}},
		{"oversized blocks", CryptoConfig{RPCLimits: map[string]RPCEndpointLimits{
			"https://rpc.example": {MaxBlocks: maxRPCBlocks + 1},
		}}},
		{"negative addresses", CryptoConfig{RPCLimits: map[string]RPCEndpointLimits{
			"https://rpc.example": {MaxAddresses: -1},
		}}},
		{"oversized addresses", CryptoConfig{RPCLimits: map[string]RPCEndpointLimits{
			"https://rpc.example": {MaxAddresses: maxRPCAddresses + 1},
		}}},
		{"empty key", CryptoConfig{RPCLimits: map[string]RPCEndpointLimits{
			" ": {MaxBlocks: 100},
		}}},
		{"invalid key", CryptoConfig{RPCLimits: map[string]RPCEndpointLimits{
			"not-a-url": {MaxBlocks: 100},
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NormalizeConfig(tc.cfg); err == nil {
				t.Fatal("unsafe RPC protection value was accepted")
			}
		})
	}
}

func TestNormalizeConfigAcceptsConfiguredAndPredeclaredRPCLimits(t *testing.T) {
	cfg, err := NormalizeConfig(CryptoConfig{
		RPC: map[string]string{ChainBSC: "https://rpc.example, https://backup.example/"},
		RPCLimits: map[string]RPCEndpointLimits{
			"https://rpc.example":     {MaxBlocks: maxRPCBlocks, MaxAddresses: maxRPCAddresses},
			"  https://new.example  ": {MaxBlocks: 100, MaxAddresses: 20},
		},
		RPCThrottleMs: maxRPCThrottleMs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RPCThrottleMs != maxRPCThrottleMs {
		t.Fatalf("rpc_throttle_ms = %d, want %d", cfg.RPCThrottleMs, maxRPCThrottleMs)
	}
	if got := cfg.RPCLimits["https://rpc.example"]; got.MaxBlocks != maxRPCBlocks || got.MaxAddresses != maxRPCAddresses {
		t.Fatalf("configured endpoint limits = %+v", got)
	}
	if _, ok := cfg.RPCLimits["https://new.example"]; !ok {
		t.Fatalf("trimmed pre-declared key missing: %+v", cfg.RPCLimits)
	}
}

// The market feed is the only external dependency of native crediting: a
// missing value must mean "public default", and a typo must fail at load time,
// not once per native deposit.
func TestNormalizeConfigNativePriceFeed(t *testing.T) {
	cfg, err := NormalizeConfig(CryptoConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NativePriceURL != "https://api.binance.com/api/v3/ticker/price" {
		t.Fatalf("native_price_url = %q, want the public Binance default", cfg.NativePriceURL)
	}

	cfg, err = NormalizeConfig(CryptoConfig{NativePriceURL: "  https://prices.example/ticker  "})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NativePriceURL != "https://prices.example/ticker" {
		t.Fatalf("native_price_url = %q, want the trimmed value", cfg.NativePriceURL)
	}

	cfg, err = NormalizeConfig(CryptoConfig{NativePriceURL: "https://prices.example/{symbol}"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NativePriceURL != "https://prices.example/{symbol}" {
		t.Fatalf("a symbol template must normalize, got %q", cfg.NativePriceURL)
	}

	for _, bad := range []string{
		"not-a-url",
		"ftp://prices.example/ticker",
		"https:///ticker",
		"http://127.0.0.1:8080/ticker",
		"http://localhost/ticker",
		"http://169.254.169.254/latest/meta-data",
		"http://100.100.100.200/{symbol}",
		"https://10.0.0.5/ticker",
		"http://[::1]:8080/{symbol}",
		"http://metadata.google.internal/{symbol}",
	} {
		if _, err := NormalizeConfig(CryptoConfig{NativePriceURL: bad}); err == nil {
			t.Fatalf("unsafe native price url %q was accepted", bad)
		}
	}
}

func TestNormalizeConfigRejectsPrivateExplorerAndWebhookURLs(t *testing.T) {
	for _, bad := range []string{
		"http://127.0.0.1:8080/api",
		"http://localhost/api",
		"http://169.254.169.254/latest/meta-data",
		"http://100.100.100.200/api",
		"https://192.168.1.10/api",
		"http://[::1]:8080/api",
		"https://metadata.google.internal/api",
		"https://api.bscscan.com/v2/api?apikey=oops",
	} {
		if _, err := NormalizeConfig(CryptoConfig{
			ExplorerAPIURL: map[string]string{ChainBSC: bad},
		}); err == nil {
			t.Fatalf("unsafe explorer_api_url %q was accepted", bad)
		}
	}

	for _, bad := range []string{
		"http://127.0.0.1:8080/push",
		"http://localhost/push",
		"http://169.254.169.254/latest/meta-data",
		"http://100.100.100.200/push",
		"https://10.0.0.1/push",
		"http://[::1]:8080/push",
		"http://metadata.google.internal/push",
	} {
		if _, err := NormalizeConfig(CryptoConfig{
			Notify: &CryptoNotifyConfig{Enabled: true, WebhookURL: bad},
		}); err == nil {
			t.Fatalf("unsafe notify.webhook_url %q was accepted", bad)
		}
	}
}

func TestConfigPatchAppliesNativePriceURL(t *testing.T) {
	url := "https://mirror.example/ticker"
	got := CryptoConfigPatch{NativePriceURL: &url}.Apply(DefaultCryptoConfig())
	if got.NativePriceURL != url {
		t.Fatalf("patched native_price_url = %q, want %q", got.NativePriceURL, url)
	}
}

// recordingSettingStore serves one stored raw config and records writes, so a
// read path that silently normalizes/backfills the row can be detected.
type recordingSettingStore struct {
	raw     string
	missing bool
	writes  int
	lastSet string
}

func (s *recordingSettingStore) GetCryptoSetting(context.Context, string) (string, error) {
	if s.missing {
		return "", service.ErrSettingNotFound
	}
	return s.raw, nil
}

func (s *recordingSettingStore) SetCryptoSetting(_ context.Context, _ string, value string) error {
	s.writes++
	s.lastSet = value
	return nil
}

// An administrator-enabled channel must survive loading: the defaults used to
// decode a partially stored row only fill absent fields, and reading the config
// must never write the normalized row back.
func TestLoadConfigKeepsStoredEnabledTrueWithoutWriteBack(t *testing.T) {
	store := &recordingSettingStore{raw: `{"enabled":true,"chains":["bsc"],"rpc":{"bsc":"https://bsc.example.test"},"credit_multiplier":5.5}`}
	cfg, err := LoadConfig(t.Context(), store)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("stored enabled=true was overwritten by the disabled default")
	}
	if cfg.CreditMultiplier != 5.5 {
		t.Fatalf("credit multiplier = %v, want the stored 5.5", cfg.CreditMultiplier)
	}
	if store.writes != 0 {
		t.Fatalf("LoadConfig wrote %d times (value %q); reads must never normalize the stored row", store.writes, store.lastSet)
	}
}

// Fields absent from an older stored row are filled from the defaults, while
// fields that are present keep their stored value.
func TestLoadConfigFillsMissingFieldsOnly(t *testing.T) {
	store := &recordingSettingStore{raw: `{"enabled":true,"chains":["bsc"],"scan_range":{"bsc":150}}`}
	cfg, err := LoadConfig(t.Context(), store)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("present enabled=true was not preserved")
	}
	if cfg.ScanRange[ChainBSC] != 150 {
		t.Fatalf("present scan_range = %d, want 150", cfg.ScanRange[ChainBSC])
	}
	if cfg.ScanRange[ChainEthereum] != DefaultCryptoConfig().ScanRange[ChainEthereum] {
		t.Fatalf("missing scan_range.ethereum = %d, want the default", cfg.ScanRange[ChainEthereum])
	}
	if cfg.CreditMultiplier != DefaultCryptoConfig().CreditMultiplier {
		t.Fatalf("missing credit_multiplier = %v, want the default", cfg.CreditMultiplier)
	}
	if store.writes != 0 {
		t.Fatalf("LoadConfig wrote the config row while backfilling defaults (%d writes)", store.writes)
	}
}

// A never-saved key is the normal first-run state and still yields defaults
// without persisting them.
func TestLoadConfigMissingKeyYieldsDefaultsWithoutWrite(t *testing.T) {
	store := &recordingSettingStore{missing: true}
	cfg, err := LoadConfig(t.Context(), store)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Enabled || cfg.CreditMultiplier != DefaultCryptoConfig().CreditMultiplier {
		t.Fatalf("first-run defaults wrong: %+v", cfg)
	}
	if store.writes != 0 {
		t.Fatalf("first-run load persisted defaults (%d writes)", store.writes)
	}
}

var _ SettingStore = (*recordingSettingStore)(nil)
