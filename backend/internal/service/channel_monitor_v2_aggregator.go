package service

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
)

const (
	channelMonitorV2AggregatorLockKey = "channel-monitor-v2-aggregator"
	// Retention walks back to the longest stored tier (1d rollup = 90d). Per-tier
	// prune in the repository drops short-lived 1m/user/hist facts earlier.
	channelMonitorV2RetentionMax = 90 * 24 * time.Hour
	// First tick after upgrade prioritizes the default 90m view (with small padding).
	channelMonitorV2BootstrapFirst = 2 * time.Hour
	// Always refresh a small trailing window so late writes land without
	// re-aggregating large history every tick.
	//
	// 注意：这个窗口只覆盖「上一条 tick 到这一条 tick 之间」。任何一次 tick 被跳过
	// （抢不到 leader 锁、运行超过 RunTimeout、进程重启、GC 停顿），中间那几分钟就
	// 落在固定窗口之外，**永远不会被重算**，于是 1m 事实表出现空洞、前端显示
	// 「样本不足」。线上实测就出现过 18:17–18:47、18:50–19:07 这种整段缺失，
	// 而 usage_logs 在那几分钟里每分钟有一千多条记录（源数据是完整的）。
	//
	// 因此真正的兜底不是把这个常量调大，而是让窗口起点取
	// min(now-RecentOverlap, 上次成功写入的 data_through)，见 run() 里的
	// liveStart。这样缺口会被下一次成功运行自动补上，而稳态开销不变。
	channelMonitorV2RecentOverlap = 2 * time.Minute
	// 追赶上限：长时间停机后不要一次重算出一整天的窗口（那会把一次 tick 拖到
	// 远超 RunTimeout，反而把缺口撕得更大）。超出部分交给历史回填慢慢走。
	channelMonitorV2LiveMaxCatchUp = 6 * time.Hour
	// Overlap/error SQL can exceed the 30s Postgres statement_timeout during
	// storms; keep the Go deadline above SET LOCAL 180s in RecomputeRange.
	channelMonitorV2RunTimeout = 3 * time.Minute
	// Crash-safety TTL must outlive the worst-case run so the lock cannot
	// expire while RecomputeRange is still holding the transaction.
	channelMonitorV2LockTTL = 4 * time.Minute

	// Gentle backfill: small adaptive chunks, never default 24h hammering.
	// Initial historical chunk after the 2h seed.
	channelMonitorV2BackfillChunkInit = time.Hour
	channelMonitorV2MinBackfillChunk  = 15 * time.Minute
	// Depth-based ceilings (product phases 90m → 1d → 7d → 30d → 90d).
	channelMonitorV2MaxChunkNear1d = 2 * time.Hour
	channelMonitorV2MaxChunkNear7d = 4 * time.Hour
	channelMonitorV2MaxChunkFar    = 6 * time.Hour

	// Soft adaptive timing: grow only when a recompute is clearly cheap.
	channelMonitorV2GrowChunkUnder = 15 * time.Second
	channelMonitorV2MaxBackoff     = 10 * time.Minute
	// Skip 30d/90d history on the same tick when the live 90m page was already
	// expensive. Kedaya's default view is 90m; historical cards can wait.
	channelMonitorV2BusyOverlap    = 8 * time.Second
	channelMonitorV2BackfillMinGap = 15 * time.Minute
)

// channelMonitorRuntimeSubscriber is the optional settings hook that lets the
// aggregator wake immediately when channel_monitor_enabled / mode flips.
type channelMonitorRuntimeSubscriber interface {
	SubscribeChannelMonitorRuntime(listener func()) (unsubscribe func())
}

