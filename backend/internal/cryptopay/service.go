package cryptopay

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/ledger"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// TokenConfig is one watched token on a chain. Symbol is display-only:
// crediting and sweeping always key on the contract address.
type TokenConfig struct {
	Contract string
	Decimals int
	Symbol   string
}

// PushNotifier delivers operator alerts. Implemented by the notify adapter;
// the narrow signature keeps this package free of a notify import.
type PushNotifier interface {
	PushToAdmins(ctx context.Context, title, body, group string) error
}

// SetPushNotifier wires operator alerts. Optional: nil stays silent.
func (s *Service) SetPushNotifier(p PushNotifier) {
	s.push = p
}

// chainDisplayName renders a chain for a human-readable alert.
func chainDisplayName(chain string) string {
	switch chain {
	case ChainBSC:
		return "BSC"
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
// verified on-chain (symbol + decimals) against the upstream bepusdt
// constant table on 2026-10-14; all addresses are lowercase to match the
// scanner's log decoding. Only canonical issuers are listed: Tether's
// bridged variants and third-party look-alikes are deliberately excluded.
var watchedTokens = map[string][]TokenConfig{
	ChainBSC: {
		{Contract: "0x55d398326f99059ff775485246999027b3197955", Decimals: 18, Symbol: "USDT"}, // BSC-USD (Tether)
		{Contract: "0x8ac76a51cc950d9822d68b83fe1ad97b32cd580d", Decimals: 18, Symbol: "USDC"}, // USD Coin (Circle)
	},
	ChainPolygon: {
		{Contract: "0xc2132d05d31c914a87c6611c10748aeb04b58e8f", Decimals: 6, Symbol: "USDT"}, // Tether USD (PoS)
		{Contract: "0x3c499c542cef5e3811e1192ce70d8cc03d5c3359", Decimals: 6, Symbol: "USDC"}, // USD Coin (native)
	},
	ChainPlasma: {
		{Contract: "0xb8ce59fc3717ada4c02eadf9682a9e934f625ebb", Decimals: 6, Symbol: "USDT"}, // USDT0 on Plasma
	},
	ChainArbitrum: {
		{Contract: "0xfd086bc7cd5c481dcc9c85ebe478a1c0b69fcbb9", Decimals: 6, Symbol: "USDT"}, // Tether USD (Arbitrum One)
		{Contract: "0xaf88d065e77c8cc2239327c5edb3a432268e5831", Decimals: 6, Symbol: "USDC"}, // USD Coin (native)
	},
	ChainBase: {
		{Contract: "0x833589fcd6edb6e08f4c7c32d4f71b54bda02913", Decimals: 6, Symbol: "USDC"}, // USD Coin (Base)
	},
	ChainXLayer: {
		{Contract: "0x779ded0c9e1022225f8e0630b35a9b54be713736", Decimals: 6, Symbol: "USDT"}, // USDT0 on X Layer
		{Contract: "0x74b7f16337b8972027f6196a17a631ac6de26d22", Decimals: 6, Symbol: "USDC"}, // USD Coin (X Layer)
	},
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
	signer    *sweeper.SignerClient
	executor  *sweeper.Executor
	addresses *addresses.Service

	// maintCancel stops the pool top-up / scheduled sweep loop.
	maintCancel context.CancelFunc
	// push delivers operator alerts (unmatched deposits). Optional.
	push PushNotifier
	// sweepMu serialises sweep runs: the manual admin trigger and the
	// scheduler must never send the same funds twice.
	sweepMu sync.Mutex
}

// NewService builds the facade and starts the scan worker when enabled.
func NewService(
	db *sql.DB,
	settings *service.SettingService,
	creditor ledger.Creditor,
	addressSvc *addresses.Service,
) *Service {
	s := &Service{
		db:        db,
		settings:  settings,
		creditor:  creditor,
		pools:     map[string]*rpc.Pool{},
		addresses: addressSvc,
	}
	s.reload(context.Background())
	return s
}

// Reload re-reads config and (re)builds pools and the worker. Called at
// startup and after every admin config save.
func (s *Service) Reload(ctx context.Context) { s.reload(ctx) }

func (s *Service) reload(ctx context.Context) {
	cfg, err := LoadConfig(ctx, NewServiceSettingStore(s.settings))
	if err != nil {
		slog.Warn("[cryptopay] config load failed", "error", err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = cfg

	if !cfg.Enabled {
		if s.worker != nil {
			s.worker.Stop()
			s.worker = nil
		}
		if s.maintCancel != nil {
			s.maintCancel()
			s.maintCancel = nil
		}
		s.executor = nil
		return
	}

	for _, chain := range cfg.Chains {
		pool, ok := s.pools[chain]
		if !ok {
			pool = rpc.NewPool(cfg.RPC[chain])
			s.pools[chain] = pool
		} else {
			pool.SetEndpoints(cfg.RPC[chain])
		}
	}

	// EVM chains only in this build; Solana scanning lands separately.
	var chainConfigs []scanner.ChainConfig
	for _, chain := range cfg.Chains {
		if chain == ChainSolana {
			continue
		}
		tokens := watchedTokens[chain]
		if len(tokens) == 0 {
			continue
		}
		contracts := make([]string, 0, len(tokens))
		decimals := make(map[string]int, len(tokens))
		for _, token := range tokens {
			contracts = append(contracts, token.Contract)
			decimals[token.Contract] = token.Decimals
		}
		chainConfigs = append(chainConfigs, scanner.ChainConfig{
			Chain:         chain,
			Tokens:        contracts,
			Confirmations: uint64(cfg.Confirmations[chain]),
			Decimals:      decimals,
			MaxRange:      uint64(cfg.ScanRange[chain]),
		})
	}

	cursor := scanner.NewCursorStore(s.db)
	led := ledger.New(s.db, s.creditor, newAddressResolver(s.addresses),
		cfg.CreditMinAmount, cfg.CreditMaxAmount, cfg.CreditMultiplier)
	// Funds that arrived but could not be credited are usually a mistyped
	// deposit or a probe: tell the operator instead of leaving it in a table.
	led.SetUnmatchedNotifier(func(ctx context.Context, t scanner.Transfer, reason string) {
		slog.Warn("[cryptopay] unmatched deposit",
			"chain", t.Chain, "tx", t.TxHash, "to", t.To, "reason", reason)
		if s.push == nil {
			return
		}
		amount := toDisplayAmount(t.Amount, tokenDecimals(t.Chain, t.Token))
		if err := s.push.PushToAdmins(ctx, "未匹配到账",
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
	// 5s poll: finality is decided by the chain's safe tag, so a tighter
	// interval only shortens detection latency, not safety.
	s.worker = scanner.NewWorker(s.db, s.pools, chainConfigs, cursor, led, addressLister, 5*time.Second, 2000)
	// A reorged credited deposit means we paid out on a block that no longer
	// exists: the operator must reconcile it by hand.
	s.worker.SetReorgNotifier(func(chain, txHash string) {
		slog.Error("[cryptopay] credited deposit reorged, manual reconciliation needed",
			"chain", chain, "tx", txHash)
		if s.push == nil {
			return
		}
		if err := s.push.PushToAdmins(context.Background(), "链上重组：已入账交易被回滚",
			fmt.Sprintf("%s 的交易 %s 已被重组，此前已入账，请人工核对",
				chainDisplayName(chain), shortAddr(txHash)),
			"deposit"); err != nil {
			slog.Warn("[cryptopay] reorg push failed", "error", err)
		}
	})
	s.worker.Start()

	s.signer = sweeper.NewSignerClient(cfg.SignerSocket)
	// Token decimals per chain: the planner and executor rebuild raw amounts
	// from NUMERIC columns, and a wrong exponent moves money by 1e12. The
	// key is chain|contract because one chain watches several tokens.
	sweepDecimals := map[string]int{}
	for chain, tokens := range watchedTokens {
		for _, token := range tokens {
			sweepDecimals[tokenDecimalsKey(chain, token.Contract)] = token.Decimals
		}
	}
	s.planner = sweeper.NewPlanner(s.db, newBalanceReader(s.db, sweepDecimals), sweeper.Config{
		GlobalThreshold: cfg.SweepGlobalThreshold,
		MinPerAddress:   cfg.SweepMinPerAddress,
		HotWalletEVM:    cfg.HotWalletEVM,
		HotWalletSolana: cfg.HotWalletSolana,
	}, s.pools)
	// The executor keys specs by chain|contract: one address may hold both
	// USDT and USDC, and each needs its own transfer spec.
	sweepTokens := map[string]sweeper.TokenSpec{}
	for chain, tokens := range watchedTokens {
		for _, token := range tokens {
			if token.Contract != "" {
				sweepTokens[tokenDecimalsKey(chain, token.Contract)] = sweeper.TokenSpec{
					Contract: token.Contract, Decimals: token.Decimals,
				}
			}
		}
	}
	s.executor = sweeper.NewExecutor(s.db, s.pools, s.signer, sweeper.Config{
		GlobalThreshold: cfg.SweepGlobalThreshold,
		MinPerAddress:   cfg.SweepMinPerAddress,
		GasTopUp:        cfg.GasTopUp,
		HotWalletEVM:    cfg.HotWalletEVM,
	}, sweepTokens, newBalanceReader(s.db, sweepDecimals), cfg.HotIndex)

	if s.maintCancel != nil {
		s.maintCancel()
	}
	maintCtx, maintCancel := context.WithCancel(context.Background())
	s.maintCancel = maintCancel
	go s.maintenance(maintCtx, cfg)
	slog.Info("[cryptopay] started", "chains", cfg.Chains)
}

// maintenance keeps the address pool topped up and, when sweep_auto is on,
// runs the sweep on a timer. Both are no-ops for deployments that leave the
// defaults alone.
func (s *Service) maintenance(ctx context.Context, cfg CryptoConfig) {
	poolTicker := time.NewTicker(10 * time.Minute)
	defer poolTicker.Stop()
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
		case <-sweepTicker.C:
			if !s.Config().SweepAuto {
				continue
			}
			maintCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
			summary, err := s.RunSweep(maintCtx, false)
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

// Config returns the current config snapshot.
func (s *Service) Config() CryptoConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
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
	return planner.BuildPlan(ctx)
}

// SweepReadiness reports whether a real sweep has everything it needs.
func (s *Service) SweepReadiness() error {
	return s.Config().ValidateSweepReady()
}

// SignerStatus reports the signing service's lock state. A locked signer
// holds no seeds, so the console must ask for the passphrase first.
func (s *Service) SignerStatus(ctx context.Context) (*sweeper.SignerStatus, error) {
	s.mu.RLock()
	signer := s.signer
	s.mu.RUnlock()
	if signer == nil {
		return nil, fmt.Errorf("cryptopay is disabled")
	}
	return signer.Status(ctx)
}

// HotWalletStatus is the gas readiness of the sweep hot wallet per chain.
// A hot wallet without native coin cannot pay for sweeps, and the failure
// would otherwise only show up as failed sweep records.
type HotWalletStatus struct {
	Address string `json:"address"`
	Chain   string `json:"chain"`
	Balance string `json:"balance"` // native coin, decimal
	Symbol  string `json:"symbol"`
	Enough  bool   `json:"enough"`
	Error   string `json:"error,omitempty"`
}

// nativeSymbol is the gas coin of each supported chain.
func nativeSymbol(chain string) string {
	switch chain {
	case ChainBSC:
		return "BNB"
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
// The threshold is the per-chain gas top-up setting: below it a sweep would
// have to fund gas first, which is what the operator wants to know.
func (s *Service) HotWalletStatuses(ctx context.Context) ([]HotWalletStatus, error) {
	cfg := s.Config()
	hot := strings.TrimSpace(cfg.HotWalletEVM)
	if hot == "" {
		return nil, fmt.Errorf("hot_wallet_evm is not configured")
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
		st := HotWalletStatus{Address: hot, Chain: chain, Symbol: nativeSymbol(chain)}
		if pool == nil {
			st.Error = "no rpc endpoint configured"
			out = append(out, st)
			continue
		}
		raw, err := pool.Balance(ctx, hot)
		if err != nil {
			st.Error = err.Error()
			out = append(out, st)
			continue
		}
		// Native coins on these chains are 18-decimal.
		st.Balance = toDisplayAmount(raw, 18)
		need := cfg.GasTopUp[chain]
		if need <= 0 {
			need = 0.002
		}
		var have float64
		_, _ = fmt.Sscanf(st.Balance, "%f", &have)
		st.Enough = have >= need
		out = append(out, st)
	}
	return out, nil
}

// UnlockSigner forwards the operator passphrase to the signer. The seeds are
// only sent on first run, when the vault does not exist yet.
func (s *Service) UnlockSigner(ctx context.Context, passphrase, depositMnemonic, hotMnemonic string) error {
	s.mu.RLock()
	signer := s.signer
	s.mu.RUnlock()
	if signer == nil {
		return fmt.Errorf("cryptopay is disabled")
	}
	return signer.Unlock(ctx, passphrase, depositMnemonic, hotMnemonic)
}

// RunSweep executes the sweep (or a dry-run) and returns the summary.
// Serialised by the caller via the admin route; sweeps are operator-driven.
func (s *Service) RunSweep(ctx context.Context, dryRun bool) (*sweeper.Summary, error) {
	s.mu.RLock()
	exec := s.executor
	s.mu.RUnlock()
	if exec == nil {
		return nil, fmt.Errorf("cryptopay is disabled")
	}
	// One run at a time: the manual trigger and the scheduler would
	// otherwise both read the same balances and double-send.
	s.sweepMu.Lock()
	defer s.sweepMu.Unlock()
	return exec.Run(ctx, dryRun)
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
}

// ListSweepRecords returns recent sweep records, optionally filtered.
func (s *Service) ListSweepRecords(ctx context.Context, chain string, limit int) ([]SweepRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT id, chain, from_address, to_address, token, amount::text,
	                 gas_tx_hash, sweep_tx_hash, status, error, created_at
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
			&r.Amount, &r.GasTxHash, &r.SweepTxHash, &r.Status, &r.Error, &created); err != nil {
			return nil, err
		}
		r.Amount = trimDecimalZeros(r.Amount)
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
}

// ListDeposits returns recent deposit observations, optionally filtered.
func (s *Service) ListDeposits(ctx context.Context, chain, status string, limit int) ([]DepositRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT id, chain, tx_hash, log_index, from_address, to_address,
	                 token, amount::text, status, reason, created_at
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
		if err := rows.Scan(&r.ID, &r.Chain, &r.TxHash, &r.LogIndex, &r.FromAddress,
			&r.ToAddress, &r.Token, &r.Amount, &r.Status, &r.Reason, &created); err != nil {
			return nil, err
		}
		r.TokenSymbol = tokenSymbol(r.Chain, r.Token)
		r.Amount = trimDecimalZeros(r.Amount)
		r.Amount = trimDecimalZeros(r.Amount)
		r.CreatedAt = created.UTC().Format(time.RFC3339)
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
	                 token, amount::text, status, reason, created_at
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
		if err := rows.Scan(&r.ID, &r.Chain, &r.TxHash, &r.LogIndex, &r.FromAddress,
			&r.ToAddress, &r.Token, &r.Amount, &r.Status, &r.Reason, &created); err != nil {
			return nil, nil, err
		}
		r.TokenSymbol = tokenSymbol(r.Chain, r.Token)
		r.Amount = trimDecimalZeros(r.Amount)
		r.Amount = trimDecimalZeros(r.Amount)
		r.CreatedAt = created.UTC().Format(time.RFC3339)
		out = append(out, r)
	}
	return out, &summary, rows.Err()
}

// DepositInfo is everything the standalone deposit page needs.
type DepositInfo struct {
	Enabled   bool           `json:"enabled"`
	Chains    []DepositChain `json:"chains"`
	Rate      float64        `json:"rate"`       // balance units per USDT
	MinAmount float64        `json:"min_amount"` // smallest credit
	MaxAmount float64        `json:"max_amount"` // largest single credit
}

// DepositChain is one chain's deposit address.
type DepositChain struct {
	Chain   string   `json:"chain"`
	Address string   `json:"address"`
	Tokens  []string `json:"tokens"` // supported symbols on this chain, e.g. ["USDT","USDC"]
}

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
	info := &DepositInfo{
		Enabled: true, Rate: cfg.CreditMultiplier,
		MinAmount: cfg.CreditMinAmount, MaxAmount: cfg.CreditMaxAmount,
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
		for _, token := range watchedTokens[chain] {
			if token.Symbol != "" {
				symbols = append(symbols, token.Symbol)
			}
		}
		if len(symbols) == 0 {
			continue
		}
		info.Chains = append(info.Chains, DepositChain{Chain: chain, Address: address, Tokens: symbols})
	}
	return info, nil
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
}

// UserDepositHistory lists the caller's deposits against their addresses.
func (s *Service) UserDepositHistory(ctx context.Context, userID int64, limit int) ([]UserDepositRecord, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.chain, d.tx_hash, d.token, d.amount::text, d.status, d.reason, d.created_at
		  FROM deposit_transactions d
		  JOIN deposit_addresses a ON lower(a.address) = lower(d.to_address)
		 WHERE a.user_id = $1
		 ORDER BY d.id DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("user deposit history: %w", err)
	}
	defer func() { _ = rows.Close() }()
	rate := s.Config().CreditMultiplier
	var out []UserDepositRecord
	for rows.Next() {
		var r UserDepositRecord
		var created time.Time
		if err := rows.Scan(&r.Chain, &r.TxHash, &r.Token, &r.Amount, &r.Status, &r.Reason, &created); err != nil {
			return nil, err
		}
		// The user sees the symbol; the raw contract stays internal.
		r.Token = tokenSymbol(r.Chain, r.Token)
		r.Amount = trimDecimalZeros(r.Amount)
		// Rejected transfers must say why: a bare "ignored" badge reads as a
		// random failure and invites support tickets. The machine code goes
		// out as-is; the UI localizes it with the current thresholds.
		r.CreatedAt = created.UTC().Format(time.RFC3339)
		if r.Status == "credited" {
			var amount float64
			_, _ = fmt.Sscanf(r.Amount, "%f", &amount)
			r.Credited = amount * rate
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
