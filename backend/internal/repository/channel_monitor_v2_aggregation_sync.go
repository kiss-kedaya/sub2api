package repository

import (
	"fmt"
	"strings"
)

// The aggregator used to rewrite every window it recomputed: DELETE the rows,
// INSERT the same rows again. Each tick recomputes the last two minutes and the
// 5m / 1h buckets they fall in, so almost every row that was deleted came back
// byte for byte identical — thousands of dead tuples and index entries per
// second for nothing.
//
// The statements built here turn the rows of a table inside a scope into exactly
// the rows of a recomputed result set (src) while touching only the difference:
//
//	rows present in the table but not in src  -> DELETE
//	rows present in both, values differ       -> UPDATE
//	rows present in src but not in the table  -> INSERT
//
// Unchanged rows are never read for update, locked or logged. The final rows are
// identical to what delete + insert produced; only computed_at differs, and it now
// means "when this row last changed".
//
// The comparison is a join filter on a plain UPDATE ... FROM, deliberately not
// INSERT ... ON CONFLICT DO UPDATE ... WHERE <changed>: the upsert takes the row
// lock of every conflicting row before it evaluates the WHERE, and the lock is
// WAL-logged and dirties the page even when the WHERE is false (200k unchanged
// rows: ~22 MB of WAL, against none for the join-filtered UPDATE).
//
// Everything a sync needs lives in one statement so the three sub-statements see
// the same snapshot, and the caller keeps it inside the transaction that also
// moves the watermark.
//
// The joins between the rows of a window and their recomputed values are meant to
// be hash joins on the whole key. Left to the planner they are not (the newest
// minutes always look like 1-2 rows to it): recomputeRange turns nested loops and
// merge joins off for the transaction, see the comment there.

// channelMonitorV2SyncTable describes one aggregate table.
type channelMonitorV2SyncTable struct {
	name   string
	keys   []string // primary key columns
	values []string // data columns; compared to decide whether a row changed
	// computedAt marks tables with a computed_at column. It is refreshed only when a
	// row is written and never takes part in the change test (it always differs).
	computedAt bool
}

// channelMonitorV2SyncScope limits which rows of the target a sync may touch. The
// argument is the alias of the target table.
type channelMonitorV2SyncScope func(alias string) string

func channelMonitorV2Cols(alias string, cols []string) string {
	parts := make([]string, len(cols))
	for i, col := range cols {
		parts[i] = alias + "." + col
	}
	return strings.Join(parts, ", ")
}

func channelMonitorV2Match(left, right string, cols []string) string {
	parts := make([]string, len(cols))
	for i, col := range cols {
		parts[i] = left + "." + col + " = " + right + "." + col
	}
	return strings.Join(parts, " AND ")
}

func (t channelMonitorV2SyncTable) deleteCTE(tag, src string, scope channelMonitorV2SyncScope) string {
	return fmt.Sprintf(`del_%s AS (
  DELETE FROM %s d
  WHERE %s
    AND NOT EXISTS (SELECT 1 FROM %s n WHERE %s)
)`, tag, t.name, scope("d"), src, channelMonitorV2Match("n", "d", t.keys))
}

func (t channelMonitorV2SyncTable) updateCTE(tag, src string, scope channelMonitorV2SyncScope) string {
	sets := make([]string, 0, len(t.values)+1)
	for _, col := range t.values {
		sets = append(sets, col+" = n."+col)
	}
	if t.computedAt {
		sets = append(sets, "computed_at = NOW()")
	}
	return fmt.Sprintf(`upd_%s AS (
  UPDATE %s u SET %s
  FROM %s n
  WHERE %s
    AND %s
    AND (%s) IS DISTINCT FROM (%s)
)`, tag, t.name, strings.Join(sets, ", "), src, scope("u"), channelMonitorV2Match("n", "u", t.keys),
		channelMonitorV2Cols("u", t.values), channelMonitorV2Cols("n", t.values))
}

