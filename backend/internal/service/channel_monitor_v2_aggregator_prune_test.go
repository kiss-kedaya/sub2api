package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 渠道监控 v2 聚合器的稳态保留期清理（PruneRetention）的调度测试。
//
// 历史回填走完以后稳态每分钟只调 RecomputeLiveRange，保留期清理只存在于 RecomputeRange 里，
// 于是 1m 事实表和汇总表一直涨过保留期。清理语句本身（过期的删、没过期的不动、分批有界、
// 失败不影响下一次）在 repository/channel_monitor_v2_prune_integration_test.go 里用真实 PostgreSQL 验证。

type cmv2PruneRepo struct {
	channelMonitorV2RepoStub
	mu sync.Mutex

	wm       ChannelMonitorV2AggregationWatermark
	pruneErr error
	pruneRes ChannelMonitorV2PruneResult
	delay    time.Duration // 每次 live/prune 调用占用的时间，让并发的机会更大

	live, backfill, prunes int
	pruneNows              []time.Time
	events                 []string
	liveUnderLock          []bool
	pruneUnderLock         []bool
	inflight, maxInflight  int

	lockCache *fakeLeaderLockCache // 非 nil 时记录调用发生时 leader 锁是不是被调用方持有
	owner     func() string
}

func (r *cmv2PruneRepo) enter(event string) (heldBy string) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.inflight++
	if r.inflight > r.maxInflight {
		r.maxInflight = r.inflight
	}
	r.mu.Unlock()
	if r.lockCache != nil {
		r.lockCache.mu.Lock()
		heldBy = r.lockCache.owners[channelMonitorV2AggregatorLockKey]
		r.lockCache.mu.Unlock()
	}
	if r.delay > 0 {
		time.Sleep(r.delay)
	}
	return heldBy
}

func (r *cmv2PruneRepo) leave() {
	r.mu.Lock()
	r.inflight--
	r.mu.Unlock()
}

func (r *cmv2PruneRepo) RecomputeLiveRange(context.Context, time.Time, time.Time) error {
	heldBy := r.enter("live")
	defer r.leave()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.live++
	if r.owner != nil {
		r.liveUnderLock = append(r.liveUnderLock, heldBy == r.owner())
	}
	return nil
}

func (r *cmv2PruneRepo) RecomputeRange(context.Context, time.Time, time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backfill++
	r.events = append(r.events, "backfill")
	return nil
}

func (r *cmv2PruneRepo) GetAggregationWatermark(context.Context) (*ChannelMonitorV2AggregationWatermark, error) {
	wm := r.wm
	return &wm, nil
}

func (r *cmv2PruneRepo) PruneRetention(_ context.Context, now time.Time) (ChannelMonitorV2PruneResult, error) {
	heldBy := r.enter("prune")
	defer r.leave()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prunes++
	r.pruneNows = append(r.pruneNows, now)
	if r.owner != nil {
		r.pruneUnderLock = append(r.pruneUnderLock, heldBy == r.owner())
	}
	return r.pruneRes, r.pruneErr
}

func cmv2SteadyStateWatermark(now time.Time) ChannelMonitorV2AggregationWatermark {
	// 历史回填已经走完：游标在 90 天的边缘之外。
	return ChannelMonitorV2AggregationWatermark{HasData: true, DataThrough: now, BackfillCursor: now.Add(-91 * 24 * time.Hour)}
}

func TestChannelMonitorV2PruneIntervalIsThirtyMinutes(t *testing.T) {
	require.Equal(t, 30*time.Minute, channelMonitorV2PruneInterval)
}

func TestChannelMonitorV2SteadyStatePrunesAtMostOncePerInterval(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	repo := &cmv2PruneRepo{wm: cmv2SteadyStateWatermark(now)}
	agg := NewChannelMonitorV2Aggregator(repo, nil, nil)

	for i := 0; i < 30; i++ {
		agg.runOnce()
	}
	require.Equal(t, 30, repo.live, "实时窗口每个 tick 都刷新")
	require.Equal(t, 1, repo.prunes, "稳态 30 个 tick：清理只在第一个 tick 跑一次，之后被 30 分钟节流挡住")
	require.Zero(t, repo.backfill)
	require.Len(t, repo.pruneNows, 1)
	require.WithinDuration(t, now, repo.pruneNows[0], 2*time.Minute)

	// 同一个进程里再按时间推进：29 分钟内不跑，满 30 分钟再跑一次。
	first := repo.pruneNows[0]
	ctx := context.Background()
	agg.maybePruneRetention(ctx, first.Add(29*time.Minute), 0)
	require.Equal(t, 1, repo.prunes)
	agg.maybePruneRetention(ctx, first.Add(30*time.Minute), 0)
	require.Equal(t, 2, repo.prunes)
	require.Equal(t, first.Add(30*time.Minute), repo.pruneNows[1])
	agg.maybePruneRetention(ctx, first.Add(59*time.Minute), 0)
	require.Equal(t, 2, repo.prunes)
}

