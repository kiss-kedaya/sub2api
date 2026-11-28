package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type memoryGroupWatchRepo struct {
	mu            sync.Mutex
	subs          map[int64]map[int64]struct{} // userID -> groupIDs
	subscribers   map[int64][]GroupWatchSubscriber
	states        map[int64]GroupMonitorWatchState
	notifications []UserGroupWatchNotification
	dedupeKeys    map[string]struct{}
	nextID        int64
}

func newMemoryGroupWatchRepo() *memoryGroupWatchRepo {
	return &memoryGroupWatchRepo{
		subs:        map[int64]map[int64]struct{}{},
		subscribers: map[int64][]GroupWatchSubscriber{},
		states:      map[int64]GroupMonitorWatchState{},
		dedupeKeys:  map[string]struct{}{},
		nextID:      1,
	}
}

func (r *memoryGroupWatchRepo) ListSubscribedGroupIDs(_ context.Context, userID int64) ([]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int64, 0, len(r.subs[userID]))
	for gid := range r.subs[userID] {
		out = append(out, gid)
	}
	return out, nil
}

func (r *memoryGroupWatchRepo) Subscribe(_ context.Context, userID, groupID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.subs[userID] == nil {
		r.subs[userID] = map[int64]struct{}{}
	}
	r.subs[userID][groupID] = struct{}{}
	return nil
}

func (r *memoryGroupWatchRepo) Unsubscribe(_ context.Context, userID, groupID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subs[userID], groupID)
	return nil
}

func (r *memoryGroupWatchRepo) ListWatchedGroupIDs(_ context.Context) ([]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[int64]struct{}{}
	for _, gmap := range r.subs {
		for gid := range gmap {
			seen[gid] = struct{}{}
		}
	}
	for gid, list := range r.subscribers {
		if len(list) > 0 {
			seen[gid] = struct{}{}
		}
	}
	out := make([]int64, 0, len(seen))
	for gid := range seen {
		out = append(out, gid)
	}
	return out, nil
}

func (r *memoryGroupWatchRepo) ListSubscriberUsersByGroupID(_ context.Context, groupID int64) ([]GroupWatchSubscriber, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]GroupWatchSubscriber(nil), r.subscribers[groupID]...), nil
}

func (r *memoryGroupWatchRepo) GetGroupWatchStates(_ context.Context, groupIDs []int64) (map[int64]GroupMonitorWatchState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[int64]GroupMonitorWatchState, len(groupIDs))
	for _, gid := range groupIDs {
		if st, ok := r.states[gid]; ok {
			out[gid] = st
		}
	}
	return out, nil
}

func (r *memoryGroupWatchRepo) UpsertGroupWatchState(_ context.Context, state GroupMonitorWatchState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states[state.GroupID] = state
	return nil
}

func (r *memoryGroupWatchRepo) InsertNotificationIfNew(_ context.Context, notif UserGroupWatchNotification) (bool, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := notif.DedupeKey + "#" + string(rune(notif.UserID))
	if _, exists := r.dedupeKeys[key]; exists {
		return false, 0, nil
	}
	r.dedupeKeys[key] = struct{}{}
	notif.ID = r.nextID
	r.nextID++
	r.notifications = append(r.notifications, notif)
	return true, notif.ID, nil
}

func (r *memoryGroupWatchRepo) SummaryNotifications(_ context.Context, userID int64) (int, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	total, unread := 0, 0
	for _, n := range r.notifications {
		if n.UserID != userID {
			continue
		}
		total++
		if n.ReadAt == nil {
			unread++
		}
	}
	return total, unread, nil
}

