package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

const localModelObject = `CASE WHEN jsonb_typeof(extra->'local_reasoning_model_pauses')='object' THEN extra->'local_reasoning_model_pauses' ELSE '{}'::jsonb END`

func (r *opsAutomationRepository) pauseLocalModel(ctx context.Context, tx *sql.Tx, p *service.LocalReasoningPlan) (bool, error) {
	var stamp time.Time
	var raw []byte
	if e := tx.QueryRowContext(ctx, `SELECT updated_at,`+localModelObject+` FROM accounts WHERE id=$1 AND deleted_at IS NULL AND status='active' FOR UPDATE`, p.AccountID).Scan(&stamp, &raw); e != nil {
		return false, e
	}
	owners := map[string]any{}
	if e := json.Unmarshal(raw, &owners); e != nil {
		return false, e
	}
	key := strconv.FormatInt(p.ID, 10)
	if _, ok := owners[key]; ok {
		return false, nil
	}
	if len(owners) >= 500 {
		return false, errors.New("model pause capacity reached")
	}
	token := uuid.NewString()
	group := int64(0)
	if p.PauseScope == "group" {
		group = p.TargetGroupID
	}
	var next time.Time
	e := tx.QueryRowContext(ctx, `UPDATE accounts SET extra=jsonb_set(COALESCE(extra,'{}'::jsonb),'{local_reasoning_model_pauses}',(`+localModelObject+`)||jsonb_build_object($2::text,jsonb_build_object('model',$3::text,'group_id',$4::bigint,'token',$5::text))),updated_at=clock_timestamp() WHERE id=$1 RETURNING updated_at`, p.AccountID, key, p.Model, group, token).Scan(&next)
	if e != nil {
		return false, e
	}
	if e = r.advanceLocalOwnerStamps(ctx, tx, p.AccountID, stamp, next); e != nil {
		return false, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO local_reasoning_owners(plan_id,account_id,token,account_updated_at,pause_scope)VALUES($1,$2,$3,$4,$5)`, p.ID, p.AccountID, token, next, p.PauseScope); e != nil {
		return false, e
	}
	if p.PauseScope == "group" {
		if _, e = tx.ExecContext(ctx, `UPDATE local_reasoning_owners SET binding_revision=COALESCE((SELECT revision FROM local_quality_group_versions WHERE account_id=$2),0) WHERE plan_id=$1`, p.ID, p.AccountID); e != nil {
			return false, e
		}
		if e = r.detachFailedQualityGroup(ctx, tx, p.AccountID, p.TargetGroupID, next); e != nil {
			return false, e
		}
	}
	return true, enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &p.AccountID, nil, nil)
}
func (r *opsAutomationRepository) advanceLocalOwnerStamps(ctx context.Context, tx *sql.Tx, account int64, old, next time.Time) error {
	if _, e := tx.ExecContext(ctx, `UPDATE local_reasoning_owners SET account_updated_at=$3 WHERE account_id=$1 AND account_updated_at=$2`, account, old, next); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `UPDATE local_quality_group_owners SET account_updated_at=$3 WHERE account_id=$1 AND account_updated_at=$2`, account, old, next)
	return e
}
func (r *opsAutomationRepository) detachFailedQualityGroup(ctx context.Context, tx *sql.Tx, account, group int64, stamp time.Time) error {
	var all bool
	e := tx.QueryRowContext(ctx, `SELECT count(*)>0 AND bool_and(o.plan_id IS NOT NULL AND o.account_updated_at=$3 AND o.binding_revision=COALESCE((SELECT revision FROM local_quality_group_versions WHERE account_id=$1),0)) FROM local_reasoning_plans p LEFT JOIN local_reasoning_owners o ON o.plan_id=p.id AND o.pause_scope='group' WHERE p.account_id=$1 AND p.target_group_id=$2 AND p.enabled AND p.action='pause' AND p.pause_scope='group'`, account, group, stamp).Scan(&all)
	if e != nil || !all {
		return e
	}
	var priority int
	var created time.Time
	e = tx.QueryRowContext(ctx, `DELETE FROM account_groups WHERE account_id=$1 AND group_id=$2 AND NOT EXISTS(SELECT 1 FROM local_quality_group_owners WHERE account_id=$1 AND group_id=$2) RETURNING priority,created_at`, account, group).Scan(&priority, &created)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	var revision int64
	if e = tx.QueryRowContext(ctx, `SELECT revision FROM local_quality_group_versions WHERE account_id=$1 FOR UPDATE`, account).Scan(&revision); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO local_quality_group_owners(account_id,group_id,token,binding_priority,binding_created_at,binding_revision,account_updated_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, account, group, uuid.NewString(), priority, created, revision, stamp)
	if e != nil {
		return e
	}
	return enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountGroupsChanged, &account, nil, buildSchedulerGroupPayload([]int64{group}))
}
func (r *opsAutomationRepository) restoreLocalModel(ctx context.Context, tx *sql.Tx, plan, account int64) (bool, error) {
	var stamp, ownedStamp time.Time
	var raw []byte
	var token, scope string
	if e := tx.QueryRowContext(ctx, `SELECT updated_at,`+localModelObject+` FROM accounts WHERE id=$1 AND deleted_at IS NULL AND status='active' FOR UPDATE`, account).Scan(&stamp, &raw); e != nil {
		return false, e
	}
	if e := tx.QueryRowContext(ctx, `SELECT token,account_updated_at,pause_scope FROM local_reasoning_owners WHERE plan_id=$1 AND account_id=$2 FOR UPDATE`, plan, account).Scan(&token, &ownedStamp, &scope); e != nil {
		return false, e
	}
	owners := map[string]map[string]any{}
	if e := json.Unmarshal(raw, &owners); e != nil {
		return false, e
	}
	key := strconv.FormatInt(plan, 10)
	if owners[key]["token"] != token || !stamp.Equal(ownedStamp) {
		return false, nil
	}
	group := int64(0)
	if v, ok := owners[key]["group_id"].(float64); ok {
		group = int64(v)
	}
	if scope == "group" {
		if e := r.restoreQualityGroup(ctx, tx, account, group, stamp); e != nil {
			return false, e
		}
	}
	var next time.Time
	if e := tx.QueryRowContext(ctx, `UPDATE accounts SET extra=jsonb_set(COALESCE(extra,'{}'::jsonb),'{local_reasoning_model_pauses}',(`+localModelObject+`)-$2::text),updated_at=clock_timestamp() WHERE id=$1 RETURNING updated_at`, account, key).Scan(&next); e != nil {
		return false, e
	}
	if _, e := tx.ExecContext(ctx, `DELETE FROM local_reasoning_owners WHERE plan_id=$1`, plan); e != nil {
		return false, e
	}
	if e := r.advanceLocalOwnerStamps(ctx, tx, account, stamp, next); e != nil {
		return false, e
	}
	return true, enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &account, nil, nil)
}
func (r *opsAutomationRepository) restoreQualityGroup(ctx context.Context, tx *sql.Tx, account, group int64, stamp time.Time) error {
	var priority int
	var created, ownedStamp time.Time
	var revision int64
	e := tx.QueryRowContext(ctx, `SELECT binding_priority,binding_created_at,binding_revision,account_updated_at FROM local_quality_group_owners WHERE account_id=$1 AND group_id=$2 FOR UPDATE`, account, group).Scan(&priority, &created, &revision, &ownedStamp)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	var current int64
	if e = tx.QueryRowContext(ctx, `SELECT revision FROM local_quality_group_versions WHERE account_id=$1 FOR UPDATE`, account).Scan(&current); e != nil {
		return e
	}
	if current != revision || !ownedStamp.Equal(stamp) {
		return errors.New("group membership changed; owned removal requires review")
	}
	insert, e := tx.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority,created_at) SELECT $1,$2,$3,$4 WHERE EXISTS(SELECT 1 FROM groups WHERE id=$2 AND deleted_at IS NULL AND status='active') ON CONFLICT DO NOTHING`, account, group, priority, created)
	if e != nil {
		return e
	}
	n, e := insert.RowsAffected()
	if e != nil {
		return e
	}
	if n == 0 {
		return errors.New("group membership no longer restorable")
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM local_quality_group_owners WHERE account_id=$1 AND group_id=$2`, account, group); e != nil {
		return e
	}
	return enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountGroupsChanged, &account, nil, buildSchedulerGroupPayload([]int64{group}))
}
