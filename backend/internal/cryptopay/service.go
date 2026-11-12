package cryptopay

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"log/slog"
	"maps"
	"math/big"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/ledger"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/nativeprice"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

// TokenConfig is one watched asset on a chain. Symbol is display-only:
// crediting and sweeping key on Contract, and native coin uses the
// scanner.NativeToken sentinel in that field so it shares one key space with
// the ERC-20 contracts.
type TokenConfig struct {
	Contract string
	Decimals int
	Symbol   string
	// Native marks the chain's mainnet coin (ETH/BNB). It has no contract and
	// no Transfer event: the scanner finds it from transactions, and the
	// sweeper signs a plain value transfer instead of an ERC-20 transfer.
	Native bool
}

// PushNotifier delivers operator alerts. Implemented by the notify adapter;
// the narrow signature keeps this package free of a notify import.
type PushNotifier interface {
	PushToAdmins(ctx context.Context, title, body, group string) error
}

// SetPushNotifier wires operator alerts. Optional: nil stays silent.
func (s *Service) SetPushNotifier(p PushNotifier) {
	if s == nil {
		return
	}
	s.pushMu.Lock()
	s.push = p
	s.pushMu.Unlock()
}

func (s *Service) getPushNotifier() PushNotifier {
	if s == nil {
		return nil
	}
	s.pushMu.RLock()
	p := s.push
	s.pushMu.RUnlock()
	return p
}

// chainDisplayName renders a chain for a human-readable alert.
func chainDisplayName(chain string) string {
	switch chain {
	case ChainBSC:
		return "BSC"
	case ChainEthereum:
		return "Ethereum"
	case ChainPolygon:
		return "Polygon"
	case ChainPlasma:
		return "Plasma"
	case ChainArbitrum:
		return "Arbitrum"
	case ChainBase:
		return "Base"
	case ChainXLayer:
		return "X Layer"
	case ChainSolana:
		return "Solana"
	}
	return chain
}

// shortAddr keeps alerts readable; the full value is in the ledger.
func shortAddr(address string) string {
	if len(address) <= 14 {
		return address
	}
	return address[:8] + "…" + address[len(address)-6:]
}

// trimDecimalZeros renders a NUMERIC column value without the padding zeros
// NUMERIC(38,18) emits: "0.010000000000000000" -> "0.01", "10.000…" -> "10".
func trimDecimalZeros(value string) string {
	if !strings.Contains(value, ".") {
		return value
	}
	trimmed := strings.TrimRight(value, "0")
	return strings.TrimSuffix(trimmed, ".")
}

// toDisplayAmount renders raw units for an alert.
func toDisplayAmount(raw *big.Int, decimals int) string {
	if raw == nil {
		return "0"
	}
	if decimals <= 0 {
		return raw.String()
	}
	digits := raw.String()
	for len(digits) <= decimals {
		digits = "0" + digits
	}
	whole := digits[:len(digits)-decimals]
	frac := strings.TrimRight(digits[len(digits)-decimals:], "0")
	if frac == "" {
		return whole
	}
	return whole + "." + frac
}

// watchedTokens maps chain -> token contracts with decimals. Kept in code
// (not settings) because a wrong contract would credit the wrong asset;
// changing it should require a code review. Every contract below was
// EVM contracts are lowercase. Native
// USDC addresses follow developers.circle.com/stablecoins/usdc-contract-addresses.
// The previously accepted X Layer bridge remains explicit as USDC.e.
var watchedTokens = map[string][]TokenConfig{
	ChainEthereum: {
		// ETH is the chain's mainnet coin: no contract, 18 decimals, and it
		// arrives as a transaction value transfer rather than a Transfer log.
		{Contract: scanner.NativeToken, Decimals: 18, Symbol: "ETH", Native: true},
		{Contract: "0xdac17f958d2ee523a2206206994597c13d831ec7", Decimals: 6, Symbol: "USDT"},
		{Contract: "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48", Decimals: 6, Symbol: "USDC"},
	},
	ChainBSC: {
		// BNB is the chain's mainnet coin; USDT/USDC on BSC are 18-decimal
		// BEP-20 tokens whose contracts are unrelated to the native coin.
		{Contract: scanner.NativeToken, Decimals: 18, Symbol: "BNB", Native: true},
		{Contract: "0x55d398326f99059ff775485246999027b3197955", Decimals: 18, Symbol: "USDT"}, // BSC-USD (Tether)
		{Contract: "0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d", Decimals: 18, Symbol: "USDC"}, // USD Coin (Circle)
	},
	ChainPolygon: {
		{Contract: "0xc2132d05d31c914a87c6611c10748aeb04b58e8f", Decimals: 6, Symbol: "USDT"}, // Tether USD (PoS)
		{Contract: "0x3c499c542cef5e3811e1192ce70d8cc03d5c3359", Decimals: 6, Symbol: "USDC"}, // USD Coin (native)
	},
	ChainPlasma: {
		{Contract: "0xb8ce59fc3717ada4c02eadf9682a9e934f625ebb", Decimals: 6, Symbol: "USDT"}, // USDT0 on Plasma
		{Contract: "0x2d661c89d812261039af9764eceaaee884f5f67f", Decimals: 6, Symbol: "USDC"}, // Circle-issued native USDC
	},
	ChainArbitrum: {
		{Contract: "0xfd086bc7cd5c481dcc9c85ebe478a1c0b69fcbb9", Decimals: 6, Symbol: "USDT"}, // Tether USD (Arbitrum One)
		{Contract: "0xaf88d065e77c8cc2239327c5edb3a432268e5831", Decimals: 6, Symbol: "USDC"}, // USD Coin (native)
	},
	ChainBase: {
		{Contract: "0x833589fcd6edb6e08f4c7c32d4f71b54bda02913", Decimals: 6, Symbol: "USDC"}, // USD Coin (Base)
	},
	ChainXLayer: {
		{Contract: "0x779ded0c9e1022225f8e0630b35a9b54be713736", Decimals: 6, Symbol: "USDT"},   // USDT0 on X Layer
		{Contract: "0xb6ceceab302e2e4948951ee7843fc24e92933061", Decimals: 6, Symbol: "USDC"},   // Circle-issued native USDC
		{Contract: "0x74b7f16337b8972027f6196a17a631ac6de26d22", Decimals: 6, Symbol: "USDC.e"}, // Existing bridged token remains accepted
	},
}

func cryptoWorkersAllowed() bool {
	return !service.BackgroundJobsDisabled() && !mode.ReadOnly()
}

// tokenDecimals resolves a token contract's decimals for a chain, falling
// back to the stablecoin default (6) for unknown contracts.
func tokenDecimals(chain, contract string) int {
	for _, t := range watchedTokens[chain] {
		if strings.EqualFold(t.Contract, contract) {
			return t.Decimals
		}
	}
	return 6
}

// tokenSymbol resolves a token contract's display symbol for a chain.
func tokenSymbol(chain, contract string) string {
	for _, t := range watchedTokens[chain] {
		if strings.EqualFold(t.Contract, contract) {
			if t.Symbol != "" {
				return t.Symbol
			}
		}
	}
	return "USDT"
}

// nativeToken returns the chain's native-coin entry when that chain watches a
// mainnet coin. Chains without one keep their previous behaviour exactly.
func nativeToken(chain string) (TokenConfig, bool) {
	for _, t := range watchedTokens[chain] {
		if t.Native {
			return t, true
		}
	}
	return TokenConfig{}, false
}

// chainScanConfig renders one chain's scanner configuration from the watched
// asset table.
//
// The native coin is the fragile part: it must set Native/NativeDecimals (the
// block-level scan is the only way to see it) while staying out of Tokens,
// which is the eth_getLogs address filter and cannot contain a non-address
// sentinel. ok is false for a chain with nothing to watch.
func chainScanConfig(chain string, confirmations, maxRange int) (scanner.ChainConfig, bool) {
	tokens := watchedTokens[chain]
	if len(tokens) == 0 {
		return scanner.ChainConfig{}, false
	}
	contracts := make([]string, 0, len(tokens))
	decimals := make(map[string]int, len(tokens))
	config := scanner.ChainConfig{Chain: chain, MaxRange: uint64(maxRange)}
	for _, token := range tokens {
		decimals[token.Contract] = token.Decimals
		if token.Native {
			config.Native = true
			config.NativeDecimals = token.Decimals
			continue
		}
		contracts = append(contracts, token.Contract)
	}
	if len(contracts) == 0 && !config.Native {
		return scanner.ChainConfig{}, false
	}
	config.Tokens = contracts
	config.Confirmations = uint64(confirmations)
	config.Decimals = decimals
	return config, true
}

