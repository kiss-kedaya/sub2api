package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

const (
	DefaultScheduledTestPrompt = "请生成可直接运行的单文件HTML，使用内联SVG绘制鹈鹕骑自行车的二维循环动画。画面以鹈鹕和自行车为主体，展示清晰的身体结构、踩踏动作和车轮转动，配合协调的背景、配色与层次。动画应流畅自然、衔接连续，并适配不同屏幕尺寸。动画必须用 CSS @keyframes 或 SMIL（animate/animateTransform）实现，不要使用 JavaScript 或 <script> 标签。禁止依赖外部资源，只输出完整HTML，不要代码围栏或解释文字。"
	DefaultScheduledTestCron   = "*/5 * * * *"
	maxScheduledTestPromptSize = 32 * 1024
)

var scheduledTestCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ScheduledTestService provides CRUD operations for scheduled test plans and results.
type ScheduledTestService struct {
	planRepo   ScheduledTestPlanRepository
	resultRepo ScheduledTestResultRepository
}

// NewScheduledTestService creates a new ScheduledTestService.
func NewScheduledTestService(
	planRepo ScheduledTestPlanRepository,
	resultRepo ScheduledTestResultRepository,
) *ScheduledTestService {
	return &ScheduledTestService{
		planRepo:   planRepo,
		resultRepo: resultRepo,
	}
}

// CreatePlan validates the cron expression, computes next_run_at, and persists the plan.
func (s *ScheduledTestService) CreatePlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	if err := normalizeScheduledTestPlan(plan); err != nil {
		return nil, err
	}
	nextRun, err := computeNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun

	if plan.MaxResults <= 0 {
		plan.MaxResults = 50
	}

	return s.planRepo.Create(ctx, plan)
}

// GetPlan retrieves a plan by ID.
func (s *ScheduledTestService) GetPlan(ctx context.Context, id int64) (*ScheduledTestPlan, error) {
	return s.planRepo.GetByID(ctx, id)
}

// ListPlansByAccount returns all plans for a given account.
func (s *ScheduledTestService) ListPlansByAccount(ctx context.Context, accountID int64) ([]*ScheduledTestPlan, error) {
	return s.planRepo.ListByAccountID(ctx, accountID)
}

// UpdatePlan validates cron and updates the plan.
func (s *ScheduledTestService) UpdatePlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	if err := normalizeScheduledTestPlan(plan); err != nil {
		return nil, err
	}
	nextRun, err := computeNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun

	return s.planRepo.Update(ctx, plan)
}

func normalizeScheduledTestPlan(plan *ScheduledTestPlan) error {
	if plan == nil {
		return fmt.Errorf("scheduled test plan is required")
	}
	plan.PromptText = strings.TrimSpace(plan.PromptText)
	if plan.PromptText == "" {
		plan.PromptText = DefaultScheduledTestPrompt
	}
	if len([]byte(plan.PromptText)) > maxScheduledTestPromptSize {
		return fmt.Errorf("scheduled test prompt is too long")
	}
	plan.CronExpression = strings.TrimSpace(plan.CronExpression)
	if plan.CronExpression == "" {
		plan.CronExpression = DefaultScheduledTestCron
	}
	return nil
}

// DeletePlan removes a plan and its results (via CASCADE).
func (s *ScheduledTestService) DeletePlan(ctx context.Context, id int64) error {
	return s.planRepo.Delete(ctx, id)
}

// ListResults returns the most recent results for a plan.
func (s *ScheduledTestService) ListResults(ctx context.Context, planID int64, limit int) ([]*ScheduledTestResult, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.resultRepo.ListByPlanID(ctx, planID, limit)
}

// SaveResult inserts a result and keeps at least the two records needed for
// consecutive-quality decisions, even when the display retention is one.
func (s *ScheduledTestService) SaveResult(ctx context.Context, planID int64, maxResults int, result *ScheduledTestResult) error {
	if result == nil {
		return fmt.Errorf("scheduled test result is required")
	}
	result.ID = 0
	result.PlanID = planID
	saved, err := s.resultRepo.Create(ctx, result)
	if err != nil {
		return err
	}
	if saved == nil || saved.ID <= 0 || saved.PlanID != planID {
		return fmt.Errorf("scheduled test result was not persisted")
	}
	*result = *saved
	return s.resultRepo.PruneOldResults(ctx, planID, max(maxResults, 2))
}

func (s *ScheduledTestService) hasConsecutiveDegradedResults(ctx context.Context, current *ScheduledTestResult) (bool, error) {
	if current == nil || current.ID <= 0 || current.Status != "degraded" {
		return false, nil
	}
	results, err := s.resultRepo.ListByPlanID(ctx, current.PlanID, 2)
	if err != nil {
		return false, err
	}
	if len(results) < 2 || results[0] == nil || results[1] == nil {
		return false, nil
	}
	latest, previous := results[0], results[1]
	return latest.ID == current.ID && previous.ID > 0 && previous.ID != latest.ID &&
		latest.PlanID == current.PlanID && previous.PlanID == current.PlanID &&
		latest.Status == "degraded" && previous.Status == "degraded", nil
}

// Legacy quality pauses share the temporary-ban fields with other subsystems.
// Clearing them requires a compare-and-clear rather than an unconditional reset.
type scheduledQualityPauseRepository interface {
	ClearScheduledQualityPause(context.Context, int64, string) (bool, error)
}

func (s *ScheduledTestService) clearScheduledQualityPause(ctx context.Context, accountID int64, reason string) (bool, error) {
	repo, ok := s.resultRepo.(scheduledQualityPauseRepository)
	if !ok {
		return false, fmt.Errorf("conditional scheduled quality recovery is unavailable")
	}
	return repo.ClearScheduledQualityPause(ctx, accountID, reason)
}

func computeNextRun(cronExpr string, from time.Time) (time.Time, error) {
	sched, err := scheduledTestCronParser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(from), nil
}
