package cryptopay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
)

// This monitor survives payment disable/reload, so a lock continues to retry
// Bark delivery. It does not run on the explicitly read-only review instance.
func (s *Service) securityMonitor(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := s.checkWalletSecurity(checkCtx)
			if err == nil {
				err = s.scanIdleWalletSecurity(checkCtx)
			}
			cancel()
			if err != nil && !errors.Is(err, wallet.ErrWalletNotConfigured) && !errors.Is(err, wallet.ErrSecurityLocked) {
				slog.Warn("[cryptopay] wallet security check failed")
			}
			// Retry after the security checks, with a separate budget so Bark
			// latency cannot consume the wallet guard's time allowance.
			alertCtx, alertCancel := context.WithTimeout(ctx, 10*time.Second)
			if err := s.deliverSweepFailureAlerts(alertCtx); err != nil {
				slog.Warn("[cryptopay] sweep failure notification pending")
			}
			alertCancel()
		}
	}
}

// Existing wallet balances remain protected after recharge is turned off.
// Reuse the scanner's batching, canonical verification and global lock, while
// keeping an independent cursor and no deposit handler/recipient list.
func (s *Service) scanIdleWalletSecurity(ctx context.Context) error {
	s.mu.RLock()
	active := s.worker != nil && s.config.Enabled && !s.securityPaused.Load()
	runtimePools := maps.Clone(s.pools)
	activeChains := map[string]bool{}
	if active {
		for _, chain := range s.config.Chains {
			activeChains[chain] = true
		}
	}
	s.mu.RUnlock()
	if s.settings == nil {
		return nil
	}
	cfg, err := LoadConfig(ctx, NewServiceSettingStore(s.settings))
	if err != nil {
		return err
	}
	pools := map[string]*rpc.Pool{}
	var chains []scanner.ChainConfig
	for _, chain := range evmChains {
		if activeChains[chain] {
			continue
		}
		if strings.TrimSpace(cfg.RPC[chain]) != "" {
			pools[chain] = rpc.NewPool(cfg.RPC[chain])
		} else if pool := runtimePools[chain]; pool != nil && len(pool.Endpoints()) > 0 {
			pools[chain] = pool.Snapshot()
		} else {
			continue
		}
		tokens := make([]string, 0, len(watchedTokens[chain]))
		for _, token := range watchedTokens[chain] {
			tokens = append(tokens, token.Contract)
		}
		chains = append(chains, scanner.ChainConfig{Chain: chain, Tokens: tokens, MaxRange: uint64(cfg.ScanRange[chain])})
	}
	if len(chains) == 0 {
		return nil
	}
	worker := scanner.NewWorker(s.db, pools, chains, scanner.NewNamespacedCursorStore(s.db, "wallet-security:"), nil, func(context.Context, string) ([]string, error) { return nil, nil }, 5*time.Second, 2000)
	worker.SetSecurityGuards(s.checkWalletSecurity, s.scanWalletOutflows)
	return worker.RunGuardScan(ctx)
}

// Atomic field update avoids overwriting a concurrent admin RPC/chain change.
// Config() and the signer separately fail closed even if this write fails.
func (s *Service) pauseCryptoForSecurity(ctx context.Context) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	if s.db == nil {
		return wallet.ErrVaultUnavailable
	}
	_, err := s.db.ExecContext(ctx, `UPDATE settings SET value=jsonb_set(value::jsonb,'{enabled}','false'::jsonb)::text,updated_at=NOW()
		WHERE key=$1 AND COALESCE((value::jsonb->>'enabled')::boolean,false)=true`, SettingKeyCryptoConfig)
	if err != nil {
		return wallet.ErrVaultUnavailable
	}
	return nil
}

func (s *Service) handleSecurityIncident(ctx context.Context, status wallet.SecurityStatus) error {
	if !status.Locked {
		return nil
	}
	s.securityPaused.Store(true)
	s.wallet.SetEnabled(false)
	pauseErr := s.pauseCryptoForSecurity(ctx)
	if push := s.getPushNotifier(); push != nil && status.AlertPending {
		if err := s.wallet.DeliverSecurityAlert(ctx, func(current wallet.SecurityStatus) error {
			return push.PushToAdmins(ctx, "高危：充值钱包已安全锁定",
				fmt.Sprintf("原因：%s\n钱包签名和充值地址发放已暂停。请核查后台安全状态，修复后使用 2FA 确认恢复。", current.Reason), "crypto-security")
		}); err != nil {
			slog.Warn("[cryptopay] wallet security alert pending")
		}
	}
	return pauseErr
}

func (s *Service) checkWalletSecurity(ctx context.Context) error {
	if s == nil || s.wallet == nil {
		return wallet.ErrVaultUnavailable
	}
	status, err := s.wallet.CheckSecurity(ctx)
	if status.Locked {
		if handleErr := s.handleSecurityIncident(ctx, status); handleErr != nil {
			return errors.Join(err, handleErr)
		}
		return wallet.ErrSecurityLocked
	}
	return err
}

func (s *Service) checkWalletActivity(ctx context.Context) error {
	if err := s.checkWalletSecurity(ctx); err != nil {
		return err
	}
	if s.securityPaused.Load() {
		return wallet.ErrSecurityLocked
	}
	if s.settings != nil {
		cfg, err := LoadConfig(ctx, NewServiceSettingStore(s.settings))
		if err != nil {
			return err
		}
		if !cfg.Enabled {
			return wallet.ErrSigningDisabled
		}
	}
	return nil
}

