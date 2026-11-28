//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

// anchoredSubRepoStub simulates persistent subscription storage for full
// end-to-end subscription lifecycle regression tests (create, extend, activate,
// automatic window reset, manual admin reset, negative-day extend, expiry).
type anchoredSubRepoStub struct {
	userSubRepoNoop

	mu     sync.Mutex
	nextID int64
	byID   map[int64]*UserSubscription
	group  *Group
}

func newAnchoredSubRepoStub(group *Group) *anchoredSubRepoStub {
	return &anchoredSubRepoStub{
		nextID: 1,
		byID:   make(map[int64]*UserSubscription),
		group:  group,
	}
}

func (r *anchoredSubRepoStub) cloneLocked(sub *UserSubscription) *UserSubscription {
	cp := *sub
	if sub.DailyWindowStart != nil {
		t := *sub.DailyWindowStart
		cp.DailyWindowStart = &t
	}
	if sub.WeeklyWindowStart != nil {
		t := *sub.WeeklyWindowStart
		cp.WeeklyWindowStart = &t
	}
	if sub.MonthlyWindowStart != nil {
		t := *sub.MonthlyWindowStart
		cp.MonthlyWindowStart = &t
	}
	cp.Group = r.group
	return &cp
}

func (r *anchoredSubRepoStub) Create(_ context.Context, sub *UserSubscription) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *sub
	cp.ID = r.nextID
	r.nextID++
	cp.Group = r.group
	r.byID[cp.ID] = &cp
	sub.ID = cp.ID
	return nil
}

func (r *anchoredSubRepoStub) GetByID(_ context.Context, id int64) (*UserSubscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub, ok := r.byID[id]
	if !ok {
		return nil, ErrSubscriptionNotFound
	}
	return r.cloneLocked(sub), nil
}

func (r *anchoredSubRepoStub) GetByIDForUpdate(_ context.Context, id int64) (*UserSubscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub, ok := r.byID[id]
	if !ok {
		return nil, ErrSubscriptionNotFound
	}
	// Mirror production GetByIDForUpdate: does NOT eager-load Group.
	cp := r.cloneLocked(sub)
	cp.Group = nil
	return cp, nil
}

func (r *anchoredSubRepoStub) GetByUserIDAndGroupID(_ context.Context, userID, groupID int64) (*UserSubscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sub := range r.byID {
		if sub.UserID == userID && sub.GroupID == groupID {
			return r.cloneLocked(sub), nil
		}
	}
	return nil, ErrSubscriptionNotFound
}

func (r *anchoredSubRepoStub) Update(_ context.Context, sub *UserSubscription) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *sub
	cp.Group = r.group
	r.byID[sub.ID] = &cp
	return nil
}

func (r *anchoredSubRepoStub) ExtendExpiry(_ context.Context, subscriptionID int64, newExpiresAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub, ok := r.byID[subscriptionID]
	if !ok {
		return ErrSubscriptionNotFound
	}
	sub.ExpiresAt = newExpiresAt
	return nil
}

func (r *anchoredSubRepoStub) UpdateStatus(_ context.Context, subscriptionID int64, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub, ok := r.byID[subscriptionID]
	if !ok {
		return ErrSubscriptionNotFound
	}
	sub.Status = status
	return nil
}

func (r *anchoredSubRepoStub) UpdateNotes(_ context.Context, subscriptionID int64, notes string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub, ok := r.byID[subscriptionID]
	if !ok {
		return ErrSubscriptionNotFound
	}
	sub.Notes = notes
	return nil
}

func (r *anchoredSubRepoStub) ActivateWindows(_ context.Context, id int64, dailyStart, periodicStart time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub, ok := r.byID[id]
	if !ok {
		return ErrSubscriptionNotFound
	}
	if sub.DailyWindowStart == nil && sub.WeeklyWindowStart == nil && sub.MonthlyWindowStart == nil {
		d, w, m := dailyStart, periodicStart, periodicStart
		sub.DailyWindowStart = &d
		sub.WeeklyWindowStart = &w
		sub.MonthlyWindowStart = &m
	}
	return nil
}

func (r *anchoredSubRepoStub) ResetDailyUsage(_ context.Context, id int64, expectedWindowStart *time.Time, newWindowStart time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub, ok := r.byID[id]
	if !ok {
		return ErrSubscriptionNotFound
	}
	if expectedWindowStart == nil && sub.DailyWindowStart != nil {
		return nil
	}
	if expectedWindowStart != nil && (sub.DailyWindowStart == nil || !sub.DailyWindowStart.Equal(*expectedWindowStart)) {
		return nil
	}
	sub.DailyUsageUSD = 0
	w := newWindowStart
	sub.DailyWindowStart = &w
	return nil
}

