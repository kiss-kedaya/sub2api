//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestLocalGroupQualityDetachesOnlyAfterAllModelsAndRestoresExactBinding(t *testing.T) {
	ctx := context.Background()
	repo := &opsAutomationRepository{db: integrationDB}
	a := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "scoped quality", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 3})
	g := mustCreateGroup(t, integrationEntClient, &service.Group{Name: fmt.Sprintf("quality-scope-%d", time.Now().UnixNano()), Platform: service.PlatformOpenAI})
	t.Cleanup(func() {
		_, _ = integrationDB.Exec(`DELETE FROM accounts WHERE id=$1`, a.ID)
		_, _ = integrationDB.Exec(`DELETE FROM groups WHERE id=$1`, g.ID)
	})
	_, e := integrationDB.Exec(`INSERT INTO account_groups(account_id,group_id,priority)VALUES($1,$2,17)`, a.ID, g.ID)
	require.NoError(t, e)
	create := func(model string) *service.LocalReasoningPlan {
		p, e := repo.SaveLocalPlan(ctx, service.LocalReasoningPlan{AccountID: a.ID, Model: model, Enabled: true, IntervalMinutes: 60, Action: "pause", AutoRestore: true, PauseScope: "group", TargetGroupID: g.ID})
		require.NoError(t, e)
		return p
	}
	p1, p2 := create("m1"), create("m2")
	run := func(p *service.LocalReasoningPlan, outcome string) {
		claimed, e := repo.ClaimLocalPlan(ctx, p.ID)
		require.NoError(t, e)
		require.NotNil(t, claimed)
		require.NoError(t, repo.CompleteLocalPlan(ctx, claimed, service.LocalReasoningResult{Outcome: outcome, QuestionID: "fixture", Family: "paths"}))
	}
	bound := func() bool {
		var v bool
		require.NoError(t, integrationDB.QueryRow(`SELECT EXISTS(SELECT 1 FROM account_groups WHERE account_id=$1 AND group_id=$2)`, a.ID, g.ID).Scan(&v))
		return v
	}
	run(p1, "failed")
	require.True(t, bound())
	run(p2, "failed")
	require.False(t, bound())
	var sched bool
	require.NoError(t, integrationDB.QueryRow(`SELECT schedulable FROM accounts WHERE id=$1`, a.ID).Scan(&sched))
	require.True(t, sched)
	run(p1, "passed")
	require.True(t, bound())
	var priority int
	require.NoError(t, integrationDB.QueryRow(`SELECT priority FROM account_groups WHERE account_id=$1 AND group_id=$2`, a.ID, g.ID).Scan(&priority))
	require.Equal(t, 17, priority)
	// A manually replaced binding is never overwritten by later quality recovery.
	run(p2, "passed")
	run(p1, "failed")
	run(p2, "failed")
	require.False(t, bound())
	_, e = integrationDB.Exec(`INSERT INTO account_groups(account_id,group_id,priority)VALUES($1,$2,99)`, a.ID, g.ID)
	require.NoError(t, e)
	claimed, e := repo.ClaimLocalPlan(ctx, p1.ID)
	require.NoError(t, e)
	require.NotNil(t, claimed)
	require.Error(t, repo.CompleteLocalPlan(ctx, claimed, service.LocalReasoningResult{Outcome: "passed", QuestionID: "fixture", Family: "paths"}))
	require.NoError(t, integrationDB.QueryRow(`SELECT priority FROM account_groups WHERE account_id=$1 AND group_id=$2`, a.ID, g.ID).Scan(&priority))
	require.Equal(t, 99, priority)
}
func TestOAuthAdmissionCASCannotReleaseManuallyEditedAccount(t *testing.T) {
	ctx := context.Background()
	repo := &opsAutomationRepository{db: integrationDB}
	// The general fixture helper defaults false schedulable values to true.
	// Create the paused account directly so migration 303's admission trigger
	// sees the same marker and account version as a real admission candidate.
	a, e := integrationEntClient.Account.Create().
		SetName("admission fixture").
		SetPlatform(service.PlatformOpenAI).
		SetType(service.AccountTypeOAuth).
		SetStatus(service.StatusActive).
		SetSchedulable(false).
		SetConcurrency(3).
		SetPriority(50).
		SetCredentials(map[string]any{}).
		SetExtra(map[string]any{service.OAuthAdmissionPauseKey: map[string]any{"token": "fixture-only", "model": "fixture"}}).
		Save(ctx)
	require.NoError(t, e)
	t.Cleanup(func() { _, _ = integrationDB.Exec(`DELETE FROM accounts WHERE id=$1`, a.ID) })
	var leaseToken string
	var versionMatches, sched bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT l.token,l.account_updated_at=a.updated_at,a.schedulable FROM oauth_admission_leases l JOIN accounts a ON a.id=l.account_id WHERE l.account_id=$1`, a.ID).Scan(&leaseToken, &versionMatches, &sched))
	require.Equal(t, "fixture-only", leaseToken)
	require.True(t, versionMatches, "the insert trigger must bind the lease to the current account version")
	require.False(t, sched, "only paused accounts are admission candidates")
	job, e := repo.ClaimOAuthAdmission(ctx)
	require.NoError(t, e)
	require.NotNil(t, job)
	require.Equal(t, a.ID, job.AccountID)
	_, e = integrationDB.Exec(`UPDATE accounts SET name='manual admission edit',updated_at=clock_timestamp() WHERE id=$1`, a.ID)
	require.NoError(t, e)
	require.NoError(t, repo.CompleteOAuthAdmission(ctx, job, "passed"))
	require.NoError(t, integrationDB.QueryRow(`SELECT schedulable FROM accounts WHERE id=$1`, a.ID).Scan(&sched))
	require.False(t, sched)
}
