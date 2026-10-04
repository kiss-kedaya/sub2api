package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func chanshuiPolicyData(t *testing.T, raw string) map[string]any {
	t.Helper()
	var d map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &d))
	return d
}
func TestChanshuiDefaultFingerprintDecision(t *testing.T) {
	plan := &ScheduledTestPlan{ModelID: "gpt-6-astra", QualityProvider: QualityProviderChanshui}
	require.NoError(t, normalizeScheduledQualityProvider(plan))
	require.Equal(t, []string{"fingerprint"}, plan.QualityConfig.Sections)
	for _, tc := range []struct{ name, body, want string }{
		{"match despite unrelated incomplete IQ", `{"verdict":{"fingerprint":{"top_model":"gpt-6-astra"},"total":{"score":56},"iq":{"complete":false}},"probes":[{"id":"Q1","status":"queued"}]}`, "audit_pass"},
		{"mismatch is immediate", `{"verdict":{"fingerprint":{"top_model":"gpt-other"}}}`, "audit_fail"},
		{"same family is not identical", `{"verdict":{"fingerprint":{"top_model":"gpt-6-astra-mini"}}}`, "audit_fail"},
		{"missing is unknown", `{"verdict":{"fingerprint":null}}`, "unknown"},
		{"unfinished is unknown", `{"verdict":{"fingerprint":{"top_model":"gpt-other"}},"probes":[{"id":"fingerprint","status":"queued"}]}`, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, evaluateChanshuiStopCondition(plan, chanshuiPolicyData(t, tc.body)).Status)
		})
	}
}