func (r *memoryGroupWatchRepo) ListNotifications(_ context.Context, userID int64, unreadOnly bool, limit int) ([]UserGroupWatchNotification, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]UserGroupWatchNotification, 0)
	for i := len(r.notifications) - 1; i >= 0; i-- {
		n := r.notifications[i]
		if n.UserID != userID {
			continue
		}
		if unreadOnly && n.ReadAt != nil {
			continue
		}
		out = append(out, n)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *memoryGroupWatchRepo) MarkNotificationRead(_ context.Context, userID, notificationID int64, readAt time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.notifications {
		if r.notifications[i].ID == notificationID && r.notifications[i].UserID == userID {
			if r.notifications[i].ReadAt == nil {
				t := readAt
				r.notifications[i].ReadAt = &t
			}
			return true, nil
		}
	}
	return false, nil
}

func (r *memoryGroupWatchRepo) MarkAllNotificationsRead(_ context.Context, userID int64, readAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.notifications {
		if r.notifications[i].UserID == userID && r.notifications[i].ReadAt == nil {
			t := readAt
			r.notifications[i].ReadAt = &t
		}
	}
	return nil
}

func (r *memoryGroupWatchRepo) PruneOldNotifications(_ context.Context, before time.Time, _ int) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.notifications[:0]
	var deleted int64
	for _, n := range r.notifications {
		if n.CreatedAt.Before(before) {
			deleted++
			continue
		}
		kept = append(kept, n)
	}
	r.notifications = kept
	return deleted, nil
}

type stubGroupWatchAuthorizer struct {
	byUser map[int64][]Group
	err    error
}

func (a *stubGroupWatchAuthorizer) GetAvailableGroups(_ context.Context, userID int64) ([]Group, error) {
	if a.err != nil {
		return nil, a.err
	}
	return a.byUser[userID], nil
}

type stubGroupWatchMonitorRepo struct {
	ChannelMonitorV2Repository
	cfg    *ChannelMonitorV2Config
	matrix *ChannelMonitorV2Matrix
}

func (m *stubGroupWatchMonitorRepo) GetConfig(_ context.Context) (*ChannelMonitorV2Config, error) {
	return m.cfg, nil
}

func (m *stubGroupWatchMonitorRepo) GetMatrix(_ context.Context, _ ChannelMonitorV2Filter, _ ChannelMonitorV2Config, _ ChannelMonitorV2GroupBy, _ bool) (*ChannelMonitorV2Matrix, error) {
	return m.matrix, nil
}

type recordingPushNotifier struct {
	mu     sync.Mutex
	pushes []struct {
		userID int64
		title  string
		body   string
		group  string
	}
	err error
}

func (p *recordingPushNotifier) PushToUser(_ context.Context, userID int64, title, body, group string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	p.pushes = append(p.pushes, struct {
		userID int64
		title  string
		body   string
		group  string
	}{userID, title, body, group})
	return nil
}

func (p *recordingPushNotifier) PushToAdmins(_ context.Context, _, _, _ string) error { return nil }

func TestClassifyMonitorBucketStatus_MatchesFrontendBarRules(t *testing.T) {
	t.Run("unknown sample is invalid", func(t *testing.T) {
		_, _, _, valid := ClassifyMonitorBucketStatus(ChannelMonitorV2TrendPoint{
			Health:  ChannelMonitorV2Health{Overall: "unknown"},
			Metrics: ChannelMonitorV2Metric{CacheRate: 0.0, ErrorRate: 1.0},
		})
		require.False(t, valid)
	})

	t.Run("low cache rate below 30 percent is black even with 100 percent availability", func(t *testing.T) {
		status, cachePct, availPct, valid := ClassifyMonitorBucketStatus(ChannelMonitorV2TrendPoint{
			Health:  ChannelMonitorV2Health{Overall: "critical"},
			Metrics: ChannelMonitorV2Metric{CacheRate: 0.299, ErrorRate: 0.0},
		})
		require.True(t, valid)
		require.Equal(t, GroupWatchStatusBlack, status)
		require.InDelta(t, 29.9, cachePct, 0.01)
		require.InDelta(t, 100.0, availPct, 0.01)
	})

	t.Run("low availability below 30 percent is black even with high cache rate", func(t *testing.T) {
		status, _, availPct, valid := ClassifyMonitorBucketStatus(ChannelMonitorV2TrendPoint{
			Health:  ChannelMonitorV2Health{Overall: "critical"},
			Metrics: ChannelMonitorV2Metric{CacheRate: 0.80, ErrorRate: 0.71},
		})
		require.True(t, valid)
		require.Equal(t, GroupWatchStatusBlack, status)
		require.InDelta(t, 29.0, availPct, 0.01)
	})

	t.Run("availability 80 percent with cache 30 percent is green", func(t *testing.T) {
		status, _, _, valid := ClassifyMonitorBucketStatus(ChannelMonitorV2TrendPoint{
			Health:  ChannelMonitorV2Health{Overall: "healthy"},
			Metrics: ChannelMonitorV2Metric{CacheRate: 0.30, ErrorRate: 0.20},
		})
		require.True(t, valid)
		require.Equal(t, GroupWatchStatusGreen, status)
	})

	t.Run("availability 75 percent with cache 50 percent is other (yellow)", func(t *testing.T) {
		status, _, _, valid := ClassifyMonitorBucketStatus(ChannelMonitorV2TrendPoint{
			Health:  ChannelMonitorV2Health{Overall: "warning"},
			Metrics: ChannelMonitorV2Metric{CacheRate: 0.50, ErrorRate: 0.25},
		})
		require.True(t, valid)
		require.Equal(t, GroupWatchStatusOther, status)
	})
}

