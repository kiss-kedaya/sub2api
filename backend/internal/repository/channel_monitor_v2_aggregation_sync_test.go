package repository

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func allChannelMonitorV2SyncTables() []channelMonitorV2SyncTable {
	return []channelMonitorV2SyncTable{
		channelMonitorV2Metrics1mTable,
		channelMonitorV2UserMetrics1mTable,
		channelMonitorV2ErrorMetrics1mTable,
		channelMonitorV2Histograms1mTable,
		channelMonitorV2MetricsRollupTable,
		channelMonitorV2UserMetricsRollupTable,
		channelMonitorV2ErrorMetricsRollupTable,
		channelMonitorV2HistogramsRollupTable,
	}
}

// channelMonitorV2DDLColumns returns the column names and primary key of a table
// as the migrations that created it declare them.
func channelMonitorV2DDLColumns(t *testing.T, table string) (columns, primaryKey []string) {
	t.Helper()
	for _, file := range []string{"194_channel_monitor_v2.sql", "199_channel_monitor_v2_fixed_rollups.sql"} {
		raw, err := migrations.FS.ReadFile(file)
		require.NoError(t, err)
		text := string(raw)
		header := "CREATE TABLE IF NOT EXISTS " + table + " ("
		at := strings.Index(text, header)
		if at < 0 {
			continue
		}
		body := text[at+len(header):]
		body = body[:strings.Index(body, "\n);")]
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case line == "" || strings.HasPrefix(line, "--"):
			case strings.HasPrefix(line, "PRIMARY KEY"):
				inner := line[strings.Index(line, "(")+1 : strings.LastIndex(line, ")")]
				for _, col := range strings.Split(inner, ",") {
					primaryKey = append(primaryKey, strings.TrimSpace(col))
				}
			default:
				columns = append(columns, strings.Fields(line)[0])
			}
		}
		return columns, primaryKey
	}
	t.Fatalf("table %s is not created by the channel monitor v2 migrations", table)
	return nil, nil
}

// The sync lists columns explicitly. A column the descriptor does not know would
// silently stop being refreshed, so the descriptors must match the DDL exactly.
func TestChannelMonitorV2SyncTablesMatchMigrationDDL(t *testing.T) {
	for _, table := range allChannelMonitorV2SyncTables() {
		columns, primaryKey := channelMonitorV2DDLColumns(t, table.name)
		want := append(append([]string{}, table.keys...), table.values...)
		if table.computedAt {
			want = append(want, "computed_at")
		}
		require.ElementsMatch(t, columns, want, table.name)
		require.Equal(t, primaryKey, table.keys, "%s: keys must be the primary key in index order (ON CONFLICT target)", table.name)
	}
}

var channelMonitorV2DistinctRowPattern = regexp.MustCompile(`IS DISTINCT FROM \(([^)]*)\)`)

// A rewrite of the whole window is exactly what this replaced. Every delete must
// be an anti-join against the recomputed rows, every update must be guarded by a
// value comparison, every insert must skip keys the table already has.
func TestChannelMonitorV2SyncStatementsWriteOnlyChangedRows(t *testing.T) {
	for _, table := range allChannelMonitorV2SyncTables() {
		t.Run(table.name, func(t *testing.T) {
			del := table.deleteCTE("t", "src", channelMonitorV2Window1m)
			require.Contains(t, del, "AND NOT EXISTS (SELECT 1 FROM src n WHERE")
			upd := table.updateCTE("t", "src", channelMonitorV2Window1m)
			require.Contains(t, upd, ") IS DISTINCT FROM (")
			ins := table.insertSQL("src", channelMonitorV2Window1m)
			require.Contains(t, ins, "WHERE NOT EXISTS (SELECT 1 FROM "+table.name+" x WHERE")
			require.Contains(t, ins, "ON CONFLICT ("+strings.Join(table.keys, ", ")+") DO UPDATE SET")
			for _, statement := range []string{upd, ins} {
				rows := channelMonitorV2DistinctRowPattern.FindAllStringSubmatch(statement, -1)
				require.NotEmpty(t, rows)
				for _, row := range rows {
					require.NotContains(t, row[1], "computed_at", "computed_at changes on every run and must not decide whether a row changed")
					require.Equal(t, len(table.values), len(strings.Split(row[1], ",")), "every data column is compared")
				}
			}
		})
	}

	// The assembled statements carry one delete/update/insert triple per table they sync.
	assembled := map[string]int{
		"facts":      3, // metrics_1m, user_metrics_1m, error_metrics_1m
		"histograms": 1,
	}
	statements := map[string]string{"facts": channelMonitorV2FactSyncSQL, "histograms": channelMonitorV2HistogramSyncSQL}
	for _, rollup := range channelMonitorV2RollupSyncs {
		assembled[rollup.table] = 1
		statements[rollup.table] = rollup.sql
	}
	for name, tables := range assembled {
		statement := statements[name]
		require.Equal(t, tables, strings.Count(statement, "DELETE FROM"), name)
		require.Equal(t, tables, strings.Count(statement, "UPDATE channel_monitor_v2_"), name)
		require.Equal(t, tables, strings.Count(statement, "INSERT INTO"), name)
		require.Equal(t, 2*tables, strings.Count(statement, "NOT EXISTS (SELECT 1 FROM"), name)
		require.NotContains(t, statement, "TRUNCATE", name)
	}
}