// insertSQL inserts the rows of src the table does not have yet. ON CONFLICT only
// matters if another writer slipped the same key in after our snapshot: then we
// converge on our values instead of failing the whole tick.
func (t channelMonitorV2SyncTable) insertSQL(src string, scope channelMonitorV2SyncScope) string {
	cols := make([]string, 0, len(t.keys)+len(t.values)+1)
	cols = append(cols, t.keys...)
	cols = append(cols, t.values...)
	insertCols := strings.Join(cols, ", ")
	selected := channelMonitorV2Cols("n", cols)
	sets := make([]string, 0, len(t.values)+1)
	for _, col := range t.values {
		sets = append(sets, col+" = EXCLUDED."+col)
	}
	if t.computedAt {
		insertCols += ", computed_at"
		selected += ", NOW()"
		sets = append(sets, "computed_at = NOW()")
	}
	return fmt.Sprintf(`INSERT INTO %s AS cur (%s)
SELECT %s FROM %s n
WHERE NOT EXISTS (SELECT 1 FROM %s x WHERE %s AND %s)
ON CONFLICT (%s) DO UPDATE SET %s
WHERE (%s) IS DISTINCT FROM (%s)`, t.name, insertCols, selected, src, t.name, scope("x"), channelMonitorV2Match("n", "x", t.keys),
		strings.Join(t.keys, ", "), strings.Join(sets, ", "),
		channelMonitorV2Cols("cur", t.values), channelMonitorV2Cols("EXCLUDED", t.values))
}

// syncStatement wraps the data-modifying CTEs of one table around its source CTEs.
// Data-modifying CTEs run to completion even when nothing reads them, so the
// statement is: all source CTEs, delete, update, then the insert as the main query.
func (t channelMonitorV2SyncTable) syncStatement(sources []string, src string, scope channelMonitorV2SyncScope) string {
	ctes := make([]string, 0, len(sources)+2)
	ctes = append(ctes, sources...)
	ctes = append(ctes, t.deleteCTE("t", src, scope), t.updateCTE("t", src, scope))
	return "WITH " + strings.Join(ctes, ",\n") + "\n" + t.insertSQL(src, scope)
}

var (
	channelMonitorV2MetricValueCols = []string{
		"success_requests", "error_requests", "upstream_affected_requests", "upstream_attempt_count",
		"input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens",
		"ttft_sum_ms", "ttft_count", "duration_sum_ms", "duration_count",
	}
	channelMonitorV2UserMetricValueCols = []string{
		"success_requests", "error_requests",
		"input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens",
		"ttft_sum_ms", "ttft_count", "duration_sum_ms", "duration_count",
	}
	// Columns the usage-log aggregation fills; the error aggregation fills the rest.
	channelMonitorV2UsageOnlyValueCols = map[string]bool{
		"success_requests": true, "input_tokens": true, "output_tokens": true,
		"cache_creation_tokens": true, "cache_read_tokens": true,
		"ttft_sum_ms": true, "ttft_count": true, "duration_sum_ms": true, "duration_count": true,
	}
)

var (
	channelMonitorV2Metrics1mTable = channelMonitorV2SyncTable{
		name:       "channel_monitor_v2_metrics_1m",
		keys:       []string{"bucket_start", "platform", "group_id", "model"},
		values:     channelMonitorV2MetricValueCols,
		computedAt: true,
	}
	channelMonitorV2UserMetrics1mTable = channelMonitorV2SyncTable{
		name:       "channel_monitor_v2_user_metrics_1m",
		keys:       []string{"bucket_start", "platform", "group_id", "model", "user_id"},
		values:     channelMonitorV2UserMetricValueCols,
		computedAt: true,
	}
	channelMonitorV2ErrorMetrics1mTable = channelMonitorV2SyncTable{
		name:   "channel_monitor_v2_error_metrics_1m",
		keys:   []string{"bucket_start", "platform", "group_id", "model", "error_category", "taxonomy_version"},
		values: []string{"error_requests"},
	}
	channelMonitorV2Histograms1mTable = channelMonitorV2SyncTable{
		name:   "channel_monitor_v2_latency_histograms_1m",
		keys:   []string{"bucket_start", "platform", "group_id", "model", "user_id", "metric", "upper_bound_ms"},
		values: []string{"sample_count"},
	}

	channelMonitorV2MetricsRollupTable = channelMonitorV2SyncTable{
		name:       "channel_monitor_v2_metrics_rollup",
		keys:       []string{"bucket_seconds", "bucket_start", "platform", "group_id", "model"},
		values:     channelMonitorV2MetricValueCols,
		computedAt: true,
	}
	channelMonitorV2UserMetricsRollupTable = channelMonitorV2SyncTable{
		name:       "channel_monitor_v2_user_metrics_rollup",
		keys:       []string{"bucket_seconds", "bucket_start", "platform", "group_id", "model", "user_id"},
		values:     channelMonitorV2UserMetricValueCols,
		computedAt: true,
	}
	channelMonitorV2ErrorMetricsRollupTable = channelMonitorV2SyncTable{
		name:   "channel_monitor_v2_error_metrics_rollup",
		keys:   []string{"bucket_seconds", "bucket_start", "platform", "group_id", "model", "error_category", "taxonomy_version"},
		values: []string{"error_requests"},
	}
	channelMonitorV2HistogramsRollupTable = channelMonitorV2SyncTable{
		name:   "channel_monitor_v2_latency_histograms_rollup",
		keys:   []string{"bucket_seconds", "bucket_start", "platform", "group_id", "model", "user_id", "metric", "upper_bound_ms"},
		values: []string{"sample_count"},
	}
)

