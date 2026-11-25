package repository

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Opt-in like the traffic Postgres test: an isolated pg_temp schema, no
// migration or production data is touched. The same checks run against the
// migrated schema in the integration suite (usage_connections_integration_test.go).
func TestConnectionTracePostgresBatchInsert(t *testing.T) {
	dsn := os.Getenv("CONNECTION_TRACE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CONNECTION_TRACE_TEST_POSTGRES_DSN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	// Temp tables live in one session.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	_, err = db.ExecContext(ctx, "SET search_path TO pg_temp")
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/276_request_connection_traces.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, strings.ReplaceAll(string(migration), "CREATE TABLE IF NOT EXISTS", "CREATE TEMP TABLE IF NOT EXISTS"))
	require.NoError(t, err)

	runConnectionTracePostgresChecks(t, db)
}

// runConnectionTracePostgresChecks is the part of the batch writer the fake
// driver cannot prove: that Postgres accepts the multi-row statement, stores
// the same values the one-row writer stored, ignores conflicts, isolates a bad
// row, and that SET LOCAL takes effect for the batch without leaking.
func runConnectionTracePostgresChecks(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()
	repo := newUsageLogRepositoryWithSQL(nil, db)
	var written []string
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM request_connection_traces WHERE trace_id = ANY($1::uuid[])`, pq.Array(written))
	})
	count := func(ids []string) int {
		t.Helper()
		var n int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM request_connection_traces WHERE trace_id = ANY($1::uuid[])`, pq.Array(ids)).Scan(&n))
		return n
	}
	show := func() string {
		t.Helper()
		var v string
		require.NoError(t, db.QueryRowContext(ctx, `SHOW synchronous_commit`).Scan(&v))
		return v
	}
	serverSetting := show()

	t.Run("values and conflicts", func(t *testing.T) {
		records := connTraceFixtures(5)
		ids := connTraceIDsOf(records)
		written = append(written, ids...)
		// A trace id repeated inside one statement must be skipped, not rejected.
		repo.flushConnectionBatch(ctx, append(append([]service.ConnectionTrace(nil), records...), records[1]))
		require.Zero(t, repo.connectionWriteDrops.total.Load())
		require.Equal(t, 5, count(ids))

		rec := records[3]
		var (
			startedAt                                                time.Time
			requestID, clientRequestID, model, path, clientIP, agent string
			userID, apiKeyID, accountID, groupID                     int64
			stream                                                   bool
			source, host, scheme, method, httpVersion, tlsVersion    string
			upstreamStatus, networkError                             string
			detailTraceID, firstByte                                 string
		)
		require.NoError(t, db.QueryRowContext(ctx, `SELECT started_at,request_id,client_request_id,user_id,api_key_id,account_id,group_id,model,path,client_ip,user_agent,stream,
 source,host,scheme,method,http_version,tls_version,upstream_status,network_error,detail->>'trace_id',detail->'events'->>'first_byte'
 FROM request_connection_traces WHERE trace_id=$1::uuid`, rec.TraceID).Scan(&startedAt, &requestID, &clientRequestID, &userID, &apiKeyID, &accountID, &groupID,
			&model, &path, &clientIP, &agent, &stream, &source, &host, &scheme, &method, &httpVersion, &tlsVersion, &upstreamStatus, &networkError, &detailTraceID, &firstByte))
		require.True(t, rec.StartedAt.Equal(startedAt))
		require.Equal(t, []any{rec.RequestID, rec.ClientRequestID, rec.UserID, rec.APIKeyID, rec.AccountID, rec.GroupID, rec.Model, rec.Path, rec.ClientIP, rec.UserAgent, rec.Stream, rec.Source, rec.Host, rec.Method, rec.UpstreamStatus, rec.NetworkError, rec.TraceID, "12.5"},
			[]any{requestID, clientRequestID, userID, apiKeyID, accountID, groupID, model, path, clientIP, agent, stream, source, host, method, upstreamStatus, networkError, detailTraceID, firstByte})
		// Protocol metadata is deliberately not stored (connectionTraceProtocol*).
		require.Equal(t, []string{"", "", ""}, []string{scheme, httpVersion, tlsVersion})

		// Replaying the batch is a no-op.
		repo.flushConnectionBatch(ctx, records)
		require.Zero(t, repo.connectionWriteDrops.total.Load())
		require.Equal(t, 5, count(ids))
	})

	t.Run("full batch", func(t *testing.T) {
		records := connTraceFixtures(connectionTraceBatchMax)
		ids := connTraceIDsOf(records)
		written = append(written, ids...)
		repo.flushConnectionBatch(ctx, records)
		require.Equal(t, connectionTraceBatchMax, count(ids))
		require.Zero(t, repo.connectionWriteDrops.total.Load())
	})

	t.Run("a bad value costs only its own trace", func(t *testing.T) {
		records := connTraceFixtures(5)
		records[1].NetworkError = strings.Repeat("e", 65) // varchar(64): SQLSTATE 22001
		records[3].TraceID = "not-a-uuid"                 // SQLSTATE 22P02
		good := []string{records[0].TraceID, records[2].TraceID, records[4].TraceID}
		written = append(written, good...)
		before := repo.connectionWriteDrops.total.Load()
		repo.flushConnectionBatch(ctx, records)
		require.Equal(t, 3, count(good))
		require.EqualValues(t, 2, repo.connectionWriteDrops.total.Load()-before)
		require.Equal(t, 0, count([]string{records[1].TraceID}))
	})

	t.Run("writer end to end", func(t *testing.T) {
		records := connTraceFixtures(1000)
		ids := connTraceIDsOf(records)
		written = append(written, ids...)
		for _, rec := range records {
			repo.RecordConnectionTrace(rec)
		}
		repo.StopConnectionDiagnostics()
		require.Equal(t, 1000, count(ids))
	})

	t.Run("async commit is local to the batch transaction", func(t *testing.T) {
		probe := connTraceFixture(1)
		written = append(written, probe.TraceID)
		require.NoError(t, execAsyncCommit(ctx, db, `INSERT INTO request_connection_traces (trace_id,started_at,request_id,client_request_id,user_id,api_key_id,path,stream,detail)
 VALUES ($1::uuid, now(), 'probe', '', 0, 0, '/probe', false, jsonb_build_object('synchronous_commit', current_setting('synchronous_commit')))`, probe.TraceID))
		var inside string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT detail->>'synchronous_commit' FROM request_connection_traces WHERE trace_id=$1::uuid`, probe.TraceID).Scan(&inside))
		require.Equal(t, "off", inside, "SET LOCAL is in effect for the write")
		require.Equal(t, serverSetting, show(), "and gone once the transaction ended")
	})
}
