package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type upstreamMonitorRepository struct{ db *sql.DB }

// NewUpstreamMonitorRepository builds the read model used by the operations
// center upstream monitor. It only reads accounts.extra and the two bounded
// history tables added by migration 307.
func NewUpstreamMonitorRepository(db *sql.DB) service.UpstreamMonitorRepository {
	return &upstreamMonitorRepository{db: db}
}

const upstreamMonitorListCTE = `
WITH src AS (
  SELECT a.id, a.name, a.platform, a.type, a.status,
         a.extra->'upstream_billing_probe' AS probe,
         a.extra->>'upstream_billing_rate_sync_enabled' AS rate_sync_text
  FROM accounts a
  WHERE a.deleted_at IS NULL
    AND ($1 = '' OR a.platform = $1)
    AND ($2 = '' OR a.type = $2)
    AND ($3 = '' OR a.status = $3)
    AND ($4 = '' OR a.name ILIKE '%' || $4 || '%')
), shaped AS (
  SELECT id, name, platform, type, status, rate_sync_text,
    NULLIF(probe->'balance'->'data'->>'remaining','') AS balance_text,
    NULLIF(probe->'balance'->'data'->>'unit','') AS unit,
    NULLIF(probe->'balance'->'data'->>'unlimited','') AS unlimited_text,
    NULLIF(probe->'balance'->>'status','') AS balance_status,
    NULLIF(probe->'balance'->>'received_at','') AS balance_received_at,
    NULLIF(probe->'balance'->>'fresh_until','') AS balance_fresh_until,
    NULLIF(probe->'balance'->>'last_error','') AS balance_error,
    NULLIF(probe->'data'->>'effective_rate_multiplier','') AS rate_text,
    NULLIF(probe->>'status','') AS rate_status,
    NULLIF(probe->>'received_at','') AS rate_received_at,
    NULLIF(probe->>'last_error','') AS rate_error,
    NULLIF(probe->>'synced_rate_multiplier','') AS rate_synced_text
  FROM src
), valued AS (
  SELECT *, CASE WHEN balance_text ~ '^-?[0-9]+(\.[0-9]+)?$' THEN balance_text::numeric END AS balance_num
  FROM shaped
)`

const upstreamMonitorOverviewCTE = `
WITH shaped AS (
  SELECT id, name, platform, type, status, rate_sync_text,
    NULLIF(extra->'upstream_billing_probe'->'balance'->'data'->>'remaining','') AS balance_text,
    NULLIF(extra->'upstream_billing_probe'->'balance'->'data'->>'unit','') AS unit,
    NULLIF(extra->'upstream_billing_probe'->'balance'->'data'->>'unlimited','') AS unlimited_text,
    NULLIF(extra->'upstream_billing_probe'->'balance'->>'status','') AS balance_status,
    NULLIF(extra->'upstream_billing_probe'->'balance'->>'received_at','') AS balance_received_at,
    NULLIF(extra->'upstream_billing_probe'->'balance'->>'fresh_until','') AS balance_fresh_until,
    NULLIF(extra->'upstream_billing_probe'->'balance'->>'last_error','') AS balance_error,
    NULLIF(extra->'upstream_billing_probe'->'data'->>'effective_rate_multiplier','') AS rate_text,
    NULLIF(extra->'upstream_billing_probe'->>'status','') AS rate_status,
    NULLIF(extra->'upstream_billing_probe'->>'received_at','') AS rate_received_at,
    NULLIF(extra->'upstream_billing_probe'->>'last_error','') AS rate_error,
    NULLIF(extra->'upstream_billing_probe'->>'synced_rate_multiplier','') AS rate_synced_text,
    extra->>'upstream_billing_rate_sync_enabled' AS rate_sync_text
  FROM accounts
  WHERE deleted_at IS NULL
), valued AS (
  SELECT *, CASE WHEN balance_text ~ '^-?[0-9]+(\.[0-9]+)?$' THEN balance_text::numeric END AS balance_num
  FROM shaped
)`

