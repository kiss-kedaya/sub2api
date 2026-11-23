package cryptopay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math/rand"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/addresses"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
)

// This monitor survives payment disable/reload, so a lock continues to retry
// Bark delivery. It does not run on the explicitly read-only review instance.
func (s *Service) securityMonitor(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var idle idleGuardPacer
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := s.checkWalletSecurity(checkCtx)
			if err == nil {
				err = s.scanIdleWalletSecurity(checkCtx, &idle)
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

// newIdleGuardWorker builds the worker of the idle-chain guard scan: its own
// "wallet-security:" cursors, no deposit handler and no recipients, and its own
// advisory lock (scanner.GuardScanLockKey), so it can never starve the deposit scan
// of the shared lock.
func newIdleGuardWorker(db *sql.DB, pools map[string]*rpc.Pool, chains []scanner.ChainConfig) *scanner.Worker {
	worker := scanner.NewWorker(db, pools, chains, scanner.NewNamespacedCursorStore(db, "wallet-security:"), nil,
		func(context.Context, string) ([]string, error) { return nil, nil }, 5*time.Second, 2000)
	worker.SetScanLockKey(scanner.GuardScanLockKey)
	return worker
}

// idleGuardPacer spaces the guard scan of EACH idle chain after its own
// consecutive failures.
//
// The guard scans every chain that is not scanned for deposits. One of them (an
// old plasma entry whose three free RPC nodes all answer 403/429) had failed on
// every tick for sixteen days: ~150 requests each, every five seconds, against
// providers that were already refusing us, while holding a database session. A
// failing chain cannot detect anything anyway, so it backs off (5s, 10s, ... up to
// five minutes) and resumes at full cadence on its first success. The state is per
// chain: a healthy idle chain keeps its five second cadence however long another
// one has been dead, because the guard exists to notice an unauthorised outflow
// from a hot wallet and must not be delayed by somebody else's outage. The zero
// value is ready to use; it is only ever touched by the security monitor goroutine.
type idleGuardPacer struct {
	chains map[string]*idleGuardState
}

type idleGuardState struct {
	failures int
	next     time.Time
}

const (
	idleGuardBaseDelay = 5 * time.Second
	idleGuardMaxDelay  = 5 * time.Minute
)

func (p *idleGuardPacer) due(chain string, now time.Time) bool {
	st := p.chains[chain]
	return st == nil || !now.Before(st.next)
}

// dueChains returns the chains whose guard scan is due now, those with the fewest
// consecutive failures first, so a chain that is probably about to fail slowly
// cannot eat the tick's time budget ahead of the healthy ones.
func (p *idleGuardPacer) dueChains(chains []scanner.ChainConfig, now time.Time) []scanner.ChainConfig {
	out := make([]scanner.ChainConfig, 0, len(chains))
	for _, chain := range chains {
		if p.due(chain.Chain, now) {
			out = append(out, chain)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return p.failuresOf(out[i].Chain) < p.failuresOf(out[j].Chain) })
	return out
}

func (p *idleGuardPacer) failuresOf(chain string) int {
	if st := p.chains[chain]; st != nil {
		return st.failures
	}
	return 0
}

// note records the outcome of one guard iteration of chain. Another instance
// holding the guard lock is neither a success nor a failure of the chain.
func (p *idleGuardPacer) note(chain string, err error, now time.Time) {
	switch {
	case err == nil:
		delete(p.chains, chain)
	case errors.Is(err, scanner.ErrScanBusy):
		// nothing was attempted; leave the state as it is
	default:
		if p.chains == nil {
			p.chains = make(map[string]*idleGuardState)
		}
		st := p.chains[chain]
		if st == nil {
			st = &idleGuardState{}
			p.chains[chain] = st
		}
		st.failures++
		delay := idleGuardBaseDelay
		for i := 1; i < st.failures && delay < idleGuardMaxDelay; i++ {
			delay *= 2
		}
		delay = min(delay, idleGuardMaxDelay)
		jitter := time.Duration(rand.Int63n(int64(delay)/5*2+1)) - delay/5 // +-20%
		st.next = now.Add(delay + jitter)
	}
}

// Existing wallet balances remain protected after recharge is turned off.
// Reuse the scanner's batching, canonical verification and global lock, while
// keeping an independent cursor and no deposit handler/recipient list.
func (s *Service) scanIdleWalletSecurity(ctx context.Context, pacer *idleGuardPacer) error {
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
			// The chain id is part of the pool's identity: it selects the gas
			// price floor a sweep may never sign below.
			pools[chain] = rpc.NewPool(cfg.RPC[chain]).WithChainID(uint64(chainlistIDs[chain]))
		} else if pool := runtimePools[chain]; pool != nil && len(pool.Endpoints()) > 0 {
			pools[chain] = pool.Snapshot()
		} else {
			continue
		}
		tokens := evmContracts(chain)
		chains = append(chains, scanner.ChainConfig{Chain: chain, Tokens: tokens, MaxRange: uint64(cfg.ScanRange[chain]), ScanConcurrency: cfg.ScanConcurrency[chain]})
	}
	if len(chains) == 0 {
		return nil
	}
	// One guard worker per chain that is due: a chain whose nodes keep refusing us
	// is backed off on its own and must not delay the others (see idleGuardPacer).
	// Each runs under the guard lock in turn, healthiest chain first.
	now := time.Now()
	duePools := map[string]*rpc.Pool{}
	var guardErrs []error
	for _, chain := range pacer.dueChains(chains, now) {
		duePools[chain.Chain] = pools[chain.Chain]
		worker := newIdleGuardWorker(s.db, map[string]*rpc.Pool{chain.Chain: pools[chain.Chain]}, []scanner.ChainConfig{chain})
		worker.SetSecurityGuards(s.scanWalletSecurityGuard, s.scanWalletOutflows)
		err := worker.RunGuardScan(ctx)
		pacer.note(chain.Chain, err, time.Now())
		if err != nil {
			guardErrs = append(guardErrs, err)
		}
	}
	// ERC-20 outflows are covered by the block guards above; a native spend has
	// no log, so the same idle window must poll the hot wallet through the
	// address API or a drained ETH/BNB balance stays silent. It paces itself
	// (NativePollSeconds) and only looks at the chains that are due.
	nativeErr := s.pollNativeOutflowsIdle(ctx, cfg, duePools)
	return errors.Join(append(guardErrs, nativeErr)...)
}

