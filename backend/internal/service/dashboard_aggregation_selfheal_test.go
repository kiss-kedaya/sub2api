package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// 迟到行自愈（每小时把最近 3 小时重新聚合一遍）的调度测试。
// 自愈补上的 SQL 效果（迟到行进汇总、日活不被压低）在
// repository/dashboard_aggregation_selfheal_pg_test.go 里用真实 PostgreSQL 验证。

type selfHealRange struct{ start, end time.Time }

// selfHealRepoStub 记录聚合调用；blockFirst 非 nil 时第一次 AggregateRange 会阻塞到 release()，
// 用来把「某个聚合作业正在运行」的状态固定住。只有第一次调用阻塞、其余调用直接通过：
// 互斥被破坏时，第二个作业会越过互斥照常调用，测试因此是断言失败而不是死锁。
type selfHealRepoStub struct {
	mu            sync.Mutex
	ranges        []selfHealRange
	recomputes    int
	watermarkSets int
	lastWatermark time.Time
	aggregateErr  error
	inflight      int
	maxInflight   int

	panicOnAggregate bool

	blockFirst  chan struct{}
	blocked     int32 // 第一次 AggregateRange 调用把它置 1：只有那一次阻塞
	releaseOnce sync.Once
	entered     chan struct{} // 第一次 AggregateRange 进来后被关闭
}

// newBlockingSelfHealRepo 返回第一次 AggregateRange 会阻塞的 stub；测试结束时一定放行，
// 断言失败也不会把被阻塞的作业留在后台。
func newBlockingSelfHealRepo(t *testing.T) *selfHealRepoStub {
	t.Helper()
	repo := &selfHealRepoStub{blockFirst: make(chan struct{}), entered: make(chan struct{})}
	t.Cleanup(repo.release)
	return repo
}

func (s *selfHealRepoStub) release() {
	s.releaseOnce.Do(func() { close(s.blockFirst) })
}

func (s *selfHealRepoStub) AggregateRange(_ context.Context, start, end time.Time) error {
	s.mu.Lock()
	s.ranges = append(s.ranges, selfHealRange{start: start, end: end})
	s.inflight++
	if s.inflight > s.maxInflight {
		s.maxInflight = s.inflight
	}
	err := s.aggregateErr
	shouldPanic := s.panicOnAggregate
	s.mu.Unlock()
	if shouldPanic {
		s.mu.Lock()
		s.inflight--
		s.mu.Unlock()
		panic("boom")
	}

	if s.blockFirst != nil && atomic.CompareAndSwapInt32(&s.blocked, 0, 1) {
		close(s.entered)
		<-s.blockFirst
	}

	s.mu.Lock()
	s.inflight--
	s.mu.Unlock()
	return err
}

func (s *selfHealRepoStub) RecomputeRange(context.Context, time.Time, time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recomputes++
	return nil
}

func (s *selfHealRepoStub) GetAggregationWatermark(context.Context) (time.Time, error) {
	return time.Now().UTC(), nil
}

func (s *selfHealRepoStub) UpdateAggregationWatermark(_ context.Context, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.watermarkSets++
	s.lastWatermark = at
	return nil
}

func (s *selfHealRepoStub) CleanupAggregates(context.Context, time.Time, time.Time) error {
	return nil
}
func (s *selfHealRepoStub) CleanupUsageLogs(context.Context, time.Time) error { return nil }
func (s *selfHealRepoStub) CleanupUsageBillingDedup(context.Context, time.Time) error {
	return nil
}
func (s *selfHealRepoStub) EnsureUsageLogsPartitions(context.Context, time.Time) error { return nil }

func (s *selfHealRepoStub) calls() []selfHealRange {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]selfHealRange(nil), s.ranges...)
}

func (s *selfHealRepoStub) callCount() int { return len(s.calls()) }

func (s *selfHealRepoStub) setAggregateErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aggregateErr = err
}

func newSelfHealService(repo DashboardAggregationRepository) *DashboardAggregationService {
	return &DashboardAggregationService{
		repo: repo,
		cfg: config.DashboardAggregationConfig{
			Enabled:            true,
			IntervalSeconds:    60,
			LookbackSeconds:    120,
			RecomputeDays:      2,
			BackfillBatchHours: 1,
			Retention: config.DashboardAggregationRetentionConfig{
				UsageLogsDays:         1,
				UsageBillingDedupDays: 2,
				HourlyDays:            1,
				DailyDays:             1,
			},
		},
	}
}

func waitSelfHealSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("等待超时: %s", what)
	}
}

