package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"

	entsql "entgo.io/ent/dialect/sql"
)

// ClaimCodexQuotaOverdraftProbe atomically reserves one quota cycle. A cycle is
// claimed at most once across all sub2api replicas.
func (r *accountRepository) ClaimCodexQuotaOverdraftProbe(
	ctx context.Context,
	id int64,
	state *service.CodexQuotaOverdraftProbeState,
) (bool, error) {
	if state == nil || strings.TrimSpace(state.CycleKey) == "" {
		return false, nil
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return false, err
	}
	recoverAt := time.Time{}
	if state.RecoverAt != nil {
		recoverAt = state.RecoverAt.UTC()
	}
	result, err := r.sql.ExecContext(ctx, `
		UPDATE accounts
		SET extra = COALESCE(extra, '{}'::jsonb) || jsonb_build_object($1::text, $2::jsonb),
			updated_at = NOW()
		WHERE id = $3
			AND deleted_at IS NULL
			AND (
				COALESCE(extra #>> '{codex_quota_overdraft_probe,cycle_key}', '') = ''
			OR (
				COALESCE(extra #>> '{codex_quota_overdraft_probe,cycle_key}', '') = $4
				AND (
					(
						extra #>> '{codex_quota_overdraft_probe,status}' = 'pending'
						AND CASE
							WHEN pg_input_is_valid(COALESCE(extra #>> '{codex_quota_overdraft_probe,started_at}', ''), 'timestamptz'::regtype)
							THEN (extra #>> '{codex_quota_overdraft_probe,started_at}')::timestamptz
							ELSE '1970-01-01'::timestamptz
						END <= NOW() - INTERVAL '2 minutes'
					)
					OR (
						extra #>> '{codex_quota_overdraft_probe,status}' = 'inconclusive'
						AND pg_input_is_valid(COALESCE(extra #>> '{codex_quota_overdraft_probe,retry_at}', ''), 'timestamptz'::regtype)
						AND (extra #>> '{codex_quota_overdraft_probe,retry_at}')::timestamptz <= NOW()
					)
				)
			)
				OR (
					-- A newer cycle may replace a recovered state immediately, or an
					-- older failed/intermediate state only after its own reset elapsed.
					-- This prevents an active terminal failure from being overwritten
					-- by a later-looking stale snapshot.
					COALESCE(extra #>> '{codex_quota_overdraft_probe,cycle_key}', '') <> $4
					AND $5::timestamptz > CASE
						WHEN pg_input_is_valid(COALESCE(extra #>> '{codex_quota_overdraft_probe,recover_at}', ''), 'timestamptz'::regtype)
						THEN (extra #>> '{codex_quota_overdraft_probe,recover_at}')::timestamptz
						ELSE '1970-01-01'::timestamptz
					END
					AND (
						extra #>> '{codex_quota_overdraft_probe,status}' = 'recovered'
						OR (
							extra #>> '{codex_quota_overdraft_probe,status}' IN ('failed', 'inconclusive', 'pending')
							AND pg_input_is_valid(COALESCE(extra #>> '{codex_quota_overdraft_probe,recover_at}', ''), 'timestamptz'::regtype)
							AND (extra #>> '{codex_quota_overdraft_probe,recover_at}')::timestamptz <= NOW()
						)
					)
				)
			)
	`, service.CodexQuotaOverdraftProbeExtraKey, string(payload), id, state.CycleKey, recoverAt)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return false, err
	}
	r.syncSchedulerAccountSnapshot(ctx, id)
	return true, nil
}

// PersistCodexQuotaOverdraftProbeUnlessFailed stores a non-failure result while
// preserving a terminal failure already confirmed for the same quota cycle.
func (r *accountRepository) PersistCodexQuotaOverdraftProbeUnlessFailed(
	ctx context.Context,
	id int64,
	state *service.CodexQuotaOverdraftProbeState,
) (bool, error) {
	if state == nil || state.Status == "failed" || strings.TrimSpace(state.CycleKey) == "" {
		return false, nil
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return false, err
	}
	recoverAt := time.Time{}
	if state.RecoverAt != nil {
		recoverAt = state.RecoverAt.UTC()
	}
	result, err := r.sql.ExecContext(ctx, `
		UPDATE accounts
		SET extra = COALESCE(extra, '{}'::jsonb) || jsonb_build_object($1::text, $2::jsonb),
			updated_at = NOW()
		WHERE id = $3
			AND deleted_at IS NULL
			AND (
				COALESCE(extra #>> '{codex_quota_overdraft_probe,cycle_key}', '') = ''
				OR (
					COALESCE(extra #>> '{codex_quota_overdraft_probe,cycle_key}', '') = $4
					AND COALESCE(extra #>> '{codex_quota_overdraft_probe,status}', '') NOT IN ('failed', 'recovered')
				)
				OR (
					COALESCE(extra #>> '{codex_quota_overdraft_probe,cycle_key}', '') <> $4
					AND $5::timestamptz > CASE
						WHEN pg_input_is_valid(COALESCE(extra #>> '{codex_quota_overdraft_probe,recover_at}', ''), 'timestamptz'::regtype)
						THEN (extra #>> '{codex_quota_overdraft_probe,recover_at}')::timestamptz
						ELSE '1970-01-01'::timestamptz
					END
					AND (
						extra #>> '{codex_quota_overdraft_probe,status}' = 'recovered'
						OR (
							extra #>> '{codex_quota_overdraft_probe,status}' IN ('failed', 'inconclusive', 'pending')
							AND pg_input_is_valid(COALESCE(extra #>> '{codex_quota_overdraft_probe,recover_at}', ''), 'timestamptz'::regtype)
							AND (extra #>> '{codex_quota_overdraft_probe,recover_at}')::timestamptz <= NOW()
						)
					)
				)
			)
	`, service.CodexQuotaOverdraftProbeExtraKey, string(payload), id, state.CycleKey, recoverAt)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return false, err
	}
	r.syncSchedulerAccountSnapshot(ctx, id)
	return true, nil
}

