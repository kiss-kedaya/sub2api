package accounttimer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timewindow"
)

// RecurringSchedule is the durable, account-scoped time-window policy.  It
// only controls schedulable; account status, credentials, quality pauses and
// quotas remain independent gates.
type RecurringSchedule struct {
	AccountID        int64      `json:"account_id"`
	Version          int64      `json:"version"`
	Timezone         string     `json:"timezone"`
	WeekdaysOnly     bool       `json:"weekdays_only"`
	Periods          []Period   `json:"periods"`
	RequestedBy      int64      `json:"requested_by"`
	Enabled          bool       `json:"enabled"`
	UpdatedAt        time.Time  `json:"updated_at"`
	NextCheckAt      time.Time  `json:"next_check_at"`
	OwnsPause        bool       `json:"-"`
	LastAppliedState *bool      `json:"last_applied_state,omitempty"`
	LastAppliedAt    *time.Time `json:"last_applied_at,omitempty"`
	LastReason       string     `json:"-"`
}

const recurringColumns = "account_id,version,timezone,weekdays_only,periods,requested_by,enabled,updated_at,next_check_at,owns_pause,last_applied_state,last_applied_at,last_reason"

func scanRecurring(row scanner) (RecurringSchedule, error) {
	var out RecurringSchedule
	var raw []byte
	if err := row.Scan(&out.AccountID, &out.Version, &out.Timezone, &out.WeekdaysOnly, &raw, &out.RequestedBy, &out.Enabled, &out.UpdatedAt, &out.NextCheckAt, &out.OwnsPause, &out.LastAppliedState, &out.LastAppliedAt, &out.LastReason); err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out.Periods); err != nil {
		return out, fmt.Errorf("decode recurring periods: %w", err)
	}
	out.UpdatedAt = out.UpdatedAt.UTC()
	out.NextCheckAt = out.NextCheckAt.UTC()
	if out.LastAppliedAt != nil {
		utc := out.LastAppliedAt.UTC()
		out.LastAppliedAt = &utc
	}
	return out, nil
}

