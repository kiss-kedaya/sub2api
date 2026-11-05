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
  SELECT a.id, a.name, COALESCE(a.notes,'') AS notes, a.platform, a.type, a.status,
         a.extra->'upstream_billing_probe' AS probe,
         a.extra->>'upstream_billing_rate_sync_enabled' AS rate_sync_text
  FROM accounts a
  WHERE a.deleted_at IS NULL
    AND ($1 = '' OR a.platform = $1)
    AND ($2 = '' OR a.type = $2)
    AND ($3 = '' OR a.status = $3)
    AND ($4 = '' OR a.name ILIKE '%' || $4 || '%' OR COALESCE(a.notes,'') ILIKE '%' || $4 || '%')
), shaped AS (
  SELECT id, name, notes, platform, type, status, rate_sync_text,
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
  SELECT id, name, COALESCE(notes, '') AS notes, platform, type, status, rate_sync_text,
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
  v.id, v.name, v.notes, v.platform, v.type, v.status,
  v.balance_text, v.unit, v.unlimited_text, v.balance_status, v.balance_received_at, v.balance_fresh_until, v.balance_error,
  v.rate_text, v.rate_status, v.rate_received_at, v.rate_error, v.rate_synced_text, v.rate_sync_text,
  st.last_sample_at, ar.threshold::float8, COALESCE(ar.enabled, false)`

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
LEFT JOIN upstream_monitor_alert_rules ar ON ar.account_id = v.id
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
	var notes sql.NullString
	var balanceText, unit, unlimitedText, balanceStatus, balanceRead, balanceFresh, balanceErr sql.NullString
	var rateText, rateStatus, rateRead, rateErr, rateSynced, rateSync sql.NullString
	var lastSample sql.NullTime
	var alertThreshold sql.NullFloat64
	var alertEnabled bool
	err := row.Scan(
		&item.AccountID, &item.Name, &notes, &item.Platform, &item.Type, &item.Status,
		&balanceText, &unit, &unlimitedText, &balanceStatus, &balanceRead, &balanceFresh, &balanceErr,
		&rateText, &rateStatus, &rateRead, &rateErr, &rateSynced, &rateSync, &lastSample,
		&alertThreshold, &alertEnabled,
	)
	if err != nil {
		return item, err
	}
	if value, ok := service.ParseUpstreamMonitorNumber(balanceText.String); ok {
		item.Balance = &value
	}
	item.Notes = notes.String
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
	if alertThreshold.Valid {
		value := alertThreshold.Float64
		item.AlertThreshold = &value
	}
	item.AlertEnabled = alertEnabled
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
LEFT JOIN upstream_monitor_alert_rules ar ON ar.account_id = v.id
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
WHERE changed_at >= $1 AND ($2 = 0 OR account_id = $2)
  AND ($3 = 0 OR (old_rate > 0 AND ABS((new_rate - old_rate) / old_rate) * 100 >= $3))`,
		filter.From, filter.AccountID, filter.MinPct).Scan(&total); err != nil {
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
  AND ($3 = 0 OR (c.old_rate > 0 AND ABS((c.new_rate - c.old_rate) / c.old_rate) * 100 >= $3))
ORDER BY c.changed_at DESC, c.id DESC
LIMIT $4 OFFSET $5`,
		filter.From, filter.AccountID, filter.MinPct, filter.PageSize, (filter.Page-1)*filter.PageSize)
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
		item.ChangePct = service.UpstreamRateChangePct(item.OldRate, item.NewRate)
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

func (r *upstreamMonitorRepository) ApplySamples(ctx context.Context, writes []service.UpstreamSampleWrite, pruneSamplesBefore, pruneRatesBefore, pruneDailyBefore time.Time) error {
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
INSERT INTO upstream_balance_samples(account_id, balance, unit, spend, sampled_at)
VALUES ($1, $2, $3, $4, $5)`, write.AccountID, write.Balance, write.Unit, write.Spend, *write.SampledAt); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
INSERT INTO upstream_monitor_daily(day, account_id, unit, spend, balance_close, updated_at)
VALUES ($1::date, $2, $3, $4, $5, NOW())
ON CONFLICT (day, account_id) DO UPDATE SET
  spend = upstream_monitor_daily.spend + EXCLUDED.spend,
  unit = EXCLUDED.unit,
  balance_close = EXCLUDED.balance_close,
  updated_at = NOW()`,
				service.UpstreamBeijingDay(*write.SampledAt),
				write.AccountID,
				write.Unit,
				write.Spend,
				write.Balance,
			); err != nil {
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM upstream_monitor_daily WHERE day < $1::date`, pruneDailyBefore); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM upstream_monitor_alerts WHERE created_at < $1`, pruneRatesBefore); err != nil {
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

func (r *upstreamMonitorRepository) SpendTotals(ctx context.Context, today time.Time) ([]service.UpstreamSpendTotal, error) {
	day := service.UpstreamBeijingDay(today)
	rows, err := r.db.QueryContext(ctx, `
