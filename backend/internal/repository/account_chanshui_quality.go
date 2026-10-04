package repository

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// These per-plan ownership markers are independent of temporary rate limits,
// overload flags and status. Multiple failing plans must each pass before the
// account can resume. State and scheduler outbox commit in one SQL statement.
func (r *accountRepository) ApplyChanshuiQualityState(ctx context.Context, accountID, planID int64, planUpdatedAt time.Time, paused bool) (bool, error) {
	const owners = `CASE WHEN jsonb_typeof(extra->'chanshui_quality_pauses')='object' THEN extra->'chanshui_quality_pauses' ELSE '{}'::jsonb END`
	query := ""
	const currentPlan = ` AND EXISTS (SELECT 1 FROM scheduled_test_plans p WHERE p.id=$2::bigint AND p.account_id=$1
 AND p.enabled=TRUE AND p.quality_check_enabled=TRUE AND p.quality_provider='chanshui' AND p.updated_at=$4)`
	if paused {
		query = `WITH changed AS (
   UPDATE accounts SET schedulable=FALSE,
    extra=jsonb_set(COALESCE(extra,'{}'::jsonb),'{chanshui_quality_pauses}',(` + owners + `) || jsonb_build_object($2::bigint::text,true)),
    updated_at=NOW()
   WHERE id=$1 AND deleted_at IS NULL AND status='active'
    AND (schedulable IS TRUE OR (` + owners + `)<>'{}'::jsonb)
    AND NOT ((` + owners + `) ? $2::bigint::text)` + currentPlan + `
   RETURNING id
  ) INSERT INTO scheduler_outbox(event_type,account_id,group_id,payload) SELECT $3,id,NULL,NULL FROM changed`
	} else {
		remaining := `((` + owners + `) - $2::bigint::text)`
		query = `WITH changed AS (
   UPDATE accounts SET
    schedulable=CASE WHEN ` + remaining + `='{}'::jsonb
      AND NOT (COALESCE(temp_unschedulable_reason,'') LIKE 'scheduled_quality_check:%' AND temp_unschedulable_until IS NULL)
      THEN TRUE ELSE schedulable END,
    extra=CASE WHEN ` + remaining + `='{}'::jsonb THEN COALESCE(extra,'{}'::jsonb)-'chanshui_quality_pauses'
      ELSE jsonb_set(COALESCE(extra,'{}'::jsonb),'{chanshui_quality_pauses}',` + remaining + `) END,
    updated_at=NOW()
   WHERE id=$1 AND deleted_at IS NULL AND status='active' AND schedulable IS FALSE
    AND (auto_pause_on_expired IS NOT TRUE OR expires_at IS NULL OR expires_at>NOW())
    AND ((` + owners + `) ? $2::bigint::text)` + currentPlan + `
   RETURNING id
  ) INSERT INTO scheduler_outbox(event_type,account_id,group_id,payload) SELECT $3,id,NULL,NULL FROM changed`
	}
	result, err := r.sql.ExecContext(ctx, query, accountID, planID, service.SchedulerOutboxEventAccountChanged, planUpdatedAt)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows > 0 {
		refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		r.syncSchedulerAccountSnapshot(refreshCtx, accountID)
		r.invalidateModelAvailabilityCacheAfterCommit(ctx)
	}
	return rows > 0, nil
}