func TestDiffGroupPricing(t *testing.T) {
	base := &Group{ID: 12, Name: "Claude 混合号池", Platform: PlatformAnthropic, RateMultiplier: 0.80}
	before := SnapshotGroupPricing(base)

	// Non-pricing edit produces no diff.
	base.Description = "only description changed"
	require.Empty(t, DiffGroupPricing(before, SnapshotGroupPricing(base)))

	// Base rate increase.
	base.RateMultiplier = 0.90
	changes := DiffGroupPricing(before, SnapshotGroupPricing(base))
	require.Len(t, changes, 1)
	require.Equal(t, "rate_multiplier", changes[0].Field)
	require.Contains(t, changes[0].Summary, "0.80x → 0.90x")
	require.Contains(t, changes[0].Summary, "上涨 +0.10x")

	// Peak rate + image price also detected.
	p1k := 0.05
	base.PeakRateEnabled = true
	base.PeakStart = "20:00"
	base.PeakEnd = "23:00"
	base.PeakRateMultiplier = 1.5
	base.ImagePrice1K = &p1k
	changes = DiffGroupPricing(before, SnapshotGroupPricing(base))
	require.Len(t, changes, 3)
}

func TestGroupWatchStateTransitions_BlackYellowGreenAndNoFlapSpam(t *testing.T) {
	ctx := context.Background()
	gid := int64(42)
	now := time.Date(2026, 11, 28, 10, 0, 0, 0, time.UTC)

	repo := newMemoryGroupWatchRepo()
	repo.subscribers[gid] = []GroupWatchSubscriber{
		{UserID: 10, Email: "alice@example.com", Username: "alice", Role: "user"},
		{UserID: 11, Email: "revoked@example.com", Username: "revoked", Role: "user"},
	}
	authz := &stubGroupWatchAuthorizer{
		byUser: map[int64][]Group{
			10: {{ID: gid, Name: "Claude 混合号池"}},
			11: {}, // no longer allowed access to group 42
		},
	}
	mon := &stubGroupWatchMonitorRepo{
		cfg: &ChannelMonitorV2Config{Enabled: true},
	}
	push := &recordingPushNotifier{}

	svc := NewGroupWatchService(repo, mon, authz, nil, nil)
	svc.SetPushNotifier(push)
	svc.syncDispatch = true
	svc.now = func() time.Time { return now }

	setBucket := func(bucketAt time.Time, overall string, cacheRate, errorRate float64) {
		mon.matrix = &ChannelMonitorV2Matrix{
			Items: []ChannelMonitorV2MatrixRow{
				{
					Platform:  PlatformAnthropic,
					GroupID:   &gid,
					GroupName: "Claude 混合号池",
					Buckets: []ChannelMonitorV2TrendPoint{
						{
							BucketStart: bucketAt,
							Health:      ChannelMonitorV2Health{Overall: overall},
							Metrics:     ChannelMonitorV2Metric{CacheRate: cacheRate, ErrorRate: errorRate},
						},
					},
				},
			},
		}
	}

	// 1) First observation (green) seeds silently -> 0 notifications.
	setBucket(now.Add(-5*time.Minute), "healthy", 0.55, 0.05)
	svc.EvaluateMonitorTransitions(ctx, now)
	require.Empty(t, repo.notifications)
	require.Equal(t, GroupWatchStatusGreen, repo.states[gid].Status)
	require.False(t, repo.states[gid].BlackNotified)

	// 2) Dip to yellow (75% avail, 50% cache) then back to green -> 0 notifications (no spam!).
	now = now.Add(5 * time.Minute)
	setBucket(now.Add(-5*time.Minute), "warning", 0.50, 0.25)
	svc.EvaluateMonitorTransitions(ctx, now)
	now = now.Add(5 * time.Minute)
	setBucket(now.Add(-5*time.Minute), "healthy", 0.52, 0.10)
	svc.EvaluateMonitorTransitions(ctx, now)
	require.Empty(t, repo.notifications)

	// 3) Cache drops to 20% -> BLACK! Only authorized user 10 gets notified (not revoked user 11).
	now = now.Add(5 * time.Minute)
	blackBucket := now.Add(-5 * time.Minute)
	setBucket(blackBucket, "critical", 0.20, 0.05)
	svc.EvaluateMonitorTransitions(ctx, now)
	require.Len(t, repo.notifications, 1)
	require.Equal(t, int64(10), repo.notifications[0].UserID)
	require.Equal(t, GroupWatchEventStatusBlack, repo.notifications[0].EventType)
	require.True(t, repo.states[gid].BlackNotified)
	require.Len(t, push.pushes, 1)

	// 4) Same black state next tick -> deduped, no repeat alert.
	now = now.Add(5 * time.Minute)
	setBucket(now.Add(-5*time.Minute), "critical", 0.18, 0.10)
	svc.EvaluateMonitorTransitions(ctx, now)
	require.Len(t, repo.notifications, 1)

	// 5) Insufficient samples ("unknown") -> must NOT clear BlackNotified or send recovery.
	now = now.Add(5 * time.Minute)
	setBucket(now.Add(-5*time.Minute), "unknown", 0.90, 0.0)
	svc.EvaluateMonitorTransitions(ctx, now)
	require.Len(t, repo.notifications, 1)
	require.True(t, repo.states[gid].BlackNotified)

	// 6) Recovers through yellow ("other") -> still keeps BlackNotified=true, waits for green.
	now = now.Add(5 * time.Minute)
	setBucket(now.Add(-5*time.Minute), "warning", 0.40, 0.25)
	svc.EvaluateMonitorTransitions(ctx, now)
	require.Len(t, repo.notifications, 1)
	require.Equal(t, GroupWatchStatusOther, repo.states[gid].Status)
	require.True(t, repo.states[gid].BlackNotified)

	// 7) Recovers to GREEN (82% avail, 36% cache) -> emits status_green and clears BlackNotified!
	now = now.Add(5 * time.Minute)
	setBucket(now.Add(-5*time.Minute), "healthy", 0.36, 0.18)
	svc.EvaluateMonitorTransitions(ctx, now)
	require.Len(t, repo.notifications, 2)
	require.Equal(t, GroupWatchEventStatusGreen, repo.notifications[1].EventType)
	require.False(t, repo.states[gid].BlackNotified)
	require.Len(t, push.pushes, 2)
}