const upstreamMonitorSelectColumns = `
  v.id, v.name, v.platform, v.type, v.status,
  v.balance_text, v.unit, v.unlimited_text, v.balance_status, v.balance_received_at, v.balance_fresh_until, v.balance_error,
  v.rate_text, v.rate_status, v.rate_received_at, v.rate_error, v.rate_synced_text, v.rate_sync_text,
  st.last_sample_at`

// The status filter treats a fresh-window expiry as stale even though the
// stored status is still "ok", matching the value the UI displays.
const upstreamMonitorStatusFilter = ` AND (
  $5 = '' OR $5 = 'all'
  OR ($5 = 'stale' AND COALESCE(v.balance_status,'unknown') = 'stale')
  OR ($5 = 'stale' AND v.balance_status = 'ok' AND v.balance_fresh_until IS NOT NULL AND v.balance_fresh_until::timestamptz < NOW())
  OR ($5 NOT IN ('','all','stale') AND COALESCE(v.balance_status,'unknown') = $5)
)`

func upstreamMonitorOrder(sort string) string {
	switch sort {
	case "balance_asc":
		return "v.balance_num ASC NULLS LAST, v.name ASC, v.id ASC"
	case "balance_desc":
		return "v.balance_num DESC NULLS LAST, v.name ASC, v.id ASC"
	default:
		return "v.name ASC, v.id ASC"
	}
}

