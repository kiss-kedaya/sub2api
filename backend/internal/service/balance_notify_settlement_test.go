//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type settlementPushSpy struct {
	mu    sync.Mutex
	calls []settlementPushCall
}

type settlementPushCall struct {
	userID int64
	body   string
}

func (s *settlementPushSpy) PushToUser(_ context.Context, userID int64, _, body, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, settlementPushCall{userID: userID, body: body})
	return nil
}

func (s *settlementPushSpy) PushToAdmins(context.Context, string, string, string) error { return nil }

func (s *settlementPushSpy) snapshot() []settlementPushCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]settlementPushCall(nil), s.calls...)
}

func (s *settlementPushSpy) forUser(userID int64) int {
	n := 0
	for _, call := range s.snapshot() {
		if call.userID == userID {
			n++
		}
	}
	return n
}

type settlementUserReaderStub struct {
	mu        sync.Mutex
	users     map[int64]*User
	err       error
	failFirst int // the first N reads fail with a transient error
	calls     int
}

func (r *settlementUserReaderStub) GetByID(_ context.Context, id int64) (*User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.calls <= r.failFirst {
		return nil, errors.New("transient read error")
	}
	if r.err != nil {
		return nil, r.err
	}
	user, ok := r.users[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	return user, nil
}

func (r *settlementUserReaderStub) setError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

func (r *settlementUserReaderStub) loadCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type settlementNotifyFixture struct {
	svc    *BalanceNotifyService
	push   *settlementPushSpy
	users  *settlementUserReaderStub
	now    time.Time
	clockM sync.Mutex
}

// newSettlementNotifyFixture wires a notify service whose global alert threshold is
// $10 and whose phone push is observable. Email delivery is a no-op (no verified
// recipients), so a "sent" alert is visible as exactly one push.
func newSettlementNotifyFixture(t *testing.T, userIDs ...int64) *settlementNotifyFixture {
	t.Helper()
	svc, settings := newBalanceNotifyServiceForTest()
	settings.data[SettingKeyBalanceLowNotifyEnabled] = "true"
	settings.data[SettingKeyBalanceLowNotifyThreshold] = "10"
	f := &settlementNotifyFixture{
		svc:   svc,
		push:  &settlementPushSpy{},
		users: &settlementUserReaderStub{users: map[int64]*User{}},
		now:   time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC),
	}
	for _, id := range userIDs {
		// No e-mail address on purpose: the alert then has no e-mail recipient, so
		// the test never touches SMTP and a delivered alert is exactly one push.
		f.users.users[id] = &User{ID: id, Username: "u", BalanceNotifyEnabled: true}
	}
	svc.SetPushNotifier(f.push)
	svc.SetUserReader(f.users)
	svc.loadRetryBackoff = time.Millisecond
	svc.clock = func() time.Time {
		f.clockM.Lock()
		defer f.clockM.Unlock()
		return f.now
	}
	return f
}

func (f *settlementNotifyFixture) advance(d time.Duration) {
	f.clockM.Lock()
	defer f.clockM.Unlock()
	f.now = f.now.Add(d)
}

// settled builds the result of one flush that moved a user from `before` to `after`
// (so Amount = before-after, NewBalance = after) with default alert preferences.
func settled(userID int64, before, after float64) UsageBalanceSettlementResult {
	return UsageBalanceSettlementResult{
		UserID: userID, EventCount: 3, Amount: before - after, NewBalance: after,
		NotifyEnabled: true, NotifyThresholdType: "fixed",
	}
}

func (f *settlementNotifyFixture) pushed(t *testing.T, userID int64, want int) {
	t.Helper()
	require.Eventually(t, func() bool { return f.push.forUser(userID) == want }, 2*time.Second, 5*time.Millisecond,
		"user %d: want %d pushes, have %d", userID, want, f.push.forUser(userID))
}

