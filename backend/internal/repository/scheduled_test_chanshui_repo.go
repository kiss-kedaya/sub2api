package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *scheduledTestPlanRepository) GetChanshuiAudit(ctx context.Context, id int64) (*service.ChanshuiAuditState, error) {
	var raw []byte
	if err := r.db.QueryRowContext(ctx, `SELECT quality_audit_state FROM scheduled_test_plans WHERE id=$1`, id).Scan(&raw); err != nil {
		return nil, err
	}
	var state service.ChanshuiAuditState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, err
	}
	if state.ID == "" && state.Status != "submitting" {
		return nil, nil
	}
	return &state, nil
}

func (r *scheduledTestPlanRepository) SetChanshuiAudit(ctx context.Context, id int64, state *service.ChanshuiAuditState) error {
	raw := []byte(`{}`)
	if state != nil {
		var err error
		raw, err = json.Marshal(state)
		if err != nil {
			return err
		}
	}
	_, err := r.db.ExecContext(ctx, `UPDATE scheduled_test_plans SET quality_audit_state=$2 WHERE id=$1`, id, string(raw))
	return err
}

func (r *scheduledTestPlanRepository) ReserveChanshuiCreate(ctx context.Context) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE scheduled_test_audit_gate SET next_allowed_at=NOW()+INTERVAL '60 seconds' WHERE id=1 AND next_allowed_at<=NOW()`)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (r *scheduledTestPlanRepository) DelayChanshuiCreate(ctx context.Context, delay time.Duration) error {
	_, err := r.db.ExecContext(ctx, `UPDATE scheduled_test_audit_gate SET next_allowed_at=GREATEST(next_allowed_at,NOW()+($1 * INTERVAL '1 second')) WHERE id=1`, int64(delay/time.Second))
	return err
}

func (r *scheduledTestPlanRepository) DeferChanshuiPlan(ctx context.Context, id int64, next time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE scheduled_test_plans SET next_run_at=$2 WHERE id=$1`, id, next)
	return err
}
