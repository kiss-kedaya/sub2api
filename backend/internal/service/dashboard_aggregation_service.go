package service

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
)

const (
	defaultDashboardAggregationTimeout         = 2 * time.Minute
	defaultDashboardAggregationBackfillTimeout = 30 * time.Minute
	// Allow a 5-minute group-rollup statement plus lock/transaction overhead.
	defaultDashboardGroupUsageSyncTimeout = 6 * time.Minute
	dashboardAggregationRetentionInterval = 6 * time.Hour

	// 迟到行自愈：每小时把最近 3 小时重新聚合一遍。
	//
	// 每分钟的聚合只重算 [水位-lookback, now)（默认回看 120 秒）。created_at 落在某个小时末尾、
	// 提交又晚于约 2 分钟的行，之后不会再被任何一次增量覆盖，用量页读小时汇总时就会一直少算，
	// 直到下次进程启动按 recompute_days 重算。这类行真实存在：Live 通话用开始时间做 created_at、
	// 结束（最长 1 小时）才落库；媒体/视频结算失败重试沿用旧 created_at。
	//
	// 成本：3 小时约 120 万行 usage_logs，按小时分 4 批（与启动重算同一条 aggregateRange 路径，
	// 每批一个有界语句），每小时一次，只在 leader 上、持有 running 互斥期间运行。
	// 窗口取 3 小时而不是 2 小时：错过一次（拿不到锁、分钟聚合恰好在跑）也还有下一次来补。
	dashboardAggregationSelfHealWindow   = 3 * time.Hour
	dashboardAggregationSelfHealInterval = time.Hour
	// 4 批小时聚合 + 取锁；必须小于 leader 锁 TTL，锁才不会在运行中途过期。
	dashboardAggregationSelfHealTimeout = 5 * time.Minute

	// dashboardAggregationLeaderLockKey 保证多副本部署中每个周期只有一个实例执行聚合。
	dashboardAggregationLeaderLockKey = "dashboard:aggregation:leader"
	// TTL 必须覆盖 dashboard 聚合与分组日汇总两个有界阶段，避免任务中途失锁。
	dashboardAggregationLeaderLockTTL = defaultDashboardAggregationTimeout + defaultDashboardGroupUsageSyncTimeout + time.Minute

	// Startup and periodic group-usage syncs share this lock. They have different
	// entry points and may overlap during rolling restarts, but both rebuild the
	// same watermark and historical buckets. The TTL covers the longer startup
	// deadline; successful jobs release it immediately.
	dashboardAggregationGroupUsageLeaderLockKey = "dashboard:aggregation:group-usage:leader"
	dashboardAggregationGroupUsageLeaderLockTTL = defaultDashboardAggregationBackfillTimeout + time.Minute
)

var (
	// ErrDashboardBackfillDisabled 当配置禁用回填时返回。
	ErrDashboardBackfillDisabled = errors.New("仪表盘聚合回填已禁用")
	// ErrDashboardBackfillTooLarge 当回填跨度超过限制时返回。
	ErrDashboardBackfillTooLarge            = errors.New("回填时间跨度过大")
	ErrDashboardRollupStorageBudgetExceeded = errors.New("仪表盘维度汇总超过存储预算")
	errDashboardAggregationRunning          = errors.New("聚合作业正在运行")
)

// DashboardAggregationRepository 定义仪表盘预聚合仓储接口。
type DashboardAggregationRepository interface {
	AggregateRange(ctx context.Context, start, end time.Time) error
	// RecomputeRange 重新计算指定时间范围内的聚合数据（包含活跃用户等派生表）。
	// 设计目的：当 usage_logs 被批量删除/回滚后，确保聚合表可恢复一致性。
	RecomputeRange(ctx context.Context, start, end time.Time) error
	GetAggregationWatermark(ctx context.Context) (time.Time, error)
	UpdateAggregationWatermark(ctx context.Context, aggregatedAt time.Time) error
	CleanupAggregates(ctx context.Context, hourlyCutoff, dailyCutoff time.Time) error
	CleanupUsageLogs(ctx context.Context, cutoff time.Time) error
	CleanupUsageBillingDedup(ctx context.Context, cutoff time.Time) error
	EnsureUsageLogsPartitions(ctx context.Context, now time.Time) error
}

