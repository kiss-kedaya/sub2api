package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/shopspring/decimal"
)

const liveBalanceOutboxClaimLockKey int64 = 0x535542324f555442

type liveBalanceAdjustmentOutboxRepository struct {
	db *sql.DB
}

func NewLiveBalanceAdjustmentOutboxRepository(db *sql.DB) service.LiveBalanceAdjustmentOutboxRepository {
	return &liveBalanceAdjustmentOutboxRepository{db: db}
}

func (r *liveBalanceAdjustmentOutboxRepository) Claim(
	ctx context.Context,
	workerID string,
	limit int,
	lease time.Duration,
) ([]service.LiveBalanceAdjustmentEvent, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("live balance adjustment outbox database is unavailable")
	}
	if limit <= 0 {
		limit = 200
	}
	leaseSeconds := int64(lease / time.Second)
	if leaseSeconds < 1 {
		leaseSeconds = 30
	}

	rows, err := r.db.QueryContext(ctx, `
		WITH claim_lock AS (
			SELECT pg_try_advisory_xact_lock($4::bigint) AS acquired
		), candidates AS (
			SELECT candidate.id
			FROM live_balance_adjustment_outbox AS candidate
			CROSS JOIN claim_lock
			WHERE candidate.delivered_at IS NULL
				AND claim_lock.acquired
				AND candidate.available_at <= NOW()
				AND (candidate.claimed_at IS NULL OR candidate.claimed_at < NOW() - ($3 * INTERVAL '1 second'))
				AND NOT EXISTS (
					SELECT 1
					FROM live_balance_adjustment_outbox AS predecessor
					WHERE predecessor.user_id = candidate.user_id
						AND predecessor.delivered_at IS NULL
						AND predecessor.id < candidate.id
				)
			ORDER BY candidate.available_at, candidate.id
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE live_balance_adjustment_outbox AS o
		SET claimed_at = NOW(),
			claimed_by = $1,
			updated_at = NOW()
		FROM candidates AS c
		WHERE o.id = c.id
		RETURNING o.id, o.user_id, o.predecessor_id, o.delta::text, o.attempts, o.created_at
	`, workerID, limit, leaseSeconds, liveBalanceOutboxClaimLockKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	events := make([]service.LiveBalanceAdjustmentEvent, 0, limit)
	for rows.Next() {
		var event service.LiveBalanceAdjustmentEvent
		var deltaText string
		if err := rows.Scan(
			&event.ID,
			&event.UserID,
			&event.PredecessorID,
			&deltaText,
			&event.Attempts,
			&event.CreatedAt,
		); err != nil {
			return nil, err
		}
		delta, err := decimal.NewFromString(deltaText)
		if err != nil {
			return nil, fmt.Errorf("parse live balance adjustment %d: %w", event.ID, err)
		}
		deltaUnits := delta.Shift(liveBalanceMoneyScale)
		if !deltaUnits.Equal(deltaUnits.Truncate(0)) || deltaUnits.IsZero() ||
			deltaUnits.GreaterThan(decimal.NewFromInt(maxExactLuaInteger)) ||
			deltaUnits.LessThan(decimal.NewFromInt(-maxExactLuaInteger)) {
			return nil, fmt.Errorf("live balance adjustment %d exceeds exact Redis money range", event.ID)
		}
		event.DeltaUnits = deltaUnits.IntPart()
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *liveBalanceAdjustmentOutboxRepository) MarkDelivered(ctx context.Context, id int64, workerID string) error {
	if r == nil || r.db == nil {
		return errors.New("live balance adjustment outbox database is unavailable")
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE live_balance_adjustment_outbox
		SET delivered_at = NOW(),
			claimed_at = NULL,
			claimed_by = NULL,
			last_error = NULL,
			updated_at = NOW()
		WHERE id = $1 AND claimed_by = $2 AND delivered_at IS NULL
	`, id, workerID)
	if err != nil {
		return err
	}
	return requireLiveBalanceOutboxClaim(result, id, workerID, "mark delivered")
}

func (r *liveBalanceAdjustmentOutboxRepository) RetryClaimed(
	ctx context.Context,
	id int64,
	workerID string,
	availableAt time.Time,
	lastError string,
) error {
	if r == nil || r.db == nil {
		return errors.New("live balance adjustment outbox database is unavailable")
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE live_balance_adjustment_outbox
		SET attempts = attempts + 1,
			available_at = $3,
			last_error = $4,
			claimed_at = NULL,
			claimed_by = NULL,
			updated_at = NOW()
		WHERE id = $1 AND claimed_by = $2 AND delivered_at IS NULL
	`, id, workerID, availableAt, lastError)
	if err != nil {
		return err
	}
	return requireLiveBalanceOutboxClaim(result, id, workerID, "retry")
}

func requireLiveBalanceOutboxClaim(result sql.Result, id int64, workerID, operation string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("live balance adjustment %d is no longer claimed by %s during %s", id, workerID, operation)
	}
	return nil
}

func (r *liveBalanceAdjustmentOutboxRepository) DeleteDelivered(
	ctx context.Context,
	before time.Time,
	limit int,
) (int64, error) {
	if r == nil || r.db == nil {
		return 0, errors.New("live balance adjustment outbox database is unavailable")
	}
	if limit <= 0 {
		limit = 10000
	}
	result, err := r.db.ExecContext(ctx, `
		WITH doomed AS (
			SELECT id
			FROM live_balance_adjustment_outbox
			WHERE delivered_at IS NOT NULL AND delivered_at < $1
			ORDER BY delivered_at, id
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		DELETE FROM live_balance_adjustment_outbox AS o
		USING doomed
		WHERE o.id = doomed.id
	`, before, limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (r *liveBalanceAdjustmentOutboxRepository) Stats(ctx context.Context) (service.LiveBalanceAdjustmentOutboxStats, error) {
	var (
		stats     service.LiveBalanceAdjustmentOutboxStats
		oldest    sql.NullTime
		lastError sql.NullString
	)
	if r == nil || r.db == nil {
		return stats, errors.New("live balance adjustment outbox database is unavailable")
	}
	err := r.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE delivered_at IS NULL),
			COUNT(*) FILTER (WHERE delivered_at IS NOT NULL),
			MIN(created_at) FILTER (WHERE delivered_at IS NULL),
			COALESCE(MAX(attempts) FILTER (WHERE delivered_at IS NULL), 0),
			(
				SELECT last_error
				FROM live_balance_adjustment_outbox
				WHERE delivered_at IS NULL AND last_error IS NOT NULL
				ORDER BY available_at DESC, id DESC
				LIMIT 1
			)
		FROM live_balance_adjustment_outbox
	`).Scan(&stats.Pending, &stats.Delivered, &oldest, &stats.MaxAttempts, &lastError)
	if err != nil {
		return stats, err
	}
	if oldest.Valid {
		value := oldest.Time
		stats.OldestCreatedAt = &value
	}
	if lastError.Valid {
		stats.LastError = lastError.String
	}
	return stats, nil
}
