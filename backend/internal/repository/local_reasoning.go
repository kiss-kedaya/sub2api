package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

const localPlanColumns = `id,account_id,model,enabled,interval_minutes,action,auto_restore,revision,last_outcome,next_run_at,lease,judge_kind,COALESCE(judge_group_id,0),judge_model,pause_scope,COALESCE(target_group_id,0)`

func scanLocalPlan(row scannable) (*service.LocalReasoningPlan, error) {
	p := &service.LocalReasoningPlan{}
	err := row.Scan(&p.ID, &p.AccountID, &p.Model, &p.Enabled, &p.IntervalMinutes, &p.Action, &p.AutoRestore, &p.Revision, &p.LastOutcome, &p.NextRunAt, &p.Lease, &p.JudgeKind, &p.JudgeGroupID, &p.JudgeModel, &p.PauseScope, &p.TargetGroupID)
	return p, err
}
func (r *opsAutomationRepository) ListLocalPlans(ctx context.Context) ([]service.LocalReasoningPlan, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+localPlanColumns+` FROM local_reasoning_plans ORDER BY id DESC LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []service.LocalReasoningPlan{}
	for rows.Next() {
		p, err := scanLocalPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}
func (r *opsAutomationRepository) SaveLocalPlan(ctx context.Context, p service.LocalReasoningPlan) (*service.LocalReasoningPlan, error) {
	if p.PauseScope == "" {
		p.PauseScope = "account"
	}
	if p.JudgeKind == "" {
		p.JudgeKind = "local"
	}
	if err := service.ValidateLocalReasoningPlan(p); err != nil {
		return nil, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if p.ID > 0 {
		old, err := scanLocalPlan(tx.QueryRowContext(ctx, `SELECT `+localPlanColumns+` FROM local_reasoning_plans WHERE id=$1 FOR UPDATE`, p.ID))
		if err != nil {
			return nil, err
		}
		if old.Revision != p.Revision {
			return nil, service.ErrChannelOperationsConflict
		}
		var owned bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM local_reasoning_owners WHERE plan_id=$1)`, p.ID).Scan(&owned); err != nil {
			return nil, err
		}
		if owned && (old.AccountID != p.AccountID || old.Model != p.Model || old.Action != p.Action || old.JudgeKind != p.JudgeKind || old.JudgeGroupID != p.JudgeGroupID || old.JudgeModel != p.JudgeModel || old.PauseScope != p.PauseScope || old.TargetGroupID != p.TargetGroupID) {
			return nil, errors.New("restore this plan's pause before changing its scope")
		}
	}
	var live bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts WHERE id=$1 AND deleted_at IS NULL AND parent_account_id IS NULL AND status='active')`, p.AccountID).Scan(&live); err != nil {
		return nil, err
	}
	if !live {
		return nil, errors.New("account is unavailable")
	}
	if p.TargetGroupID > 0 {
		var bound bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_groups WHERE account_id=$1 AND group_id=$2) OR EXISTS(SELECT 1 FROM local_quality_group_owners WHERE account_id=$1 AND group_id=$2)`, p.AccountID, p.TargetGroupID).Scan(&bound); err != nil {
			return nil, err
		}
		if !bound {
			return nil, errors.New("account is not bound to target group")
		}
	}
	var saved *service.LocalReasoningPlan
	if p.ID == 0 {
		p.Revision = 1
		saved, err = scanLocalPlan(tx.QueryRowContext(ctx, `INSERT INTO local_reasoning_plans(account_id,model,enabled,interval_minutes,action,auto_restore,judge_kind,judge_group_id,judge_model) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8::bigint,0),$9) RETURNING `+localPlanColumns, p.AccountID, p.Model, p.Enabled, p.IntervalMinutes, p.Action, p.AutoRestore, p.JudgeKind, p.JudgeGroupID, p.JudgeModel))
	} else {
		saved, err = scanLocalPlan(tx.QueryRowContext(ctx, `UPDATE local_reasoning_plans SET account_id=$2,model=$3,enabled=$4,interval_minutes=$5,action=$6,auto_restore=$7,revision=revision+1,updated_at=NOW(),lease='',lease_until=NULL,judge_kind=$8,judge_group_id=NULLIF($9::bigint,0),judge_model=$10 WHERE id=$1 RETURNING `+localPlanColumns, p.ID, p.AccountID, p.Model, p.Enabled, p.IntervalMinutes, p.Action, p.AutoRestore, p.JudgeKind, p.JudgeGroupID, p.JudgeModel))
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE local_reasoning_plans SET pause_scope=$2,target_group_id=NULLIF($3::bigint,0) WHERE id=$1`, saved.ID, p.PauseScope, p.TargetGroupID); err != nil {
		return nil, err
	}
	saved.PauseScope, saved.TargetGroupID = p.PauseScope, p.TargetGroupID
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return saved, nil
}
func (r *opsAutomationRepository) ClaimLocalPlan(ctx context.Context, id int64) (*service.LocalReasoningPlan, error) {
	p, err := scanLocalPlan(r.db.QueryRowContext(ctx, `UPDATE local_reasoning_plans SET lease=$2,lease_until=NOW()+INTERVAL '4 minutes' WHERE id=(SELECT p.id FROM local_reasoning_plans p JOIN accounts a ON a.id=p.account_id WHERE a.deleted_at IS NULL AND a.status='active' AND (($1::bigint>0 AND p.id=$1) OR ($1=0 AND p.enabled AND p.next_run_at<=NOW())) AND (p.lease='' OR p.lease_until<NOW()) ORDER BY p.next_run_at,p.id LIMIT 1 FOR UPDATE OF p SKIP LOCKED) RETURNING `+localPlanColumns, id, uuid.NewString()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}
func (r *opsAutomationRepository) CompleteLocalPlan(ctx context.Context, p *service.LocalReasoningPlan, result service.LocalReasoningResult) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var revision int64
	var lease string
	err = tx.QueryRowContext(ctx, `SELECT revision,lease FROM local_reasoning_plans WHERE id=$1 AND lease_until>NOW() FOR UPDATE`, p.ID).Scan(&revision, &lease)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if revision != p.Revision || lease != p.Lease {
		return nil
	}
	action := "observed"
	changed := false
	if p.Action == "pause" && result.Outcome == "failed" {
		changed, err = r.pauseLocal(ctx, tx, p)
		if changed {
			action = "paused"
		}
	} else if p.Action == "pause" && p.AutoRestore && result.Outcome == "passed" {
		changed, err = r.restoreLocal(ctx, tx, p.ID, p.AccountID)
		if changed {
			action = "restored"
		} else {
			action = "ownership_retained"
		}
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO local_reasoning_results(plan_id,account_id,model,question_id,family,outcome,duration_ms,action) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, p.ID, p.AccountID, p.Model, result.QuestionID, result.Family, result.Outcome, result.DurationMS, action)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE local_reasoning_plans SET last_outcome=$2,next_run_at=NOW()+($3*INTERVAL '1 minute'),lease='',lease_until=NULL WHERE id=$1`, p.ID, result.Outcome, p.IntervalMinutes)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if changed && r.changed != nil {
		r.changed(ctx, p.AccountID)
	}
	return nil
}

