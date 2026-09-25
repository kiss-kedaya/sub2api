package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestTicketRequesterStatsIndependentBudgetsAndAvailability(t *testing.T) {
	for _, mode := range []string{"zero", "recharge_timeout", "both_timeout", "fallback_timeout", "reset_stale"} {
		t.Run(mode, func(t *testing.T) {
			repo, mock := ticketSQLMock(t)
			usage := mock.ExpectQuery(regexp.QuoteMeta(ticketRequesterHourlySQL))
			switch mode {
			case "both_timeout":
				usage.WillDelayFor(2 * time.Second).WillReturnRows(sqlmock.NewRows([]string{"tokens", "cost"}).AddRow(1, 1))
			case "fallback_timeout":
				usage.WillDelayFor(500 * time.Millisecond).WillReturnError(&pq.Error{Code: "42P01"})
				mock.ExpectQuery(regexp.QuoteMeta(ticketRequesterUsageSQL)).WillDelayFor(time.Second).
					WillReturnRows(sqlmock.NewRows([]string{"tokens", "cost"}).AddRow(1, 1))
			case "reset_stale":
				usage.WillReturnError(errors.New("usage unavailable"))
			default:
				usage.WillReturnRows(sqlmock.NewRows([]string{"tokens", "cost"}).AddRow(0, 0))
			}
			recharge := mock.ExpectQuery(regexp.QuoteMeta(ticketRequesterRechargeSQL))
			switch mode {
			case "both_timeout", "recharge_timeout":
				recharge.WillDelayFor(2 * time.Second).WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(1))
			case "reset_stale":
				recharge.WillReturnError(errors.New("recharge unavailable"))
			default:
				recharge.WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(0))
			}
			requester := &service.TicketRequester{ID: 7, TodayTokens: 100, TodayCost: 5, Recharged14d: 8,
				UsageStatsAvailable: true, RechargeStatsAvailable: true}
			started := time.Now()
			repo.fillRequesterStats(context.Background(), requester)
			require.Less(t, time.Since(started), 1900*time.Millisecond)
			require.Equal(t, mode == "zero" || mode == "recharge_timeout", requester.UsageStatsAvailable)
			require.Equal(t, mode == "zero" || mode == "fallback_timeout", requester.RechargeStatsAvailable)
			require.Zero(t, requester.TodayTokens)
			require.Zero(t, requester.TodayCost)
			require.Zero(t, requester.Recharged14d)
			encoded, err := json.Marshal(requester)
			require.NoError(t, err)
			require.Contains(t, string(encoded), fmt.Sprintf("\"usage_stats_available\":%t", requester.UsageStatsAvailable))
			require.Contains(t, string(encoded), fmt.Sprintf("\"recharge_stats_available\":%t", requester.RechargeStatsAvailable))
		})
	}
}

func ticketStatsLocalPG(t *testing.T) *sql.DB {
	t.Helper()
	credentialsPath := os.Getenv("TICKET_STATS_LOCAL_PG_CREDENTIALS")
	if credentialsPath == "" {
		t.Skip("isolated PostgreSQL acceptance is opt-in")
	}
	encoded, err := os.ReadFile(credentialsPath)
	require.NoError(t, err)
	var credentials struct {
		Host, User, Password string
		Port                 int
	}
	require.NoError(t, json.Unmarshal(encoded, &credentials))
	require.Equal(t, "127.0.0.1", credentials.Host)
	require.Equal(t, 55432, credentials.Port)
	require.Equal(t, "tickets_test", credentials.User)
	connection := &url.URL{Scheme: "postgres", Host: credentials.Host + ":" + strconv.Itoa(credentials.Port),
		Path: "/sub2_tickets_test", User: url.UserPassword(credentials.User, credentials.Password)}
	options := url.Values{"sslmode": {"disable"}, "connect_timeout": {"5"}, "search_path": {"pg_temp"},
		"statement_timeout": {"20000"}, "application_name": {"ticket_stats_acceptance"}}
	connection.RawQuery = options.Encode()
	db, err := sql.Open("postgres", connection.String())
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	var database, username, address, searchPath string
	var port int
	require.NoError(t, db.QueryRow("SELECT current_database(), current_user, host(inet_server_addr()), inet_server_port(), current_setting('search_path')").
		Scan(&database, &username, &address, &port, &searchPath))
	require.Equal(t, "sub2_tickets_test", database)
	require.Equal(t, "tickets_test", username)
	require.Equal(t, "127.0.0.1", address)
	require.Equal(t, 55432, port)
	require.Equal(t, "pg_temp", searchPath)
	t.Logf("isolated endpoint=%s:%d database=%s role=%s search_path=%s", address, port, database, username, searchPath)
	return db
}

func ticketStatsPGExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	_, err := db.Exec(query, args...)
	require.NoError(t, err)
}

func ticketStatsPGTables(t *testing.T, db *sql.DB) {
	t.Helper()
	ticketStatsPGExec(t, db, "DROP TABLE IF EXISTS pg_temp.usage_logs, pg_temp.usage_dashboard_hourly_users, pg_temp.payment_orders")
	ticketStatsPGExec(t, db, "CREATE TEMP TABLE usage_logs (user_id bigint NOT NULL, created_at timestamptz NOT NULL, input_tokens integer NOT NULL DEFAULT 0, output_tokens integer NOT NULL DEFAULT 0, cache_creation_tokens integer NOT NULL DEFAULT 0, cache_read_tokens integer NOT NULL DEFAULT 0, actual_cost numeric(20,10) NOT NULL DEFAULT 0)")
	ticketStatsPGExec(t, db, "CREATE INDEX idx_usage_logs_user_created ON pg_temp.usage_logs(user_id, created_at)")
	ticketStatsPGExec(t, db, "CREATE TEMP TABLE usage_dashboard_hourly_users (bucket_start timestamptz NOT NULL, user_id bigint NOT NULL, input_tokens bigint NOT NULL DEFAULT 0, output_tokens bigint NOT NULL DEFAULT 0, cache_creation_tokens bigint NOT NULL DEFAULT 0, cache_read_tokens bigint NOT NULL DEFAULT 0, actual_cost numeric(20,10) NOT NULL DEFAULT 0, total_requests bigint NOT NULL DEFAULT 0, computed_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(bucket_start, user_id))")
	ticketStatsPGExec(t, db, "CREATE INDEX idx_usage_dashboard_hourly_users_bucket_start ON pg_temp.usage_dashboard_hourly_users(bucket_start)")
	ticketStatsPGExec(t, db, "CREATE TEMP TABLE payment_orders (user_id bigint NOT NULL, order_type varchar(20) NOT NULL, status varchar(20) NOT NULL, amount decimal(20,8) NOT NULL, pay_amount decimal(20,8) NOT NULL, paid_at timestamptz)")
	for _, column := range []string{"user_id", "status", "paid_at", "order_type"} {
		ticketStatsPGExec(t, db, "CREATE INDEX idx_payment_orders_"+column+" ON pg_temp.payment_orders("+column+")")
	}
}

func ticketStatsPGFixture(t *testing.T, db *sql.DB, today, now time.Time) {
	t.Helper()
	ticketStatsPGExec(t, db, "INSERT INTO pg_temp.usage_logs(user_id, created_at, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, actual_cost) SELECT 7, $1::timestamptz + hour * interval '1 hour', (hour+1)*10, (hour+1)*20, (hour+1)*30, (hour+1)*40, hour+1 FROM generate_series(0,3) AS hour", today)
	ticketStatsPGExec(t, db, "INSERT INTO pg_temp.usage_logs(user_id, created_at, input_tokens, actual_cost) VALUES (7,$1::timestamptz-interval '1 microsecond',99999,999),(7,$2,99999,999),(8,$1,99999,999)", today, now)
	ticketStatsPGExec(t, db, "INSERT INTO pg_temp.usage_dashboard_hourly_users SELECT $1::timestamptz + hour * interval '1 hour', 7, (hour+1)*10, (hour+1)*20, (hour+1)*30, (hour+1)*40, hour+1, 1, $1::timestamptz + (hour+1) * interval '1 hour' FROM generate_series(0,2) AS hour", today)
	ticketStatsPGExec(t, db, "INSERT INTO pg_temp.usage_dashboard_hourly_users VALUES ($1::timestamptz+interval '3 hours',7,99999,0,0,0,999,1,$2)", today, now)
	for index, status := range []string{"PAID", "RECHARGING", "COMPLETED", "PENDING", "REFUNDED", "PARTIALLY_REFUNDED", "REFUNDING", "FAILED", "EXPIRED"} {
		ticketStatsPGExec(t, db, "INSERT INTO pg_temp.payment_orders VALUES (7,'balance',$1,$2,999,$3)", status, (index+1)*10, today)
	}
	start := today.AddDate(0, 0, -13)
	ticketStatsPGExec(t, db, "INSERT INTO pg_temp.payment_orders VALUES (7,'balance','COMPLETED',5,999,$1),(7,'balance','COMPLETED',999,999,$1::timestamptz-interval '1 microsecond'),(7,'balance','COMPLETED',999,999,$2),(7,'subscription','COMPLETED',999,999,$1),(8,'balance','COMPLETED',999,999,$1),(7,'balance','COMPLETED',999,999,NULL)", start, now)
}