func (r *anchoredSubRepoStub) ResetWeeklyUsage(_ context.Context, id int64, expectedWindowStart *time.Time, newWindowStart time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub, ok := r.byID[id]
	if !ok {
		return ErrSubscriptionNotFound
	}
	if expectedWindowStart == nil && sub.WeeklyWindowStart != nil {
		return nil
	}
	if expectedWindowStart != nil && (sub.WeeklyWindowStart == nil || !sub.WeeklyWindowStart.Equal(*expectedWindowStart)) {
		return nil
	}
	sub.WeeklyUsageUSD = 0
	w := newWindowStart
	sub.WeeklyWindowStart = &w
	return nil
}

func (r *anchoredSubRepoStub) ResetMonthlyUsage(_ context.Context, id int64, expectedWindowStart *time.Time, newWindowStart time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub, ok := r.byID[id]
	if !ok {
		return ErrSubscriptionNotFound
	}
	if expectedWindowStart == nil && sub.MonthlyWindowStart != nil {
		return nil
	}
	if expectedWindowStart != nil && (sub.MonthlyWindowStart == nil || !sub.MonthlyWindowStart.Equal(*expectedWindowStart)) {
		return nil
	}
	sub.MonthlyUsageUSD = 0
	w := newWindowStart
	sub.MonthlyWindowStart = &w
	return nil
}

func (r *anchoredSubRepoStub) ResetUsageWindows(_ context.Context, id int64, resetDaily, resetWeekly, resetMonthly bool, dailyStart, periodicStart time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub, ok := r.byID[id]
	if !ok {
		return ErrSubscriptionNotFound
	}
	if resetDaily {
		sub.DailyUsageUSD = 0
		d := dailyStart
		sub.DailyWindowStart = &d
	}
	if resetWeekly {
		sub.WeeklyUsageUSD = 0
		w := periodicStart
		sub.WeeklyWindowStart = &w
	}
	if resetMonthly {
		sub.MonthlyUsageUSD = 0
		m := periodicStart
		sub.MonthlyWindowStart = &m
	}
	return nil
}

func (r *anchoredSubRepoStub) IncrementUsage(_ context.Context, id int64, costUSD float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	sub, ok := r.byID[id]
	if !ok {
		return ErrSubscriptionNotFound
	}
	sub.DailyUsageUSD += costUSD
	sub.WeeklyUsageUSD += costUSD
	sub.MonthlyUsageUSD += costUSD
	return nil
}

func newAnchoredTestGroup(dailyLimit float64, mode string) *Group {
	limit := dailyLimit
	return &Group{
		ID:               10,
		Name:             "Anchored-Test",
		Platform:         PlatformAnthropic,
		Status:           StatusActive,
		SubscriptionType: SubscriptionTypeSubscription,
		DailyResetMode:   mode,
		DailyLimitUSD:    &limit,
	}
}

// simulateRequestAt mirrors middleware hot path: ValidateAndCheckLimits ->
// EnsureWindowMaintenance (if needed) -> re-validate -> RecordUsage.
func simulateRequestAt(t *testing.T, svc *SubscriptionService, repo *anchoredSubRepoStub, subID int64, group *Group, at time.Time, cost float64) (*UserSubscription, error) {
	t.Helper()
	svc.now = func() time.Time { return at }
	sub, err := repo.GetByID(context.Background(), subID)
	require.NoError(t, err)

	needsMaintenance, err := svc.ValidateAndCheckLimits(sub, group)
	if needsMaintenance {
		sub, err = svc.EnsureWindowMaintenance(context.Background(), sub)
		require.NoError(t, err)
		_, err = svc.ValidateAndCheckLimits(sub, group)
	}
	if err != nil {
		return sub, err
	}
	require.NoError(t, svc.RecordUsage(context.Background(), subID, cost))
	return repo.GetByID(context.Background(), subID)
}

func TestAnchored24h_OneDaySubscriptionGetsExactlyOneWindow(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	day1Noon := time.Date(2026, 8, 6, 12, 0, 0, 0, loc)

	group := newAnchoredTestGroup(100, DailyResetModeAnchored24h)
	repo := newAnchoredSubRepoStub(group)
	svc := NewSubscriptionService(&subscriptionGroupRepoStub{group: group}, repo, nil, nil, nil)
	svc.now = func() time.Time { return day1Noon }

	sub, reused, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:       1,
		GroupID:      group.ID,
		ValidityDays: 1,
	})
	require.NoError(t, err)
	require.False(t, reused)

	// Day 1 12:05: spend over $100 quota in window 1 (ValidateAndCheckLimits rejects when usage > limit).
	sub, err = simulateRequestAt(t, svc, repo, sub.ID, group, day1Noon.Add(5*time.Minute), 100.01)
	require.NoError(t, err)
	require.NotNil(t, sub.DailyWindowStart)
	require.True(t, day1Noon.Equal(*sub.DailyWindowStart))
	require.Nil(t, sub.NextDailyResetAt(), "1-day anchored_24h subscription has no next daily reset before expiry")
	resetTime := sub.DailyResetTime()
	require.NotNil(t, resetTime)
	require.True(t, sub.ExpiresAt.Equal(*resetTime))

	// Day 2 00:01: crossing midnight MUST NOT reset quota.
	_, err = simulateRequestAt(t, svc, repo, sub.ID, group, time.Date(2026, 8, 7, 0, 1, 0, 0, loc), 1)
	require.ErrorIs(t, err, ErrDailyLimitExceeded)

	// Day 2 11:59: still exhausted.
	_, err = simulateRequestAt(t, svc, repo, sub.ID, group, day1Noon.Add(24*time.Hour-time.Minute), 1)
	require.ErrorIs(t, err, ErrDailyLimitExceeded)

	// Day 2 12:00 (exact boundary == expires_at): expires directly, no 2nd window.
	_, err = simulateRequestAt(t, svc, repo, sub.ID, group, day1Noon.Add(24*time.Hour), 1)
	require.ErrorIs(t, err, ErrSubscriptionExpired)
}