type ChannelMonitorV2Aggregator struct {
	repo       ChannelMonitorV2Repository
	db         *sql.DB
	lockCache  LeaderLockCache
	settings   channelMonitorRuntimeReader
	instanceID string
	stopCh     chan struct{}
	// kickCh wakes the loop early after a settings change (buffered 1).
	kickCh    chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once
	mu        sync.Mutex
	// backfillAt is the earliest minute already recomputed (mirrors DB cursor).
	// Zero means "not yet loaded from durable watermark this process".
	backfillAt       time.Time
	backfillChunk    time.Duration
	backfillFailures int
	// liveThrough 是持久化水位（watermarks.data_through）的内存镜像，即「已经
	// 成功聚合到哪一分钟」。实时窗口用它兜住被跳过的 tick：只要它落后于
	// now-RecentOverlap，窗口就从它开始重算，把空洞补回来。
	liveThrough time.Time
	// lastBackfillAt is the last time this process attempted a historical chunk.
	// Live 90m ticks do not update it.
	lastBackfillAt time.Time
	// nextWaitFloor is applied after runOnce when failures require backoff.
	nextWaitFloor time.Duration
	// cursorLoaded is true after the first successful watermark read (or init).
	cursorLoaded bool
	// hasAggregated is true once any recompute in this process (or durable data) exists.
	hasAggregated bool
	unsub         func()
	ctx           context.Context
	cancel        context.CancelFunc
}

func NewChannelMonitorV2Aggregator(repo ChannelMonitorV2Repository, db *sql.DB, settings channelMonitorRuntimeReader) *ChannelMonitorV2Aggregator {
	return &ChannelMonitorV2Aggregator{
		repo:          repo,
		db:            db,
		settings:      settings,
		instanceID:    uuid.NewString(),
		stopCh:        make(chan struct{}),
		kickCh:        make(chan struct{}, 1),
		backfillChunk: channelMonitorV2BackfillChunkInit,
	}
}

// SetLeaderLock injects the Redis leader-lock cache (and optional DB fallback)
// so only one gateway process aggregates channel-monitor facts each tick.
func (s *ChannelMonitorV2Aggregator) SetLeaderLock(lockCache LeaderLockCache, db *sql.DB) {
	if s == nil {
		return
	}
	s.lockCache = lockCache
	if db != nil {
		s.db = db
	}
}

func (s *ChannelMonitorV2Aggregator) Start() {
	if s == nil || s.repo == nil {
		return
	}
	s.startOnce.Do(func() {
		s.mu.Lock()
		s.ctx, s.cancel = context.WithCancel(context.Background())
		s.mu.Unlock()
		if sub, ok := s.settings.(channelMonitorRuntimeSubscriber); ok && sub != nil {
			unsub := sub.SubscribeChannelMonitorRuntime(func() {
				s.kick()
			})
			s.mu.Lock()
			stopped := s.ctx == nil
			if !stopped {
				select {
				case <-s.ctx.Done():
					stopped = true
				default:
				}
			}
			if !stopped {
				s.unsub = unsub
			}
			s.mu.Unlock()
			if stopped && unsub != nil {
				unsub()
			}
		}
		go s.loop()
	})
}

func (s *ChannelMonitorV2Aggregator) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		s.mu.Lock()
		cancel := s.cancel
		unsub := s.unsub
		s.cancel = nil
		s.unsub = nil
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if unsub != nil {
			unsub()
		}
		close(s.stopCh)
	})
}

// kick wakes the aggregation loop so mode flips take effect without waiting
// for the next refresh interval.
func (s *ChannelMonitorV2Aggregator) kick() {
	if s == nil {
		return
	}
	select {
	case s.kickCh <- struct{}{}:
	default:
	}
}