func (s *Store) saveRecurring(ctx context.Context, id, actor int64, req SaveRequest) (Task, error) {
	var zero Task
	if id <= 0 || actor <= 0 || req.Action != "recurring" || req.ExpectedVersion < 0 {
		return zero, invalid("request")
	}
	if req.Mode != "recurring" {
		return zero, invalid("mode")
	}
	now := s.now()
	preview, err := Resolve(req.Input, now, s.policy())
	if err != nil {
		return zero, err
	}
	periods, err := json.Marshal(req.Periods)
	if err != nil {
		return zero, invalid("period")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := lockAccount(ctx, tx, id, false)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && a.deleted != nil) {
		return zero, invalid("account_deleted")
	}
	if err != nil {
		return zero, err
	}
	var version int64
	err = tx.QueryRowContext(ctx, "SELECT version FROM account_schedulable_recurring WHERE account_id=$1 FOR UPDATE", id).Scan(&version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return zero, err
	}
	if version != req.ExpectedVersion {
		return zero, invalid("version")
	}
	// A recurring policy supersedes pending one-shot plans. Existing recurring
	// configuration is replaced in place below and remains audit-versioned.
	if err = CancelOneShotPending(ctx, tx, id, actor, now, "recurring_schedule_replaced"); err != nil {
		return zero, err
	}
	version++
	_, err = tx.ExecContext(ctx, `INSERT INTO account_schedulable_recurring
 (account_id,version,timezone,weekdays_only,periods,requested_by,enabled,updated_at,next_check_at,owns_pause,last_applied_state,last_applied_at,last_reason)
 VALUES ($1,$2,$3,$4,$5::jsonb,$6,TRUE,$7,$7,TRUE,NULL,NULL,'')
 ON CONFLICT (account_id) DO UPDATE SET version=EXCLUDED.version,timezone=EXCLUDED.timezone,
 weekdays_only=EXCLUDED.weekdays_only,periods=EXCLUDED.periods,requested_by=EXCLUDED.requested_by,
	 enabled=TRUE,updated_at=EXCLUDED.updated_at,next_check_at=EXCLUDED.next_check_at,
	 owns_pause=TRUE,last_applied_state=NULL,last_applied_at=NULL,last_reason=''`, id, version, req.Timezone, req.WeekdaysOnly, periods, actor, now)
	if err != nil {
		return zero, err
	}
	result, reason := "scheduled", ""
	if version > 1 {
		result, reason = "replaced", "recurring_schedule_replaced"
	}
	if err = audit(ctx, tx, Task{AccountID: id, Action: "recurring", Version: version, ExecuteAt: preview.ExecuteAt, Timezone: req.Timezone, RequestedBy: actor}, actor, now, result, reason); err != nil {
		return zero, err
	}
	if s.Enqueue != nil {
		if err = s.Enqueue(ctx, tx, id); err != nil {
			return zero, err
		}
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	if s.AfterCommit != nil {
		s.AfterCommit(ctx, id)
	}
	return Task{AccountID: id, Action: "recurring", Version: version, ExecuteAt: preview.ExecuteAt, Timezone: req.Timezone, RequestedBy: actor, Status: "pending"}, nil
}

func (s *Store) cancelRecurring(ctx context.Context, id, actor, version int64) error {
	if id <= 0 || actor <= 0 || version <= 0 {
		return invalid("request")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := lockAccount(ctx, tx, id, false)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && a.deleted != nil) {
		return invalid("account_deleted")
	}
	if err != nil {
		return err
	}
	var current int64
	var timezone string
	err = tx.QueryRowContext(ctx, "SELECT version,timezone FROM account_schedulable_recurring WHERE account_id=$1 AND enabled=TRUE FOR UPDATE", id).Scan(&current, &timezone)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) || current != version {
		return invalid("version")
	}
	now := s.now()
	_, err = tx.ExecContext(ctx, `UPDATE account_schedulable_recurring SET enabled=FALSE,version=version+1,updated_at=$2 WHERE account_id=$1 AND version=$3 AND enabled=TRUE`, id, now, version)
	if err != nil {
		return err
	}
	if err = audit(ctx, tx, Task{AccountID: id, Action: "recurring", Version: version, ExecuteAt: now, Timezone: timezone, RequestedBy: actor}, actor, now, "cancelled", "admin_cancelled"); err != nil {
		return err
	}
	if s.Enqueue != nil {
		if err = s.Enqueue(ctx, tx, id); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if s.AfterCommit != nil {
		s.AfterCommit(ctx, id)
	}
	return nil
}

// CancelRecurringPending is used by an explicit manual switch or one-shot
// override while the account row is already locked by the caller.
func CancelRecurringPending(ctx context.Context, exec Executor, id int64, now time.Time, actor int64, reason string) error {
	if actor <= 0 {
		return nil
	}
	if reason == "" {
		reason = "manual_override"
	}
	_, err := exec.ExecContext(ctx, `WITH cancelled AS (
 UPDATE account_schedulable_recurring SET enabled=FALSE,version=version+1,updated_at=$2,last_reason=$3
 WHERE account_id=$1 AND enabled=TRUE
 RETURNING account_id,version-1 AS version,timezone,requested_by
), audited AS (
 INSERT INTO account_schedulable_timer_audit
 (account_id,action,version,actor_id,execute_at,timezone,actual_at,result,reason)
 SELECT account_id,'recurring',version,$4,$2,timezone,$2,'cancelled',$3 FROM cancelled
 RETURNING id
)
SELECT count(*) FROM audited`, id, now, reason, actor)
	return err
}

// CancelOneShotPending is used when a recurring schedule replaces one-shot
// actions. It runs in the account transaction and leaves an audit record.
func CancelOneShotPending(ctx context.Context, exec Executor, id, actor int64, now time.Time, reason string) error {
	if actor <= 0 {
		return nil
	}
	if reason == "" {
		reason = "recurring_schedule_replaced"
	}
	_, err := exec.ExecContext(ctx, `WITH cancelled AS (
 UPDATE account_schedulable_timers SET status='cancelled',version=version+1,actual_at=$3,reason=$4
 WHERE account_id=$1 AND status='pending' RETURNING account_id,action,version-1 AS version,execute_at,timezone,requested_by
)
INSERT INTO account_schedulable_timer_audit
 (account_id,action,version,actor_id,execute_at,timezone,actual_at,result,reason)
SELECT account_id,action,version,$2,execute_at,timezone,$3,'cancelled',$4 FROM cancelled`, id, actor, now, reason)
	return err
}

// RunRecurringDue evaluates configured schedules.  Each account is claimed by
// locking its account row with SKIP LOCKED, so multiple app instances cannot
// apply the same transition or publish duplicate scheduler snapshots.
func (s *Store) RunRecurringDue(ctx context.Context) error {
	var errs []error
	for {
		now := s.now()
		rows, err := s.DB.QueryContext(ctx, `WITH due AS (
 SELECT account_id,version FROM account_schedulable_recurring
 WHERE enabled=TRUE AND next_check_at<=$1
 ORDER BY next_check_at,account_id
 LIMIT 200 FOR UPDATE SKIP LOCKED
)
UPDATE account_schedulable_recurring AS schedule
 SET next_check_at=$1 + make_interval(secs => $2)
 FROM due
 WHERE schedule.account_id=due.account_id AND schedule.version=due.version
		RETURNING schedule.account_id,schedule.version`, now, PollInterval.Seconds())
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		candidates := make([]recurringCandidate, 0, 200)
		for rows.Next() {
			var candidate recurringCandidate
			if err = rows.Scan(&candidate.accountID, &candidate.version); err != nil {
				_ = rows.Close()
				return errors.Join(append(errs, err)...)
			}
			candidates = append(candidates, candidate)
		}
		if err = rows.Err(); err != nil {
			_ = rows.Close()
			return errors.Join(append(errs, err)...)
		}
		_ = rows.Close()
		if len(candidates) == 0 {
			break
		}
		for _, candidate := range candidates {
			if err := s.executeRecurring(ctx, candidate); err != nil {
				errs = append(errs, err)
			}
			if ctx.Err() != nil {
				return errors.Join(append(errs, ctx.Err())...)
			}
		}
	}
	return errors.Join(errs...)
}

type recurringCandidate struct {
	accountID int64
	version   int64
}

func (s *Store) executeRecurring(ctx context.Context, candidate recurringCandidate) error {
	id := candidate.accountID
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := lockAccount(ctx, tx, id, true)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM accounts WHERE id=$1)", id).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil // a busy account is not a deleted account
		}
		if err = disableMissingRecurring(ctx, tx, id, s.now()); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	schedule, err := scanRecurring(tx.QueryRowContext(ctx, "SELECT "+recurringColumns+" FROM account_schedulable_recurring WHERE account_id=$1 AND version=$2 AND enabled=TRUE FOR UPDATE SKIP LOCKED", id, candidate.version))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if a.deleted != nil {
		if err = disableRecurring(ctx, tx, schedule, s.now(), "account_deleted"); err != nil {
			return err
		}
		return tx.Commit()
	}
	loc, err := timewindow.LoadLocation(schedule.Timezone)
	if err != nil {
		if err = disableRecurring(ctx, tx, schedule, s.now(), "invalid_schedule"); err != nil {
			return err
		}
		return tx.Commit()
	}
	active, err := recurringActive(Input{Mode: "recurring", Timezone: schedule.Timezone, WeekdaysOnly: schedule.WeekdaysOnly, Periods: schedule.Periods}, s.now(), loc)
	if err != nil {
		if err = disableRecurring(ctx, tx, schedule, s.now(), "invalid_schedule"); err != nil {
			return err
		}
		return tx.Commit()
	}
	now := s.now()
	reason := ""
	var changed bool
	ownsPause := schedule.OwnsPause
	if active && !ownsPause {
		current, blocked, stateErr := recurringOpenState(ctx, tx, id, now)
		reason = blocked
		err = stateErr
		if err != nil {
			return err
		}
		if current {
			ownsPause = false
			reason = ""
		} else if reason == "" {
			reason = "manual_pause"
		}
	} else if active {
		if a.status != "active" {
			reason = "account_inactive"
		} else {
			result, execErr := tx.ExecContext(ctx, recurringOpenUpdateSQL, now, id)
			if execErr != nil {
				return execErr
			}
			n, rowsErr := result.RowsAffected()
			if rowsErr != nil {
				return rowsErr
			}
			changed = n > 0
			if changed {
				ownsPause = false
			} else {
				var current bool
				current, reason, err = recurringOpenState(ctx, tx, id, now)
				if err != nil {
					return err
				}
				if current {
					ownsPause = false
					reason = ""
				} else if reason == "" {
					reason = "other_pause"
				}
			}
		}
	} else {
		result, execErr := tx.ExecContext(ctx, "UPDATE accounts SET schedulable=FALSE WHERE id=$1 AND deleted_at IS NULL AND schedulable IS DISTINCT FROM FALSE", id)
		if execErr != nil {
			return execErr
		}
		n, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return rowsErr
		}
		changed = n > 0
		if changed {
			ownsPause = true
		}
	}
	if changed && s.Enqueue != nil {
		if err = s.Enqueue(ctx, tx, id); err != nil {
			return err
		}
	}
	lastReasonChanged := schedule.LastReason != reason
	_, err = tx.ExecContext(ctx, `UPDATE account_schedulable_recurring SET next_check_at=$2,owns_pause=$3,
 last_applied_state=CASE WHEN $5 THEN $4 ELSE last_applied_state END,
 last_applied_at=CASE WHEN $5 THEN $8 ELSE last_applied_at END,last_reason=$6
 WHERE account_id=$1 AND version=$7 AND enabled=TRUE`, id, now.Add(PollInterval), ownsPause, active, changed, reason, schedule.Version, now)
	if err != nil {
		return err
	}
	if changed || lastReasonChanged && reason != "" {
		action := "recurring_close"
		if active {
			action = "recurring_open"
		}
		result, auditReason := "executed", ""
		if !changed {
			result, auditReason = "skipped", reason
		}
		if err = audit(ctx, tx, Task{AccountID: id, Action: action, Version: schedule.Version, ExecuteAt: now, Timezone: schedule.Timezone, RequestedBy: schedule.RequestedBy}, schedule.RequestedBy, now, result, auditReason); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if changed && s.AfterCommit != nil {
		s.AfterCommit(ctx, id)
	}
	return nil
}