func TestChannelMonitorV2SyncTouchesComputedAtOnlyWhenARowChanges(t *testing.T) {
	for _, table := range allChannelMonitorV2SyncTables() {
		update := table.updateCTE("t", "src", channelMonitorV2Window1m)
		insert := table.insertSQL("src", channelMonitorV2Window1m)
		for _, statement := range []string{update, insert} {
			if table.computedAt {
				require.Contains(t, statement, "computed_at = NOW()", table.name)
			} else {
				require.NotContains(t, statement, "computed_at", table.name)
			}
		}
	}
}

// The usage side owns the success columns, the error side the error columns; a
// key seen by only one side keeps 0 for the other. That is what "insert the
// usage rows, then upsert the error columns onto them" produced.
func TestChannelMonitorV2MergeFullJoinTakesEachColumnFromItsOwnSide(t *testing.T) {
	merged := channelMonitorV2MergeFullJoin(channelMonitorV2UserMetrics1mTable, "usage_u", "err_u")
	for _, col := range []string{"success_requests", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens",
		"ttft_sum_ms", "ttft_count", "duration_sum_ms", "duration_count"} {
		require.Contains(t, merged, fmt.Sprintf("COALESCE(u.%s, 0) AS %s", col, col))
	}
	require.Contains(t, merged, "COALESCE(e.error_requests, 0) AS error_requests")
	for _, key := range channelMonitorV2UserMetrics1mTable.keys {
		require.Contains(t, merged, fmt.Sprintf("COALESCE(u.%s, e.%s) AS %s", key, key, key))
	}
	require.Contains(t, merged, "FULL JOIN err_u e ON")

	metrics := channelMonitorV2MergeFullJoin(channelMonitorV2Metrics1mTable, "usage_m", "err_m")
	for _, col := range []string{"error_requests", "upstream_affected_requests", "upstream_attempt_count"} {
		require.Contains(t, metrics, fmt.Sprintf("COALESCE(e.%s, 0) AS %s", col, col))
	}
}

// ---- which rollup tiers a window refreshes ----

func channelMonitorV2ContainsMatcher() sqlmock.QueryMatcher {
	compact := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	return sqlmock.QueryMatcherFunc(func(expected, actual string) error {
		if !strings.Contains(compact(actual), compact(expected)) {
			return fmt.Errorf("statement %q does not contain %q", compact(actual), compact(expected))
		}
		return nil
	})
}

