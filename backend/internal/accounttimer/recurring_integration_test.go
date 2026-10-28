//go:build integration

package accounttimer

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func recurringFixtureExec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.DB.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func saveRecurringFixture(t *testing.T, s *Store, id, version int64, in Input) Task {
	t.Helper()
	task, err := s.Save(context.Background(), id, 9, SaveRequest{Input: in, Action: "recurring", ExpectedVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func recurringWorkdayInput() Input {
	return Input{Mode: "recurring", Timezone: "UTC", WeekdaysOnly: true, Periods: []Period{{Start: "09:00", End: "17:00"}}}
}

func runRecurringFixture(t *testing.T, s *Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.RunRecurringDue(ctx); err != nil {
		t.Fatal(err)
	}
}

func clearRecurringFixtureGates(t *testing.T, s *Store) {
	t.Helper()
	recurringFixtureExec(t, s, "UPDATE accounts SET temp_unschedulable_until=NULL,temp_unschedulable_reason=NULL,rate_limit_reset_at=NULL,overload_until=NULL WHERE id=7")
}

func TestIntegrationRecurringSaveListJSONBAndWorkWindows(t *testing.T) {
	s, now, notified := integrationStore(t)
	clearRecurringFixtureGates(t, s)
	in := Input{Mode: "recurring", Timezone: "Asia/Shanghai", WeekdaysOnly: true, Periods: []Period{{Start: "09:30", End: "12:00"}, {Start: "13:00", End: "18:00"}}}
	saved := saveRecurringFixture(t, s, 7, 0, in)
	snap, err := s.List(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Recurring == nil || !snap.Recurring.Enabled || snap.Recurring.Version != saved.Version || snap.Recurring.Timezone != in.Timezone || !snap.Recurring.WeekdaysOnly || !reflect.DeepEqual(snap.Recurring.Periods, in.Periods) {
		t.Fatalf("JSONB schedule did not round-trip: %+v", snap.Recurring)
	}
	if snap.Recurring.UpdatedAt.Location() != time.UTC || snap.Recurring.NextCheckAt.Location() != time.UTC {
		t.Fatal("database timezone leaked into recurring timestamps")
	}
	if len(snap.Tasks) != 0 || len(snap.History) != 1 || snap.History[0].Action != "recurring" || snap.History[0].Result != "scheduled" {
		t.Fatalf("schedule creation audit missing: %+v", snap)
	}
	// Explicitly installing a recurring schedule grants it control of
	// schedulable within its configured window, even if the account was
	// previously paused. Independent quality, quota, expiry and runtime gates
	// remain authoritative; an explicit later manual toggle cancels the plan.
	if scalar(t, s.DB, "SELECT count(*) FROM accounts WHERE id=7 AND schedulable") != 0 {
		t.Fatal("saving a schedule must wait for the next worker poll")
	}
	recurringFixtureExec(t, s, "DELETE FROM fixture_outbox")
	notified.Store(0)
	// Saving need not toggle eligibility synchronously. The next poll applies
	// the current window, and both sides of lunch must be respected.
	for _, step := range []struct {
		at      time.Time
		enabled int
	}{
		{time.Date(2026, 10, 22, 1, 30, 0, 0, time.UTC), 1},
		{time.Date(2026, 10, 22, 4, 0, 0, 0, time.UTC), 0},
		{time.Date(2026, 10, 22, 5, 0, 0, 0, time.UTC), 1},
		{time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC), 0},
	} {
		*now = step.at
		runRecurringFixture(t, s)
		if got := scalar(t, s.DB, "SELECT count(*) FROM accounts WHERE id=7 AND schedulable"); got != step.enabled {
			t.Fatalf("at %s schedulable count=%d, want %d", step.at, got, step.enabled)
		}
	}
	if got := scalar(t, s.DB, "SELECT count(*) FROM fixture_outbox"); got != 4 || notified.Load() != 4 {
		t.Fatalf("each transition must publish once: outbox=%d notifications=%d", got, notified.Load())
	}
	if scalar(t, s.DB, "SELECT count(*) FROM account_schedulable_timer_audit WHERE action IN ('recurring_open','recurring_close') AND result='executed'") != 4 {
		t.Fatal("recurring transition audit missing")
	}
	if scalar(t, s.DB, "SELECT count(*) FROM accounts WHERE quota_used=17 AND quality_reason='quality-isolation' AND manual_ip_acl='deny-fixture' AND balance=23") != 1 {
		t.Fatal("recurring schedule changed a safety or financial sentinel")
	}
}

func TestIntegrationRecurringConcurrentWorkersPublishOneTransition(t *testing.T) {
	s, now, notified := integrationStore(t)
	clearRecurringFixtureGates(t, s)
	*now = time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	saveRecurringFixture(t, s, 7, 0, recurringWorkdayInput())
	recurringFixtureExec(t, s, "DELETE FROM fixture_outbox")
	notified.Store(0)
	*now = now.Add(time.Hour)
	start := make(chan struct{})
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		worker := *s
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- worker.RunRecurringDue(context.Background())
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	// A process restart or another poll within the same window is a no-op.
	restarted := *s
	*now = now.Add(PollInterval)
	runRecurringFixture(t, &restarted)
	if scalar(t, s.DB, "SELECT count(*) FROM accounts WHERE id=7 AND schedulable") != 1 || scalar(t, s.DB, "SELECT count(*) FROM account_schedulable_recurring WHERE account_id=7 AND enabled") != 1 {
		t.Fatal("busy workers disabled the plan or lost its open transition")
	}
	if scalar(t, s.DB, "SELECT count(*) FROM fixture_outbox") != 1 || notified.Load() != 1 || scalar(t, s.DB, "SELECT count(*) FROM account_schedulable_timer_audit WHERE action='recurring_open' AND result='executed'") != 1 {
		t.Fatal("eight workers need one logical effect, one audit and one publication")
	}
}

func TestIntegrationRecurringBusyAccountIsNotDeleted(t *testing.T) {
	s, now, _ := integrationStore(t)
	clearRecurringFixtureGates(t, s)
	*now = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	saveRecurringFixture(t, s, 7, 0, recurringWorkdayInput())
	lock, err := s.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lock.ExecContext(context.Background(), "SELECT id FROM accounts WHERE id=7 FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	runRecurringFixture(t, s)
	if scalar(t, s.DB, "SELECT count(*) FROM account_schedulable_recurring WHERE account_id=7 AND enabled") != 1 || scalar(t, s.DB, "SELECT count(*) FROM account_schedulable_timer_audit WHERE reason='account_deleted'") != 0 {
		t.Fatal("SKIP LOCKED confused a busy account with a deleted account")
	}
	if err = lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(PollInterval)
	runRecurringFixture(t, s)
	if scalar(t, s.DB, "SELECT count(*) FROM accounts WHERE id=7 AND schedulable") != 1 {
		t.Fatal("unlocked account did not resume its pending schedule")
	}
}

func TestIntegrationRecurringDoesNotAdoptAnotherPause(t *testing.T) {
	s, now, _ := integrationStore(t)
	clearRecurringFixtureGates(t, s)
	*now = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	saveRecurringFixture(t, s, 7, 0, recurringWorkdayInput())
	runRecurringFixture(t, s)
	// An unrelated automatic policy changes the toggle after this plan opened
	// it. No explicit manual-intent cancellation occurs.
	recurringFixtureExec(t, s, "UPDATE accounts SET schedulable=FALSE WHERE id=7")
	*now = now.Add(PollInterval)
	runRecurringFixture(t, s)
	*now = time.Date(2026, 10, 22, 17, 0, 0, 0, time.UTC)
	runRecurringFixture(t, s)
	*now = time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	runRecurringFixture(t, s)
	if scalar(t, s.DB, "SELECT count(*) FROM accounts WHERE id=7 AND schedulable") != 0 || scalar(t, s.DB, "SELECT count(*) FROM account_schedulable_recurring WHERE account_id=7 AND owns_pause") != 0 {
		t.Fatal("a schedule adopted and reopened another subsystem's pause")
	}
	if scalar(t, s.DB, "SELECT count(*) FROM account_schedulable_timer_audit WHERE action='recurring_open' AND result='executed'") != 1 {
		t.Fatal("the unrelated pause was silently reopened")
	}
}

func TestIntegrationRecurringPreservesSafetyGates(t *testing.T) {
	for _, tc := range []struct {
		name, mutation string
	}{
		{"chanshui", "extra='{\"chanshui_quality_pauses\":{\"fixture\":true}}'::jsonb"},
		{"local_quality", "extra='{\"local_reasoning_quality_pauses\":{\"fixture\":\"owned\"}}'::jsonb"},
		{"local_model_quality", "extra='{\"local_reasoning_model_pauses\":{\"fixture\":{\"token\":\"owned\"}}}'::jsonb"},
		{"oauth_admission", "extra='{\"oauth_admission_pause\":{\"token\":\"fixture-owned\"}}'::jsonb"},
		{"legacy_quality", "temp_unschedulable_reason='scheduled_quality_check:fixture',temp_unschedulable_until=NULL"},
		{"temporary", "temp_unschedulable_until='2030-01-01Z',temp_unschedulable_reason='fixture temporary pause'"},
		{"overload", "overload_until='2030-01-01Z'"},
		{"rate_limit", "rate_limit_reset_at='2030-01-01Z'"},
		{"disabled", "status='disabled'"},
		{"expiry", "expires_at='2026-10-21Z',auto_pause_on_expired=TRUE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, now, _ := integrationStore(t)
			clearRecurringFixtureGates(t, s)
			*now = time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
			saveRecurringFixture(t, s, 7, 0, recurringWorkdayInput())
			recurringFixtureExec(t, s, "UPDATE accounts SET "+tc.mutation+" WHERE id=7")
			recurringFixtureExec(t, s, "DELETE FROM fixture_outbox")
			*now = now.Add(time.Hour)
			runRecurringFixture(t, s)
			*now = now.Add(PollInterval)
			runRecurringFixture(t, s)
			if scalar(t, s.DB, "SELECT count(*) FROM accounts WHERE id=7 AND schedulable") != 0 || scalar(t, s.DB, "SELECT count(*) FROM fixture_outbox") != 0 {
				t.Fatal("recurring open bypassed an independent safety gate")
			}
			if scalar(t, s.DB, "SELECT count(*) FROM account_schedulable_timer_audit WHERE action='recurring_open' AND result='skipped'") != 1 {
				t.Fatal("blocked transition needs one deduplicated skip audit")
			}
		})
	}
}

func TestIntegrationRecurringHonorsDisabledExpiryAutoPause(t *testing.T) {
	s, now, _ := integrationStore(t)
	clearRecurringFixtureGates(t, s)
	*now = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	saveRecurringFixture(t, s, 7, 0, recurringWorkdayInput())
	recurringFixtureExec(t, s, "UPDATE accounts SET expires_at='2026-10-21Z',auto_pause_on_expired=FALSE WHERE id=7")
	runRecurringFixture(t, s)
	if scalar(t, s.DB, "SELECT count(*) FROM accounts WHERE id=7 AND schedulable") != 1 {
		t.Fatal("a disabled expiration auto-pause must not block the explicit recurring window")
	}
}

func TestIntegrationRecurringManualCancelAndStaleVersion(t *testing.T) {
	s, now, _ := integrationStore(t)
	clearRecurringFixtureGates(t, s)
	*now = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	in := recurringWorkdayInput()
	first := saveRecurringFixture(t, s, 7, 0, in)
	if _, err := s.Save(context.Background(), 7, 10, SaveRequest{Input: in, Action: "recurring", ExpectedVersion: 0}); ErrorCode(err) != "version" {
		t.Fatalf("stale recurring save accepted: %v", err)
	}
	replacement := saveRecurringFixture(t, s, 7, first.Version, in)
	if err := s.Cancel(context.Background(), 7, 10, "recurring", first.Version); ErrorCode(err) != "version" {
		t.Fatalf("stale recurring cancel accepted: %v", err)
	}
	runRecurringFixture(t, s)
	if err := s.Manual(context.Background(), 7, 10, false); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(24 * time.Hour)
	runRecurringFixture(t, s)
	if scalar(t, s.DB, "SELECT count(*) FROM accounts WHERE id=7 AND schedulable") != 0 || scalar(t, s.DB, "SELECT count(*) FROM account_schedulable_recurring WHERE account_id=7 AND enabled") != 0 {
		t.Fatal("manual pause was reopened by a cancelled recurring policy")
	}
	var version int64
	if err := s.DB.QueryRow("SELECT version FROM account_schedulable_recurring WHERE account_id=7").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != replacement.Version+1 || scalar(t, s.DB, "SELECT count(*) FROM account_schedulable_timer_audit WHERE action='recurring' AND result='cancelled' AND reason='manual_override' AND actor_id=10") != 1 {
		t.Fatal("manual cancellation must be versioned and audited")
	}
}

func TestIntegrationRecurringAndOneShotAreMutuallyExclusive(t *testing.T) {
	s, now, _ := integrationStore(t)
	clearRecurringFixtureGates(t, s)
	*now = time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	saveRelative(t, s, "open", 1)
	first := saveRecurringFixture(t, s, 7, 0, recurringWorkdayInput())
	if scalar(t, s.DB, "SELECT count(*) FROM account_schedulable_timers WHERE account_id=7 AND status='pending'") != 0 {
		t.Fatal("recurring save retained a conflicting one-shot")
	}
	saveRelative(t, s, "close", 2)
	if scalar(t, s.DB, "SELECT count(*) FROM account_schedulable_recurring WHERE account_id=7 AND enabled") != 0 || scalar(t, s.DB, "SELECT count(*) FROM account_schedulable_timer_audit WHERE action='recurring' AND result='cancelled' AND reason='one_shot_schedule_override'") != 1 {
		t.Fatal("one-shot save must cancel and audit the recurring policy")
	}
	saveRecurringFixture(t, s, 7, first.Version+1, recurringWorkdayInput())
	if scalar(t, s.DB, "SELECT count(*) FROM account_schedulable_timers WHERE account_id=7 AND status='pending'") != 0 || scalar(t, s.DB, "SELECT count(*) FROM account_schedulable_recurring WHERE account_id=7 AND enabled") != 1 {
		t.Fatal("switching back to recurring left two live policy types")
	}
}

func TestIntegrationRecurringConcurrentOneShotSaveIsSerialized(t *testing.T) {
	s, now, _ := integrationStore(t)
	clearRecurringFixtureGates(t, s)
	*now = time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, request := range []SaveRequest{
		{Input: recurringWorkdayInput(), Action: "recurring"},
		{Input: Input{Mode: "relative", Hours: 2, Timezone: "UTC"}, Action: "close"},
	} {
		wg.Add(1)
		go func(req SaveRequest) {
			defer wg.Done()
			<-start
			_, err := s.Save(context.Background(), 7, 9, req)
			errs <- err
		}(request)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if scalar(t, s.DB, "SELECT (SELECT count(*) FROM account_schedulable_recurring WHERE account_id=7 AND enabled) + (SELECT count(*) FROM account_schedulable_timers WHERE account_id=7 AND status='pending')") != 1 {
		t.Fatal("concurrent policy saves did not serialize to one live policy")
	}
}

func TestIntegrationRecurringDueQueueProcessesBeyondFirstPage(t *testing.T) {
	s, now, notified := integrationStore(t)
	*now = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	recurringFixtureExec(t, s, "INSERT INTO accounts(id) SELECT generate_series(1000,1424)")
	recurringFixtureExec(t, s, "INSERT INTO account_schedulable_recurring (account_id,version,timezone,weekdays_only,periods,requested_by,enabled,updated_at,next_check_at,owns_pause) SELECT id,1,'UTC',TRUE,'[{\"start\":\"09:00\",\"end\":\"17:00\"}]'::jsonb,9,TRUE,$1,$1,TRUE FROM accounts WHERE id BETWEEN 1000 AND 1424", *now)
	runRecurringFixture(t, s)
	if scalar(t, s.DB, "SELECT count(*) FROM accounts WHERE id BETWEEN 1000 AND 1424 AND schedulable") != 425 || scalar(t, s.DB, "SELECT count(*) FROM fixture_outbox") != 425 || notified.Load() != 425 {
		t.Fatal("the recurring due queue starved accounts after the first 200")
	}
	if scalar(t, s.DB, "SELECT count(*) FROM account_schedulable_timer_audit WHERE action='recurring_open' AND result='executed'") != 425 {
		t.Fatal("some page transitions were missed or duplicated")
	}
	var due int
	if err := s.DB.QueryRow("SELECT count(*) FROM account_schedulable_recurring WHERE enabled AND next_check_at<=$1", *now).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if due != 0 {
		t.Fatalf("%d due schedules never advanced their queue deadline", due)
	}
}

func TestIntegrationRecurringOutboxFailureRollsBackTransition(t *testing.T) {
	s, now, notified := integrationStore(t)
	clearRecurringFixtureGates(t, s)
	*now = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	saveRecurringFixture(t, s, 7, 0, recurringWorkdayInput())
	recurringFixtureExec(t, s, "DELETE FROM fixture_outbox")
	notified.Store(0)
	original := s.Enqueue
	s.Enqueue = func(context.Context, *sql.Tx, int64) error { return errors.New("fixture outbox unavailable") }
	if err := s.RunRecurringDue(context.Background()); err == nil {
		t.Fatal("outbox failure must fail the transition")
	}
	if scalar(t, s.DB, "SELECT count(*) FROM accounts WHERE id=7 AND schedulable") != 0 || scalar(t, s.DB, "SELECT count(*) FROM account_schedulable_timer_audit WHERE action='recurring_open'") != 0 || notified.Load() != 0 {
		t.Fatal("failed transaction leaked a transition, audit or publication")
	}
	s.Enqueue = original
	runRecurringFixture(t, s)
	if scalar(t, s.DB, "SELECT count(*) FROM accounts WHERE id=7 AND schedulable") != 1 || scalar(t, s.DB, "SELECT count(*) FROM fixture_outbox") != 1 || notified.Load() != 1 {
		t.Fatal("rolled-back recurring transition could not retry safely")
	}
}