// DashboardAggregationService 负责定时聚合与回填。
type DashboardAggregationService struct {
	repo                 DashboardAggregationRepository
	timingWheel          *TimingWheelService
	cfg                  config.DashboardAggregationConfig
	running              int32
	lastRetentionCleanup atomic.Value // time.Time
	lastSelfHeal         atomic.Value // time.Time，最近一次自愈开始的时间（成功或失败都记）

	lockCache  LeaderLockCache
	db         *sql.DB
	instanceID string
}

// NewDashboardAggregationService 创建聚合服务。
func NewDashboardAggregationService(repo DashboardAggregationRepository, timingWheel *TimingWheelService, cfg *config.Config) *DashboardAggregationService {
	var aggCfg config.DashboardAggregationConfig
	if cfg != nil {
		aggCfg = cfg.DashboardAgg
	}
	if setter, ok := repo.(interface{ SetDashboardDimensionRollupsEnabled(bool) }); ok {
		setter.SetDashboardDimensionRollupsEnabled(aggCfg.DimensionRollupsEnabled)
	}
	return &DashboardAggregationService{
		repo:        repo,
		timingWheel: timingWheel,
		cfg:         aggCfg,
		instanceID:  uuid.NewString(),
	}
}

// SetLeaderLock injects the leader-lock cache and DB used to elect a single
// instance for the periodic scheduled aggregation. When both are nil the job runs
// ungated (single-instance / test behavior).
func (s *DashboardAggregationService) SetLeaderLock(lockCache LeaderLockCache, db *sql.DB) {
	if s == nil {
		return
	}
	s.lockCache = lockCache
	s.db = db
}

// Start 启动定时聚合作业（重启生效配置）。
func (s *DashboardAggregationService) Start() {
	if s == nil || s.repo == nil || s.timingWheel == nil {
		return
	}
	if !s.cfg.Enabled {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 聚合作业已禁用")
		return
	}
	go s.runStartupGroupUsageSync()

	interval := time.Duration(s.cfg.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = time.Minute
	}

	if s.cfg.RecomputeDays > 0 {
		go s.recomputeRecentDays()
		// 启动重算刚覆盖过最近几天，第一次自愈从一小时后算起。
		s.lastSelfHeal.Store(time.Now().UTC())
	}

	s.timingWheel.ScheduleRecurring("dashboard:aggregation", interval, func() {
		s.runScheduledAggregation()
		// 分钟聚合刚释放 running 互斥，紧接着检查自愈（一小时内只会真正执行一次）。
		s.runSelfHealSafely()
	})
	logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 聚合作业启动 (interval=%v, lookback=%ds)", interval, s.cfg.LookbackSeconds)
	if !s.cfg.BackfillEnabled {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 回填已禁用，如需补齐保留窗口以外历史数据请手动回填")
	}
}

// TriggerBackfill 触发回填（异步）。
func (s *DashboardAggregationService) TriggerBackfill(start, end time.Time) error {
	if s == nil || s.repo == nil {
		return errors.New("聚合服务未初始化")
	}
	if !s.cfg.BackfillEnabled {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 回填被拒绝: backfill_enabled=false")
		return ErrDashboardBackfillDisabled
	}
	if !end.After(start) {
		return errors.New("回填时间范围无效")
	}
	if s.cfg.BackfillMaxDays > 0 {
		maxRange := time.Duration(s.cfg.BackfillMaxDays) * 24 * time.Hour
		if end.Sub(start) > maxRange {
			return ErrDashboardBackfillTooLarge
		}
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), defaultDashboardAggregationBackfillTimeout)
		defer cancel()
		if err := s.backfillRange(ctx, start, end); err != nil {
			logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 回填失败: %v", err)
		}
	}()
	return nil
}

// TriggerRecomputeRange 触发指定范围的重新计算（异步）。
// 与 TriggerBackfill 不同：
// - 不依赖 backfill_enabled（这是内部一致性修复）
// - 不更新 watermark（避免影响正常增量聚合游标）
func (s *DashboardAggregationService) TriggerRecomputeRange(start, end time.Time) error {
	if s == nil || s.repo == nil {
		return errors.New("聚合服务未初始化")
	}
	if !s.cfg.Enabled {
		return errors.New("聚合服务已禁用")
	}
	if !end.After(start) {
		return errors.New("重新计算时间范围无效")
	}

	go func() {
		const maxRetries = 3
		for i := 0; i < maxRetries; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), defaultDashboardAggregationBackfillTimeout)
			err := s.recomputeRange(ctx, start, end)
			cancel()
			if err == nil {
				return
			}
			if !errors.Is(err, errDashboardAggregationRunning) {
				logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 重新计算失败: %v", err)
				return
			}
			time.Sleep(5 * time.Second)
		}
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 重新计算放弃: 聚合作业持续占用")
	}()
	return nil
}