// sweepDecimalsByAsset maps chain|contract -> decimals for the balance reader.
// Native coin is included under its sentinel key, so a stored native row is
// rebuilt with 18 decimals instead of the stablecoin fallback of 6 (which would
// shift the amount by twelve orders of magnitude).
func sweepDecimalsByAsset() map[string]int {
	decimals := map[string]int{}
	for chain, tokens := range watchedTokens {
		for _, token := range tokens {
			decimals[tokenDecimalsKey(chain, token.Contract)] = token.Decimals
		}
	}
	return decimals
}

// sweepTokenSpecs renders the watched asset table into the executor's
// (chain|contract) -> TokenSpec map.
//
// The Native flag is load-bearing: the executor decides there whether a sweep
// is a plain value transfer (21000 gas, no calldata) or an ERC-20 transfer to
// a token contract. Dropping it here would make every native sweep fail
// validation as a malformed contract address.
func sweepTokenSpecs() map[string]sweeper.TokenSpec {
	specs := map[string]sweeper.TokenSpec{}
	for chain, tokens := range watchedTokens {
		for _, token := range tokens {
			if token.Contract == "" {
				continue
			}
			specs[tokenDecimalsKey(chain, token.Contract)] = sweeper.TokenSpec{
				Contract: token.Contract, Decimals: token.Decimals, Native: token.Native,
			}
		}
	}
	return specs
}

// evmContracts returns only the ERC-20 contract addresses watched on a chain.
// The native coin has no contract: scanner.NativeToken is not a hex address, so
// it must never reach an eth_getLogs address filter, a balanceOf call or a
// signed ERC-20 transfer.
func evmContracts(chain string) []string {
	tokens := watchedTokens[chain]
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if t.Native || t.Contract == "" {
			continue
		}
		out = append(out, t.Contract)
	}
	return out
}

// Service is the cryptopay facade: owns config snapshots, the RPC pools,
// the scanner worker, the ledger and the sweeper.
type Service struct {
	db       *sql.DB
	settings *service.SettingService
	creditor ledger.Creditor

	mu        sync.RWMutex
	config    CryptoConfig
	pools     map[string]*rpc.Pool
	worker    *scanner.Worker
	planner   *sweeper.Planner
	wallet    *wallet.Manager
	executor  *sweeper.Executor
	addresses *addresses.Service
	// nativePrice is the process-wide market feed. It is rebuilt only when the
	// configured endpoint changes, so its 60s cache survives scan passes and
	// manual confirmations share the same observations.
	nativePrice *nativeprice.Provider

	// maintCancel stops the pool top-up / scheduled sweep loop.
	maintCancel     context.CancelFunc
	securityCancel  context.CancelFunc
	discoveryCancel context.CancelFunc
	// nativeCancel stops the address-based native-coin poller. It is separate
	// from the block worker loop because its cadence is a per-address polling
	// interval, not the chain scan interval.
	nativeCancel context.CancelFunc
	// hotResolver caches the derived gas-wallet address across reloads so the
	// hot-wallet poll does not audit a derivation row per tick.
	hotResolver *hotAddressResolver
	// nativeGuardAt throttles the disabled-channel native outflow guard.
	nativeGuardMu  sync.Mutex
	nativeGuardAt  time.Time
	securityPaused atomic.Bool
	// push delivers operator alerts (unmatched deposits). Optional.
	// Keep its lock separate: reload holds mu while waiting for worker.Stop,
	// and the worker can still deliver an alert before it exits.
	pushMu sync.RWMutex
	push   PushNotifier
	// sweepMu serialises sweep runs: the manual admin trigger and the
	// scheduler must never send the same funds twice.
	sweepMu sync.Mutex
	// chainMon backs the admin node-status page (probe cache, sample window,
	// on-demand ticker). Read-only; the zero value is ready to use.
	chainMon chainMonitor
}

// NewService builds the facade and starts the scan worker when enabled.
func NewService(
	db *sql.DB,
	settings *service.SettingService,
	creditor ledger.Creditor,
	addressSvc *addresses.Service,
	appConfig *config.Config,
) *Service {
	var masterKey string
	if appConfig != nil && appConfig.Totp.HasPersistentEncryptionKey() {
		masterKey = appConfig.Totp.EncryptionKey
	}
	s := &Service{
		db:        db,
		settings:  settings,
		creditor:  creditor,
		pools:     map[string]*rpc.Pool{},
		wallet:    wallet.New(db, masterKey),
		addresses: addressSvc,
	}
	s.wallet.EnableSecurityMonitoring()
	s.hotResolver = &hotAddressResolver{svc: s}
	if addressSvc != nil {
		addressSvc.SetSecurityGuards(s.checkWalletActivity, s.validateWalletAssignment)
	}
	s.reload(context.Background())
	if masterKey != "" && cryptoWorkersAllowed() && service.CryptoInstanceEnabled() {
		securityCtx, cancel := context.WithCancel(context.Background())
		s.securityCancel = cancel
		go s.securityMonitor(securityCtx)
	}
	return s
}

// Reload re-reads config and (re)builds pools and the worker. Called at
// startup and after every admin config save.
func (s *Service) Reload(ctx context.Context) { s.reload(ctx) }

// applyRPCThrottle publishes the configured pacing so every endpoint waits its
// turn instead of firing together. Zero clears it.
func applyRPCThrottle(cfg CryptoConfig) {
	ms := cfg.RPCThrottleMs
	if ms < 0 {
		ms = 0
	}
	rpc.SetEndpointThrottleInterval(time.Duration(ms) * time.Millisecond)
}