func (s *ChannelMonitorV2Aggregator) loop() {
	for {
		interval := time.Minute
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if !s.passiveAggregationAllowed(ctx) {
			cancel()
			if !s.wait(interval) {
				return
			}
			continue
		}
		if cfg, err := s.repo.GetConfig(ctx); err == nil {
			if !cfg.Enabled {
				cancel()
				if !s.wait(interval) {
					return
				}
				continue
			}
			if cfg.RefreshIntervalSeconds > 0 {
				interval = time.Duration(cfg.RefreshIntervalSeconds) * time.Second
			}
		}
		cancel()
		s.runOnce()
		// Hard gate: never compress bootstrap to multi-Hz ticks. Soft gate: on
		// repeated failures raise the wait floor (exponential backoff).
		s.mu.Lock()
		floor := s.nextWaitFloor
		s.mu.Unlock()
		if floor > interval {
			interval = floor
		}
		if !s.wait(interval) {
			return
		}
	}
}

func (s *ChannelMonitorV2Aggregator) passiveAggregationAllowed(ctx context.Context) bool {
	if s == nil || s.settings == nil {
		// Fail closed without settings: do not aggregate under ambiguous mode.
		return false
	}
	return s.settings.GetChannelMonitorRuntime(ctx).PassiveAggregationAllowed()
}

func (s *ChannelMonitorV2Aggregator) wait(interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-s.kickCh:
		// Drain any coalesced kicks so a burst of settings writes only wakes once.
		for {
			select {
			case <-s.kickCh:
			default:
				return true
			}
		}
	case <-s.stopCh:
		return false
	}
}