func TestDashboardAggregationSelfHealRange(t *testing.T) {
	// 窗口常量故意在测试里写死 3 小时：把窗口改短，迟到几十分钟的行就补不回来。
	for _, tc := range []struct {
		name  string
		now   time.Time
		start time.Time
	}{
		{"mid hour", time.Date(2026, 11, 26, 10, 25, 30, 0, time.UTC), time.Date(2026, 11, 26, 7, 0, 0, 0, time.UTC)},
		{"on the hour", time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC), time.Date(2026, 11, 26, 7, 0, 0, 0, time.UTC)},
		{"last second of the hour", time.Date(2026, 11, 26, 10, 59, 59, 0, time.UTC), time.Date(2026, 11, 26, 7, 0, 0, 0, time.UTC)},
		{"across midnight", time.Date(2026, 11, 26, 1, 5, 0, 0, time.UTC), time.Date(2026, 11, 25, 22, 0, 0, 0, time.UTC)},
	} {
		start, end := DashboardAggregationSelfHealRange(tc.now)
		require.Equal(t, tc.start, start, tc.name)
		require.Equal(t, tc.now, end, tc.name)
	}

	require.Equal(t, 3*time.Hour, dashboardAggregationSelfHealWindow)
	require.Equal(t, time.Hour, dashboardAggregationSelfHealInterval)
	// 锁 TTL 必须盖过自愈的最长运行时间，否则锁会在运行中途过期。
	require.Less(t, dashboardAggregationSelfHealTimeout, dashboardAggregationLeaderLockTTL)
}

func TestDashboardAggregationSelfHealReaggregatesTrailingWindowByHour(t *testing.T) {
	repo := &selfHealRepoStub{}
	svc := newSelfHealService(repo)

	before := time.Now().UTC()
	svc.runSelfHeal()
	after := time.Now().UTC()

	calls := repo.calls()
	require.GreaterOrEqual(t, len(calls), 3)
	first, last := calls[0], calls[len(calls)-1]
	require.False(t, last.end.Before(before))
	require.False(t, last.end.After(after))
	require.Equal(t, last.end.Add(-3*time.Hour).Truncate(time.Hour), first.start, "窗口是最近 3 小时，起点对齐到整点")
	for i, c := range calls {
		require.Equal(t, c.start.Truncate(time.Hour), c.start, "每批从整点开始")
		require.LessOrEqual(t, c.end.Sub(c.start), time.Hour, "每批至多一个小时：单个语句有界")
		if i > 0 {
			require.Equal(t, calls[i-1].end, c.start, "批次首尾相接")
		}
	}

	// 不能走 RecomputeRange（会把窗口所在整天的 daily_users 删掉后只按窗口重建），也不动水位。
	require.Zero(t, repo.recomputes)
	require.Zero(t, repo.watermarkSets)
	require.Zero(t, atomic.LoadInt32(&svc.running), "跑完释放 running 互斥")
}

func TestDashboardAggregationSelfHealFollowsBackfillBatchHours(t *testing.T) {
	repo := &selfHealRepoStub{}
	svc := newSelfHealService(repo)
	svc.cfg.BackfillBatchHours = 2

	svc.runSelfHeal()

	for _, c := range repo.calls() {
		require.LessOrEqual(t, c.end.Sub(c.start), 2*time.Hour)
	}
	require.Less(t, repo.callCount(), 4)
}

func TestDashboardAggregationSelfHealRunsAtMostOncePerHour(t *testing.T) {
	repo := &selfHealRepoStub{}
	svc := newSelfHealService(repo)

	svc.runSelfHeal()
	ran := repo.callCount()
	require.Positive(t, ran)

	svc.runSelfHeal()
	svc.runSelfHeal()
	require.Equal(t, ran, repo.callCount(), "一小时内不重复跑")

	svc.lastSelfHeal.Store(time.Now().UTC().Add(-dashboardAggregationSelfHealInterval + time.Minute))
	svc.runSelfHeal()
	require.Equal(t, ran, repo.callCount(), "距上次不满一小时仍然不跑")

	svc.lastSelfHeal.Store(time.Now().UTC().Add(-dashboardAggregationSelfHealInterval - time.Minute))
	svc.runSelfHeal()
	require.Greater(t, repo.callCount(), ran, "过了一小时再跑一次")
}

func TestDashboardAggregationSelfHealFailureWaitsForNextHourAndLeavesMinuteJobAlone(t *testing.T) {
	repo := &selfHealRepoStub{aggregateErr: errors.New("statement timeout")}
	svc := newSelfHealService(repo)

	svc.runSelfHeal() // 失败只记日志，不 panic
	require.Equal(t, 1, repo.callCount(), "第一批失败就停，不继续撞数据库")
	require.Zero(t, atomic.LoadInt32(&svc.running), "失败也要释放 running 互斥")

	svc.runSelfHeal()
	require.Equal(t, 1, repo.callCount(), "失败也算一次尝试：下个小时再试，不是下个 tick")

	// 每分钟的聚合不受影响。
	repo.setAggregateErr(nil)
	svc.runScheduledAggregation()
	require.Equal(t, 2, repo.callCount())
	require.Equal(t, 1, repo.watermarkSets, "分钟聚合照常推进水位")
}