// Do not rewrite accounts.updated_at: quality/admission leases own that stamp.
// Outbox publication still refreshes scheduler snapshots transactionally.
const recurringOpenUpdateSQL = `UPDATE accounts SET schedulable=TRUE
 WHERE id=$2 AND deleted_at IS NULL AND status='active' AND schedulable IS DISTINCT FROM TRUE
 AND (auto_pause_on_expired IS NOT TRUE OR expires_at IS NULL OR expires_at>$1)
 AND (overload_until IS NULL OR overload_until<=$1)
 AND (rate_limit_reset_at IS NULL OR rate_limit_reset_at<=$1)
 AND (temp_unschedulable_until IS NULL OR temp_unschedulable_until<=$1)
 AND NOT (temp_unschedulable_until IS NULL AND COALESCE(temp_unschedulable_reason,'') LIKE 'scheduled_quality_check:%')
 AND NOT (COALESCE(jsonb_typeof(extra->'chanshui_quality_pauses')='object' AND extra->'chanshui_quality_pauses'<>'{}'::jsonb,FALSE)
 OR COALESCE(jsonb_typeof(extra->'local_reasoning_quality_pauses')='object' AND extra->'local_reasoning_quality_pauses'<>'{}'::jsonb,FALSE)
 OR COALESCE(jsonb_typeof(extra->'local_reasoning_model_pauses')='object' AND extra->'local_reasoning_model_pauses'<>'{}'::jsonb,FALSE)
 OR COALESCE(NULLIF(extra->'oauth_admission_pause'->>'token','') IS NOT NULL,FALSE))`

