// Package cryptopay implements the built-in crypto deposit channel:
// per-user HD deposit addresses, on-chain scanning, crediting and sweeps.
//
// Everything in this package is additive and disabled by default; when
// CryptoConfig.Enabled is false the process behaves exactly as before.
package cryptopay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/nativeprice"
	"maps"
	"math"
	"net/url"
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
	ChainEthereum = "ethereum"
	ChainPolygon  = "polygon"
	ChainPlasma   = "plasma"
	ChainArbitrum = "arbitrum"
	ChainBase     = "base"
	ChainXLayer   = "xlayer"
	ChainSolana   = "solana"
)

var evmChains = []string{ChainBSC, ChainEthereum, ChainPolygon, ChainPlasma, ChainArbitrum, ChainBase, ChainXLayer}

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

// RPC protection bounds. These are deliberately conservative: a typo that
// stalls every call or disables scanning must be rejected, not clamped.
const (
	maxRPCThrottleMs = 10_000
	maxRPCBlocks     = 100_000
	maxRPCAddresses  = 10_000
)

// CryptoConfig is the persisted configuration (JSON in settings).
type CryptoConfig struct {
	Enabled bool     `json:"enabled"`
	Chains  []string `json:"chains"`

	// RPC endpoints per chain; comma-separated list for rotation.
	RPC map[string]string `json:"rpc"`

	// RPCLimits records what each endpoint accepts in one eth_getLogs.
	// Keyed by endpoint URL. Without it the scanner can only discover a limit
	// by being rejected, which costs a request per attempt and lets one strict
	// provider in a rotation throttle the whole chain.
	RPCLimits map[string]RPCEndpointLimits `json:"rpc_limits,omitempty"`

	// RPCThrottleMs paces calls to each endpoint. Public providers rate-limit
	// per endpoint and the scanner is bursty: one pass fires several batches per
	// chain across several chains. Without pacing a burst gets 429 and, because
	// the pool rotates on failure, the next endpoint is burned too. Zero
	// disables pacing and keeps the previous behaviour.
	RPCThrottleMs int `json:"rpc_throttle_ms,omitempty"`

	// Sweep settings.
	SweepGlobalThreshold float64            `json:"sweep_global_threshold"`
	SweepMinPerAddress   map[string]float64 `json:"sweep_min_per_address"`
	GasTopUp             map[string]float64 `json:"gas_topup"`
	HotWalletEVM         string             `json:"hot_wallet_evm"`
	HotWalletSolana      string             `json:"hot_wallet_solana"`

	// Confirmations required before crediting, per chain.
	//
	// With instant credit on these become the FINAL depth: the balance lands on
	// the first verified observation and these confirmations only promote the
	// provisional credit to final (no second balance movement). With instant
	// credit off they gate the credit exactly as before.
	Confirmations map[string]int `json:"confirmations"`

	// InstantCreditEnabled credits a deposit as soon as the first observation
	// is verified on the canonical chain, i.e. at 0 confirmations. A reorg or a
	// failed/replaced transaction then automatically revokes the credited
	// amount. Pointer-typed on purpose: a config stored before this switch
	// existed must default to ON, and a plain bool would read as OFF.
	InstantCreditEnabled *bool `json:"instant_credit_enabled,omitempty"`
	// InstantCreditMinAmount is the credited-amount floor (USD, the same unit
	// as the balance credit) for the instant path. A transfer below it keeps
	// the confirmation-gated path. 0 means every amount is eligible.
	InstantCreditMinAmount float64 `json:"instant_credit_min_amount"`

	// ScanRange caps the block span of one eth_getLogs call per chain.
	// Providers enforce different limits (some public X Layer endpoints cap
	// at 100 blocks); missing key = use the built-in default (2000).
	ScanRange map[string]int `json:"scan_range"`

	// ScanConcurrency overrides the log-batch and native-block worker count per
	// chain. Missing or zero keeps the automatic public-node limit; 1..64 is explicit.
	ScanConcurrency map[string]int `json:"scan_concurrency"`

	// Crediting.
	CreditMinAmount  float64 `json:"credit_min_amount"`
	CreditMaxAmount  float64 `json:"credit_max_amount"`
	CreditMultiplier float64 `json:"credit_multiplier"`

	// NativePriceURL is the market feed that settles native-coin (ETH/BNB)
	// deposits. It is a ticker endpoint: the symbol is passed either as
	// Binance's `?symbol=` query parameter or through a `{symbol}` placeholder
	// in the URL. Empty uses nativeprice.DefaultBaseURL. Native deposits are
	// never credited without a fresh quote from this feed.
	NativePriceURL string `json:"native_price_url"`

	// HotIndex is the HD index of the hot/gas wallet. 0 = the first derived
	// address, which is also the default hot wallet.
	HotIndex uint32 `json:"hot_index"`

	// SweepAuto enables the scheduled sweep run (still manual by default).
	SweepAuto bool `json:"sweep_auto"`

	// NativeScan selects how native-coin (ETH/BNB) deposits are discovered.
	// "blocks" (default) keeps the per-block eth_getBlockByNumber scan: one
	// request per block, so the count follows the chain's block rate. "txlist"
	// is the optional account-API path: one request per watched address per
	// poll interval, so the request count scales with addresses × interval
	// instead of with the block rate.
	NativeScan string `json:"native_scan"`
	// NativePollSeconds paces the native poll. Default 10. It is deliberately a
	// wall-clock interval, not a block window: in txlist mode the cost is
	// addresses × (86400 / interval); in blocks mode it paces the hot-wallet
	// native-outflow guard.
	NativePollSeconds int `json:"native_poll_seconds"`
	// ExplorerAPIKey authenticates the account API. When it is empty the
	// txlist scan reports an error and keeps its cursor instead of silently
	// falling back to the block-rate scan, which would exhaust the provider
	// plan.
	ExplorerAPIKey string `json:"explorer_api_key"`
	// ExplorerAPIURL overrides the account-API root per chain (no query
	// string). The v2 endpoints receive chainid from the client.
	ExplorerAPIURL map[string]string `json:"explorer_api_url"`

	// SweepDestinationLock pins the sweep destination that was in force when
	// the browser-held signer shards were created. Without it, an attacker who
	// owns the host can rewrite hot_wallet_evm/hot_wallet_solana in settings and
	// the next sweep sends every deposit to the attacker's address. The lock
	// turns that silent rewrite into a 409; changing it requires the dedicated
	// sweep-destination endpoint with 2FA and the risk phrase.
	SweepDestinationLock *SweepDestinationLock `json:"sweep_destination_lock,omitempty"`

	// Notify delivers a best-effort message for every sensitive wallet action.
	// Optional and disabled by default: channels are operator-managed and never
	// hard-coded in the repository.
	Notify *CryptoNotifyConfig `json:"notify,omitempty"`
}