func TestDashboardAggregationSelfHealPanicDoesNotEscapeIntoTheMinuteJob(t *testing.T) {
	repo := &selfHealRepoStub{panicOnAggregate: true}
	svc := newSelfHealService(repo)

	require.NotPanics(t, svc.runSelfHealSafely, "回调里的 panic 会让定时任务不再续期，连分钟聚合一起停")
	require.Zero(t, atomic.LoadInt32(&svc.running), "panic 之后 running 互斥要被还回去")
	require.NotNil(t, svc.lastSelfHeal.Load(), "算一次尝试：下个小时再试")

	repo.mu.Lock()
	repo.panicOnAggregate = false
	repo.mu.Unlock()
	before := repo.callCount()
	svc.runScheduledAggregation()
	require.Greater(t, repo.callCount(), before, "分钟聚合照常跑")
}

// 上面的测试都直接调 runSelfHeal；自愈是否真的接进了分钟聚合的定时回调，要经 Start 单独验证
// （接线丢了，自愈就是一段永远不会运行的死代码）。
func TestDashboardAggregationStartRunsSelfHealFromTheMinuteTimer(t *testing.T) {
	tw, err := NewTimingWheelService()
	require.NoError(t, err)
	t.Cleanup(tw.Stop)

	repo := &selfHealRepoStub{}
	svc := newSelfHealService(repo)
	svc.cfg.IntervalSeconds = 1
	svc.timingWheel = tw

	svc.Start()

	started, ok := svc.lastSelfHeal.Load().(time.Time)
	require.True(t, ok, "Start 记下自愈的起点：启动重算刚覆盖过最近几天，第一次自愈从一小时后算起")
	require.WithinDuration(t, time.Now().UTC(), started, 10*time.Second)

	// 把「上次自愈」拨到两小时前：分钟聚合的下一次定时回调之后，自愈应当跑起来并重新记时间。
	old := time.Now().UTC().Add(-2 * time.Hour)
	svc.lastSelfHeal.Store(old)
	require.Eventually(t, func() bool {
		last, isTime := svc.lastSelfHeal.Load().(time.Time)
		return isTime && last.After(old.Add(time.Hour))
	}, 15*time.Second, 50*time.Millisecond, "分钟聚合的定时回调没有调用自愈")
}

func TestDashboardAggregationStartDoesNotArmSelfHealWithoutRecomputeDays(t *testing.T) {
	tw, err := NewTimingWheelService()
	require.NoError(t, err)
	t.Cleanup(tw.Stop)

	repo := &selfHealRepoStub{}
	svc := newSelfHealService(repo)
	svc.cfg.RecomputeDays = 0
	svc.timingWheel = tw

	svc.Start()

	require.Nil(t, svc.lastSelfHeal.Load(), "recompute_days<=0：不重算、也不自愈")
}

func TestDashboardAggregationSelfHealSkippedWhenAggregationDisabled(t *testing.T) {
	for name, mutate := range map[string]func(*config.DashboardAggregationConfig){
		"aggregation disabled": func(c *config.DashboardAggregationConfig) { c.Enabled = false },
		"recompute_days = 0":   func(c *config.DashboardAggregationConfig) { c.RecomputeDays = 0 },
		"recompute_days < 0":   func(c *config.DashboardAggregationConfig) { c.RecomputeDays = -1 },
	} {
		repo := &selfHealRepoStub{}
		svc := newSelfHealService(repo)
		mutate(&svc.cfg)

		svc.runSelfHeal()

		require.Zero(t, repo.callCount(), name)
		require.Nil(t, svc.lastSelfHeal.Load(), name)
	}
}

func TestDashboardAggregationSelfHealDoesNotRunWhileMinuteAggregationRuns(t *testing.T) {
	repo := newBlockingSelfHealRepo(t)
	svc := newSelfHealService(repo)

	minuteDone := make(chan struct{})
	go func() {
		defer close(minuteDone)
		svc.runScheduledAggregation()
	}()
	waitSelfHealSignal(t, repo.entered, "分钟聚合进入 AggregateRange")

	svc.runSelfHeal() // 分钟聚合还没结束：自愈必须直接让路，不并发
	require.Equal(t, 1, repo.callCount(), "只有分钟聚合那一次调用")
	require.Nil(t, svc.lastSelfHeal.Load(), "没跑成不记时间戳，下一个 tick 再来")

	repo.release()
	waitSelfHealSignal(t, minuteDone, "分钟聚合结束")

	svc.runSelfHeal()
	require.Greater(t, repo.callCount(), 1, "分钟聚合结束后自愈可以跑")
	require.Equal(t, 1, repo.maxInflight, "任何时刻最多一个聚合在写汇总")
}