func TestAnchored24h_OneDayExtendedToTwoDaysGetsExactlyTwoWindows(t *testing.T) {
	// Exact Issue #726 reproduction scenario:
	// Day 1 12:00 activate 1-day -> Day 1 18:00 extend +1 day (expires Day 3 12:00).
	// Must grant exactly 2 daily quota windows (200 USD), NOT 3 (300 USD).
	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	day1Noon := time.Date(2026, 8, 6, 12, 0, 0, 0, loc)
	day1Evening := time.Date(2026, 8, 6, 18, 0, 0, 0, loc)
	day2Midnight := time.Date(2026, 8, 7, 0, 1, 0, 0, loc)
	day2Noon := time.Date(2026, 8, 7, 12, 0, 0, 0, loc)
	day3Midnight := time.Date(2026, 8, 8, 0, 1, 0, 0, loc)
	day3Noon := time.Date(2026, 8, 8, 12, 0, 0, 0, loc)

	group := newAnchoredTestGroup(100, DailyResetModeAnchored24h)
	repo := newAnchoredSubRepoStub(group)
	svc := NewSubscriptionService(&subscriptionGroupRepoStub{group: group}, repo, nil, nil, nil)

	// Step 1: Day 1 12:00 activate 1-day subscription and exhaust window 1.
	svc.now = func() time.Time { return day1Noon }
	sub, _, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
		UserID: 1, GroupID: group.ID, ValidityDays: 1,
	})
	require.NoError(t, err)
	sub, err = simulateRequestAt(t, svc, repo, sub.ID, group, day1Noon.Add(10*time.Minute), 100.01)
	require.NoError(t, err)

	// Step 2: Day 1 18:00 extend by 1 day -> StartsAt stays Day 1 12:00, ExpiresAt becomes Day 3 12:00.
	svc.now = func() time.Time { return day1Evening }
	sub, reused, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
		UserID: 1, GroupID: group.ID, ValidityDays: 1,
	})
	require.NoError(t, err)
	require.True(t, reused)
	require.True(t, day1Noon.Equal(sub.StartsAt))
	require.True(t, day3Noon.Equal(sub.ExpiresAt))
	require.False(t, sub.HasOneTimeDailyQuota())

	// Next reset in window 1 is Day 2 12:00.
	nextReset := sub.NextDailyResetAt()
	require.NotNil(t, nextReset)
	require.True(t, day2Noon.Equal(*nextReset))

	// Step 3: Day 2 00:01 stays in window 1 -> still blocked by $100 daily limit!
	_, err = simulateRequestAt(t, svc, repo, sub.ID, group, day2Midnight, 10)
	require.ErrorIs(t, err, ErrDailyLimitExceeded, "Day 2 00:01 must stay in window 1")

	// Also verify list normalization at Day 2 00:01 does NOT clear usage.
	subCopy := *sub
	subs := []UserSubscription{subCopy}
	normalizeExpiredWindowsAt(subs, day2Midnight)
	require.Equal(t, 100.01, subs[0].DailyUsageUSD)
	require.NotNil(t, subs[0].DailyWindowStart)

	// Step 4: Day 2 12:00 switches to window 2 -> fresh daily quota available.
	sub, err = simulateRequestAt(t, svc, repo, sub.ID, group, day2Noon, 100.01)
	require.NoError(t, err)
	require.NotNil(t, sub.DailyWindowStart)
	require.True(t, day2Noon.Equal(*sub.DailyWindowStart))
	require.Equal(t, 100.01, sub.DailyUsageUSD)

	// Window 2's next 24h boundary == Day 3 12:00 == ExpiresAt -> NextDailyResetAt MUST be nil!
	require.Nil(t, sub.NextDailyResetAt(), "final window whose 24h boundary == expires_at must not display another reset")
	progress := svc.calculateProgress(sub, group)
	require.NotNil(t, progress.Daily)
	require.True(t, day3Noon.Equal(progress.Daily.ResetsAt))

	// Step 5: Day 3 00:01 stays in window 2 -> still blocked!
	_, err = simulateRequestAt(t, svc, repo, sub.ID, group, day3Midnight, 10)
	require.ErrorIs(t, err, ErrDailyLimitExceeded, "Day 3 00:01 must stay in window 2")

	// Step 6: Day 3 12:00 expires directly -> NO 3rd window!
	_, err = simulateRequestAt(t, svc, repo, sub.ID, group, day3Noon, 10)
	require.ErrorIs(t, err, ErrSubscriptionExpired, "Day 3 12:00 must expire directly")
	require.False(t, sub.NeedsDailyResetAt(day3Noon))
}