func (s *Service) reload(ctx context.Context) {
	cfg, err := LoadConfig(ctx, NewServiceSettingStore(s.settings))
	if err != nil {
		slog.Warn("[cryptopay] config load failed", "error", err)
		return
	}
	// Two independent decisions share one stored row:
	//   channelEnabled - the admin switch customers see and the user API serves.
	//   scanEnabled    - whether THIS host runs the scanner, maintenance and
	//                    sweep loops. CRYPTO_INSTANCE_ENABLED only removes that
	//                    local work so a shared database is not scanned twice.
	// A host that delegates scanning still answers deposit-address and history
	// requests, so the deployment gate must not be folded into cfg.Enabled.
	channelEnabled := cfg.Enabled
	if channelEnabled && !mode.ReadOnly() {
		guardCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		guardErr := s.checkWalletSecurity(guardCtx)
		cancel()
		if guardErr != nil {
			channelEnabled = false
			slog.Warn("[cryptopay] channel paused by wallet security guard")
		}
	}
	cfg.Enabled = channelEnabled
	scanEnabled := channelEnabled && service.CryptoInstanceEnabled()

	// A queued run must not retain an executor built from obsolete config.
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	// The feed is cheap to build; its quote cache lives inside the provider.
	priceProvider := nativeprice.New(cfg.NativePriceURL)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.discoveryCancel != nil {
		s.discoveryCancel()
		s.discoveryCancel = nil
	}
	s.config = cfg
	s.nativePrice = priceProvider
	// An imported hot mnemonic changes the gas wallet address; the next poll
	// must derive it again instead of trusting the cached one.
	if s.hotResolver != nil {
		s.hotResolver.reset()
	}
	if cfg.Enabled {
		s.securityPaused.Store(false)
	}
	// Vault administration must remain available while deposits are paused.
	// The same in-process wallet survives configuration reloads. Persisted seeds load automatically when used.
	if err := s.wallet.SetDestination(cfg.HotWalletEVM); err != nil {
		slog.Error("[cryptopay] invalid wallet destination", "error", err)
	}
	// Disabling proactive jobs must not disable an authenticated manual sweep.
	// Read-only deployments still refuse both signing and wallet mutations.
	s.wallet.SetEnabled(scanEnabled && !mode.ReadOnly())
	if s.addresses != nil {
		s.addresses.SetDeriver(s.wallet)
	}
	// Keep the selected chains' RPC pools available for the independent
	// outflow guard even while new deposits and sweeps are disabled.
	applyRPCLimits(cfg)
	applyRPCThrottle(cfg)
	s.pools = poolsForConfig(cfg, s.pools)
	s.startRPCDiscoveryLocked()

	if !scanEnabled {
		if s.worker != nil {
			s.worker.Stop()
			s.worker = nil
		}
		s.stopNativePollerLocked()
		if s.maintCancel != nil {
			s.maintCancel()
			s.maintCancel = nil
		}
		s.executor = nil
		s.planner = nil
		return
	}

	// The map is immutable per worker; only each Pool's synchronized endpoint
	// list can change when verified automatic candidates become available.
	pools := s.pools

	// EVM networks use the same ledger and shared scan lock.
	var chainConfigs []scanner.ChainConfig
	for _, chain := range cfg.Chains {
		if !IsEVMChain(chain) {
			continue
		}
		chainConfig, ok := chainScanConfig(chain, cfg.Confirmations[chain], cfg.ScanRange[chain])
		if !ok {
			continue
		}
		// The native source decides which loop owns discovery, so the block
		// pass only reads blocks for the mainnet coin in fallback mode.
		chainConfig.NativeSource = cfg.NativeScan
		chainConfigs = append(chainConfigs, chainConfig)
	}

	cursor := scanner.NewCursorStore(s.db)
	led := ledger.New(s.db, s.creditor, newAddressResolver(s.addresses),
		cfg.CreditMinAmount, cfg.CreditMaxAmount, cfg.CreditMultiplier)
	led.SetNativePricer(priceProvider)
	// Funds that arrived but could not be credited are usually a mistyped
	// deposit or a probe: tell the operator instead of leaving it in a table.
	led.SetUnmatchedNotifier(func(ctx context.Context, t scanner.Transfer, reason string) {
		slog.Warn("[cryptopay] unmatched deposit",
			"chain", t.Chain, "tx", t.TxHash, "to", t.To, "reason", reason)
		push := s.getPushNotifier()
		if push == nil {
			return
		}
		amount := toDisplayAmount(t.Amount, tokenDecimals(t.Chain, t.Token))
		if err := push.PushToAdmins(ctx, "未匹配到账",
			fmt.Sprintf("%s 收到 %s（%s），未入账：%s",
				chainDisplayName(t.Chain), amount, shortAddr(t.To), reason),
			"deposit"); err != nil {
			slog.Warn("[cryptopay] unmatched deposit push failed", "error", err)
		}
	})
	addressLister := func(ctx context.Context, chain string) ([]string, error) {
		family, err := addresses.FamilyForChain(chain)
		if err != nil {
			return nil, err
		}
		return s.addresses.ActiveAddresses(ctx, family)
	}

	if s.worker != nil {
		s.worker.Stop()
	}
	// The previous poller writes through the previous worker; retire it before
	// publishing the replacement.
	s.stopNativePollerLocked()
	// 5s poll: finality is decided by the chain's safe tag, so a tighter
	// interval only shortens detection latency, not safety.
	s.worker = scanner.NewWorker(s.db, pools, chainConfigs, cursor, led, addressLister, 5*time.Second, 2000)
	s.worker.SetSecurityGuards(s.checkWalletActivity, s.scanWalletOutflows)
	// A reorged credited deposit means we paid out on a block that no longer
	// exists: the operator must reconcile it by hand.
	s.worker.SetReorgNotifier(func(chain, txHash string) {
		slog.Error("[cryptopay] credited deposit reorged, manual reconciliation needed",
			"chain", chain, "tx", txHash)
		push := s.getPushNotifier()
		if push == nil {
			return
		}
		if err := push.PushToAdmins(context.Background(), "链上重组：已入账交易被回滚",
			fmt.Sprintf("%s 的交易 %s 已被重组，此前已入账，请人工核对",
				chainDisplayName(chain), shortAddr(txHash)),
			"deposit"); err != nil {
			slog.Warn("[cryptopay] reorg push failed", "error", err)
		}
	})
	if cryptoWorkersAllowed() {
		s.worker.Start()
	}

	// Native-coin source. The default txlist poll costs one request per watched
	// address per interval, independent of the chain's block rate; the block
	// scan stays available as an explicit fallback, and even then the poller
	// still guards the hot wallet's native spend (deposits are already covered
	// by the block scan). A missing key is reported by the poller instead of
	// silently degrading to a block-rate scan that would exhaust the provider.
	if client, err := newExplorerClient(cfg); err != nil {
		slog.Error("[cryptopay] native txlist client unavailable", "cause", err)
	} else {
		poll := s.newNativePoll(cfg, chainConfigs, s.worker, client, addressLister)
		if cryptoWorkersAllowed() {
			s.startNativePoller(context.Background(), time.Duration(cfg.NativePollSeconds)*time.Second, poll)
		}
	}

	// Token decimals per chain: the planner and executor rebuild raw amounts
	// from NUMERIC columns, and a wrong exponent moves money by 1e12. The
	// key is chain|contract because one chain watches several assets.
	sweepDecimals := sweepDecimalsByAsset()
	sweepMin := make(map[string]float64, len(cfg.Chains))
	sweepConfirmations := make(map[string]uint64, len(pools))
	for _, chain := range cfg.Chains {
		sweepMin[chain] = cfg.SweepMinPerAddress[chain]
	}
	for chain := range pools {
		depth, exists := cfg.Confirmations[chain]
		if !exists {
			depth = DefaultCryptoConfig().Confirmations[chain]
		}
		sweepConfirmations[chain] = uint64(depth)
	}
	s.planner = sweeper.NewPlanner(s.db, newBalanceReader(s.db, sweepDecimals), sweeper.Config{
		GlobalThreshold: cfg.SweepGlobalThreshold,
		MinPerAddress:   sweepMin,
		GasTopUp:        cfg.GasTopUp,
		HotWalletEVM:    cfg.HotWalletEVM,
		HotWalletSolana: cfg.HotWalletSolana,
	}, pools)
	// The executor keys specs by chain|contract: one address may hold both
	// USDT and USDC, and each needs its own transfer spec. The Native flag is
	// what tells the executor to sign a value transfer instead of an ERC-20
	// transfer, so it must be carried through here.
	sweepTokens := sweepTokenSpecs()
	s.executor = sweeper.NewExecutor(s.db, pools, s.wallet, sweeper.Config{
		Confirmations:   sweepConfirmations,
		GlobalThreshold: cfg.SweepGlobalThreshold,
		MinPerAddress:   sweepMin,
		GasTopUp:        cfg.GasTopUp,
		HotWalletEVM:    cfg.HotWalletEVM,
	}, sweepTokens, newBalanceReader(s.db, sweepDecimals), cfg.HotIndex)

	if s.maintCancel != nil {
		s.maintCancel()
	}
	if cryptoWorkersAllowed() {
		maintCtx, maintCancel := context.WithCancel(context.Background())
		s.maintCancel = maintCancel
		go s.maintenance(maintCtx, cfg)
	}
	slog.Info("[cryptopay] started", "chains", cfg.Chains)
}

