package service

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
)

const subscriptionDayDuration = 24 * time.Hour

type UserSubscription struct {
	ID      int64
	UserID  int64
	GroupID int64

	StartsAt  time.Time
	ExpiresAt time.Time
	Status    string

	DailyWindowStart   *time.Time
	WeeklyWindowStart  *time.Time
	MonthlyWindowStart *time.Time

	DailyUsageUSD   float64
	WeeklyUsageUSD  float64
	MonthlyUsageUSD float64

	AssignedBy *int64
	AssignedAt time.Time
	Notes      string

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time

	User           *User
	Group          *Group
	AssignedByUser *User
}

func (s *UserSubscription) IsActive() bool {
	return s.Status == SubscriptionStatusActive && time.Now().Before(s.ExpiresAt)
}

func (s *UserSubscription) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

func (s *UserSubscription) DaysRemaining() int {
	return s.daysRemainingAt(time.Now())
}

func (s *UserSubscription) daysRemainingAt(now time.Time) int {
	remaining := s.ExpiresAt.Sub(now)
	if remaining <= 0 {
		return 0
	}

	days := int(remaining / subscriptionDayDuration)
	if remaining%subscriptionDayDuration != 0 {
		days++
	}
	return days
}

func (s *UserSubscription) IsWindowActivated() bool {
	return s.DailyWindowStart != nil || s.WeeklyWindowStart != nil || s.MonthlyWindowStart != nil
}

func (s *UserSubscription) HasOneTimeDailyQuota() bool {
	if s == nil || s.StartsAt.IsZero() || s.ExpiresAt.IsZero() {
		return false
	}
	return !s.ExpiresAt.After(s.StartsAt.AddDate(0, 0, 1))
}

func (s *UserSubscription) effectiveDailyResetMode(group *Group) string {
	if group != nil {
		return group.EffectiveDailyResetMode()
	}
	if s != nil && s.Group != nil {
		return s.Group.EffectiveDailyResetMode()
	}
	return DailyResetModeCalendar
}

// anchoredDailyWindowAt returns the 0-based 24-hour window index k and boundary
// starts_at + k*24h containing now.
func (s *UserSubscription) anchoredDailyWindowAt(now time.Time) (int64, time.Time, bool) {
	if s == nil || s.StartsAt.IsZero() {
		return 0, time.Time{}, false
	}
	if now.Before(s.StartsAt) {
		return 0, s.StartsAt, false
	}
	k := int64(now.Sub(s.StartsAt) / subscriptionDayDuration)
	return k, s.StartsAt.Add(time.Duration(k) * subscriptionDayDuration), true
}

// currentDailyWindowStartAtForGroup returns the canonical daily_window_start
// anchor for now under the group's reset mode:
//   - calendar: configured-timezone start of day (00:00)
//   - anchored_24h: starts_at + k*24h for the 24h window containing now
func (s *UserSubscription) currentDailyWindowStartAtForGroup(group *Group, now time.Time) time.Time {
	if s.effectiveDailyResetMode(group) == DailyResetModeAnchored24h {
		if _, windowStart, ok := s.anchoredDailyWindowAt(now); ok {
			return windowStart
		}
		if s != nil && !s.StartsAt.IsZero() {
			return s.StartsAt
		}
		return now
	}
	return timezone.StartOfDay(now)
}

func (s *UserSubscription) NeedsDailyReset() bool {
	return s.NeedsDailyResetAt(time.Now())
}

func (s *UserSubscription) NeedsDailyResetAt(now time.Time) bool {
	return s.NeedsDailyResetAtForGroup(nil, now)
}

func (s *UserSubscription) NeedsDailyResetAtForGroup(group *Group, now time.Time) bool {
	_, ok := s.automaticDailyWindowStartAtForGroup(group, now)
	return ok
}

func (s *UserSubscription) NeedsWeeklyReset() bool {
	return s.NeedsWeeklyResetAt(time.Now())
}

func (s *UserSubscription) NeedsWeeklyResetAt(now time.Time) bool {
	if s.WeeklyWindowStart == nil {
		return false
	}
	return !now.Before(s.WeeklyWindowStart.Add(7 * 24 * time.Hour))
}

func (s *UserSubscription) NeedsMonthlyReset() bool {
	return s.NeedsMonthlyResetAt(time.Now())
}

func (s *UserSubscription) NeedsMonthlyResetAt(now time.Time) bool {
	if s.MonthlyWindowStart == nil {
		return false
	}
	return !now.Before(s.MonthlyWindowStart.Add(30 * 24 * time.Hour))
}

func (s *UserSubscription) canAutomaticallyResetDailyAt(now time.Time) bool {
	return s.canAutomaticallyResetDailyAtForGroup(nil, now)
}

