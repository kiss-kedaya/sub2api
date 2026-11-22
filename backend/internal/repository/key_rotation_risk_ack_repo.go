package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// keyRotationRiskAckRepository 密钥轮换风险确认仓储（raw SQL，append-only）。
//
// 校验路径按 (user_id, expires_at DESC) 索引取最近一条未过期记录，
// 再在 Go 侧比对 cutoff_snapshot，保证「换 cutoff 后旧确认自动失效」。
type keyRotationRiskAckRepository struct {
	db *sql.DB
}

// NewKeyRotationRiskAckRepository 创建风险确认仓储。
func NewKeyRotationRiskAckRepository(db *sql.DB) service.KeyRotationRiskAckRepository {
	return &keyRotationRiskAckRepository{db: db}
}

const keyRotationRiskAckColumns = `id, user_id, acknowledged_at, expires_at,
client_ip, user_agent, cutoff_snapshot, created_at`

func (r *keyRotationRiskAckRepository) Create(ctx context.Context, ack *service.KeyRotationRiskAck) (*service.KeyRotationRiskAck, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil key rotation risk ack repository")
	}
	if ack == nil {
		return nil, fmt.Errorf("nil key rotation risk ack")
	}
	now := ack.AcknowledgedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	expiresAt := ack.ExpiresAt
	if expiresAt.IsZero() {
		return nil, fmt.Errorf("key rotation risk ack expires_at is required")
	}
	cutoff := ack.CutoffSnapshot
	if cutoff.IsZero() {
		return nil, fmt.Errorf("key rotation risk ack cutoff_snapshot is required")
	}
	createdAt := ack.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}

	row := r.db.QueryRowContext(ctx, `
		INSERT INTO key_rotation_risk_acks
			(user_id, acknowledged_at, expires_at, client_ip, user_agent, cutoff_snapshot, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+keyRotationRiskAckColumns,
		ack.UserID,
		now.UTC(),
		expiresAt.UTC(),
		ack.ClientIP,
		ack.UserAgent,
		cutoff.UTC(),
		createdAt.UTC(),
	)
	created, err := scanKeyRotationRiskAck(row)
	if err != nil {
		return nil, fmt.Errorf("insert key rotation risk ack: %w", err)
	}
	return created, nil
}

func (r *keyRotationRiskAckRepository) FindLatestValid(
	ctx context.Context,
	userID int64,
	cutoff time.Time,
	now time.Time,
) (*service.KeyRotationRiskAck, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil key rotation risk ack repository")
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT `+keyRotationRiskAckColumns+`
		FROM key_rotation_risk_acks
		WHERE user_id = $1
		  AND expires_at > $2
		  AND cutoff_snapshot = $3
		ORDER BY expires_at DESC
		LIMIT 1`,
		userID,
		now.UTC(),
		cutoff.UTC(),
	)
	ack, err := scanKeyRotationRiskAck(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find valid key rotation risk ack: %w", err)
	}
	return ack, nil
}

func scanKeyRotationRiskAck(row *sql.Row) (*service.KeyRotationRiskAck, error) {
	var ack service.KeyRotationRiskAck
	err := row.Scan(
		&ack.ID,
		&ack.UserID,
		&ack.AcknowledgedAt,
		&ack.ExpiresAt,
		&ack.ClientIP,
		&ack.UserAgent,
		&ack.CutoffSnapshot,
		&ack.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &ack, nil
}
