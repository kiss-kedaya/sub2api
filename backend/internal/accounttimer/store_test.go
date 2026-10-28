package accounttimer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func testStore(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, m, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := &Store{DB: db, Now: func() time.Time { return time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC) }, Policy: DefaultPolicy()}
	t.Cleanup(func() {
		if err := m.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})
	return s, m
}
func expectAccount(m sqlmock.Sqlmock, status string, deleted bool) {
	var d any
	if deleted {
		d = time.Now()
	}
	m.ExpectQuery("SELECT status, expires_at, deleted_at FROM accounts").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"status", "expires_at", "deleted_at"}).AddRow(status, nil, d))
}
func TestSaveRejectsConflictingOppositeAction(t *testing.T) {
	s, m := testStore(t)
	m.ExpectBegin()
	expectAccount(m, "active", false)
	m.ExpectQuery("SELECT version FROM account_schedulable_timers").WithArgs(int64(7), "open").WillReturnRows(sqlmock.NewRows([]string{"version"}))
	m.ExpectQuery("SELECT EXISTS").WithArgs(int64(7), "open", s.now().Add(8*time.Hour)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	m.ExpectRollback()
	_, err := s.Save(context.Background(), 7, 9, SaveRequest{Input: Input{Mode: "relative", Hours: 8, Timezone: "Asia/Shanghai"}, Action: "open"})
	if ErrorCode(err) != "conflict" {
		t.Fatalf("want conflict, got %v", err)
	}
}
func TestSaveRejectsStaleEdit(t *testing.T) {
	s, m := testStore(t)
	m.ExpectBegin()
	expectAccount(m, "active", false)
	m.ExpectQuery("SELECT version FROM account_schedulable_timers").WithArgs(int64(7), "open").WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(4))
	m.ExpectRollback()
	_, err := s.Save(context.Background(), 7, 9, SaveRequest{Input: Input{Mode: "relative", Hours: 8, Timezone: "UTC"}, Action: "open", ExpectedVersion: 3})
	if ErrorCode(err) != "version" {
		t.Fatalf("want version, got %v", err)
	}
}
func TestSaveRequiresFixedPreviewConfirmation(t *testing.T) {
	s, m := testStore(t)
	m.ExpectBegin()
	expectAccount(m, "active", false)
	m.ExpectRollback()
	_, err := s.Save(context.Background(), 7, 9, SaveRequest{Input: Input{Mode: "absolute", Time: "08:00", Timezone: "Asia/Shanghai"}, Action: "open"})
	if ErrorCode(err) != "confirmation" {
		t.Fatalf("want confirmation, got %v", err)
	}
}
func TestCancelVersionChecked(t *testing.T) {
	s, m := testStore(t)
	m.ExpectBegin()
	expectAccount(m, "active", false)
	m.ExpectQuery("SELECT "+regexp.QuoteMeta(taskColumns)).WithArgs(int64(7), "open").WillReturnRows(taskRows().AddRow(7, "open", 4, s.now().Add(time.Hour), "UTC", 9, "pending", 0, nil, ""))
	m.ExpectRollback()
	if err := s.Cancel(context.Background(), 7, 9, "open", 3); ErrorCode(err) != "version" {
		t.Fatalf("want version, got %v", err)
	}
}

func TestRecurringCancelReturnsDatabaseReadError(t *testing.T) {
	s, m := testStore(t)
	databaseErr := errors.New("fixture database read failure")
	m.ExpectBegin()
	expectAccount(m, "active", false)
	m.ExpectQuery("SELECT version,timezone FROM account_schedulable_recurring").
		WithArgs(int64(7)).WillReturnError(databaseErr)
	m.ExpectRollback()
	if err := s.Cancel(context.Background(), 7, 9, "recurring", 1); !errors.Is(err, databaseErr) {
		t.Fatalf("database error must not be reported as version conflict: %v", err)
	}
}

func taskRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"account_id", "action", "version", "execute_at", "timezone", "requested_by", "status", "attempts", "actual_at", "reason"})
}