func (s *UserSubscription) canAutomaticallyResetDailyAtForGroup(group *Group, now time.Time) bool {
	_, ok := s.automaticDailyWindowStartAtForGroup(group, now)
	return ok
}

// automaticDailyWindowStartAt 计算日窗口的当前有效起点：
//   - calendar 模式（默认）：按配置时区日历日 0 点对齐；只要持久化起点落在更早的
//     日历日，就推进到今天 0 点，非 0 点锚点在下一个 0 点自动拉回。
//   - anchored_24h 模式：以 StartsAt 为固定锚点按整 24 小时步进（StartsAt + k*24h），
//     永不越过或落于 ExpiresAt 上；达到 ExpiresAt 时直接到期，不再授予新窗口。
func (s *UserSubscription) automaticDailyWindowStartAt(now time.Time) (time.Time, bool) {
	return s.automaticDailyWindowStartAtForGroup(nil, now)
}

func (s *UserSubscription) automaticDailyWindowStartAtForGroup(group *Group, now time.Time) (time.Time, bool) {
	if s == nil || s.DailyWindowStart == nil {
		return time.Time{}, false
	}
	if s.HasOneTimeDailyQuota() {
		return time.Time{}, false
	}
	if s.effectiveDailyResetMode(group) == DailyResetModeAnchored24h {
		return s.automaticAnchoredDailyWindowStartAt(now)
	}
	today := timezone.StartOfDay(now)
	if !today.After(timezone.StartOfDay(*s.DailyWindowStart)) {
		return time.Time{}, false
	}
	return today, true
}

func (s *UserSubscription) automaticAnchoredDailyWindowStartAt(now time.Time) (time.Time, bool) {
	if s == nil || s.DailyWindowStart == nil || s.StartsAt.IsZero() {
		return time.Time{}, false
	}
	if !s.ExpiresAt.IsZero() && !now.Before(s.ExpiresAt) {
		return time.Time{}, false
	}
	k, windowStart, ok := s.anchoredDailyWindowAt(now)
	if !ok || k <= 0 {
		return time.Time{}, false
	}
	if !s.ExpiresAt.IsZero() && !windowStart.Before(s.ExpiresAt) {
		return time.Time{}, false
	}
	if !s.DailyWindowStart.Before(windowStart) {
		return time.Time{}, false
	}
	return windowStart, true
}

func (s *UserSubscription) canAutomaticallyResetWeeklyAt(now time.Time) bool {
	_, ok := s.automaticWindowStartAt(s.WeeklyWindowStart, 7*24*time.Hour, now)
	return ok
}

func (s *UserSubscription) canAutomaticallyResetMonthlyAt(now time.Time) bool {
	_, ok := s.automaticWindowStartAt(s.MonthlyWindowStart, 30*24*time.Hour, now)
	return ok
}

// windowResetAnchor 返回周/月窗口实际推进所依据的锚点。
// 早期订阅把首个窗口初始化在开通日零点；只有这个初始值是无歧义的，之后出现的
// 零点锚点可能来自手动重置，必须保持权威。
// 自动推进（automaticWindowStartAt）与对外展示的重置时间（WeeklyResetTime/
// MonthlyResetTime）必须共用这一修正，否则仪表盘显示的重置时间会早于窗口实际
// 滚动的时间。
// 日窗口按日历日对齐（automaticDailyWindowStartAt），不走这里。
func (s *UserSubscription) windowResetAnchor(previous time.Time) time.Time {
	legacyAnchor := startOfDay(s.StartsAt)
	if legacyAnchor.Before(s.StartsAt) && previous.Equal(legacyAnchor) {
		return s.StartsAt
	}
	return previous
}

// automaticWindowStartAt 计算周/月窗口（期限对齐滚动窗口）的当前窗口起点。
// 窗口从锚点按整数个 period 步进，且不越过订阅到期时间，避免最后一个不完整
// 周期重复发放额度（issue #5051）。日窗口不走此函数，见 automaticDailyWindowStartAt。
func (s *UserSubscription) automaticWindowStartAt(previous *time.Time, period time.Duration, now time.Time) (time.Time, bool) {
	if previous == nil {
		return time.Time{}, false
	}

	anchor := s.windowResetAnchor(*previous)
	next := anchor.Add(period)
	if now.Before(next) || !next.Before(s.ExpiresAt) {
		return time.Time{}, false
	}

	periods := now.Sub(anchor) / period
	lastPeriodBeforeExpiry := (s.ExpiresAt.Sub(anchor) - 1) / period
	if periods > lastPeriodBeforeExpiry {
		periods = lastPeriodBeforeExpiry
	}
	return anchor.Add(periods * period), true
}