func (s *DashboardAggregationService) recomputeRecentDays() {
	days := s.cfg.RecomputeDays
	if days <= 0 {
		return
	}
	now := time.Now().UTC()
	start := now.AddDate(0, 0, -days)

	ctx, cancel := context.WithTimeout(context.Background(), defaultDashboardAggregationBackfillTimeout)
	defer cancel()
	if err := s.backfillRange(ctx, start, now); err != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 启动重算失败: %v", err)
		return
	}
}

func (s *DashboardAggregationService) recomputeRange(ctx context.Context, start, end time.Time) error {
	if !atomic.CompareAndSwapInt32(&s.running, 0, 1) {
		return errDashboardAggregationRunning
	}
	defer atomic.StoreInt32(&s.running, 0)

	jobStart := time.Now().UTC()
	if err := s.repo.RecomputeRange(ctx, start, end); err != nil {
		return err
	}
	logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 重新计算完成 (start=%s end=%s duration=%s)",
		start.UTC().Format(time.RFC3339),
		end.UTC().Format(time.RFC3339),
		time.Since(jobStart).String(),
	)
	return nil
}

func (s *DashboardAggregationService) runScheduledAggregation() {
	if !atomic.CompareAndSwapInt32(&s.running, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&s.running, 0)

	jobStart := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), defaultDashboardAggregationTimeout)
	defer cancel()

	// Multi-instance guard: only the leader runs the periodic aggregation; peers
	// skip this cycle to avoid N× redundant GROUP BY queries and watermark races.
	release, ok := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, dashboardAggregationLeaderLockKey, s.instanceID, dashboardAggregationLeaderLockTTL)
	if !ok {
		return
	}
	defer release()
	defer s.runScheduledGroupUsageSync()

	now := time.Now().UTC()
	last, err := s.repo.GetAggregationWatermark(ctx)
	if err != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 读取水位失败: %v", err)
		last = time.Unix(0, 0).UTC()
	}

	lookback := time.Duration(s.cfg.LookbackSeconds) * time.Second
	epoch := time.Unix(0, 0).UTC()
	start := last.Add(-lookback)
	if !last.After(epoch) {
		retentionDays := s.cfg.Retention.UsageLogsDays
		if retentionDays <= 0 {
			retentionDays = 1
		}
		start = truncateToDayUTC(now.AddDate(0, 0, -retentionDays))
	} else if start.After(now) {
		start = now.Add(-lookback)
	}

	if err := s.aggregateRange(ctx, start, now); err != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 聚合失败: %v", err)
		return
	}

	updateErr := s.repo.UpdateAggregationWatermark(ctx, now)
	if updateErr != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 更新水位失败: %v", updateErr)
	}
	slog.Debug("[DashboardAggregation] 聚合完成",
		"start", start.Format(time.RFC3339),
		"end", now.Format(time.RFC3339),
		"duration", time.Since(jobStart).String(),
		"watermark_updated", updateErr == nil,
	)

	s.maybeCleanupRetention(ctx, now)
}

func (s *DashboardAggregationService) runScheduledGroupUsageSync() {
	ctx, cancel := context.WithTimeout(context.Background(), defaultDashboardGroupUsageSyncTimeout)
	defer cancel()
	release, ok := tryAcquireSingletonLeaderLock(
		ctx,
		s.lockCache,
		s.db,
		dashboardAggregationGroupUsageLeaderLockKey,
		s.instanceID,
		dashboardAggregationGroupUsageLeaderLockTTL,
	)
	if !ok {
		return
	}
	defer release()
	if err := s.syncGroupUsageRollups(ctx, time.Now().UTC()); err != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 分组用量日汇总失败: %v", err)
	}
}

func (s *DashboardAggregationService) runStartupGroupUsageSync() {
	ctx, cancel := context.WithTimeout(context.Background(), defaultDashboardAggregationBackfillTimeout)
	defer cancel()
	release, ok := tryAcquireSingletonLeaderLock(
		ctx,
		s.lockCache,
		s.db,
		dashboardAggregationGroupUsageLeaderLockKey,
		s.instanceID,
		dashboardAggregationGroupUsageLeaderLockTTL,
	)
	if !ok {
		return
	}
	defer release()
	if err := s.syncGroupUsageRollups(ctx, time.Now().UTC()); err != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 启动分组用量回填失败: %v", err)
	}
}

