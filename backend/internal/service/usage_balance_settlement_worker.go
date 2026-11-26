package service

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const (
	usageBalanceSettlementPollInterval = 250 * time.Millisecond
	usageBalanceSettlementBatchSize    = 2000
	usageBalanceSettlementRetention    = 15 * time.Minute
	// 余额合并结算开启后，队列稳态行数 ≈ 扣费速率 × retention（默认 15 分钟）。
	// 清理必须能跟上写入速率，否则表会无界增长：这里按 30s 一轮、每轮 5 万行，
	// 即约 1600 行/秒的清理吞吐，覆盖千级 RPS 的扣费速率。
	usageBalanceSettlementCleanupEvery = 30 * time.Second
	usageBalanceSettlementCleanupBatch = 50000
)

// usageBalanceSettlementConsumerRunning 表示本进程是否有一个正在排空
// billing_balance_settlements 的 worker。
//
// 计费仓储只有在它为 true 时才把余额扣费写入持久化队列（合并结算）；否则退回同步
// 扣费。这样即使配置误开（例如测试模式或 SUB2API_DISABLE_BACKGROUND_JOBS 关闭了
// 后台任务），也不会有扣费被留在队列里无人结算。
var usageBalanceSettlementConsumerRunning atomic.Bool

// UsageBalanceSettlementConsumerRunning 报告本进程是否有余额结算消费者在运行。
func UsageBalanceSettlementConsumerRunning() bool {
	return usageBalanceSettlementConsumerRunning.Load()
}

// UsageBalanceSettlementWorkerStats 是结算 worker 的累计计数，用于灰度期观测。
type UsageBalanceSettlementWorkerStats struct {
	// Flushes 成功提交的结算事务数（每个事务按用户合并一次余额更新）。
	Flushes uint64
	// Events 已结算的扣费事件数（billing_balance_settlements 行数）。
	Events uint64
	// Users 已更新的用户余额行数（合并后）。
	Users uint64
	// Failures 失败的结算事务数。
	Failures uint64
}

// UsageBalanceSettlementWorker drains compact billing events. Every process may
// run a worker; the repository uses a PostgreSQL advisory transaction lock so
// exactly one instance coalesces a batch at a time.
//
// With billing.balance_merge_settlement_enabled on, the same worker also watches
// the queue (depth / oldest age), flips this process's new charges back to the
// synchronous path while the backlog is over its limits, and hands every
// committed flush to the low-balance observer. With the switch off none of that
// runs and the worker behaves exactly as before.
type UsageBalanceSettlementWorker struct {
	repo UsageBalanceSettlementRepository
	opts UsageBalanceSettlementWorkerOptions

	// backlogReader is non-nil only when merge settlement is enabled and the
	// repository can read the queue; it gates the monitor goroutine.
	backlogReader UsageBalanceSettlementBacklogReader
	// observeCh is non-nil only when merge settlement is enabled and an observer
	// is configured; it gates the observer goroutine and the request-path
	// low-balance suppression.
	observeCh chan []UsageBalanceSettlementResult

	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once

	flushes  atomic.Uint64
	events   atomic.Uint64
	users    atomic.Uint64
	failures atomic.Uint64

	loggedFlushes  atomic.Uint64
	loggedEvents   atomic.Uint64
	loggedFailures atomic.Uint64

	lastBacklog         atomic.Pointer[usageBalanceSettlementBacklogSnapshot]
	fallbackSince       atomic.Int64
	fallbackTransitions atomic.Uint64
	sampleFailures      atomic.Uint64
	observerDropped     atomic.Uint64
	lastBacklogLog      atomic.Int64
	lastSampleErrLog    atomic.Int64
	lastObserveDropLog  atomic.Int64

	zapLog *zap.Logger // test hook; nil means the process logger
}

// logr is the logger for the guard / observer / stats lines.
func (w *UsageBalanceSettlementWorker) logr() *zap.Logger {
	if w.zapLog != nil {
		return w.zapLog
	}
	return logger.L()
}

func NewUsageBalanceSettlementWorker(
	repo UsageBalanceSettlementRepository,
) *UsageBalanceSettlementWorker {
	return NewUsageBalanceSettlementWorkerWithOptions(repo, UsageBalanceSettlementWorkerOptions{})
}

