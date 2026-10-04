package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type groupQualityCheckRepository struct {
	db *sql.DB
}

func NewGroupQualityCheckRepository(db *sql.DB) service.GroupQualityCheckRepository {
	return &groupQualityCheckRepository{db: db}
}

func (r *groupQualityCheckRepository) UpsertSettings(ctx context.Context, s *service.GroupQualityCheckSettings) (*service.GroupQualityCheckSettings, error) {
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO group_quality_check_settings (group_id, enabled, interval_minutes, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (group_id) DO UPDATE
		SET enabled = EXCLUDED.enabled, interval_minutes = EXCLUDED.interval_minutes, updated_at = NOW()
		RETURNING group_id, enabled, interval_minutes, last_run_at, created_at, updated_at
	`, s.GroupID, s.Enabled, s.IntervalMinutes)
	return scanGroupQualityCheckSettings(row)
}

func (r *groupQualityCheckRepository) GetSettings(ctx context.Context, groupID int64) (*service.GroupQualityCheckSettings, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT group_id, enabled, interval_minutes, last_run_at, created_at, updated_at
		FROM group_quality_check_settings WHERE group_id = $1
	`, groupID)
	return scanGroupQualityCheckSettings(row)
}

func (r *groupQualityCheckRepository) ListAllSettings(ctx context.Context) ([]*service.GroupQualityCheckSettings, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT group_id, enabled, interval_minutes, last_run_at, created_at, updated_at
		FROM group_quality_check_settings
		ORDER BY group_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanGroupQualityCheckSettingsList(rows)
}