func TestTicketRequesterStatsLocalPG(t *testing.T) {
	db := ticketStatsLocalPG(t)
	repo := &ticketRepository{db: db}
	today := time.Date(2026, 9, 25, 0, 0, 0, 0, ticketStatsLocation)
	now := today.Add(3*time.Hour + 30*time.Minute)
	for _, scenario := range []struct {
		name, mutation                    string
		usageAvailable, rechargeAvailable bool
	}{
		{"complete", "", true, true},
		{"missing_hour", "DELETE FROM pg_temp.usage_dashboard_hourly_users WHERE bucket_start = '2026-09-25 01:00+08'", true, true},
		{"empty_rollup", "TRUNCATE pg_temp.usage_dashboard_hourly_users", true, true},
		{"dau_only", "UPDATE pg_temp.usage_dashboard_hourly_users SET total_requests=0, input_tokens=99999", true, true},
		{"incomplete_hour", "UPDATE pg_temp.usage_dashboard_hourly_users SET computed_at=bucket_start+interval '30 minutes', input_tokens=99999", true, true},
		{"missing_column", "ALTER TABLE pg_temp.usage_dashboard_hourly_users DROP COLUMN actual_cost", true, true},
		{"legacy_rollup", "DROP TABLE pg_temp.usage_dashboard_hourly_users; CREATE TEMP TABLE usage_dashboard_hourly_users(bucket_start timestamptz, user_id bigint, PRIMARY KEY(bucket_start,user_id))", true, true},
		{"missing_rollup", "DROP TABLE pg_temp.usage_dashboard_hourly_users", true, true},
		{"missing_usage", "DROP TABLE pg_temp.usage_dashboard_hourly_users, pg_temp.usage_logs", false, true},
		{"missing_payment", "DROP TABLE pg_temp.payment_orders", true, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ticketStatsPGTables(t, db)
			ticketStatsPGFixture(t, db, today, now)
			var rawTokens int64
			var rawCost float64
			require.NoError(t, db.QueryRow(ticketRequesterUsageSQL, 7, today, now).Scan(&rawTokens, &rawCost))
			require.Equal(t, int64(1000), rawTokens)
			require.Equal(t, 10.0, rawCost)
			if scenario.mutation != "" {
				ticketStatsPGExec(t, db, scenario.mutation)
			}
			requester := &service.TicketRequester{ID: 7}
			repo.fillRequesterStatsAt(context.Background(), requester, now)
			require.Equal(t, scenario.usageAvailable, requester.UsageStatsAvailable)
			require.Equal(t, scenario.rechargeAvailable, requester.RechargeStatsAvailable)
			if scenario.usageAvailable {
				require.Equal(t, rawTokens, requester.TodayTokens)
				require.Equal(t, rawCost, requester.TodayCost)
			} else {
				require.Zero(t, requester.TodayTokens)
				require.Zero(t, requester.TodayCost)
			}
			if scenario.rechargeAvailable {
				require.Equal(t, 65.0, requester.Recharged14d)
			} else {
				require.Zero(t, requester.Recharged14d)
			}
		})
	}
	t.Run("shanghai_day_and_hour_boundaries", func(t *testing.T) {
		ticketStatsPGTables(t, db)
		ticketStatsPGFixture(t, db, today, now)
		for _, boundary := range []struct {
			now    time.Time
			tokens int64
			cost   float64
		}{
			{today.Add(-time.Microsecond), 0, 0}, {today, 0, 0}, {today.Add(time.Microsecond), 100, 1},
			{today.Add(time.Hour), 100, 1}, {today.Add(time.Hour + time.Microsecond), 300, 3},
		} {
			requester := &service.TicketRequester{ID: 7}
			repo.fillRequesterStatsAt(context.Background(), requester, boundary.now)
			require.True(t, requester.UsageStatsAvailable)
			require.Equal(t, boundary.tokens, requester.TodayTokens, boundary.now.String())
			require.Equal(t, boundary.cost, requester.TodayCost, boundary.now.String())
		}
	})
	t.Run("int32_token_sum", func(t *testing.T) {
		ticketStatsPGTables(t, db)
		ticketStatsPGExec(t, db, "INSERT INTO pg_temp.usage_logs VALUES(7,$1,2000000000,2000000000,2000000000,2000000000,1)", today)
		requester := &service.TicketRequester{ID: 7}
		repo.fillRequesterStatsAt(context.Background(), requester, now)
		require.True(t, requester.UsageStatsAvailable)
		require.Equal(t, int64(8000000000), requester.TodayTokens)
		require.True(t, requester.RechargeStatsAvailable)
		require.Zero(t, requester.Recharged14d)
	})
}

