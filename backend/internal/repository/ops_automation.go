package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type opsAutomationRepository struct {
	db      *sql.DB
	changed func(context.Context, int64)
}

func NewOpsAutomationRepository(db *sql.DB, accounts service.AccountRepository) service.OpsAutomationRepository {
	r := &opsAutomationRepository{db: db}
	if a, ok := accounts.(*accountRepository); ok {
		r.changed = a.syncSchedulerAccountSnapshot
	}
	return r
}
func (r *opsAutomationRepository) SaveConfig(ctx context.Context, c service.OpsAutomationConfig, expected string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('ops_automation_v1',0))`); err != nil {
		return err
	}
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR UPDATE`, service.OpsAutomationSettingsKey).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	current := service.DefaultOpsAutomationConfig()
	if raw != "" && json.Unmarshal([]byte(raw), &current) != nil {
		return errors.New("invalid saved automation config")
	}
	if current.Revision != expected {
		return service.ErrChannelOperationsConflict
	}
	encoded, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES($1,$2,NOW()) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_at=NOW()`, service.OpsAutomationSettingsKey, string(encoded))
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *opsAutomationRepository) RecordOutcome(ctx context.Context, e service.AutomationOutcome, c service.OpsAutomationConfig) error {
	if !c.Ramp.Enabled || e.ID == "" || e.StartedAt.Before(c.UpdatedAt) {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR SHARE`, service.OpsAutomationSettingsKey).Scan(&raw); err != nil {
		return err
	}
	actual := service.DefaultOpsAutomationConfig()
	if json.Unmarshal([]byte(raw), &actual) != nil {
		return errors.New("invalid automation config")
	}
	if !actual.Ramp.Enabled || actual.Revision != c.Revision || e.StartedAt.Before(actual.UpdatedAt) {
		return nil
	}
	if e.Success {
		allowed := false
		for _, g := range actual.Ramp.GroupIDs {
			if e.GroupID == g {
				allowed = true
				break
			}
		}
		if !allowed {
			return tx.Commit()
		}
	}
	var current int
	var stateRaw []byte
	err = tx.QueryRowContext(ctx, `SELECT concurrency,COALESCE(extra->'ops_automation_ramp','{}'::jsonb) FROM accounts a WHERE id=$1 AND deleted_at IS NULL AND parent_account_id IS NULL AND type='oauth' AND concurrency>0 AND EXISTS(SELECT 1 FROM account_groups ag JOIN groups g ON g.id=ag.group_id WHERE ag.account_id=a.id AND ag.group_id=ANY($2) AND g.deleted_at IS NULL AND g.status='active') AND (NOT $3 OR (a.status='active' AND a.schedulable AND (rate_limit_reset_at IS NULL OR rate_limit_reset_at<NOW()) AND (overload_until IS NULL OR overload_until<NOW()) AND (temp_unschedulable_until IS NULL OR temp_unschedulable_until<NOW()))) FOR UPDATE`, e.AccountID, pq.Array(actual.Ramp.GroupIDs), e.Success).Scan(&current, &stateRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	insert, err := tx.ExecContext(ctx, `INSERT INTO ops_automation_outcomes(id,account_id,revision,succeeded) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, e.ID, e.AccountID, c.Revision, e.Success)
	if err != nil {
		return err
	}
	n, err := insert.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	state := service.AutomationRampState{}
	if json.Unmarshal(stateRaw, &state) != nil {
		return errors.New("invalid ramp progress")
	}
	if !e.Success {
		state.Successes = 0
	}
	state, next := service.AdvanceAutomationRamp(state, current, actual, e, time.Now().UTC())
	encoded, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET concurrency=$2,extra=jsonb_set(COALESCE(extra,'{}'::jsonb),'{ops_automation_ramp}',$3::jsonb),updated_at=CASE WHEN concurrency<>$2 THEN NOW() ELSE updated_at END WHERE id=$1`, e.AccountID, next, string(encoded))
	if err != nil {
		return err
	}
	if next != current {
		if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &e.AccountID, nil, nil); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO ops_automation_events(account_id,kind,old_concurrency,new_concurrency) VALUES($1,'concurrency_upgrade',$2,$3)`, e.AccountID, current, next); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if next != current && r.changed != nil {
		r.changed(ctx, e.AccountID)
	}
	return nil
}
func (r *opsAutomationRepository) RankingSignals(ctx context.Context, q service.AutomationRankQuery) (map[int64]service.AutomationRankSignal, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT account_id,count(*),COALESCE(percentile_cont(.9) WITHIN GROUP(ORDER BY first_token_ms) FILTER(WHERE stream AND first_token_ms>0),0) FROM usage_logs WHERE group_id=$1 AND account_id=ANY($2) AND created_at>=$3 AND COALESCE(NULLIF(requested_model,''),model)=$4 AND (input_tokens>0 OR output_tokens>0 OR cache_read_tokens>0 OR cache_creation_tokens>0 OR actual_cost>0) GROUP BY account_id`, q.GroupID, pq.Array(q.AccountIDs), q.Since, q.Model)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]service.AutomationRankSignal{}
	for rows.Next() {
		var id int64
		var v service.AutomationRankSignal
		if err = rows.Scan(&id, &v.Samples, &v.P90MS); err != nil {
			return nil, err
		}
		out[id] = v
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	quality, err := r.db.QueryContext(ctx, `SELECT account_id,count(*) FILTER(WHERE outcome='passed')::float8/NULLIF(count(*) FILTER(WHERE outcome IN ('passed','failed')),0),count(*) FILTER(WHERE outcome IN ('passed','failed')) FROM local_reasoning_results WHERE account_id=ANY($1) AND model=$2 AND created_at>=$3 GROUP BY account_id`, pq.Array(q.AccountIDs), q.Model, q.Since)
	if err != nil {
		return nil, err
	}
	defer func() { _ = quality.Close() }()
	for quality.Next() {
		var id int64
		var value sql.NullFloat64
		var samples int
		if err = quality.Scan(&id, &value, &samples); err != nil {
			return nil, err
		}
		if v, ok := out[id]; ok && value.Valid {
			v.Quality = &value.Float64
			v.QualitySamples = samples
			out[id] = v
		}
	}
	return out, quality.Err()
}

func (r *opsAutomationRepository) PruneAutomationHistory(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM ops_automation_outcomes WHERE id IN (SELECT id FROM ops_automation_outcomes WHERE created_at<NOW()-INTERVAL '1 hour' ORDER BY created_at LIMIT 5000)`)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `DELETE FROM ops_automation_events WHERE id IN (SELECT id FROM ops_automation_events WHERE created_at<NOW()-INTERVAL '90 days' ORDER BY created_at LIMIT 1000)`)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `DELETE FROM local_reasoning_results WHERE id IN (SELECT id FROM local_reasoning_results WHERE created_at<NOW()-INTERVAL '90 days' ORDER BY created_at LIMIT 1000)`)
	return err
}