func TestGroupWatchSubscribe_AuthorizationAndBlackSeed(t *testing.T) {
	ctx := context.Background()
	gid := int64(77)
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	repo := newMemoryGroupWatchRepo()
	authz := &stubGroupWatchAuthorizer{
		byUser: map[int64][]Group{
			10: {{ID: gid, Name: "Claude Pro"}},
			12: {},
		},
	}
	mon := &stubGroupWatchMonitorRepo{
		cfg: &ChannelMonitorV2Config{Enabled: true},
		matrix: &ChannelMonitorV2Matrix{
			Items: []ChannelMonitorV2MatrixRow{
				{
					Platform:  PlatformAnthropic,
					GroupID:   &gid,
					GroupName: "Claude Pro",
					Buckets: []ChannelMonitorV2TrendPoint{
						{
							BucketStart: now.Add(-5 * time.Minute),
							Health:      ChannelMonitorV2Health{Overall: "critical"},
							Metrics:     ChannelMonitorV2Metric{CacheRate: 0.12, ErrorRate: 0.05},
						},
					},
				},
			},
		},
	}

	svc := NewGroupWatchService(repo, mon, authz, nil, nil)
	svc.syncDispatch = true
	svc.now = func() time.Time { return now }

	// Unauthorized regular user is rejected.
	err := svc.Subscribe(ctx, 12, gid, false)
	require.ErrorIs(t, err, ErrGroupWatchGroupNotAvailable)

	// Authorized user succeeds and seeds currently-black state with BlackNotified=true.
	require.NoError(t, svc.Subscribe(ctx, 10, gid, false))
	require.Equal(t, GroupWatchStatusBlack, repo.states[gid].Status)
	require.True(t, repo.states[gid].BlackNotified)

	// Unsubscribe removes subscription.
	require.NoError(t, svc.Unsubscribe(ctx, 10, gid))
	ids, err := svc.ListSubscribedGroupIDs(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, ids)
}

