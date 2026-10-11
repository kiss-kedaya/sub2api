package service

import (
	"context"
	"database/sql"
	"net/http"
	"os"
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
)

// ScheduledTestRunnerService periodically scans due test plans and executes them.
type ScheduledTestRunnerService struct {
	planRepo       ScheduledTestPlanRepository
	scheduledSvc   *ScheduledTestService
	accountTestSvc *AccountTestService
	rateLimitSvc   *RateLimitService
	cfg            *config.Config
	chanshuiClient *http.Client

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
	if plan.QualityCheckEnabled {
		result, wait, err := s.runChanshuiPlan(ctx, plan)
		if err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d audit step failed: %v", plan.ID, err)
			return
		}
		if wait > 0 {
			if repo, ok := s.planRepo.(ScheduledChanshuiRepository); ok {
				if err := repo.DeferChanshuiPlan(ctx, plan.ID, time.Now().Add(wait)); err != nil {
					logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d audit defer failed", plan.ID)
				}
			}
			return
		}
		s.completePlanRun(ctx, plan, result)
		return
	}
	var (
		result *ScheduledTestResult
		err    error
	)
	// Plain plans keep the upstream behavior: the platform default test
	// prompt, no artwork generation.
	result, err = s.accountTestSvc.RunTestBackground(ctx, plan.AccountID, plan.ModelID)
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d RunTestBackground error: %v", plan.ID, err)
		return
	}

	s.completePlanRun(ctx, plan, result)
}

func (s *ScheduledTestRunnerService) completePlanRun(ctx context.Context, plan *ScheduledTestPlan, result *ScheduledTestResult) {
	if err := s.scheduledSvc.SaveResult(ctx, plan.ID, plan.MaxResults, result); err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d SaveResult error: %v", plan.ID, err)
	} else if result != nil {
		if plan.QualityCheckEnabled {
			if result.Status == "audit_pass" || result.Status == "audit_fail" {
				if s.accountTestSvc == nil {
					return
				}
				stateRepo, ok := s.accountTestSvc.accountRepo.(ScheduledChanshuiAccountState)
				if !ok {
					logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d conditional audit scheduling unavailable", plan.ID)
					return
				}
				if _, err := stateRepo.ApplyChanshuiQualityState(ctx, plan.AccountID, plan.ID, plan.UpdatedAt, result.Status == "audit_fail"); err != nil {
					logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d audit scheduling update failed", plan.ID)
					return
				}
			}
			if repo, ok := s.planRepo.(ScheduledChanshuiRepository); ok {
				if err := repo.SetChanshuiAudit(ctx, plan.ID, nil); err != nil {
					logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d audit state clear failed", plan.ID)
					return
				}
			}
		} else if result.Status == "success" && plan.AutoRecover && s.rateLimitSvc != nil {
			// Upstream parity for plain plans: a passing test clears recoverable
			// runtime state only when the plan opted into auto recovery.
			if _, err := s.rateLimitSvc.RecoverAccountAfterSuccessfulTest(ctx, plan.AccountID); err != nil {
				logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d auto-recover failed: %v", plan.ID, err)
			}
		}
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