// maintenance keeps the address pool topped up and, when sweep_auto is on,
// runs the sweep on a timer. Both are no-ops for deployments that leave the
// defaults alone.
func (s *Service) maintenance(ctx context.Context, cfg CryptoConfig) {
	poolTicker := time.NewTicker(10 * time.Minute)
	defer poolTicker.Stop()
	// The admin config lives in the database, but the running process only
	// reads it at startup and on an authenticated save. Patching a row directly
	// — which is what an operator does at 05:00 when a provider runs out of
	// quota — therefore did nothing until the next restart, and a restart with
	// long-lived streams can take many minutes. Poll the stored config on the
	// same tick as the pool top-up and reload when it actually changed.
	cfgFingerprint := configFingerprint(cfg)
	// Sweeps move money: check rarely and only on the hour, so a fork that
	// enables it never sees more than one automatic run per sweep cycle.
	sweepTicker := time.NewTicker(time.Hour)
	defer sweepTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-poolTicker.C:
			if err := s.addresses.EnsurePool(ctx); err != nil {
				slog.Warn("[cryptopay] pool top-up failed", "error", err)
			}
			if reloaded, err := s.reloadIfConfigChanged(ctx, &cfgFingerprint); err != nil {
				slog.Warn("[cryptopay] config reload check failed", "error", err)
			} else if reloaded {
				slog.Info("[cryptopay] config changed in storage; reloaded")
			}
		case <-sweepTicker.C:
			// A swept record is a claim about the chain, not the chain itself.
			// Re-read the recent window even when automatic sweeps are off: a
			// manual run's rollback is just as real, and this is the only place
			// that would notice it.
			s.auditRecentSweeps(ctx)
			if !s.Config().SweepAuto {
				continue
			}
			maintCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
			summary, err := s.runSweep(maintCtx, false, false, nil)
			cancel()
			if err != nil {
				slog.Warn("[cryptopay] scheduled sweep failed", "error", err)
				continue
			}
			if summary.Swept > 0 || summary.Failed > 0 {
				slog.Info("[cryptopay] scheduled sweep done",
					"swept", summary.Swept, "failed", summary.Failed)
			}
		}
	}
}

// auditRecentSweeps re-reads the receipts of recently swept jobs against the
// canonical chain. A proven rollback turns the record into failed so the
// address can be planned again; an unreadable chain changes nothing.
func (s *Service) auditRecentSweeps(ctx context.Context) {
	s.mu.RLock()
	exec := s.executor
	s.mu.RUnlock()
	if exec == nil {
		return
	}
	auditCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	audited, rolledBack, err := exec.AuditSweptRecent(auditCtx)
	if err != nil {
		slog.Warn("[cryptopay] swept audit incomplete", "audited", audited,
			"rolled_back", rolledBack, "error", rpc.SafeError(err))
		return
	}
	if rolledBack > 0 {
		slog.Error("[cryptopay] swept audit found rolled back jobs", "audited", audited,
			"rolled_back", rolledBack)
	}
}

// Config returns the current config snapshot.
func (s *Service) Config() CryptoConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cfg := s.config
	if s.securityPaused.Load() {
		cfg.Enabled = false
	}
	if s.wallet != nil && s.wallet.SecurityLocked() {
		cfg.Enabled = false
	}
	cfg.Chains = slices.Clone(cfg.Chains)
	cfg.RPC = maps.Clone(cfg.RPC)
	cfg.Confirmations = maps.Clone(cfg.Confirmations)
	cfg.ScanRange = maps.Clone(cfg.ScanRange)
	cfg.SweepMinPerAddress = maps.Clone(cfg.SweepMinPerAddress)
	cfg.GasTopUp = maps.Clone(cfg.GasTopUp)
	return cfg
}

// ResolveDepositAddress implements the payment service's resolver: it
// returns the user's EVM deposit address (assigning one on first use) so
// the crypto channel can hand it to the checkout flow.
func (s *Service) ResolveDepositAddress(ctx context.Context, userID int64) (string, error) {
	if !s.Config().Enabled {
		return "", fmt.Errorf("crypto channel is disabled")
	}
	assigned, err := s.addresses.GetOrAssign(ctx, userID)
	if err != nil {
		return "", err
	}
	address := assigned[addresses.FamilyEVM]
	if strings.TrimSpace(address) == "" {
		return "", fmt.Errorf("no EVM deposit address available")
	}
	return address, nil
}

// BuildSweepPlan returns the current plan (dry-run: never executes).
func (s *Service) BuildSweepPlan(ctx context.Context) (*sweeper.Plan, error) {
	s.mu.RLock()
	planner := s.planner
	s.mu.RUnlock()
	if planner == nil {
		return nil, fmt.Errorf("cryptopay is disabled")
	}
	return planner.BuildManualPlan(ctx)
}

// SweepReadiness reports whether a real sweep has everything it needs.
func (s *Service) SweepReadiness() error {
	return s.Config().ValidateSweepReady()
}

// WalletStatus reports managed wallet configuration and readiness.
func (s *Service) WalletStatus(ctx context.Context) (*wallet.Status, error) {
	s.mu.RLock()
	wallet := s.wallet
	s.mu.RUnlock()
	if wallet == nil {
		return nil, fmt.Errorf("cryptopay is disabled")
	}
	return wallet.Status(ctx)
}

// HotAddresses returns the Gas wallet's public addresses. The console uses
// them for Gas funding and can explicitly choose one as a sweep destination.
func (s *Service) HotAddresses(ctx context.Context) (*sweeper.HotAddress, error) {
	s.mu.RLock()
	wallet := s.wallet
	s.mu.RUnlock()
	if wallet == nil {
		return nil, fmt.Errorf("cryptopay is disabled")
	}
	return wallet.HotAddresses(ctx)
}

// HotWalletStatus is the gas readiness of the sweep hot wallet per chain.
// A hot wallet without native coin cannot pay for sweeps, and the failure
// would otherwise only show up as failed sweep records.
type HotWalletStatus struct {
	// Address is the GAS PAYER: the wallet's Gas-seed address. This is the
	// wallet the operator must fund with native coin, which is not
	// necessarily the sweep receiving address.
	Address string `json:"address"`
	// Receiving is the configured sweep destination (where swept USDT
	// lands). It needs no gas.
	Receiving string `json:"receiving,omitempty"`
	Chain     string `json:"chain"`
	Balance   string `json:"balance"` // native coin, decimal
	Symbol    string `json:"symbol"`
	Enough    bool   `json:"enough"`
	Error     string `json:"error,omitempty"`
	RPCSource string `json:"rpc_source"`
	RPCReady  bool   `json:"rpc_ready"`
	ErrorCode string `json:"error_code,omitempty"`
}

// nativeSymbol is the gas coin of each supported chain.
func nativeSymbol(chain string) string {
	switch chain {
	case ChainBSC:
		return "BNB"
	case ChainEthereum:
		return "ETH"
	case ChainPolygon:
		return "POL"
	case ChainPlasma:
		return "XPL"
	case ChainArbitrum:
		return "ETH"
	case ChainBase:
		return "ETH"
	case ChainXLayer:
		return "OKB"
	case ChainSolana:
		return "SOL"
	}
	return "native"
}

// HotWalletStatuses reads the hot wallet balance on each enabled EVM chain.
// Readiness uses the estimated cost to fund one child, rather than requiring
// the full configured per-address funding limit to be held in this wallet.
func (s *Service) HotWalletStatuses(ctx context.Context) ([]HotWalletStatus, error) {
	cfg := s.Config()
	receiving := strings.TrimSpace(cfg.HotWalletEVM)
	addr, err := s.HotAddresses(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot determine gas wallet: %w", err)
	}
	gasWallet := strings.TrimSpace(addr.EVM)
	if !validEVMAddress(gasWallet) {
		return nil, fmt.Errorf("wallet returned an invalid gas wallet")
	}
	s.mu.RLock()
	pools := s.pools
	s.mu.RUnlock()
	var out []HotWalletStatus
	for _, chain := range cfg.Chains {
		if !IsEVMChain(chain) {
			continue
		}
		pool := pools[chain]
		st := HotWalletStatus{
			Address: gasWallet, Receiving: receiving,
			Chain: chain, Symbol: nativeSymbol(chain),
			RPCSource: "configured",
		}
		if strings.TrimSpace(cfg.RPC[chain]) == "" {
			st.RPCSource = "chainlist"
		}
		if pool == nil || len(pool.Endpoints()) == 0 {
			st.Error = "RPC endpoints are being discovered from Chainlist"
			st.ErrorCode = "RPC_DISCOVERY_PENDING"
			out = append(out, st)
			continue
		}
		raw, err := pool.Balance(ctx, gasWallet)
		if err != nil {
			st.Error = "RPC request failed; check endpoint configuration and server logs"
			st.ErrorCode = "RPC_UNAVAILABLE"
			out = append(out, st)
			continue
		}
		// Native coins on these chains are 18-decimal.
		st.Balance = toDisplayAmount(raw, 18)
		price, err := pool.GasPrice(ctx)
		if err != nil {
			st.Error = "RPC request failed; check endpoint configuration and server logs"
			st.ErrorCode = "RPC_UNAVAILABLE"
			out = append(out, st)
			continue
		}
		st.RPCReady = true
		st.Enough = raw.Cmp(sweeper.GasWalletRequirement(price)) >= 0
		out = append(out, st)
	}
	return out, nil
}