func ticketStatsPGExplain(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	var encoded []byte
	require.NoError(t, db.QueryRow("EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+query, args...).Scan(&encoded))
	var plans []map[string]any
	require.NoError(t, json.Unmarshal(encoded, &plans))
	require.Len(t, plans, 1)
	t.Logf("execution_ms=%v planning_ms=%v", plans[0]["Execution Time"], plans[0]["Planning Time"])
	var visit func(map[string]any)
	visit = func(node map[string]any) {
		if relation, ok := node["Relation Name"].(string); ok {
			t.Logf("relation=%s node=%v index=%v rows=%v loops=%v removed=%v cond=%v", relation, node["Node Type"], node["Index Name"], node["Actual Rows"], node["Actual Loops"], node["Rows Removed by Filter"], node["Index Cond"])
			loops, ok := node["Actual Loops"].(float64)
			require.True(t, ok)
			if loops > 0 {
				require.NotEqual(t, "Seq Scan", node["Node Type"], relation)
			}
		}
		if index, ok := node["Index Name"].(string); ok && node["Relation Name"] == nil {
			t.Logf("index=%s node=%v rows=%v loops=%v cond=%v", index, node["Node Type"], node["Actual Rows"], node["Actual Loops"], node["Index Cond"])
		}
		if children, ok := node["Plans"].([]any); ok {
			for _, child := range children {
				childPlan, ok := child.(map[string]any)
				require.True(t, ok)
				visit(childPlan)
			}
		}
	}
	rootPlan, ok := plans[0]["Plan"].(map[string]any)
	require.True(t, ok)
	visit(rootPlan)
}

func TestTicketRequesterStatsLocalPGExplain(t *testing.T) {
	db := ticketStatsLocalPG(t)
	ticketStatsPGTables(t, db)
	today := time.Date(2026, 9, 25, 0, 0, 0, 0, ticketStatsLocation)
	now := today.Add(3*time.Hour + 30*time.Minute)
	ticketStatsPGFixture(t, db, today, now)
	ticketStatsPGExec(t, db, "INSERT INTO pg_temp.usage_logs SELECT 100+sample%1000, $1::timestamptz - interval '15 days' + (sample/1000)*interval '1 hour', 10,20,30,40,1 FROM generate_series(1,300000) sample", today)
	ticketStatsPGExec(t, db, "INSERT INTO pg_temp.usage_dashboard_hourly_users SELECT $1::timestamptz - interval '5 days' + hour*interval '1 hour', user_id,10,20,30,40,1,1,$1::timestamptz+interval '1 day' FROM generate_series(100,1099) user_id CROSS JOIN generate_series(0,239) hour", today)
	ticketStatsPGExec(t, db, "INSERT INTO pg_temp.payment_orders SELECT 100+sample%1000,'balance','COMPLETED',10,99,$1::timestamptz-interval '30 days'+(sample/1000)*interval '3 hours' FROM generate_series(1,200000) sample", today)
	for _, relation := range []string{"usage_logs", "usage_dashboard_hourly_users", "payment_orders"} {
		ticketStatsPGExec(t, db, "ANALYZE pg_temp."+relation)
		var count int
		require.NoError(t, db.QueryRow("SELECT count(*) FROM pg_temp."+relation).Scan(&count))
		t.Logf("fixture %s rows=%d", relation, count)
	}
	var seqscan string
	require.NoError(t, db.QueryRow("SHOW enable_seqscan").Scan(&seqscan))
	require.Equal(t, "on", seqscan)
	t.Run("complete", func(t *testing.T) { ticketStatsPGExplain(t, db, ticketRequesterHourlySQL, 7, today, now) })
	t.Run("missing_hour", func(t *testing.T) {
		ticketStatsPGExec(t, db, "DELETE FROM pg_temp.usage_dashboard_hourly_users WHERE user_id=7 AND bucket_start=$1", today.Add(time.Hour))
		ticketStatsPGExplain(t, db, ticketRequesterHourlySQL, 7, today, now)
	})
	t.Run("raw_fallback", func(t *testing.T) { ticketStatsPGExplain(t, db, ticketRequesterUsageSQL, 7, today, now) })
	t.Run("recharge", func(t *testing.T) {
		ticketStatsPGExplain(t, db, ticketRequesterRechargeSQL, 7, today.AddDate(0, 0, -13), now)
	})
}

func TestTicketRequesterStatsShanghaiWindows(t *testing.T) {
	for _, test := range []struct {
		name, now, today, rechargeStart string
	}{
		{"before_midnight", "2026-09-24T15:59:59.999999999Z", "2026-09-24T00:00:00+08:00", "2026-09-11T00:00:00+08:00"},
		{"midnight", "2026-09-24T16:00:00Z", "2026-09-25T00:00:00+08:00", "2026-09-12T00:00:00+08:00"},
		{"after_midnight", "2026-09-24T16:00:00.000000001Z", "2026-09-25T00:00:00+08:00", "2026-09-12T00:00:00+08:00"},
		{"hour_boundary", "2026-09-24T17:00:00Z", "2026-09-25T00:00:00+08:00", "2026-09-12T00:00:00+08:00"},
		{"year_boundary", "2025-12-31T16:15:00Z", "2026-01-01T00:00:00+08:00", "2025-12-19T00:00:00+08:00"},
		{"leap_year", "2024-03-01T01:15:00Z", "2024-03-01T00:00:00+08:00", "2024-02-17T00:00:00+08:00"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, mock := ticketSQLMock(t)
			now, err := time.Parse(time.RFC3339Nano, test.now)
			require.NoError(t, err)
			today, err := time.Parse(time.RFC3339, test.today)
			require.NoError(t, err)
			rechargeStart, err := time.Parse(time.RFC3339, test.rechargeStart)
			require.NoError(t, err)
			mock.ExpectQuery(regexp.QuoteMeta(ticketRequesterHourlySQL)).
				WithArgs(int64(7), today.In(ticketStatsLocation), now.In(ticketStatsLocation)).
				WillReturnRows(sqlmock.NewRows([]string{"tokens", "cost"}).AddRow(int64(9000000000), 3.25))
			mock.ExpectQuery(regexp.QuoteMeta(ticketRequesterRechargeSQL)).
				WithArgs(int64(7), rechargeStart.In(ticketStatsLocation), now.In(ticketStatsLocation)).
				WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(80.0))
			requester := &service.TicketRequester{ID: 7}
			repo.fillRequesterStatsAt(context.Background(), requester, now)
			require.Equal(t, int64(9000000000), requester.TodayTokens)
			require.Equal(t, 3.25, requester.TodayCost)
			require.Equal(t, 80.0, requester.Recharged14d)
			require.True(t, requester.UsageStatsAvailable)
			require.True(t, requester.RechargeStatsAvailable)
		})
	}
}

