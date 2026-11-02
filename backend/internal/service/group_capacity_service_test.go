package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type groupCapacityAccountRepoStub struct {
	AccountRepository
	rows      []GroupAccountCapacityRow
	requested []int64
}

func (s *groupCapacityAccountRepoStub) ListSchedulableCapacityByGroupIDs(_ context.Context, groupIDs []int64) ([]GroupAccountCapacityRow, error) {
	s.requested = append([]int64(nil), groupIDs...)
	return append([]GroupAccountCapacityRow(nil), s.rows...), nil
}

type groupCapacityGroupRepoStub struct {
	GroupRepository
	groupIDs  []int64
	listCalls int
}

func (s *groupCapacityGroupRepoStub) ListActiveIDs(context.Context) ([]int64, error) {
	s.listCalls++
	return append([]int64(nil), s.groupIDs...), nil
}

type groupCapacityConcurrencyCacheStub struct {
	ConcurrencyCache
	counts    map[int64]int
	requested []int64
	err       error
}

func (s *groupCapacityConcurrencyCacheStub) GetAccountConcurrencyBatch(_ context.Context, accountIDs []int64) (map[int64]int, error) {
	s.requested = append([]int64(nil), accountIDs...)
	out := make(map[int64]int, len(accountIDs))
	for _, id := range accountIDs {
		out[id] = s.counts[id]
	}
	return out, s.err
}

type groupCapacitySessionCacheStub struct {
	SessionLimitCache
	counts       map[int64]int
	requested    []int64
	idleTimeouts map[int64]time.Duration
	err          error
}

func (s *groupCapacitySessionCacheStub) GetActiveSessionCountBatch(_ context.Context, accountIDs []int64, idleTimeouts map[int64]time.Duration) (map[int64]int, error) {
	s.requested = append([]int64(nil), accountIDs...)
	s.idleTimeouts = make(map[int64]time.Duration, len(idleTimeouts))
	for id, timeout := range idleTimeouts {
		s.idleTimeouts[id] = timeout
	}
	out := make(map[int64]int, len(accountIDs))
	for _, id := range accountIDs {
		out[id] = s.counts[id]
	}
	return out, s.err
}

type groupCapacityRPMCacheStub struct {
	RPMCache
	counts    map[int64]int
	requested []int64
	err       error
}

func (s *groupCapacityRPMCacheStub) GetRPMBatch(_ context.Context, accountIDs []int64) (map[int64]int, error) {
	s.requested = append([]int64(nil), accountIDs...)
	out := make(map[int64]int, len(accountIDs))
	for _, id := range accountIDs {
		out[id] = s.counts[id]
	}
	return out, s.err
}

func TestGetAllGroupCapacityBatchAggregatesRuntimeAndLimits(t *testing.T) {
	accountRepo := &groupCapacityAccountRepoStub{
		rows: []GroupAccountCapacityRow{
			{
				GroupID:     10,
				AccountID:   1,
				Concurrency: 2,
				Extra: map[string]any{
					"max_sessions":                 3,
					"session_idle_timeout_minutes": 7,
					"base_rpm":                     11,
				},
			},
			{
				GroupID:     20,
				AccountID:   1,
				Concurrency: 2,
				Extra: map[string]any{
					"max_sessions":                 3,
					"session_idle_timeout_minutes": 7,
					"base_rpm":                     11,
				},
			},
			{
				GroupID:     20,
				AccountID:   2,
				Concurrency: 4,
				Extra: map[string]any{
					"max_sessions":                 1,
					"session_idle_timeout_minutes": 9,
					"base_rpm":                     13,
				},
			},
		},
	}
	groupRepo := &groupCapacityGroupRepoStub{groupIDs: []int64{10, 20}}
	concurrencyCache := &groupCapacityConcurrencyCacheStub{counts: map[int64]int{1: 1, 2: 2}}
	sessionCache := &groupCapacitySessionCacheStub{counts: map[int64]int{1: 2, 2: 1}}
	rpmCache := &groupCapacityRPMCacheStub{counts: map[int64]int{1: 5, 2: 7}}
	svc := NewGroupCapacityService(
		accountRepo,
		groupRepo,
		NewConcurrencyService(concurrencyCache),
		sessionCache,
		rpmCache,
	)

	results, err := svc.GetAllGroupCapacity(context.Background())
	require.NoError(t, err)

	require.Equal(t, 1, groupRepo.listCalls)
	require.Equal(t, []int64{10, 20}, accountRepo.requested)
	require.Equal(t, []int64{1, 2}, concurrencyCache.requested)
	require.ElementsMatch(t, []int64{1, 2}, sessionCache.requested)
	require.ElementsMatch(t, []int64{1, 2}, rpmCache.requested)
	require.Equal(t, 7*time.Minute, sessionCache.idleTimeouts[1])
	require.Equal(t, 9*time.Minute, sessionCache.idleTimeouts[2])

	require.Equal(t, []GroupCapacitySummary{
		{
			GroupID:         10,
			ConcurrencyUsed: 1,
			ConcurrencyMax:  2,
			SessionsUsed:    2,
			SessionsMax:     3,
			SessionsTracked: true,
			RPMUsed:         5,
			RPMMax:          11,
			RuntimeKnown:    true,
		},
		{
			GroupID:         20,
			ConcurrencyUsed: 3,
			ConcurrencyMax:  6,
			SessionsUsed:    3,
			SessionsMax:     4,
			SessionsTracked: true,
			RPMUsed:         12,
			RPMMax:          24,
			RuntimeKnown:    true,
		},
	}, results)
}

