// Package cryptopay implements the built-in crypto deposit channel:
// per-user HD deposit addresses, on-chain scanning, crediting and sweeps.
//
// Everything in this package is additive and disabled by default; when
// CryptoConfig.Enabled is false the process behaves exactly as before.
package cryptopay

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"maps"
	"slices"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Setting keys live under the "crypto." namespace so fork users who never
// enable the channel are unaffected.
const (
	SettingKeyCryptoConfig = "crypto.config"
)

// Chain identifiers supported for deposits. EVM chains share one address.
const (
	ChainBSC      = "bsc"
	ChainPolygon  = "polygon"
	ChainPlasma   = "plasma"
	ChainArbitrum = "arbitrum"
	ChainBase     = "base"
	ChainXLayer   = "xlayer"
	ChainSolana   = "solana"
)

var evmChains = []string{ChainBSC, ChainPolygon, ChainPlasma, ChainArbitrum, ChainBase, ChainXLayer}

// IsEVMChain reports whether chain uses the shared EVM address.
func IsEVMChain(chain string) bool {
	for _, c := range evmChains {
		if c == chain {
			return true
		}
	}
	return false
}

// KnownChain reports whether chain is supported at all.
func KnownChain(chain string) bool {
	return IsEVMChain(chain) || chain == ChainSolana
}

// CryptoConfig is the persisted configuration (JSON in settings).
type CryptoConfig struct {
	Enabled bool     `json:"enabled"`
	Chains  []string `json:"chains"`

	// RPC endpoints per chain; comma-separated list for rotation.
	RPC map[string]string `json:"rpc"`

	// Sweep settings.
	SweepGlobalThreshold float64            `json:"sweep_global_threshold"`
	SweepMinPerAddress   map[string]float64 `json:"sweep_min_per_address"`
	GasTopUp             map[string]float64 `json:"gas_topup"`
	HotWalletEVM         string             `json:"hot_wallet_evm"`
	HotWalletSolana      string             `json:"hot_wallet_solana"`

	// Confirmations required before crediting, per chain.
	Confirmations map[string]int `json:"confirmations"`

	// ScanRange caps the block span of one eth_getLogs call per chain.
	// Providers enforce different limits (some public X Layer endpoints cap
	// at 100 blocks); missing key = use the built-in default (2000).
	ScanRange map[string]int `json:"scan_range"`

	// Crediting.
	CreditMinAmount  float64 `json:"credit_min_amount"`
	CreditMaxAmount  float64 `json:"credit_max_amount"`
	CreditMultiplier float64 `json:"credit_multiplier"`

	// HotIndex is the HD index of the hot/gas wallet. 0 = the first derived
	// address, which is also the default hot wallet.
	HotIndex uint32 `json:"hot_index"`

	// SweepAuto enables the scheduled sweep run (still manual by default).
	SweepAuto bool `json:"sweep_auto"`
}

// DefaultCryptoConfig returns the disabled default with sane thresholds.
func DefaultCryptoConfig() CryptoConfig {
	return CryptoConfig{
		Enabled:              false,
		Chains:               []string{ChainBSC},
		RPC:                  map[string]string{},
		SweepGlobalThreshold: 1000,
		SweepMinPerAddress: map[string]float64{
			ChainBSC: 3, ChainPolygon: 3, ChainPlasma: 3,
			ChainArbitrum: 3, ChainBase: 3, ChainXLayer: 3,
			ChainSolana: 1,
		},
		GasTopUp: map[string]float64{
			ChainBSC: 0.002, ChainPolygon: 0.5, ChainPlasma: 0.002,
			ChainArbitrum: 0.0002, ChainBase: 0.0002, ChainXLayer: 0.002,
		},
		Confirmations: map[string]int{
			ChainBSC: 5, ChainPolygon: 5, ChainPlasma: 5,
			ChainArbitrum: 5, ChainBase: 5, ChainXLayer: 5,
			ChainSolana: 1,
		},
		// Conservative spans that every known public endpoint accepts:
		// X Layer's public RPC rejects anything over 100 blocks.
		ScanRange: map[string]int{
			ChainBSC: 2000, ChainPolygon: 2000, ChainPlasma: 2000,
			ChainArbitrum: 2000, ChainBase: 2000, ChainXLayer: 100,
		},
		// 0.001 keeps probe deposits creditable; 10000 caps one transfer so a
		// fat-fingered amount cannot mint an absurd balance in one go.
		CreditMinAmount: 0.001,
		CreditMaxAmount: 10000,
		// 1 USDT credits 6.7 balance units (operator-confirmed 2026-10-12).
		CreditMultiplier: 6.7,
	}
}