func TestTicketRequesterStatsMissingRollupFallback(t *testing.T) {
	for _, missing := range []error{&pq.Error{Code: "42P01"}, &pq.Error{Code: "42703"}, sql.ErrNoRows} {
		t.Run(missing.Error(), func(t *testing.T) {
			repo, mock := ticketSQLMock(t)
			now := time.Date(2026, 9, 25, 12, 34, 56, 0, ticketStatsLocation)
			today := time.Date(2026, 9, 25, 0, 0, 0, 0, ticketStatsLocation)
			mock.ExpectQuery(regexp.QuoteMeta(ticketRequesterHourlySQL)).WithArgs(int64(7), today, now).WillReturnError(missing)
			mock.ExpectQuery(regexp.QuoteMeta(ticketRequesterUsageSQL)).WithArgs(int64(7), today, now).
				WillReturnRows(sqlmock.NewRows([]string{"tokens", "cost"}).AddRow(350, 1.25))
			mock.ExpectQuery(regexp.QuoteMeta(ticketRequesterRechargeSQL)).WithArgs(int64(7), today.AddDate(0, 0, -13), now).
				WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(20.0))
			requester := &service.TicketRequester{ID: 7}
			repo.fillRequesterStatsAt(context.Background(), requester, now)
			require.Equal(t, int64(350), requester.TodayTokens)
			require.Equal(t, 1.25, requester.TodayCost)
			require.Equal(t, 20.0, requester.Recharged14d)
			require.True(t, requester.UsageStatsAvailable)
			require.True(t, requester.RechargeStatsAvailable)
		})
	}
}