// ListRecentResults aggregates the group's accounts' scheduled test results.
// Probes are produced by the per-account scheduled test plans, so this only
// reads; nothing is written back. When an account has several plans, the newest
// result per account wins so one noisy account cannot dominate the ratio.
func (r *groupQualityCheckRepository) ListRecentResults(ctx context.Context, groupID int64, since time.Time, limit int) ([]*service.GroupQualityCheckResult, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT ON (p.account_id)
		       r.id, p.account_id, r.status, r.error_message, r.latency_ms, r.created_at
		FROM scheduled_test_results r
		JOIN scheduled_test_plans p ON p.id = r.plan_id
		JOIN account_groups ag ON ag.account_id = p.account_id AND ag.group_id = $1
		JOIN accounts a ON a.id = p.account_id
		WHERE r.created_at >= $2
		  AND a.deleted_at IS NULL
		  AND a.status = 'active'
		  AND (a.schedulable IS TRUE OR (jsonb_typeof(a.extra->'chanshui_quality_pauses')='object' AND a.extra->'chanshui_quality_pauses'<>'{}'::jsonb))
		  AND (a.temp_unschedulable_until IS NULL OR a.temp_unschedulable_until <= NOW())
		ORDER BY p.account_id, r.created_at DESC
		LIMIT $3
	`, groupID, since, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	results := make([]*service.GroupQualityCheckResult, 0, limit)
	for rows.Next() {
		out := &service.GroupQualityCheckResult{GroupID: groupID}
		var errMsg sql.NullString
		if err := rows.Scan(
			&out.ID, &out.AccountID, &out.Status, &errMsg, &out.LatencyMs, &out.CreatedAt,
		); err != nil {
			return nil, err
		}
		out.ErrorMessage = errMsg.String
		results = append(results, out)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// ListGroupBuckets aggregates scheduled test results per group and time bucket.
// Each account contributes only its newest result in the window so several
// plans on one account cannot weight the bucket.
func (r *groupQualityCheckRepository) ListGroupBuckets(ctx context.Context, groupIDs []int64, since time.Time, bucketSeconds int64) ([]service.GroupQualityBucketRow, error) {
	if len(groupIDs) == 0 || bucketSeconds <= 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		WITH latest AS (
			SELECT DISTINCT ON (ag.group_id, p.account_id)
			       ag.group_id,
			       p.account_id,
			       r.status,
			       r.created_at
			FROM scheduled_test_results r
			JOIN scheduled_test_plans p ON p.id = r.plan_id
			JOIN account_groups ag ON ag.account_id = p.account_id
			JOIN accounts a ON a.id = p.account_id
			WHERE ag.group_id = ANY($1) AND r.created_at >= $2
			  AND a.deleted_at IS NULL
			  AND a.status = 'active'
			  AND (a.schedulable IS TRUE OR (jsonb_typeof(a.extra->'chanshui_quality_pauses')='object' AND a.extra->'chanshui_quality_pauses'<>'{}'::jsonb))
			  AND (a.temp_unschedulable_until IS NULL OR a.temp_unschedulable_until <= NOW())
			ORDER BY ag.group_id, p.account_id, r.created_at DESC
		)
		SELECT group_id,
		       to_timestamp(floor(extract(epoch FROM created_at) / $3) * $3) AS bucket_start,
		       COUNT(*) AS checked,
		       COUNT(*) FILTER (WHERE status IN ('degraded', 'audit_fail')) AS degraded
		FROM latest
		WHERE status IN ('success', 'degraded', 'audit_pass', 'audit_fail')
		GROUP BY group_id, bucket_start
		ORDER BY group_id, bucket_start
	`, pq.Array(groupIDs), since, bucketSeconds)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]service.GroupQualityBucketRow, 0, len(groupIDs)*8)
	for rows.Next() {
		var row service.GroupQualityBucketRow
		if err := rows.Scan(&row.GroupID, &row.BucketStart, &row.Checked, &row.Degraded); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListGroupEvents returns the group's most recent probes that produced a
// verdict (success / degraded). Failed and inconclusive runs never surface to
// end users, so the filter lives in SQL rather than the caller.
func (r *groupQualityCheckRepository) ListGroupEvents(ctx context.Context, groupID int64, limit int) ([]*service.GroupQualityEvent, error) {
	if limit <= 0 {
		limit = 30
	} else if limit > 100 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT r.id, ag.group_id, p.account_id, COALESCE(p.model_id, ''),
		       r.status, COALESCE(r.error_message, ''), r.created_at,
		       CASE WHEN r.status IN ('audit_pass', 'audit_fail') THEN COALESCE(r.response_text, '') ELSE '' END
		FROM scheduled_test_results r
		JOIN scheduled_test_plans p ON p.id = r.plan_id
		JOIN account_groups ag ON ag.account_id = p.account_id AND ag.group_id = $1
		JOIN accounts a ON a.id = p.account_id
		WHERE r.status IN ('success', 'degraded', 'audit_pass', 'audit_fail')
		  AND a.deleted_at IS NULL
		  AND a.status = 'active'
		  AND (a.schedulable IS TRUE OR (jsonb_typeof(a.extra->'chanshui_quality_pauses')='object' AND a.extra->'chanshui_quality_pauses'<>'{}'::jsonb))
		  AND (a.temp_unschedulable_until IS NULL OR a.temp_unschedulable_until <= NOW())
		ORDER BY r.created_at DESC, r.id DESC
		LIMIT $2
	`, groupID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]*service.GroupQualityEvent, 0, limit)
	for rows.Next() {
		event := &service.GroupQualityEvent{}
		var rawAudit string
		if err := rows.Scan(
			&event.ID, &event.GroupID, &event.AccountID, &event.ModelID,
			&event.Status, &event.ErrorMessage, &event.CreatedAt, &rawAudit,
		); err != nil {
			return nil, err
		}
		service.PopulateGroupQualityAudit(event, rawAudit)
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetGroupEventArtwork returns one verdict-bearing probe's stored artwork,
// scoped to the group so a viewer cannot read arbitrary results by id.
func (r *groupQualityCheckRepository) GetGroupEventArtwork(ctx context.Context, groupID, resultID int64) (string, error) {
	var text sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT r.response_text
		FROM scheduled_test_results r
		JOIN scheduled_test_plans p ON p.id = r.plan_id
		JOIN account_groups ag ON ag.account_id = p.account_id AND ag.group_id = $1
		JOIN accounts a ON a.id = p.account_id
		WHERE r.id = $2 AND r.status IN ('success', 'degraded')
		  AND a.deleted_at IS NULL
		  AND a.status = 'active'
		  AND (a.schedulable IS TRUE OR (jsonb_typeof(a.extra->'chanshui_quality_pauses')='object' AND a.extra->'chanshui_quality_pauses'<>'{}'::jsonb))
		  AND (a.temp_unschedulable_until IS NULL OR a.temp_unschedulable_until <= NOW())
	`, groupID, resultID).Scan(&text)
	if err == sql.ErrNoRows {
		return "", service.ErrGroupQualityEventNotFound
	}
	if err != nil {
		return "", err
	}
	return text.String, nil
}

func scanGroupQualityCheckSettings(row *sql.Row) (*service.GroupQualityCheckSettings, error) {
	out := &service.GroupQualityCheckSettings{}
	if err := row.Scan(
		&out.GroupID, &out.Enabled, &out.IntervalMinutes, &out.LastRunAt, &out.CreatedAt, &out.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return out, nil
}

func scanGroupQualityCheckSettingsList(rows *sql.Rows) ([]*service.GroupQualityCheckSettings, error) {
	out := make([]*service.GroupQualityCheckSettings, 0, 8)
	for rows.Next() {
		s := &service.GroupQualityCheckSettings{}
		if err := rows.Scan(
			&s.GroupID, &s.Enabled, &s.IntervalMinutes, &s.LastRunAt, &s.CreatedAt, &s.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