func (s *DashboardAggregationService) syncGroupUsageRollups(ctx context.Context, now time.Time) error {
	repo, ok := s.repo.(GroupUsageRollupRepository)
	if !ok {
		return nil
	}
	return repo.SyncGroupUsageRollups(ctx, GroupUsageTodayStart(now))
}

// acquireAggregationRun 同时拿下进程内的 running 互斥和跨实例的 leader 锁（与分钟聚合同一把），
// 缺一就返回 errDashboardAggregationRunning。返回的 release 依次释放两者。
func (s *DashboardAggregationService) acquireAggregationRun(ctx context.Context) (func(), error) {
	if !atomic.CompareAndSwapInt32(&s.running, 0, 1) {
		return nil, errDashboardAggregationRunning
	}
	releaseLock, ok := tryAcquireSingletonLeaderLock(ctx, s.lockCache, s.db, dashboardAggregationLeaderLockKey, s.instanceID, dashboardAggregationLeaderLockTTL)
	if !ok {
		atomic.StoreInt32(&s.running, 0)
		return nil, errDashboardAggregationRunning
	}
	return func() {
		releaseLock()
		atomic.StoreInt32(&s.running, 0)
	}, nil
}

// aggregateInBatches 按 backfill_batch_hours 把 [start, end) 切成整点对齐的批次逐批聚合，
// 每批一个有界语句，批间检查维度汇总的存储预算。启动重算、回填与迟到行自愈共用。
func (s *DashboardAggregationService) aggregateInBatches(ctx context.Context, start, end time.Time) error {
	cursor := start.Truncate(time.Hour)
	batchHours := s.cfg.BackfillBatchHours
	if batchHours <= 0 {
		batchHours = 1
	}
	// Bound user-provided configuration before converting to time.Duration;
	// an overflowing duration would move the cursor backwards indefinitely.
	if batchHours > 24*365 {
		batchHours = 24 * 365
	}
	for cursor.Before(end) {
		windowEnd := cursor.Add(time.Duration(batchHours) * time.Hour)
		if windowEnd.After(end) {
			windowEnd = end
		}
		if err := s.aggregateRange(ctx, cursor, windowEnd); err != nil {
			return err
		}
		if err := s.ensureDimensionRollupStorageBudget(ctx); err != nil {
			return err
		}
		cursor = windowEnd
	}
	return nil
}

// DashboardAggregationSelfHealRange 返回迟到行自愈在 now 时刻重新聚合的 [start, end)：
// 最近 dashboardAggregationSelfHealWindow，起点对齐到整点。
func DashboardAggregationSelfHealRange(now time.Time) (start, end time.Time) {
	end = now.UTC()
	return end.Add(-dashboardAggregationSelfHealWindow).Truncate(time.Hour), end
}

// runSelfHealSafely 在分钟聚合的定时回调里调用自愈。回调里的 panic 会让这个定时任务不再被重新排期
// （ScheduleRecurring 在回调返回之后才续期），那就连每分钟的聚合一起停了；自愈的任何意外都不该
// 把分钟聚合带走，所以在这里接住、只记日志。
func (s *DashboardAggregationService) runSelfHealSafely() {
	defer func() {
		if r := recover(); r != nil {
			logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 迟到行自愈 panic（已忽略，不影响分钟聚合）: %v", r)
		}
	}()
	s.runSelfHeal()
}

// runSelfHeal 每小时（节流）把最近 3 小时重新聚合一遍，补上分钟聚合覆盖不到的迟到行
// （原因与成本见 dashboardAggregationSelfHealWindow 的注释）。
//
//   - 与分钟聚合互斥：先拿 running 互斥再拿同一把 leader 锁，拿不到就直接返回、不记时间戳，
//     下一个分钟 tick 再来看；
//   - 失败只记日志，不影响分钟聚合；失败也算一次尝试，下个小时再试；
//   - recompute_days<=0 或聚合被禁用时不跑；
//   - 不调 repo.RecomputeRange：它会先删掉窗口所在整天的 daily_users 再只按窗口内的用户重建，
//     3 小时窗口会把当天日活压成窗口内的人数；这里走 upsert（与分钟聚合同一条 SQL），
//     也不动水位。
func (s *DashboardAggregationService) runSelfHeal() {
	if s == nil || s.repo == nil || !s.cfg.Enabled || s.cfg.RecomputeDays <= 0 {
		return
	}
	now := time.Now().UTC()
	if last, ok := s.lastSelfHeal.Load().(time.Time); ok && now.Sub(last) < dashboardAggregationSelfHealInterval {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), dashboardAggregationSelfHealTimeout)
	defer cancel()
	release, err := s.acquireAggregationRun(ctx)
	if err != nil {
		return
	}
	defer release()

	s.lastSelfHeal.Store(now)
	start, end := DashboardAggregationSelfHealRange(now)
	if err := s.aggregateInBatches(ctx, start, end); err != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 迟到行自愈失败，下个小时再试 (start=%s end=%s): %v",
			start.Format(time.RFC3339), end.Format(time.RFC3339), err)
		return
	}
	logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 迟到行自愈完成 (start=%s end=%s duration=%s)",
		start.Format(time.RFC3339), end.Format(time.RFC3339), time.Since(now).String())
}

