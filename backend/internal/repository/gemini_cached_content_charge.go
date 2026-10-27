package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *geminiCachedContentRepository) CommitChargedMutation(ctx context.Context, id int64, token string, target time.Time, intent *service.MediaBillingIntent) error {
	if intent == nil || intent.Command.RequestID == "" {
		return errors.New("cached content charge intent is missing")
	}
	payload, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	// The old expiration remains the committed resource state until the frozen
	// ledger command has settled. No crash can publish an unpaid extension.
	result, err := r.sql.ExecContext(ctx, `UPDATE gemini_cached_contents
 SET charge_intent=$4, charge_target_expire_time=$3, billing_ready=FALSE,
 mutation_token=NULL, mutation_lease_until=NULL, updated_at=NOW()
 WHERE id=$1 AND mutation_token=$2 AND deleted_at IS NULL AND charge_intent IS NULL AND billing_ready`, id, token, target, payload)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return service.ErrGeminiCachedContentMutationConflict
	}
	return nil
}

func (r *geminiCachedContentRepository) CompleteCharge(ctx context.Context, id int64, requestID string) error {
	result, err := r.sql.ExecContext(ctx, `UPDATE gemini_cached_contents
 SET expire_time=COALESCE(charge_target_expire_time,expire_time), billing_ready=TRUE,
 charge_intent=NULL, charge_target_expire_time=NULL, updated_at=NOW()
 WHERE id=$1 AND deleted_at IS NULL AND charge_intent->'command'->>'RequestID'=$2`, id, requestID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 1 {
		return nil
	}
	// Two recovering instances may finish the same immutable intent. The ledger
	// already deduplicates it; accepting an already-ready row is safe.
	var ready bool
	if err := r.sql.QueryRowContext(ctx, `SELECT billing_ready AND charge_intent IS NULL FROM gemini_cached_contents WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&ready); err != nil {
		return err
	}
	if !ready {
		return service.ErrGeminiCachedContentMutationConflict
	}
	return nil
}

func (r *geminiCachedContentRepository) ListPendingCharges(ctx context.Context, limit int) ([]*service.GeminiCachedContent, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT `+geminiCachedContentColumns+` FROM gemini_cached_contents
 WHERE deleted_at IS NULL AND charge_intent IS NOT NULL ORDER BY id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var records []*service.GeminiCachedContent
	for rows.Next() {
		record, err := scanGeminiCachedContent(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}
