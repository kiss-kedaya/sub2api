package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

const (
	usageCleanupWorkerName          = "usage_cleanup_worker"
	defaultUsageCleanupPreviewLimit = 100000
	usageCleanupPreviewTimeout      = 5 * time.Second
)

// UsageCleanupService 负责创建与执行使用记录清理任务
type UsageCleanupService struct {
	repo        UsageCleanupRepository
	timingWheel *TimingWheelService
	dashboard   *DashboardAggregationService
	cfg         *config.Config

	running   int32
	recheck   int32
	startOnce sync.Once
	stopOnce  sync.Once

	workerCtx    context.Context
	workerCancel context.CancelFunc
}

func NewUsageCleanupService(repo UsageCleanupRepository, timingWheel *TimingWheelService, dashboard *DashboardAggregationService, cfg *config.Config) *UsageCleanupService {
	workerCtx, workerCancel := context.WithCancel(context.Background())
	return &UsageCleanupService{
		repo:         repo,
		timingWheel:  timingWheel,
		dashboard:    dashboard,
		cfg:          cfg,
		workerCtx:    workerCtx,
		workerCancel: workerCancel,
	}
}

func describeUsageCleanupFilters(filters UsageCleanupFilters) string {
	var parts []string
	parts = append(parts, "start="+filters.StartTime.UTC().Format(time.RFC3339))
	parts = append(parts, "end="+filters.EndTime.UTC().Format(time.RFC3339))
	if filters.UserID != nil {
		parts = append(parts, fmt.Sprintf("user_id=%d", *filters.UserID))
	}
	if filters.APIKeyID != nil {
		parts = append(parts, fmt.Sprintf("api_key_id=%d", *filters.APIKeyID))
	}
	if filters.AccountID != nil {
		parts = append(parts, fmt.Sprintf("account_id=%d", *filters.AccountID))
	}
	if filters.GroupID != nil {
		parts = append(parts, fmt.Sprintf("group_id=%d", *filters.GroupID))
	}
	if filters.Model != nil {
		parts = append(parts, "model="+strings.TrimSpace(*filters.Model))
	}
	if filters.RequestType != nil {
		parts = append(parts, "request_type="+RequestTypeFromInt16(*filters.RequestType).String())
	}
	if filters.Stream != nil {
		parts = append(parts, fmt.Sprintf("stream=%t", *filters.Stream))
	}
	if filters.BillingType != nil {
		parts = append(parts, fmt.Sprintf("billing_type=%d", *filters.BillingType))
	}
	if filters.RequestID != nil {
		parts = append(parts, "request_id="+strings.TrimSpace(*filters.RequestID))
	}
	if filters.NativeCompactionV2 != nil {
		parts = append(parts, fmt.Sprintf("native_compaction_v2=%t", *filters.NativeCompactionV2))
	}
	if filters.BillingMode != nil {
		parts = append(parts, "billing_mode="+strings.TrimSpace(*filters.BillingMode))
	}
	if filters.UpstreamModelMismatch != nil {
		parts = append(parts, fmt.Sprintf("upstream_model_mismatch=%t", *filters.UpstreamModelMismatch))
	}
	return strings.Join(parts, " ")
}

func (s *UsageCleanupService) Start() {
	if s == nil {
		return
	}
	if s.cfg != nil && !s.cfg.UsageCleanup.Enabled {
		logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] not started (disabled)")
		return
	}
	if s.repo == nil || s.timingWheel == nil {
		logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] not started (missing deps)")
		return
	}

	interval := s.workerInterval()
	s.startOnce.Do(func() {
		s.timingWheel.ScheduleRecurring(usageCleanupWorkerName, interval, s.runOnce)
		logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] started (interval=%s max_range_days=%d batch_size=%d task_timeout=%s)", interval, s.maxRangeDays(), s.batchSize(), s.taskTimeout())
	})
}

func (s *UsageCleanupService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.workerCancel != nil {
			s.workerCancel()
		}
		if s.timingWheel != nil {
			s.timingWheel.Cancel(usageCleanupWorkerName)
		}
		logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] stopped")
	})
}

func (s *UsageCleanupService) ListTasks(ctx context.Context, params pagination.PaginationParams) ([]UsageCleanupTask, *pagination.PaginationResult, error) {
	if s == nil || s.repo == nil {
		return nil, nil, fmt.Errorf("cleanup service not ready")
	}
	return s.repo.ListTasks(ctx, params)
}

