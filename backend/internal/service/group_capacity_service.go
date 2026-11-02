package service

import (
	"context"
	"sync"
	"time"
)

// GroupActiveUsersWindow is the rolling window used to count distinct users that
// actually produced traffic. Five minutes matches the shortest RPM window and
// keeps the aggregate bounded to a tiny slice of usage_logs.
const GroupActiveUsersWindow = 5 * time.Minute

// groupActiveUsersCacheTTL deduplicates repeated capacity polls; the underlying
// query is index bounded but still hits PostgreSQL.
const groupActiveUsersCacheTTL = 30 * time.Second

// GroupCapacitySummary holds aggregated capacity for a single group.
//
// Truthfulness contract: SessionsUsed is only meaningful when SessionsTracked is
// true. Session counters are written exclusively for accounts with
// max_sessions > 0, so a group without such accounts has no session data at all;
// clients must not render SessionsUsed as "0 users" in that case. ActiveUsers is
// the honest substitute: distinct users with a request inside
// ActiveUsersWindowSeconds, or ActiveUsersKnown=false when the probe is
// unavailable.
type GroupCapacitySummary struct {
	GroupID         int64 `json:"group_id"`
	ConcurrencyUsed int   `json:"concurrency_used"`
	ConcurrencyMax  int   `json:"concurrency_max"`
	SessionsUsed    int   `json:"sessions_used"`
	SessionsMax     int   `json:"sessions_max"`
	SessionsTracked bool  `json:"sessions_tracked"`
	RPMUsed         int   `json:"rpm_used"`
	RPMMax          int   `json:"rpm_max"`
	// ActiveUsers counts distinct users with a billed request inside the window.
	ActiveUsers              int  `json:"active_users"`
	ActiveUsersWindowSeconds int  `json:"active_users_window_seconds"`
	ActiveUsersKnown         bool `json:"active_users_known"`
	RuntimeKnown             bool `json:"runtime_known"`
}

// GroupActiveUsersProbe reports distinct active users per group. It is optional:
// when absent the summary reports ActiveUsersKnown=false instead of zero.
type GroupActiveUsersProbe interface {
	GetGroupActiveUserCounts(ctx context.Context, groupIDs []int64, since time.Time) (map[int64]int, error)
}

// GroupCapacityOption customises GroupCapacityService without breaking the
// existing provider signature.
type GroupCapacityOption func(*GroupCapacityService)

// WithGroupActiveUsersProbe wires the distinct-active-user probe.
func WithGroupActiveUsersProbe(probe GroupActiveUsersProbe) GroupCapacityOption {
	return func(s *GroupCapacityService) { s.activeUsersProbe = probe }
}

type groupActiveUsersCacheEntry struct {
	counts    map[int64]int
	expiresAt time.Time
}

// GroupAccountCapacityRow is the lightweight account projection needed for
// capacity summary aggregation.
type GroupAccountCapacityRow struct {
	GroupID             int64
	AccountID           int64
	Concurrency         int
	Extra               map[string]any
	SessionWindowStart  *time.Time
	SessionWindowEnd    *time.Time
	SessionWindowStatus string
}

type groupCapacityActiveGroupIDLister interface {
	ListActiveIDs(ctx context.Context) ([]int64, error)
}

type groupCapacityAccountLister interface {
	ListSchedulableCapacityByGroupIDs(ctx context.Context, groupIDs []int64) ([]GroupAccountCapacityRow, error)
}

// GroupCapacityService aggregates per-group capacity from runtime data.
type GroupCapacityService struct {
	accountRepo        AccountRepository
	groupRepo          GroupRepository
	concurrencyService *ConcurrencyService
	sessionLimitCache  SessionLimitCache
	rpmCache           RPMCache

	activeUsersProbe GroupActiveUsersProbe
	activeUsersMu    sync.Mutex
	activeUsersCache *groupActiveUsersCacheEntry
}

// NewGroupCapacityService creates a new GroupCapacityService.
func NewGroupCapacityService(
	accountRepo AccountRepository,
	groupRepo GroupRepository,
	concurrencyService *ConcurrencyService,
	sessionLimitCache SessionLimitCache,
	rpmCache RPMCache,
	options ...GroupCapacityOption,
) *GroupCapacityService {
	s := &GroupCapacityService{
		accountRepo:        accountRepo,
		groupRepo:          groupRepo,
		concurrencyService: concurrencyService,
		sessionLimitCache:  sessionLimitCache,
		rpmCache:           rpmCache,
	}
	for _, option := range options {
		if option != nil {
			option(s)
		}
	}
	return s
}

