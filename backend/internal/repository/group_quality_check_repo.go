package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
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

func (r *groupQualityCheckRepository) ListEnabledSettings(ctx context.Context) ([]*service.GroupQualityCheckSettings, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT group_id, enabled, interval_minutes, last_run_at, created_at, updated_at
		FROM group_quality_check_settings
		WHERE enabled = true
		ORDER BY group_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanGroupQualityCheckSettingsList(rows)
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

func (r *groupQualityCheckRepository) UpdateLastRun(ctx context.Context, groupID int64, lastRunAt time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE group_quality_check_settings SET last_run_at = $2, updated_at = NOW() WHERE group_id = $1
	`, groupID, lastRunAt)
	return err
}

func (r *groupQualityCheckRepository) CreateResult(ctx context.Context, result *service.GroupQualityCheckResult) (*service.GroupQualityCheckResult, error) {
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO group_quality_check_results (group_id, account_id, status, error_message, latency_ms, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		RETURNING id, group_id, account_id, status, error_message, latency_ms, created_at
	`, result.GroupID, result.AccountID, result.Status, result.ErrorMessage, result.LatencyMs)

	out := &service.GroupQualityCheckResult{}
	if err := row.Scan(
		&out.ID, &out.GroupID, &out.AccountID, &out.Status, &out.ErrorMessage, &out.LatencyMs, &out.CreatedAt,
	); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *groupQualityCheckRepository) ListRecentResults(ctx context.Context, groupID int64, since time.Time, limit int) ([]*service.GroupQualityCheckResult, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, group_id, account_id, status, error_message, latency_ms, created_at
		FROM group_quality_check_results
		WHERE group_id = $1 AND created_at >= $2
		ORDER BY created_at DESC
		LIMIT $3
	`, groupID, since, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	results := make([]*service.GroupQualityCheckResult, 0, limit)
	for rows.Next() {
		out := &service.GroupQualityCheckResult{}
		if err := rows.Scan(
			&out.ID, &out.GroupID, &out.AccountID, &out.Status, &out.ErrorMessage, &out.LatencyMs, &out.CreatedAt,
		); err != nil {
			return nil, err
		}
		results = append(results, out)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
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