// NormalizeConfig fills defaults for zero values and validates.
func NormalizeConfig(cfg CryptoConfig) (CryptoConfig, error) {
	if cfg.HotIndex != 0 {
		return cfg, fmt.Errorf("crypto config: Gas wallet index must be zero")
	}
	cfg.Chains = slices.Clone(cfg.Chains)
	cfg.RPC = maps.Clone(cfg.RPC)
	cfg.Confirmations = maps.Clone(cfg.Confirmations)
	cfg.ScanRange = maps.Clone(cfg.ScanRange)
	cfg.SweepMinPerAddress = maps.Clone(cfg.SweepMinPerAddress)
	cfg.GasTopUp = maps.Clone(cfg.GasTopUp)
	def := DefaultCryptoConfig()
	if cfg.Chains == nil {
		cfg.Chains = def.Chains
	}
	// A config saved before RPC keys existed deserializes rpc as nil; the
	// admin console indexes it directly, so backfill an empty map.
	if cfg.RPC == nil {
		cfg.RPC = map[string]string{}
	}
	for _, chain := range cfg.Chains {
		if !IsEVMChain(chain) {
			return cfg, fmt.Errorf("crypto config: deposits are not implemented for chain %q", chain)
		}
	}
	if cfg.SweepGlobalThreshold <= 0 {
		cfg.SweepGlobalThreshold = def.SweepGlobalThreshold
	}
	if cfg.SweepMinPerAddress == nil {
		cfg.SweepMinPerAddress = def.SweepMinPerAddress
	}
	if cfg.GasTopUp == nil {
		cfg.GasTopUp = def.GasTopUp
	}
	if cfg.Confirmations == nil {
		cfg.Confirmations = def.Confirmations
	}
	if cfg.ScanRange == nil {
		cfg.ScanRange = def.ScanRange
	}
	for _, chain := range cfg.Chains {
		if _, ok := cfg.Confirmations[chain]; !ok {
			cfg.Confirmations[chain] = def.Confirmations[chain]
		}
		if _, ok := cfg.ScanRange[chain]; !ok {
			cfg.ScanRange[chain] = def.ScanRange[chain]
		}
	}
	if cfg.CreditMinAmount <= 0 {
		cfg.CreditMinAmount = def.CreditMinAmount
	}
	if cfg.CreditMaxAmount <= 0 {
		cfg.CreditMaxAmount = def.CreditMaxAmount
	}
	if cfg.CreditMultiplier <= 0 {
		cfg.CreditMultiplier = def.CreditMultiplier
	}
	// Hot wallets may stay empty: deposits, scanning and crediting all work
	// without them. Only the sweep path needs a destination, and it refuses
	// to run until one is configured (see ValidateSweepReady). When set,
	// they must be well-formed: common.HexToAddress silently pads or
	// truncates, so "0x" would otherwise become the zero address.
	if v := strings.TrimSpace(cfg.HotWalletEVM); v != "" && !validEVMAddress(v) {
		return cfg, fmt.Errorf("crypto config: hot_wallet_evm is not a valid EVM address")
	}
	if v := strings.TrimSpace(cfg.HotWalletSolana); v != "" && !validSolanaAddress(v) {
		return cfg, fmt.Errorf("crypto config: hot_wallet_solana is not a valid Solana address")
	}
	for chain, value := range cfg.Confirmations {
		cfg.Confirmations[chain] = clampInt(value, 0, 1000)
	}
	for chain, value := range cfg.ScanRange {
		cfg.ScanRange[chain] = clampInt(value, 1, 100000)
	}
	for chain, value := range cfg.SweepMinPerAddress {
		if value < 0 {
			cfg.SweepMinPerAddress[chain] = 0
		}
	}
	for chain, value := range cfg.GasTopUp {
		if value < 0 {
			cfg.GasTopUp[chain] = 0
		}
	}
	return cfg, nil
}

// ValidateSweepReady reports why a real sweep cannot run yet. Empty is the
// expected first-run state; the message tells the operator what to set.
func (cfg CryptoConfig) ValidateSweepReady() error {
	hot := strings.TrimSpace(cfg.HotWalletEVM)
	if hot == "" {
		return fmt.Errorf("sweep disabled: set the EVM hot wallet (receiving address) first")
	}
	if !validEVMAddress(hot) {
		return fmt.Errorf("sweep disabled: the EVM hot wallet is not a valid 0x-prefixed 20-byte address")
	}
	return nil
}

// isZeroAddress catches the all-zero placeholder: sending funds there burns
// them, and an unset field often round-trips as 0x000…0 rather than "".
func isZeroAddress(address string) bool {
	trimmed := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(address)), "0x")
	if trimmed == "" {
		return false
	}
	return strings.Trim(trimmed, "0") == ""
}

// validEVMAddress accepts only the canonical 0x-prefixed 20-byte hex form.
// common.HexToAddress pads or truncates anything else without an error, so
// "0x" or a short string would silently become a different destination.
func validEVMAddress(address string) bool {
	trimmed := strings.TrimSpace(address)
	if len(trimmed) != 42 || !strings.HasPrefix(trimmed, "0x") {
		return false
	}
	if _, err := hex.DecodeString(trimmed[2:]); err != nil {
		return false
	}
	return !isZeroAddress(trimmed)
}