// CryptoNotifyConfig is the operator-managed notification channel set.
type CryptoNotifyConfig struct {
	Enabled bool `json:"enabled"`
	// WebhookURL receives one HTTP POST with a JSON body. A Bark device URL
	// (https://api.day.app/<device-key>) works because the payload also carries
	// the title/body fields Bark expects.
	WebhookURL string `json:"webhook_url,omitempty"`
	// Email reuses the gateway's existing SMTP configuration. Empty disables
	// the email channel.
	Email string `json:"email,omitempty"`
}

// maxNotifyURLBytes bounds the webhook URL so a mistyped config cannot push a
// megabytes-long value into the settings row.
const maxNotifyURLBytes = 2048

// DefaultCryptoConfig returns the disabled default with sane thresholds.
func DefaultCryptoConfig() CryptoConfig {
	return CryptoConfig{
		Enabled:              false,
		Chains:               []string{ChainBSC},
		RPC:                  map[string]string{},
		SweepGlobalThreshold: 1000,
		SweepMinPerAddress: map[string]float64{
			ChainBSC: 3, ChainEthereum: 3, ChainPolygon: 3, ChainPlasma: 3,
			ChainArbitrum: 3, ChainBase: 3, ChainXLayer: 3,
			ChainSolana: 1,
		},
		GasTopUp: map[string]float64{
			ChainBSC: 0.002, ChainEthereum: 0.002, ChainPolygon: 0.5, ChainPlasma: 0.002,
			ChainArbitrum: 0.0002, ChainBase: 0.0002, ChainXLayer: 0.002,
		},
		Confirmations: map[string]int{
			ChainBSC: 3, ChainEthereum: 64, ChainPolygon: 5, ChainPlasma: 5,
			ChainArbitrum: 5, ChainBase: 5, ChainXLayer: 5,
			ChainSolana: 1,
		},
		// Instant credit is the shipped default (the whole point of the
		// feature); the switch exists so an operator can fall back to the old
		// confirmation-gated behaviour with one config write. 0 = no floor.
		InstantCreditEnabled:   boolPtr(true),
		InstantCreditMinAmount: 0,
		// Conservative spans that every known public endpoint accepts:
		// X Layer's public RPC rejects anything over 100 blocks.
		ScanRange: map[string]int{
			ChainBSC: 2000, ChainEthereum: 2000, ChainPolygon: 2000, ChainPlasma: 2000,
			ChainArbitrum: 2000, ChainBase: 2000, ChainXLayer: 100,
		},
		ScanConcurrency: map[string]int{},
		// 0.001 keeps probe deposits creditable; 10000 caps one transfer so a
		// fat-fingered amount cannot mint an absurd balance in one go.
		CreditMinAmount: 0.001,
		CreditMaxAmount: 10000,
		// 1 USDT credits 6.7 balance units (operator-confirmed 2026-10-12).
		CreditMultiplier: 6.7,
		NativePriceURL:   nativeprice.DefaultBaseURL,
		// The block scan is the default native source. The address poll is an
		// operator choice: it costs one request per address per interval
		// instead of one request per block, but needs an explorer key.
		NativeScan:        NativeScanBlocks,
		NativePollSeconds: DefaultNativePollSeconds,
		ExplorerAPIURL:    DefaultExplorerAPIURLs(),
	}
}