func (r *upstreamMonitorRepository) ListAccounts(ctx context.Context, filter service.UpstreamMonitorFilter) ([]service.UpstreamMonitorAccount, int64, error) {
	args := []any{filter.Platform, filter.AccountType, filter.AccountStatus, filter.Search, filter.BalanceStatus}
	var total int64
	if err := r.db.QueryRowContext(ctx, upstreamMonitorListCTE+`
SELECT COUNT(*) FROM valued v WHERE TRUE`+upstreamMonitorStatusFilter, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []service.UpstreamMonitorAccount{}, 0, nil
	}

	pageArgs := append(append([]any{}, args...), filter.PageSize, (filter.Page-1)*filter.PageSize)
	query := upstreamMonitorListCTE + `
SELECT` + upstreamMonitorSelectColumns + `
FROM valued v
LEFT JOIN upstream_monitor_state st ON st.account_id = v.id
WHERE TRUE` + upstreamMonitorStatusFilter + `
ORDER BY ` + upstreamMonitorOrder(filter.Sort) + `
LIMIT $6 OFFSET $7`
	rows, err := r.db.QueryContext(ctx, query, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	items := make([]service.UpstreamMonitorAccount, 0, filter.PageSize)
	for rows.Next() {
		item, err := scanUpstreamMonitorAccount(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func scanUpstreamMonitorAccount(row scannable) (service.UpstreamMonitorAccount, error) {
	var item service.UpstreamMonitorAccount
	var balanceText, unit, unlimitedText, balanceStatus, balanceRead, balanceFresh, balanceErr sql.NullString
	var rateText, rateStatus, rateRead, rateErr, rateSynced, rateSync sql.NullString
	var lastSample sql.NullTime
	err := row.Scan(
		&item.AccountID, &item.Name, &item.Platform, &item.Type, &item.Status,
		&balanceText, &unit, &unlimitedText, &balanceStatus, &balanceRead, &balanceFresh, &balanceErr,
		&rateText, &rateStatus, &rateRead, &rateErr, &rateSynced, &rateSync, &lastSample,
	)
	if err != nil {
		return item, err
	}
	if value, ok := service.ParseUpstreamMonitorNumber(balanceText.String); ok {
		item.Balance = &value
	}
	item.Unit = unit.String
	item.Unlimited = unlimitedText.String == "true"
	if balanceStatus.Valid {
		item.BalanceStatus = balanceStatus.String
	}
	item.BalanceReadAt = service.ParseUpstreamSnapshotTime(balanceRead.String)
	item.BalanceFreshTo = service.ParseUpstreamSnapshotTime(balanceFresh.String)
	item.BalanceError = balanceErr.String
	if value, ok := service.ParseUpstreamMonitorNumber(rateText.String); ok {
		item.Rate = &value
	}
	item.RateStatus = rateStatus.String
	item.RateError = rateErr.String
	if value, ok := service.ParseUpstreamMonitorNumber(rateSynced.String); ok {
		item.RateSynced = &value
	}
	item.RateSyncEnabled = rateSync.String == "true"
	item.RateReadAt = service.ParseUpstreamSnapshotTime(rateRead.String)
	if lastSample.Valid {
		value := lastSample.Time.UTC()
		item.LastSampledAt = &value
	}
	return item, nil
}

func (r *upstreamMonitorRepository) Overview(ctx context.Context, now time.Time) (service.UpstreamMonitorOverview, error) {
	overview := service.UpstreamMonitorOverview{GeneratedAt: now}
	err := r.db.QueryRowContext(ctx, upstreamMonitorOverviewCTE+`
SELECT
  COUNT(*),
  COUNT(*) FILTER (WHERE v.balance_num IS NOT NULL),
  COUNT(*) FILTER (WHERE v.balance_status = 'ok' AND (v.balance_fresh_until IS NULL OR v.balance_fresh_until::timestamptz >= NOW())),
  COUNT(*) FILTER (WHERE v.balance_status = 'failed'),
  COUNT(*) FILTER (WHERE v.balance_status = 'unsupported'),
  COUNT(*) FILTER (WHERE COALESCE(v.balance_status,'unknown') = 'stale' OR (v.balance_status = 'ok' AND v.balance_fresh_until IS NOT NULL AND v.balance_fresh_until::timestamptz < NOW())),
  COUNT(*) FILTER (WHERE v.rate_text IS NOT NULL)
FROM valued v`).Scan(
		&overview.TotalAccounts,
		&overview.WithBalance,
		&overview.Healthy,
		&overview.Failed,
		&overview.Unsupported,
		&overview.Stale,
		&overview.RateKnown,
	)
	if err != nil {
		return overview, err
	}

	currencyRows, err := r.db.QueryContext(ctx, upstreamMonitorOverviewCTE+`
SELECT COALESCE(v.unit,''), SUM(v.balance_num)::float8, COUNT(*)
FROM valued v
WHERE v.balance_num IS NOT NULL
GROUP BY COALESCE(v.unit,'')
ORDER BY SUM(v.balance_num) DESC`)
	if err != nil {
		return overview, err
	}
	defer func() { _ = currencyRows.Close() }()
	overview.Currencies = make([]service.UpstreamCurrencyTotal, 0, 4)
	for currencyRows.Next() {
		var total service.UpstreamCurrencyTotal
		if err := currencyRows.Scan(&total.Unit, &total.Total, &total.Accounts); err != nil {
			return overview, err
		}
		overview.Currencies = append(overview.Currencies, total)
	}
	if err := currencyRows.Err(); err != nil {
		return overview, err
	}

	attentionRows, err := r.db.QueryContext(ctx, upstreamMonitorOverviewCTE+`
SELECT`+upstreamMonitorSelectColumns+`
FROM valued v
LEFT JOIN upstream_monitor_state st ON st.account_id = v.id
WHERE v.balance_status IS NOT NULL AND (COALESCE(v.balance_status,'unknown') <> 'ok'
   OR (v.balance_status = 'ok' AND v.balance_fresh_until IS NOT NULL AND v.balance_fresh_until::timestamptz < NOW()))
ORDER BY CASE
    WHEN COALESCE(v.balance_status,'unknown') = 'failed' THEN 0
    WHEN COALESCE(v.balance_status,'unknown') = 'unsupported' THEN 1
    WHEN COALESCE(v.balance_status,'unknown') = 'stale' THEN 2
    WHEN v.balance_status = 'ok' AND v.balance_fresh_until IS NOT NULL AND v.balance_fresh_until::timestamptz < NOW() THEN 3
    ELSE 4 END,
  v.name ASC, v.id ASC
LIMIT 5`)
	if err != nil {
		return overview, err
	}
	defer func() { _ = attentionRows.Close() }()
	overview.Attention = make([]service.UpstreamMonitorAccount, 0, 5)
	for attentionRows.Next() {
		item, err := scanUpstreamMonitorAccount(attentionRows)
		if err != nil {
			return overview, err
		}
		overview.Attention = append(overview.Attention, item)
	}
	if err := attentionRows.Err(); err != nil {
		return overview, err
	}

	if err := r.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM upstream_rate_changes WHERE changed_at >= NOW() - INTERVAL '24 hours'`).Scan(&overview.RateChanges24h); err != nil {
		return overview, err
	}
	var lastSample sql.NullTime
	if err := r.db.QueryRowContext(ctx, `SELECT MAX(sampled_at) FROM upstream_balance_samples`).Scan(&lastSample); err != nil {
		return overview, err
	}
	if lastSample.Valid {
		value := lastSample.Time.UTC()
		overview.LastSampleAt = &value
	}
	return overview, nil
}

func (r *upstreamMonitorRepository) BalanceHistory(ctx context.Context, accountID int64, from time.Time) ([]service.UpstreamMonitorPoint, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT balance::float8, COALESCE(unit,''), sampled_at
FROM upstream_balance_samples
WHERE account_id = $1 AND sampled_at >= $2
ORDER BY sampled_at ASC
LIMIT 5000`, accountID, from)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	points := make([]service.UpstreamMonitorPoint, 0, 256)
	for rows.Next() {
		var point service.UpstreamMonitorPoint
		if err := rows.Scan(&point.Balance, &point.Unit, &point.SampledAt); err != nil {
			return nil, err
		}
		point.SampledAt = point.SampledAt.UTC()
		points = append(points, point)
	}
	return points, rows.Err()
}

func (r *upstreamMonitorRepository) RateChanges(ctx context.Context, filter service.UpstreamRateFilter) ([]service.UpstreamRateChange, int64, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM upstream_rate_changes
WHERE changed_at >= $1 AND ($2 = 0 OR account_id = $2)`, filter.From, filter.AccountID).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []service.UpstreamRateChange{}, 0, nil
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT c.id, c.account_id, COALESCE(a.name,''), c.old_rate::float8, c.new_rate::float8, c.changed_at
FROM upstream_rate_changes c
LEFT JOIN accounts a ON a.id = c.account_id
WHERE c.changed_at >= $1 AND ($2 = 0 OR c.account_id = $2)
ORDER BY c.changed_at DESC, c.id DESC
LIMIT $3 OFFSET $4`, filter.From, filter.AccountID, filter.PageSize, (filter.Page-1)*filter.PageSize)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.UpstreamRateChange, 0, filter.PageSize)
	for rows.Next() {
		var item service.UpstreamRateChange
		if err := rows.Scan(&item.ID, &item.AccountID, &item.Name, &item.OldRate, &item.NewRate, &item.ChangedAt); err != nil {
			return nil, 0, err
		}
		item.ChangedAt = item.ChangedAt.UTC()
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (r *upstreamMonitorRepository) ListProbeSnapshots(ctx context.Context) ([]service.UpstreamProbeSnapshot, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT a.id,
  COALESCE(NULLIF(a.extra->'upstream_billing_probe'->'balance'->'data'->>'remaining',''),''),
  COALESCE(NULLIF(a.extra->'upstream_billing_probe'->'balance'->'data'->>'unit',''),''),
  COALESCE(NULLIF(a.extra->'upstream_billing_probe'->'data'->>'effective_rate_multiplier',''),'')
FROM accounts a
WHERE a.deleted_at IS NULL AND a.extra ? 'upstream_billing_probe'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.UpstreamProbeSnapshot, 0, 256)
	for rows.Next() {
		var item service.UpstreamProbeSnapshot
		if err := rows.Scan(&item.AccountID, &item.BalanceText, &item.Unit, &item.RateText); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *upstreamMonitorRepository) LoadSampleStates(ctx context.Context) (map[int64]service.UpstreamMonitorState, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT account_id, last_balance::float8, COALESCE(last_unit,''), last_rate::float8, last_sample_at
FROM upstream_monitor_state`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	states := make(map[int64]service.UpstreamMonitorState)
	for rows.Next() {
		var accountID int64
		var balance, rate sql.NullFloat64
		var unit string
		var lastSample sql.NullTime
		if err := rows.Scan(&accountID, &balance, &unit, &rate, &lastSample); err != nil {
			return nil, err
		}
		state := service.UpstreamMonitorState{LastUnit: unit}
		if balance.Valid {
			value := balance.Float64
			state.LastBalance = &value
		}
		if rate.Valid {
			value := rate.Float64
			state.LastRate = &value
		}
		if lastSample.Valid {
			value := lastSample.Time.UTC()
			state.LastSampleAt = &value
		}
		states[accountID] = state
	}
	return states, rows.Err()
}

func (r *upstreamMonitorRepository) ApplySamples(ctx context.Context, writes []service.UpstreamSampleWrite, pruneSamplesBefore, pruneRatesBefore time.Time) error {
	if len(writes) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, write := range writes {
		if write.SampledAt != nil {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO upstream_balance_samples(account_id, balance, unit, sampled_at)
VALUES ($1, $2, $3, $4)`, write.AccountID, write.Balance, write.Unit, *write.SampledAt); err != nil {
				return err
			}
		}
		var sampleArg any
		if write.SampledAt != nil {
			sampleArg = *write.SampledAt
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO upstream_monitor_state(account_id, last_balance, last_unit, last_rate, last_sample_at, updated_at)
VALUES ($1, $2, $3, $4, $5, NOW())
ON CONFLICT (account_id) DO UPDATE SET
  last_balance = CASE WHEN $6 THEN EXCLUDED.last_balance ELSE upstream_monitor_state.last_balance END,
  last_unit = CASE WHEN $6 THEN EXCLUDED.last_unit ELSE upstream_monitor_state.last_unit END,
  last_rate = CASE WHEN $7 THEN EXCLUDED.last_rate ELSE upstream_monitor_state.last_rate END,
  last_sample_at = CASE WHEN $5 IS NOT NULL THEN EXCLUDED.last_sample_at ELSE upstream_monitor_state.last_sample_at END,
  updated_at = NOW()`,
			write.AccountID,
			nullableUpstreamFloat(write.HasBalance, write.Balance),
			write.Unit,
			nullableUpstreamFloat(write.HasRate, write.Rate),
			sampleArg,
			write.HasBalance,
			write.HasRate,
		); err != nil {
			return err
		}
		if write.RateChanged && write.PreviousRate != nil {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO upstream_rate_changes(account_id, old_rate, new_rate, source, changed_at)
VALUES ($1, $2, $3, 'probe', $4)`, write.AccountID, *write.PreviousRate, write.Rate, time.Now().UTC()); err != nil {
				return err
			}
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM upstream_balance_samples WHERE sampled_at < $1`, pruneSamplesBefore); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM upstream_rate_changes WHERE changed_at < $1`, pruneRatesBefore); err != nil {
		return err
	}
	return tx.Commit()
}

func nullableUpstreamFloat(ok bool, value float64) any {
	if !ok {
		return nil
	}
	return value
}
