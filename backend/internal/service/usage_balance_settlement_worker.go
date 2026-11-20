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
type UsageBalanceSettlementWorker struct {
	repo UsageBalanceSettlementRepository

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
}

func NewUsageBalanceSettlementWorker(
	repo UsageBalanceSettlementRepository,
) *UsageBalanceSettlementWorker {
	worker := &UsageBalanceSettlementWorker{repo: repo}
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

// logStats 每分钟输出一次增量统计，让灰度期可以从日志判断合并结算的合并率
// （events / flushes）与失败情况，而不用把计数暴露成新的指标端点。
func (w *UsageBalanceSettlementWorker) logStats() {
	flushes := w.flushes.Load()
	events := w.events.Load()
	failures := w.failures.Load()
	prevFlushes := w.loggedFlushes.Swap(flushes)
	prevEvents := w.loggedEvents.Swap(events)
	prevFailures := w.loggedFailures.Swap(failures)
	if flushes == prevFlushes && failures == prevFailures {
		return
	}
	// 合并率 = 每轮结算事务平均合并的扣费事件数；1.0 表示完全没有合并。
	ratio := 0.0
	if flushes > prevFlushes {
		ratio = float64(events-prevEvents) / float64(flushes-prevFlushes)
	}
	logger.L().Info("billing.balance_settlement_flushed",
		zap.Uint64("flushes", flushes-prevFlushes),
		zap.Uint64("events", events-prevEvents),
		zap.Uint64("failures", failures-prevFailures),
		zap.Float64("events_per_flush", ratio),
		zap.Uint64("total_flushes", flushes),
		zap.Uint64("total_events", events),
	)
}
