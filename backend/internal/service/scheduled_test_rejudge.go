package service

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const (
	// qualityRejudgeMarkerPrefix marks a result whose re-check already ran and
	// still returned degraded, so it is never re-checked again.
	qualityRejudgeMarkerPrefix = "rechecked; "
	// qualityRejudgeBatch and qualityRejudgeWorkers bound the work added to a
	// single runner tick so normal scheduled tests keep their cadence.
	qualityRejudgeBatch   = 8
	qualityRejudgeWorkers = 4
	// qualityRejudgeTimeout bounds one candidate end to end (render + review).
	qualityRejudgeTimeout = 210 * time.Second
	// qualityRejudgeBudget stops launching new candidates once the phase ran
	// this long; already running ones still finish and the rest wait for the
	// next tick.
	qualityRejudgeBudget = 4 * time.Minute
)

// qualityRejudgeCutoff is the moment the lenient visual rubric (v2.0.19) took
// over the scheduled runner on 2026-09-28. Verdicts stored before it were
// produced by the older, stricter reviewer and are re-checked once.
var qualityRejudgeCutoff = time.Date(2026, 9, 28, 11, 42, 0, 0, time.UTC)

// rejudgeLegacyQualityResults re-checks historical degraded verdicts with the
// current rubric and rewrites the stored verdict when it no longer holds. It
// runs after the due plans of the tick, is bounded per tick, and stops by
// itself once every pre-cutoff verdict has been re-checked.
func (s *ScheduledTestRunnerService) rejudgeLegacyQualityResults(ctx context.Context) {
	if s == nil || s.scheduledSvc == nil || s.accountTestSvc == nil || s.accountTestSvc.accountRepo == nil || s.planRepo == nil {
		return
	}
	queryCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	candidates, err := s.scheduledSvc.ListRejudgeCandidates(queryCtx, qualityRejudgeCutoff, qualityRejudgeBatch)
	cancel()
	if err != nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[QualityRejudge] candidate query failed: %v", err)
		return
	}
	if len(candidates) == 0 {
		return
	}

	assess := s.rejudgeAssess
	if assess == nil {
		assess = s.accountTestSvc.assessScheduledVisualQuality
	}

	var (
		wg                 sync.WaitGroup
		sem                = make(chan struct{}, qualityRejudgeWorkers)
		processed, flipped int
		kept, inconclusive int
		mu                 sync.Mutex
	)
	started := time.Now()
	for _, candidate := range candidates {
		if candidate == nil || candidate.ID <= 0 {
			continue
		}
		if time.Since(started) > qualityRejudgeBudget {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(result *ScheduledTestResult) {
			defer wg.Done()
			defer func() { <-sem }()
			outcome := s.rejudgeOneResult(assess, result)
			mu.Lock()
			defer mu.Unlock()
			processed++
			switch outcome {
			case "success":
				flipped++
			case "degraded":
				kept++
			default:
				inconclusive++
			}
		}(candidate)
	}
	wg.Wait()
	logger.LegacyPrintf("service.scheduled_test_runner",
		"[QualityRejudge] batch done: processed=%d flipped=%d kept=%d inconclusive=%d",
		processed, flipped, kept, inconclusive)
}

// rejudgeOneResult re-checks one stored verdict. It returns the outcome so the
// batch summary can count it. Every write is a targeted status/message update;
// scheduling state is never touched here.
func (s *ScheduledTestRunnerService) rejudgeOneResult(assess func(context.Context, *ScheduledTestPlan, string) (string, string), result *ScheduledTestResult) string {
	readCtx, readCancel := context.WithTimeout(context.Background(), 10*time.Second)
	plan, err := s.planRepo.GetByID(readCtx, result.PlanID)
	readCancel()
	if err != nil || plan == nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[QualityRejudge] result=%d plan read failed: %v", result.ID, err)
		return "error"
	}
	readCtx, readCancel = context.WithTimeout(context.Background(), 10*time.Second)
	account, err := s.accountTestSvc.accountRepo.GetByID(readCtx, plan.AccountID)
	readCancel()
	if err != nil || account == nil {
		logger.LegacyPrintf("service.scheduled_test_runner", "[QualityRejudge] result=%d account=%d read failed: %v", result.ID, plan.AccountID, err)
		return "error"
	}

	reviewCtx, cancel := context.WithTimeout(context.Background(), qualityRejudgeTimeout)
	defer cancel()
	status, reason := assess(reviewCtx, plan, result.ResponseText)

	writeCtx, writeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer writeCancel()
	switch status {
	case "success":
		if err := s.scheduledSvc.UpdateResultStatus(writeCtx, result.ID, "success", ""); err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[QualityRejudge] result=%d success update failed: %v", result.ID, err)
			return "error"
		}
		logger.LegacyPrintf("service.scheduled_test_runner", "[QualityRejudge] result=%d plan=%d account=%d degraded -> success", result.ID, plan.ID, plan.AccountID)
		return "success"
	case "degraded":
		message := qualityRejudgeMarkerPrefix + strings.TrimPrefix(reason, "quality check failed: ")
		message = "quality check failed: " + message
		if err := s.scheduledSvc.UpdateResultStatus(writeCtx, result.ID, "degraded", message); err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[QualityRejudge] result=%d degraded update failed: %v", result.ID, err)
			return "error"
		}
		logger.LegacyPrintf("service.scheduled_test_runner", "[QualityRejudge] result=%d plan=%d account=%d degraded confirmed again", result.ID, plan.ID, plan.AccountID)
		return "degraded"
	default:
		message := "quality check inconclusive: re-checked with the current rubric"
		if reason != "" {
			message = reason + " (re-checked)"
		}
		if err := s.scheduledSvc.UpdateResultStatus(writeCtx, result.ID, "unknown", message); err != nil {
			logger.LegacyPrintf("service.scheduled_test_runner", "[QualityRejudge] result=%d unknown update failed: %v", result.ID, err)
			return "error"
		}
		logger.LegacyPrintf("service.scheduled_test_runner", "[QualityRejudge] result=%d plan=%d account=%d degraded -> inconclusive", result.ID, plan.ID, plan.AccountID)
		return "unknown"
	}
}