// SettingKeySecurityPauseMarker stores the lock episode that already applied
// the automatic channel pause. The security monitor observes a persistent lock
// on every tick (5s), so without this marker each observation rewrites
// crypto.config.enabled=false and silently undoes an operator's explicit
// re-enable within seconds — even after the lock was the only thing stopping
// the channel.
const SettingKeySecurityPauseMarker = "crypto.security.pause.v1"

// Atomic field update avoids overwriting a concurrent admin RPC/chain change.
// Config() and the signer separately fail closed even if this write fails.
// This is the forced variant: recovery must prove the channel is off before it
// clears the lock, so it always runs.
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

// securityLockEpisode identifies one persistent lock. A later incident gets a
// new LockedAt, so it pauses the channel again; the same episode never does.
func securityLockEpisode(status wallet.SecurityStatus) string {
	if episode := strings.TrimSpace(status.LockedAt); episode != "" {
		return episode
	}
	if reason := strings.TrimSpace(status.Reason); reason != "" {
		return reason
	}
	return "locked"
}

// pauseCryptoForSecurityOnce disables the customer-facing channel at most once
// per lock episode. The marker and the config write commit together, so
// concurrent instances cannot double-write the row and an operator re-enable
// made after the pause stands until a genuinely new incident.
//
// Signing and deposit-address issuance stay hard-blocked by the wallet lock
// itself (checkSecurityInTx / ValidateAssignment), so this does not reopen fund
// movement when an operator re-enables the channel control plane.
func (s *Service) pauseCryptoForSecurityOnce(ctx context.Context, status wallet.SecurityStatus) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	if s.db == nil {
		return wallet.ErrVaultUnavailable
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return wallet.ErrVaultUnavailable
	}
	defer func() { _ = tx.Rollback() }()

	// Claim the episode with one conditional upsert: a second observation of
	// the same lock, or another instance, gets no row and skips the write.
	var claimed int
	err = tx.QueryRowContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW())
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()
		WHERE settings.value IS DISTINCT FROM EXCLUDED.value
		RETURNING 1`, SettingKeySecurityPauseMarker, securityLockEpisode(status)).Scan(&claimed)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return wallet.ErrVaultUnavailable
	}
	if _, err = tx.ExecContext(ctx, `UPDATE settings SET value=jsonb_set(value::jsonb,'{enabled}','false'::jsonb)::text,updated_at=NOW()
		WHERE key=$1 AND COALESCE((value::jsonb->>'enabled')::boolean,false)=true`, SettingKeyCryptoConfig); err != nil {
		return wallet.ErrVaultUnavailable
	}
	return tx.Commit()
}

func (s *Service) handleSecurityIncident(ctx context.Context, status wallet.SecurityStatus) error {
	if !status.Locked {
		return nil
	}
	s.securityPaused.Store(true)
	s.wallet.SetEnabled(false)
	pauseErr := s.pauseCryptoForSecurityOnce(ctx, status)
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

// scanWalletSecurityGuard is the security guard for the deposit scan path only.
//
// It differs from checkWalletSecurity in exactly one case: a v3 vault sealed
// behind recovery shards (ErrSignerShareRequired). That is a supported steady
// state, not an incident — the watch list and the deposit assignments are read
// from the authenticated DB snapshot, so scanning an inbound range needs no key
// material at all. Treating it as a security failure re-froze the deposit
// cursor: the scan pass failed in the security stage on the very first block
// range after the cursor, so the cursor could never advance and the chain fell
// permanently behind (production BSC sat at one block for hours while the chain
// grew ~133 blocks/min).
//
// A real security incident (status.Locked) still blocks the range, and the
// signing path independently refuses to broadcast with no data key, so nothing
// unsafe becomes reachable here.
func (s *Service) scanWalletSecurityGuard(ctx context.Context) error {
	return forgiveSealedVault(s.checkWalletSecurity(ctx))
}

// forgiveSealedVault maps the sealed-vault steady state to success and passes
// every other result through unchanged. Kept separate from the DB-backed guard
// so the rule is testable without fabricating the whole security snapshot load.
func forgiveSealedVault(err error) error {
	if isSealedVaultSteadyState(err) {
		return nil
	}
	return err
}

// isSealedVaultSteadyState reports whether err is the one condition the deposit
// scan may ignore. It is deliberately narrow: every other error — a real
// security incident included — keeps blocking the scan.
func isSealedVaultSteadyState(err error) bool {
	return err != nil && errors.Is(err, wallet.ErrSignerShareRequired)
}

func (s *Service) checkWalletActivity(ctx context.Context) error {
	if err := s.checkWalletSecurity(ctx); err != nil {
		if errors.Is(err, wallet.ErrSignerShareRequired) {
			// Same steady state as scanWalletOutflows: no key means no outflow
			// guard, but deposits must keep crediting.
			return nil
		}
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
	// A sealed v3 vault must not fail the range; see scanWalletSecurityGuard.
	if err := s.scanWalletSecurityGuard(ctx); err != nil {
		return err
	}
	watch, err := s.wallet.SecurityWatchEVMAddresses(ctx)
	if err != nil {
		if errors.Is(err, wallet.ErrSignerShareRequired) {
			// A v3 vault sealed behind recovery shards is a supported steady
			// state: the watch list needs derived addresses and therefore a
			// data key, but the chain still has to be scanned. Failing the range
			// here froze the deposit cursor for hours after the first shard
			// migration. Skip the outflow guard until the operator unlocks; the
			// signing path independently refuses to broadcast with no key.
			return nil
		}
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
			if isSealedVaultSteadyState(err) {
				// Verifying one observed outflow needs the signer audit table and
				// therefore an open vault. A sealed v3 vault cannot answer it, and
				// letting that abort the range froze the deposit cursor exactly as
				// the guard above used to: one outflow in the scanned range was
				// enough to stop every later deposit from crediting. Skip the
				// verdict for this transfer instead of failing the block range; the
				// outflow stays on-chain and is re-judged once the operator
				// unlocks, and signing still refuses to broadcast with no key.
				continue
			}
			return err
		}
		if authorized {
			continue
		}
		return s.lockOnOutflow(ctx, chain.Chain, transfer.TxHash)
	}
	return nil
}

// scanNativeOutflow applies the same protection as scanWalletOutflows to a
// native-coin (ETH/BNB) transfer out of a watched address.
//
// It is fed by the address-based txlist poll (from == hot wallet && value > 0)
// rather than by the block-range log scan, because a native spend has no log.
// The decision rule is deliberately the existing one, not a second threshold:
// a positive native amount that the signer did not authorise is an incident,
// exactly like an unauthorised ERC-20 transfer. A spend to one of our own
// addresses is the gas top-up the sweeper signs from the hot wallet, so it is
// internal movement, not theft.
func (s *Service) scanNativeOutflow(ctx context.Context, chain scanner.ChainConfig, transfer scanner.Transfer) error {
	// A sealed v3 vault must not abort the address poll that feeds deposits.
	if err := s.scanWalletSecurityGuard(ctx); err != nil {
		return err
	}
	if transfer.Amount == nil || transfer.Amount.Sign() <= 0 || !transfer.Native {
		return nil
	}
	watch, err := s.wallet.SecurityWatchEVMAddresses(ctx)
	if err != nil {
		if isSealedVaultSteadyState(err) {
			return nil
		}
		return err
	}
	chainID := cryptoSecurityChainID(chain.Chain)
	authorized, err := s.wallet.AuthorizedTokenTransfer(ctx, chain.Chain, chainID, transfer.TxHash,
		transfer.From, transfer.To, scanner.NativeToken, transfer.Amount.String())
	if err != nil {
		if isSealedVaultSteadyState(err) {
			return nil
		}
		return err
	}
	if !nativeOutflowVerdict(transfer, watch, authorized) {
		return nil
	}
	slog.Warn("[cryptopay] unauthorised native-coin outflow",
		"chain", chain.Chain, "tx", transfer.TxHash, "to", transfer.To)
	return s.lockOnOutflow(ctx, chain.Chain, transfer.TxHash)
}

// nativeOutflowVerdict is the native-coin rule, expressed once so the policy
// is testable without a wallet vault: a positive native spend that our signer
// did not authorise, to an address we do not own, is an incident. A spend to
// one of our own watch addresses is the gas top-up the sweeper signs from the
// hot wallet, so it is internal movement, not theft.
func nativeOutflowVerdict(transfer scanner.Transfer, watch []string, authorized bool) bool {
	if transfer.Amount == nil || transfer.Amount.Sign() <= 0 || !transfer.Native {
		return false
	}
	for _, own := range watch {
		if strings.EqualFold(own, transfer.To) {
			return false
		}
	}
	return !authorized
}

// lockOnOutflow is the single incident path for both asset classes: the stored
// reason, the wallet security lock, the pause of the recharge channel and the
// admin alert are shared, so a native outflow cannot alert differently.
func (s *Service) lockOnOutflow(ctx context.Context, chain, txHash string) error {
	lockErr := s.wallet.LockSecurity(ctx, "unauthorized_token_outflow", chain, txHash)
	status, _ := s.WalletSecurity(ctx)
	_ = s.handleSecurityIncident(ctx, status)
	return errors.Join(wallet.ErrSecurityLocked, lockErr)
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
	s.closed = true
	if s.configCancel != nil {
		s.configCancel()
	}
	configDone := s.configDone
	s.mu.Unlock()
	// Never wait while holding mu: the watcher may be inside reload.
	if configDone != nil {
		<-configDone
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
	s.stopNativePollerLocked()
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
	// The notification dispatcher owns a background worker; close it so the
	// process can exit promptly. Closing twice is safe.
	s.eventsMu.RLock()
	notifier := s.events
	s.eventsMu.RUnlock()
	if closer, ok := notifier.(interface{ Close() }); ok {
		closer.Close()
	}
}