SELECT unit,
  COALESCE(SUM(spend) FILTER (WHERE day = $1::date), 0)::float8,
  COALESCE(SUM(spend) FILTER (WHERE day >= $1::date - 6), 0)::float8,
  COALESCE(SUM(spend) FILTER (WHERE day >= $1::date - 29), 0)::float8
FROM upstream_monitor_daily
WHERE day >= $1::date - 29
GROUP BY unit
ORDER BY unit ASC`, day)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.UpstreamSpendTotal, 0, 4)
	for rows.Next() {
		var item service.UpstreamSpendTotal
		if err := rows.Scan(&item.Unit, &item.Today, &item.Last7Days, &item.Last30Days); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *upstreamMonitorRepository) OpenAlertCount(ctx context.Context, since time.Time) (int64, error) {
	var total int64
	err := r.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM upstream_monitor_alerts
WHERE acked_at IS NULL AND created_at >= $1`, since).Scan(&total)
	return total, err
}

func (r *upstreamMonitorRepository) Trend(ctx context.Context, from time.Time) ([]service.UpstreamTrendPoint, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT to_char(day, 'YYYY-MM-DD'), unit,
  COALESCE(SUM(spend), 0)::float8,
  SUM(balance_close)::float8,
  COUNT(*) FILTER (WHERE balance_close IS NOT NULL)
FROM upstream_monitor_daily
WHERE day >= $1::date
GROUP BY day, unit
ORDER BY day ASC, unit ASC`, service.UpstreamBeijingDay(from))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.UpstreamTrendPoint, 0, 64)
	for rows.Next() {
		var item service.UpstreamTrendPoint
		var balance sql.NullFloat64
		if err := rows.Scan(&item.Day, &item.Unit, &item.Spend, &balance, &item.Accounts); err != nil {
			return nil, err
		}
		if balance.Valid && item.Accounts > 0 {
			value := balance.Float64
			item.BalanceClose = &value
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *upstreamMonitorRepository) LoadAlertRules(ctx context.Context) (map[int64]service.UpstreamAlertRule, error) {
	rows, err := r.db.QueryContext(ctx, `
SELECT account_id, threshold::float8, COALESCE(unit,''), rate_min_pct::float8, enabled, updated_at
FROM upstream_monitor_alert_rules`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	rules := make(map[int64]service.UpstreamAlertRule)
	for rows.Next() {
		var rule service.UpstreamAlertRule
		if err := rows.Scan(&rule.AccountID, &rule.Threshold, &rule.Unit, &rule.RateMinPct, &rule.Enabled, &rule.UpdatedAt); err != nil {
			return nil, err
		}
		rules[rule.AccountID] = rule
	}
	return rules, rows.Err()
}

func (r *upstreamMonitorRepository) UpsertAlertRule(ctx context.Context, rule service.UpstreamAlertRule) (service.UpstreamAlertRule, error) {
	err := r.db.QueryRowContext(ctx, `
INSERT INTO upstream_monitor_alert_rules(account_id, threshold, unit, rate_min_pct, enabled, updated_at)
VALUES ($1, $2, $3, $4, $5, NOW())
ON CONFLICT (account_id) DO UPDATE SET
  threshold = EXCLUDED.threshold,
  unit = EXCLUDED.unit,
  rate_min_pct = EXCLUDED.rate_min_pct,
  enabled = EXCLUDED.enabled,
  updated_at = NOW()
RETURNING account_id, threshold::float8, COALESCE(unit,''), rate_min_pct::float8, enabled, updated_at`,
		rule.AccountID, rule.Threshold, rule.Unit, rule.RateMinPct, rule.Enabled,
	).Scan(&rule.AccountID, &rule.Threshold, &rule.Unit, &rule.RateMinPct, &rule.Enabled, &rule.UpdatedAt)
	return rule, err
}

func (r *upstreamMonitorRepository) DeleteAlertRule(ctx context.Context, accountID int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM upstream_monitor_alert_rules WHERE account_id = $1`, accountID)
	return err
}

