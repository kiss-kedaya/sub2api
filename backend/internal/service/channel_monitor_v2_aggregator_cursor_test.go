//go:build unit

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 审计 AUD-7：保留期清理的门槛读「库里的 backfill_cursor」，而历史回填走的是「进程内存里的游标」。
// 两个都在跑的实例里，先走完的那个让库里游标到了 90 天边缘、清理开始以后，落后的那个如果不跟上库里的游标，
// 就会对已经被清理过的区间继续调 RecomputeRange，把 12h / 1d 汇总重建成残缺值。
//
// cursorSyncRepo 按真实仓储的语义模拟这两件事：RecomputeRange 用 LEAST 推进库里的游标；
// PruneRetention 只有在库里游标已到 90 天边缘时才真的删。

type cursorSyncRepo struct {
	channelMonitorV2RepoStub
	mu sync.Mutex

	cursor  time.Time // 库里的 backfill_cursor
	through time.Time

	pruneTookEffect bool
	ranges          []cursorSyncRange
}

type cursorSyncRange struct {
	start, end           time.Time
	afterPruneTookEffect bool
}

func (r *cursorSyncRepo) GetAggregationWatermark(context.Context) (*ChannelMonitorV2AggregationWatermark, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return &ChannelMonitorV2AggregationWatermark{HasData: true, DataThrough: r.through, BackfillCursor: r.cursor}, nil
}

func (r *cursorSyncRepo) RecomputeLiveRange(_ context.Context, _, end time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.through = end
	return nil
}

func (r *cursorSyncRepo) RecomputeRange(_ context.Context, start, end time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ranges = append(r.ranges, cursorSyncRange{start: start, end: end, afterPruneTookEffect: r.pruneTookEffect})
	if r.cursor.IsZero() || start.Before(r.cursor) {
		r.cursor = start
	}
	return nil
}

func (r *cursorSyncRepo) PruneRetention(_ context.Context, now time.Time) (ChannelMonitorV2PruneResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.cursor.IsZero() && !r.cursor.After(now.Add(-channelMonitorV2RetentionMax)) {
		r.pruneTookEffect = true
		return ChannelMonitorV2PruneResult{Deleted: 1000, Batches: 1}, nil
	}
	return ChannelMonitorV2PruneResult{}, nil
}

func (r *cursorSyncRepo) snapshotRanges() []cursorSyncRange {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]cursorSyncRange(nil), r.ranges...)
}

func (r *cursorSyncRepo) setCursor(c time.Time) {
	r.mu.Lock()
	r.cursor = c
	r.mu.Unlock()
}

// allowBackfillNow 让下一个 tick 通过回填的 15 分钟间隔门槛。
func allowBackfillNow(a *ChannelMonitorV2Aggregator, now time.Time) {
	a.mu.Lock()
	a.lastBackfillAt = now.Add(-time.Hour)
	a.lastPruneAt = time.Time{}
	a.mu.Unlock()
}

func memoryCursor(a *ChannelMonitorV2Aggregator) time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.backfillAt
}

func TestChannelMonitorV2LaggingProcessDoesNotReplayHistoryOnceAPeerFinishedTheWalk(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	repo := &cursorSyncRepo{cursor: now.Add(-30 * 24 * time.Hour), through: now}

	// 进程 B：历史回填走到 30 天前的时候起来的，内存游标 = 库里当时的游标。
	b := NewChannelMonitorV2Aggregator(repo, nil, nil)
	b.runOnce()
	require.Equal(t, now.Add(-30*24*time.Hour), memoryCursor(b), "B 启动时读到库里的游标")

	// 进程 A（另一台机器上的实例）把历史走完：库里游标到 90 天边缘之外。
	repo.setCursor(now.Add(-91 * 24 * time.Hour))

	for i := 0; i < 40; i++ {
		allowBackfillNow(b, now)
		b.runOnce()
	}

	require.True(t, repo.pruneTookEffect, "库里游标已经走完，清理应当生效")
	require.Empty(t, repo.snapshotRanges(), "清理生效以后，落后的 B 不得再对历史区间调 RecomputeRange")
	require.Equal(t, now.Add(-91*24*time.Hour), memoryCursor(b), "B 的内存游标应当跟上库里更早的游标")
}

func TestChannelMonitorV2LaggingProcessContinuesFromTheDatabaseCursorNotItsOwn(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	repo := &cursorSyncRepo{cursor: now.Add(-30 * 24 * time.Hour), through: now}

	b := NewChannelMonitorV2Aggregator(repo, nil, nil)
	b.runOnce()

	// 对端把历史走到 60 天前（还没走完）：B 不应再重走 30～60 天这一段。
	dbCursor := now.Add(-60 * 24 * time.Hour)
	repo.setCursor(dbCursor)

	allowBackfillNow(b, now)
	b.runOnce()

	got := repo.snapshotRanges()
	require.Len(t, got, 1, "还没走完，B 应当继续往更早的历史走一块")
	require.False(t, got[0].end.After(dbCursor), "新一块必须从库里的游标往更早走，而不是从 B 自己的内存游标（%s）往回重走：end=%s", now.Add(-30*24*time.Hour), got[0].end)
	require.True(t, got[0].start.Before(got[0].end))
}

func TestChannelMonitorV2MemoryCursorIgnoresLaterOrEmptyDatabaseCursor(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	mem := now.Add(-30 * 24 * time.Hour)

	for name, dbCursor := range map[string]time.Time{
		"empty (operator cleared it)": {},
		"later than the process own":  now.Add(-10 * 24 * time.Hour),
	} {
		t.Run(name, func(t *testing.T) {
			repo := &cursorSyncRepo{cursor: mem, through: now}
			b := NewChannelMonitorV2Aggregator(repo, nil, nil)
			b.runOnce()
			require.Equal(t, mem, memoryCursor(b))

			repo.setCursor(dbCursor)
			b.runOnce()
			require.Equal(t, mem, memoryCursor(b), "只采纳更早的库游标；空的或更晚的不改内存游标，也不放行任何额外回放")
		})
	}
}
