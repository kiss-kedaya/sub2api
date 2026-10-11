package cryptopay

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/ledger"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// TokenConfig is one watched token on a chain.
type TokenConfig struct {
	Contract string
	Decimals int
}

// watchedTokens maps chain -> token contracts with decimals. Kept in code
// (not settings) because a wrong contract would credit the wrong asset;
// changing it should require a code review.
var watchedTokens = map[string]TokenConfig{
	ChainBSC:     {Contract: "0x55d398326f99059ff775485246999027b3197955", Decimals: 18}, // USDT (BSC-USD)
	ChainPolygon: {Contract: "0xc2132d05d31c914a87c6611c10748aeb04b58e8f", Decimals: 6},  // USDT
	ChainPlasma:  {Contract: "", Decimals: 6},                                            // native-stable chain; filled from config when known
}

// Service is the cryptopay facade: owns config snapshots, the RPC pools,
// the scanner worker, the ledger and the sweeper.
type Service struct {
	db       *sql.DB
	settings *service.SettingService
	creditor ledger.Creditor

	mu       sync.RWMutex
	config   CryptoConfig
	pools    map[string]*rpc.Pool
	worker   *scanner.Worker
	planner  *sweeper.Planner
	signer   *sweeper.SignerClient
	addresses *addresses.Service
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
		token, ok := watchedTokens[chain]
		if !ok || token.Contract == "" {
			continue
		}
		chainConfigs = append(chainConfigs, scanner.ChainConfig{
			Chain:         chain,
			Tokens:        []string{token.Contract},
			Confirmations: uint64(cfg.Confirmations[chain]),
			Decimals:      map[string]int{token.Contract: token.Decimals},
		})
	}

	cursor := scanner.NewCursorStore(s.db)
	led := ledger.New(s.db, s.creditor, newAddressResolver(s.addresses), cfg.CreditMinAmount, cfg.CreditMultiplier)
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
	s.worker = scanner.NewWorker(s.pools, chainConfigs, cursor, led, addressLister, 15*time.Second, 2000)
	s.worker.Start()

	s.signer = sweeper.NewSignerClient(cfg.SignerSocket)
	s.planner = sweeper.NewPlanner(s.db, newBalanceReader(s.db), sweeper.Config{
		GlobalThreshold: cfg.SweepGlobalThreshold,
		MinPerAddress:   cfg.SweepMinPerAddress,
		HotWalletEVM:    cfg.HotWalletEVM,
		HotWalletSolana: cfg.HotWalletSolana,
	}, s.pools)
	slog.Info("[cryptopay] started", "chains", cfg.Chains)
}

// Config returns the current config snapshot.
func (s *Service) Config() CryptoConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
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
