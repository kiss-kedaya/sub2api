package service_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestService_PersistedHistoryOrdering(t *testing.T) {
	for _, retention := range []int{0, 1, 2, 50} {
		t.Run(fmt.Sprint(retention), func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			repo := repository.NewScheduledTestResultRepository(db)
			svc := service.NewScheduledTestService(nil, repo)
			now := time.Now().UTC()
			columns := []string{"id", "plan_id", "status", "response_text", "error_message", "latency_ms", "started_at", "finished_at", "created_at"}
			result := &service.ScheduledTestResult{Status: "degraded", ResponseText: "test output", ErrorMessage: "quality degraded", LatencyMs: 2, StartedAt: now, FinishedAt: now}
			mock.ExpectQuery("INSERT INTO scheduled_test_results").
				WithArgs(int64(18), result.Status, result.ResponseText, result.ErrorMessage, result.LatencyMs, now, now).
				WillReturnRows(sqlmock.NewRows(columns).AddRow(11, 18, result.Status, result.ResponseText, result.ErrorMessage, 2, now, now, now))
			mock.ExpectExec("DELETE FROM scheduled_test_results.*PARTITION BY plan_id ORDER BY created_at DESC, id DESC").
				WithArgs(int64(18), max(retention, 2)).WillReturnResult(sqlmock.NewResult(0, 0))
			require.NoError(t, svc.SaveResult(context.Background(), 18, retention, result))
			require.Equal(t, int64(11), result.ID)
			require.Equal(t, int64(18), result.PlanID)
			require.Equal(t, now, result.CreatedAt)
			mock.ExpectQuery(`FROM scheduled_test_results WHERE plan_id = \$1 ORDER BY created_at DESC, id DESC LIMIT \$2`).
				WithArgs(int64(18), 2).
				WillReturnRows(sqlmock.NewRows(columns).
					AddRow(11, 18, "degraded", "", "", 0, now, now, now).
					AddRow(10, 18, "unknown", "", "", 0, now, now, now))
			results, err := svc.ListResults(context.Background(), 18, 2)
			require.NoError(t, err)
			require.Len(t, results, 2)
			require.Equal(t, int64(11), results[0].ID)
			require.Equal(t, "unknown", results[1].Status)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestScheduledTestService_LegacyPauseCompareAndClear(t *testing.T) {
	for _, mode := range []string{"matched", "changed_reason", "unrelated", "write_error"} {
		t.Run(mode, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			repo := repository.NewScheduledTestResultRepository(db).(interface {
				ClearScheduledQualityPause(context.Context, int64, string) (bool, error)
			})
			reason := "scheduled_quality_check: plan=18 legacy"
			if mode == "unrelated" {
				reason = "other temporary ban"
			} else {
				expect := mock.ExpectExec(`WITH updated AS \(\s*UPDATE accounts SET temp_unschedulable_until = NULL, temp_unschedulable_reason = NULL, updated_at = NOW\(\) WHERE id = \$1 AND deleted_at IS NULL AND temp_unschedulable_reason = \$2 RETURNING id\s*\)\s*INSERT INTO scheduler_outbox`).
					WithArgs(int64(29173), reason, service.SchedulerOutboxEventAccountChanged)
				if mode == "write_error" {
					expect.WillReturnError(errors.New("outbox write failed"))
				} else {
					var affected int64
					if mode == "matched" {
						affected = 1
					}
					expect.WillReturnResult(sqlmock.NewResult(0, affected))
				}
			}
			cleared, err := repo.ClearScheduledQualityPause(context.Background(), 29173, reason)
			if mode == "write_error" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, mode == "matched", cleared)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