func TestGetAllGroupCapacityBatchKeepsEmptyGroupRows(t *testing.T) {
	accountRepo := &groupCapacityAccountRepoStub{
		rows: []GroupAccountCapacityRow{
			{GroupID: 20, AccountID: 2, Concurrency: 4},
		},
	}
	groupRepo := &groupCapacityGroupRepoStub{groupIDs: []int64{10, 20}}
	svc := NewGroupCapacityService(accountRepo, groupRepo, nil, nil, nil)

	results, err := svc.GetAllGroupCapacity(context.Background())
	require.NoError(t, err)

	require.Equal(t, []GroupCapacitySummary{
		{GroupID: 10, RuntimeKnown: true},
		{GroupID: 20, ConcurrencyMax: 4},
	}, results)
}

func TestGetAllGroupCapacityBatchMarksAllEmptyGroupsKnown(t *testing.T) {
	accountRepo := &groupCapacityAccountRepoStub{}
	groupRepo := &groupCapacityGroupRepoStub{groupIDs: []int64{10, 20}}
	svc := NewGroupCapacityService(accountRepo, groupRepo, nil, nil, nil)

	results, err := svc.GetAllGroupCapacity(context.Background())
	require.NoError(t, err)
	require.Equal(t, []GroupCapacitySummary{
		{GroupID: 10, RuntimeKnown: true},
		{GroupID: 20, RuntimeKnown: true},
	}, results)
}

func TestGetAllGroupCapacityMarksRuntimeUnknownWhenRedisReadFails(t *testing.T) {
	accountRepo := &groupCapacityAccountRepoStub{rows: []GroupAccountCapacityRow{{GroupID: 10, AccountID: 1, Concurrency: 2}}}
	groupRepo := &groupCapacityGroupRepoStub{groupIDs: []int64{10}}
	concurrencyCache := &groupCapacityConcurrencyCacheStub{err: context.DeadlineExceeded}
	svc := NewGroupCapacityService(accountRepo, groupRepo, NewConcurrencyService(concurrencyCache), nil, nil)

	results, err := svc.GetAllGroupCapacity(context.Background())
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, 2, results[0].ConcurrencyMax)
	require.Zero(t, results[0].ConcurrencyUsed)
	require.False(t, results[0].RuntimeKnown)
}

// --- distinct active users (capacity display for groups without session limits) ---

type groupCapacityActiveUsersStub struct {
	counts   map[int64]int
	err      error
	calls    int
	groupIDs []int64
	since    time.Time
}

func (s *groupCapacityActiveUsersStub) GetGroupActiveUserCounts(_ context.Context, groupIDs []int64, since time.Time) (map[int64]int, error) {
	s.calls++
	s.groupIDs = append([]int64(nil), groupIDs...)
	s.since = since
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[int64]int, len(groupIDs))
	for _, id := range groupIDs {
		out[id] = s.counts[id]
	}
	return out, nil
}