// InitializeWallet imports two independent seeds exactly once.
func (s *Service) InitializeWallet(ctx context.Context, depositMnemonic, hotMnemonic string) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	s.mu.RLock()
	wallet := s.wallet
	s.mu.RUnlock()
	if wallet == nil {
		return fmt.Errorf("cryptopay is disabled")
	}
	if err := wallet.Initialize(ctx, depositMnemonic, hotMnemonic); err != nil {
		return err
	}
	return s.checkWalletSecurity(ctx)
}

// RunSweep executes the sweep (or a dry-run) and returns the summary.
// Serialised by the caller via the admin route; sweeps are operator-driven.
func (s *Service) RunSweep(ctx context.Context, dryRun bool) (summary *sweeper.Summary, err error) {
	return s.runSweep(ctx, dryRun, true, nil)
}

// RunSweepChains is RunSweep restricted to the named chains. An empty list
// keeps the global behaviour, so existing callers are unaffected.
func (s *Service) RunSweepChains(ctx context.Context, dryRun bool, chains []string) (summary *sweeper.Summary, err error) {
	return s.runSweep(ctx, dryRun, true, chains)
}

func (s *Service) runSweep(ctx context.Context, dryRun, manual bool, chains []string) (summary *sweeper.Summary, err error) {
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	return s.runSweepLocked(ctx, dryRun, manual, chains)
}

// runSweepLocked is shared by synchronous and asynchronous runs. Its caller
// owns sweepMu; acquiring it again would deadlock every asynchronous run.
func (s *Service) runSweepLocked(ctx context.Context, dryRun, manual bool, chains []string) (summary *sweeper.Summary, err error) {
	if !dryRun {
		// Notify on the common result path: manual runs, scheduled sweeps and
		// resumption of already-signed jobs must report the same failures.
		// Notification errors never replace the actual transaction outcome.
		defer func() { s.reportSweepFailure(ctx, summary, err) }()
	}
	if !dryRun {
		if err := mode.CheckWritable(); err != nil {
			return nil, err
		}
		if s.wallet != nil {
			if err := s.checkWalletActivity(ctx); err != nil {
				return nil, err
			}
		}
	}
	s.mu.RLock()
	exec := s.executor
	s.mu.RUnlock()
	if exec == nil {
		return nil, fmt.Errorf("cryptopay is disabled")
	}
	// One run at a time: the manual trigger and the scheduler would
	// otherwise both read the same balances and double-send.
	if !dryRun && s.wallet != nil {
		authorized, revoke, err := s.wallet.AuthorizeSweep(ctx)
		if err != nil {
			return nil, err
		}
		defer revoke()
		ctx = authorized
	}
	if len(chains) > 0 {
		exec = exec.WithChains(chains)
	}
	if manual && !dryRun {
		// A single click must clear the whole queue. First put back failures
		// that never produced a transaction (safe: nothing was broadcast), then
		// run passes until no new retryable failure appears. Broadcast failures
		// stay untouched: retrying those needs an on-chain check, not a guess.
		if _, err := exec.ResetRetryableFailures(ctx, 200); err != nil {
			return nil, err
		}
		merged := &sweeper.Summary{DryRun: false}
		// Gas top-ups are serialized by the payer nonce, so one pass can only
		// sweep the addresses whose gas confirmed while it ran. Keep passing
		// until the queue is empty (or a retryable failure stops being reset),
		// otherwise a click only ever drains the first handful of addresses.
		for round := 0; round < 60; round++ {
			reset, rerr := 0, error(nil)
			if round > 0 {
				reset, rerr = exec.ResetRetryableFailures(ctx, 200)
				if rerr != nil {
					return merged, rerr
				}
				// Give the previous round's gas top-ups time to mine; without
				// this sleep every extra round just sees the same pending gas.
				select {
				case <-time.After(8 * time.Second):
				case <-ctx.Done():
					return merged, ctx.Err()
				}
			}
			pass, perr := exec.RunManualAndWait(ctx, dryRun)
			mergeSweepSummary(merged, pass)
			if perr != nil {
				return merged, perr
			}
			if pass.Pending == 0 && pass.Planned == 0 && reset == 0 {
				break
			}
		}
		return merged, nil
	}
	if manual {
		return exec.RunManualAndWait(ctx, dryRun)
	}
	return exec.RunAndWait(ctx, dryRun)
}

// SweepRunState is the in-memory record of one asynchronous sweep.
//
// The run itself already outlives the HTTP request (see the handler), but the
// operator still needs to know it is running and what it produced without
// holding a connection open for minutes. The state lives in memory on purpose:
// it describes one process's current work, not durable history — the durable
// record is sweep_records, which every job writes as it goes.
type SweepRunState struct {
	ID        string           `json:"id"`
	DryRun    bool             `json:"dry_run"`
	Chains    []string         `json:"chains"`
	StartedAt time.Time        `json:"started_at"`
	Finished  bool             `json:"finished"`
	Error     string           `json:"error,omitempty"`
	Summary   *sweeper.Summary `json:"summary,omitempty"`
}

var (
	sweepRunsMu sync.Mutex
	sweepRuns   = map[string]*SweepRunState{}
)

// sweepRunRetention bounds how many finished runs stay queryable, so a process
// that has been up for weeks does not accumulate them.
const sweepRunRetention = 20

// StartSweepAsync launches a sweep and returns immediately with a run id the
// caller can poll. Refuses when a run is already active: two concurrent sweeps
// would read the same balances and could double-send.
func (s *Service) StartSweepAsync(dryRun bool, chains []string) (*SweepRunState, error) {
	if !s.sweepMu.TryLock() {
		return nil, ErrScanBusy
	}
	// Run the cheap preflight in the request so a disabled wallet/security lock
	// is returned to the operator now instead of a 202 followed by an in-memory
	// error they can never see. The worker repeats it while holding the sweep.
	if !dryRun {
		preflightCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		preErr := func() error {
			if err := mode.CheckWritable(); err != nil {
				return err
			}
			if err := s.checkWalletActivity(preflightCtx); err != nil {
				return err
			}
			if s.wallet != nil {
				_, revoke, err := s.wallet.AuthorizeSweep(preflightCtx)
				if err != nil {
					return err
				}
				revoke()
			}
			return nil
		}()
		cancel()
		if preErr != nil {
			s.sweepMu.Unlock()
			return nil, preErr
		}
	}
	state := &SweepRunState{
		ID:        "sweep-" + uuid.NewString(),
		DryRun:    dryRun,
		Chains:    append([]string(nil), chains...),
		StartedAt: time.Now().UTC(),
	}
	sweepRunsMu.Lock()
	sweepRuns[state.ID] = state
	for id, run := range sweepRuns {
		if run.Finished && len(sweepRuns) > sweepRunRetention {
			delete(sweepRuns, id)
		}
	}
	sweepRunsMu.Unlock()
	// Do not expose the object the worker will update to the JSON encoder.
	initial := *state
	initial.Chains = slices.Clone(state.Chains)

	go func() {
		defer s.sweepMu.Unlock()
		// Detached from any request: a browser leaving must not cancel the run.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		summary, err := s.runSweepLocked(ctx, dryRun, true, state.Chains)
		sweepRunsMu.Lock()
		state.Finished = true
		state.Summary = summary
		if err != nil {
			state.Error = rpc.SafeError(err)
			// The async route returns 202 before the preflight runs, so without
			// this line the operator sees "failed" with no server-side evidence.
			slog.Error("cryptopay.sweep.async_failed", "run_id", state.ID,
				"chains", state.Chains, "error", rpc.SafeError(err))
		}
		sweepRunsMu.Unlock()
	}()
	return &initial, nil
}