// channelMonitorV2MergeFullJoin combines the usage-side and error-side aggregates
// of one key into the row the 1m table holds: every key either side saw, success
// columns from the usage side and error columns from the error side, 0 where a
// side has nothing. This is what "insert the usage rows, then upsert the error
// columns onto them" produced.
func channelMonitorV2MergeFullJoin(t channelMonitorV2SyncTable, usageCTE, errorCTE string) string {
	selected := make([]string, 0, len(t.keys)+len(t.values))
	for _, col := range t.keys {
		selected = append(selected, fmt.Sprintf("COALESCE(u.%s, e.%s) AS %s", col, col, col))
	}
	for _, col := range t.values {
		side := "e"
		if channelMonitorV2UsageOnlyValueCols[col] {
			side = "u"
		}
		selected = append(selected, fmt.Sprintf("COALESCE(%s.%s, 0) AS %s", side, col, col))
	}
	return fmt.Sprintf("SELECT %s\nFROM %s u\nFULL JOIN %s e ON %s",
		strings.Join(selected, ", "), usageCTE, errorCTE, channelMonitorV2Match("u", "e", t.keys))
}

func channelMonitorV2Window1m(alias string) string {
	return alias + ".bucket_start >= $1 AND " + alias + ".bucket_start < $2"
}

// channelMonitorV2FactSyncSQL brings the metrics, user-metrics and error-category
// 1m tables of [$1,$2) in line with the usage logs and error logs. $3 is the
// error dedup lookback in seconds.
var channelMonitorV2FactSyncSQL = buildChannelMonitorV2FactSyncSQL()