func (s *UsageCleanupService) PreviewTask(ctx context.Context, filters UsageCleanupFilters) (*UsageCleanupPreview, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("cleanup service not ready")
	}
	if s.cfg != nil && !s.cfg.UsageCleanup.Enabled {
		return nil, infraerrors.New(http.StatusServiceUnavailable, "USAGE_CLEANUP_DISABLED", "usage cleanup is disabled")
	}
	sanitizeUsageCleanupFilters(&filters)
	if err := s.validateFilters(filters); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	previewCtx, cancel := context.WithTimeout(ctx, usageCleanupPreviewTimeout)
	defer cancel()
	matched, truncated, err := s.repo.CountUsageLogs(previewCtx, filters, defaultUsageCleanupPreviewLimit)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(previewCtx.Err(), context.DeadlineExceeded) {
			return nil, infraerrors.New(http.StatusGatewayTimeout, "USAGE_CLEANUP_PREVIEW_TIMEOUT", "cleanup preview timed out")
		}
		return nil, fmt.Errorf("preview cleanup task: %w", err)
	}
	return &UsageCleanupPreview{
		MatchedRows:  matched,
		Truncated:    truncated,
		MaxRangeDays: s.maxRangeDays(),
		StartTime:    filters.StartTime,
		EndTime:      filters.EndTime,
	}, nil
}

func (s *UsageCleanupService) CreateTask(ctx context.Context, filters UsageCleanupFilters, createdBy int64) (*UsageCleanupTask, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("cleanup service not ready")
	}
	if s.cfg != nil && !s.cfg.UsageCleanup.Enabled {
		return nil, infraerrors.New(http.StatusServiceUnavailable, "USAGE_CLEANUP_DISABLED", "usage cleanup is disabled")
	}
	if createdBy <= 0 {
		return nil, infraerrors.BadRequest("USAGE_CLEANUP_INVALID_CREATOR", "invalid creator")
	}

	logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] create_task requested: operator=%d %s", createdBy, describeUsageCleanupFilters(filters))
	sanitizeUsageCleanupFilters(&filters)
	if err := s.validateFilters(filters); err != nil {
		logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] create_task rejected: operator=%d err=%v %s", createdBy, err, describeUsageCleanupFilters(filters))
		return nil, err
	}

	task := &UsageCleanupTask{
		Status:    UsageCleanupStatusPending,
		Filters:   filters,
		CreatedBy: createdBy,
	}
	if err := s.repo.CreateTask(ctx, task); err != nil {
		logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] create_task persist failed: operator=%d err=%v %s", createdBy, err, describeUsageCleanupFilters(filters))
		return nil, fmt.Errorf("create cleanup task: %w", err)
	}
	logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] create_task persisted: task=%d operator=%d status=%s deleted_rows=%d %s", task.ID, createdBy, task.Status, task.DeletedRows, describeUsageCleanupFilters(filters))
	go s.runOnce()
	return task, nil
}

func (s *UsageCleanupService) runOnce() {
	svc := s
	if svc == nil {
		return
	}
	for {
		if !atomic.CompareAndSwapInt32(&svc.running, 0, 1) {
			atomic.StoreInt32(&svc.recheck, 1)
			logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] run_once skipped: already_running=true")
			return
		}
		stopLoop := func() bool {
			defer atomic.StoreInt32(&svc.running, 0)
			atomic.StoreInt32(&svc.recheck, 0)
			return svc.drainPendingTasks()
		}()
		if stopLoop || atomic.SwapInt32(&svc.recheck, 0) == 0 {
			return
		}
		if svc.workerCtx != nil && svc.workerCtx.Err() != nil {
			return
		}
	}
}

func (s *UsageCleanupService) drainPendingTasks() bool {
	parent := context.Background()
	if s.workerCtx != nil {
		parent = s.workerCtx
	}

	for {
		if parent.Err() != nil {
			return true
		}
		ctx, cancel := context.WithTimeout(parent, s.taskTimeout())
		task, err := s.repo.ClaimNextPendingTask(ctx, int64(s.taskTimeout().Seconds()))
		if err != nil {
			cancel()
			logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] claim pending task failed: %v", err)
			return true
		}
		if task == nil {
			cancel()
			slog.Debug("[UsageCleanup] run_once done: no_task=true")
			return false
		}

		logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] task claimed: task=%d status=%s created_by=%d deleted_rows=%d %s", task.ID, task.Status, task.CreatedBy, task.DeletedRows, describeUsageCleanupFilters(task.Filters))
		interrupted := s.executeTask(ctx, task)
		cancel()
		if interrupted {
			return true
		}
	}
}