// boolPtr returns a pointer to v; used for tri-state config switches whose
// zero value must be distinguishable from "not configured".
func boolPtr(v bool) *bool { return &v }

// InstantCreditOn reports whether deposits are credited before finality. A
// config that predates the switch (nil) reads as ON, matching the default.
func (cfg CryptoConfig) InstantCreditOn() bool {
	return cfg.InstantCreditEnabled == nil || *cfg.InstantCreditEnabled
}

// DefaultExplorerAPIURLs returns the Etherscan v2 account-API roots. Both
// Etherscan and BscScan v2 answer module=account&action=txlist; the client
// appends the chainid the v2 API requires.
func DefaultExplorerAPIURLs() map[string]string {
	return map[string]string{
		ChainEthereum: "https://api.etherscan.io/v2/api",
		ChainBSC:      "https://api.bscscan.com/v2/api",
	}
}

// explorerChainIDs maps a chain to the chainid parameter of its account API.
func explorerChainIDs() map[string]int64 {
	return map[string]int64{
		ChainEthereum: 1,
		ChainBSC:      56,
	}
}

// normalizeRPCLimits validates operator-declared eth_getLogs limits. The map
// is keyed by the full endpoint URL, which may contain credentials, so callers
// must never expose it without fresh 2FA. A key must either match an endpoint
// currently in the rotation or be an absolute http(s) URL, which lets an
// operator pre-declare a provider before adding it to rpc.
func normalizeRPCLimits(cfg *CryptoConfig) error {
	if len(cfg.RPCLimits) == 0 {
		cfg.RPCLimits = nil
		return nil
	}
	configured := configuredRPCEndpoints(cfg.RPC)
	limits := make(map[string]RPCEndpointLimits, len(cfg.RPCLimits))
	for endpoint, value := range cfg.RPCLimits {
		key := strings.TrimSpace(endpoint)
		if key == "" {
			return fmt.Errorf("crypto config: rpc_limits endpoint must not be empty")
		}
		if value.MaxBlocks > maxRPCBlocks {
			return fmt.Errorf("crypto config: rpc_limits max_blocks must be <= %d", maxRPCBlocks)
		}
		if value.MaxAddresses < 0 || value.MaxAddresses > maxRPCAddresses {
			return fmt.Errorf("crypto config: rpc_limits max_addresses must be between 0 and %d", maxRPCAddresses)
		}
		if _, ok := configured[rpcEndpointKey(key)]; !ok && !validRPCURL(key) {
			return fmt.Errorf("crypto config: rpc_limits endpoint must match a configured RPC endpoint or be an absolute http(s) URL")
		}
		limits[key] = value
	}
	cfg.RPCLimits = limits
	return nil
}

