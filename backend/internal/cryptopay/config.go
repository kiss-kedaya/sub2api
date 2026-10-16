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

	// Signer socket path (unix).
	SignerSocket string `json:"signer_socket"`

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
		SignerSocket:     "/run/sub2api/signer.sock",
	}
}

// NormalizeConfig fills defaults for zero values and validates.
func NormalizeConfig(cfg CryptoConfig) (CryptoConfig, error) {
	def := DefaultCryptoConfig()
	if cfg.Chains == nil {
		cfg.Chains = def.Chains
	}
	// A config saved before RPC keys existed deserializes rpc as nil; the
	// admin console indexes it directly, so backfill an empty map.
	if cfg.RPC == nil {
		cfg.RPC = map[string]string{}
	}
	// A config saved before RPC keys existed deserializes rpc as nil; the
	// admin console indexes it directly, so backfill an empty map.
	if cfg.RPC == nil {
		cfg.RPC = map[string]string{}
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
	if cfg.ScanRange == nil {
		cfg.ScanRange = def.ScanRange
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
	if strings.TrimSpace(cfg.SignerSocket) == "" {
		cfg.SignerSocket = def.SignerSocket
	}
	// Hot wallets may stay empty: deposits, scanning and crediting all work
	// without them. Only the sweep path needs a destination, and it refuses
	// to run until one is configured (see ValidateSweepReady).
	return cfg, nil
}

// ValidateSweepReady reports why a real sweep cannot run yet. Empty is the
// expected first-run state; the message tells the operator what to set.
func (cfg CryptoConfig) ValidateSweepReady() error {
	hot := strings.TrimSpace(cfg.HotWalletEVM)
	if hot == "" {
		return fmt.Errorf("sweep disabled: set the EVM hot wallet (receiving address) first")
	}
	if isZeroAddress(hot) {
		return fmt.Errorf("sweep disabled: the EVM hot wallet is the zero address")
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

// SettingKeySignerPolicy mirrors the signer's own policy key. The signer
// reloads it before every signature, so keeping the sweep destinations in
// sync here is what makes an operator-changed receiving address take effect
// without restarting the signer.
const SettingKeySignerPolicy = "crypto.signer.policy"

// SyncSignerTargets merges the configured sweep destinations into the
// signer policy's target list. It tracks which targets were added by this
// automated sync in "managed_targets", so a later config save can remove
// stale auto-added addresses without touching targets the operator added
// manually (for example a cold wallet). Limits, rate and hot index are
// preserved.
func SyncSignerTargets(ctx context.Context, store SettingStore, cfg CryptoConfig) (bool, error) {
	wanted := make([]string, 0, 2)
	if v := strings.TrimSpace(cfg.HotWalletEVM); v != "" {
		wanted = append(wanted, v)
	}
	if v := strings.TrimSpace(cfg.HotWalletSolana); v != "" {
		wanted = append(wanted, v)
	}

	raw, err := store.GetCryptoSetting(ctx, SettingKeySignerPolicy)
	if err != nil {
		return false, fmt.Errorf("read signer policy: %w", err)
	}
	// Only merge into an already-configured policy. Writing a fresh one here
	// would omit max_per_tx/max_daily, and the signer would then reject every
	// signature with "no_per_tx_limit_configured" — a silent total outage of
	// sweeps. A missing policy is the operator's setup step, not ours.
	if strings.TrimSpace(raw) == "" {
		return false, nil
	}
	policy := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return false, fmt.Errorf("parse signer policy: %w", err)
	}

	existing := make([]string, 0, 8)
	if list, ok := policy["targets"].([]any); ok {
		for _, item := range list {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				existing = append(existing, s)
			}
		}
	}
	managed := make([]string, 0, 4)
	if list, ok := policy["managed_targets"].([]any); ok {
		for _, item := range list {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				managed = append(managed, s)
			}
		}
	}

	// Drop stale addresses that came from an earlier automatic sync.
	managedSet := make(map[string]struct{}, len(managed))
	for _, m := range managed {
		managedSet[strings.ToLower(m)] = struct{}{}
	}
	kept := make([]string, 0, len(existing))
	for _, t := range existing {
		if _, tracked := managedSet[strings.ToLower(t)]; tracked {
			continue
		}
		kept = append(kept, t)
	}
	existing = kept

	// Add the current destinations without duplicates.
	seen := make(map[string]struct{}, len(existing)+len(wanted))
	for _, t := range existing {
		seen[strings.ToLower(t)] = struct{}{}
	}
	changed := false
	for _, t := range wanted {
		if _, dup := seen[strings.ToLower(t)]; dup {
			continue
		}
		existing = append(existing, t)
		seen[strings.ToLower(t)] = struct{}{}
		changed = true
	}
	if !changed && len(managed) != len(wanted) {
		changed = true
	}
	policy["targets"] = existing
	policy["managed_targets"] = wanted
	// The signer signs gas top-ups with the hot seed at policy.hot_index and
	// rejects any other. If the console's hot_index diverges from the
	// policy's, gas would be paid from the wrong wallet (or the derived
	// address the health panel reports would be wrong). Keep them equal.
	if current, ok := policy["hot_index"].(float64); !ok || uint32(current) != cfg.HotIndex {
		policy["hot_index"] = cfg.HotIndex
		changed = true
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return false, fmt.Errorf("encode signer policy: %w", err)
	}
	if err := store.SetCryptoSetting(ctx, SettingKeySignerPolicy, string(encoded)); err != nil {
		return false, fmt.Errorf("write signer policy: %w", err)
	}
	return changed, nil
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
	SignerSocket         *string             `json:"signer_socket"`
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
	if p.SignerSocket != nil {
		out.SignerSocket = *p.SignerSocket
	}
	if p.HotIndex != nil {
		out.HotIndex = *p.HotIndex
	}
	if p.SweepAuto != nil {
		out.SweepAuto = *p.SweepAuto
	}
	return out
}
