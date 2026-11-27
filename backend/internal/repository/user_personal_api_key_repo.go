package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	getUserPersonalAPIKeyByUserSQL = `
SELECT user_id, key_prefix, key_hash, created_at, updated_at, last_used_at, last_used_ip
FROM user_personal_api_keys
WHERE user_id = $1`

	getUserPersonalAPIKeyByHashSQL = `
SELECT user_id, key_prefix, key_hash, created_at, updated_at, last_used_at, last_used_ip
FROM user_personal_api_keys
WHERE key_hash = $1`

	upsertUserPersonalAPIKeySQL = `
INSERT INTO user_personal_api_keys (user_id, key_prefix, key_hash, created_at, updated_at, last_used_at, last_used_ip)
VALUES ($1, $2, $3, $4, $5, NULL, '')
ON CONFLICT (user_id) DO UPDATE SET
    key_prefix = EXCLUDED.key_prefix,
    key_hash = EXCLUDED.key_hash,
    created_at = EXCLUDED.created_at,
    updated_at = EXCLUDED.updated_at,
    last_used_at = NULL,
    last_used_ip = ''
RETURNING (xmax <> 0)`

	deleteUserPersonalAPIKeySQL = `
DELETE FROM user_personal_api_keys
WHERE user_id = $1`

	touchUserPersonalAPIKeySQL = `
UPDATE user_personal_api_keys
SET last_used_at = $4, last_used_ip = $3
WHERE user_id = $1 AND key_hash = $2`
)

type userPersonalAPIKeyRepository struct {
	db *sql.DB
}

// NewUserPersonalAPIKeyRepository 创建 user_personal_api_keys 仓储。
func NewUserPersonalAPIKeyRepository(sqlDB *sql.DB) service.UserPersonalAPIKeyRepository {
	return &userPersonalAPIKeyRepository{db: sqlDB}
}

func scanUserPersonalAPIKey(row interface{ Scan(...any) error }) (*service.UserPersonalAPIKey, error) {
	item := &service.UserPersonalAPIKey{}
	var lastUsedAt sql.NullTime
	err := row.Scan(
		&item.UserID,
		&item.KeyPrefix,
		&item.KeyHash,
		&item.CreatedAt,
		&item.UpdatedAt,
		&lastUsedAt,
		&item.LastUsedIP,
	)
	if err == sql.ErrNoRows {
		return nil, service.ErrUserPersonalAPIKeyNotFound
	}
	if err != nil {
		return nil, err
	}
	item.KeyHash = strings.TrimSpace(item.KeyHash)
	if lastUsedAt.Valid {
		t := lastUsedAt.Time.UTC()
		item.LastUsedAt = &t
	}
	return item, nil
}

func (r *userPersonalAPIKeyRepository) GetByUserID(ctx context.Context, userID int64) (*service.UserPersonalAPIKey, error) {
	if r == nil || r.db == nil || userID <= 0 {
		return nil, fmt.Errorf("get user personal api key by user: invalid argument")
	}
	return scanUserPersonalAPIKey(r.db.QueryRowContext(ctx, getUserPersonalAPIKeyByUserSQL, userID))
}

func (r *userPersonalAPIKeyRepository) GetByHash(ctx context.Context, keyHash string) (*service.UserPersonalAPIKey, error) {
	if r == nil || r.db == nil || strings.TrimSpace(keyHash) == "" {
		return nil, fmt.Errorf("get user personal api key by hash: invalid argument")
	}
	return scanUserPersonalAPIKey(r.db.QueryRowContext(ctx, getUserPersonalAPIKeyByHashSQL, strings.TrimSpace(keyHash)))
}

func (r *userPersonalAPIKeyRepository) Upsert(ctx context.Context, key *service.UserPersonalAPIKey) (bool, error) {
	if r == nil || r.db == nil || key == nil || key.UserID <= 0 || key.KeyPrefix == "" || key.KeyHash == "" {
		return false, fmt.Errorf("upsert user personal api key: invalid argument")
	}
	var replaced bool
	err := r.db.QueryRowContext(
		ctx,
		upsertUserPersonalAPIKeySQL,
		key.UserID,
		key.KeyPrefix,
		key.KeyHash,
		key.CreatedAt.UTC(),
		key.UpdatedAt.UTC(),
	).Scan(&replaced)
	if err != nil {
		return false, fmt.Errorf("upsert user personal api key: %w", err)
	}
	return replaced, nil
}

func (r *userPersonalAPIKeyRepository) DeleteByUserID(ctx context.Context, userID int64) (bool, error) {
	if r == nil || r.db == nil || userID <= 0 {
		return false, fmt.Errorf("delete user personal api key: invalid argument")
	}
	res, err := r.db.ExecContext(ctx, deleteUserPersonalAPIKeySQL, userID)
	if err != nil {
		return false, fmt.Errorf("delete user personal api key: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete user personal api key rows affected: %w", err)
	}
	return affected > 0, nil
}

func (r *userPersonalAPIKeyRepository) TouchLastUsed(ctx context.Context, userID int64, expectedHash, clientIP string, usedAt time.Time) error {
	if r == nil || r.db == nil || userID <= 0 || expectedHash == "" {
		return nil
	}
	_, err := r.db.ExecContext(ctx, touchUserPersonalAPIKeySQL, userID, expectedHash, clientIP, usedAt.UTC())
	return err
}
