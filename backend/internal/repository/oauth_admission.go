package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

func (r *opsAutomationRepository) ClaimOAuthAdmission(ctx context.Context) (*service.OAuthAdmissionJob, error) {
	job := &service.OAuthAdmissionJob{}
	e := r.db.QueryRowContext(ctx, `UPDATE oauth_admission_leases l SET lease=$1,lease_until=NOW()+INTERVAL '2 minutes' WHERE account_id=(SELECT l.account_id FROM oauth_admission_leases l JOIN accounts a ON a.id=l.account_id WHERE a.deleted_at IS NULL AND a.status='active' AND a.type='oauth' AND a.schedulable IS FALSE AND a.updated_at=l.account_updated_at AND a.extra->'oauth_admission_pause'->>'token'=l.token AND l.next_run_at<=NOW() AND (l.lease='' OR l.lease_until<NOW()) ORDER BY l.next_run_at LIMIT 1 FOR UPDATE OF l SKIP LOCKED) RETURNING l.account_id,l.token,l.lease,(SELECT extra->'oauth_admission_pause'->>'model' FROM accounts a WHERE a.id=l.account_id)`, uuid.NewString()).Scan(&job.AccountID, &job.Token, &job.Lease, &job.Model)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	return job, e
}
func (r *opsAutomationRepository) CompleteOAuthAdmission(ctx context.Context, job *service.OAuthAdmissionJob, outcome string) error {
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer func() { _ = tx.Rollback() }()
	// Only the same request lease, account version and marker may release this
	// admission. Other quality owners and expiry remain independently effective.
	if outcome == "passed" {
		result, e := tx.ExecContext(ctx, `UPDATE accounts a SET extra=COALESCE(a.extra,'{}'::jsonb)-'oauth_admission_pause',schedulable=CASE WHEN (extra->'local_reasoning_quality_pauses' IS NULL OR extra->'local_reasoning_quality_pauses'='{}'::jsonb) AND (extra->'chanshui_quality_pauses' IS NULL OR extra->'chanshui_quality_pauses'='{}'::jsonb) AND NOT (COALESCE(temp_unschedulable_reason,'') LIKE 'scheduled_quality_check:%' AND temp_unschedulable_until IS NULL) AND (auto_pause_on_expired IS NOT TRUE OR expires_at IS NULL OR expires_at>NOW()) THEN TRUE ELSE FALSE END,updated_at=clock_timestamp() WHERE a.id=$1 AND a.deleted_at IS NULL AND a.status='active' AND a.schedulable IS FALSE AND a.extra->'oauth_admission_pause'->>'token'=$2 AND EXISTS(SELECT 1 FROM oauth_admission_leases l WHERE l.account_id=a.id AND l.token=$2 AND l.lease=$3 AND l.lease_until>NOW() AND l.account_updated_at=a.updated_at)`, job.AccountID, job.Token, job.Lease)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n > 0 {
			if _, e = tx.ExecContext(ctx, `DELETE FROM oauth_admission_leases WHERE account_id=$1 AND token=$2`, job.AccountID, job.Token); e != nil {
				return e
			}
			if e = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &job.AccountID, nil, nil); e != nil {
				return e
			}
			if e = tx.Commit(); e != nil {
				return e
			}
			if r.changed != nil {
				r.changed(ctx, job.AccountID)
			}
			return nil
		}
	}
	_, e = tx.ExecContext(ctx, `UPDATE oauth_admission_leases SET last_outcome=$4,lease='',lease_until=NULL,next_run_at=NOW()+INTERVAL '10 minutes' WHERE account_id=$1 AND token=$2 AND lease=$3`, job.AccountID, job.Token, job.Lease, outcome)
	if e != nil {
		return e
	}
	return tx.Commit()
}