func (s *ChannelMonitorV2Aggregator) runOnce() {
	s.mu.Lock()
	parent := s.ctx
	s.mu.Unlock()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, channelMonitorV2RunTimeout)
	defer cancel()
	release, acquired := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, channelMonitorV2AggregatorLockKey, s.instanceID, channelMonitorV2LockTTL)
	if !acquired {
		return
	}
	if release != nil {
		defer release()
	}

	now := time.Now().UTC().Truncate(time.Minute)
	if err := s.ensureCursor(ctx, now); err != nil {
		logger.LegacyPrintf("service.channel_monitor_v2", "[ChannelMonitorV2] load watermark failed: %v", err)
		return
	}

	// 每个 tick 都重新读一次持久化水位。它是「已经聚合到哪一分钟」的唯一真相，
	// 而下面用它兜住被跳过的 tick（liveStart）。
	//
	// 只在进程启动时读一次（ensureCursor 的 cursorLoaded 短路）会有一个要命的后果：
	// 运维想把 data_through 往回拨来回填历史空洞时，**必须重启进程才生效**。而生产
	// 要求 24h 不停机、只接受滚动上线，重启是被严格限制的操作——不该为了补数据而重启。
	// 一次单行读的开销相对每分钟一次聚合可以忽略。
	if wm, wmErr := s.repo.GetAggregationWatermark(ctx); wmErr == nil && wm != nil && !wm.DataThrough.IsZero() {
		s.mu.Lock()
		s.liveThrough = wm.DataThrough.UTC().Truncate(time.Minute)
		s.mu.Unlock()
	}

	s.mu.Lock()
	cursor := s.backfillAt
	hasData := s.hasAggregated
	s.mu.Unlock()

	// Phase 1 (first upgrade / empty): seed the default 90m UI window quickly.
	if !hasData || cursor.IsZero() {
		start := now.Add(-channelMonitorV2BootstrapFirst)
		started := time.Now()
		if err := s.repo.RecomputeRange(ctx, start, now); err != nil {
			logger.LegacyPrintf("service.channel_monitor_v2", "[ChannelMonitorV2] bootstrap recent aggregation failed: %v", err)
			s.recordBackfillFailure(now, cursor)
			return
		}
		s.recordBackfillSuccess(start, time.Since(started), now)
		return
	}

	// Always refresh the trailing overlap so late usage/error writes land in 1m facts.
	//
	// 起点还要兜住被跳过的 tick：只要持久化水位落后于 now-RecentOverlap，说明中间
	// 那几分钟从没被重算过，就从水位开始补（上限 LiveMaxCatchUp，避免长时间停机后
	// 一次 tick 被拖到远超 RunTimeout）。稳态下水位每 tick 都等于 now，这一步是空操作。
	liveStart := now.Add(-channelMonitorV2RecentOverlap)
	s.mu.Lock()
	if !s.liveThrough.IsZero() && s.liveThrough.Before(liveStart) {
		liveStart = s.liveThrough
	}
	s.mu.Unlock()
	if floor := now.Add(-channelMonitorV2LiveMaxCatchUp); liveStart.Before(floor) {
		liveStart = floor
	}
	startedLive := time.Now()
	if err := s.repo.RecomputeLiveRange(ctx, liveStart, now); err != nil {
		logger.LegacyPrintf("service.channel_monitor_v2", "[ChannelMonitorV2] overlap aggregation failed: %v", err)
		return
	}
	s.mu.Lock()
	// 成功后推进内存水位，下一次 tick 只需覆盖 now-RecentOverlap。
	s.liveThrough = now
	lastBackfill := s.lastBackfillAt
	s.mu.Unlock()
	if !channelMonitorV2AllowBackfill(time.Since(startedLive), lastBackfill, now) {
		if lastBackfill.IsZero() {
			s.mu.Lock()
			if s.lastBackfillAt.IsZero() {
				s.lastBackfillAt = now
			}
			s.mu.Unlock()
		}
		return
	}

	// Phase 2: walk history backward at most one chunk per tick until retention max (90d).
	// Product UI (30d) fills first; remaining 30–90d continues silently.
	retentionCutoff := now.Add(-channelMonitorV2RetentionMax)
	if !cursor.After(retentionCutoff) {
		return
	}
	end := cursor
	s.mu.Lock()
	chunk := s.backfillChunk
	s.mu.Unlock()
	if chunk <= 0 {
		chunk = channelMonitorV2BackfillChunkInit
	}
	maxChunk := channelMonitorV2MaxChunkForDepth(now, end)
	if chunk > maxChunk {
		chunk = maxChunk
	}
	if chunk < channelMonitorV2MinBackfillChunk {
		chunk = channelMonitorV2MinBackfillChunk
	}
	start := end.Add(-chunk)
	// Once bootstrap reaches historical data, keep chunks on day boundaries so
	// daily rollups never depend on 1m rows from two independently pruned chunks.
	if end.Before(now.Add(-7 * 24 * time.Hour)) {
		aligned := end.Add(-chunk).Truncate(24 * time.Hour)
		if aligned.Before(end) {
			start = aligned
		}
	}
	if start.Before(retentionCutoff) {
		start = retentionCutoff
	}
	if !start.Before(end) {
		return
	}
	started := time.Now()
	if err := s.repo.RecomputeRange(ctx, start, end); err != nil {
		logger.LegacyPrintf("service.channel_monitor_v2", "[ChannelMonitorV2] backfill failed %s..%s: %v", start, end, err)
		s.recordBackfillFailure(now, end)
		return
	}
	s.recordBackfillSuccess(start, time.Since(started), now)
}

// channelMonitorV2MaxChunkForDepth returns the hard ceiling for a historical
// chunk ending at `end` (earliest already covered / next walk end).
func channelMonitorV2MaxChunkForDepth(now, end time.Time) time.Duration {
	age := now.Sub(end)
	switch {
	case age < 24*time.Hour:
		return channelMonitorV2MaxChunkNear1d
	case age < 7*24*time.Hour:
		return channelMonitorV2MaxChunkNear7d
	default:
		return channelMonitorV2MaxChunkFar
	}
}

// channelMonitorV2AllowBackfill gates 30d/90d history behind a cheap live tick.
// The default channel-status page is 90m; history can wait when Postgres is busy.
func channelMonitorV2AllowBackfill(overlapElapsed time.Duration, lastBackfill, now time.Time) bool {
	if overlapElapsed > channelMonitorV2BusyOverlap {
		return false
	}
	if lastBackfill.IsZero() {
		return false
	}
	if now.Sub(lastBackfill) < channelMonitorV2BackfillMinGap {
		return false
	}
	return true
}

