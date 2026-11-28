package service

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
)

func TestOpsCleanupPlan(t *testing.T) {
	now := time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name         string
		days         int
		wantOK       bool
		wantTruncate bool
		wantCutoff   time.Time
	}{
		{name: "negative skips", days: -1, wantOK: false},
		{name: "zero truncates", days: 0, wantOK: true, wantTruncate: true},
		{name: "positive yields past cutoff", days: 7, wantOK: true, wantCutoff: now.AddDate(0, 0, -7)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cutoff, truncate, ok := opsCleanupPlan(now, tc.days)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if truncate != tc.wantTruncate {
				t.Fatalf("truncate = %v, want %v", truncate, tc.wantTruncate)
			}
			if !tc.wantTruncate && !cutoff.Equal(tc.wantCutoff) {
				t.Fatalf("cutoff = %v, want %v", cutoff, tc.wantCutoff)
			}
		})
	}
}

func TestIsMissingRelationError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil is not missing", err: nil, want: false},
		{name: "match relation does not exist", err: fakeErr(`pq: relation "ops_error_logs" does not exist`), want: true},
		{name: "match case-insensitive", err: fakeErr(`ERROR: Relation "x" Does Not Exist`), want: true},
		{name: "non-matching error", err: fakeErr("connection refused"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMissingRelationError(tc.err); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

type fakeErr string

func (e fakeErr) Error() string { return string(e) }

type opsCleanupChannelMonitorRepoStub struct {
	ChannelMonitorRepository
	watermarkCalls    int
	deleteHistCalls   int
	deleteRollupCalls int
}

func (s *opsCleanupChannelMonitorRepoStub) LoadAggregationWatermark(context.Context) (*time.Time, error) {
	s.watermarkCalls++
	yesterday := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	return &yesterday, nil
}

func (s *opsCleanupChannelMonitorRepoStub) DeleteHistoryBefore(context.Context, time.Time) (int64, error) {
	s.deleteHistCalls++
	return 0, nil
}

func (s *opsCleanupChannelMonitorRepoStub) DeleteRollupsBefore(context.Context, time.Time) (int64, error) {
	s.deleteRollupCalls++
	return 0, nil
}

func TestOpsCleanupDeleteBatchSQL_UsesCTIDAndSafeBatchBounds(t *testing.T) {
	if opsCleanupBatchSize < 1000 || opsCleanupBatchSize > 2000 {
		t.Fatalf("opsCleanupBatchSize = %d, want within [1000, 2000]", opsCleanupBatchSize)
	}
	if opsCleanupBatchTimeout <= 0 || opsCleanupBatchTimeout > 20*time.Second {
		t.Fatalf("opsCleanupBatchTimeout = %v, want within (0, 20s]", opsCleanupBatchTimeout)
	}
	if opsCleanupTableTimeout <= opsCleanupBatchTimeout || opsCleanupTableTimeout >= opsCleanupRunTimeout {
		t.Fatalf("opsCleanupTableTimeout = %v, want between (%v, %v)", opsCleanupTableTimeout, opsCleanupBatchTimeout, opsCleanupRunTimeout)
	}

	tsSQL := opsCleanupDeleteBatchSQL("ops_error_logs", "created_at", false)
	for _, want := range []string{
		"DELETE FROM ops_error_logs",
		"WHERE ctid = ANY(ARRAY(",
		"SELECT ctid FROM ops_error_logs",
		"WHERE created_at < $1",
		"LIMIT $2",
	} {
		if !strings.Contains(tsSQL, want) {
			t.Fatalf("timestamp SQL missing %q:\n%s", want, tsSQL)
		}
	}
	if strings.Contains(tsSQL, "ORDER BY id") {
		t.Fatalf("batch delete SQL must not ORDER BY id:\n%s", tsSQL)
	}

	dateSQL := opsCleanupDeleteBatchSQL("ops_metrics_daily", "bucket_date", true)
	if !strings.Contains(dateSQL, "WHERE bucket_date < $1::date") {
		t.Fatalf("date SQL missing ::date cast:\n%s", dateSQL)
	}
}

func TestOpsCleanupRunOnce_TableFailureDoesNotSkipRemainingTablesOrChannelMonitor(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	errLogTimeout := errors.New("pq: canceling statement due to statement timeout")
	sysLogErr := errors.New("pq: deadlock detected")

	// 0: ops_error_logs deletes 1000 rows in batch 1, then fails on batch 2.
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_error_logs")).
		WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
		WillReturnResult(sqlmock.NewResult(0, 1000))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_error_logs")).
		WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
		WillReturnError(errLogTimeout)

	// 1: ops_ingress_reject_aggregates
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_ingress_reject_aggregates")).
		WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
		WillReturnResult(sqlmock.NewResult(0, 12))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_ingress_reject_aggregates")).
		WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
		WillReturnResult(sqlmock.NewResult(0, 0))

	// 2: ops_alert_events
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_alert_events")).
		WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_alert_events")).
		WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
		WillReturnResult(sqlmock.NewResult(0, 0))

	// 3: ops_system_logs fails immediately on batch 1.
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_system_logs")).
		WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
		WillReturnError(sysLogErr)

	// 4: ops_system_log_cleanup_audits
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_system_log_cleanup_audits")).
		WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
		WillReturnResult(sqlmock.NewResult(0, 4))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_system_log_cleanup_audits")).
		WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
		WillReturnResult(sqlmock.NewResult(0, 0))

	// 5: ops_system_metrics
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_system_metrics")).
		WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
		WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_system_metrics")).
		WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
		WillReturnResult(sqlmock.NewResult(0, 0))

	// 6: ops_metrics_hourly
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_metrics_hourly")).
		WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
		WillReturnResult(sqlmock.NewResult(0, 6))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_metrics_hourly")).
		WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
		WillReturnResult(sqlmock.NewResult(0, 0))

	// 7: ops_metrics_daily
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_metrics_daily")).
		WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
		WillReturnResult(sqlmock.NewResult(0, 7))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_metrics_daily")).
		WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
		WillReturnResult(sqlmock.NewResult(0, 0))

	cmRepo := &opsCleanupChannelMonitorRepoStub{}
	svc := &OpsCleanupService{
		db:                db,
		cfg:               &config.Config{},
		channelMonitorSvc: &ChannelMonitorService{repo: cmRepo},
		effective: opsCleanupEffectiveConfig{
			OpsCleanupConfig: config.OpsCleanupConfig{
				Enabled:                    true,
				ErrorLogRetentionDays:      7,
				MinuteMetricsRetentionDays: 30,
				HourlyMetricsRetentionDays: 30,
			},
			SystemLogRetentionDays: 30,
		},
	}

	counts, runErr := svc.runCleanupOnce(context.Background())
	if runErr == nil {
		t.Fatalf("expected aggregated error, got nil")
	}
	if !errors.Is(runErr, errLogTimeout) || !errors.Is(runErr, sysLogErr) {
		t.Fatalf("aggregated error should wrap both table errors, got: %v", runErr)
	}
	if !strings.Contains(runErr.Error(), "ops_error_logs") || !strings.Contains(runErr.Error(), "ops_system_logs") {
		t.Fatalf("aggregated error should name both failing tables, got: %v", runErr)
	}

	wantCounts := opsCleanupDeletedCounts{
		errorLogs:      1000,
		ingressRejects: 12,
		alertEvents:    3,
		systemLogs:     0,
		logAudits:      4,
		systemMetrics:  5,
		hourlyPreagg:   6,
		dailyPreagg:    7,
	}
	if counts != wantCounts {
		t.Fatalf("counts = %+v, want %+v", counts, wantCounts)
	}

	if cmRepo.watermarkCalls != 1 || cmRepo.deleteHistCalls != 1 || cmRepo.deleteRollupCalls != 1 {
		t.Fatalf("channelMonitorSvc.RunDailyMaintenance was not executed fully: %+v", cmRepo)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestDeleteOldRowsByCTID_RespectsBatchTimeoutAndMissingRelation(t *testing.T) {
	t.Run("missing relation returns nil without error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_error_logs")).
			WithArgs(sqlmock.AnyArg(), 1000).
			WillReturnError(fakeErr(`pq: relation "ops_error_logs" does not exist`))

		n, err := deleteOldRowsByCTID(context.Background(), db, "ops_error_logs", "created_at", time.Now().UTC(), 1000, false)
		if err != nil || n != 0 {
			t.Fatalf("got (%d, %v), want (0, nil)", n, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("slow batch hits per-batch timeout while preserving parent ctx", func(t *testing.T) {
		prevTimeout := opsCleanupBatchTimeout
		opsCleanupBatchTimeout = 30 * time.Millisecond
		t.Cleanup(func() { opsCleanupBatchTimeout = prevTimeout })

		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })

		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_error_logs")).
			WithArgs(sqlmock.AnyArg(), 1000).
			WillDelayFor(250 * time.Millisecond).
			WillReturnResult(sqlmock.NewResult(0, 1000))

		parentCtx := context.Background()
		started := time.Now()
		n, err := deleteOldRowsByCTID(parentCtx, db, "ops_error_logs", "created_at", time.Now().UTC(), 1000, false)
		elapsed := time.Since(started)
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, sqlmock.ErrCancelled) {
			t.Fatalf("expected timeout/cancel error, got (%d, %v)", n, err)
		}
		if elapsed >= 200*time.Millisecond {
			t.Fatalf("batch did not cancel promptly on opsCleanupBatchTimeout: elapsed=%v", elapsed)
		}
		if parentCtx.Err() != nil {
			t.Fatalf("parent ctx should stay alive after per-batch timeout")
		}
	})

	t.Run("backlogged table hits per-table timeout without starving later tables or channel monitor", func(t *testing.T) {
		prevTableTimeout := opsCleanupTableTimeout
		prevBatchTimeout := opsCleanupBatchTimeout
		opsCleanupTableTimeout = 40 * time.Millisecond
		opsCleanupBatchTimeout = 200 * time.Millisecond
		t.Cleanup(func() {
			opsCleanupTableTimeout = prevTableTimeout
			opsCleanupBatchTimeout = prevBatchTimeout
		})

		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })

		// Table 0 (ops_error_logs): batch 1 succeeds quickly, batch 2 exceeds table budget.
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_error_logs")).
			WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
			WillDelayFor(15 * time.Millisecond).
			WillReturnResult(sqlmock.NewResult(0, 1000))
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM ops_error_logs")).
			WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
			WillDelayFor(120 * time.Millisecond).
			WillReturnResult(sqlmock.NewResult(0, 1000))

		// Remaining 7 tables must still execute under the intact parent ctx.
		for _, table := range []string{
			"ops_ingress_reject_aggregates",
			"ops_alert_events",
			"ops_system_logs",
			"ops_system_log_cleanup_audits",
			"ops_system_metrics",
			"ops_metrics_hourly",
			"ops_metrics_daily",
		} {
			mock.ExpectExec(regexp.QuoteMeta("DELETE FROM "+table)).
				WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(regexp.QuoteMeta("DELETE FROM "+table)).
				WithArgs(sqlmock.AnyArg(), opsCleanupBatchSize).
				WillReturnResult(sqlmock.NewResult(0, 0))
		}

		cmRepo := &opsCleanupChannelMonitorRepoStub{}
		svc := &OpsCleanupService{
			db:                db,
			cfg:               &config.Config{},
			channelMonitorSvc: &ChannelMonitorService{repo: cmRepo},
			effective: opsCleanupEffectiveConfig{
				OpsCleanupConfig: config.OpsCleanupConfig{
					Enabled:                    true,
					ErrorLogRetentionDays:      7,
					MinuteMetricsRetentionDays: 30,
					HourlyMetricsRetentionDays: 30,
				},
				SystemLogRetentionDays: 30,
			},
		}

		parentCtx := context.Background()
		counts, runErr := svc.runCleanupOnce(parentCtx)
		if runErr == nil || !strings.Contains(runErr.Error(), "ops_error_logs") {
			t.Fatalf("expected ops_error_logs table-timeout error, got %v", runErr)
		}
		if parentCtx.Err() != nil {
			t.Fatalf("parent ctx should stay alive after per-table timeout")
		}
		if counts.errorLogs != 1000 || counts.ingressRejects != 1 || counts.dailyPreagg != 1 {
			t.Fatalf("unexpected counts after table timeout: %+v", counts)
		}
		if cmRepo.watermarkCalls != 1 || cmRepo.deleteHistCalls != 1 || cmRepo.deleteRollupCalls != 1 {
			t.Fatalf("channelMonitorSvc.RunDailyMaintenance was starved: %+v", cmRepo)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})
}