func (f *settlementNotifyFixture) neverPushed(t *testing.T, userID int64) {
	t.Helper()
	require.Never(t, func() bool { return f.push.forUser(userID) > 0 }, 150*time.Millisecond, 10*time.Millisecond,
		"user %d must not be alerted", userID)
}

func TestSettlementLowBalance_AlertsOnceWhenAFlushCrossesTheThreshold(t *testing.T) {
	f := newSettlementNotifyFixture(t, 1)

	f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{settled(1, 11, 9)})
	f.pushed(t, 1, 1)
	require.Contains(t, f.push.snapshot()[0].body, "9.00", "the alert carries the exact balance after the flush")
	require.Contains(t, f.push.snapshot()[0].body, "10.00")

	// Every later flush of the same user is already below the threshold: silent,
	// no matter how many requests it coalesced.
	f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{settled(1, 9, 7), settled(1, 7, 3)})
	f.neverPushedMore(t, 1, 1)
}

func (f *settlementNotifyFixture) neverPushedMore(t *testing.T, userID int64, total int) {
	t.Helper()
	require.Never(t, func() bool { return f.push.forUser(userID) > total }, 150*time.Millisecond, 10*time.Millisecond)
	require.Equal(t, total, f.push.forUser(userID))
}

func TestSettlementLowBalance_CrossingBoundaries(t *testing.T) {
	// All balances are exactly representable in binary so that
	// NewBalance+Amount reproduces the "before" value bit for bit.
	tests := []struct {
		name          string
		before, after float64
		wantAlert     bool
	}{
		{"above to below", 12, 9.5, true},
		{"exactly at the threshold before, below after", 10, 9.5, true},
		{"still exactly at the threshold after", 11, 10, false},
		{"stays well above", 100, 95, false},
		{"already below before the flush", 9, 7, false},
		{"crosses by a small step", 10.0625, 9.9375, true},
		{"jumps from far above to negative", 50, -2, true},
		{"already negative", -1, -3, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newSettlementNotifyFixture(t, 1)
			f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{settled(1, tt.before, tt.after)})
			if tt.wantAlert {
				f.pushed(t, 1, 1)
			} else {
				f.neverPushed(t, 1)
				require.Zero(t, f.users.loadCalls(), "no crossing, no user read")
			}
		})
	}
}

func TestSettlementLowBalance_PreferencesDecideTheThreshold(t *testing.T) {
	t.Run("per-user fixed threshold overrides the global one", func(t *testing.T) {
		f := newSettlementNotifyFixture(t, 1)
		custom := 5.0
		r := settled(1, 11, 9) // crosses the global 10, not the user's 5
		r.NotifyThreshold = &custom
		f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{r})
		f.neverPushed(t, 1)

		r = settled(1, 6, 4) // crosses the user's 5, not the global 10 (already below it)
		r.NotifyThreshold = &custom
		f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{r})
		f.pushed(t, 1, 1)
	})
	t.Run("percentage threshold uses total recharged", func(t *testing.T) {
		f := newSettlementNotifyFixture(t, 1)
		percent := 10.0
		r := settled(1, 25, 15) // 10% of 200 recharged = $20
		r.NotifyThreshold = &percent
		r.NotifyThresholdType = "percentage"
		r.TotalRecharged = 200
		f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{r})
		f.pushed(t, 1, 1)
	})
	t.Run("user turned alerts off", func(t *testing.T) {
		f := newSettlementNotifyFixture(t, 1)
		r := settled(1, 11, 9)
		r.NotifyEnabled = false
		f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{r})
		f.neverPushed(t, 1)
	})
	t.Run("global switch off", func(t *testing.T) {
		svc, settings := newBalanceNotifyServiceForTest()
		settings.data[SettingKeyBalanceLowNotifyEnabled] = "false"
		push := &settlementPushSpy{}
		svc.SetPushNotifier(push)
		svc.SetUserReader(&settlementUserReaderStub{users: map[int64]*User{1: {ID: 1, BalanceNotifyEnabled: true}}})
		svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{settled(1, 11, 9)})
		require.Never(t, func() bool { return len(push.snapshot()) > 0 }, 150*time.Millisecond, 10*time.Millisecond)
	})
	t.Run("zero threshold", func(t *testing.T) {
		svc, settings := newBalanceNotifyServiceForTest()
		settings.data[SettingKeyBalanceLowNotifyEnabled] = "true"
		settings.data[SettingKeyBalanceLowNotifyThreshold] = "0"
		push := &settlementPushSpy{}
		svc.SetPushNotifier(push)
		svc.SetUserReader(&settlementUserReaderStub{users: map[int64]*User{1: {ID: 1, BalanceNotifyEnabled: true}}})
		svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{settled(1, 1, -1)})
		require.Never(t, func() bool { return len(push.snapshot()) > 0 }, 150*time.Millisecond, 10*time.Millisecond)
	})
}