func (s *Service) validateWalletAssignment(ctx context.Context, row addresses.Assignment) error {
	err := s.wallet.ValidateAssignment(ctx, row)
	if err != nil && s.wallet.SecurityLocked() {
		status, _ := s.WalletSecurity(ctx)
		_ = s.handleSecurityIncident(ctx, status)
	}
	return err
}

// scanWalletOutflows is shared by live windows, contiguous catch-up and manual
// backfills. RPC errors stop cursor advancement; they never prove an incident.
func (s *Service) scanWalletOutflows(ctx context.Context, evm *scanner.EVMScanner, pool *rpc.Pool, chain scanner.ChainConfig, from, to uint64) error {
	if err := s.checkWalletSecurity(ctx); err != nil {
		return err
	}
	watch, err := s.wallet.SecurityWatchEVMAddresses(ctx)
	if err != nil {
		return err
	}
	transfers, err := evm.ScanOutgoingRange(ctx, chain.Chain, from, to, chain.Tokens, watch)
	if err != nil {
		return err
	}
	chainID := cryptoSecurityChainID(chain.Chain)
	for _, transfer := range transfers {
		if transfer.BlockNumber < from || transfer.BlockNumber > to || transfer.Amount == nil || transfer.Amount.Sign() <= 0 {
			continue
		}
		watched := false
		for _, token := range chain.Tokens {
			if strings.EqualFold(token, transfer.Token) {
				watched = true
				break
			}
		}
		if !watched {
			continue
		}
		if err := scanner.VerifyObservedTransfer(ctx, pool, transfer); err != nil {
			return err
		}
		authorized, err := s.wallet.AuthorizedTokenTransfer(ctx, chain.Chain, chainID, transfer.TxHash, transfer.From, transfer.To, transfer.Token, transfer.Amount.String())
		if err != nil {
			return err
		}
		if authorized {
			continue
		}
		// A relayer may mine before the sweep caller has polled the final hash.
		// Bind its exact persisted order and receipt before evaluating outflow.
		s.mu.RLock()
		executor := s.executor
		provider := s.config.SweepProvider
		s.mu.RUnlock()
		if executor == nil && provider == "bitget" {
			executor = sweeper.NewExecutor(s.db, nil, s.wallet, sweeper.Config{Provider: provider}, nil, nil, 0)
		}
		if executor != nil {
			bound, bindErr := executor.AuthorizeGaslessObserved(ctx, pool, chain.Chain, transfer.TxHash, transfer.From)
			if bindErr != nil {
				return bindErr
			}
			if bound {
				authorized, err = s.wallet.AuthorizedTokenTransfer(ctx, chain.Chain, chainID, transfer.TxHash, transfer.From, transfer.To, transfer.Token, transfer.Amount.String())
				if err != nil {
					return err
				}
				if authorized {
					continue
				}
			}
		}
		lockErr := s.wallet.LockSecurity(ctx, "unauthorized_token_outflow", chain.Chain, transfer.TxHash)
		status, _ := s.WalletSecurity(ctx)
		_ = s.handleSecurityIncident(ctx, status)
		return errors.Join(wallet.ErrSecurityLocked, lockErr)
	}
	return nil
}

func cryptoSecurityChainID(chain string) int64 {
	switch chain {
	case ChainEthereum:
		return 1
	case ChainBSC:
		return 56
	case ChainPolygon:
		return 137
	case ChainPlasma:
		return 9745
	case ChainArbitrum:
		return 42161
	case ChainBase:
		return 8453
	case ChainXLayer:
		return 196
	}
	return 0
}

func (s *Service) WalletSecurity(ctx context.Context) (wallet.SecurityStatus, error) {
	if s == nil || s.wallet == nil {
		return wallet.SecurityStatus{}, wallet.ErrVaultUnavailable
	}
	status, err := s.wallet.Status(ctx)
	if err != nil {
		return wallet.SecurityStatus{}, err
	}
	if status.Security == nil {
		return wallet.SecurityStatus{}, wallet.ErrVaultUnavailable
	}
	return *status.Security, nil
}

func (s *Service) RecoverWalletSecurity(ctx context.Context) (wallet.SecurityStatus, error) {
	if s == nil || s.wallet == nil {
		return wallet.SecurityStatus{}, wallet.ErrVaultUnavailable
	}
	if err := s.pauseCryptoForSecurity(ctx); err != nil {
		return wallet.SecurityStatus{}, err
	}
	if err := s.wallet.RecoverSecurity(ctx); err != nil {
		return wallet.SecurityStatus{}, err
	}
	return s.WalletSecurity(ctx)
}

// Close stops this facade's loops; it never clears a persistent security lock.
func (s *Service) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.discoveryCancel != nil {
		s.discoveryCancel()
		s.discoveryCancel = nil
	}
	if s.securityCancel != nil {
		s.securityCancel()
		s.securityCancel = nil
	}
	if s.maintCancel != nil {
		s.maintCancel()
		s.maintCancel = nil
	}
	w := s.worker
	s.worker = nil
	s.mu.Unlock()
	if w != nil {
		w.Stop()
	}
	if s.wallet != nil {
		s.wallet.Close()
	}
}