// validSolanaAddress is a length/charset sanity check for a base58 pubkey.
func validSolanaAddress(address string) bool {
	trimmed := strings.TrimSpace(address)
	if len(trimmed) < 32 || len(trimmed) > 44 {
		return false
	}
	for _, r := range trimmed {
		switch {
		case r >= '1' && r <= '9':
		case r >= 'A' && r <= 'H':
		case r >= 'J' && r <= 'N':
		case r >= 'P' && r <= 'Z':
		case r >= 'a' && r <= 'k':
		case r >= 'm' && r <= 'z':
		default:
			return false
		}
	}
	return true
}

// clampInt bounds a configured integer so a negative or absurd value cannot
// wrap when cast to uint64 by the scanner.
func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

// SettingStore is the narrow persistence surface the config needs: read and
// write one settings key. *SettingService-backed adapters implement it.
type SettingStore interface {
	GetCryptoSetting(ctx context.Context, key string) (string, error)
	SetCryptoSetting(ctx context.Context, key, value string) error
}

// LoadConfig reads and normalizes the config from settings.
func LoadConfig(ctx context.Context, store SettingStore) (CryptoConfig, error) {
	raw, err := store.GetCryptoSetting(ctx, SettingKeyCryptoConfig)
	if err != nil {
		// A never-saved key is the normal first-run state: return defaults.
		if errors.Is(err, service.ErrSettingNotFound) {
			return DefaultCryptoConfig(), nil
		}
		return DefaultCryptoConfig(), err
	}
	cfg := DefaultCryptoConfig()
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return DefaultCryptoConfig(), fmt.Errorf("crypto config: invalid JSON: %w", err)
		}
	}
	return NormalizeConfig(cfg)
}

// SaveConfig validates and persists the config.
func SaveConfig(ctx context.Context, store SettingStore, cfg CryptoConfig) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	normalized, err := NormalizeConfig(cfg)
	if err != nil {
		return err
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return err
	}
	return store.SetCryptoSetting(ctx, SettingKeyCryptoConfig, string(data))
}

// CryptoConfigPatch is a partial update. Pointer fields distinguish "not
// sent" (keep the stored value) from "sent as empty" (clear it), so a client
// that omits keys can never zero the configuration by accident.
type CryptoConfigPatch struct {
	Enabled              *bool               `json:"enabled"`
	Chains               *[]string           `json:"chains"`
	RPC                  *map[string]string  `json:"rpc"`
	SweepGlobalThreshold *float64            `json:"sweep_global_threshold"`
	SweepMinPerAddress   *map[string]float64 `json:"sweep_min_per_address"`
	GasTopUp             *map[string]float64 `json:"gas_topup"`
	HotWalletEVM         *string             `json:"hot_wallet_evm"`
	HotWalletSolana      *string             `json:"hot_wallet_solana"`
	Confirmations        *map[string]int     `json:"confirmations"`
	ScanRange            *map[string]int     `json:"scan_range"`
	CreditMinAmount      *float64            `json:"credit_min_amount"`
	CreditMaxAmount      *float64            `json:"credit_max_amount"`
	CreditMultiplier     *float64            `json:"credit_multiplier"`
	HotIndex             *uint32             `json:"hot_index"`
	SweepAuto            *bool               `json:"sweep_auto"`
}

// Apply overlays the patch onto base, leaving omitted fields untouched.
func (p CryptoConfigPatch) Apply(base CryptoConfig) CryptoConfig {
	out := base
	if p.Enabled != nil {
		out.Enabled = *p.Enabled
	}
	if p.Chains != nil {
		out.Chains = *p.Chains
	}
	if p.RPC != nil {
		out.RPC = *p.RPC
	}
	if p.SweepGlobalThreshold != nil {
		out.SweepGlobalThreshold = *p.SweepGlobalThreshold
	}
	if p.SweepMinPerAddress != nil {
		out.SweepMinPerAddress = *p.SweepMinPerAddress
	}
	if p.GasTopUp != nil {
		out.GasTopUp = *p.GasTopUp
	}
	if p.HotWalletEVM != nil {
		out.HotWalletEVM = *p.HotWalletEVM
	}
	if p.HotWalletSolana != nil {
		out.HotWalletSolana = *p.HotWalletSolana
	}
	if p.Confirmations != nil {
		out.Confirmations = *p.Confirmations
	}
	if p.ScanRange != nil {
		out.ScanRange = *p.ScanRange
	}
	if p.CreditMinAmount != nil {
		out.CreditMinAmount = *p.CreditMinAmount
	}
	if p.CreditMaxAmount != nil {
		out.CreditMaxAmount = *p.CreditMaxAmount
	}
	if p.CreditMultiplier != nil {
		out.CreditMultiplier = *p.CreditMultiplier
	}
	if p.HotIndex != nil {
		out.HotIndex = *p.HotIndex
	}
	if p.SweepAuto != nil {
		out.SweepAuto = *p.SweepAuto
	}
	return out
}