const localPauseObject = `CASE WHEN jsonb_typeof(extra->'local_reasoning_quality_pauses')='object' THEN extra->'local_reasoning_quality_pauses' ELSE '{}'::jsonb END`

func (r *opsAutomationRepository) pauseLocal(ctx context.Context, tx *sql.Tx, p *service.LocalReasoningPlan) (bool, error) {
	if p.PauseScope == "model" || p.PauseScope == "group" {
		return r.pauseLocalModel(ctx, tx, p)
	}
	var enabled bool
	var raw []byte
	var chanshui bool
	var previousStamp time.Time
	if err := tx.QueryRowContext(ctx, `SELECT schedulable,`+localPauseObject+`,COALESCE(jsonb_typeof(extra->'chanshui_quality_pauses')='object' AND extra->'chanshui_quality_pauses'<>'{}'::jsonb,FALSE),updated_at FROM accounts WHERE id=$1 AND deleted_at IS NULL AND status='active' FOR UPDATE`, p.AccountID).Scan(&enabled, &raw, &chanshui, &previousStamp); err != nil {
		return false, err
	}
	owners := map[string]any{}
	if err := json.Unmarshal(raw, &owners); err != nil {
		return false, err
	}
	key := strconv.FormatInt(p.ID, 10)
	if _, found := owners[key]; found {
		return false, nil
	}
	if !enabled && len(owners) == 0 && !chanshui {
		return false, nil
	} // Never adopt a manual or another subsystem's pause.
	token := uuid.NewString()
	var stamp time.Time
	if err := tx.QueryRowContext(ctx, `UPDATE accounts SET schedulable=FALSE,extra=jsonb_set(COALESCE(extra,'{}'::jsonb),'{local_reasoning_quality_pauses}',(`+localPauseObject+`)||jsonb_build_object($2::text,$3::text)),updated_at=clock_timestamp() WHERE id=$1 RETURNING updated_at`, p.AccountID, key, token).Scan(&stamp); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE local_reasoning_owners SET account_updated_at=$2 WHERE account_id=$1 AND account_updated_at=$3`, p.AccountID, stamp, previousStamp); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO local_reasoning_owners(plan_id,account_id,token,account_updated_at) VALUES($1,$2,$3,$4)`, p.ID, p.AccountID, token, stamp); err != nil {
		return false, err
	}
	return true, enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &p.AccountID, nil, nil)
}
func (r *opsAutomationRepository) restoreLocal(ctx context.Context, tx *sql.Tx, planID, accountID int64) (bool, error) {
	var scope string
	if err := tx.QueryRowContext(ctx, `SELECT pause_scope FROM local_reasoning_owners WHERE plan_id=$1 AND account_id=$2`, planID, accountID).Scan(&scope); err == nil && scope != "account" {
		return r.restoreLocalModel(ctx, tx, planID, accountID)
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var ownerToken, token, status string
	var accountStamp, ownerStamp time.Time
	var extra []byte
	var sched bool
	err := tx.QueryRowContext(ctx, `SELECT status,schedulable,COALESCE(extra,'{}'::jsonb),updated_at FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, accountID).Scan(&status, &sched, &extra, &accountStamp)
	if err != nil {
		return false, err
	}
	err = tx.QueryRowContext(ctx, `SELECT token,account_updated_at FROM local_reasoning_owners WHERE plan_id=$1 AND account_id=$2 FOR UPDATE`, planID, accountID).Scan(&ownerToken, &ownerStamp)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	fields := map[string]any{}
	if err = json.Unmarshal(extra, &fields); err != nil {
		return false, err
	}
	owners, _ := fields["local_reasoning_quality_pauses"].(map[string]any)
	key := strconv.FormatInt(planID, 10)
	token, _ = owners[key].(string)
	if sched || status != "active" || ownerToken != token || !accountStamp.Equal(ownerStamp) {
		return false, nil
	}
	// Releasing one token cannot release another plan or Chanshui's quarantine.
	var stamp time.Time
	err = tx.QueryRowContext(ctx, `UPDATE accounts SET extra=jsonb_set(COALESCE(extra,'{}'::jsonb),'{local_reasoning_quality_pauses}',(`+localPauseObject+`)-$2::text),schedulable=CASE WHEN ((`+localPauseObject+`)-$2::text)='{}'::jsonb AND (extra->'chanshui_quality_pauses'='{}'::jsonb OR extra->'chanshui_quality_pauses' IS NULL) AND NOT (COALESCE(temp_unschedulable_reason,'') LIKE 'scheduled_quality_check:%' AND temp_unschedulable_until IS NULL) AND (auto_pause_on_expired IS NOT TRUE OR expires_at IS NULL OR expires_at>NOW()) THEN TRUE ELSE schedulable END,updated_at=clock_timestamp() WHERE id=$1 RETURNING updated_at`, accountID, key).Scan(&stamp)
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM local_reasoning_owners WHERE plan_id=$1`, planID); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE local_reasoning_owners SET account_updated_at=$2 WHERE account_id=$1 AND account_updated_at=$3`, accountID, stamp, accountStamp); err != nil {
		return false, err
	}
	return true, enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil)
}
func (r *opsAutomationRepository) LocalOwnership(ctx context.Context, id int64, fence string, restore, remove bool) (*service.LocalReasoningOwnership, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var account, revision int64
	if err = tx.QueryRowContext(ctx, `SELECT account_id,revision FROM local_reasoning_plans WHERE id=$1 FOR UPDATE`, id).Scan(&account, &revision); err != nil {
		return nil, err
	}
	var stamp time.Time
	var extra []byte
	var sched bool
	if err = tx.QueryRowContext(ctx, `SELECT updated_at,COALESCE(extra,'{}'::jsonb),schedulable FROM accounts WHERE id=$1 FOR UPDATE`, account).Scan(&stamp, &extra, &sched); err != nil {
		return nil, err
	}
	var token, scope string
	var ownedStamp time.Time
	err = tx.QueryRowContext(ctx, `SELECT token,account_updated_at,pause_scope FROM local_reasoning_owners WHERE plan_id=$1`, id).Scan(&token, &ownedStamp, &scope)
	owned := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	groupState := ""
	groupRestorable := true
	if scope == "group" {
		var current, expected int64
		var groupStamp time.Time
		e := tx.QueryRowContext(ctx, `SELECT COALESCE(v.revision,0),o.binding_revision,o.account_updated_at FROM local_quality_group_owners o LEFT JOIN local_quality_group_versions v ON v.account_id=o.account_id WHERE o.account_id=$1 AND o.group_id=(SELECT target_group_id FROM local_reasoning_plans WHERE id=$2)`, account, id).Scan(&current, &expected, &groupStamp)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		if e == nil {
			groupState = fmt.Sprintf("%d:%d:%s", current, expected, groupStamp.UTC().Format(time.RFC3339Nano))
			groupRestorable = current == expected && groupStamp.Equal(stamp)
		}
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s:%s:%s:%s", id, revision, stamp.UTC().Format(time.RFC3339Nano), token, string(extra), groupState)))
	out := &service.LocalReasoningOwnership{PlanID: id, AccountID: account, Owned: owned, Fence: fmt.Sprintf("%x", digest), CanRestore: owned && (!sched || scope != "account") && stamp.Equal(ownedStamp) && groupRestorable}
	if restore || remove {
		if fence == "" || fence != out.Fence {
			return nil, service.ErrChannelOperationsConflict
		}
		if owned {
			if !restore && remove {
				// Explicit deletion may relinquish only this plan's marker. Keep
				// schedulable=false, so a manual edit is never undone by cleanup.
				query := `UPDATE accounts SET extra=jsonb_set(COALESCE(extra,'{}'::jsonb),'{local_reasoning_quality_pauses}',(` + localPauseObject + `)-$2::text),updated_at=clock_timestamp() WHERE id=$1 AND extra->'local_reasoning_quality_pauses'->>$2=$3`
				if scope != "account" {
					query = `UPDATE accounts SET extra=jsonb_set(COALESCE(extra,'{}'::jsonb),'{local_reasoning_model_pauses}',(` + localModelObject + `)-$2::text),updated_at=clock_timestamp() WHERE id=$1 AND extra->'local_reasoning_model_pauses'->$2->>'token'=$3`
				}
				if _, err = tx.ExecContext(ctx, query, account, strconv.FormatInt(id, 10), token); err != nil {
					return nil, err
				}
				if _, err = tx.ExecContext(ctx, `DELETE FROM local_reasoning_owners WHERE plan_id=$1`, id); err != nil {
					return nil, err
				}
				if scope == "group" {
					if _, err = tx.ExecContext(ctx, `DELETE FROM local_quality_group_owners WHERE account_id=$1 AND group_id=(SELECT target_group_id FROM local_reasoning_plans WHERE id=$2)`, account, id); err != nil {
						return nil, err
					}
				}
				if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &account, nil, nil); err != nil {
					return nil, err
				}
				out.Owned = false
				out.CanRestore = false
			} else if !restore {
				return nil, errors.New("restore owned pause before deleting")
			} else {
				changed, err := r.restoreLocal(ctx, tx, id, account)
				if err != nil {
					return nil, err
				}
				if !changed {
					return nil, errors.New("account changed; ownership retained for review")
				}
				out.Owned = false
				out.CanRestore = false
			}
		}
		if remove {
			if _, err = tx.ExecContext(ctx, `DELETE FROM local_reasoning_plans WHERE id=$1`, id); err != nil {
				return nil, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	if (restore || remove) && owned && r.changed != nil {
		r.changed(ctx, account)
	}
	return out, nil
}
