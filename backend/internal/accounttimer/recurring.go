package accounttimer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// RecurringSchedule is the durable, account-scoped time-window policy.  It
// only controls schedulable; account status, credentials, quality pauses and
// quotas remain independent gates.
type RecurringSchedule struct {
	AccountID    int64     `json:"account_id"`
	Version      int64     `json:"version"`
	Timezone     string    `json:"timezone"`
	WeekdaysOnly bool      `json:"weekdays_only"`
	Periods      []Period  `json:"periods"`
	RequestedBy  int64     `json:"requested_by"`
	Enabled      bool      `json:"enabled"`
	UpdatedAt    time.Time `json:"updated_at"`
}

const recurringColumns = "account_id,version,timezone,weekdays_only,periods,requested_by,enabled,updated_at"

func scanRecurring(row scanner) (RecurringSchedule, error) {
	var out RecurringSchedule
	var raw []byte
	if err := row.Scan(&out.AccountID, &out.Version, &out.Timezone, &out.WeekdaysOnly, &raw, &out.RequestedBy, &out.Enabled, &out.UpdatedAt); err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out.Periods); err != nil {
		return out, fmt.Errorf("decode recurring periods: %w", err)
	}
	out.UpdatedAt = out.UpdatedAt.UTC()
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
	// A recurring policy supersedes pending one-shot open/close plans for the
	// same account; otherwise an old one-shot could overwrite the next window.
	if err = CancelPending(WithManualIntent(ctx, actor), tx, id, now); err != nil {
		return zero, err
	}
	version++
	_, err = tx.ExecContext(ctx, `INSERT INTO account_schedulable_recurring
 (account_id,version,timezone,weekdays_only,periods,requested_by,enabled,updated_at)
 VALUES ($1,$2,$3,$4,$5::jsonb,$6,TRUE,$7)
 ON CONFLICT (account_id) DO UPDATE SET version=EXCLUDED.version,timezone=EXCLUDED.timezone,
 weekdays_only=EXCLUDED.weekdays_only,periods=EXCLUDED.periods,requested_by=EXCLUDED.requested_by,
 enabled=TRUE,updated_at=EXCLUDED.updated_at`, id, version, req.Timezone, req.WeekdaysOnly, periods, actor, now)
	if err != nil {
		return zero, err
	}
	if err = audit(ctx, tx, Task{AccountID: id, Action: "recurring", Version: version, ExecuteAt: preview.ExecuteAt, Timezone: req.Timezone, RequestedBy: actor}, actor, now, "scheduled", ""); err != nil {
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
	err = tx.QueryRowContext(ctx, "SELECT version FROM account_schedulable_recurring WHERE account_id=$1 AND enabled=TRUE FOR UPDATE", id).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) || current != version {
		return invalid("version")
	}
	now := s.now()
	_, err = tx.ExecContext(ctx, `UPDATE account_schedulable_recurring SET enabled=FALSE,version=version+1,updated_at=$2 WHERE account_id=$1 AND version=$3 AND enabled=TRUE`, id, now, version)
	if err != nil {
		return err
	}
	if err = audit(ctx, tx, Task{AccountID: id, Action: "recurring", Version: version, ExecuteAt: now, Timezone: "UTC", RequestedBy: actor}, actor, now, "cancelled", "admin_cancelled"); err != nil {
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

// CancelRecurringPending is used by an explicit manual switch while the
// account row is already locked by the caller.
func CancelRecurringPending(ctx context.Context, exec Executor, id int64, now time.Time, actor int64) error {
	if actor <= 0 {
		return nil
	}
	_, err := exec.ExecContext(ctx, `UPDATE account_schedulable_recurring SET enabled=FALSE,version=version+1,updated_at=$2 WHERE account_id=$1 AND enabled=TRUE`, id, now)
	return err
}

// RunRecurringDue evaluates configured schedules.  Each account is claimed by
// locking its account row with SKIP LOCKED, so multiple app instances cannot
// apply the same transition or publish duplicate scheduler snapshots.
func (s *Store) RunRecurringDue(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, "SELECT account_id FROM account_schedulable_recurring WHERE enabled=TRUE ORDER BY account_id LIMIT 200")
	if err != nil {
		return err
	}
	ids := make([]int64, 0, 32)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	var errs []error
	for _, id := range ids {
		if err := s.executeRecurring(ctx, id); err != nil {
			errs = append(errs, err)
		}
		if ctx.Err() != nil {
			break
		}
	}
	return errors.Join(errs...)
}

func (s *Store) executeRecurring(ctx context.Context, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	a, err := lockAccount(ctx, tx, id, true)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var schedule RecurringSchedule
	var raw []byte
	err = tx.QueryRowContext(ctx, "SELECT "+recurringColumns+" FROM account_schedulable_recurring WHERE account_id=$1 AND enabled=TRUE FOR UPDATE SKIP LOCKED", id).Scan(&schedule.AccountID, &schedule.Version, &schedule.Timezone, &schedule.WeekdaysOnly, &raw, &schedule.RequestedBy, &schedule.Enabled, &schedule.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &schedule.Periods); err != nil {
		return err
	}
	loc, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		return err
	}
	active, err := recurringActive(Input{Mode: "recurring", Timezone: schedule.Timezone, WeekdaysOnly: schedule.WeekdaysOnly, Periods: schedule.Periods}, s.now(), loc)
	if err != nil {
		return err
	}
	now := s.now()
	desired := active
	reason := ""
	if active {
		if a.deleted != nil {
			desired, reason = false, "account_deleted"
		} else if a.status != "active" {
			desired, reason = false, "account_inactive"
		} else if a.expires != nil && !a.expires.After(now) {
			desired, reason = false, "account_expired"
		}
	}
	var changed bool
	if desired {
		result, execErr := tx.ExecContext(ctx, `UPDATE accounts SET schedulable=TRUE,updated_at=$1 WHERE id=$2 AND deleted_at IS NULL AND schedulable IS DISTINCT FROM TRUE
 AND NOT (COALESCE(jsonb_typeof(extra->'chanshui_quality_pauses')='object' AND extra->'chanshui_quality_pauses'<>'{}'::jsonb,FALSE)
 OR COALESCE(jsonb_typeof(extra->'local_reasoning_quality_pauses')='object' AND extra->'local_reasoning_quality_pauses'<>'{}'::jsonb,FALSE))`, now, id)
		if execErr != nil {
			return execErr
		}
		n, _ := result.RowsAffected()
		changed = n > 0
		if !changed {
			var current bool
			if err := tx.QueryRowContext(ctx, "SELECT schedulable FROM accounts WHERE id=$1", id).Scan(&current); err != nil {
				return err
			}
			if !current {
				reason = "quality_paused"
			}
		}
	} else {
		result, execErr := tx.ExecContext(ctx, "UPDATE accounts SET schedulable=FALSE,updated_at=$1 WHERE id=$2 AND deleted_at IS NULL AND schedulable IS DISTINCT FROM FALSE", now, id)
		if execErr != nil {
			return execErr
		}
		n, _ := result.RowsAffected()
		changed = n > 0
	}
	if changed && s.Enqueue != nil {
		if err = s.Enqueue(ctx, tx, id); err != nil {
			return err
		}
	}
	if changed {
		_, err = tx.ExecContext(ctx, "UPDATE account_schedulable_recurring SET last_applied_state=$2,last_applied_at=$3,last_reason=$4 WHERE account_id=$1", id, desired, now, reason)
		if err != nil {
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
