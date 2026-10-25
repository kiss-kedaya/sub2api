package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
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
	if limit <= 0 || limit > service.LiveBalanceOutboxMaxBatchSize {
		limit = service.LiveBalanceOutboxMaxBatchSize
	}
	leaseSeconds := int64(lease / time.Second)
	if leaseSeconds < 1 {
		leaseSeconds = 30
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	rollback := func() { _ = tx.Rollback() }
	var acquired bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1::bigint)`, liveBalanceOutboxClaimLockKey).Scan(&acquired); err != nil {
		rollback()
		return nil, err
	}
	if !acquired {
		rollback()
		return []service.LiveBalanceAdjustmentEvent{}, nil
	}

	rows, err := tx.QueryContext(ctx, `
	WITH active_users AS (
		SELECT h.user_id, first_event.id AS first_id, first_event.available_at, first_event.predecessor_id
		FROM live_balance_adjustment_heads AS h
		CROSS JOIN LATERAL (
			SELECT o.id, o.predecessor_id, o.available_at, o.claimed_at
			FROM live_balance_adjustment_outbox AS o
			WHERE o.user_id = h.user_id AND o.delivered_at IS NULL
			ORDER BY o.id
			LIMIT 1
		) AS first_event
		WHERE first_event.available_at <= NOW()
			AND (first_event.claimed_at IS NULL OR first_event.claimed_at < NOW() - ($3 * INTERVAL '1 second'))
			AND (first_event.predecessor_id = 0 OR NOT EXISTS (
				SELECT 1 FROM live_balance_adjustment_outbox AS predecessor
				WHERE predecessor.id = first_event.predecessor_id AND predecessor.delivered_at IS NULL
			))
		ORDER BY first_event.available_at, first_event.id
		LIMIT 8
	), user_pages AS (
		SELECT active.user_id, page.id, page.predecessor_id, page.available_at, page.claimed_at,
			row_number() OVER (PARTITION BY active.user_id ORDER BY page.id) AS seq,
			lag(page.id) OVER (PARTITION BY active.user_id ORDER BY page.id) AS previous_id
		FROM active_users AS active
		CROSS JOIN LATERAL (
			-- Bound each selected chain page so a single large backlog cannot
			-- materialize unbounded rows while the outer batch remains capped.
			SELECT o.id, o.predecessor_id, o.available_at, o.claimed_at
			FROM live_balance_adjustment_outbox AS o
			WHERE o.user_id = active.user_id AND o.delivered_at IS NULL
			ORDER BY o.id
			LIMIT $4
		) AS page
	), ready_pages AS (
		SELECT page.id, page.seq,
			BOOL_AND(
				(page.seq = 1 OR page.predecessor_id = page.previous_id)
				AND page.available_at <= NOW()
				AND (page.claimed_at IS NULL OR page.claimed_at < NOW() - ($3 * INTERVAL '1 second'))
			) OVER (PARTITION BY page.user_id ORDER BY page.id ROWS UNBOUNDED PRECEDING) AS ready
		FROM user_pages AS page
	), candidates AS (
		SELECT page.id FROM ready_pages AS page
		WHERE page.ready
		-- Round-robin across selected users, always retaining each user's prefix.
		ORDER BY page.seq, page.id
		LIMIT $2
	), claimed AS (
		SELECT o.id
		FROM live_balance_adjustment_outbox AS o
		JOIN candidates AS c ON c.id = o.id
		FOR UPDATE OF o SKIP LOCKED
	)
		UPDATE live_balance_adjustment_outbox AS o
		SET claimed_at = NOW(),
			claimed_by = $1,
			updated_at = NOW()
	FROM claimed AS c
		WHERE o.id = c.id
		RETURNING o.id, o.user_id, o.predecessor_id, o.delta::text, o.attempts, o.created_at
	`, workerID, limit, leaseSeconds, service.LiveBalanceOutboxPerUserMaxBatchSize)
	if err != nil {
		rollback()
		return nil, err
	}

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
			_ = rows.Close()
			rollback()
			return nil, err
		}
		delta, err := decimal.NewFromString(deltaText)
		if err != nil {
			_ = rows.Close()
			rollback()
			return nil, fmt.Errorf("parse live balance adjustment %d: %w", event.ID, err)
		}
		deltaUnits := delta.Shift(liveBalanceMoneyScale)
		if !deltaUnits.Equal(deltaUnits.Truncate(0)) || deltaUnits.IsZero() ||
			deltaUnits.GreaterThan(decimal.NewFromInt(maxExactLuaInteger)) ||
			deltaUnits.LessThan(decimal.NewFromInt(-maxExactLuaInteger)) {
			_ = rows.Close()
			rollback()
			return nil, fmt.Errorf("live balance adjustment %d exceeds exact Redis money range", event.ID)
		}
		event.DeltaUnits = deltaUnits.IntPart()
		events = append(events, event)
	}
	if err := rows.Close(); err != nil {
		rollback()
		return nil, err
	}
	if err := rows.Err(); err != nil {
		rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	sort.Slice(events, func(i, j int) bool { return events[i].ID < events[j].ID })
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

func (r *liveBalanceAdjustmentOutboxRepository) MarkDeliveredBatch(ctx context.Context, ids []int64, workerID string) error {
	if r == nil || r.db == nil {
		return errors.New("live balance adjustment outbox database is unavailable")
	}
	if len(ids) == 0 {
		return nil
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE live_balance_adjustment_outbox
		SET delivered_at = NOW(), claimed_at = NULL, claimed_by = NULL,
			last_error = NULL, updated_at = NOW()
		WHERE id = ANY($1::bigint[]) AND claimed_by = $2 AND delivered_at IS NULL
	`, pq.Array(ids), workerID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != int64(len(ids)) {
		return fmt.Errorf("live balance adjustment batch claim changed: updated %d of %d", affected, len(ids))
	}
	return nil
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

func (r *liveBalanceAdjustmentOutboxRepository) RetryClaimedBatch(
	ctx context.Context, ids []int64, workerID string, availableAt time.Time, lastError string,
) error {
	if r == nil || r.db == nil {
		return errors.New("live balance adjustment outbox database is unavailable")
	}
	if len(ids) == 0 {
		return nil
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE live_balance_adjustment_outbox
		SET attempts = attempts + 1, available_at = $3, last_error = $4,
			claimed_at = NULL, claimed_by = NULL, updated_at = NOW()
		WHERE id = ANY($1::bigint[]) AND claimed_by = $2 AND delivered_at IS NULL
	`, pq.Array(ids), workerID, availableAt, lastError)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != int64(len(ids)) {
		return fmt.Errorf("live balance adjustment batch claim changed during retry: updated %d of %d", affected, len(ids))
	}
	return nil
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
