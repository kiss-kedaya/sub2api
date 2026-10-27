//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestLocalAndChanshuiOwnersDoNotLaunderManualEdits(t *testing.T) {
	ctx := context.Background()
	account := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "quality coexist fixture", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 3})
	t.Cleanup(func() { _, _ = integrationDB.Exec(`DELETE FROM accounts WHERE id=$1`, account.ID) })
	local := &opsAutomationRepository{db: integrationDB}
	p, e := local.SaveLocalPlan(ctx, service.LocalReasoningPlan{AccountID: account.ID, Model: "fixture", Enabled: true, IntervalMinutes: 60, Action: "pause", AutoRestore: true})
	require.NoError(t, e)
	plan, e := NewScheduledTestPlanRepository(integrationDB).Create(ctx, &service.ScheduledTestPlan{AccountID: account.ID, ModelID: "fixture", CronExpression: "*/5 * * * *", Enabled: true, MaxResults: 20, QualityCheckEnabled: true, QualityProvider: "chanshui"})
	require.NoError(t, e)
	run := func(outcome string) {
		claimed, e := local.ClaimLocalPlan(ctx, p.ID)
		require.NoError(t, e)
		require.NotNil(t, claimed)
		require.NoError(t, local.CompleteLocalPlan(ctx, claimed, service.LocalReasoningResult{QuestionID: "fixture", Family: "paths", Outcome: outcome}))
	}
	chanshui := newAccountRepositoryWithSQL(nil, integrationDB, nil)
	run("failed")
	changed, e := chanshui.ApplyChanshuiQualityState(ctx, account.ID, plan.ID, plan.UpdatedAt, true)
	require.NoError(t, e)
	require.True(t, changed)
	// Chanshui's automatic edit may advance a still-valid local ownership stamp.
	preview, e := local.LocalOwnership(ctx, p.ID, "", false, false)
	require.NoError(t, e)
	require.True(t, preview.CanRestore)
	run("passed")
	var enabled bool
	require.NoError(t, integrationDB.QueryRow(`SELECT schedulable FROM accounts WHERE id=$1`, account.ID).Scan(&enabled))
	require.False(t, enabled)
	changed, e = chanshui.ApplyChanshuiQualityState(ctx, account.ID, plan.ID, plan.UpdatedAt, false)
	require.NoError(t, e)
	require.True(t, changed)
	require.NoError(t, integrationDB.QueryRow(`SELECT schedulable FROM accounts WHERE id=$1`, account.ID).Scan(&enabled))
	require.True(t, enabled)
	run("failed")
	_, e = integrationDB.Exec(`UPDATE accounts SET name='manual ownership edit',updated_at=clock_timestamp() WHERE id=$1`, account.ID)
	require.NoError(t, e)
	changed, e = chanshui.ApplyChanshuiQualityState(ctx, account.ID, plan.ID, plan.UpdatedAt, true)
	require.NoError(t, e)
	require.True(t, changed)
	preview, e = local.LocalOwnership(ctx, p.ID, "", false, false)
	require.NoError(t, e)
	require.False(t, preview.CanRestore)
	changed, e = chanshui.ApplyChanshuiQualityState(ctx, account.ID, plan.ID, plan.UpdatedAt, false)
	require.NoError(t, e)
	require.True(t, changed)
	run("passed")
	require.NoError(t, integrationDB.QueryRow(`SELECT schedulable FROM accounts WHERE id=$1`, account.ID).Scan(&enabled))
	require.False(t, enabled)
}