// SweepRun returns the current state of an asynchronous run, or nil.
func (s *Service) SweepRun(id string) *SweepRunState {
	sweepRunsMu.Lock()
	defer sweepRunsMu.Unlock()
	run, ok := sweepRuns[id]
	if !ok {
		return nil
	}
	snapshot := *run
	snapshot.Chains = slices.Clone(run.Chains)
	return &snapshot
}

// SweepRuns lists known runs, newest first.
func (s *Service) SweepRuns() []*SweepRunState {
	sweepRunsMu.Lock()
	defer sweepRunsMu.Unlock()
	out := make([]*SweepRunState, 0, len(sweepRuns))
	for _, run := range sweepRuns {
		snapshot := *run
		snapshot.Chains = slices.Clone(run.Chains)
		out = append(out, &snapshot)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// SweepRecord is one sweep_records row for the admin UI.
type SweepRecord struct {
	ID          int64  `json:"id"`
	Chain       string `json:"chain"`
	FromAddress string `json:"from_address"`
	ToAddress   string `json:"to_address"`
	Token       string `json:"token"`
	Amount      string `json:"amount"`
	GasTxHash   string `json:"gas_tx_hash"`
	SweepTxHash string `json:"sweep_tx_hash"`
	Status      string `json:"status"`
	Error       string `json:"error"`
	CreatedAt   string `json:"created_at"`
	// JobVersion separates current-format jobs from legacy or hand-handled rows.
	// The admin console hides 0 by default so old records never mix with work.
	JobVersion int `json:"job_version"`
	// TokenSymbol and UpdatedAt are filled by ListSweepRecordsPage only.
	TokenSymbol string `json:"token_symbol"`
	UpdatedAt   string `json:"updated_at"`
}

// ReconcileSweep checks one stuck job against the chain without signing,
// broadcasting, or releasing an address based only on a missing receipt.
func (s *Service) ReconcileSweep(ctx context.Context, jobID int64) (*sweeper.ReconcileResult, error) {
	if jobID <= 0 {
		return nil, fmt.Errorf("invalid sweep job id")
	}
	// A sweep run holds sweepMu for its whole length (tens of seconds). Waiting
	// here left the admin's request hanging until the browser gave up; say so at once.
	if !s.sweepMu.TryLock() {
		return nil, ErrScanBusy
	}
	defer s.sweepMu.Unlock()
	s.mu.RLock()
	exec := s.executor
	s.mu.RUnlock()
	if exec == nil {
		return nil, fmt.Errorf("cryptopay is disabled")
	}
	return exec.Reconcile(ctx, jobID)
}

// ListBlockedSweeps returns unfinished jobs holding a reserved transaction.
func (s *Service) ListBlockedSweeps(ctx context.Context, limit int) ([]sweeper.BlockedJob, error) {
	s.mu.RLock()
	exec := s.executor
	s.mu.RUnlock()
	if exec == nil {
		return nil, nil
	}
	return exec.ListBlocked(ctx, limit)
}

// ListSweepRecords returns recent sweep records, optionally filtered.
func (s *Service) ListSweepRecords(ctx context.Context, chain string, limit int) ([]SweepRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT id, chain, from_address, to_address, token, amount::text,
	                 gas_tx_hash, sweep_tx_hash, status, error, created_at, job_version
	            FROM sweep_records`
	args := []any{}
	if chain != "" {
		query += ` WHERE chain = $1`
		args = append(args, chain)
	}
	query += ` ORDER BY id DESC LIMIT ` + fmt.Sprintf("%d", limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sweep records: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SweepRecord
	for rows.Next() {
		var r SweepRecord
		var created time.Time
		if err := rows.Scan(&r.ID, &r.Chain, &r.FromAddress, &r.ToAddress, &r.Token,
			&r.Amount, &r.GasTxHash, &r.SweepTxHash, &r.Status, &r.Error, &created, &r.JobVersion); err != nil {
			return nil, err
		}
		r.Amount = trimDecimalZeros(r.Amount)
		r.Error = rpc.SafeRecordedError(r.Error)
		r.CreatedAt = created.UTC().Format(time.RFC3339)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DepositRecord is one deposit_transactions row for reconciliation.
type DepositRecord struct {
	ID          int64  `json:"id"`
	Chain       string `json:"chain"`
	TxHash      string `json:"tx_hash"`
	LogIndex    int    `json:"log_index"`
	FromAddress string `json:"from_address"`
	ToAddress   string `json:"to_address"`
	Token       string `json:"token"`
	TokenSymbol string `json:"token_symbol"`
	Amount      string `json:"amount"`
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
	CreatedAt   string `json:"created_at"`
	// NativePriceUSD/At/Source answer "which price, observed when" for a
	// native-coin deposit. Empty for stablecoins, whose amount is already USD.
	NativePriceUSD    string `json:"native_price_usd,omitempty"`
	NativePriceAt     string `json:"native_price_at,omitempty"`
	NativePriceSource string `json:"native_price_source,omitempty"`
}

// scanDepositPrice fills the native settlement fields from nullable columns.
// Kept in one place so the three list/export queries cannot drift.
func (r *DepositRecord) scanDepositPrice(priceUSD sql.NullString, priceAt sql.NullTime, source sql.NullString) {
	r.NativePriceUSD = trimDecimalZeros(priceUSD.String)
	if priceAt.Valid {
		r.NativePriceAt = priceAt.Time.UTC().Format(time.RFC3339)
	}
	r.NativePriceSource = source.String
}

// ListDeposits returns recent deposit observations, optionally filtered.
func (s *Service) ListDeposits(ctx context.Context, chain, status string, limit int) ([]DepositRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT id, chain, tx_hash, log_index, from_address, to_address,
		       token, amount::text, status, reason, created_at,
		       COALESCE(CAST(native_price_usd AS TEXT), ''), native_price_at, COALESCE(native_price_source, '')
	            FROM deposit_transactions WHERE 1=1`
	args := []any{}
	if chain != "" {
		args = append(args, chain)
		query += fmt.Sprintf(" AND chain = $%d", len(args))
	}
	if status != "" {
		args = append(args, status)
		query += fmt.Sprintf(" AND status = $%d", len(args))
	}
	query += ` ORDER BY id DESC LIMIT ` + fmt.Sprintf("%d", limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list deposits: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []DepositRecord
	for rows.Next() {
		var r DepositRecord
		var created time.Time
		var priceUSD sql.NullString
		var priceAt sql.NullTime
		var source sql.NullString
		if err := rows.Scan(&r.ID, &r.Chain, &r.TxHash, &r.LogIndex, &r.FromAddress,
			&r.ToAddress, &r.Token, &r.Amount, &r.Status, &r.Reason, &created,
			&priceUSD, &priceAt, &source); err != nil {
			return nil, err
		}
		r.TokenSymbol = tokenSymbol(r.Chain, r.Token)
		r.Amount = trimDecimalZeros(r.Amount)
		r.Amount = trimDecimalZeros(r.Amount)
		r.CreatedAt = created.UTC().Format(time.RFC3339)
		r.scanDepositPrice(priceUSD, priceAt, source)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DepositSummary totals the deposit ledger for the console header.
type DepositSummary struct {
	Total    int64   `json:"total"`
	Credited int64   `json:"credited"`
	Pending  int64   `json:"pending"`
	Ignored  int64   `json:"ignored"`
	Amount   float64 `json:"amount"`
}

// ListDepositsPaged returns a page plus whole-set counters. The console needs
// both: the rows to inspect and the totals that must not move when paging.
func (s *Service) ListDepositsPaged(ctx context.Context, chain, status string, limit, offset int) ([]DepositRecord, *DepositSummary, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where := " WHERE 1=1"
	args := []any{}
	if chain != "" {
		args = append(args, chain)
		where += fmt.Sprintf(" AND chain = $%d", len(args))
	}
	if status != "" {
		args = append(args, status)
		where += fmt.Sprintf(" AND status = $%d", len(args))
	}

	var summary DepositSummary
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE status = 'credited'),
		       COUNT(*) FILTER (WHERE status = 'pending'),
		       COUNT(*) FILTER (WHERE status = 'ignored'),
		       COALESCE(SUM(amount) FILTER (WHERE status = 'credited'), 0)
		  FROM deposit_transactions`+where, args...).
		Scan(&summary.Total, &summary.Credited, &summary.Pending, &summary.Ignored, &summary.Amount)
	if err != nil {
		return nil, nil, fmt.Errorf("deposit summary: %w", err)
	}

	query := `SELECT id, chain, tx_hash, log_index, from_address, to_address,
	                 token, amount::text, status, reason, created_at,
	                 COALESCE(CAST(native_price_usd AS TEXT), ''), native_price_at, COALESCE(native_price_source, '')
	            FROM deposit_transactions` + where +
		fmt.Sprintf(" ORDER BY id DESC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	rows, err := s.db.QueryContext(ctx, query, append(args, limit, offset)...)
	if err != nil {
		return nil, nil, fmt.Errorf("list deposits: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []DepositRecord
	for rows.Next() {
		var r DepositRecord
		var created time.Time
		var priceUSD sql.NullString
		var priceAt sql.NullTime
		var source sql.NullString
		if err := rows.Scan(&r.ID, &r.Chain, &r.TxHash, &r.LogIndex, &r.FromAddress,
			&r.ToAddress, &r.Token, &r.Amount, &r.Status, &r.Reason, &created,
			&priceUSD, &priceAt, &source); err != nil {
			return nil, nil, err
		}
		r.TokenSymbol = tokenSymbol(r.Chain, r.Token)
		r.Amount = trimDecimalZeros(r.Amount)
		r.Amount = trimDecimalZeros(r.Amount)
		r.CreatedAt = created.UTC().Format(time.RFC3339)
		r.scanDepositPrice(priceUSD, priceAt, source)
		out = append(out, r)
	}
	return out, &summary, rows.Err()
}

// ListDepositsExportPage uses a descending primary-key cursor and deliberately
// skips the whole-ledger summary query. CSV export only needs rows; recomputing
// COUNT/SUM for every page repeats an increasingly expensive full table scan.
func (s *Service) ListDepositsExportPage(ctx context.Context, chain, status string, beforeID int64, limit int) ([]DepositRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	// Zero marks the first page; applying id < 0 would silently export no rows.
	where := " WHERE TRUE"
	var args []any
	if beforeID > 0 {
		where = " WHERE id < $1"
		args = append(args, beforeID)
	}
	if chain != "" {
		args = append(args, chain)
		where += fmt.Sprintf(" AND chain = $%d", len(args))
	}
	if status != "" {
		args = append(args, status)
		where += fmt.Sprintf(" AND status = $%d", len(args))
	}
	args = append(args, limit)
	query := `SELECT id, chain, tx_hash, log_index, from_address, to_address,
	                 token, amount::text, status, reason, created_at,
	                 COALESCE(CAST(native_price_usd AS TEXT), ''), native_price_at, COALESCE(native_price_source, '')
	            FROM deposit_transactions` + where +
		fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("export deposits: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []DepositRecord
	for rows.Next() {
		var r DepositRecord
		var created time.Time
		var priceUSD sql.NullString
		var priceAt sql.NullTime
		var source sql.NullString
		if err := rows.Scan(&r.ID, &r.Chain, &r.TxHash, &r.LogIndex, &r.FromAddress,
			&r.ToAddress, &r.Token, &r.Amount, &r.Status, &r.Reason, &created,
			&priceUSD, &priceAt, &source); err != nil {
			return nil, err
		}
		r.TokenSymbol = tokenSymbol(r.Chain, r.Token)
		r.Amount = trimDecimalZeros(r.Amount)
		r.CreatedAt = created.UTC().Format(time.RFC3339)
		r.scanDepositPrice(priceUSD, priceAt, source)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DepositInfo is everything the standalone deposit page needs.
//
// There is deliberately no response-wide rate. A rate is a property of the
// asset being sent, not of the account: since native deposits settle at the
// market price observed when they are credited (PR #306), the old top-level
// `rate` told every client that 1 ETH was worth the stablecoin multiplier.
// Clients must read DepositAsset.Rate / MarketPriced instead.
type DepositInfo struct {
	Enabled bool           `json:"enabled"`
	Chains  []DepositChain `json:"chains"`
	// MinAmount / MaxAmount are the credit thresholds. A stablecoin is
	// compared by its token amount (already USD-denominated); a market-priced
	// asset is compared by USD notional (amount × price at credit time), so a
	// client must label them as USD for that asset, never as a coin amount.
	MinAmount float64 `json:"min_amount"`
	MaxAmount float64 `json:"max_amount"`
}

// DepositAsset is one asset a user can send to a chain's deposit address.
// Native distinguishes ETH/BNB from the ERC-20 tokens so the UI can label the
// asset correctly: the address and QR code are the same for both.
type DepositAsset struct {
	Symbol string `json:"symbol"`
	Native bool   `json:"native"`
	// MarketPriced marks an asset that is settled at the market price
	// observed when the deposit is credited (ETH, BNB). A fixed rate must
	// never be rendered for it: Rate is null.
	MarketPriced bool `json:"market_priced"`
	// Rate is how many balance units one whole asset is worth under the fixed
	// rate. It is set for stablecoins and null for market-priced assets,
	// where a constant number would misstate what the user receives.
	Rate *float64 `json:"rate"`
	// ReferencePriceUSD is a display-only quote for a market-priced asset
	// ("about $3,210 per ETH"). It is best-effort: absent whenever the feed is
	// unavailable, and it is never used to compute a credit.
	ReferencePriceUSD *string `json:"reference_price_usd,omitempty"`
}

// DepositChain is one chain's deposit address.
type DepositChain struct {
	Chain   string   `json:"chain"`
	Address string   `json:"address"`
	Tokens  []string `json:"tokens"` // supported symbols on this chain, e.g. ["USDT","USDC"]
	// Assets is Tokens with the native-coin flag, in the same order. Kept
	// alongside Tokens so an older client that only reads Tokens still shows
	// every accepted symbol.
	Assets []DepositAsset `json:"assets"`
}

// nativeReferencePriceTimeout caps the whole "about $X" decoration pass, so a
// hung ticker can delay the deposit page by seconds but never fail it. The
// price is a courtesy for the user, not part of the credit calculation.
const nativeReferencePriceTimeout = 2 * time.Second

// DepositInfo resolves (assigning on first use) the user's addresses.
func (s *Service) DepositInfo(ctx context.Context, userID int64) (*DepositInfo, error) {
	cfg := s.Config()
	if !cfg.Enabled {
		return &DepositInfo{Enabled: false}, nil
	}
	assigned, err := s.addresses.GetOrAssign(ctx, userID)
	if err != nil {
		return nil, err
	}
	info := buildDepositInfo(cfg, assigned)
	s.attachNativeReferencePrices(ctx, info)
	return info, nil
}

// buildDepositInfo renders the public deposit page from config alone. Kept
// pure so the pricing contract can be tested without a database: every asset
// declares how it is priced, and only fixed-rate assets carry a rate.
func buildDepositInfo(cfg CryptoConfig, assigned map[string]string) *DepositInfo {
	rate := cfg.CreditMultiplier
	info := &DepositInfo{
		Enabled:   true,
		MinAmount: cfg.CreditMinAmount,
		MaxAmount: cfg.CreditMaxAmount,
	}
	for _, chain := range cfg.Chains {
		family := addresses.FamilyEVM
		if chain == ChainSolana {
			family = addresses.FamilySolana
		}
		address := assigned[family]
		if address == "" {
			continue
		}
		symbols := make([]string, 0, len(watchedTokens[chain]))
		assets := make([]DepositAsset, 0, len(watchedTokens[chain]))
		for _, token := range watchedTokens[chain] {
			if token.Symbol == "" {
				continue
			}
			symbols = append(symbols, token.Symbol)
			asset := DepositAsset{Symbol: token.Symbol, Native: token.Native}
			if token.Native {
				// Native coin: settled at the market price at credit time, so
				// no fixed rate is exposed at all.
				asset.MarketPriced = true
			} else {
				// Stablecoin: the token amount is the USD amount, so the
				// configured multiplier still applies directly.
				fixed := rate
				asset.Rate = &fixed
			}
			assets = append(assets, asset)
		}
		if len(symbols) == 0 {
			continue
		}
		info.Chains = append(info.Chains, DepositChain{Chain: chain, Address: address, Tokens: symbols, Assets: assets})
	}
	return info
}

// attachNativeReferencePrices fills ReferencePriceUSD for market-priced assets.
// Every failure path is silent by design: no quote, stale quote, unmapped
// chain, no configured feed or an exhausted time budget all leave the field
// absent. The deposit page must render with or without a market quote.
func (s *Service) attachNativeReferencePrices(parent context.Context, info *DepositInfo) {
	if info == nil || s.nativePrice == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, nativeReferencePriceTimeout)
	defer cancel()
	for ci := range info.Chains {
		chain := info.Chains[ci].Chain
		for ai := range info.Chains[ci].Assets {
			asset := &info.Chains[ci].Assets[ai]
			if !asset.MarketPriced || asset.ReferencePriceUSD != nil {
				continue
			}
			if ctx.Err() != nil {
				// The budget is spent: leave the remaining assets unquoted
				// rather than making the page wait for another exchange call.
				return
			}
			quote, err := s.nativePrice.Quote(ctx, chain)
			if err != nil || !quote.Usable(time.Now()) {
				continue
			}
			price := trimDecimalZeros(quote.USD.String())
			asset.ReferencePriceUSD = &price
		}
	}
}

// UserDepositRecord is one deposit row for the user's own history.
type UserDepositRecord struct {
	Chain     string  `json:"chain"`
	TxHash    string  `json:"tx_hash"`
	Token     string  `json:"token"`
	Amount    string  `json:"amount"`
	Status    string  `json:"status"`
	Reason    string  `json:"reason,omitempty"` // human text for non-credited rows
	CreatedAt string  `json:"created_at"`
	Credited  float64 `json:"credited"`
	// CreditedKnown distinguishes a resolved settlement amount from legacy
	// credited rows whose payment order can no longer be resolved. A numeric
	// zero must not be presented as a confirmed zero-credit result.
	CreditedKnown *bool `json:"credited_known,omitempty"`
	// BlockNumber, Confirmations and RequiredConfirmations drive the
	// "confirming n/m" progress for pending deposits.
	BlockNumber           uint64 `json:"block_number"`
	Confirmations         int    `json:"confirmations"`
	RequiredConfirmations int    `json:"required_confirmations"`
}

// UserDepositHistory lists the caller's deposits against their addresses.
func (s *Service) UserDepositHistory(ctx context.Context, userID int64, limit int) ([]UserDepositRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.chain, d.tx_hash, d.token, d.amount::text, d.status, d.reason, d.created_at,
		       COALESCE(o.amount, 0), d.log_index, d.order_id, d.block_number, d.confirmations
		  FROM deposit_transactions d
		  JOIN deposit_addresses a ON
		       (a.chain = 'evm' AND d.chain <> 'solana' AND lower(a.address) = lower(d.to_address))
		       OR (a.chain = 'solana' AND d.chain = 'solana' AND a.address = d.to_address)
		  LEFT JOIN payment_orders o ON o.id = d.order_id AND o.user_id = a.user_id
		 WHERE a.user_id = $1
		 ORDER BY d.id DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("user deposit history: %w", err)
	}
	defer func() { _ = rows.Close() }()
	required := s.Config().Confirmations
	var out []UserDepositRecord
	legacy := map[string][]int{}
	for rows.Next() {
		var r UserDepositRecord
		var created time.Time
		var logIndex int
		var orderID sql.NullInt64
		if err := rows.Scan(&r.Chain, &r.TxHash, &r.Token, &r.Amount, &r.Status, &r.Reason, &created, &r.Credited, &logIndex, &orderID,
			&r.BlockNumber, &r.Confirmations); err != nil {
			return nil, err
		}
		r.RequiredConfirmations = required[r.Chain]
		if r.Status == "credited" && r.Confirmations < r.RequiredConfirmations {
			// Rows credited before the counter existed stored 0. Credit already
			// required full depth, so show the requirement instead of 0/n.
			r.Confirmations = r.RequiredConfirmations
		}
		if r.Status == "credited" {
			known := orderID.Valid
			r.CreditedKnown = &known
		}
		// The user sees the symbol; the raw contract stays internal.
		r.Token = tokenSymbol(r.Chain, r.Token)
		r.Amount = trimDecimalZeros(r.Amount)
		// Rejected transfers must say why: a bare "ignored" badge reads as a
		// random failure and invites support tickets. The machine code goes
		// out as-is; the UI localizes it with the current thresholds.
		r.CreatedAt = created.UTC().Format(time.RFC3339)
		if r.Status != "credited" {
			r.Credited = 0
		} else if !orderID.Valid {
			_, identity := depositOrderIdentity(r.Chain, r.TxHash, logIndex)
			legacy[identity] = append(legacy[identity], len(out))
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// Old credited rows predate order_id linking. Resolve all deterministic
	// identities in one indexed batch, without repricing or per-row queries.
	if len(legacy) > 0 {
		args := []any{userID}
		params := make([]string, 0, len(legacy))
		for identity := range legacy {
			args = append(args, identity)
			params = append(params, fmt.Sprintf("$%d", len(args)))
		}
		orders, err := s.db.QueryContext(ctx, `SELECT out_trade_no, amount FROM payment_orders
			WHERE user_id = $1 AND payment_type = 'crypto' AND status = 'COMPLETED'
			AND out_trade_no IN (`+strings.Join(params, ",")+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("load historical credit amounts: %w", err)
		}
		defer func() { _ = orders.Close() }()
		for orders.Next() {
			var identity string
			var amount float64
			if err := orders.Scan(&identity, &amount); err != nil {
				return nil, err
			}
			for _, index := range legacy[identity] {
				out[index].Credited = amount
				known := true
				out[index].CreditedKnown = &known
			}
		}
		if err := orders.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// applyRPCLimits publishes the operator-declared per-endpoint capabilities so
// the scanner can size an eth_getLogs before sending it. Replacing the whole
// registry means removing an entry takes effect on reload, and an empty map
// restores the previous "learn the limit by rejection" behaviour.
func applyRPCLimits(cfg CryptoConfig) {
	limits := make(map[string]rpc.EndpointLimits, len(cfg.RPCLimits))
	for endpoint, declared := range cfg.RPCLimits {
		limits[endpoint] = rpc.EndpointLimits{
			MaxBlocks:    declared.MaxBlocks,
			MaxAddresses: declared.MaxAddresses,
		}
	}
	rpc.ReplaceEndpointLimits(limits)
}

// reloadIfConfigChanged re-reads the stored config and rebuilds pools and the
// worker when the operator edited it out-of-band. Returns whether it reloaded.
//
// The comparison is against the last fingerprint this process applied, so a
// successful reload updates the caller's marker: a storage failure then retries
// on the next tick instead of being masked by a stale marker.
func (s *Service) reloadIfConfigChanged(ctx context.Context, last *string) (bool, error) {
	stored, err := LoadConfig(ctx, NewServiceSettingStore(s.settings))
	if err != nil {
		return false, err
	}
	if configFingerprint(stored) == *last {
		return false, nil
	}
	// Reload reads the config again itself; only the fingerprint matters here.
	s.Reload(ctx)
	s.mu.RLock()
	applied := s.config
	s.mu.RUnlock()
	*last = configFingerprint(applied)
	return true, nil
}

func mergeSweepSummary(dst, src *sweeper.Summary) {
	if dst == nil || src == nil {
		return
	}
	dst.Planned += src.Planned
	dst.Swept += src.Swept
	dst.Failed += src.Failed
	dst.Pending += src.Pending
	dst.GasTopUp += src.GasTopUp
	dst.Entries = append(dst.Entries, src.Entries...)
}