func (s *DashboardAggregationService) backfillRange(ctx context.Context, start, end time.Time) error {
	release, err := s.acquireAggregationRun(ctx)
	if err != nil {
		return err
	}
	defer release()

	jobStart := time.Now().UTC()
	startUTC := start.UTC()
	endUTC := end.UTC()
	if !endUTC.After(startUTC) {
		return errors.New("回填时间范围无效")
	}
	if err := s.aggregateInBatches(ctx, startUTC, endUTC); err != nil {
		return err
	}

	updateErr := s.repo.UpdateAggregationWatermark(ctx, endUTC)
	if updateErr != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 更新水位失败: %v", updateErr)
	}
	logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 回填聚合完成 (start=%s end=%s duration=%s watermark_updated=%t)",
		startUTC.Format(time.RFC3339),
		endUTC.Format(time.RFC3339),
		time.Since(jobStart).String(),
		updateErr == nil,
	)

	s.maybeCleanupRetention(ctx, endUTC)
	return nil
}

func (s *DashboardAggregationService) ensureDimensionRollupStorageBudget(ctx context.Context) error {
	if !s.cfg.DimensionRollupsEnabled || s.cfg.RollupStorageBudgetGB <= 0 {
		return nil
	}
	sizer, ok := s.repo.(interface {
		GetDashboardDimensionRollupSizeBytes(context.Context) (int64, error)
	})
	if !ok {
		return nil
	}
	size, err := sizer.GetDashboardDimensionRollupSizeBytes(ctx)
	if err != nil {
		return err
	}
	limit := int64(s.cfg.RollupStorageBudgetGB) << 30
	if size > limit {
		return ErrDashboardRollupStorageBudgetExceeded
	}
	return nil
}

func (s *DashboardAggregationService) aggregateRange(ctx context.Context, start, end time.Time) error {
	if !end.After(start) {
		return nil
	}
	if err := s.repo.EnsureUsageLogsPartitions(ctx, end); err != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 分区检查失败: %v", err)
	}
	return s.repo.AggregateRange(ctx, start, end)
}

func (s *DashboardAggregationService) maybeCleanupRetention(ctx context.Context, now time.Time) {
	lastAny := s.lastRetentionCleanup.Load()
	if lastAny != nil {
		if last, ok := lastAny.(time.Time); ok && now.Sub(last) < dashboardAggregationRetentionInterval {
			return
		}
	}

	hourlyCutoff := now.AddDate(0, 0, -s.cfg.Retention.HourlyDays)
	dailyCutoff := now.AddDate(0, 0, -s.cfg.Retention.DailyDays)
	usageCutoff := now.AddDate(0, 0, -s.cfg.Retention.UsageLogsDays)
	dedupCutoff := now.AddDate(0, 0, -s.cfg.Retention.UsageBillingDedupDays)

	aggErr := s.repo.CleanupAggregates(ctx, hourlyCutoff, dailyCutoff)
	if aggErr != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] 聚合保留清理失败: %v", aggErr)
	}
	usageErr := s.repo.CleanupUsageLogs(ctx, usageCutoff)
	if usageErr != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] usage_logs 保留清理失败: %v", usageErr)
	}
	dedupErr := s.repo.CleanupUsageBillingDedup(ctx, dedupCutoff)
	if dedupErr != nil {
		logger.LegacyPrintf("service.dashboard_aggregation", "[DashboardAggregation] usage_billing_dedup 保留清理失败: %v", dedupErr)
	}
	if aggErr == nil && usageErr == nil && dedupErr == nil {
		s.lastRetentionCleanup.Store(now)
	}
}

func truncateToDayUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
