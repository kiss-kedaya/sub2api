package service

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

type codexQuotaOverdraftCandidateRepository interface {
	ListCodexQuotaOverdraftCandidates(context.Context, *int64, bool) ([]Account, error)
}

type codexQuotaOverdraftCandidateCacheEntry struct {
	accounts  []Account
	updatedAt time.Time
}

func codexQuotaOverdraftBypassesSchedulingThreshold(ctx context.Context, account *Account) bool {
	return codexQuotaOverdraftSchedulingEnabled(ctx) && isCodexQuotaOverdraftAccount(account) &&
		codexQuotaOverdraftSchedulingAllowed(account, time.Now().UTC())
}

func (s *RateLimitService) notifyCodexQuotaOverdraftAwareSchedulingBlock(
	ctx context.Context,
	account *Account,
	until time.Time,
) {
	if !codexQuotaOverdraftBypassesSchedulingThreshold(ctx, account) {
		s.notifyAccountSchedulingBlocked(account, until, "account_scheduling_threshold")
	}
}

func (s *OpenAIGatewayService) listCodexQuotaOverdraftSchedulableAccounts(
	ctx context.Context,
	groupID *int64,
	platform string,
) ([]Account, bool, error) {
	if !CodexQuotaOverdraftSchedulingEnabled(ctx) || platform != PlatformOpenAI || s.accountRepo == nil {
		return nil, false, nil
	}
	// The caller tries the shared scheduler snapshot first. This helper is only
	// invoked after that normal candidate pass is empty, so threshold-paused
	// accounts never enter the shared snapshot/cache.
	now := time.Now().UTC()
	s.cleanupCodexQuotaOverdraftCandidateCache(now)
	key := int64(1 << 62)
	if groupID != nil && *groupID > 0 {
		key = *groupID
	}
	cacheKey := fmt.Sprintf("%d:%s", key, platform)
	if s.codexQuotaOverdraftFallbackThrottle != nil && !s.codexQuotaOverdraftFallbackThrottle.Allow(key, now) {
		if cached, ok := s.codexQuotaOverdraftCandidateCache.Load(cacheKey); ok {
			if entry, ok := cached.(codexQuotaOverdraftCandidateCacheEntry); ok && time.Since(entry.updatedAt) < 2*time.Second {
				markCodexQuotaOverdraftCandidates(ctx, entry.accounts)
				return append([]Account(nil), entry.accounts...), true, nil
			}
		}
		return nil, false, nil
	}
	candidates, ok := s.accountRepo.(codexQuotaOverdraftCandidateRepository)
	if !ok {
		return nil, true, nil
	}
	var accounts []Account
	var err error
	includeUngrouped := s.cfg == nil || s.cfg.RunMode != config.RunModeSimple
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		groupID = nil
	}
	queryCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	accounts, err = candidates.ListCodexQuotaOverdraftCandidates(queryCtx, groupID, includeUngrouped)
	if err != nil {
		return nil, true, fmt.Errorf("query overdraft accounts failed: %w", err)
	}
	accounts = normalizeCodexQuotaOverdraftAccountsForScheduling(ctx, accounts)
	accounts = s.filterOpenAIAccountsBySchedulingThreshold(ctx, accounts)
	markCodexQuotaOverdraftCandidates(ctx, accounts)
	// Keep this request-side cache bounded.  It is an optimization only; when
	// the cap is reached the per-key throttle still protects the database and a
	// later request simply refreshes the candidates.
	if s.codexQuotaOverdraftCandidateCacheSize() < 512 {
		s.codexQuotaOverdraftCandidateCache.Store(cacheKey, codexQuotaOverdraftCandidateCacheEntry{accounts: append([]Account(nil), accounts...), updatedAt: now})
	}
	return accounts, true, nil
}

func (s *OpenAIGatewayService) cleanupCodexQuotaOverdraftCandidateCache(now time.Time) {
	if s == nil {
		return
	}
	s.codexQuotaOverdraftCandidateCache.Range(func(key, value any) bool {
		entry, ok := value.(codexQuotaOverdraftCandidateCacheEntry)
		if !ok || now.Sub(entry.updatedAt) > 10*time.Second {
			s.codexQuotaOverdraftCandidateCache.Delete(key)
		}
		return true
	})
}

func (s *OpenAIGatewayService) codexQuotaOverdraftCandidateCacheSize() int {
	if s == nil {
		return 0
	}
	size := 0
	s.codexQuotaOverdraftCandidateCache.Range(func(_, value any) bool {
		if _, ok := value.(codexQuotaOverdraftCandidateCacheEntry); ok {
			size++
		}
		return size < 512
	})
	return size
}

func (s *OpenAIGatewayService) handleCodexQuotaOverdraftUpstream429(
	ctx context.Context,
	account *Account,
	statusCode int,
	headers http.Header,
	responseBody []byte,
	canonicalModel []string,
) bool {
	if statusCode != http.StatusTooManyRequests || s.codexQuotaOverdraft == nil || !codexQuotaOverdraftSchedulingEnabled(ctx) {
		return false
	}
	preferredModel := ""
	if len(canonicalModel) > 0 {
		preferredModel = canonicalModel[0]
	}
	return s.codexQuotaOverdraft.HandleQuota429(ctx, account, headers, responseBody, preferredModel)
}

func (s *OpenAIGatewayService) processCodexQuotaOverdraftUsageSnapshot(
	ctx context.Context,
	accountID int64,
	now time.Time,
	updates map[string]any,
) {
	persistSnapshot := s.getCodexSnapshotThrottle().Allow(accountID, now)
	businessSuccess := codexQuotaOverdraftWasInjected(ctx, accountID)
	if !persistSnapshot && !businessSuccess {
		// Do not turn every high-utilization response into a DB read/write. The
		// next throttled snapshot (or an explicit quota 429) will advance state.
		return
	}

	go func() {
		updateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var account *Account
		if persistSnapshot {
			if err := s.accountRepo.UpdateExtra(updateCtx, accountID, updates); err != nil {
				return
			}
		}
		if s.codexQuotaOverdraft == nil {
			return
		}
		if account == nil {
			current, err := s.accountRepo.GetByID(updateCtx, accountID)
			if err != nil || current == nil {
				return
			}
			account = current
		}
		mergeAccountExtra(account, updates)
		if businessSuccess {
			s.codexQuotaOverdraft.observeBusinessSuccess(account, "")
		} else {
			s.codexQuotaOverdraft.observeAccount(account, "")
		}
	}()
}

func (s *OpenAIGatewayService) observeCodexQuotaOverdraftScheduleSuccess(
	accountID int64,
	model string,
	requestCtx []context.Context,
) {
	if len(requestCtx) > 0 && s.codexQuotaOverdraft != nil && codexQuotaOverdraftWasInjected(requestCtx[0], accountID) {
		s.codexQuotaOverdraft.ObserveBusinessSuccessByID(accountID, model)
	}
}