func TestAnchored24h_ThirtyDaySubscriptionSpans31CalendarDaysWithExactly30Windows(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	startsAt := time.Date(2026, 8, 1, 15, 0, 0, 0, loc)
	expiresAt := startsAt.AddDate(0, 0, 30) // 2026-08-31 15:00

	group := newAnchoredTestGroup(50, DailyResetModeAnchored24h)
	repo := newAnchoredSubRepoStub(group)
	svc := NewSubscriptionService(&subscriptionGroupRepoStub{group: group}, repo, nil, nil, nil)
	svc.now = func() time.Time { return startsAt }

	sub, _, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
		UserID: 7, GroupID: group.ID, ValidityDays: 30,
	})
	require.NoError(t, err)
	require.True(t, expiresAt.Equal(sub.ExpiresAt))

	windowsGranted := 0
	for day := 0; day < 30; day++ {
		boundary := startsAt.Add(time.Duration(day) * 24 * time.Hour)
		// Exhaust the $50 quota at boundary + 1m.
		updated, reqErr := simulateRequestAt(t, svc, repo, sub.ID, group, boundary.Add(time.Minute), 50.01)
		require.NoError(t, reqErr, "window %d should succeed", day)
		require.NotNil(t, updated.DailyWindowStart)
		require.True(t, boundary.Equal(*updated.DailyWindowStart), "window %d start mismatch", day)
		windowsGranted++

		if day < 29 {
			expectedNext := boundary.Add(24 * time.Hour)
			gotNext := updated.NextDailyResetAt()
			require.NotNil(t, gotNext, "window %d should have next reset", day)
			require.True(t, expectedNext.Equal(*gotNext))
		} else {
			require.Nil(t, updated.NextDailyResetAt(), "30th window must have nil NextDailyResetAt")
		}

		// Verify crossing midnight inside this window (boundary + 9h01m = next calendar day 00:01) does NOT reset.
		midnightAfter := boundary.Add(9*time.Hour + time.Minute)
		_, midErr := simulateRequestAt(t, svc, repo, sub.ID, group, midnightAfter, 1)
		require.ErrorIs(t, midErr, ErrDailyLimitExceeded, "midnight inside window %d must not grant extra quota", day)
	}

	require.Equal(t, 30, windowsGranted, "30-day anchored_24h subscription must grant exactly 30 windows across 31 calendar days")

	// Exact expiry boundary (Day 31 15:00): expires directly.
	_, err = simulateRequestAt(t, svc, repo, sub.ID, group, expiresAt, 1)
	require.ErrorIs(t, err, ErrSubscriptionExpired)
}

