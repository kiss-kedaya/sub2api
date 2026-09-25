package service

import (
	"context"
	"database/sql"
	"os"
	"strconv"
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
	gqcRepo        GroupQualityCheckRepository
	cfg            *config.Config

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

// SetGroupQualityCheckRepo injects the group quality check repository used by
// the group-level degradation detection pass.
func (s *ScheduledTestRunnerService) SetGroupQualityCheckRepo(repo GroupQualityCheckRepository) {
	if s == nil {
		return
	}
	s.gqcRepo = repo
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
	release, acquired := tryAcquireSingletonLeaderLock(lockCtx, s.lockCache, s.db, scheduledTestRunnerLeaderLockKey, s.instanceID, scheduledTestRunnerLeaderLockTTL)
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
	if len(plans) == 0 {
		return
	}

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

	s.runGroupQualityChecks(ctx)
}

func (s *ScheduledTestRunnerService) runOnePlan(ctx context.Context, plan *ScheduledTestPlan) {
	result, err := s.accountTestSvc.RunTestBackground(ctx, plan.AccountID, plan.ModelID, plan.PromptText)
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d RunTestBackground error: %v", plan.ID, err)
		return
	}

	if err := s.scheduledSvc.SaveResult(ctx, plan.ID, plan.MaxResults, result); err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d SaveResult error: %v", plan.ID, err)
	}

	now := time.Now()
	nextRun, err := computeNextRun(plan.CronExpression, now)
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d computeNextRun error: %v", plan.ID, err)
		return
	}

	s.updateScheduledQualityState(ctx, plan, result, nextRun)

	// Auto-recover account if test succeeded and auto_recover is enabled.
	if result.Status == "success" && plan.AutoRecover {
		s.tryRecoverAccount(ctx, plan.AccountID, plan.ID)
	}

	if err := s.planRepo.UpdateAfterRun(ctx, plan.ID, now, nextRun); err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d UpdateAfterRun error: %v", plan.ID, err)
	}
}

func (s *ScheduledTestRunnerService) updateScheduledQualityState(ctx context.Context, plan *ScheduledTestPlan, result *ScheduledTestResult, nextRun time.Time) {
	if s == nil || s.accountTestSvc == nil || s.accountTestSvc.accountRepo == nil || result == nil {
		return
	}

	account, err := s.accountTestSvc.accountRepo.GetByID(ctx, plan.AccountID)
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d quality state read failed: %v", plan.ID, plan.AccountID, err)
		return
	}

	if result.Status == "degraded" {
		if account.TempUnschedulableUntil != nil && account.TempUnschedulableUntil.After(time.Now()) &&
			!strings.HasPrefix(account.TempUnschedulableReason, scheduledQualityReasonPrefix) {
			logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d quality pause skipped: another temporary reason is active", plan.ID, plan.AccountID)
			return
		}
		reason := scheduledQualityReasonPrefix + " plan=" + strconv.FormatInt(plan.ID, 10) + " " + result.ErrorMessage
		if err := s.accountTestSvc.accountRepo.SetTempUnschedulable(ctx, plan.AccountID, nextRun, reason); err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d quality pause failed: %v", plan.ID, plan.AccountID, err)
			return
		}
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d paused until next quality check", plan.ID, plan.AccountID)
		return
	}

	if result.Status == "success" && strings.HasPrefix(account.TempUnschedulableReason, scheduledQualityReasonPrefix) {
		if err := s.accountTestSvc.accountRepo.ClearTempUnschedulable(ctx, plan.AccountID); err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d quality recovery failed: %v", plan.ID, plan.AccountID, err)
			return
		}
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d account=%d quality recovered", plan.ID, plan.AccountID)
	}
}

// runGroupQualityChecks probes a few schedulable accounts per enabled group on
// their configured interval, records the results, and pauses degraded accounts
// until the next group check. It shares the runner's leader lock so only one
// instance executes it per tick.
func (s *ScheduledTestRunnerService) runGroupQualityChecks(ctx context.Context) {
	if s == nil || s.gqcRepo == nil || s.accountTestSvc == nil || s.accountTestSvc.accountRepo == nil {
		return
	}

	now := time.Now()
	settings, err := s.gqcRepo.ListEnabledSettings(ctx)
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[GroupQualityCheck] ListEnabledSettings error: %v", err)
		return
	}

	type dueGroup struct {
		settings *GroupQualityCheckSettings
		accounts []Account
	}
	var due []dueGroup
	for _, setting := range settings {
		interval := time.Duration(setting.IntervalMinutes) * time.Minute
		if interval <= 0 {
			interval = defaultGroupQualityCheckIntervalMinutes * time.Minute
		}
		if setting.LastRunAt != nil && setting.LastRunAt.Add(interval).After(now) {
			continue
		}
		accounts, err := s.accountTestSvc.accountRepo.ListSchedulableByGroupID(ctx, setting.GroupID)
		if err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[GroupQualityCheck] group=%d list accounts error: %v", setting.GroupID, err)
			continue
		}
		if len(accounts) == 0 {
			logger.LegacyPrintf("service.scheduled_test_runner", "[GroupQualityCheck] group=%d has no schedulable accounts", setting.GroupID)
			continue
		}
		due = append(due, dueGroup{settings: setting, accounts: accounts})
	}

	if len(due) == 0 {
		return
	}

	// Cap concurrent groups (3) and keep per-group probes at a small sample (5)
	// so detection stays cheap while covering the group.
	const maxGroupsConcurrent = 3
	const maxAccountsPerGroup = 5

	sem := make(chan struct{}, maxGroupsConcurrent)
	var wg sync.WaitGroup

	for _, item := range due {
		sem <- struct{}{}
		wg.Add(1)
		go func(dg dueGroup) {
			defer wg.Done()
			defer func() { <-sem }()
			s.runOneGroupQualityCheck(ctx, dg.settings, dg.accounts, maxAccountsPerGroup)
		}(item)
	}

	wg.Wait()
}