// ProvideGroupCapacityService is the wire provider. It keeps the extra optional
// active-user probe out of NewGroupCapacityService so tests and other callers
// are unaffected, while wire regeneration still wires the probe.
func ProvideGroupCapacityService(
	accountRepo AccountRepository,
	groupRepo GroupRepository,
	concurrencyService *ConcurrencyService,
	sessionLimitCache SessionLimitCache,
	rpmCache RPMCache,
	activeUsersService *UsageService,
) *GroupCapacityService {
	return NewGroupCapacityService(
		accountRepo,
		groupRepo,
		concurrencyService,
		sessionLimitCache,
		rpmCache,
		WithGroupActiveUsersProbe(activeUsersService),
	)
}

// loadActiveUsers returns distinct active users per requested group, or
// known=false when the probe is missing or failing. Results are cached for
// groupActiveUsersCacheTTL so a polling admin page does not re-hit the database.
func (s *GroupCapacityService) loadActiveUsers(ctx context.Context, groupIDs []int64, now time.Time) (map[int64]int, bool) {
	if s.activeUsersProbe == nil || len(groupIDs) == 0 {
		return nil, false
	}

	s.activeUsersMu.Lock()
	cached := s.activeUsersCache
	if cached != nil && now.Before(cached.expiresAt) && coversAllGroupIDs(cached.counts, groupIDs) {
		s.activeUsersMu.Unlock()
		return cached.counts, true
	}
	s.activeUsersMu.Unlock()

	counts, err := s.activeUsersProbe.GetGroupActiveUserCounts(ctx, groupIDs, now.Add(-GroupActiveUsersWindow))
	if err != nil {
		return nil, false
	}
	normalized := make(map[int64]int, len(groupIDs))
	for _, groupID := range groupIDs {
		normalized[groupID] = counts[groupID]
	}

	s.activeUsersMu.Lock()
	s.activeUsersCache = &groupActiveUsersCacheEntry{
		counts:    normalized,
		expiresAt: now.Add(groupActiveUsersCacheTTL),
	}
	s.activeUsersMu.Unlock()
	return normalized, true
}

func coversAllGroupIDs(counts map[int64]int, groupIDs []int64) bool {
	if counts == nil {
		return false
	}
	for _, groupID := range groupIDs {
		if _, ok := counts[groupID]; !ok {
			return false
		}
	}
	return true
}

// GetAllGroupCapacity returns capacity summary for all active groups.
func (s *GroupCapacityService) GetAllGroupCapacity(ctx context.Context) ([]GroupCapacitySummary, error) {
	groupIDs, err := s.listActiveGroupIDs(ctx)
	if err != nil {
		return nil, err
	}

	if lister, ok := s.accountRepo.(groupCapacityAccountLister); ok {
		return s.getGroupCapacitiesBatch(ctx, groupIDs, lister)
	}

	return s.getGroupCapacitiesSequential(ctx, groupIDs), nil
}

func (s *GroupCapacityService) listActiveGroupIDs(ctx context.Context) ([]int64, error) {
	if lister, ok := s.groupRepo.(groupCapacityActiveGroupIDLister); ok {
		return lister.ListActiveIDs(ctx)
	}

	groups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	groupIDs := make([]int64, 0, len(groups))
	for i := range groups {
		groupIDs = append(groupIDs, groups[i].ID)
	}
	return groupIDs, nil
}

func (s *GroupCapacityService) getGroupCapacitiesSequential(ctx context.Context, groupIDs []int64) []GroupCapacitySummary {
	results := make([]GroupCapacitySummary, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		cap, err := s.getGroupCapacity(ctx, groupID)
		if err != nil {
			// Skip groups with errors, return partial results
			continue
		}
		cap.GroupID = groupID
		results = append(results, cap)
	}
	if len(results) > 0 {
		activeGroupIDs := make([]int64, 0, len(results))
		for i := range results {
			activeGroupIDs = append(activeGroupIDs, results[i].GroupID)
		}
		if counts, known := s.loadActiveUsers(ctx, activeGroupIDs, time.Now()); known {
			for i := range results {
				results[i].ActiveUsers = counts[results[i].GroupID]
				results[i].ActiveUsersWindowSeconds = int(GroupActiveUsersWindow.Seconds())
				results[i].ActiveUsersKnown = true
			}
		}
	}
	return results
}

type groupCapacityAccountRef struct {
	groupID   int64
	accountID int64
}

