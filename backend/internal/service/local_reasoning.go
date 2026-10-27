package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/localreasoning"
)

func ValidateLocalReasoningPlan(p LocalReasoningPlan) error {
	if p.AccountID <= 0 || strings.TrimSpace(p.Model) == "" || len(p.Model) > 200 || strings.ContainsAny(p.Model, "\r\n\t") || p.IntervalMinutes < 5 || p.IntervalMinutes > 10080 || p.Revision < 0 || (p.Action != "observe" && p.Action != "pause") {
		return errors.New("invalid local reasoning plan")
	}
	if p.JudgeKind != "" && p.JudgeKind != "local" && p.JudgeKind != "llm" && p.JudgeKind != "jev" {
		return errors.New("invalid judge kind")
	}
	if p.JudgeKind == "llm" {
		if p.JudgeGroupID <= 0 || strings.TrimSpace(p.JudgeModel) == "" || len(p.JudgeModel) > 200 || strings.ContainsAny(p.JudgeModel, "\r\n\t") {
			return errors.New("LLM judge requires a group and model")
		}
	} else if p.JudgeGroupID != 0 || p.JudgeModel != "" {
		return errors.New("local and Jev judges do not use business group or model")
	}
	return nil
}
func (s *OpsAutomationService) ListLocalPlans(ctx context.Context) ([]LocalReasoningPlan, error) {
	return s.repo.ListLocalPlans(ctx)
}
func (s *OpsAutomationService) SaveLocalPlan(ctx context.Context, p LocalReasoningPlan) (*LocalReasoningPlan, error) {
	if p.JudgeKind == "" {
		p.JudgeKind = "local"
	}
	if err := ValidateLocalReasoningPlan(p); err != nil {
		return nil, err
	}
	if p.JudgeKind == "llm" {
		g, e := s.groups.GetByID(ctx, p.JudgeGroupID)
		if e != nil || g == nil || g.Status != StatusActive || g.ModelAllowlistEnabled() && !g.ModelAllowlist.Allows(p.JudgeModel) {
			return nil, errors.New("judge group or model unavailable")
		}
	}
	return s.repo.SaveLocalPlan(ctx, p)
}
func (s *OpsAutomationService) LocalOwnership(ctx context.Context, id int64, fence string, restore, remove bool) (*LocalReasoningOwnership, error) {
	return s.repo.LocalOwnership(ctx, id, fence, restore, remove)
}
func (s *OpsAutomationService) RunLocalPlan(ctx context.Context, id int64) error {
	if id <= 0 {
		return errors.New("plan ID is required")
	}
	p, err := s.repo.ClaimLocalPlan(ctx, id)
	if err != nil {
		return err
	}
	if p == nil {
		return errors.New("plan is busy or unavailable")
	}
	return s.runLocalPlan(ctx, p)
}
func (s *OpsAutomationService) runDueLocal(ctx context.Context) {
	for n := 0; n < 2; n++ {
		query, stop := context.WithTimeout(ctx, 3*time.Second)
		p, err := s.repo.ClaimLocalPlan(query, 0)
		stop()
		if err != nil || p == nil {
			return
		}
		_ = s.runLocalPlan(ctx, p)
	}
}
func (s *OpsAutomationService) runLocalPlan(ctx context.Context, p *LocalReasoningPlan) error {
	q, err := localreasoning.Generate(nil)
	if err != nil {
		return err
	}
	result := LocalReasoningResult{QuestionID: q.ID, Family: q.Family, Outcome: "inconclusive"}
	started := time.Now()
	query, stop := context.WithTimeout(ctx, 150*time.Second)
	if s.tester != nil {
		sampleCtx, sampleStop := context.WithTimeout(query, 90*time.Second)
		sample, e := runLocalQualitySample(sampleCtx, s.tester, p.AccountID, p.Model, q.Prompt)
		sampleCancelled := sampleCtx.Err() != nil
		sampleStop()
		if e == nil && !sampleCancelled && query.Err() == nil && sample != nil && sample.Status == "success" {
			result.Outcome = "failed"
			if p.JudgeKind == "llm" || p.JudgeKind == "jev" {
				result.Outcome = "inconclusive"
				if s.judge != nil {
					result.Outcome = s.judge.Judge(query, p, q, sample.ResponseText)
				}
			} else if localreasoning.Matches(q, sample.ResponseText) {
				result.Outcome = "passed"
			}
		}
	}
	stop()
	result.DurationMS = time.Since(started).Milliseconds()
	save, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	return s.repo.CompleteLocalPlan(save, p, result)
}

func HasLocalReasoningPause(a *Account) bool {
	if a == nil {
		return false
	}
	v, ok := a.Extra["local_reasoning_quality_pauses"].(map[string]any)
	return ok && len(v) > 0
}