func (s *UsageCleanupService) executeTask(ctx context.Context, task *UsageCleanupTask) bool {
	if task == nil {
		return false
	}

	batchSize := s.batchSize()
	initialDeleted := task.DeletedRows
	deletedTotal := task.DeletedRows
	start := time.Now()
	logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] task started: task=%d batch_size=%d deleted_rows=%d %s", task.ID, batchSize, deletedTotal, describeUsageCleanupFilters(task.Filters))
	var batchNum int

	for {
		if ctx != nil && ctx.Err() != nil {
			logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] task interrupted: task=%d err=%v", task.ID, ctx.Err())
			if deletedTotal > initialDeleted {
				s.triggerDashboardRecompute(task.ID, task.Filters.StartTime, task.Filters.EndTime)
			}
			return true
		}
		canceled, err := s.isTaskCanceled(ctx, task.ID)
		if err != nil {
			s.markTaskFailed(task.ID, deletedTotal, err)
			if deletedTotal > initialDeleted {
				s.triggerDashboardRecompute(task.ID, task.Filters.StartTime, task.Filters.EndTime)
			}
			return false
		}
		if canceled {
			logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] task canceled: task=%d deleted_rows=%d duration=%s", task.ID, deletedTotal, time.Since(start))
			if deletedTotal > initialDeleted {
				s.triggerDashboardRecompute(task.ID, task.Filters.StartTime, task.Filters.EndTime)
			}
			return false
		}

		batchNum++
		deleted, err := s.repo.DeleteUsageLogsBatch(ctx, task.Filters, batchSize)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				// 任务被中断（例如服务停止/超时），保持 running 状态，后续通过 stale reclaim 续跑。
				logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] task interrupted: task=%d err=%v", task.ID, err)
				if deletedTotal > initialDeleted {
					s.triggerDashboardRecompute(task.ID, task.Filters.StartTime, task.Filters.EndTime)
				}
				return true
			}
			s.markTaskFailed(task.ID, deletedTotal, err)
			if deletedTotal > initialDeleted {
				s.triggerDashboardRecompute(task.ID, task.Filters.StartTime, task.Filters.EndTime)
			}
			return false
		}
		deletedTotal += deleted
		if deleted > 0 {
			updateCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			if err := s.repo.UpdateTaskProgress(updateCtx, task.ID, deletedTotal); err != nil {
				logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] task progress update failed: task=%d deleted_rows=%d err=%v", task.ID, deletedTotal, err)
			}
			cancel()
		}
		if batchNum <= 3 || batchNum%20 == 0 || deleted < int64(batchSize) {
			logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] task batch done: task=%d batch=%d deleted=%d deleted_total=%d", task.ID, batchNum, deleted, deletedTotal)
		}
		if deleted == 0 || deleted < int64(batchSize) {
			break
		}
	}

	if canceled, err := s.isTaskCanceled(ctx, task.ID); err == nil && canceled {
		logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] task canceled before finalize: task=%d deleted_rows=%d duration=%s", task.ID, deletedTotal, time.Since(start))
		if deletedTotal > initialDeleted {
			s.triggerDashboardRecompute(task.ID, task.Filters.StartTime, task.Filters.EndTime)
		}
		return false
	}

	updateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.repo.MarkTaskSucceeded(updateCtx, task.ID, deletedTotal); err != nil {
		logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] update task succeeded failed: task=%d err=%v", task.ID, err)
	} else {
		logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] task succeeded: task=%d deleted_rows=%d duration=%s", task.ID, deletedTotal, time.Since(start))
	}

	s.triggerDashboardRecompute(task.ID, task.Filters.StartTime, task.Filters.EndTime)
	return false
}

func (s *UsageCleanupService) triggerDashboardRecompute(taskID int64, startTime, endTime time.Time) {
	if s.dashboard == nil {
		return
	}
	if err := s.dashboard.TriggerRecomputeRange(startTime, endTime); err != nil {
		logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] trigger dashboard recompute failed: task=%d err=%v", taskID, err)
	} else {
		logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] trigger dashboard recompute: task=%d start=%s end=%s", taskID, startTime.UTC().Format(time.RFC3339), endTime.UTC().Format(time.RFC3339))
	}
}