func TestSettlementLowBalance_CooldownStopsRepeatAlertsPerUser(t *testing.T) {
	f := newSettlementNotifyFixture(t, 1, 2)

	f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{settled(1, 11, 9)})
	f.pushed(t, 1, 1)

	// The balance comes back above the threshold (a small top-up) and is spent
	// again 5 minutes later: a genuine new crossing, but inside the cooldown.
	f.advance(5 * time.Minute)
	f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{settled(1, 10.5, 9.5)})
	f.neverPushedMore(t, 1, 1)

	// Another user is unaffected by user 1's cooldown.
	f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{settled(2, 11, 9)})
	f.pushed(t, 2, 1)

	// Past the cooldown the next crossing alerts again.
	f.advance(settlementLowBalanceCooldown)
	f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{settled(1, 10.5, 9.5)})
	f.pushed(t, 1, 2)
}

func TestSettlementLowBalance_CooldownBoundaryIsExclusive(t *testing.T) {
	var tracker settlementAlertTracker
	start := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	require.True(t, tracker.claim(1, start, time.Minute))
	require.False(t, tracker.claim(1, start.Add(time.Minute-time.Nanosecond), time.Minute))
	require.True(t, tracker.claim(1, start.Add(time.Minute), time.Minute), "exactly one cooldown later the user may be alerted again")
	require.False(t, tracker.claim(1, start.Add(time.Minute+time.Second), time.Minute), "the new alert restarts the cooldown")
}

func TestSettlementLowBalance_CooldownTableStaysBounded(t *testing.T) {
	var tracker settlementAlertTracker
	start := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	for id := int64(1); id <= settlementLowBalanceCooldownMaxEntries; id++ {
		require.True(t, tracker.claim(id, start, time.Minute))
	}
	// A later claim past every cooldown prunes the expired entries instead of growing.
	require.True(t, tracker.claim(1_000_000, start.Add(2*time.Minute), time.Minute))
	require.LessOrEqual(t, len(tracker.at), 2)
	// Entries still inside their cooldown are never pruned.
	require.False(t, tracker.claim(1_000_000, start.Add(2*time.Minute+time.Second), time.Minute))
}

func TestSettlementLowBalance_UserReadFailureDoesNotConsumeTheCooldown(t *testing.T) {
	f := newSettlementNotifyFixture(t, 1)
	f.users.setError(errors.New("db timeout"))

	f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{settled(1, 11, 9)})
	f.neverPushed(t, 1)
	require.Equal(t, settlementLowBalanceLoadAttempts, f.users.loadCalls(), "a bounded number of attempts, then give up")

	// The alert could not be delivered, so the next crossing still gets its chance
	// (it must not be swallowed by a cooldown that nothing was sent for).
	f.users.setError(nil)
	f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{settled(1, 10.5, 9.5)})
	f.pushed(t, 1, 1)
}