func TestChanshuiCustomStopConditions(t *testing.T) {
	threshold := 80.0
	fp := ChanshuiStopRule{Type: "fingerprint_mismatch"}
	score := ChanshuiStopRule{Type: "total_score_below", Threshold: &threshold}
	for _, tc := range []struct {
		name, match, body, want string
		rules                   []ChanshuiStopRule
	}{
		{"below threshold", "any", `{"verdict":{"total":{"score":79,"max":100}}}`, "audit_fail", []ChanshuiStopRule{score}},
		{"at threshold passes", "any", `{"verdict":{"total":{"score":80,"max":100}}}`, "audit_pass", []ChanshuiStopRule{score}},
		{"unfinished total unknown", "any", `{"verdict":{"total":{"score":56},"iq":{"complete":false}}}`, "unknown", []ChanshuiStopRule{score}},
		{"any definite mismatch suffices", "any", `{"verdict":{"fingerprint":{"top_model":"other"}}}`, "audit_fail", []ChanshuiStopRule{fp, score}},
		{"all requires all evidence", "all", `{"verdict":{"fingerprint":{"top_model":"other"}}}`, "unknown", []ChanshuiStopRule{fp, score}},
		{"all false with complete evidence passes", "all", `{"verdict":{"fingerprint":{"top_model":"other"},"total":{"score":100}}}`, "audit_pass", []ChanshuiStopRule{fp, score}},
		{"literal section status", "any", `{"verdict":{"hidden":{"status":"injected"}}}`, "audit_fail", []ChanshuiStopRule{{Type: "section_status", Section: "hidden", Status: "injected"}}},
		{"unknown is not success", "any", `{"verdict":{"hidden":{"status":"unknown"}}}`, "unknown", []ChanshuiStopRule{{Type: "section_status", Section: "hidden", Status: "injected"}}},
		{"explicit unknown condition allowed", "any", `{"verdict":{"cache":{"status":"unknown"}}}`, "audit_fail", []ChanshuiStopRule{{Type: "section_status", Section: "cache", Status: "unknown"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &ScheduledTestPlan{ModelID: "gpt-test", QualityProvider: QualityProviderChanshui, QualityConfig: ChanshuiConfig{Sections: []string{}, StopCondition: &ChanshuiStopCondition{Match: tc.match, Rules: tc.rules}}}
			require.NoError(t, normalizeScheduledQualityProvider(p))
			require.Equal(t, tc.want, evaluateChanshuiStopCondition(p, chanshuiPolicyData(t, tc.body)).Status)
		})
	}
}

type chanshuiStateAccountFake struct {
	*scheduledQualityAccountRepo
	changes int
}

func (r *chanshuiStateAccountFake) ApplyChanshuiQualityState(_ context.Context, _ int64, planID int64, _ time.Time, paused bool) (bool, error) {
	a := r.account
	if a == nil || a.Status != StatusActive {
		return false, nil
	}
	if a.Extra == nil {
		a.Extra = map[string]any{}
	}
	owners, _ := a.Extra[ChanshuiQualityPausesExtraKey].(map[string]any)
	if owners == nil {
		owners = map[string]any{}
	}
	key := fmt.Sprint(planID)
	if paused {
		if !a.Schedulable && len(owners) == 0 {
			return false, nil
		}
		if _, exists := owners[key]; exists {
			return false, nil
		}
		owners[key] = true
		a.Schedulable = false
		a.Extra[ChanshuiQualityPausesExtraKey] = owners
	} else {
		if a.Schedulable || owners[key] == nil {
			return false, nil
		}
		delete(owners, key)
		if len(owners) == 0 {
			delete(a.Extra, ChanshuiQualityPausesExtraKey)
			if !strings.HasPrefix(a.TempUnschedulableReason, scheduledQualityReasonPrefix) {
				a.Schedulable = true
			}
		} else {
			a.Extra[ChanshuiQualityPausesExtraKey] = owners
		}
	}
	r.changes++
	return true, nil
}

func TestChanshuiPauseUnknownAndRecoverOnlyOwnPlans(t *testing.T) {
	account := &Account{ID: 2, Status: StatusActive, Schedulable: true}
	results := &scheduledQualityResultRepo{}
	runner, base, _ := scheduledQualityRunner(account, results)
	accounts := &chanshuiStateAccountFake{scheduledQualityAccountRepo: base}
	runner.accountTestSvc.accountRepo = accounts
	p := &ScheduledTestPlan{ID: 1, AccountID: 2, MaxResults: 20, CronExpression: "*/5 * * * *", QualityCheckEnabled: true, QualityProvider: QualityProviderChanshui, AutoRecover: false}
	runner.completePlanRun(context.Background(), p, &ScheduledTestResult{Status: "audit_fail"})
	require.False(t, account.Schedulable, "first mismatch must stop, no two-strike delay")
	runner.completePlanRun(context.Background(), p, &ScheduledTestResult{Status: "unknown"})
	require.False(t, account.Schedulable)
	p2 := *p
	p2.ID = 3
	runner.completePlanRun(context.Background(), &p2, &ScheduledTestResult{Status: "audit_fail"})
	runner.completePlanRun(context.Background(), p, &ScheduledTestResult{Status: "audit_pass"})
	require.False(t, account.Schedulable, "another failing plan still owns a pause")
	runner.completePlanRun(context.Background(), &p2, &ScheduledTestResult{Status: "audit_pass"})
	require.True(t, account.Schedulable, "a confirmed pass recovers without legacy auto_recover")
	account.Schedulable = false // An unowned manual pause is not ours to clear.
	runner.completePlanRun(context.Background(), p, &ScheduledTestResult{Status: "audit_pass"})
	require.False(t, account.Schedulable)
}

func TestChanshuiPersistFailureCannotChangeScheduling(t *testing.T) {
	account := &Account{ID: 2, Status: StatusActive, Schedulable: true}
	results := &scheduledQualityResultRepo{createErr: fmt.Errorf("persist failed")}
	runner, base, _ := scheduledQualityRunner(account, results)
	accounts := &chanshuiStateAccountFake{scheduledQualityAccountRepo: base}
	runner.accountTestSvc.accountRepo = accounts
	runner.completePlanRun(context.Background(), &ScheduledTestPlan{ID: 1, AccountID: 2, MaxResults: 20, CronExpression: "*/5 * * * *", QualityCheckEnabled: true, QualityProvider: QualityProviderChanshui}, &ScheduledTestResult{Status: "audit_fail"})
	require.True(t, account.Schedulable)
	require.Zero(t, accounts.changes)
}