func (s *GroupCapacityService) getGroupCapacitiesBatch(ctx context.Context, groupIDs []int64, lister groupCapacityAccountLister) ([]GroupCapacitySummary, error) {
	results := make([]GroupCapacitySummary, len(groupIDs))
	groupIndex := make(map[int64]int, len(groupIDs))
	for i, groupID := range groupIDs {
		results[i].GroupID = groupID
		groupIndex[groupID] = i
	}
	if len(groupIDs) == 0 {
		return results, nil
	}

	rows, err := lister.ListSchedulableCapacityByGroupIDs(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		for i := range results {
			results[i].RuntimeKnown = true
		}
		return results, nil
	}

	refs := make([]groupCapacityAccountRef, 0, len(rows))
	groupHasAccounts := make(map[int64]bool, len(groupIDs))
	seenGroupAccount := make(map[groupCapacityAccountRef]struct{}, len(rows))
	accountIDSet := make(map[int64]struct{}, len(rows))
	accountIDs := make([]int64, 0, len(rows))
	sessionTimeouts := make(map[int64]time.Duration)

	for _, row := range rows {
		idx, ok := groupIndex[row.GroupID]
		if !ok || row.AccountID <= 0 {
			continue
		}
		groupHasAccounts[row.GroupID] = true

		ref := groupCapacityAccountRef{groupID: row.GroupID, accountID: row.AccountID}
		if _, ok := seenGroupAccount[ref]; ok {
			continue
		}
		seenGroupAccount[ref] = struct{}{}
		refs = append(refs, ref)

		if _, ok := accountIDSet[row.AccountID]; !ok {
			accountIDSet[row.AccountID] = struct{}{}
			accountIDs = append(accountIDs, row.AccountID)
		}

		acc := Account{
			ID:                  row.AccountID,
			Concurrency:         row.Concurrency,
			Extra:               row.Extra,
			SessionWindowStart:  row.SessionWindowStart,
			SessionWindowEnd:    row.SessionWindowEnd,
			SessionWindowStatus: row.SessionWindowStatus,
		}

		results[idx].ConcurrencyMax += acc.Concurrency

		if maxSessions := acc.GetMaxSessions(); maxSessions > 0 {
			results[idx].SessionsMax += maxSessions
			timeout := time.Duration(acc.GetSessionIdleTimeoutMinutes()) * time.Minute
			if timeout <= 0 {
				timeout = 5 * time.Minute
			}
			sessionTimeouts[acc.ID] = timeout
		}

		if rpm := acc.GetBaseRPM(); rpm > 0 {
			results[idx].RPMMax += rpm
		}
	}

	if len(accountIDs) == 0 {
		for i := range results {
			results[i].RuntimeKnown = true
		}
		return results, nil
	}

	concurrencyMap := map[int64]int{}
	runtimeKnown := s.concurrencyService != nil
	if s.concurrencyService != nil {
		var cacheErr error
		concurrencyMap, cacheErr = s.concurrencyService.GetAccountConcurrencyBatch(ctx, accountIDs)
		runtimeKnown = cacheErr == nil
	}

	sessionAccountIDs := accountIDsForGroupsWithLimit(refs, groupIndex, results, func(summary GroupCapacitySummary) bool {
		return summary.SessionsMax > 0
	})
	var sessionsMap map[int64]int
	if len(sessionAccountIDs) > 0 && s.sessionLimitCache != nil {
		var cacheErr error
		sessionsMap, cacheErr = s.sessionLimitCache.GetActiveSessionCountBatch(ctx, sessionAccountIDs, sessionTimeouts)
		runtimeKnown = runtimeKnown && cacheErr == nil
	} else if len(sessionAccountIDs) > 0 {
		runtimeKnown = false
	}

	rpmAccountIDs := accountIDsForGroupsWithLimit(refs, groupIndex, results, func(summary GroupCapacitySummary) bool {
		return summary.RPMMax > 0
	})
	var rpmMap map[int64]int
	if len(rpmAccountIDs) > 0 && s.rpmCache != nil {
		var cacheErr error
		rpmMap, cacheErr = s.rpmCache.GetRPMBatch(ctx, rpmAccountIDs)
		runtimeKnown = runtimeKnown && cacheErr == nil
	} else if len(rpmAccountIDs) > 0 {
		runtimeKnown = false
	}

	for _, ref := range refs {
		idx := groupIndex[ref.groupID]
		results[idx].ConcurrencyUsed += concurrencyMap[ref.accountID]
		if sessionsMap != nil && results[idx].SessionsMax > 0 {
			results[idx].SessionsUsed += sessionsMap[ref.accountID]
		}
		if rpmMap != nil && results[idx].RPMMax > 0 {
			results[idx].RPMUsed += rpmMap[ref.accountID]
		}
	}
	for i := range results {
		results[i].RuntimeKnown = !groupHasAccounts[results[i].GroupID] || runtimeKnown
		results[i].SessionsTracked = results[i].SessionsMax > 0
	}
	if counts, known := s.loadActiveUsers(ctx, groupIDs, time.Now()); known {
		for i := range results {
			results[i].ActiveUsers = counts[results[i].GroupID]
			results[i].ActiveUsersWindowSeconds = int(GroupActiveUsersWindow.Seconds())
			results[i].ActiveUsersKnown = true
		}
	}
	return results, nil
}

