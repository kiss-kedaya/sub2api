package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// ListDueModelWhitelistAutoSyncAccounts returns active API-key accounts that
// opted into model whitelist auto-sync, ordered by their stored next_run_at so
// the earliest due accounts survive the LIMIT. Timestamps are stored as
// RFC3339 UTC strings, which sort chronologically; accounts that never ran sort
// first (empty string).
//
// The SQL only filters candidates; run-state parsing stays in Go so a malformed
// value cannot break the whole scan (the runner treats it as "never ran").
func (r *accountRepository) ListDueModelWhitelistAutoSyncAccounts(ctx context.Context, now time.Time, limit int) ([]service.Account, error) {
	if limit <= 0 {
		return []service.Account{}, nil
	}
	if r.sql == nil {
		return nil, errors.New("account repository SQL executor not configured")
	}

	rows, err := r.sql.QueryContext(ctx, `
		SELECT id
		FROM accounts
		WHERE deleted_at IS NULL
			AND status = 'active'
			AND type = 'apikey'
			AND extra @> '{"model_whitelist_auto_sync":{"enabled":true}}'::jsonb
		ORDER BY COALESCE(extra #>> '{model_whitelist_auto_sync_state,next_run_at}', '') ASC,
			id ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	ids := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []service.Account{}, nil
	}

	loaded, err := r.GetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	accounts := make([]service.Account, 0, len(loaded))
	for _, account := range loaded {
		if account == nil {
			continue
		}
		accounts = append(accounts, *account)
	}
	return accounts, nil
}

// UpdateAccountModelMappingCAS replaces credentials.model_mapping only when the
// stored mapping still equals expected. It returns false (no error) when a
// concurrent edit changed the mapping or deleted the account, which the runner
// reports as skipped instead of overwriting somebody else's work.
//
// A single statement is used so the guard and the write cannot interleave. Only
// the model_mapping key is touched: credentials.tokens and every other
// credential stay exactly as they are, so a token refresh racing this job is
// never clobbered.
func (r *accountRepository) UpdateAccountModelMappingCAS(ctx context.Context, id int64, expected, next map[string]string) (bool, error) {
	if r.sql == nil {
		return false, errors.New("account repository SQL executor not configured")
	}
	nextPayload, err := json.Marshal(next)
	if err != nil {
		return false, err
	}
	expectedPayload, err := json.Marshal(expected)
	if err != nil {
		return false, err
	}

	result, err := r.sql.ExecContext(ctx, `
		UPDATE accounts
		SET credentials = jsonb_set(COALESCE(credentials, '{}'::jsonb), '{model_mapping}', $1::jsonb, true),
			updated_at = NOW()
		WHERE id = $2
			AND deleted_at IS NULL
			AND COALESCE(credentials -> 'model_mapping', '{}'::jsonb) = $3::jsonb
	`, string(nextPayload), id, string(expectedPayload))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil
	}
	if r.client != nil {
		_ = enqueueSchedulerOutbox(ctx, r.sql, service.SchedulerOutboxEventAccountChanged, &id, nil, nil)
	}
	r.syncSchedulerAccountSnapshot(ctx, id)
	return true, nil
}