// NewUsageBalanceSettlementWorkerWithOptions builds the worker with the merge
// settlement guard configured. The zero options value is the legacy worker.
func NewUsageBalanceSettlementWorkerWithOptions(
	repo UsageBalanceSettlementRepository,
	opts UsageBalanceSettlementWorkerOptions,
) *UsageBalanceSettlementWorker {
	worker := &UsageBalanceSettlementWorker{repo: repo, opts: opts}
	if opts.MergeEnabled {
		if reader, ok := repo.(UsageBalanceSettlementBacklogReader); ok {
			worker.backlogReader = reader
		}
		if opts.Observer != nil {
			worker.observeCh = make(chan []UsageBalanceSettlementResult, usageBalanceSettlementObserveBuffer)
		}
	}
	startBackgroundJob(worker.Start)
	return worker
}

func (w *UsageBalanceSettlementWorker) Start() {
	if BackgroundJobsDisabled() {
		return
	}
	if w == nil || w.repo == nil || w.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.wg.Add(1)
	go w.run(ctx)
	if w.backlogReader != nil {
		w.wg.Add(1)
		go w.runBacklogMonitor(ctx)
	}
	if w.observeCh != nil {
		w.wg.Add(1)
		go w.runObserver(ctx)
		usageBalanceSettlementNotifierActive.Store(true)
	}
	usageBalanceSettlementConsumerRunning.Store(true)
}

func (w *UsageBalanceSettlementWorker) Stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() {
		if w.cancel != nil {
			w.cancel()
		}
		w.wg.Wait()
		usageBalanceSettlementConsumerRunning.Store(false)
		// 没有 worker 时没有积压读数可言：清掉退回状态与提醒标志，避免陈旧状态影响
		// 之后重新启动的 worker（或同进程里的其他测试）。
		usageBalanceSettlementBacklogFallback.Store(false)
		usageBalanceSettlementNotifierActive.Store(false)
	})
}

// Stats 返回累计计数。灰度期用它判断合并结算是否真的在跑、以及吞吐是否符合预期。
func (w *UsageBalanceSettlementWorker) Stats() UsageBalanceSettlementWorkerStats {
	if w == nil {
		return UsageBalanceSettlementWorkerStats{}
	}
	return UsageBalanceSettlementWorkerStats{
		Flushes:  w.flushes.Load(),
		Events:   w.events.Load(),
		Users:    w.users.Load(),
		Failures: w.failures.Load(),
	}
}

func (w *UsageBalanceSettlementWorker) run(ctx context.Context) {
	defer w.wg.Done()
	pollTicker := time.NewTicker(usageBalanceSettlementPollInterval)
	cleanupTicker := time.NewTicker(usageBalanceSettlementCleanupEvery)
	defer pollTicker.Stop()
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-pollTicker.C:
			w.flush(ctx)
		case <-cleanupTicker.C:
			w.cleanup(ctx)
		}
	}
}

func (w *UsageBalanceSettlementWorker) flush(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	results, err := w.repo.FlushPendingBalanceSettlements(ctx, usageBalanceSettlementBatchSize)
	if err != nil {
		w.failures.Add(1)
		logger.L().Warn("billing.balance_settlement_flush_failed", zap.Error(err))
		return
	}
	if len(results) == 0 {
		return
	}
	var events uint64
	for i := range results {
		events += uint64(results[i].EventCount)
	}
	w.flushes.Add(1)
	w.events.Add(events)
	w.users.Add(uint64(len(results)))
	w.observe(results)
}

func (w *UsageBalanceSettlementWorker) cleanup(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	_, err := w.repo.DeleteAppliedBalanceSettlements(
		ctx,
		time.Now().Add(-usageBalanceSettlementRetention),
		usageBalanceSettlementCleanupBatch,
	)
	if err != nil {
		logger.L().Warn("billing.balance_settlement_cleanup_failed", zap.Error(err))
	}
	w.logStats()
}