func accountIDsForGroupsWithLimit(refs []groupCapacityAccountRef, groupIndex map[int64]int, summaries []GroupCapacitySummary, include func(GroupCapacitySummary) bool) []int64 {
	seen := make(map[int64]struct{})
	accountIDs := make([]int64, 0)
	for _, ref := range refs {
		idx, ok := groupIndex[ref.groupID]
		if !ok || !include(summaries[idx]) {
			continue
		}
		if _, ok := seen[ref.accountID]; ok {
			continue
		}
		seen[ref.accountID] = struct{}{}
		accountIDs = append(accountIDs, ref.accountID)
	}
	return accountIDs
}

func (s *GroupCapacityService) getGroupCapacity(ctx context.Context, groupID int64) (GroupCapacitySummary, error) {
	accounts, err := s.accountRepo.ListSchedulableByGroupID(ctx, groupID)
	if err != nil {
		return GroupCapacitySummary{}, err
	}
	if len(accounts) == 0 {
		return GroupCapacitySummary{GroupID: groupID, RuntimeKnown: true}, nil
	}

	// Collect account IDs and config values
	accountIDs := make([]int64, 0, len(accounts))
	sessionTimeouts := make(map[int64]time.Duration)
	var concurrencyMax, sessionsMax, rpmMax int

	for i := range accounts {
		acc := &accounts[i]
		accountIDs = append(accountIDs, acc.ID)
		concurrencyMax += acc.Concurrency

		if ms := acc.GetMaxSessions(); ms > 0 {
			sessionsMax += ms
			timeout := time.Duration(acc.GetSessionIdleTimeoutMinutes()) * time.Minute
			if timeout <= 0 {
				timeout = 5 * time.Minute
			}
			sessionTimeouts[acc.ID] = timeout
		}

		if rpm := acc.GetBaseRPM(); rpm > 0 {
			rpmMax += rpm
		}
	}

	// Batch query runtime data from Redis
	runtimeKnown := s.concurrencyService != nil
	concurrencyMap := map[int64]int{}
	if s.concurrencyService != nil {
		var cacheErr error
		concurrencyMap, cacheErr = s.concurrencyService.GetAccountConcurrencyBatch(ctx, accountIDs)
		runtimeKnown = cacheErr == nil
	}

	var sessionsMap map[int64]int
	if sessionsMax > 0 && s.sessionLimitCache != nil {
		var cacheErr error
		sessionsMap, cacheErr = s.sessionLimitCache.GetActiveSessionCountBatch(ctx, accountIDs, sessionTimeouts)
		runtimeKnown = runtimeKnown && cacheErr == nil
	} else if sessionsMax > 0 {
		runtimeKnown = false
	}

	var rpmMap map[int64]int
	if rpmMax > 0 && s.rpmCache != nil {
		var cacheErr error
		rpmMap, cacheErr = s.rpmCache.GetRPMBatch(ctx, accountIDs)
		runtimeKnown = runtimeKnown && cacheErr == nil
	} else if rpmMax > 0 {
		runtimeKnown = false
	}

	// Aggregate
	var concurrencyUsed, sessionsUsed, rpmUsed int
	for _, id := range accountIDs {
		concurrencyUsed += concurrencyMap[id]
		if sessionsMap != nil {
			sessionsUsed += sessionsMap[id]
		}
		if rpmMap != nil {
			rpmUsed += rpmMap[id]
		}
	}

	return GroupCapacitySummary{
		ConcurrencyUsed: concurrencyUsed,
		ConcurrencyMax:  concurrencyMax,
		SessionsUsed:    sessionsUsed,
		SessionsMax:     sessionsMax,
		SessionsTracked: sessionsMax > 0,
		RPMUsed:         rpmUsed,
		RPMMax:          rpmMax,
		RuntimeKnown:    runtimeKnown,
	}, nil
}
