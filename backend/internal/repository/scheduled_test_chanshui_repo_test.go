package repository_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestScheduledChanshuiPlanConfigAndPrivateStateRoundTrip(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := repository.NewScheduledTestPlanRepository(db)
	now := time.Now().UTC()
	next := now.Add(5 * time.Minute)
	config := service.ChanshuiConfig{BaseURL: "https://audit.example", Protocol: "auto", Timeout: 360, Sections: []string{"tools", "cache"}}
	raw, err := json.Marshal(config)
	require.NoError(t, err)
	columns := []string{"id", "account_id", "model_id", "prompt_text", "cron_expression", "enabled", "max_results", "auto_recover", "quality_check_enabled", "quality_provider", "quality_config", "quality_audit_state", "last_run_at", "next_run_at", "created_at", "updated_at"}
	rows := func(state string) *sqlmock.Rows {
		return sqlmock.NewRows(columns).AddRow(1, 2, "gpt-test", "", "*/5 * * * *", true, 20, false, true, "chanshui", string(raw), state, nil, next, now, now)
	}
	plan := &service.ScheduledTestPlan{AccountID: 2, ModelID: "gpt-test", CronExpression: "*/5 * * * *", Enabled: true, MaxResults: 20, QualityCheckEnabled: true, QualityProvider: "chanshui", QualityConfig: config, NextRunAt: &next}
	mock.ExpectQuery("INSERT INTO scheduled_test_plans").WithArgs(int64(2), "gpt-test", "", "*/5 * * * *", true, 20, false, true, "chanshui", string(raw), next).WillReturnRows(rows(`{}`))
	saved, err := repo.Create(context.Background(), plan)
	require.NoError(t, err)
	require.Equal(t, config, saved.QualityConfig)
	state := `{"id":"audit-fixture","status":"running","key_fingerprint":"private-hash","poll_url":"https://audit.example/api/v1/audits/audit-fixture"}`
	mock.ExpectQuery("SELECT.*FROM scheduled_test_plans WHERE id").WithArgs(int64(1)).WillReturnRows(rows(state))
	saved, err = repo.GetByID(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, "audit-fixture", saved.ActiveAudit.ID)
	exposed, err := json.Marshal(saved)
	require.NoError(t, err)
	require.NotContains(t, string(exposed), "private-hash")
	require.NotContains(t, string(exposed), "poll_url")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScheduledChanshuiGateAndRetryAfterAreSharedAndPersistent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo, ok := repository.NewScheduledTestPlanRepository(db).(service.ScheduledChanshuiRepository)
	require.True(t, ok)
	for _, rows := range []int64{1, 0} {
		mock.ExpectExec("UPDATE scheduled_test_audit_gate SET next_allowed_at=NOW\\(\\)\\+INTERVAL '60 seconds' WHERE id=1 AND next_allowed_at<=NOW\\(\\)").WillReturnResult(sqlmock.NewResult(0, rows))
		reserved, err := repo.ReserveChanshuiCreate(context.Background())
		require.NoError(t, err)
		require.Equal(t, rows == 1, reserved)
	}
	mock.ExpectExec("UPDATE scheduled_test_audit_gate SET next_allowed_at=GREATEST").WithArgs(int64(137)).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.DelayChanshuiCreate(context.Background(), 137*time.Second))
	mock.ExpectExec("UPDATE scheduled_test_plans SET quality_audit_state").WithArgs(int64(1), `{}`).WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.SetChanshuiAudit(context.Background(), 1, nil))
	require.NoError(t, mock.ExpectationsWereMet())
}
