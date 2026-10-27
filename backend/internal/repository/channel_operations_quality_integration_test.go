//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelOperationsAuditSummaryIsSafeAndMalformedTextIsUnknown(t *testing.T) {
	for _, tc := range []struct {
		name, raw, model, status string
		empty                    bool
	}{
		{name: "malformed", raw: "legacy text", empty: true},
		{name: "other provider", raw: `{"provider":"other","model":"model"}`, empty: true},
		{name: "valid", raw: `{"provider":"chanshui","model":"gpt-example","audit_status":"done","decision":{"status":"audit_pass","reason":"secret"},"api_key":"synthetic-secret","probes":[{"url":"https://private.invalid"}]}`, model: "gpt-example", status: "audit_pass"},
		{name: "secret model", raw: `{"provider":"chanshui","model":"sk-synthetic-key","audit_status":"done","decision":{"status":"audit_fail"}}`, status: "audit_fail"},
		{name: "upstream URL", raw: `{"provider":"chanshui","model":"https://private.invalid","audit_status":"done","decision":{"status":"audit_fail"}}`, status: "audit_fail"},
		{name: "unfinished", raw: `{"provider":"chanshui","model":"gpt-example","audit_status":"running","decision":{"status":"audit_pass"}}`, model: "gpt-example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var raw []byte
			require.NoError(t, integrationDB.QueryRow(`SELECT channel_operations_audit_summary($1)`, tc.raw).Scan(&raw))
			if tc.empty {
				require.Empty(t, raw)
				return
			}
			var got map[string]string
			require.NoError(t, json.Unmarshal(raw, &got))
			require.Equal(t, map[string]string{"model": tc.model, "decision_status": tc.status}, got)
			require.NotContains(t, string(raw), "private.invalid")
			require.NotContains(t, string(raw), "secret")
		})
	}
}

func TestChannelOperationsQualityUsesPersistedChanshuiAuditNotCurrentPlan(t *testing.T) {
	f := newChannelOperationsFixture(t)
	ctx := context.Background()
	plan, err := NewScheduledTestPlanRepository(integrationDB).Create(ctx, &service.ScheduledTestPlan{AccountID: f.account, ModelID: "current-plan-model", CronExpression: "*/5 * * * *", Enabled: true, MaxResults: 20, QualityCheckEnabled: true, QualityProvider: "chanshui"})
	require.NoError(t, err)
	now := time.Now()
	for _, sample := range []struct{ status, report string }{
		{"audit_pass", `{"provider":"chanshui","model":"historical-model","audit_status":"done","decision":{"status":"audit_pass"}}`},
		{"audit_fail", `{"provider":"chanshui","model":"historical-model","audit_status":"done","decision":{"status":"audit_fail"}}`},
		{"unknown", `{"provider":"chanshui","model":"historical-model"}`},
		{"audit_pass", `{"provider":"chanshui","model":"historical-model","audit_status":"running","decision":{"status":"audit_pass"}}`},
		{"success", "legacy result without JSON"},
	} {
		_, err := NewScheduledTestResultRepository(integrationDB).Create(ctx, &service.ScheduledTestResult{PlanID: plan.ID, Status: sample.status, ResponseText: sample.report, StartedAt: now, FinishedAt: now})
		require.NoError(t, err)
	}
	filter := service.ChannelOperationsFilter{ChannelID: f.channel, AccountID: f.account, From: now.Add(-time.Hour), Until: time.Now().Add(time.Second), Model: "historical-model", ModelScope: "upstream", Dimension: "account", Page: 1, PageSize: 20}
	snapshot := f.repo.ReadChannelOperations(ctx, filter)
	require.Equal(t, "ready", snapshot.Quality.Status, snapshot.Quality.Error)
	var got struct {
		Summary struct{ Rounds, Passed, Degraded, Inconclusive int }
		Rows    []struct {
			MeasuredModel string `json:"measured_model"`
		}
	}
	require.NoError(t, json.Unmarshal(snapshot.Quality.Data, &got))
	require.Equal(t, 4, got.Summary.Rounds)
	require.Equal(t, 1, got.Summary.Passed)
	require.Equal(t, 1, got.Summary.Degraded)
	require.Equal(t, 2, got.Summary.Inconclusive)
	for _, row := range got.Rows {
		require.Equal(t, "historical-model", row.MeasuredModel)
	}
	filter.Model = "current-plan-model"
	snapshot = f.repo.ReadChannelOperations(ctx, filter)
	require.NoError(t, json.Unmarshal(snapshot.Quality.Data, &got))
	require.Zero(t, got.Summary.Rounds)
}