func (r *upstreamMonitorRepository) RecordAlerts(ctx context.Context, alerts []service.UpstreamAlert, cooldownSince time.Time) (int, error) {
	if len(alerts) == 0 {
		return 0, nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	recorded := 0
	for _, alert := range alerts {
		result, err := tx.ExecContext(ctx, `
INSERT INTO upstream_monitor_alerts(account_id, kind, level, balance, threshold, unit, old_rate, new_rate, message, created_at)
SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
WHERE NOT EXISTS (
  SELECT 1 FROM upstream_monitor_alerts
  WHERE account_id = $1 AND kind = $2 AND created_at >= $11
)`,
			alert.AccountID, alert.Kind, alert.Level,
			nullableUpstreamFloat(alert.Balance != nil, floatValue(alert.Balance)),
			nullableUpstreamFloat(alert.Threshold != nil, floatValue(alert.Threshold)),
			alert.Unit,
			nullableUpstreamFloat(alert.OldRate != nil, floatValue(alert.OldRate)),
			nullableUpstreamFloat(alert.NewRate != nil, floatValue(alert.NewRate)),
			alert.Message, alert.CreatedAt, cooldownSince,
		)
		if err != nil {
			return recorded, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return recorded, err
		}
		recorded += int(affected)
	}
	return recorded, tx.Commit()
}

func (r *upstreamMonitorRepository) Alerts(ctx context.Context, filter service.UpstreamAlertFilter) ([]service.UpstreamAlert, int64, error) {
	args := []any{filter.From, filter.AccountID, filter.Kind, filter.Acked}
	const where = `
WHERE al.created_at >= $1
  AND ($2 = 0 OR al.account_id = $2)
  AND ($3 = '' OR al.kind = $3)
  AND ($4 = '' OR ($4 = 'open' AND al.acked_at IS NULL) OR ($4 = 'acked' AND al.acked_at IS NOT NULL))`
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM upstream_monitor_alerts al`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return []service.UpstreamAlert{}, 0, nil
	}
	pageArgs := append(append([]any{}, args...), filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := r.db.QueryContext(ctx, `
SELECT al.id, al.account_id, COALESCE(a.name,''), al.kind, al.level,
  al.balance::float8, al.threshold::float8, COALESCE(al.unit,''),
  al.old_rate::float8, al.new_rate::float8, al.message, al.created_at, al.acked_at
FROM upstream_monitor_alerts al
LEFT JOIN accounts a ON a.id = al.account_id`+where+`
ORDER BY al.created_at DESC, al.id DESC
LIMIT $5 OFFSET $6`, pageArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.UpstreamAlert, 0, filter.PageSize)
	for rows.Next() {
		var item service.UpstreamAlert
		var balance, threshold, oldRate, newRate sql.NullFloat64
		var acked sql.NullTime
		if err := rows.Scan(&item.ID, &item.AccountID, &item.Name, &item.Kind, &item.Level,
			&balance, &threshold, &item.Unit, &oldRate, &newRate, &item.Message, &item.CreatedAt, &acked); err != nil {
			return nil, 0, err
		}
		item.Balance = nullableUpstreamFloatPtr(balance)
		item.Threshold = nullableUpstreamFloatPtr(threshold)
		item.OldRate = nullableUpstreamFloatPtr(oldRate)
		item.NewRate = nullableUpstreamFloatPtr(newRate)
		item.CreatedAt = item.CreatedAt.UTC()
		if acked.Valid {
			value := acked.Time.UTC()
			item.AckedAt = &value
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

func (r *upstreamMonitorRepository) AckAlert(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `
UPDATE upstream_monitor_alerts SET acked_at = NOW() WHERE id = $1 AND acked_at IS NULL`, id)
	return err
}

func (r *upstreamMonitorRepository) ListAccountsForExport(ctx context.Context, filter service.UpstreamMonitorFilter, limit int) ([]service.UpstreamMonitorAccount, error) {
	args := []any{filter.Platform, filter.AccountType, filter.AccountStatus, filter.Search, filter.BalanceStatus, limit}
	rows, err := r.db.QueryContext(ctx, upstreamMonitorListCTE+`
SELECT`+upstreamMonitorSelectColumns+`
FROM valued v
LEFT JOIN upstream_monitor_state st ON st.account_id = v.id
LEFT JOIN upstream_monitor_alert_rules ar ON ar.account_id = v.id
WHERE TRUE`+upstreamMonitorStatusFilter+`
ORDER BY `+upstreamMonitorOrder(filter.Sort)+`
LIMIT $6`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]service.UpstreamMonitorAccount, 0, 64)
	for rows.Next() {
		item, err := scanUpstreamMonitorAccount(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func floatValue(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func nullableUpstreamFloatPtr(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	result := value.Float64
	return &result
}