func recurringOpenState(ctx context.Context, tx *sql.Tx, id int64, now time.Time) (bool, string, error) {
	var current bool
	var reason string
	err := tx.QueryRowContext(ctx, `SELECT schedulable,CASE
 WHEN status<>'active' THEN 'account_inactive'
 WHEN auto_pause_on_expired IS TRUE AND expires_at IS NOT NULL AND expires_at<=$2 THEN 'account_expired'
 WHEN COALESCE(NULLIF(extra->'oauth_admission_pause'->>'token','') IS NOT NULL,FALSE) THEN 'oauth_admission_paused'
 WHEN COALESCE(jsonb_typeof(extra->'chanshui_quality_pauses')='object' AND extra->'chanshui_quality_pauses'<>'{}'::jsonb,FALSE)
   OR COALESCE(jsonb_typeof(extra->'local_reasoning_quality_pauses')='object' AND extra->'local_reasoning_quality_pauses'<>'{}'::jsonb,FALSE)
   OR COALESCE(jsonb_typeof(extra->'local_reasoning_model_pauses')='object' AND extra->'local_reasoning_model_pauses'<>'{}'::jsonb,FALSE)
   OR (temp_unschedulable_until IS NULL AND COALESCE(temp_unschedulable_reason,'') LIKE 'scheduled_quality_check:%') THEN 'quality_paused'
 WHEN temp_unschedulable_until>$2 THEN 'temporary_pause'
 WHEN overload_until>$2 THEN 'overloaded'
 WHEN rate_limit_reset_at>$2 THEN 'rate_limited'
 ELSE '' END
 FROM accounts WHERE id=$1 AND deleted_at IS NULL`, id, now).Scan(&current, &reason)
	return current, reason, err
}