// normalizeNativeScan validates the address-based native scan settings. It
// refuses values that would make the poller useless (a typo'd mode, a poll
// interval that drains the provider plan) instead of clamping them silently.
func normalizeNativeScan(cfg *CryptoConfig, def CryptoConfig) error {
	switch strings.ToLower(strings.TrimSpace(cfg.NativeScan)) {
	case "":
		cfg.NativeScan = def.NativeScan
	case NativeScanTxList:
		cfg.NativeScan = NativeScanTxList
	case NativeScanBlocks:
		cfg.NativeScan = NativeScanBlocks
	default:
		return fmt.Errorf("crypto config: native_scan must be %q or %q", NativeScanTxList, NativeScanBlocks)
	}
	if cfg.NativePollSeconds == 0 {
		cfg.NativePollSeconds = def.NativePollSeconds
	}
	if cfg.NativePollSeconds < 10 || cfg.NativePollSeconds > 3600 {
		return fmt.Errorf("crypto config: native_poll_seconds must be between 10 and 3600")
	}
	cfg.ExplorerAPIKey = strings.TrimSpace(cfg.ExplorerAPIKey)
	urls := make(map[string]string, len(def.ExplorerAPIURL))
	for chain, root := range def.ExplorerAPIURL {
		urls[chain] = root
	}
	for chain, root := range cfg.ExplorerAPIURL {
		key := strings.ToLower(strings.TrimSpace(chain))
		root = strings.TrimSpace(root)
		if _, ok := explorerChainIDs()[key]; !ok {
			continue
		}
		if root == "" {
			continue // keep the built-in default for this chain
		}
		if !validRPCURL(root) {
			return fmt.Errorf("crypto config: explorer_api_url for chain %q is not an absolute http(s) URL", chain)
		}
		urls[key] = strings.TrimSuffix(root, "/")
	}
	cfg.ExplorerAPIURL = urls
	return nil
}

func configuredRPCEndpoints(rpc map[string]string) map[string]struct{} {
	out := make(map[string]struct{}, len(rpc))
	for _, list := range rpc {
		for _, endpoint := range strings.Split(list, ",") {
			if key := rpcEndpointKey(endpoint); key != "" {
				out[key] = struct{}{}
			}
		}
	}
	return out
}

// rpcEndpointKey matches the registry's URL normalisation so a trailing slash
// or a case difference does not make an explicitly configured limit inert.
func rpcEndpointKey(endpoint string) string {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(trimmed), "/")
}