// NextDailyResetAt 返回到期前下一次自动刷新日额度的时刻；若无后续自动刷新
// （窗口未激活、订阅已过期/撤销、一次性日卡或下一次刷新边界 >= ExpiresAt），返回 nil。
func (s *UserSubscription) NextDailyResetAt() *time.Time {
	return s.NextDailyResetAtForGroupAt(nil, time.Time{})
}

// NextDailyResetAtAt 在指定评估时刻 now 下计算下一次自动刷新日额度时刻；
// 若 now >= ExpiresAt（已过期），返回 nil。
func (s *UserSubscription) NextDailyResetAtAt(now time.Time) *time.Time {
	return s.NextDailyResetAtForGroupAt(nil, now)
}

func (s *UserSubscription) NextDailyResetAtForGroup(group *Group) *time.Time {
	return s.NextDailyResetAtForGroupAt(group, time.Time{})
}

func (s *UserSubscription) NextDailyResetAtForGroupAt(group *Group, now time.Time) *time.Time {
	if s == nil || s.DailyWindowStart == nil {
		return nil
	}
	if s.Status == SubscriptionStatusExpired || s.Status == SubscriptionStatusRevoked || s.DeletedAt != nil {
		return nil
	}
	if !now.IsZero() && !s.ExpiresAt.IsZero() && !now.Before(s.ExpiresAt) {
		return nil
	}
	if s.HasOneTimeDailyQuota() {
		return nil
	}

	var next time.Time
	if s.effectiveDailyResetMode(group) == DailyResetModeAnchored24h {
		if s.StartsAt.IsZero() {
			return nil
		}
		var k int64
		if !s.DailyWindowStart.Before(s.StartsAt) {
			k = int64(s.DailyWindowStart.Sub(s.StartsAt) / subscriptionDayDuration)
		}
		next = s.StartsAt.Add(time.Duration(k+1) * subscriptionDayDuration)
	} else {
		// 日窗口按日历日对齐：下次刷新固定在窗口起点所在日的次日 0 点。
		next = timezone.StartOfDay(*s.DailyWindowStart).AddDate(0, 0, 1)
	}

	if !s.ExpiresAt.IsZero() && !next.Before(s.ExpiresAt) {
		return nil
	}
	return &next
}

func (s *UserSubscription) DailyResetTime() *time.Time {
	return s.DailyResetTimeForGroupAt(nil, time.Time{})
}

func (s *UserSubscription) DailyResetTimeAt(now time.Time) *time.Time {
	return s.DailyResetTimeForGroupAt(nil, now)
}

func (s *UserSubscription) DailyResetTimeForGroup(group *Group) *time.Time {
	return s.DailyResetTimeForGroupAt(group, time.Time{})
}

func (s *UserSubscription) DailyResetTimeForGroupAt(group *Group, now time.Time) *time.Time {
	if s == nil || s.DailyWindowStart == nil {
		return nil
	}
	if next := s.NextDailyResetAtForGroupAt(group, now); next != nil {
		return next
	}
	if !s.ExpiresAt.IsZero() {
		t := s.ExpiresAt
		return &t
	}
	return nil
}

func (s *UserSubscription) WeeklyResetTime() *time.Time {
	if s.WeeklyWindowStart == nil {
		return nil
	}
	t := s.windowResetAnchor(*s.WeeklyWindowStart).Add(7 * 24 * time.Hour)
	return &t
}

func (s *UserSubscription) MonthlyResetTime() *time.Time {
	if s.MonthlyWindowStart == nil {
		return nil
	}
	t := s.windowResetAnchor(*s.MonthlyWindowStart).Add(30 * 24 * time.Hour)
	return &t
}

func (s *UserSubscription) CheckDailyLimit(group *Group, additionalCost float64) bool {
	if !group.HasDailyLimit() {
		return true
	}
	return s.DailyUsageUSD+additionalCost <= *group.DailyLimitUSD
}

func (s *UserSubscription) CheckWeeklyLimit(group *Group, additionalCost float64) bool {
	if !group.HasWeeklyLimit() {
		return true
	}
	return s.WeeklyUsageUSD+additionalCost <= *group.WeeklyLimitUSD
}

func (s *UserSubscription) CheckMonthlyLimit(group *Group, additionalCost float64) bool {
	if !group.HasMonthlyLimit() {
		return true
	}
	return s.MonthlyUsageUSD+additionalCost <= *group.MonthlyLimitUSD
}

func (s *UserSubscription) CheckAllLimits(group *Group, additionalCost float64) (daily, weekly, monthly bool) {
	daily = s.CheckDailyLimit(group, additionalCost)
	weekly = s.CheckWeeklyLimit(group, additionalCost)
	monthly = s.CheckMonthlyLimit(group, additionalCost)
	return
}