// logStats 每 30 秒输出一次增量统计，让灰度期可以从日志判断合并结算的合并率
// （events / flushes）与失败情况，而不用把计数暴露成新的指标端点。
//
// 开启合并结算时，同一行日志还带上最近一次积压采样（队列深度 / 最老一笔等待时间 /
// 是否已退回同步扣费）。队列里还有积压或已退回时，即使这 30 秒没有成功的结算也会输出，
// 这正是「worker 停了」时唯一能在日志里看到的信号。
func (w *UsageBalanceSettlementWorker) logStats() {
	flushes := w.flushes.Load()
	events := w.events.Load()
	failures := w.failures.Load()
	prevFlushes := w.loggedFlushes.Swap(flushes)
	prevEvents := w.loggedEvents.Swap(events)
	prevFailures := w.loggedFailures.Swap(failures)
	snapshot := w.lastBacklog.Load()
	fallback := UsageBalanceSettlementBacklogFallbackActive()
	backlogQuiet := snapshot == nil || (snapshot.backlog.Pending == 0 && !fallback)
	if flushes == prevFlushes && failures == prevFailures && backlogQuiet {
		return
	}
	// 合并率 = 每轮结算事务平均合并的扣费事件数；1.0 表示完全没有合并。
	ratio := 0.0
	if flushes > prevFlushes {
		ratio = float64(events-prevEvents) / float64(flushes-prevFlushes)
	}
	fields := []zap.Field{
		zap.Uint64("flushes", flushes-prevFlushes),
		zap.Uint64("events", events-prevEvents),
		zap.Uint64("failures", failures-prevFailures),
		zap.Float64("events_per_flush", ratio),
		zap.Uint64("total_flushes", flushes),
		zap.Uint64("total_events", events),
	}
	if snapshot != nil {
		fields = append(fields,
			zap.Int64("backlog_depth", snapshot.backlog.Pending),
			zap.Bool("backlog_depth_capped", snapshot.backlog.PendingCapped),
			zap.Int64("oldest_age_ms", snapshot.backlog.OldestAge.Milliseconds()),
			zap.Bool("sync_fallback", fallback),
		)
	}
	w.logr().Info("billing.balance_settlement_flushed", fields...)
}

// runBacklogMonitor 周期性读取队列积压，更新「是否退回同步扣费」状态。独立于 flush 循环，
// 采样慢不会拖慢结算。每个进程各自判断；状态来自共享队列，所以多个进程同时在线时结论一致。
func (w *UsageBalanceSettlementWorker) runBacklogMonitor(ctx context.Context) {
	defer w.wg.Done()
	interval := w.opts.BacklogSampleInterval
	if interval <= 0 {
		interval = usageBalanceSettlementBacklogSampleInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// 启动即采一次：进程可能是在事故后重启，不能先让新请求入队一个采样周期。
	w.sampleBacklog(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sampleBacklog(ctx)
		}
	}
}

func (w *UsageBalanceSettlementWorker) sampleBacklog(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, usageBalanceSettlementBacklogQueryTimeout)
	defer cancel()
	backlog, err := w.backlogReader.BalanceSettlementBacklog(ctx, usageBalanceSettlementDepthCap(w.opts.MaxBacklog))
	if err != nil {
		if parent.Err() != nil {
			return
		}
		// 读不到积压时保持原状态：库在抖动时，入队（一次 INSERT）不比同步扣费（抢用户行锁）更糟，
		// 已退回的也留在同步路径直到读数恢复。
		w.sampleFailures.Add(1)
		if settlementLogDue(&w.lastSampleErrLog) {
			w.logr().Warn("billing.balance_settlement_backlog_sample_failed", zap.Error(err))
		}
		return
	}
	w.applyBacklog(backlog)
}

// applyBacklog 记录一次读数，并按迟滞规则更新退回状态。只由监控 goroutine 调用（单写者）。
func (w *UsageBalanceSettlementWorker) applyBacklog(backlog UsageBalanceSettlementBacklog) {
	now := time.Now()
	w.lastBacklog.Store(&usageBalanceSettlementBacklogSnapshot{backlog: backlog, sampledAt: now})

	maxBacklog, maxAge := w.opts.MaxBacklog, w.opts.MaxAge
	active := usageBalanceSettlementBacklogFallback.Load()
	next := nextBacklogFallbackState(active, backlog, maxBacklog, maxAge)
	if next != active {
		usageBalanceSettlementBacklogFallback.Store(next)
		w.fallbackTransitions.Add(1)
		if next {
			w.fallbackSince.Store(now.UnixNano())
			w.logr().Warn("billing.balance_settlement_fallback_enter",
				zap.Int64("backlog_depth", backlog.Pending),
				zap.Int64("oldest_age_ms", backlog.OldestAge.Milliseconds()),
				zap.Int64("max_backlog", maxBacklog),
				zap.Int64("max_age_seconds", int64(maxAge/time.Second)),
			)
		} else {
			w.fallbackSince.Store(0)
			w.logr().Info("billing.balance_settlement_fallback_exit",
				zap.Int64("backlog_depth", backlog.Pending),
				zap.Int64("oldest_age_ms", backlog.OldestAge.Milliseconds()),
			)
		}
	}
	if backlogOverLimit(backlog, maxBacklog, maxAge) && settlementLogDue(&w.lastBacklogLog) {
		w.logr().Warn("billing.balance_settlement_backlog_high",
			zap.Int64("backlog_depth", backlog.Pending),
			zap.Bool("backlog_depth_capped", backlog.PendingCapped),
			zap.Int64("oldest_age_ms", backlog.OldestAge.Milliseconds()),
			zap.Int64("max_backlog", maxBacklog),
			zap.Int64("max_age_seconds", int64(maxAge/time.Second)),
			zap.Bool("sync_fallback", usageBalanceSettlementBacklogFallback.Load()),
		)
	}
}