func TestAnchored24h_ManualAdminResetPreservesCadenceAndSupportsNegativeDayExtend(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	day1Noon := time.Date(2026, 8, 6, 12, 0, 0, 0, loc)
	day1Afternoon := time.Date(2026, 8, 6, 16, 0, 0, 0, loc)
	day2Midnight := time.Date(2026, 8, 7, 0, 1, 0, 0, loc)
	day2Noon := time.Date(2026, 8, 7, 12, 0, 0, 0, loc)
	day2Afternoon := time.Date(2026, 8, 7, 16, 0, 0, 0, loc)
	day3Noon := time.Date(2026, 8, 8, 12, 0, 0, 0, loc)

	group := newAnchoredTestGroup(100, DailyResetModeAnchored24h)
	repo := newAnchoredSubRepoStub(group)
	svc := NewSubscriptionService(&subscriptionGroupRepoStub{group: group}, repo, nil, nil, nil)

	// 3-day subscription starting Day 1 12:00 (expires Day 4 12:00).
	svc.now = func() time.Time { return day1Noon }
	sub, _, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
		UserID: 9, GroupID: group.ID, ValidityDays: 3,
	})
	require.NoError(t, err)

	// Exhaust window 0.
	_, err = simulateRequestAt(t, svc, repo, sub.ID, group, day1Noon.Add(30*time.Minute), 100.01)
	require.NoError(t, err)

	// Day 1 16:00: Admin shortens validity by -1 day (Day 4 12:00 -> Day 3 12:00)
	// and manually resets daily quota.
	svc.now = func() time.Time { return day1Afternoon }
	shortened, err := svc.ExtendSubscription(context.Background(), sub.ID, -1)
	require.NoError(t, err)
	require.True(t, day3Noon.Equal(shortened.ExpiresAt))

	resetSub, err := svc.AdminResetQuota(context.Background(), sub.ID, true, false, false)
	require.NoError(t, err)
	require.Zero(t, resetSub.DailyUsageUSD)
	require.NotNil(t, resetSub.DailyWindowStart)
	require.True(t, day1Noon.Equal(*resetSub.DailyWindowStart), "manual reset must keep starts_at 24h boundary, not drift to 16:00")
	nextReset := resetSub.NextDailyResetAt()
	require.NotNil(t, nextReset)
	require.True(t, day2Noon.Equal(*nextReset), "next automatic reset must remain Day 2 12:00")

	// User exhausts the early-reset quota at Day 1 16:10.
	_, err = simulateRequestAt(t, svc, repo, sub.ID, group, day1Afternoon.Add(10*time.Minute), 100.01)
	require.NoError(t, err)

	// Day 2 00:01: still in window 0 (blocked).
	_, err = simulateRequestAt(t, svc, repo, sub.ID, group, day2Midnight, 1)
	require.ErrorIs(t, err, ErrDailyLimitExceeded)

	// Day 2 12:00: advances cleanly on starts_at cadence to window 1 (NOT waiting until 16:00!).
	sub, err = simulateRequestAt(t, svc, repo, sub.ID, group, day2Noon, 100.01)
	require.NoError(t, err)
	require.True(t, day2Noon.Equal(*sub.DailyWindowStart))
	require.Nil(t, sub.NextDailyResetAt(), "shortened expiry Day 3 12:00 matches next boundary, so no further reset")

	// Day 2 16:00: trying to shorten by -1 day again would move expiry to Day 2 12:00 <= now -> rejected!
	svc.now = func() time.Time { return day2Afternoon }
	_, err = svc.ExtendSubscription(context.Background(), sub.ID, -1)
	require.ErrorIs(t, err, ErrAdjustWouldExpire)

	// Day 3 12:00: expires directly (total granted: 1 original + 1 manual trade-in + 1 remaining = 3).
	_, err = simulateRequestAt(t, svc, repo, sub.ID, group, day3Noon, 1)
	require.ErrorIs(t, err, ErrSubscriptionExpired)
}

func TestAnchored24h_CalendarModeRemainsDefaultUnchanged(t *testing.T) {
	loc := timezone.Location()
	day1Noon := timezone.StartOfDay(time.Date(2026, 8, 6, 12, 0, 0, 0, loc)).Add(12 * time.Hour)
	day2Midnight := timezone.StartOfDay(day1Noon).AddDate(0, 0, 1).Add(time.Minute)

	for _, mode := range []string{"", DailyResetModeCalendar} {
		t.Run("mode="+mode, func(t *testing.T) {
			group := newAnchoredTestGroup(100, mode)
			repo := newAnchoredSubRepoStub(group)
			svc := NewSubscriptionService(&subscriptionGroupRepoStub{group: group}, repo, nil, nil, nil)
			svc.now = func() time.Time { return day1Noon }

			sub, _, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
				UserID: 3, GroupID: group.ID, ValidityDays: 2,
			})
			require.NoError(t, err)

			// Spend $100 on Day 1.
			sub, err = simulateRequestAt(t, svc, repo, sub.ID, group, day1Noon.Add(time.Minute), 100)
			require.NoError(t, err)
			require.True(t, timezone.StartOfDay(day1Noon).Equal(*sub.DailyWindowStart))

			// Crossing midnight on Day 2 MUST still reset in calendar mode.
			sub, err = simulateRequestAt(t, svc, repo, sub.ID, group, day2Midnight, 10)
			require.NoError(t, err)
			require.Equal(t, 10.0, sub.DailyUsageUSD)
			require.True(t, timezone.StartOfDay(day2Midnight).Equal(*sub.DailyWindowStart))
		})
	}
}