func TestTicketRequesterStatsSQLBoundsAndExclusiveHourlySources(t *testing.T) {
	for _, clause := range []string{
		"generate_series($2::timestamptz, $3::timestamptz, interval '1 hour')",
		"user_id = $1 AND bucket_start = hours.bucket_start",
		"bucket_start + interval '1 hour' <= $3",
		"computed_at >= bucket_start + interval '1 hour' AND total_requests > 0",
		"hourly.bucket_start IS NULL AND user_id = $1",
		"created_at >= hours.bucket_start",
		"created_at < LEAST(hours.bucket_start + interval '1 hour', $3::timestamptz)",
		"WHERE hours.bucket_start < $3",
		"COALESCE(hourly.tokens, raw.tokens)",
		"COALESCE(hourly.cost, raw.cost)",
		"SUM(actual_cost)",
		"SUM(input_tokens::bigint + output_tokens + cache_creation_tokens + cache_read_tokens)",
	} {
		require.Contains(t, ticketRequesterHourlySQL, clause)
	}
	require.Contains(t, ticketRequesterUsageSQL, "user_id = $1 AND created_at >= $2 AND created_at < $3")
	require.Contains(t, ticketRequesterUsageSQL, "SUM(actual_cost)")
	require.Contains(t, ticketRequesterUsageSQL, "input_tokens::bigint")
	require.NotContains(t, ticketRequesterHourlySQL, "total_cost")
	require.NotContains(t, ticketRequesterUsageSQL, "total_cost")
	require.Contains(t, ticketRequesterRechargeSQL, "SUM(amount)")
	require.Contains(t, ticketRequesterRechargeSQL, "user_id = $1 AND order_type = 'balance'")
	require.Contains(t, ticketRequesterRechargeSQL, "status IN ('PAID', 'RECHARGING', 'COMPLETED')")
	require.Contains(t, ticketRequesterRechargeSQL, "paid_at >= $2 AND paid_at < $3")
	require.NotContains(t, ticketRequesterRechargeSQL, "pay_amount")
	require.NotContains(t, ticketRequesterRechargeSQL, "created_at")
}

func TestTicketRequesterStatsDoesNotPublishPartialScan(t *testing.T) {
	repo, mock := ticketSQLMock(t)
	mock.ExpectQuery(regexp.QuoteMeta(ticketRequesterHourlySQL)).
		WillReturnRows(sqlmock.NewRows([]string{"tokens", "cost"}).AddRow(999, "invalid-cost"))
	mock.ExpectQuery(regexp.QuoteMeta(ticketRequesterRechargeSQL)).
		WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(25.0))
	requester := &service.TicketRequester{ID: 7}
	repo.fillRequesterStats(context.Background(), requester)
	require.Zero(t, requester.TodayTokens)
	require.Zero(t, requester.TodayCost)
	require.Equal(t, 25.0, requester.Recharged14d)
	require.False(t, requester.UsageStatsAvailable)
	require.True(t, requester.RechargeStatsAvailable)
}

func TestTicketRequesterStatsFallbackFailureIsOptional(t *testing.T) {
	repo, mock := ticketSQLMock(t)
	mock.ExpectQuery(regexp.QuoteMeta(ticketRequesterHourlySQL)).WillReturnError(&pq.Error{Code: "42P01"})
	mock.ExpectQuery(regexp.QuoteMeta(ticketRequesterUsageSQL)).WillReturnError(errors.New("usage unavailable"))
	mock.ExpectQuery(regexp.QuoteMeta(ticketRequesterRechargeSQL)).WillReturnError(errors.New("payment unavailable"))
	requester := &service.TicketRequester{ID: 7}
	repo.fillRequesterStats(context.Background(), requester)
	require.Equal(t, &service.TicketRequester{ID: 7}, requester)
}