// 穿越是只会出现一次的边沿：之后的 flush 看到的余额都已经在阈值以下。读用户偶发失败不能
// 成为这条提醒丢失的原因，所以在放弃之前重试几次。
func TestSettlementLowBalance_TransientUserReadFailureIsRetried(t *testing.T) {
	f := newSettlementNotifyFixture(t, 1)
	f.users.failFirst = settlementLowBalanceLoadAttempts - 1

	f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{settled(1, 11, 9)})

	f.pushed(t, 1, 1)
	require.Equal(t, settlementLowBalanceLoadAttempts, f.users.loadCalls())
	f.neverPushedMore(t, 1, 1)
}

// 用户已经被删掉时没有收件人可发，也没有重试的意义。
func TestSettlementLowBalance_DeletedUserIsNotRetried(t *testing.T) {
	f := newSettlementNotifyFixture(t) // user 1 does not exist
	f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{settled(1, 11, 9)})
	f.neverPushed(t, 1)
	require.Equal(t, 1, f.users.loadCalls())
}

func TestSettlementLowBalance_UserWhoJustDisabledAlertsIsNotAlerted(t *testing.T) {
	f := newSettlementNotifyFixture(t, 1)
	f.users.users[1].BalanceNotifyEnabled = false
	f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{settled(1, 11, 9)})
	f.neverPushed(t, 1)
	require.Equal(t, 1, f.users.loadCalls())
}

func TestSettlementLowBalance_OnlyCrossingUsersInABatchAreAlerted(t *testing.T) {
	f := newSettlementNotifyFixture(t, 1, 2, 3, 4)
	f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{
		settled(1, 11, 9),    // crosses
		settled(2, 100, 90),  // far above
		settled(3, 9, 8),     // already below
		settled(4, 10.5, 10), // lands exactly on the threshold: not below it
	})
	f.pushed(t, 1, 1)
	f.neverPushed(t, 2)
	f.neverPushed(t, 3)
	f.neverPushed(t, 4)
	require.Equal(t, 1, f.users.loadCalls(), "only the crossing user is read")
}

func TestSettlementLowBalance_IgnoresEmptyBatchesAndUnchangedBalances(t *testing.T) {
	f := newSettlementNotifyFixture(t, 1)
	unchanged := settled(1, 9, 9) // Amount 0: nothing was deducted, nothing can have crossed
	require.NotPanics(t, func() { f.svc.ObserveBalanceSettlements(nil) })
	f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{unchanged})
	f.neverPushed(t, 1)
	require.Zero(t, f.users.loadCalls())
}

func TestSettlementLowBalance_NeedsAUserReaderAndServices(t *testing.T) {
	var nilSvc *BalanceNotifyService
	require.NotPanics(t, func() { nilSvc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{settled(1, 11, 9)}) })

	svc, settings := newBalanceNotifyServiceForTest()
	settings.data[SettingKeyBalanceLowNotifyEnabled] = "true"
	settings.data[SettingKeyBalanceLowNotifyThreshold] = "10"
	push := &settlementPushSpy{}
	svc.SetPushNotifier(push)
	require.NotPanics(t, func() { svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{settled(1, 11, 9)}) },
		"without a user reader the observer does nothing")
	require.Never(t, func() bool { return len(push.snapshot()) > 0 }, 100*time.Millisecond, 10*time.Millisecond)
}

// 同一次扣费从「逐请求」改成「按 flush 合并」之后，提醒次数不能变多：
// 一个把用户压到阈值以下的 flush 合并了 200 个请求，也只提醒一次。
func TestSettlementLowBalance_CoalescedRequestsAlertOnce(t *testing.T) {
	f := newSettlementNotifyFixture(t, 1)
	r := settled(1, 10.2, 9.0)
	r.EventCount = 200
	f.svc.ObserveBalanceSettlements([]UsageBalanceSettlementResult{r})
	f.pushed(t, 1, 1)
	f.neverPushedMore(t, 1, 1)
}