func (s *ScheduledTestRunnerService) runOneGroupQualityCheck(ctx context.Context, setting *GroupQualityCheckSettings, accounts []Account, maxAccounts int) {
	groupID := setting.GroupID
	interval := time.Duration(setting.IntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = defaultGroupQualityCheckIntervalMinutes * time.Minute
	}
	now := time.Now()
	pauseUntil := now.Add(interval)

	if len(accounts) > maxAccounts {
		accounts = accounts[:maxAccounts]
	}

	for _, account := range accounts {
		result, err := s.accountTestSvc.RunTestBackground(ctx, account.ID, "", DefaultScheduledTestPrompt)
		if err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[GroupQualityCheck] group=%d account=%d RunTestBackground error: %v", groupID, account.ID, err)
			continue
		}
		if _, err := s.gqcRepo.CreateResult(ctx, &GroupQualityCheckResult{
			GroupID:      groupID,
			AccountID:    account.ID,
			Status:       result.Status,
			ErrorMessage: result.ErrorMessage,
			LatencyMs:    result.LatencyMs,
		}); err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[GroupQualityCheck] group=%d account=%d CreateResult error: %v", groupID, account.ID, err)
		}

		switch result.Status {
		case "degraded":
			if account.TempUnschedulableUntil != nil && account.TempUnschedulableUntil.After(now) &&
				!strings.HasPrefix(account.TempUnschedulableReason, scheduledQualityReasonPrefix) {
				logger.LegacyPrintf("service.scheduled_test_runner", "[GroupQualityCheck] group=%d account=%d pause skipped: another temporary reason is active", groupID, account.ID)
				continue
			}
			reason := scheduledQualityReasonPrefix + " group=" + strconv.FormatInt(groupID, 10) + " " + result.ErrorMessage
			if err := s.accountTestSvc.accountRepo.SetTempUnschedulable(ctx, account.ID, pauseUntil, reason); err != nil {
				logger.LegacyPrintf("service.scheduled_test_runner", "[GroupQualityCheck] group=%d account=%d pause failed: %v", groupID, account.ID, err)
				continue
			}
			logger.LegacyPrintf("service.scheduled_test_runner", "[GroupQualityCheck] group=%d account=%d degraded, paused until next check", groupID, account.ID)
		case "success":
			if strings.HasPrefix(account.TempUnschedulableReason, scheduledQualityReasonPrefix) {
				if err := s.accountTestSvc.accountRepo.ClearTempUnschedulable(ctx, account.ID); err != nil {
					logger.LegacyPrintf("service.scheduled_test_runner", "[GroupQualityCheck] group=%d account=%d recovery failed: %v", groupID, account.ID, err)
				}
			}
		}
	}

	if err := s.gqcRepo.UpdateLastRun(ctx, groupID, now); err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[GroupQualityCheck] group=%d UpdateLastRun error: %v", groupID, err)
	}
	logger.LegacyPrintf("service.scheduled_test_runner", "[GroupQualityCheck] group=%d checked %d accounts", groupID, len(accounts))
}

// tryRecoverAccount restores account runtime state with the same path as
// POST /api/v1/admin/accounts/:id/recover-state.
func (s *ScheduledTestRunnerService) tryRecoverAccount(ctx context.Context, accountID int64, planID int64) {
	if s.rateLimitSvc == nil {
		return
	}

	recovery, err := s.rateLimitSvc.RecoverAccountState(ctx, accountID, AccountRecoveryOptions{
		InvalidateToken: true,
	})
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d auto-recover failed: %v", planID, err)
		return
	}
	if recovery == nil {
		return
	}

	if recovery.ClearedError {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d auto-recover: account=%d recovered from error status", planID, accountID)
	}
	if recovery.ClearedRateLimit {
		logger.LegacyPrintf("service.scheduled_test_runner", "[ScheduledTestRunner] plan=%d auto-recover: account=%d cleared rate-limit/runtime state", planID, accountID)
	}
}