func TestAnchored24h_ExpiredRenewalAndLateActivationAndLegacyMidnightAnchor(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	day1Noon := time.Date(2026, 8, 6, 12, 0, 0, 0, loc)
	day2Afternoon := time.Date(2026, 8, 7, 15, 0, 0, 0, loc)
	day2Noon := time.Date(2026, 8, 7, 12, 0, 0, 0, loc)
	day3Noon := time.Date(2026, 8, 8, 12, 0, 0, 0, loc)
	day5Afternoon := time.Date(2026, 8, 10, 14, 30, 0, 0, loc)

	group := newAnchoredTestGroup(100, DailyResetModeAnchored24h)
	repo := newAnchoredSubRepoStub(group)
	svc := NewSubscriptionService(&subscriptionGroupRepoStub{group: group}, repo, nil, nil, nil)

	// 1. Late first-request activation on Day 2 15:00 activates straight into B_1 (Day 2 12:00).
	svc.now = func() time.Time { return day1Noon }
	sub, _, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
		UserID: 11, GroupID: group.ID, ValidityDays: 2,
	})
	require.NoError(t, err)
	sub, err = simulateRequestAt(t, svc, repo, sub.ID, group, day2Afternoon, 40)
	require.NoError(t, err)
	require.NotNil(t, sub.DailyWindowStart)
	require.True(t, day2Noon.Equal(*sub.DailyWindowStart), "late activation must align to B_1 = starts_at + 24h")
	require.Nil(t, sub.NextDailyResetAt(), "B_2 == expires_at has no next reset")

	// 2. Renewal after expiry resets anchor to new StartsAt (not midnight).
	svc.now = func() time.Time { return day5Afternoon }
	renewed, reused, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
		UserID: 11, GroupID: group.ID, ValidityDays: 2,
	})
	require.NoError(t, err)
	require.True(t, reused)
	require.True(t, day5Afternoon.Equal(renewed.StartsAt))
	require.NotNil(t, renewed.DailyWindowStart)
	require.True(t, day5Afternoon.Equal(*renewed.DailyWindowStart), "expired renewal in anchored_24h mode must anchor daily_window_start at new starts_at")

	// 3. Switching an existing subscription from calendar (legacy midnight anchor < starts_at) to anchored_24h
	// must NOT grant a free reset inside window 0, and must self-heal to starts_at + 24h on boundary 1.
	legacyMidnight := timezone.StartOfDay(day1Noon)
	legacySub := &UserSubscription{
		ID:               99,
		UserID:           12,
		GroupID:          group.ID,
		Status:           SubscriptionStatusActive,
		StartsAt:         day1Noon,
		ExpiresAt:        day3Noon,
		DailyWindowStart: &legacyMidnight,
		DailyUsageUSD:    100,
		Group:            group,
	}
	require.False(t, legacySub.NeedsDailyResetAt(day1Noon.Add(3*time.Hour)), "legacy midnight anchor must not trigger free reset in window 0")
	require.False(t, legacySub.NeedsDailyResetAt(time.Date(2026, 8, 7, 0, 1, 0, 0, loc)), "legacy midnight anchor must not reset at Day 2 00:01 under anchored_24h")
	nextReset := legacySub.NextDailyResetAt()
	require.NotNil(t, nextReset)
	require.True(t, day2Noon.Equal(*nextReset))
	newStart, ok := legacySub.automaticDailyWindowStartAt(day2Noon)
	require.True(t, ok)
	require.True(t, day2Noon.Equal(newStart))
}