// A group whose accounts do not enable max_sessions has no session data at all.
// The summary must expose that as sessions_tracked=false plus real active users,
// never as "0 sessions in use".
func TestGetAllGroupCapacityReportsActiveUsersWithoutSessionTracking(t *testing.T) {
	accountRepo := &groupCapacityAccountRepoStub{
		rows: []GroupAccountCapacityRow{
			{GroupID: 10, AccountID: 1, Concurrency: 2},
			{GroupID: 20, AccountID: 2, Concurrency: 4},
		},
	}
	groupRepo := &groupCapacityGroupRepoStub{groupIDs: []int64{10, 20}}
	probe := &groupCapacityActiveUsersStub{counts: map[int64]int{10: 3, 20: 7}}
	svc := NewGroupCapacityService(
		accountRepo,
		groupRepo,
		nil,
		nil,
		nil,
		WithGroupActiveUsersProbe(probe),
	)

	results, err := svc.GetAllGroupCapacity(context.Background())
	require.NoError(t, err)
	require.Len(t, results, 2)

	for _, row := range results {
		require.False(t, row.SessionsTracked, "no account in these groups has max_sessions")
		require.Zero(t, row.SessionsMax)
		require.True(t, row.ActiveUsersKnown)
		require.Equal(t, int(GroupActiveUsersWindow.Seconds()), row.ActiveUsersWindowSeconds)
	}
	require.Equal(t, 3, results[0].ActiveUsers)
	require.Equal(t, 7, results[1].ActiveUsers)

	require.Equal(t, 1, probe.calls)
	require.ElementsMatch(t, []int64{10, 20}, probe.groupIDs)
	require.WithinDuration(t, time.Now().Add(-GroupActiveUsersWindow), probe.since, time.Minute)
}

func TestGetAllGroupCapacityActiveUsersProbeFailureStaysUnknown(t *testing.T) {
	accountRepo := &groupCapacityAccountRepoStub{rows: []GroupAccountCapacityRow{{GroupID: 10, AccountID: 1, Concurrency: 2}}}
	groupRepo := &groupCapacityGroupRepoStub{groupIDs: []int64{10}}
	probe := &groupCapacityActiveUsersStub{err: context.DeadlineExceeded}
	svc := NewGroupCapacityService(accountRepo, groupRepo, nil, nil, nil, WithGroupActiveUsersProbe(probe))

	results, err := svc.GetAllGroupCapacity(context.Background())
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.False(t, results[0].ActiveUsersKnown)
	require.Zero(t, results[0].ActiveUsers)
}

func TestGetAllGroupCapacityWithoutActiveUsersProbeStaysUnknown(t *testing.T) {
	accountRepo := &groupCapacityAccountRepoStub{rows: []GroupAccountCapacityRow{{GroupID: 10, AccountID: 1, Concurrency: 2}}}
	groupRepo := &groupCapacityGroupRepoStub{groupIDs: []int64{10}}
	svc := NewGroupCapacityService(accountRepo, groupRepo, nil, nil, nil)

	results, err := svc.GetAllGroupCapacity(context.Background())
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.False(t, results[0].ActiveUsersKnown)
}

func TestGetAllGroupCapacityCachesActiveUsersBetweenPolls(t *testing.T) {
	accountRepo := &groupCapacityAccountRepoStub{rows: []GroupAccountCapacityRow{{GroupID: 10, AccountID: 1, Concurrency: 2}}}
	groupRepo := &groupCapacityGroupRepoStub{groupIDs: []int64{10}}
	probe := &groupCapacityActiveUsersStub{counts: map[int64]int{10: 5}}
	svc := NewGroupCapacityService(accountRepo, groupRepo, nil, nil, nil, WithGroupActiveUsersProbe(probe))

	_, err := svc.GetAllGroupCapacity(context.Background())
	require.NoError(t, err)
	_, err = svc.GetAllGroupCapacity(context.Background())
	require.NoError(t, err)

	require.Equal(t, 1, probe.calls, "second poll inside the TTL must reuse the cached counts")
}
