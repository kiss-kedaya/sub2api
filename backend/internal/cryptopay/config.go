// Package cryptopay implements the built-in crypto deposit channel:
// per-user HD deposit addresses, on-chain scanning, crediting and sweeps.
//
// Everything in this package is additive and disabled by default; when
// CryptoConfig.Enabled is false the process behaves exactly as before.
package cryptopay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	ChainBSC     = "bsc"
	ChainPolygon = "polygon"
	ChainPlasma  = "plasma"
	ChainSolana  = "solana"
)

var evmChains = []string{ChainBSC, ChainPolygon, ChainPlasma}

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

	// Crediting.
	CreditMinAmount  float64 `json:"credit_min_amount"`
	CreditMultiplier float64 `json:"credit_multiplier"`

	// Signer socket path (unix).
	SignerSocket string `json:"signer_socket"`
}

// DefaultCryptoConfig returns the disabled default with sane thresholds.
func DefaultCryptoConfig() CryptoConfig {
	return CryptoConfig{
		Enabled: false,
		Chains:  []string{ChainBSC},
		RPC:     map[string]string{},
		SweepGlobalThreshold: 1000,
		SweepMinPerAddress: map[string]float64{
			ChainBSC: 3, ChainPolygon: 3, ChainPlasma: 3, ChainSolana: 1,
		},
		GasTopUp: map[string]float64{
			ChainBSC: 0.002, ChainPolygon: 0.5, ChainPlasma: 0.002,
		},
		Confirmations: map[string]int{
			ChainBSC: 5, ChainPolygon: 5, ChainPlasma: 5, ChainSolana: 1,
		},
		CreditMinAmount:  0.1,
		// 1 USDT credits 6.7 balance units (operator-confirmed 2026-10-12).
		CreditMultiplier: 6.7,
		SignerSocket:     "/run/sub2api/signer.sock",
	}
}

// NormalizeConfig fills defaults for zero values and validates.
func NormalizeConfig(cfg CryptoConfig) (CryptoConfig, error) {
	def := DefaultCryptoConfig()
	if cfg.Chains == nil {
		cfg.Chains = def.Chains
	}
	for _, chain := range cfg.Chains {
		if !KnownChain(chain) {
			return cfg, fmt.Errorf("crypto config: unknown chain %q", chain)
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
	if cfg.CreditMinAmount <= 0 {
		cfg.CreditMinAmount = def.CreditMinAmount
	}
	if cfg.CreditMultiplier <= 0 {
		cfg.CreditMultiplier = def.CreditMultiplier
	}
	if strings.TrimSpace(cfg.SignerSocket) == "" {
		cfg.SignerSocket = def.SignerSocket
	}
	if cfg.Enabled {
		if strings.TrimSpace(cfg.HotWalletEVM) == "" {
			return cfg, fmt.Errorf("crypto config: hot_wallet_evm is required when enabled")
		}
		if strings.TrimSpace(cfg.HotWalletSolana) == "" {
			return cfg, fmt.Errorf("crypto config: hot_wallet_solana is required when enabled")
		}
	}
	return cfg, nil
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