func TestGroupWatchPriceChanged_NotifiesAuthorizedSubscriberAndIgnoresPushError(t *testing.T) {
	ctx := context.Background()
	gid := int64(55)
	repo := newMemoryGroupWatchRepo()
	repo.subscribers[gid] = []GroupWatchSubscriber{
		{UserID: 20, Email: "bob@example.com", Username: "bob", Role: "user"},
	}
	authz := &stubGroupWatchAuthorizer{
		byUser: map[int64][]Group{20: {{ID: gid, Name: "OpenAI Pool"}}},
	}
	push := &recordingPushNotifier{err: errors.New("bark offline")}
	svc := NewGroupWatchService(repo, nil, authz, nil, nil)
	svc.SetPushNotifier(push)
	svc.syncDispatch = true

	before := SnapshotGroupPricing(&Group{ID: gid, Name: "OpenAI Pool", Platform: PlatformOpenAI, RateMultiplier: 1.0})
	after := SnapshotGroupPricing(&Group{ID: gid, Name: "OpenAI Pool", Platform: PlatformOpenAI, RateMultiplier: 0.85})
	svc.NotifyGroupPriceChanged(ctx, before, after)

	require.Len(t, repo.notifications, 1)
	require.Equal(t, GroupWatchEventPriceChanged, repo.notifications[0].EventType)
	require.Contains(t, repo.notifications[0].Content, "1.00x → 0.85x")
}

func TestCollectGroupWatchEmailRecipients_IncludesRegisteredAndExtraEmails(t *testing.T) {
	sub := GroupWatchSubscriber{
		UserID:              42,
		Email:               "Registered@Example.com",
		PrimaryEmailBlocked: true, // low-balance primary opt-out must NOT suppress explicit group watch
		ExtraNotifyEmails: []NotifyEmailEntry{
			{Email: "registered@example.com", Verified: true, Disabled: false}, // duplicate case-insensitive
			{Email: "ops-alert@example.com", Verified: true, Disabled: false},  // included
			{Email: "pending@example.com", Verified: false, Disabled: false},   // unverified -> skipped
			{Email: "muted@example.com", Verified: true, Disabled: true},       // disabled -> skipped
		},
	}
	got := collectGroupWatchEmailRecipients(sub)
	require.Equal(t, []string{"Registered@Example.com", "ops-alert@example.com"}, got)
}
