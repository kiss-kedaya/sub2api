package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
)

// Reads behind the key self-service account summary (GET /v1/sub2api/account): what the account behind an API key
// holds and has used. They are thin on purpose. Every figure comes from a read that already exists and already has its
// own cache and concurrency gate; nothing here queries usage_logs by itself, and nothing here writes.
//
// Callers must pass the owner of the authenticated key (apiKey.UserID) and nothing a client chose: the account is
// never selected by a request parameter.

// AccountWallet is the wallet of one user, in USD: Balance is what can be spent, Frozen is held for asynchronous
// batch jobs that are still running (the console shows the same two figures).
type AccountWallet struct {
	Balance float64
	Frozen  float64
}

// GetAccountWallet reads one user's wallet straight from the user row, the same value the console and GET /v1/usage
// show. The balance carried by the API key auth snapshot is not used: it can be older than the last request.
func (s *APIKeyService) GetAccountWallet(ctx context.Context, userID int64) (*AccountWallet, error) {
	if s == nil || s.userRepo == nil || userID <= 0 {
		return nil, fmt.Errorf("get account wallet: invalid user")
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get account wallet: %w", err)
	}
	if user == nil {
		return nil, fmt.Errorf("get account wallet: %w", ErrUserNotFound)
	}
	return &AccountWallet{Balance: user.Balance, Frozen: user.FrozenBalance}, nil
}

// ListActiveSubscriptions returns the user's active (not expired) subscriptions with their groups loaded: the read
// GetAvailableGroups already makes, in one round trip.
func (s *APIKeyService) ListActiveSubscriptions(ctx context.Context, userID int64) ([]UserSubscription, error) {
	if s == nil || s.userSubRepo == nil || userID <= 0 {
		return nil, fmt.Errorf("list active subscriptions: invalid user")
	}
	subs, err := s.userSubRepo.ListActiveByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list active subscriptions: %w", err)
	}
	return subs, nil
}

// GetUserDayUsageSummary returns the sanitized aggregate counters for ONE USER (all of the user's API keys together)
// over the local day [dayStart, dayEnd).
//
// It reuses what exists instead of scanning usage_logs again:
//   - When the day is the one the user dashboard calls "today" (the site's own calendar day, still running), the
//     answer is the dashboard's: GetUserDashboardStats, whose usage part is cached (days that are over once, today
//     briefly) and shared with the console, so a status bar and the console page read the same entry.
//   - Any other day boundary (a caller's time zone with a different midnight) is the per-user aggregate
//     GetUserStatsAggregated, which has its own cache keyed by the exact range and the same concurrency gate.
//
// Either way a busy or slow database comes back as an error and the caller leaves the block out; it is never
// turned into zeroes here.
func (s *UsageService) GetUserDayUsageSummary(ctx context.Context, userID int64, dayStart, dayEnd time.Time) (*usagestats.APIKeyUsageSummary, error) {
	if s == nil || s.usageRepo == nil || userID <= 0 {
		return nil, fmt.Errorf("get user day usage summary: invalid user")
	}
	if today := timezone.Today(); dayStart.Equal(today) && dayEnd.Equal(today.AddDate(0, 0, 1)) {
		stats, err := s.usageRepo.GetUserDashboardStats(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("get user day usage summary: %w", err)
		}
		if stats == nil {
			return nil, fmt.Errorf("get user day usage summary: empty dashboard stats")
		}
		return &usagestats.APIKeyUsageSummary{
			Requests:         stats.TodayRequests,
			InputTokens:      stats.TodayInputTokens,
			OutputTokens:     stats.TodayOutputTokens,
			CacheReadTokens:  stats.TodayCacheReadTokens,
			CacheWriteTokens: stats.TodayCacheCreationTokens,
			TotalTokens:      stats.TodayTokens,
			Cost:             stats.TodayCost,
			ActualCost:       stats.TodayActualCost,
		}, nil
	}
	stats, err := s.usageRepo.GetUserStatsAggregated(ctx, userID, dayStart, dayEnd)
	if err != nil {
		return nil, fmt.Errorf("get user day usage summary: %w", err)
	}
	if stats == nil {
		return nil, fmt.Errorf("get user day usage summary: empty stats")
	}
	return &usagestats.APIKeyUsageSummary{
		Requests:         stats.TotalRequests,
		InputTokens:      stats.TotalInputTokens,
		OutputTokens:     stats.TotalOutputTokens,
		CacheReadTokens:  stats.TotalCacheReadTokens,
		CacheWriteTokens: stats.TotalCacheCreationTokens,
		TotalTokens:      stats.TotalTokens,
		Cost:             stats.TotalCost,
		ActualCost:       stats.TotalActualCost,
	}, nil
}
