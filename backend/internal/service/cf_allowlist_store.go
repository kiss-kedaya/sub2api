package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type CFAllowlistRow struct {
	ID        int64
	UserID    int64
	IP        string
	CFRuleID  string
	CreatedAt time.Time
}

type CFAllowlistRepository struct {
	db *sql.DB
}

func NewCFAllowlistRepository(db *sql.DB) *CFAllowlistRepository {
	return &CFAllowlistRepository{db: db}
}

func (r *CFAllowlistRepository) ListByUser(ctx context.Context, userID int64) ([]CFAllowlistRow, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("cf allowlist repository is not configured")
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT id, user_id, ip, cf_rule_id, created_at
FROM user_cf_ip_allowlist
WHERE user_id = $1
ORDER BY id ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list cf allowlist: %w", err)
	}
	defer rows.Close()
	out := make([]CFAllowlistRow, 0)
	for rows.Next() {
		var row CFAllowlistRow
		if err := rows.Scan(&row.ID, &row.UserID, &row.IP, &row.CFRuleID, &row.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (r *CFAllowlistRepository) CountByUser(ctx context.Context, userID int64) (int, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("cf allowlist repository is not configured")
	}
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_cf_ip_allowlist WHERE user_id = $1`, userID).Scan(&n)
	return n, err
}

func (r *CFAllowlistRepository) Insert(ctx context.Context, userID int64, ip, ruleID string) (CFAllowlistRow, error) {
	var row CFAllowlistRow
	err := r.db.QueryRowContext(ctx, `
INSERT INTO user_cf_ip_allowlist (user_id, ip, cf_rule_id)
VALUES ($1, $2, $3)
RETURNING id, user_id, ip, cf_rule_id, created_at`, userID, ip, ruleID).
		Scan(&row.ID, &row.UserID, &row.IP, &row.CFRuleID, &row.CreatedAt)
	return row, err
}

func (r *CFAllowlistRepository) GetByIDForUser(ctx context.Context, id, userID int64) (CFAllowlistRow, error) {
	var row CFAllowlistRow
	err := r.db.QueryRowContext(ctx, `
SELECT id, user_id, ip, cf_rule_id, created_at
FROM user_cf_ip_allowlist
WHERE id = $1 AND user_id = $2`, id, userID).
		Scan(&row.ID, &row.UserID, &row.IP, &row.CFRuleID, &row.CreatedAt)
	return row, err
}

func (r *CFAllowlistRepository) DeleteByIDForUser(ctx context.Context, id, userID int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM user_cf_ip_allowlist WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