// The outbox is a transactional dependency; a failure must roll back schedulable
// and the terminal result, leaving a bounded retry instead of a lost event.
func TestExecuteOutboxFailureRollsBackAndRecordsRetry(t *testing.T) {
	s, m := testStore(t)
	s.Enqueue = func(context.Context, *sql.Tx, int64) error { return errors.New("fixture outbox unavailable") }
	now := s.now()
	task := Task{AccountID: 7, Action: "open", Version: 1, ExecuteAt: now, Timezone: "UTC", RequestedBy: 9, Status: "pending"}
	m.ExpectBegin()
	expectAccount(m, "active", false)
	m.ExpectQuery("SELECT "+regexp.QuoteMeta(taskColumns)).WithArgs(int64(7), "open", now).WillReturnRows(taskRows().AddRow(7, "open", 1, now, "UTC", 9, "pending", 0, nil, ""))
	m.ExpectExec("UPDATE accounts SET schedulable").WithArgs(true, now, int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectRollback()
	m.ExpectBegin()
	expectAccount(m, "active", false)
	m.ExpectExec("UPDATE account_schedulable_timers").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("INSERT INTO account_schedulable_timer_audit").WillReturnResult(sqlmock.NewResult(1, 1))
	m.ExpectCommit()
	if err := s.execute(context.Background(), task); err != nil {
		t.Fatal(err)
	}
}

func TestSavePersistsUTCAndOriginalZoneAndReplacesSameAction(t *testing.T) {
	for _, version := range []int64{0, 3} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			s, m := testStore(t)
			now := s.now()
			due := now.Add(8 * time.Hour)
			m.ExpectBegin()
			expectAccount(m, "active", false)
			rows := sqlmock.NewRows([]string{"version"})
			if version > 0 {
				rows.AddRow(version)
			}
			m.ExpectQuery("SELECT version FROM account_schedulable_timers").WithArgs(int64(7), "open").WillReturnRows(rows)
			m.ExpectQuery("SELECT EXISTS").WithArgs(int64(7), "open", due).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
			m.ExpectExec("WITH cancelled AS").WithArgs(int64(7), due, "one_shot_schedule_override", int64(9)).WillReturnResult(sqlmock.NewResult(0, 0))
			m.ExpectExec("INSERT INTO account_schedulable_timers").WithArgs(int64(7), "open", version+1, due, "Asia/Shanghai", int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
			result := "scheduled"
			if version > 0 {
				result = "replaced"
			}
			m.ExpectExec("INSERT INTO account_schedulable_timer_audit").WithArgs(int64(7), "open", version+1, int64(9), due, "Asia/Shanghai", now, result, "").WillReturnResult(sqlmock.NewResult(1, 1))
			m.ExpectCommit()
			task, err := s.Save(context.Background(), 7, 9, SaveRequest{Input: Input{Mode: "relative", Hours: 8, Timezone: "Asia/Shanghai"}, Action: "open", ExpectedVersion: version})
			if err != nil || !task.ExecuteAt.Equal(due) || task.Version != version+1 || task.Timezone != "Asia/Shanghai" {
				t.Fatalf("%+v %v", task, err)
			}
		})
	}
}
func TestManualOverrideCancelsBothActionsAndPublishesOnlyAfterCommit(t *testing.T) {
	s, m := testStore(t)
	now := s.now()
	notified := 0
	s.Enqueue = func(ctx context.Context, tx *sql.Tx, id int64) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO fixture_outbox VALUES ($1)", id)
		return err
	}
	s.AfterCommit = func(context.Context, int64) { notified++ }
	m.ExpectBegin()
	expectAccount(m, "active", false)
	m.ExpectExec("WITH cancelled AS").WithArgs(int64(7), int64(9), now, "manual_override").WillReturnResult(sqlmock.NewResult(0, 2))
	m.ExpectExec("WITH cancelled AS").WithArgs(int64(7), now, "manual_override", int64(9)).WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectExec("UPDATE accounts SET schedulable").WithArgs(false, now, int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("INSERT INTO fixture_outbox").WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	if err := s.Manual(context.Background(), 7, 9, false); err != nil {
		t.Fatal(err)
	}
	if notified != 1 {
		t.Fatal("cache update missing")
	}
}
func TestManualOutboxFailureRollsBackCancellationAndSwitch(t *testing.T) {
	s, m := testStore(t)
	s.Enqueue = func(context.Context, *sql.Tx, int64) error { return errors.New("fixture failure") }
	s.AfterCommit = func(context.Context, int64) { t.Error("cache must not change before commit") }
	m.ExpectBegin()
	expectAccount(m, "active", false)
	m.ExpectExec("WITH cancelled AS").WithArgs(int64(7), int64(9), s.now(), "manual_override").WillReturnResult(sqlmock.NewResult(0, 2))
	m.ExpectExec("WITH cancelled AS").WithArgs(int64(7), s.now(), "manual_override", int64(9)).WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectExec("UPDATE accounts SET schedulable").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectRollback()
	if err := s.Manual(context.Background(), 7, 9, false); err == nil {
		t.Fatal("must reject partial update")
	}
}
func TestAutomaticPoliciesDoNotCancelPlans(t *testing.T) {
	s, _ := testStore(t)
	if err := CancelPending(context.Background(), s.DB, 7, s.now()); err != nil {
		t.Fatal(err)
	}
	ctx := WithManualAccount(WithManualIntent(context.Background(), 9), 8)
	if err := CancelPending(ctx, s.DB, 7, s.now()); err != nil {
		t.Fatal(err)
	}
}
func TestExecutionRechecksVersionStatusAndNotifiesAfterCommit(t *testing.T) {
	cases := []struct {
		name, action, status, accountStatus, reason string
		version                                     int64
		deleted                                     bool
	}{
		{"restart opens once", "open", "pending", "active", "", 1, false},
		{"stale worker cannot overwrite edit", "open", "pending", "active", "", 2, false},
		{"second instance sees executed", "open", "executed", "active", "", 1, false},
		{"deleted account skipped", "open", "pending", "active", "account_deleted", 1, true},
		{"disabled remains disabled", "open", "pending", "disabled", "account_inactive", 1, false},
		{"offline close catchup", "close", "pending", "active", "", 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, m := testStore(t)
			now := s.now()
			due := now
			if c.action == "close" {
				due = now.Add(-24 * time.Hour)
			}
			candidate := Task{AccountID: 7, Action: c.action, Version: 1, ExecuteAt: due, Timezone: "Asia/Shanghai", RequestedBy: 9, Status: "pending"}
			notified := 0
			s.AfterCommit = func(context.Context, int64) { notified++ }
			m.ExpectBegin()
			expectAccount(m, c.accountStatus, c.deleted)
			m.ExpectQuery("SELECT "+regexp.QuoteMeta(taskColumns)).WithArgs(int64(7), c.action, now).WillReturnRows(taskRows().AddRow(7, c.action, c.version, due, "Asia/Shanghai", 9, c.status, 0, nil, ""))
			active := c.version == 1 && c.status == "pending"
			result := "executed"
			if c.reason != "" {
				result = "skipped"
			}
			if active {
				if c.reason == "" {
					m.ExpectExec("UPDATE accounts SET schedulable").WithArgs(c.action == "open", now, int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
				}
				m.ExpectExec("UPDATE account_schedulable_timers").WithArgs(int64(7), c.action, result, now, c.reason, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
				m.ExpectExec("INSERT INTO account_schedulable_timer_audit").WithArgs(int64(7), c.action, int64(1), int64(9), due, "Asia/Shanghai", now, result, c.reason).WillReturnResult(sqlmock.NewResult(1, 1))
				m.ExpectCommit()
			} else {
				m.ExpectRollback()
			}
			if err := s.execute(context.Background(), candidate); err != nil {
				t.Fatal(err)
			}
			want := 0
			if active && c.reason == "" {
				want = 1
			}
			if notified != want {
				t.Fatalf("cache calls %d want %d", notified, want)
			}
		})
	}
}
func TestThirdFailureBecomesTerminalAndStaleRetryCannotChangeNewVersion(t *testing.T) {
	for _, affected := range []int64{1, 0} {
		t.Run(fmt.Sprint(affected), func(t *testing.T) {
			s, m := testStore(t)
			now := s.now()
			task := Task{AccountID: 7, Action: "open", Version: 1, ExecuteAt: now, Timezone: "UTC", RequestedBy: 9, Attempts: 2}
			m.ExpectBegin()
			expectAccount(m, "active", false)
			m.ExpectExec("UPDATE account_schedulable_timers").WithArgs(int64(7), "open", int64(1), 3, "failed", now.Add(120*time.Second), now, 2).WillReturnResult(sqlmock.NewResult(0, affected))
			if affected == 1 {
				m.ExpectExec("INSERT INTO account_schedulable_timer_audit").WillReturnResult(sqlmock.NewResult(1, 1))
				m.ExpectCommit()
			} else {
				m.ExpectRollback()
			}
			if err := s.retry(context.Background(), task); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestBusyAccountIsSkippedWithoutMisreportingDeletion(t *testing.T) {
	s, m := testStore(t)
	m.ExpectBegin()
	m.ExpectQuery("SELECT status, expires_at, deleted_at FROM accounts").WillReturnRows(sqlmock.NewRows([]string{"status", "expires_at", "deleted_at"}))
	m.ExpectQuery("SELECT EXISTS").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	m.ExpectRollback()
	if err := s.execute(context.Background(), Task{AccountID: 7, Action: "open", Version: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestScheduledOpenDoesNotOverrideQualityOwnedPause(t *testing.T) {
	s, m := testStore(t)
	now := s.now()
	candidate := Task{AccountID: 7, Action: "open", Version: 1, ExecuteAt: now, Timezone: "UTC", RequestedBy: 1, Status: "pending"}
	s.Enqueue = func(context.Context, *sql.Tx, int64) error {
		t.Error("a skipped timer must not publish a scheduling change")
		return nil
	}
	s.AfterCommit = func(context.Context, int64) { t.Error("a skipped timer must not replace the scheduler snapshot") }
	m.ExpectBegin()
	expectAccount(m, "active", false)
	m.ExpectQuery("SELECT "+regexp.QuoteMeta(taskColumns)).WithArgs(int64(7), "open", now).WillReturnRows(taskRows().AddRow(7, "open", 1, now, "UTC", 1, "pending", 0, nil, ""))
	m.ExpectExec("(?s)UPDATE accounts SET schedulable.*chanshui_quality_pauses").WithArgs(true, now, int64(7)).WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectExec("UPDATE account_schedulable_timers").WithArgs(int64(7), "open", "skipped", now, "quality_paused", int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("INSERT INTO account_schedulable_timer_audit").WithArgs(int64(7), "open", int64(1), int64(1), now, "UTC", now, "skipped", "quality_paused").WillReturnResult(sqlmock.NewResult(1, 1))
	m.ExpectCommit()
	if err := s.execute(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
}