// ClearCodexQuotaOverdraftPauseIfState clears stale scheduling state only when
// the persisted overdraft result is still the expected available state.
func (r *accountRepository) ClearCodexQuotaOverdraftPauseIfState(
	ctx context.Context,
	id int64,
	cycleKey string,
	status string,
	expectedTempReason string,
) (bool, bool, error) {
	if id <= 0 || strings.TrimSpace(cycleKey) == "" || (status != "passed" && status != "recovered") {
		return false, false, nil
	}
	beginner, ok := r.sql.(codexQuotaOverdraftTxBeginner)
	if !ok {
		return false, false, errors.New("account repository does not support transactions")
	}
	tx, err := beginner.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = tx.Rollback() }()

	var clearedTemp bool
	err = tx.QueryRowContext(ctx, `
		WITH target AS (
			SELECT id,
				($4::text <> '' AND COALESCE(temp_unschedulable_reason = $4, FALSE)) AS clear_temp
			FROM accounts
			WHERE id = $1
				AND deleted_at IS NULL
				AND extra #>> '{codex_quota_overdraft_probe,cycle_key}' = $2
				AND extra #>> '{codex_quota_overdraft_probe,status}' = $3
			FOR UPDATE
		)
		UPDATE accounts AS a
		SET temp_unschedulable_until = CASE WHEN target.clear_temp THEN NULL ELSE a.temp_unschedulable_until END,
			temp_unschedulable_reason = CASE WHEN target.clear_temp THEN NULL ELSE a.temp_unschedulable_reason END,
			updated_at = NOW()
		FROM target
		WHERE a.id = target.id
			AND target.clear_temp
		RETURNING target.clear_temp
	`, id, cycleKey, status, expectedTempReason).Scan(&clearedTemp)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &id, nil, nil); err != nil {
		return false, false, err
	}
	if err := tx.Commit(); err != nil {
		return false, false, err
	}
	r.syncSchedulerAccountSnapshotDetached(ctx, id)
	return false, clearedTemp, nil
}

type codexQuotaOverdraftTxBeginner interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}

// FinalizeCodexQuotaOverdraftProbeFailed persists the terminal probe state,
// account pause, and scheduler notification in one transaction.
func (r *accountRepository) FinalizeCodexQuotaOverdraftProbeFailed(
	ctx context.Context,
	id int64,
	state *service.CodexQuotaOverdraftProbeState,
	until time.Time,
	reason string,
) (bool, error) {
	if state == nil || state.Status != "failed" || strings.TrimSpace(state.CycleKey) == "" || until.IsZero() {
		return false, nil
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return false, err
	}
	beginner, ok := r.sql.(codexQuotaOverdraftTxBeginner)
	if !ok {
		return false, errors.New("account repository does not support transactions")
	}
	tx, err := beginner.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
		UPDATE accounts
		SET extra = COALESCE(extra, '{}'::jsonb) || jsonb_build_object($1::text, $2::jsonb),
			temp_unschedulable_until = CASE
				WHEN temp_unschedulable_until IS NULL OR temp_unschedulable_until < $3 THEN $3
				ELSE temp_unschedulable_until
			END,
			temp_unschedulable_reason = CASE
				WHEN temp_unschedulable_until IS NULL OR temp_unschedulable_until < $3 THEN $4
				ELSE temp_unschedulable_reason
			END,
			updated_at = NOW()
		WHERE id = $5
			AND deleted_at IS NULL
			AND extra #>> '{codex_quota_overdraft_probe,cycle_key}' = $6
			AND (
				extra #>> '{codex_quota_overdraft_probe,status}' IN ('pending', 'failed')
				OR ($7::boolean AND extra #>> '{codex_quota_overdraft_probe,status}' IN ('passed', 'inconclusive'))
			)
	`, service.CodexQuotaOverdraftProbeExtraKey, string(payload), until, reason, id, state.CycleKey, state.ReasonCode == "business_quota_limited")
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
	if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &id, nil, nil); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	r.syncSchedulerAccountSnapshotDetached(ctx, id)
	return true, nil
}

func extendCodexQuotaOverdraftTempUnschedulablePredicates(
	ctx context.Context,
	s *entsql.Selector,
	predicates []*entsql.Predicate,
) []*entsql.Predicate {
	if !service.CodexQuotaOverdraftSchedulingEnabled(ctx) {
		return predicates
	}
	reasonCol := s.C("temp_unschedulable_reason")
	return append(predicates, entsql.And(
		entsql.EQ(s.C("platform"), service.PlatformOpenAI),
		entsql.EQ(s.C("type"), service.AccountTypeOAuth),
		entsql.IsNull(s.C("parent_account_id")),
		entsql.Contains(reasonCol, `"source":"`+service.AccountSchedulingThresholdReasonSource+`"`),
	))
}