func TestChannelMonitorV2PruneFailureIsLoggedAndDoesNotDisturbAggregation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	repo := &cmv2PruneRepo{
		wm:       cmv2SteadyStateWatermark(now),
		pruneErr: errors.New("prune channel_monitor_v2_metrics_1m: statement timeout"),
		pruneRes: ChannelMonitorV2PruneResult{Deleted: 40000, Batches: 2, Backlog: true},
	}
	agg := NewChannelMonitorV2Aggregator(repo, nil, nil)

	for i := 0; i < 5; i++ {
		agg.runOnce()
	}
	require.Equal(t, 5, repo.live, "清理失败不影响每分钟的聚合")
	require.Equal(t, 1, repo.prunes, "失败也算一次尝试：下个周期再来，不在每个 tick 上撞数据库")
	require.Zero(t, agg.nextWaitFloor, "清理失败不触发聚合的退避")
	require.Zero(t, agg.backfillFailures)

	// 下个周期（满 30 分钟）再试，这次成功。
	repo.mu.Lock()
	repo.pruneErr = nil
	repo.mu.Unlock()
	agg.maybePruneRetention(context.Background(), repo.pruneNows[0].Add(30*time.Minute), 0)
	require.Equal(t, 2, repo.prunes)
}

func TestChannelMonitorV2PruneYieldsToABusyDatabase(t *testing.T) {
	repo := &cmv2PruneRepo{}
	agg := NewChannelMonitorV2Aggregator(repo, nil, nil)
	now := time.Now().UTC().Truncate(time.Minute)

	agg.maybePruneRetention(context.Background(), now, channelMonitorV2BusyOverlap+time.Second)
	require.Zero(t, repo.prunes, "实时窗口刚才很慢：数据库忙，清理让路")
	require.True(t, agg.lastPruneAt.IsZero(), "让路不记时间戳，下一轮再看")

	agg.maybePruneRetention(context.Background(), now, time.Second)
	require.Equal(t, 1, repo.prunes)
}

func TestChannelMonitorV2PruneRunsAfterLiveWindowAndBeforeBackfillGate(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	repo := &cmv2PruneRepo{wm: ChannelMonitorV2AggregationWatermark{HasData: true, DataThrough: now, BackfillCursor: now.Add(-3 * time.Hour)}}
	agg := NewChannelMonitorV2Aggregator(repo, nil, nil)
	agg.lastBackfillAt = now.Add(-time.Hour) // 历史块之间的 15 分钟间隔已经过了

	agg.runOnce()

	require.Equal(t, []string{"live", "prune", "backfill"}, repo.events, "先刷新实时窗口，再清理，清理不拦着历史回填")
	require.Equal(t, 1, repo.backfill)
}

func TestChannelMonitorV2PruneOnlyRunsUnderTheLeaderLock(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cache := &fakeLeaderLockCache{}
	repo := &cmv2PruneRepo{wm: cmv2SteadyStateWatermark(now), lockCache: cache}
	agg := NewChannelMonitorV2Aggregator(repo, nil, nil)
	agg.SetLeaderLock(cache, nil)
	repo.owner = func() string { return agg.instanceID }

	// 别的实例是 leader：实时窗口和清理都不跑。
	ok, err := cache.TryAcquireLeaderLock(context.Background(), channelMonitorV2AggregatorLockKey, "peer", time.Hour)
	require.NoError(t, err)
	require.True(t, ok)
	agg.runOnce()
	require.Zero(t, repo.live)
	require.Zero(t, repo.prunes)

	// 自己拿到锁：清理和实时窗口都在持锁期间调用，跑完释放。
	require.NoError(t, cache.ReleaseLeaderLock(context.Background(), channelMonitorV2AggregatorLockKey, "peer"))
	agg.runOnce()
	require.Equal(t, 1, repo.live)
	require.Equal(t, 1, repo.prunes)
	require.Equal(t, []bool{true}, repo.liveUnderLock)
	require.Equal(t, []bool{true}, repo.pruneUnderLock, "清理必须在持有 leader 锁时调用")
	ok, err = cache.TryAcquireLeaderLock(context.Background(), channelMonitorV2AggregatorLockKey, "next", time.Hour)
	require.NoError(t, err)
	require.True(t, ok, "跑完释放锁")
}

func TestChannelMonitorV2PruneNeverOverlapsLiveWindowAcrossInstances(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cache := &fakeLeaderLockCache{}
	repo := &cmv2PruneRepo{wm: cmv2SteadyStateWatermark(now), delay: time.Millisecond}
	aggs := make([]*ChannelMonitorV2Aggregator, 2)
	for i := range aggs {
		aggs[i] = NewChannelMonitorV2Aggregator(repo, nil, nil)
		aggs[i].SetLeaderLock(cache, nil)
	}

	var wg sync.WaitGroup
	for _, agg := range aggs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				agg.runOnce()
				// 让每个实例都有机会满足 30 分钟间隔：把它的节流时间往回拨。
				agg.mu.Lock()
				agg.lastPruneAt = time.Time{}
				agg.mu.Unlock()
			}
		}()
	}
	wg.Wait()

	require.Positive(t, repo.live)
	require.Positive(t, repo.prunes)
	require.Equal(t, 1, repo.maxInflight, "任何时刻只有一个实例的一个调用在写汇总：清理不与实时窗口刷新并发")
}
