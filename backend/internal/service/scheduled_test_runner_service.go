package service

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

const (
	scheduledTestDefaultMaxWorkers = 10
	disableScheduledTestRunnerEnv  = "SUB2API_DISABLE_SCHEDULED_TEST_RUNNER"
	// The runner is instantiated by every API/worker process. Keep the lock
	// longer than the five-minute execution context so a slow run cannot lose
	// leadership before its plans have finished.
	scheduledTestRunnerLeaderLockKey = "scheduled-test-runner"
	scheduledTestRunnerLeaderLockTTL = 10 * time.Minute
	scheduledQualityReasonPrefix     = "scheduled_quality_check:"
)

// ScheduledTestRunnerService periodically scans due test plans and executes them.
type ScheduledTestRunnerService struct {
	planRepo       ScheduledTestPlanRepository
	scheduledSvc   *ScheduledTestService
	accountTestSvc *AccountTestService
	rateLimitSvc   *RateLimitService
	cfg            *config.Config
	qualityCheck   func(string, string) (string, string)

	// lockCache/db elect one process to execute each cron tick across all
	// instances. With no backend configured the existing single-instance/test
	// behavior remains ungated.
	lockCache  LeaderLockCache
	db         *sql.DB
	instanceID string
	// alignmentDelay is kept injectable so lock behavior can be tested without
	// waiting for the production ten-second alignment delay.
	alignmentDelay time.Duration

	cron      *cron.Cron
	startOnce sync.Once
	stopOnce  sync.Once
}

// NewScheduledTestRunnerService creates a new runner.
func NewScheduledTestRunnerService(
	planRepo ScheduledTestPlanRepository,
	scheduledSvc *ScheduledTestService,
	accountTestSvc *AccountTestService,
	rateLimitSvc *RateLimitService,
	cfg *config.Config,
) *ScheduledTestRunnerService {
	return &ScheduledTestRunnerService{
		planRepo:       planRepo,
		scheduledSvc:   scheduledSvc,
		accountTestSvc: accountTestSvc,
		rateLimitSvc:   rateLimitSvc,
		cfg:            cfg,
		instanceID:     uuid.NewString(),
		alignmentDelay: 10 * time.Second,
	}
}

// SetLeaderLock injects the shared lock backends used to coordinate scheduled
// tests across API and worker instances.
func (s *ScheduledTestRunnerService) SetLeaderLock(lockCache LeaderLockCache, db *sql.DB) {
	if s == nil {
		return
	}
	s.lockCache = lockCache
	s.db = db
}

// Start begins the cron ticker (every minute).
func (s *ScheduledTestRunnerService) Start() {
	if s == nil {
		return
	}
	if parseDebugEnvBool(os.Getenv(disableScheduledTestRunnerEnv)) {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] disabled by %s", disableScheduledTestRunnerEnv)
		return
	}
	s.startOnce.Do(func() {
		loc := time.Local
		if s.cfg != nil {
			if parsed, err := time.LoadLocation(s.cfg.Timezone); err == nil && parsed != nil {
				loc = parsed
			}
		}

		c := cron.New(cron.WithParser(scheduledTestCronParser), cron.WithLocation(loc))
		_, err := c.AddFunc("* * * * *", func() { s.runScheduled() })
		if err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] not started (invalid schedule): %v", err)
			return
		}
		s.cron = c
		s.cron.Start()
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] started (tick=every minute)")
	})
}

// Stop gracefully shuts down the cron scheduler.
func (s *ScheduledTestRunnerService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		if s.cron != nil {
			ctx := s.cron.Stop()
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
				logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] cron stop timed out")
			}
		}
	})
}

func (s *ScheduledTestRunnerService) runScheduled() {
	lockCtx, cancelLock := context.WithTimeout(context.Background(), 2*time.Second)
	release, acquired := s.tryAcquireLeaderLock(lockCtx)
	cancelLock()
	if !acquired {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] tick skipped: this instance is not leader")
		return
	}
	defer release()

	// Delay 10s so execution lands at ~:10 of each minute instead of :00.
	if s.alignmentDelay > 0 {
		time.Sleep(s.alignmentDelay)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	now := time.Now()
	plans, err := s.planRepo.ListDue(ctx, now)
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] ListDue error: %v", err)
		return
	}

	if len(plans) > 0 {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] found %d due plans", len(plans))

		sem := make(chan struct{}, scheduledTestDefaultMaxWorkers)
		var wg sync.WaitGroup

		for _, plan := range plans {
			sem <- struct{}{}
			wg.Add(1)
			go func(p *ScheduledTestPlan) {
				defer wg.Done()
				defer func() { <-sem }()
				s.runOnePlan(ctx, p)
			}(plan)
		}

		wg.Wait()
	}
}

func (s *ScheduledTestRunnerService) tryAcquireLeaderLock(ctx context.Context) (func(), bool) {
	if s.lockCache == nil {
		return tryAcquireSingletonLeaderLock(ctx, nil, s.db, scheduledTestRunnerLeaderLockKey, s.instanceID, scheduledTestRunnerLeaderLockTTL)
	}
	// A peer may still hold Redis leadership when this instance loses Redis.
	// Switching lock backends would allow the same plan to run twice.
	acquired, err := s.lockCache.TryAcquireLeaderLock(ctx, scheduledTestRunnerLeaderLockKey, s.instanceID, scheduledTestRunnerLeaderLockTTL)
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] lock unavailable; skipping tick: %v", err)
		return nil, false
	}
	if !acquired {
		return nil, false
	}
	return func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.lockCache.ReleaseLeaderLock(releaseCtx, scheduledTestRunnerLeaderLockKey, s.instanceID)
	}, true
}

