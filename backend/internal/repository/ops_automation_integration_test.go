//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAutomationConfigCASAndRealSuccessConcurrency(t *testing.T) {
	ctx := context.Background()
	repo := &opsAutomationRepository{db: integrationDB}
	old := service.DefaultOpsAutomationConfig()
	var previous string
	_ = integrationDB.QueryRow(`SELECT value FROM settings WHERE key=$1`, service.OpsAutomationSettingsKey).Scan(&previous)
	t.Cleanup(func() {
		if previous == "" {
			_, _ = integrationDB.Exec(`DELETE FROM settings WHERE key=$1`, service.OpsAutomationSettingsKey)
		} else {
			_, _ = integrationDB.Exec(`UPDATE settings SET value=$2 WHERE key=$1`, service.OpsAutomationSettingsKey, previous)
		}
	})
	_, err := integrationDB.Exec(`DELETE FROM settings WHERE key=$1`, service.OpsAutomationSettingsKey)
	require.NoError(t, err)
	group := mustCreateGroup(t, integrationEntClient, &service.Group{Name: fmt.Sprintf("ramp-%d", time.Now().UnixNano()), Platform: service.PlatformOpenAI})
	account := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "ramp fixture", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Concurrency: 2, Status: service.StatusActive, Schedulable: true})
	_, err = integrationDB.Exec(`INSERT INTO account_groups(account_id,group_id) VALUES($1,$2)`, account.ID, group.ID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec(`DELETE FROM accounts WHERE id=$1`, account.ID)
		_, _ = integrationDB.Exec(`DELETE FROM groups WHERE id=$1`, group.ID)
	})
	c := old
	c.Revision = "revision-a"
	c.UpdatedAt = time.Now().Add(-time.Hour).UTC()
	c.Ramp.Enabled = true
	c.Ramp.GroupIDs = []int64{group.ID}
	c.Ramp.SuccessesPerStep = 2
	c.Ramp.Maximum = 3
	c.Ramp.Step = 1
	require.NoError(t, repo.SaveConfig(ctx, c, ""))
	require.ErrorIs(t, repo.SaveConfig(ctx, c, ""), service.ErrChannelOperationsConflict)
	state := service.AutomationRampState{Revision: c.Revision, Concurrency: 2}
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	_, err = integrationDB.Exec(`UPDATE accounts SET extra=jsonb_set(COALESCE(extra,'{}'::jsonb),'{ops_automation_ramp}',$2::jsonb) WHERE id=$1`, account.ID, string(raw))
	require.NoError(t, err)
	e := service.AutomationOutcome{ID: "once-ramp-1", AccountID: account.ID, StartedAt: time.Now(), Success: true}
	require.NoError(t, repo.RecordOutcome(ctx, e, c))
	require.NoError(t, repo.RecordOutcome(ctx, e, c))
	var current int
	require.NoError(t, integrationDB.QueryRow(`SELECT concurrency FROM accounts WHERE id=$1`, account.ID).Scan(&current))
	require.Equal(t, 2, current)
	e.ID = "once-ramp-2"
	require.NoError(t, repo.RecordOutcome(ctx, e, c))
	require.NoError(t, integrationDB.QueryRow(`SELECT concurrency FROM accounts WHERE id=$1`, account.ID).Scan(&current))
	require.Equal(t, 3, current)
	var n int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM scheduler_outbox WHERE account_id=$1`, account.ID).Scan(&n))
	require.Positive(t, n)
}

func TestLocalReasoningMultipleOwnersAndManualEditsNeverUnlock(t *testing.T) {
	ctx := context.Background()
	repo := &opsAutomationRepository{db: integrationDB}
	account := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "local quality fixture", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Concurrency: 3, Status: service.StatusActive, Schedulable: true})
	t.Cleanup(func() { _, _ = integrationDB.Exec(`DELETE FROM accounts WHERE id=$1`, account.ID) })
	makePlan := func() *service.LocalReasoningPlan {
		p, err := repo.SaveLocalPlan(ctx, service.LocalReasoningPlan{AccountID: account.ID, Model: "fixture-model", Enabled: true, IntervalMinutes: 60, Action: "pause", AutoRestore: true})
		require.NoError(t, err)
		return p
	}
	p1, p2 := makePlan(), makePlan()
	run := func(p *service.LocalReasoningPlan, outcome string) {
		claimed, err := repo.ClaimLocalPlan(ctx, p.ID)
		require.NoError(t, err)
		require.NotNil(t, claimed)
		require.NoError(t, repo.CompleteLocalPlan(ctx, claimed, service.LocalReasoningResult{QuestionID: "fixture-only", Family: "paths", Outcome: outcome, DurationMS: 1}))
	}
	sched := func() bool {
		var v bool
		require.NoError(t, integrationDB.QueryRow(`SELECT schedulable FROM accounts WHERE id=$1`, account.ID).Scan(&v))
		return v
	}
	run(p1, "failed")
	require.False(t, sched())
	run(p2, "failed")
	require.False(t, sched())
	run(p1, "passed")
	require.False(t, sched())
	run(p2, "passed")
	require.True(t, sched())
	run(p1, "failed")
	_, err := integrationDB.Exec(`UPDATE accounts SET name='manual edit',updated_at=clock_timestamp() WHERE id=$1`, account.ID)
	require.NoError(t, err)
	run(p1, "passed")
	require.False(t, sched())
	// A second automated owner may join but must not refresh the first owner's
	// stale stamp after a manual edit and thereby regain recovery authority.
	run(p2, "failed")
	run(p2, "passed")
	require.False(t, sched())
	run(p1, "passed")
	require.False(t, sched())
	preview, err := repo.LocalOwnership(ctx, p1.ID, "", false, false)
	require.NoError(t, err)
	require.True(t, preview.Owned)
	require.False(t, preview.CanRestore)
	_, err = repo.LocalOwnership(ctx, p1.ID, preview.Fence, true, false)
	require.Error(t, err)
	_, err = repo.LocalOwnership(ctx, p1.ID, preview.Fence, false, true)
	require.NoError(t, err)
	require.False(t, sched())
}