func disableRecurring(ctx context.Context, tx *sql.Tx, schedule RecurringSchedule, now time.Time, reason string) error {
	_, err := tx.ExecContext(ctx, `WITH disabled AS (
 UPDATE account_schedulable_recurring SET enabled=FALSE,version=version+1,updated_at=$2,
 next_check_at=$2,last_reason=$3 WHERE account_id=$1 AND version=$4 AND enabled=TRUE
 RETURNING account_id,version-1 AS version,timezone,requested_by
)
INSERT INTO account_schedulable_timer_audit
 (account_id,action,version,actor_id,execute_at,timezone,actual_at,result,reason)
SELECT account_id,'recurring',version,requested_by,$2,timezone,$2,'skipped',$3 FROM disabled`, schedule.AccountID, now, reason, schedule.Version)
	return err
}

func disableMissingRecurring(ctx context.Context, tx *sql.Tx, id int64, now time.Time) error {
	_, err := tx.ExecContext(ctx, `WITH disabled AS (
 UPDATE account_schedulable_recurring SET enabled=FALSE,version=version+1,updated_at=$2,
 next_check_at=$2,last_reason='account_deleted' WHERE account_id=$1 AND enabled=TRUE
 RETURNING account_id,version-1 AS version,timezone,requested_by
)
INSERT INTO account_schedulable_timer_audit
 (account_id,action,version,actor_id,execute_at,timezone,actual_at,result,reason)
SELECT account_id,'recurring',version,requested_by,$2,timezone,$2,'skipped','account_deleted' FROM disabled`, id, now)
	return err
}