func (s *ScheduledTestRunnerService) runOnePlan(ctx context.Context, plan *ScheduledTestPlan) {
	result, err := s.accountTestSvc.RunTestBackground(ctx, plan.AccountID, plan.ModelID, plan.PromptText)
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d RunTestBackground error: %v", plan.ID, err)
		return
	}

	s.completePlanRun(ctx, plan, result)
}

func (s *ScheduledTestRunnerService) completePlanRun(ctx context.Context, plan *ScheduledTestPlan, result *ScheduledTestResult) {
	if result != nil && result.Status == "success" {
		var status, reason string
		if s.qualityCheck != nil {
			status, reason = s.qualityCheck(result.ResponseText, plan.PromptText)
		} else {
			status, reason = s.accountTestSvc.assessScheduledVisualQuality(ctx, plan, result.ResponseText)
		}
		if status != "success" {
			result.Status = status
			result.ErrorMessage = reason
		}
	}
	if err := s.scheduledSvc.SaveResult(ctx, plan.ID, plan.MaxResults, result); err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d SaveResult error: %v", plan.ID, err)
	} else {
		// Never make a scheduling decision from old history after a failed write.
		s.updateScheduledQualityState(ctx, plan, result)
	}

	now := time.Now()
	nextRun, err := computeNextRun(plan.CronExpression, now)
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d computeNextRun error: %v", plan.ID, err)
		return
	}
	if err := s.planRepo.UpdateAfterRun(ctx, plan.ID, now, nextRun); err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d UpdateAfterRun error: %v", plan.ID, err)
	}
}

func (s *ScheduledTestRunnerService) updateScheduledQualityState(ctx context.Context, plan *ScheduledTestPlan, result *ScheduledTestResult) {
	if s == nil || s.accountTestSvc == nil || s.accountTestSvc.accountRepo == nil || plan == nil || result == nil || result.ID <= 0 || result.PlanID != plan.ID {
		return
	}
	if result.Status != "success" && result.Status != "degraded" {
		return
	}
	if result.Status == "degraded" {
		if s.scheduledSvc == nil {
			return
		}
		consecutive, err := s.scheduledSvc.hasConsecutiveDegradedResults(ctx, result)
		if err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d quality history read failed: %v", plan.ID, err)
			return
		}
		if !consecutive {
			return
		}
	}

	account, err := s.accountTestSvc.accountRepo.GetByID(ctx, plan.AccountID)
	if err != nil || account == nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d quality state read failed: %v", plan.ID, plan.AccountID, err)
		return
	}
	if result.Status == "degraded" {
		// A persistent switch cannot expire while the next test is still running.
		// Keep every unrelated error, cooldown and temporary-ban reason intact.
		if account.Schedulable {
			if err := s.accountTestSvc.accountRepo.SetSchedulable(ctx, plan.AccountID, false); err != nil {
				logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d quality pause failed: %v", plan.ID, plan.AccountID, err)
				return
			}
			logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d paused after consecutive degraded results", plan.ID, plan.AccountID)
		}
		return
	}

	// Explicit success enables this account even when the legacy auto_recover
	// flag is off. It does not prove other runtime cooldowns are safe to clear.
	s.tryRecoverAccount(ctx, plan, account)
}

func (s *ScheduledTestRunnerService) tryRecoverAccount(ctx context.Context, plan *ScheduledTestPlan, account *Account) {
	repo := s.accountTestSvc.accountRepo
	if strings.HasPrefix(account.TempUnschedulableReason, scheduledQualityReasonPrefix) {
		observedReason := account.TempUnschedulableReason
		if s.scheduledSvc == nil {
			return
		}
		cleared, err := s.scheduledSvc.clearScheduledQualityPause(ctx, plan.AccountID, observedReason)
		if err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d quality recovery failed: %v", plan.ID, err)
			return
		}
		if !cleared {
			// The observed reason changed. Do not clear a newer runtime block.
			return
		}
		if s.rateLimitSvc != nil && s.rateLimitSvc.tempUnschedCache != nil {
			cache, ok := s.rateLimitSvc.tempUnschedCache.(interface {
				DeleteTempUnschedIfReason(context.Context, int64, string) (bool, error)
			})
			if !ok {
				logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d conditional quality cache recovery unavailable", plan.ID)
				return
			}
			if _, err := cache.DeleteTempUnschedIfReason(ctx, plan.AccountID, observedReason); err != nil {
				logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d quality cache recovery failed: %v", plan.ID, err)
				return
			}
		}
	}

	if account.Status == StatusError {
		// ClearError is a targeted, conditional update and refreshes snapshots.
		if err := repo.ClearError(ctx, plan.AccountID); err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d error recovery failed: %v", plan.ID, err)
			return
		}
		if s.rateLimitSvc != nil && s.rateLimitSvc.tokenCacheInvalidator != nil && account.IsOAuth() {
			if err := s.rateLimitSvc.tokenCacheInvalidator.InvalidateToken(ctx, account); err != nil {
				logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d token invalidation failed: %v", plan.ID, err)
			}
		}
	} else if account.Status != StatusActive || !account.Schedulable {
		active, schedulable := StatusActive, true
		// The existing partial-update API is restricted to this one tested ID.
		if _, err := repo.BulkUpdate(ctx, []int64{plan.AccountID}, AccountBulkUpdate{Status: &active, Schedulable: &schedulable}); err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d scheduling recovery failed: %v", plan.ID, err)
			return
		}
	}
}