func TestDashboardAggregationMinuteAggregationYieldsWhileSelfHealRuns(t *testing.T) {
	repo := newBlockingSelfHealRepo(t)
	svc := newSelfHealService(repo)

	healDone := make(chan struct{})
	go func() {
		defer close(healDone)
		svc.runSelfHeal()
	}()
	waitSelfHealSignal(t, repo.entered, "自愈进入 AggregateRange")

	svc.runScheduledAggregation() // 自愈在跑：分钟聚合直接返回
	require.Equal(t, 1, repo.callCount())

	repo.release()
	waitSelfHealSignal(t, healDone, "自愈结束")
	require.Equal(t, 1, repo.maxInflight)

	before := repo.callCount()
	svc.runScheduledAggregation()
	require.Greater(t, repo.callCount(), before, "自愈结束后分钟聚合恢复")
}

func TestDashboardAggregationSelfHealYieldsToPeerLeader(t *testing.T) {
	ctx := context.Background()
	cache := &fakeLeaderLockCache{}
	ok, err := cache.TryAcquireLeaderLock(ctx, dashboardAggregationLeaderLockKey, "peer", time.Hour)
	require.NoError(t, err)
	require.True(t, ok)

	repo := &selfHealRepoStub{}
	svc := newSelfHealService(repo)
	svc.lockCache = cache
	svc.instanceID = "me"

	svc.runSelfHeal()
	require.Zero(t, repo.callCount(), "别的实例是 leader（同一把锁）：这一轮不跑")
	require.Nil(t, svc.lastSelfHeal.Load())
	require.Zero(t, atomic.LoadInt32(&svc.running), "没拿到锁要把 running 互斥还回去")

	require.NoError(t, cache.ReleaseLeaderLock(ctx, dashboardAggregationLeaderLockKey, "peer"))
	svc.runSelfHeal()
	require.Positive(t, repo.callCount())

	ok, err = cache.TryAcquireLeaderLock(ctx, dashboardAggregationLeaderLockKey, "next", time.Hour)
	require.NoError(t, err)
	require.True(t, ok, "跑完释放 leader 锁，分钟聚合可以接着拿")
}

func TestDashboardAggregationSelfHealStopsWhenRollupStorageBudgetExceeded(t *testing.T) {
	repo := &selfHealRepoStub{}
	svc := newSelfHealService(repo)
	svc.cfg.DimensionRollupsEnabled = true
	svc.cfg.RollupStorageBudgetGB = 1
	sized := &selfHealSizedRepo{selfHealRepoStub: repo, size: 2 << 30}
	svc.repo = sized

	svc.runSelfHeal()

	require.Equal(t, 1, repo.callCount(), "超预算：与启动重算一样在第一批之后停下")
	require.Zero(t, atomic.LoadInt32(&svc.running))
}

type selfHealSizedRepo struct {
	*selfHealRepoStub
	size int64
}

func (s *selfHealSizedRepo) GetDashboardDimensionRollupSizeBytes(context.Context) (int64, error) {
	return s.size, nil
}

// backfillRange 的批次循环抽成了 aggregateInBatches 供自愈共用：回填本身的行为不能变。
func TestDashboardAggregationBackfillRangeStillBatchesByHourAndAdvancesWatermark(t *testing.T) {
	repo := &selfHealRepoStub{}
	svc := newSelfHealService(repo)
	start := time.Date(2026, 11, 20, 3, 30, 0, 0, time.UTC)
	end := time.Date(2026, 11, 20, 6, 0, 0, 0, time.UTC)

	require.NoError(t, svc.backfillRange(context.Background(), start, end))

	require.Equal(t, []selfHealRange{
		{time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC), time.Date(2026, 11, 20, 4, 0, 0, 0, time.UTC)},
		{time.Date(2026, 11, 20, 4, 0, 0, 0, time.UTC), time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC)},
		{time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC), time.Date(2026, 11, 20, 6, 0, 0, 0, time.UTC)},
	}, repo.calls())
	require.Equal(t, 1, repo.watermarkSets)
	require.Equal(t, end, repo.lastWatermark)
	require.Zero(t, atomic.LoadInt32(&svc.running))

	// running 互斥被占用时回填直接让路。
	atomic.StoreInt32(&svc.running, 1)
	require.ErrorIs(t, svc.backfillRange(context.Background(), start, end), errDashboardAggregationRunning)
	require.Len(t, repo.calls(), 3)
}