func validRPCURL(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	return (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

// NormalizeConfig fills defaults for zero values and validates.
func NormalizeConfig(cfg CryptoConfig) (CryptoConfig, error) {
	if cfg.HotIndex != 0 {
		return cfg, fmt.Errorf("crypto config: Gas wallet index must be zero")
	}
	cfg.Chains = slices.Clone(cfg.Chains)
	cfg.RPC = maps.Clone(cfg.RPC)
	cfg.RPCLimits = maps.Clone(cfg.RPCLimits)
	cfg.Confirmations = maps.Clone(cfg.Confirmations)
	cfg.ScanRange = maps.Clone(cfg.ScanRange)
	cfg.ScanConcurrency = maps.Clone(cfg.ScanConcurrency)
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
	if cfg.RPCThrottleMs < 0 || cfg.RPCThrottleMs > maxRPCThrottleMs {
		return cfg, fmt.Errorf("crypto config: rpc_throttle_ms must be between 0 and %d", maxRPCThrottleMs)
	}
	if err := normalizeRPCLimits(&cfg); err != nil {
		return cfg, err
	}
	if err := normalizeNativeScan(&cfg, def); err != nil {
		return cfg, err
	}
	seen := make(map[string]bool, len(cfg.Chains))
	chains := make([]string, 0, len(cfg.Chains))
	for _, chain := range cfg.Chains {
		if !IsEVMChain(chain) {
			return cfg, fmt.Errorf("crypto config: deposits are not implemented for chain %q", chain)
		}
		if !seen[chain] {
			chains = append(chains, chain)
			seen[chain] = true
		}
	}
	cfg.Chains = chains
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
	if cfg.ScanConcurrency == nil {
		cfg.ScanConcurrency = def.ScanConcurrency
	}
	for _, chain := range cfg.Chains {
		if _, ok := cfg.SweepMinPerAddress[chain]; !ok {
			cfg.SweepMinPerAddress[chain] = def.SweepMinPerAddress[chain]
		}
		if _, ok := cfg.GasTopUp[chain]; !ok && IsEVMChain(chain) {
			cfg.GasTopUp[chain] = def.GasTopUp[chain]
		}
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
	// A stored config from before the instant-credit switch has no key; JSON
	// leaves the pointer nil, which must mean "the shipped default", never OFF.
	if cfg.InstantCreditEnabled == nil {
		cfg.InstantCreditEnabled = def.InstantCreditEnabled
	}
	if cfg.InstantCreditMinAmount < 0 || math.IsNaN(cfg.InstantCreditMinAmount) || math.IsInf(cfg.InstantCreditMinAmount, 0) {
		// A negative / non-finite floor is a typo, not "credit nothing": it
		// would otherwise block every deposit and stall the whole channel.
		cfg.InstantCreditMinAmount = 0
	}
	// The native price feed is the only external dependency of crediting. An
	// empty value means "use the public default"; anything else must be an
	// absolute http(s) endpoint so a typo fails at config load, not silently
	// at every native deposit.
	cfg.NativePriceURL = strings.TrimSpace(cfg.NativePriceURL)
	if cfg.NativePriceURL == "" {
		cfg.NativePriceURL = def.NativePriceURL
	}
	if err := validateNativePriceURL(cfg.NativePriceURL); err != nil {
		return cfg, err
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
	for chain, value := range cfg.ScanConcurrency {
		if value != 0 {
			cfg.ScanConcurrency[chain] = clampInt(value, 1, 64)
		}
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
	if err := normalizeSweepDestinationLock(&cfg); err != nil {
		return cfg, err
	}
	if err := normalizeNotifyConfig(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// ValidateSweepReady reports why a real sweep cannot run yet. Empty is the
// expected first-run state; the message tells the operator what to set.
//
// The returned sentinels are used by the admin readiness endpoint to map the
// block to a stable reason code; the console never re-derives this by itself.
func (cfg CryptoConfig) ValidateSweepReady() error {
	// A settings row that no longer matches the pinned destination means either
	// a direct database edit or a rollback that bypassed the dedicated endpoint.
	// Sweeping is exactly how that difference turns into stolen deposits, so it
	// must refuse instead of "just using the new address".
	if err := cfg.EnforceSweepDestinationLock(); err != nil {
		return fmt.Errorf("sweep disabled: %w", err)
	}
	hot := strings.TrimSpace(cfg.HotWalletEVM)
	if hot == "" {
		return errSweepReadyDestinationMissing
	}
	if !validEVMAddress(hot) {
		return errSweepReadyDestinationInvalid
	}
	return nil
}

// errSweepReadyDestinationMissing / errSweepReadyDestinationInvalid let the
// readiness endpoint classify a blocked sweep without matching on message text.
var (
	errSweepReadyDestinationMissing = errors.New("sweep disabled: set the EVM hot wallet (receiving address) first")
	errSweepReadyDestinationInvalid = errors.New("sweep disabled: the EVM hot wallet is not a valid 0x-prefixed 20-byte address")
)

// isZeroAddress catches the all-zero placeholder: sending funds there burns
// them, and an unset field often round-trips as 0x000…0 rather than "".
func isZeroAddress(address string) bool {
	trimmed := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(address)), "0x")
	if trimmed == "" {
		return false
	}
	return strings.Trim(trimmed, "0") == ""
}

// validateNativePriceURL accepts an absolute http(s) ticker endpoint or a
// template carrying {symbol}. It runs at config load so a typo is reported to
// the operator instead of failing each native deposit later.
func validateNativePriceURL(raw string) error {
	if !strings.Contains(raw, "{symbol}") {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" {
			return fmt.Errorf("crypto config: native_price_url must be an absolute URL")
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return fmt.Errorf("crypto config: native_price_url must use http or https")
		}
		return nil
	}
	// A template still has to be absolute once the placeholder is filled.
	parsed, err := url.Parse(strings.ReplaceAll(raw, "{symbol}", "ETHUSDT"))
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("crypto config: native_price_url template must be an absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("crypto config: native_price_url must use http or https")
	}
	return nil
}

// validEVMAddress accepts only the canonical 0x-prefixed 20-byte hex form.// common.HexToAddress pads or truncates anything else without an error, so
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
	Enabled                *bool                         `json:"enabled"`
	Chains                 *[]string                     `json:"chains"`
	RPC                    *map[string]string            `json:"rpc"`
	RPCLimits              *map[string]RPCEndpointLimits `json:"rpc_limits"`
	RPCThrottleMs          *int                          `json:"rpc_throttle_ms"`
	SweepGlobalThreshold   *float64                      `json:"sweep_global_threshold"`
	SweepMinPerAddress     *map[string]float64           `json:"sweep_min_per_address"`
	GasTopUp               *map[string]float64           `json:"gas_topup"`
	HotWalletEVM           *string                       `json:"hot_wallet_evm"`
	HotWalletSolana        *string                       `json:"hot_wallet_solana"`
	Confirmations          *map[string]int               `json:"confirmations"`
	InstantCreditEnabled   *bool                         `json:"instant_credit_enabled"`
	InstantCreditMinAmount *float64                      `json:"instant_credit_min_amount"`
	ScanRange              *map[string]int               `json:"scan_range"`
	ScanConcurrency        *map[string]int               `json:"scan_concurrency"`
	CreditMinAmount        *float64                      `json:"credit_min_amount"`
	CreditMaxAmount        *float64                      `json:"credit_max_amount"`
	CreditMultiplier       *float64                      `json:"credit_multiplier"`
	NativePriceURL         *string                       `json:"native_price_url"`
	HotIndex               *uint32                       `json:"hot_index"`
	SweepAuto              *bool                         `json:"sweep_auto"`
	NativeScan             *string                       `json:"native_scan"`
	NativePollSeconds      *int                          `json:"native_poll_seconds"`
	ExplorerAPIKey         *string                       `json:"explorer_api_key"`
	ExplorerAPIURL         *map[string]string            `json:"explorer_api_url"`
	// Notify replaces the whole notification channel set when present. The
	// webhook URL is a bearer secret (it embeds the Bark device key), so the
	// public config view returns it blank; the console must send only changed
	// fields, otherwise an empty string disables the webhook.
	Notify *CryptoNotifyConfig `json:"notify"`
	// SweepDestinationLock is deliberately absent: callers must not be able to
	// write the lock through the generic config endpoint.
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
	if p.RPCLimits != nil {
		out.RPCLimits = *p.RPCLimits
	}
	if p.RPCThrottleMs != nil {
		out.RPCThrottleMs = *p.RPCThrottleMs
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
	if p.InstantCreditEnabled != nil {
		// Copy the value: the patch struct may be reused by the caller.
		out.InstantCreditEnabled = boolPtr(*p.InstantCreditEnabled)
	}
	if p.InstantCreditMinAmount != nil {
		out.InstantCreditMinAmount = *p.InstantCreditMinAmount
	}
	if p.ScanRange != nil {
		out.ScanRange = *p.ScanRange
	}
	if p.ScanConcurrency != nil {
		out.ScanConcurrency = *p.ScanConcurrency
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
	if p.NativePriceURL != nil {
		out.NativePriceURL = *p.NativePriceURL
	}
	if p.HotIndex != nil {
		out.HotIndex = *p.HotIndex
	}
	if p.SweepAuto != nil {
		out.SweepAuto = *p.SweepAuto
	}
	if p.NativeScan != nil {
		out.NativeScan = *p.NativeScan
	}
	if p.NativePollSeconds != nil {
		out.NativePollSeconds = *p.NativePollSeconds
	}
	if p.ExplorerAPIKey != nil {
		out.ExplorerAPIKey = *p.ExplorerAPIKey
	}
	if p.ExplorerAPIURL != nil {
		out.ExplorerAPIURL = *p.ExplorerAPIURL
	}
	if p.Notify != nil {
		out.Notify = p.Notify
	}
	return out
}

// configFingerprint hashes the entire normalized persisted snapshot. Every
// CryptoConfig field affects runtime configuration; none is a timestamp, nonce
// or display-only value. JSON sorts map keys, so identical snapshots stay stable
// and new persisted fields are covered automatically. Never log it or the JSON.
func configFingerprint(cfg CryptoConfig) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", errors.New("crypto config: cannot serialize configuration fingerprint")
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// RPCEndpointLimits is the per-endpoint eth_getLogs capability an operator can
// declare so the scanner sizes each query correctly instead of discovering the
// limit by rejection.
type RPCEndpointLimits struct {
	// MaxBlocks caps the block span of one eth_getLogs. 0 = unknown.
	MaxBlocks uint64 `json:"max_blocks,omitempty"`
	// MaxAddresses caps how many addresses may appear in the topic filter.
	MaxAddresses int `json:"max_addresses,omitempty"`
}