func newChannelMonitorV2RollupTx(t *testing.T) (*sql.Tx, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(channelMonitorV2ContainsMatcher()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectBegin()
	tx, err := db.Begin()
	require.NoError(t, err)
	return tx, mock
}

var channelMonitorV2RollupTables = []string{
	"channel_monitor_v2_metrics_rollup",
	"channel_monitor_v2_user_metrics_rollup",
	"channel_monitor_v2_latency_histograms_rollup",
	"channel_monitor_v2_error_metrics_rollup",
}

func expectChannelMonitorV2RollupSync(mock sqlmock.Sqlmock, seconds int, start, end time.Time) {
	for _, table := range channelMonitorV2RollupTables {
		mock.ExpectExec("INSERT INTO "+table+" AS cur").
			WithArgs(fmt.Sprintf("%d seconds", seconds), seconds, start, end).
			WillReturnResult(sqlmock.NewResult(0, 0))
	}
}

func expectChannelMonitorV2CoarseDrop(mock sqlmock.Sqlmock, seconds int, start, end time.Time) {
	for _, table := range channelMonitorV2RollupTables {
		mock.ExpectExec("DELETE FROM "+table+" WHERE bucket_seconds = $1 AND bucket_start >= $2 AND bucket_start < $3").
			WithArgs(seconds, start, end).
			WillReturnResult(sqlmock.NewResult(0, 0))
	}
}

func TestChannelMonitorV2RecomputeFixedRollupsTiers(t *testing.T) {
	day := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	at := func(h, m int) time.Time { return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }
	tests := []struct {
		name       string
		start, end time.Time
		// expect describes the statements in order.
		expect func(mock sqlmock.Sqlmock, start, end time.Time)
	}{
		{
			// The normal live tick: 5m and 1h buckets are refreshed, 12h and 1d are not
			// touched at all.
			name: "inside one bucket of every size", start: at(10, 2), end: at(10, 4),
			expect: func(mock sqlmock.Sqlmock, start, end time.Time) {
				expectChannelMonitorV2RollupSync(mock, 300, start, end)
				expectChannelMonitorV2RollupSync(mock, 3600, start, end)
			},
		},
		{
			// A bucket that starts inside a skipped coarse tier's window: the delete + insert
			// version wiped it and did not rebuild it, so it must still be dropped.
			name: "window starts a 12h and a 1d bucket", start: at(0, 0), end: at(0, 2),
			expect: func(mock sqlmock.Sqlmock, start, end time.Time) {
				expectChannelMonitorV2RollupSync(mock, 300, start, end)
				expectChannelMonitorV2RollupSync(mock, 3600, start, end)
				expectChannelMonitorV2CoarseDrop(mock, 43200, start, end)
				expectChannelMonitorV2CoarseDrop(mock, 86400, start, end)
			},
		},
		{
			name: "window starts only a 12h bucket", start: at(12, 0), end: at(12, 2),
			expect: func(mock sqlmock.Sqlmock, start, end time.Time) {
				expectChannelMonitorV2RollupSync(mock, 300, start, end)
				expectChannelMonitorV2RollupSync(mock, 3600, start, end)
				expectChannelMonitorV2CoarseDrop(mock, 43200, start, end)
			},
		},
		{
			name: "window crosses noon", start: at(11, 59), end: at(12, 1),
			expect: func(mock sqlmock.Sqlmock, start, end time.Time) {
				expectChannelMonitorV2RollupSync(mock, 300, start, end)
				expectChannelMonitorV2RollupSync(mock, 3600, start, end)
				expectChannelMonitorV2RollupSync(mock, 43200, start, end)
			},
		},
		{
			name: "window crosses midnight", start: at(23, 59), end: at(24, 1),
			expect: func(mock sqlmock.Sqlmock, start, end time.Time) {
				expectChannelMonitorV2RollupSync(mock, 300, start, end)
				expectChannelMonitorV2RollupSync(mock, 3600, start, end)
				expectChannelMonitorV2RollupSync(mock, 43200, start, end)
				expectChannelMonitorV2RollupSync(mock, 86400, start, end)
			},
		},
		{
			name: "multi-hour catch-up window", start: at(8, 30), end: at(11, 0),
			expect: func(mock sqlmock.Sqlmock, start, end time.Time) {
				expectChannelMonitorV2RollupSync(mock, 300, start, end)
				expectChannelMonitorV2RollupSync(mock, 3600, start, end)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx, mock := newChannelMonitorV2RollupTx(t)
			test.expect(mock, test.start, test.end)
			repo := &channelMonitorV2Repository{}
			require.NoError(t, repo.recomputeFixedRollups(context.Background(), tx, test.start, test.end))
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// The whole live tick is: one transaction, the 1m facts, the histograms, the
// rollups, the watermark — and nothing that rewrites the window wholesale.
func TestChannelMonitorV2RecomputeLiveRangeStatementOrder(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(channelMonitorV2ContainsMatcher()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := &channelMonitorV2Repository{db: db}

	// 10:30 UTC three days ago: recent enough to survive the retention clamp, far from
	// any 12h/1d boundary, so only the 5m and 1h tiers are refreshed.
	end := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -3).Add(10*time.Hour + 30*time.Minute)
	start := end.Add(-2 * time.Minute)
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL statement_timeout = '180s'").WillReturnResult(sqlmock.NewResult(0, 0))
	// Hash joins only: with stale estimates the planner otherwise picks a nested loop or a
	// merge join on bucket_start alone, which is rows x rows within one bucket.
	mock.ExpectExec("SET LOCAL enable_nestloop = off").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SET LOCAL enable_mergejoin = off").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("usage_m AS MATERIALIZED").
		WithArgs(start, end, int64(channelMonitorV2ErrorLookbackLive/time.Second)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("new_h AS MATERIALIZED").WithArgs(start, end).WillReturnResult(sqlmock.NewResult(0, 0))
	expectChannelMonitorV2RollupSync(mock, 300, start, end)
	expectChannelMonitorV2RollupSync(mock, 3600, start, end)
	mock.ExpectExec("INSERT INTO channel_monitor_v2_watermarks").WithArgs(start, end).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.RecomputeLiveRange(context.Background(), start, end))
	require.NoError(t, mock.ExpectationsWereMet())
}