func (s *UsageCleanupService) markTaskFailed(taskID int64, deletedRows int64, err error) {
	msg := strings.TrimSpace(err.Error())
	if len(msg) > 500 {
		msg = msg[:500]
	}
	logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] task failed: task=%d deleted_rows=%d err=%s", taskID, deletedRows, msg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if updateErr := s.repo.MarkTaskFailed(ctx, taskID, deletedRows, msg); updateErr != nil {
		logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] update task failed failed: task=%d err=%v", taskID, updateErr)
	}
}

func (s *UsageCleanupService) isTaskCanceled(ctx context.Context, taskID int64) (bool, error) {
	if s == nil || s.repo == nil {
		return false, fmt.Errorf("cleanup service not ready")
	}
	checkCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status, err := s.repo.GetTaskStatus(checkCtx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if status == UsageCleanupStatusCanceled {
		logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] task cancel detected: task=%d", taskID)
	}
	return status == UsageCleanupStatusCanceled, nil
}

func (s *UsageCleanupService) validateFilters(filters UsageCleanupFilters) error {
	if filters.StartTime.IsZero() || filters.EndTime.IsZero() {
		return infraerrors.BadRequest("USAGE_CLEANUP_MISSING_RANGE", "start_date and end_date are required")
	}
	if filters.EndTime.Before(filters.StartTime) {
		return infraerrors.BadRequest("USAGE_CLEANUP_INVALID_RANGE", "end_date must be after start_date")
	}
	maxDays := s.maxRangeDays()
	if maxDays > 0 {
		delta := filters.EndTime.Sub(filters.StartTime)
		// Allow <1s sub-second expansion from inclusive end-second boundaries (e.g. exact 24h/31d windows).
		if delta >= time.Duration(maxDays)*24*time.Hour+time.Second {
			return infraerrors.BadRequest("USAGE_CLEANUP_RANGE_TOO_LARGE", fmt.Sprintf("date range exceeds %d days", maxDays)).
				WithMetadata(map[string]string{"max_range_days": strconv.Itoa(maxDays)})
		}
	}
	if filters.BillingMode != nil {
		switch BillingMode(*filters.BillingMode) {
		case BillingModeToken, BillingModePerRequest, BillingModeImage, BillingModeVideo:
		default:
			return infraerrors.BadRequest("USAGE_CLEANUP_INVALID_BILLING_MODE", "invalid billing_mode")
		}
	}
	if filters.RequestID != nil {
		if err := validateUsageCleanupRequestID(*filters.RequestID, filters.StartTime, filters.EndTime); err != nil {
			return err
		}
	}
	return nil
}

func validateUsageCleanupRequestID(input string, start, end time.Time) error {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}
	exact := strings.HasPrefix(input, "=")
	if exact || strings.HasPrefix(input, "^") {
		input = strings.TrimSpace(input[1:])
	}
	normalized := NormalizeUsageRequestIDKey(input)
	if normalized == "" {
		return infraerrors.BadRequest("USAGE_CLEANUP_INVALID_REQUEST_ID", "request_id cannot be empty")
	}
	if exact {
		return nil
	}
	run := 0
	for _, r := range normalized {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			run++
		} else {
			run = 0
		}
		if run >= 3 {
			return nil
		}
	}
	if end.Sub(start) > 24*time.Hour+time.Second {
		return infraerrors.BadRequest("USAGE_CLEANUP_REQUEST_ID_RANGE_TOO_LARGE", "short request_id pattern requires a date range within 24 hours, or use =exact_id")
	}
	return nil
}