// settlementLogDue 把同一类重复日志限制在每 usageBalanceSettlementBacklogLogEvery 一条。
func settlementLogDue(last *atomic.Int64) bool {
	now := time.Now().UnixNano()
	prev := last.Load()
	if now-prev < int64(usageBalanceSettlementBacklogLogEvery) {
		return false
	}
	return last.CompareAndSwap(prev, now)
}

// observe 把已提交的结算结果交给低余额观察者。非阻塞：观察者要读设置、必要时读用户，
// 绝不能拖慢结算循环；来不及处理时丢弃并计数（正常情况下缓冲永远不会满）。
func (w *UsageBalanceSettlementWorker) observe(results []UsageBalanceSettlementResult) {
	if w.observeCh == nil || len(results) == 0 {
		return
	}
	select {
	case w.observeCh <- results:
	default:
		w.observerDropped.Add(1)
		if settlementLogDue(&w.lastObserveDropLog) {
			w.logr().Warn("billing.balance_settlement_observer_dropped",
				zap.Uint64("dropped_total", w.observerDropped.Load()))
		}
	}
}

func (w *UsageBalanceSettlementWorker) runObserver(ctx context.Context) {
	defer w.wg.Done()
	for {
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case results := <-w.observeCh:
			w.deliver(ctx, results)
		}
	}
}

func (w *UsageBalanceSettlementWorker) deliver(ctx context.Context, results []UsageBalanceSettlementResult) {
	defer func() {
		if r := recover(); r != nil {
			w.logr().Error("billing.balance_settlement_observer_panic", zap.Any("recover", r))
		}
	}()
	w.opts.Observer.ObserveBalanceSettlements(ctx, results)
}

// Health 返回运维健康接口用的只读快照。积压读数是一次有界的实时查询（和队列深度无关），
// 不依赖监控 goroutine 是否在跑，所以开关关闭时运维也能看到队列状态。
func (w *UsageBalanceSettlementWorker) Health(ctx context.Context) UsageBalanceSettlementHealth {
	health := UsageBalanceSettlementHealth{}
	if w == nil {
		return health
	}
	health.MergeEnabled = w.opts.MergeEnabled
	health.ConsumerRunning = UsageBalanceSettlementConsumerRunning()
	health.MaxBacklog = w.opts.MaxBacklog
	health.MaxAgeSeconds = int64(w.opts.MaxAge / time.Second)
	health.FallbackActive = UsageBalanceSettlementBacklogFallbackActive()
	if since := w.fallbackSince.Load(); since > 0 && health.FallbackActive {
		at := time.Unix(0, since).UTC()
		health.FallbackSince = &at
	}
	health.FallbackTransitions = w.fallbackTransitions.Load()
	health.FallbackCharges = usageBalanceSettlementFallbackCharges.Load()
	if snapshot := w.lastBacklog.Load(); snapshot != nil {
		at := snapshot.sampledAt.UTC()
		health.LastSampleAt = &at
	}
	stats := w.Stats()
	health.Flushes, health.Events, health.Failures = stats.Flushes, stats.Events, stats.Failures
	health.SampleFailures = w.sampleFailures.Load()
	health.ObserverDropped = w.observerDropped.Load()

	reader, ok := w.repo.(UsageBalanceSettlementBacklogReader)
	if !ok {
		return health
	}
	queryCtx, cancel := context.WithTimeout(ctx, usageBalanceSettlementBacklogQueryTimeout)
	defer cancel()
	backlog, err := reader.BalanceSettlementBacklog(queryCtx, usageBalanceSettlementDepthCap(w.opts.MaxBacklog))
	if err != nil {
		health.StatsError = boundedLiveBalanceOutboxError(err)
		return health
	}
	health.Pending = backlog.Pending
	health.PendingCapped = backlog.PendingCapped
	health.OldestAgeMs = backlog.OldestAge.Milliseconds()
	return health
}
