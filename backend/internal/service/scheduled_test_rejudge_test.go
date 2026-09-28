package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type rejudgePlanRepo struct {
	ScheduledTestPlanRepository
	plan *ScheduledTestPlan
	err  error
}

func (r *rejudgePlanRepo) GetByID(context.Context, int64) (*ScheduledTestPlan, error) {
	return r.plan, r.err
}

func newRejudgeRunner(verdict, reason string) (*ScheduledTestRunnerService, *scheduledQualityResultRepo) {
	account := &Account{ID: 2, Status: StatusActive, Schedulable: true}
	accounts := &scheduledQualityAccountRepo{account: account}
	results := &scheduledQualityResultRepo{
		results: []*ScheduledTestResult{
			{ID: 1, PlanID: 7, Status: "degraded", ResponseText: "<html><svg></svg></html>", ErrorMessage: "quality check failed: old rubric"},
			{ID: 2, PlanID: 7, Status: "degraded", ResponseText: "", ErrorMessage: "quality check failed: no artwork"},
		},
	}
	plans := &rejudgePlanRepo{plan: &ScheduledTestPlan{ID: 7, AccountID: 2, QualityCheckEnabled: true, PromptText: DefaultScheduledTestPrompt}}
	runner := &ScheduledTestRunnerService{
		planRepo:       plans,
		scheduledSvc:   NewScheduledTestService(plans, results),
		accountTestSvc: &AccountTestService{accountRepo: accounts},
		rejudgeAssess: func(context.Context, *ScheduledTestPlan, string) (string, string) {
			return verdict, reason
		},
	}
	return runner, results
}

func TestScheduledTestRunnerService_RejudgeRewritesHistoricalVerdicts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		verdict    string
		reason     string
		wantStatus string
		wantErrMsg string
	}{
		{"passes_under_new_rubric", "success", "", "success", ""},
		{"still_degraded_stamps_marker", "degraded", "quality check failed: feet do not plausibly contact crank pedals", "degraded", "quality check failed: rechecked; feet do not plausibly contact crank pedals"},
		{"inconclusive_becomes_unknown", "unknown", "quality check inconclusive: insufficient visual evidence", "unknown", "quality check inconclusive: insufficient visual evidence (re-checked)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, results := newRejudgeRunner(tc.verdict, tc.reason)
			runner.rejudgeLegacyQualityResults(context.Background())
			require.Equal(t, tc.wantStatus, results.results[0].Status)
			require.Equal(t, tc.wantErrMsg, results.results[0].ErrorMessage)
			require.Equal(t, "degraded", results.results[1].Status, "a result without artwork must stay untouched")
			require.Equal(t, "quality check failed: no artwork", results.results[1].ErrorMessage)
		})
	}
}

func TestScheduledTestRunnerService_RejudgeSkipsWhenPlanIsMissing(t *testing.T) {
	runner, results := newRejudgeRunner("success", "")
	runner.planRepo = &rejudgePlanRepo{err: errors.New("plan gone")}
	runner.scheduledSvc = NewScheduledTestService(runner.planRepo, results)
	runner.rejudgeLegacyQualityResults(context.Background())
	require.Equal(t, "degraded", results.results[0].Status)
	require.Equal(t, "quality check failed: old rubric", results.results[0].ErrorMessage)
}

func TestScheduledTestRunnerService_RejudgeStopsWhenNoCandidatesRemain(t *testing.T) {
	runner, results := newRejudgeRunner("success", "")
	runner.rejudgeLegacyQualityResults(context.Background())
	require.Equal(t, "success", results.results[0].Status)
	// A second pass finds nothing left (status no longer degraded).
	runner.rejudgeLegacyQualityResults(context.Background())
	require.Equal(t, "success", results.results[0].Status)
}