func (s *UsageCleanupService) CancelTask(ctx context.Context, taskID int64, canceledBy int64) error {
	if s == nil || s.repo == nil {
		return fmt.Errorf("cleanup service not ready")
	}
	if s.cfg != nil && !s.cfg.UsageCleanup.Enabled {
		return infraerrors.New(http.StatusServiceUnavailable, "USAGE_CLEANUP_DISABLED", "usage cleanup is disabled")
	}
	if canceledBy <= 0 {
		return infraerrors.BadRequest("USAGE_CLEANUP_INVALID_CANCELLER", "invalid canceller")
	}
	status, err := s.repo.GetTaskStatus(ctx, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return infraerrors.New(http.StatusNotFound, "USAGE_CLEANUP_TASK_NOT_FOUND", "cleanup task not found")
		}
		return err
	}
	logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] cancel_task requested: task=%d operator=%d status=%s", taskID, canceledBy, status)
	if status == UsageCleanupStatusCanceled {
		logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] cancel_task idempotent hit: task=%d operator=%d", taskID, canceledBy)
		return nil
	}
	if status != UsageCleanupStatusPending && status != UsageCleanupStatusRunning {
		return infraerrors.New(http.StatusConflict, "USAGE_CLEANUP_CANCEL_CONFLICT", "cleanup task cannot be canceled in current status")
	}
	ok, err := s.repo.CancelTask(ctx, taskID, canceledBy)
	if err != nil {
		return err
	}
	if !ok {
		// 状态可能并发改变
		currentStatus, getErr := s.repo.GetTaskStatus(ctx, taskID)
		if getErr == nil && currentStatus == UsageCleanupStatusCanceled {
			logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] cancel_task idempotent race hit: task=%d operator=%d", taskID, canceledBy)
			return nil
		}
		return infraerrors.New(http.StatusConflict, "USAGE_CLEANUP_CANCEL_CONFLICT", "cleanup task cannot be canceled in current status")
	}
	logger.LegacyPrintf("service.usage_cleanup", "[UsageCleanup] cancel_task done: task=%d operator=%d", taskID, canceledBy)
	return nil
}

func sanitizeUsageCleanupFilters(filters *UsageCleanupFilters) {
	if filters == nil {
		return
	}
	if filters.UserID != nil && *filters.UserID <= 0 {
		filters.UserID = nil
	}
	if filters.APIKeyID != nil && *filters.APIKeyID <= 0 {
		filters.APIKeyID = nil
	}
	if filters.AccountID != nil && *filters.AccountID <= 0 {
		filters.AccountID = nil
	}
	if filters.GroupID != nil && *filters.GroupID <= 0 {
		filters.GroupID = nil
	}
	if filters.Model != nil {
		model := strings.TrimSpace(*filters.Model)
		if model == "" {
			filters.Model = nil
		} else {
			filters.Model = &model
		}
	}
	if filters.RequestType != nil {
		requestType := RequestType(*filters.RequestType)
		if !requestType.IsValid() {
			filters.RequestType = nil
		} else {
			value := int16(requestType.Normalize())
			filters.RequestType = &value
			filters.Stream = nil
		}
	}
	if filters.BillingType != nil && *filters.BillingType < 0 {
		filters.BillingType = nil
	}
	if filters.RequestID != nil {
		requestID := strings.TrimSpace(*filters.RequestID)
		if requestID == "" {
			filters.RequestID = nil
		} else {
			filters.RequestID = &requestID
		}
	}
	if filters.BillingMode != nil {
		billingMode := strings.ToLower(strings.TrimSpace(*filters.BillingMode))
		if billingMode == "" {
			filters.BillingMode = nil
		} else {
			filters.BillingMode = &billingMode
		}
	}
}

func (s *UsageCleanupService) maxRangeDays() int {
	if s == nil || s.cfg == nil {
		return 31
	}
	if s.cfg.UsageCleanup.MaxRangeDays > 0 {
		return s.cfg.UsageCleanup.MaxRangeDays
	}
	return 31
}

func (s *UsageCleanupService) batchSize() int {
	if s == nil || s.cfg == nil {
		return 5000
	}
	if s.cfg.UsageCleanup.BatchSize > 0 {
		return s.cfg.UsageCleanup.BatchSize
	}
	return 5000
}

func (s *UsageCleanupService) workerInterval() time.Duration {
	if s == nil || s.cfg == nil {
		return 10 * time.Second
	}
	if s.cfg.UsageCleanup.WorkerIntervalSeconds > 0 {
		return time.Duration(s.cfg.UsageCleanup.WorkerIntervalSeconds) * time.Second
	}
	return 10 * time.Second
}

func (s *UsageCleanupService) taskTimeout() time.Duration {
	if s == nil || s.cfg == nil {
		return 30 * time.Minute
	}
	if s.cfg.UsageCleanup.TaskTimeoutSeconds > 0 {
		return time.Duration(s.cfg.UsageCleanup.TaskTimeoutSeconds) * time.Second
	}
	return 30 * time.Minute
}