func TestTicketRequesterStatsInvalidRequesterSkipsQueries(t *testing.T) {
	repo, _ := ticketSQLMock(t)
	for _, requester := range []*service.TicketRequester{nil, {}, {ID: -1}} {
		repo.fillRequesterStats(context.Background(), requester)
	}
	var nilRepo *ticketRepository
	nilRepo.fillRequesterStats(context.Background(), &service.TicketRequester{ID: 7})
	(&ticketRepository{}).fillRequesterStats(context.Background(), &service.TicketRequester{ID: 7})
}

func TestTicketRepositoryAdminDetailSurvivesRequesterStatsFailure(t *testing.T) {
	for _, mode := range []string{"errors", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			repo, mock := ticketSQLMock(t)
			ticketExpectLookup(mock, ticketSQLRows("open", 100), false, true)
			mock.ExpectQuery("SELECT m.id, m.ticket_id").WillReturnRows(ticketMessageRows(100)).RowsWillBeClosed()
			mock.ExpectQuery("SELECT id, COALESCE").WithArgs(int64(7)).
				WillReturnRows(sqlmock.NewRows([]string{"id", "username", "email", "balance", "status", "created_at"}).
					AddRow(7, "requester", "requester@example.test", 12.5, "active", ticketSQLTime))
			mock.ExpectExec("INSERT INTO support_ticket_views").WithArgs(int64(10), int64(9), true, int64(100)).WillReturnResult(sqlmock.NewResult(0, 1))
			usage := mock.ExpectQuery(regexp.QuoteMeta(ticketRequesterHourlySQL))
			if mode == "timeout" {
				usage.WillDelayFor(3 * time.Second).WillReturnRows(sqlmock.NewRows([]string{"tokens", "cost"}).AddRow(100, 1.0))
				mock.ExpectQuery(regexp.QuoteMeta(ticketRequesterRechargeSQL)).WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow(25.0))
			} else {
				usage.WillReturnError(errors.New("usage unavailable"))
				mock.ExpectQuery(regexp.QuoteMeta(ticketRequesterRechargeSQL)).WillReturnError(errors.New("payment unavailable"))
			}
			ctx := context.Background()
			started := time.Now()
			detail, err := repo.Detail(ctx, service.TicketActor{UserID: 9, Admin: true}, 10, 0)
			require.NoError(t, err)
			require.NoError(t, ctx.Err())
			require.Less(t, time.Since(started), 2800*time.Millisecond)
			require.Len(t, detail.Messages, 1)
			require.Equal(t, 12.5, detail.Requester.Balance)
			require.Zero(t, detail.Requester.TodayTokens)
			require.Zero(t, detail.Requester.TodayCost)
			require.False(t, detail.Requester.UsageStatsAvailable)
			require.Equal(t, mode == "timeout", detail.Requester.RechargeStatsAvailable)
			if mode == "timeout" {
				require.Equal(t, 25.0, detail.Requester.Recharged14d)
			} else {
				require.Zero(t, detail.Requester.Recharged14d)
			}
		})
	}
}

func TestTicketRepositoryUserDetailDoesNotExposeRequesterStats(t *testing.T) {
	repo, mock := ticketSQLMock(t)
	ticketExpectLookup(mock, ticketSQLRows("open", 100), false, false)
	mock.ExpectQuery("SELECT m.id, m.ticket_id").WillReturnRows(ticketMessageRows(100)).RowsWillBeClosed()
	mock.ExpectExec("INSERT INTO support_ticket_views").WithArgs(int64(10), int64(7), false, int64(100)).WillReturnResult(sqlmock.NewResult(0, 1))
	detail, err := repo.Detail(context.Background(), ticketSQLActor, 10, 0)
	require.NoError(t, err)
	require.Nil(t, detail.Requester)
	encoded, err := json.Marshal(detail)
	require.NoError(t, err)
	for _, field := range []string{"requester", "today_tokens", "today_cost", "recharged_14d", "usage_stats_available", "recharge_stats_available"} {
		require.NotContains(t, string(encoded), field)
	}
}