func buildChannelMonitorV2FactSyncSQL() string {
	metrics := channelMonitorV2Metrics1mTable
	users := channelMonitorV2UserMetrics1mTable
	categories := channelMonitorV2ErrorMetrics1mTable
	scope := channelMonitorV2SyncScope(channelMonitorV2Window1m)

	ctes := []string{
		"usage_m AS MATERIALIZED (" + fmt.Sprintf(channelMonitorV2UsageMetricsSQL, channelMonitorV2PlatformSQL, channelMonitorV2ModelSQL) + ")",
		"usage_u AS MATERIALIZED (" + fmt.Sprintf(channelMonitorV2UserMetricsSQL, channelMonitorV2PlatformSQL, channelMonitorV2ModelSQL) + ")",
		channelMonitorV2ErrorClassifiedCTEs,
		`err_m AS MATERIALIZED (
  SELECT bucket_start, platform, group_id, model, COUNT(*) AS error_requests,
         COUNT(*) FILTER (WHERE upstream_affected) AS upstream_affected_requests,
         SUM(upstream_attempts) AS upstream_attempt_count
  FROM classified GROUP BY 1, 2, 3, 4
)`,
		`err_u AS MATERIALIZED (
  SELECT bucket_start, platform, group_id, model, user_id, COUNT(*) AS error_requests
  FROM classified WHERE user_id IS NOT NULL GROUP BY 1, 2, 3, 4, 5
)`,
		`err_c AS MATERIALIZED (
  SELECT bucket_start, platform, group_id, model, category AS error_category,
         1::smallint AS taxonomy_version, COUNT(*) AS error_requests
  FROM classified GROUP BY 1, 2, 3, 4, 5
)`,
		"new_m AS MATERIALIZED (" + channelMonitorV2MergeFullJoin(metrics, "usage_m", "err_m") + ")",
		"new_u AS MATERIALIZED (" + channelMonitorV2MergeFullJoin(users, "usage_u", "err_u") + ")",
		metrics.deleteCTE("m", "new_m", scope), metrics.updateCTE("m", "new_m", scope),
		"ins_m AS (" + metrics.insertSQL("new_m", scope) + ")",
		users.deleteCTE("u", "new_u", scope), users.updateCTE("u", "new_u", scope),
		"ins_u AS (" + users.insertSQL("new_u", scope) + ")",
		categories.deleteCTE("c", "err_c", scope), categories.updateCTE("c", "err_c", scope),
	}
	return "WITH " + strings.Join(ctes, ",\n") + "\n" + categories.insertSQL("err_c", scope)
}

// channelMonitorV2HistogramSyncSQL brings the 1m latency histograms of [$1,$2) in
// line with the usage logs.
var channelMonitorV2HistogramSyncSQL = buildChannelMonitorV2HistogramSyncSQL()

func buildChannelMonitorV2HistogramSyncSQL() string {
	histogram := fmt.Sprintf(channelMonitorV2HistogramSQL,
		channelMonitorV2PlatformSQL, channelMonitorV2ModelSQL, channelMonitorV2HistogramBoundSQL("latency.value_ms"))
	return channelMonitorV2Histograms1mTable.syncStatement(
		[]string{"new_h AS MATERIALIZED (" + histogram + ")"}, "new_h", channelMonitorV2Window1m)
}

// channelMonitorV2RollupSync is the sync of one rollup table for one bucket size:
// $1 interval, $2 bucket_seconds, $3 window start, $4 window end. The buckets
// covering the window are rebuilt from the 1m facts and the rollup rows of those
// buckets are brought in line with them.
type channelMonitorV2RollupSync struct {
	table string
	sql   string
}

func channelMonitorV2RollupScope(alias string) string {
	return alias + ".bucket_seconds = $2::integer AND " +
		alias + ".bucket_start >= (SELECT start_at FROM bounds) AND " +
		alias + ".bucket_start < (SELECT end_at FROM bounds)"
}

func buildChannelMonitorV2RollupSyncSQL(t channelMonitorV2SyncTable, aggregate string) string {
	return t.syncStatement([]string{
		channelMonitorV2FixedRollupBoundsCTE,
		"agg AS MATERIALIZED (" + aggregate + ")",
	}, "agg", channelMonitorV2RollupScope)
}

// channelMonitorV2RollupSyncs is ordered like the statements the delete + insert
// version ran: metrics, user metrics, histograms, errors.
var channelMonitorV2RollupSyncs = []channelMonitorV2RollupSync{
	{table: channelMonitorV2MetricsRollupTable.name, sql: buildChannelMonitorV2RollupSyncSQL(channelMonitorV2MetricsRollupTable, channelMonitorV2MetricsRollupSQL)},
	{table: channelMonitorV2UserMetricsRollupTable.name, sql: buildChannelMonitorV2RollupSyncSQL(channelMonitorV2UserMetricsRollupTable, channelMonitorV2UserMetricsRollupSQL)},
	{table: channelMonitorV2HistogramsRollupTable.name, sql: buildChannelMonitorV2RollupSyncSQL(channelMonitorV2HistogramsRollupTable, channelMonitorV2HistogramRollupSQL)},
	{table: channelMonitorV2ErrorMetricsRollupTable.name, sql: buildChannelMonitorV2RollupSyncSQL(channelMonitorV2ErrorMetricsRollupTable, channelMonitorV2ErrorRollupSQL)},
}