func (s *ChannelMonitorV2Aggregator) recordBackfillSuccess(coveredFrom time.Time, elapsed time.Duration, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backfillAt = coveredFrom
	s.hasAggregated = true
	s.backfillFailures = 0
	s.nextWaitFloor = 0
	s.lastBackfillAt = now
	maxChunk := channelMonitorV2MaxChunkForDepth(now, coveredFrom)
	// Grow slowly only when the recompute was clearly cheap.
	if elapsed > 0 && elapsed < channelMonitorV2GrowChunkUnder {
		next := s.backfillChunk
		if next <= 0 {
			next = channelMonitorV2BackfillChunkInit
		}
		next = time.Duration(float64(next) * 1.5)
		if next > maxChunk {
			next = maxChunk
		}
		if next < channelMonitorV2MinBackfillChunk {
			next = channelMonitorV2MinBackfillChunk
		}
		s.backfillChunk = next
		return
	}
	// Keep a healthy chunk within the depth ceiling after success.
	if s.backfillChunk <= 0 || s.backfillChunk > maxChunk {
		s.backfillChunk = channelMonitorV2BackfillChunkInit
		if s.backfillChunk > maxChunk {
			s.backfillChunk = maxChunk
		}
	}
}

func (s *ChannelMonitorV2Aggregator) recordBackfillFailure(now, end time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backfillFailures++
	// Shrink chunk toward the minimum so large DBs self-throttle.
	if s.backfillChunk <= 0 {
		s.backfillChunk = channelMonitorV2BackfillChunkInit
	}
	s.backfillChunk /= 2
	if s.backfillChunk < channelMonitorV2MinBackfillChunk {
		s.backfillChunk = channelMonitorV2MinBackfillChunk
	}
	maxChunk := channelMonitorV2MaxChunkForDepth(now, end)
	if s.backfillChunk > maxChunk {
		s.backfillChunk = maxChunk
	}
	// Exponential backoff on wait floor: 1m, 2m, 4m… capped at 10m.
	floor := time.Minute << uint(s.backfillFailures-1)
	if floor > channelMonitorV2MaxBackoff {
		floor = channelMonitorV2MaxBackoff
	}
	if floor < time.Minute {
		floor = time.Minute
	}
	s.nextWaitFloor = floor
}

// ensureCursor restores durable backfill_cursor after process restart so progress
// and historical walk continue instead of re-seeding only the last 2h.
func (s *ChannelMonitorV2Aggregator) ensureCursor(ctx context.Context, now time.Time) error {
	s.mu.Lock()
	loaded := s.cursorLoaded
	s.mu.Unlock()
	if loaded {
		return nil
	}
	wm, err := s.repo.GetAggregationWatermark(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cursorLoaded {
		return nil
	}
	if wm != nil {
		if !wm.BackfillCursor.IsZero() {
			s.backfillAt = wm.BackfillCursor.UTC().Truncate(time.Minute)
		}
		if wm.HasData || !wm.DataThrough.IsZero() {
			s.hasAggregated = true
			// 持久化水位改成内存镜像，供实时窗口兜住被跳过的 tick。
			if !wm.DataThrough.IsZero() {
				s.liveThrough = wm.DataThrough.UTC().Truncate(time.Minute)
			}
			// Legacy rows may have data_through but null backfill_cursor (older workers).
			// Infer cursor from data_through − initial window so we do not re-bootstrap
			// only 2h and claim zero progress forever.
			if s.backfillAt.IsZero() && !wm.DataThrough.IsZero() {
				inferred := wm.DataThrough.UTC().Truncate(time.Minute).Add(-channelMonitorV2BootstrapFirst)
				if inferred.After(now) {
					inferred = now.Add(-channelMonitorV2BootstrapFirst)
				}
				s.backfillAt = inferred
			}
		}
	}
	s.cursorLoaded = true
	return nil
}