func TestAnchored24h_ConcurrentValidationAndMaintenanceRaceSafe(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	day1Noon := time.Date(2026, 8, 6, 12, 0, 0, 0, loc)
	day2Noon := day1Noon.Add(24 * time.Hour)

	group := newAnchoredTestGroup(100, DailyResetModeAnchored24h)
	repo := newAnchoredSubRepoStub(group)
	svc := NewSubscriptionService(&subscriptionGroupRepoStub{group: group}, repo, nil, nil, nil)
	svc.now = func() time.Time { return day1Noon }

	sub, _, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
		UserID: 21, GroupID: group.ID, ValidityDays: 2,
	})
	require.NoError(t, err)
	_, err = simulateRequestAt(t, svc, repo, sub.ID, group, day1Noon.Add(time.Minute), 100)
	require.NoError(t, err)

	svc.now = func() time.Time { return day2Noon }
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap, getErr := repo.GetByID(context.Background(), sub.ID)
			if getErr != nil {
				errs <- getErr
				return
			}
			needs, valErr := svc.ValidateAndCheckLimits(snap, group)
			if valErr != nil && !errors.Is(valErr, ErrDailyLimitExceeded) {
				errs <- valErr
				return
			}
			if needs {
				refreshed, mErr := svc.EnsureWindowMaintenance(context.Background(), snap)
				if mErr != nil {
					errs <- mErr
					return
				}
				if _, reErr := svc.ValidateAndCheckLimits(refreshed, group); reErr != nil {
					errs <- reErr
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	finalSub, err := repo.GetByID(context.Background(), sub.ID)
	require.NoError(t, err)
	require.NotNil(t, finalSub.DailyWindowStart)
	require.True(t, day2Noon.Equal(*finalSub.DailyWindowStart))
	require.Zero(t, finalSub.DailyUsageUSD)
}

func TestGroupDailyResetMode_ValidationNormalizationDuplicateAndAuthSnapshot(t *testing.T) {
	require.NoError(t, ValidateDailyResetMode(""))
	require.NoError(t, ValidateDailyResetMode(DailyResetModeCalendar))
	require.NoError(t, ValidateDailyResetMode(DailyResetModeAnchored24h))
	require.Error(t, ValidateDailyResetMode("invalid_mode"))

	require.Equal(t, DailyResetModeCalendar, NormalizeDailyResetMode(""))
	require.Equal(t, DailyResetModeCalendar, NormalizeDailyResetMode("unknown"))
	require.Equal(t, DailyResetModeAnchored24h, NormalizeDailyResetMode(DailyResetModeAnchored24h))

	// Duplicate preserves anchored_24h on subscription groups and forces calendar on standard groups.
	subDup := cloneGroupForDuplicate(&Group{
		Name:             "Sub",
		SubscriptionType: SubscriptionTypeSubscription,
		DailyResetMode:   DailyResetModeAnchored24h,
	}, "op-1")
	require.Equal(t, DailyResetModeAnchored24h, subDup.DailyResetMode)

	stdDup := cloneGroupForDuplicate(&Group{
		Name:             "Std",
		SubscriptionType: SubscriptionTypeStandard,
		DailyResetMode:   DailyResetModeAnchored24h,
	}, "op-2")
	require.Equal(t, DailyResetModeCalendar, stdDup.DailyResetMode)

	// Auth cache snapshot round-trip preserves DailyResetMode and normalizes legacy empty snapshots to calendar.
	apiKeySvc := &APIKeyService{}
	snap := apiKeySvc.snapshotFromAPIKey(context.Background(), &APIKey{
		ID:     1,
		UserID: 2,
		User:   &User{ID: 2},
		Group: &Group{
			ID:               10,
			SubscriptionType: SubscriptionTypeSubscription,
			DailyResetMode:   DailyResetModeAnchored24h,
		},
	})
	require.Equal(t, DailyResetModeAnchored24h, snap.Group.DailyResetMode)
	restored := apiKeySvc.snapshotToAPIKey("test-key", snap)
	require.Equal(t, DailyResetModeAnchored24h, restored.Group.DailyResetMode)

	snap.Group.DailyResetMode = ""
	legacyRestored := apiKeySvc.snapshotToAPIKey("test-key", snap)
	require.Equal(t, DailyResetModeCalendar, legacyRestored.Group.DailyResetMode)

	// Rolling-upgrade safety: v28 rejects legacy v27 snapshots that omit daily_reset_mode.
	require.Equal(t, 28, APIKeyAuthCacheSchemaVersion)
	legacyV27Entry := &APIKeyAuthCacheEntry{
		Snapshot: &APIKeyAuthSnapshot{
			Version:  27,
			APIKeyID: 1,
			UserID:   2,
			Status:   StatusActive,
			User:     APIKeyAuthUserSnapshot{ID: 2, Status: StatusActive},
			Group: &APIKeyAuthGroupSnapshot{
				ID:               10,
				SubscriptionType: SubscriptionTypeSubscription,
			},
		},
	}
	key, accepted, err := apiKeySvc.applyAuthCacheEntry("test-key", legacyV27Entry)
	require.NoError(t, err)
	require.False(t, accepted, "v28 must reject v27 snapshots lacking daily_reset_mode")
	require.Nil(t, key)
}

func TestAnchored24h_ExpiredSubscriptionWithStaleWindowOmitsNextDailyResetAt(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	aug6Noon := time.Date(2026, 8, 6, 12, 0, 0, 0, loc)
	aug7Noon := aug6Noon.Add(24 * time.Hour)
	aug8Noon := aug6Noon.Add(48 * time.Hour)
	aug9Morning := time.Date(2026, 8, 9, 10, 0, 0, 0, loc)

	group := newAnchoredTestGroup(100, DailyResetModeAnchored24h)
	sub := &UserSubscription{
		ID:               55,
		UserID:           1,
		GroupID:          group.ID,
		Status:           SubscriptionStatusActive, // status not yet swept
		StartsAt:         aug6Noon,
		ExpiresAt:        aug8Noon,
		DailyWindowStart: &aug6Noon, // stale window start from day 1
		DailyUsageUSD:    20,
		Group:            group,
	}

	// While active on Aug 6 18:00, interior boundary Aug 7 12:00 is returned.
	activeNext := sub.NextDailyResetAtAt(aug6Noon.Add(6 * time.Hour))
	require.NotNil(t, activeNext)
	require.True(t, aug7Noon.Equal(*activeNext))

	// At exact expiry (Aug 8 12:00) and after expiry (Aug 9 10:00), NextDailyResetAtAt MUST be nil
	// and DailyResetTimeAt MUST fall back to ExpiresAt.
	for _, evalAt := range []time.Time{aug8Noon, aug9Morning} {
		require.Nil(t, sub.NextDailyResetAtAt(evalAt))
		resetTime := sub.DailyResetTimeAt(evalAt)
		require.NotNil(t, resetTime)
		require.True(t, aug8Noon.Equal(*resetTime))
	}

	// calculateProgress at Aug 9 must also cap ResetsAt to ExpiresAt.
	svc := NewSubscriptionService(groupRepoNoop{}, userSubRepoNoop{}, nil, nil, nil)
	svc.now = func() time.Time { return aug9Morning }
	progress := svc.calculateProgress(sub, group)
	require.NotNil(t, progress.Daily)
	require.True(t, aug8Noon.Equal(progress.Daily.ResetsAt))
	require.Zero(t, progress.Daily.ResetsInSeconds)

	// Explicitly expired status always omits NextDailyResetAt.
	sub.Status = SubscriptionStatusExpired
	require.Nil(t, sub.NextDailyResetAt())
}

func TestAnchored24h_ExtendExpiredSubscriptionResetsAnchorAndGrantsSingleWindow(t *testing.T) {
	loc := time.FixedZone("Asia/Shanghai", 8*3600)
	aug6Noon := time.Date(2026, 8, 6, 12, 0, 0, 0, loc)
	aug10Afternoon := time.Date(2026, 8, 10, 14, 30, 0, 0, loc)
	aug11Noon := time.Date(2026, 8, 11, 12, 0, 0, 0, loc) // old stale StartsAt hour!
	aug11Afternoon := aug10Afternoon.AddDate(0, 0, 1)

	group := newAnchoredTestGroup(100, DailyResetModeAnchored24h)
	repo := newAnchoredSubRepoStub(group)
	svc := NewSubscriptionService(&subscriptionGroupRepoStub{group: group}, repo, nil, nil, nil)

	// 1. Activate 1-day subscription on Aug 6 12:00 (expires Aug 7 12:00) and exhaust quota.
	svc.now = func() time.Time { return aug6Noon }
	sub, _, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
		UserID: 31, GroupID: group.ID, ValidityDays: 1,
	})
	require.NoError(t, err)
	sub, err = simulateRequestAt(t, svc, repo, sub.ID, group, aug6Noon.Add(15*time.Minute), 100.01)
	require.NoError(t, err)

	// 2. Admin extends the now-expired subscription by +1 day on Aug 10 14:30.
	svc.now = func() time.Time { return aug10Afternoon }
	extended, err := svc.ExtendSubscription(context.Background(), sub.ID, 1)
	require.NoError(t, err)
	require.Equal(t, SubscriptionStatusActive, extended.Status)
	require.True(t, aug10Afternoon.Equal(extended.StartsAt), "extending an expired subscription must reset StartsAt to now")
	require.True(t, aug11Afternoon.Equal(extended.ExpiresAt))
	require.NotNil(t, extended.DailyWindowStart)
	require.True(t, aug10Afternoon.Equal(*extended.DailyWindowStart), "daily window must anchor at new StartsAt")
	require.Zero(t, extended.DailyUsageUSD, "stale usage from expired term must be cleared")
	require.True(t, extended.HasOneTimeDailyQuota())
	require.Nil(t, extended.NextDailyResetAtAt(aug10Afternoon))

	// 3. User exhausts the single 1-day quota at Aug 10 14:40.
	_, err = simulateRequestAt(t, svc, repo, sub.ID, group, aug10Afternoon.Add(10*time.Minute), 100.01)
	require.NoError(t, err)

	// 4. Crossing midnight (Aug 11 00:01) AND passing old stale StartsAt hour (Aug 11 12:00) MUST NOT grant a 2nd window!
	_, err = simulateRequestAt(t, svc, repo, sub.ID, group, time.Date(2026, 8, 11, 0, 1, 0, 0, loc), 1)
	require.ErrorIs(t, err, ErrDailyLimitExceeded)
	_, err = simulateRequestAt(t, svc, repo, sub.ID, group, aug11Noon, 1)
	require.ErrorIs(t, err, ErrDailyLimitExceeded, "stale pre-expiry StartsAt hour (12:00) must not grant a second quota window")

	// 5. Expires cleanly at Aug 11 14:30 -> exactly 1 quota window granted.
	_, err = simulateRequestAt(t, svc, repo, sub.ID, group, aug11Afternoon, 1)
	require.ErrorIs(t, err, ErrSubscriptionExpired)

	// 6. Contrast: extending an ACTIVE subscription preserves original StartsAt anchor.
	svc.now = func() time.Time { return aug6Noon }
	activeSub, _, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
		UserID: 32, GroupID: group.ID, ValidityDays: 1,
	})
	require.NoError(t, err)
	svc.now = func() time.Time { return aug6Noon.Add(6 * time.Hour) }
	extendedActive, err := svc.ExtendSubscription(context.Background(), activeSub.ID, 1)
	require.NoError(t, err)
	require.True(t, aug6Noon.Equal(extendedActive.StartsAt), "extending an active subscription must keep existing StartsAt anchor")
	require.True(t, aug6Noon.AddDate(0, 0, 2).Equal(extendedActive.ExpiresAt))
}
