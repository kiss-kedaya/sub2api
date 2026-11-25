//go:build integration

package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// Differential test of the channel monitor v2 aggregator.
//
// The aggregator used to rewrite every window with DELETE + INSERT; it now writes
// only the rows that changed. Both versions run side by side on the same random
// event stream (see channel_monitor_v2_aggregation_legacy_oracle_test.go for the
// frozen original) in two isolated schemas of the same PostgreSQL, each with its own
// copy of the source tables and of the 8 aggregate tables, and every table is
// compared row for row (computed_at aside, it is the one column that is meant to
// differ) after every tick.

type cmv2Impl interface {
	RecomputeRange(ctx context.Context, start, end time.Time) error
	RecomputeLiveRange(ctx context.Context, start, end time.Time) error
}

type cmv2LegacyImpl struct{ repo *channelMonitorV2Repository }

func (l cmv2LegacyImpl) RecomputeRange(ctx context.Context, start, end time.Time) error {
	return l.repo.recomputeRangeLegacy(ctx, start, end, channelMonitorV2RecomputeOpts{
		errorLookback: channelMonitorV2ErrorLookbackBackfill,
		prune:         true,
	})
}

func (l cmv2LegacyImpl) RecomputeLiveRange(ctx context.Context, start, end time.Time) error {
	return l.repo.recomputeRangeLegacy(ctx, start, end, channelMonitorV2RecomputeOpts{
		errorLookback: channelMonitorV2ErrorLookbackLive,
		prune:         false,
	})
}

// cmv2Side is one schema: source tables, aggregate tables and the implementation
// under test.
type cmv2Side struct {
	name     string
	schema   string
	timeZone string
	db       *sql.DB
	impl     cmv2Impl
	restart  func()
}

var cmv2DiffTables = []string{
	"channel_monitor_v2_metrics_1m",
	"channel_monitor_v2_user_metrics_1m",
	"channel_monitor_v2_error_metrics_1m",
	"channel_monitor_v2_latency_histograms_1m",
	"channel_monitor_v2_metrics_rollup",
	"channel_monitor_v2_user_metrics_rollup",
	"channel_monitor_v2_error_metrics_rollup",
	"channel_monitor_v2_latency_histograms_rollup",
}

// cmv2SchemaConnector pins every pooled connection to one schema and session time
// zone, so the unqualified table names in the aggregation SQL resolve to this
// side's private copies.
type cmv2SchemaConnector struct {
	driver.Connector
	schema   string
	timeZone string
}

func (c cmv2SchemaConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	execer, ok := conn.(driver.ExecerContext)
	if !ok {
		_ = conn.Close()
		return nil, errors.New("postgres connection does not support ExecContext")
	}
	for _, stmt := range []string{
		"SET search_path TO " + pq.QuoteIdentifier(c.schema),
		"SET TIME ZONE " + pq.QuoteLiteral(c.timeZone),
		"SET synchronous_commit TO off", // durability is irrelevant here; fsync waits are not
	} {
		if _, err := execer.ExecContext(ctx, stmt, nil); err != nil {
			_ = conn.Close()
			return nil, err
		}
	}
	return conn, nil
}

func newCMV2Side(t *testing.T, name, timeZone string, build func(db *sql.DB) (cmv2Impl, func())) *cmv2Side {
	t.Helper()
	require.NotEmpty(t, integrationDSN, "integration DSN is not set")
	ctx := context.Background()
	schema := fmt.Sprintf("cmv2_%s_%d", name, time.Now().UnixNano())
	_, err := integrationDB.ExecContext(ctx, "CREATE SCHEMA "+pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+pq.QuoteIdentifier(schema)+" CASCADE")
	})

	connector, err := pq.NewConnector(integrationDSN)
	require.NoError(t, err)
	db := sql.OpenDB(cmv2SchemaConnector{Connector: connector, schema: schema, timeZone: timeZone})
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })

	// Source tables copy the real columns; only the aggregate tables' DDL comes
	// straight from the migrations that created them.
	for _, ddl := range []string{
		`CREATE TABLE groups (LIKE public.groups INCLUDING DEFAULTS)`,
		`CREATE TABLE accounts (LIKE public.accounts INCLUDING DEFAULTS)`,
		`CREATE TABLE usage_logs (LIKE public.usage_logs INCLUDING DEFAULTS)`,
		`CREATE TABLE ops_error_logs (LIKE public.ops_error_logs INCLUDING DEFAULTS)`,
		`CREATE INDEX idx_cmv2_usage_logs_created_at ON usage_logs (created_at)`,
		`CREATE INDEX idx_cmv2_ops_error_logs_created_at ON ops_error_logs (created_at)`,
		`CREATE INDEX idx_cmv2_ops_error_logs_request_id ON ops_error_logs (request_id)`,
	} {
		_, err = db.ExecContext(ctx, ddl)
		require.NoError(t, err, ddl)
	}
	for _, name := range []string{"194_channel_monitor_v2.sql", "199_channel_monitor_v2_fixed_rollups.sql"} {
		ddl, readErr := migrations.FS.ReadFile(name)
		require.NoError(t, readErr)
		_, err = db.ExecContext(ctx, string(ddl))
		require.NoError(t, err, name)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO groups (id, name, platform) VALUES
			(1, 'g1', 'anthropic'), (2, 'g2', 'openai'), (3, 'g3', 'grok'), (4, 'g4', 'gemini'),
			(5, 'g5', 'kimi'), (6, 'g6', 'deepseek'), (7, 'g7', 'composite'), (8, 'g8', '');
		INSERT INTO accounts (id, name, platform, type) VALUES
			(1, 'a1', 'anthropic', 'apikey'), (2, 'a2', 'openai', 'apikey'), (3, 'a3', 'grok', 'apikey'),
			(4, 'a4', 'Gemini', 'apikey'), (5, 'a5', 'kimi', 'apikey'), (6, 'a6', 'zhipu', 'apikey'),
			(7, 'a7', 'deepseek', 'apikey'), (8, 'a8', 'antigravity', 'apikey'), (9, 'a9', 'kiro', 'apikey'),
			(10, 'a10', 'OpenAI', 'apikey'), (11, 'a11', 'anthropic', 'oauth'), (12, 'a12', 'openai', 'oauth');
	`)
	require.NoError(t, err)

	side := &cmv2Side{name: name, schema: schema, timeZone: timeZone, db: db}
	side.impl, side.restart = build(db)
	return side
}

func newCMV2Pair(t *testing.T, timeZone string) (legacy, fresh *cmv2Side) {
	t.Helper()
	legacy = newCMV2Side(t, "legacy", timeZone, func(db *sql.DB) (cmv2Impl, func()) {
		return cmv2LegacyImpl{repo: &channelMonitorV2Repository{db: db}}, func() {}
	})
	fresh = newCMV2Side(t, "fresh", timeZone, func(db *sql.DB) (cmv2Impl, func()) {
		var repo *channelMonitorV2Repository
		restart := func() { repo = &channelMonitorV2Repository{db: db} }
		restart()
		return cmv2Forward{get: func() *channelMonitorV2Repository { return repo }}, restart
	})
	return legacy, fresh
}

// cmv2Forward lets a "process restart" swap the repository (and with it any
// in-memory state it keeps) without rebuilding the side.
type cmv2Forward struct {
	get func() *channelMonitorV2Repository
}

func (f cmv2Forward) RecomputeRange(ctx context.Context, start, end time.Time) error {
	return f.get().RecomputeRange(ctx, start, end)
}

func (f cmv2Forward) RecomputeLiveRange(ctx context.Context, start, end time.Time) error {
	return f.get().RecomputeLiveRange(ctx, start, end)
}

func (s *cmv2Side) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	_, err := s.db.ExecContext(context.Background(), query, args...)
	require.NoError(t, err, "%s: %s", s.name, query)
}

func (s *cmv2Side) dataThrough(t *testing.T) time.Time {
	t.Helper()
	var through sql.NullTime
	require.NoError(t, s.db.QueryRowContext(context.Background(),
		`SELECT data_through FROM channel_monitor_v2_watermarks WHERE id = 1`).Scan(&through))
	if !through.Valid {
		return time.Time{}
	}
	return through.Time.UTC()
}

// liveTick is what ChannelMonitorV2Aggregator.runOnce does once seeded: refresh
// the trailing overlap, extended back to the persisted watermark when ticks were
// missed (capped at the 6h catch-up limit).
func (s *cmv2Side) liveTick(t *testing.T, now time.Time) {
	t.Helper()
	liveStart := now.Add(-2 * time.Minute)
	if through := s.dataThrough(t); !through.IsZero() && through.Before(liveStart) {
		liveStart = through
	}
	if floor := now.Add(-6 * time.Hour); liveStart.Before(floor) {
		liveStart = floor
	}
	require.NoError(t, s.impl.RecomputeLiveRange(context.Background(), liveStart, now), "%s live tick %s", s.name, now.Format(time.RFC3339))
}

// ---- table comparison ----

func (s *cmv2Side) tableColumns(t *testing.T, table string) string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2 AND column_name <> 'computed_at'
		ORDER BY ordinal_position`, s.schema, table)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var cols []string
	for rows.Next() {
		var col string
		require.NoError(t, rows.Scan(&col))
		cols = append(cols, col)
	}
	require.NoError(t, rows.Err())
	require.NotEmpty(t, cols, table)
	return strings.Join(cols, ", ")
}

type cmv2Digest struct {
	rows int
	hash string
}

// rowsSince limits a comparison to the part of a table the live ticks can still
// touch: recent buckets, plus every 12h/1d rollup (few rows). Zero = everything.
func cmv2RowsSince(table string, since time.Time) string {
	if since.IsZero() {
		return ""
	}
	cut := pq.QuoteLiteral(since.UTC().Format(time.RFC3339)) + "::timestamptz"
	if strings.HasSuffix(table, "_rollup") {
		return "WHERE bucket_seconds >= 43200 OR bucket_start >= " + cut
	}
	return "WHERE bucket_start >= " + cut
}

// digest is order independent (no sort): the sum of 64-bit hashes of the row
// texts. Rows are compared as text, so every column, timestamps included, counts.
func (s *cmv2Side) digest(t *testing.T, table, cols string, since time.Time) cmv2Digest {
	t.Helper()
	var d cmv2Digest
	require.NoError(t, s.db.QueryRowContext(context.Background(), fmt.Sprintf(
		`SELECT count(*), COALESCE(sum(hashtextextended(r::text, 0)::numeric), 0)::text FROM (SELECT %s FROM %s %s) r`,
		cols, table, cmv2RowsSince(table, since))).Scan(&d.rows, &d.hash), "%s %s", s.name, table)
	return d
}

func (s *cmv2Side) rowTexts(t *testing.T, table, cols string, since time.Time) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), fmt.Sprintf(`SELECT r::text FROM (SELECT %s FROM %s %s) r ORDER BY 1`, cols, table, cmv2RowsSince(table, since)))
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var text string
		require.NoError(t, rows.Scan(&text))
		out = append(out, text)
	}
	require.NoError(t, rows.Err())
	return out
}

func (s *cmv2Side) watermark(t *testing.T) string {
	t.Helper()
	var text string
	require.NoError(t, s.db.QueryRowContext(context.Background(), `
		SELECT concat_ws('|', usage_coverage_start, error_coverage_start, data_through, backfill_cursor)
		FROM channel_monitor_v2_watermarks WHERE id = 1`).Scan(&text))
	return text
}

func cmv2SetDiff(a, b []string) []string {
	seen := make(map[string]int, len(b))
	for _, row := range b {
		seen[row]++
	}
	var out []string
	for _, row := range a {
		if seen[row] > 0 {
			seen[row]--
			continue
		}
		out = append(out, row)
	}
	return out
}

func cmv2Head(rows []string, n int) string {
	sort.Strings(rows)
	if len(rows) > n {
		rows = append(rows[:n:n], fmt.Sprintf("... %d more", len(rows)-n))
	}
	return strings.Join(rows, "\n    ")
}

// requireSidesEqual compares all 8 aggregate tables and the watermark. since
// limits the comparison to recent rows (zero compares every row).
func requireSidesEqual(t *testing.T, legacy, fresh *cmv2Side, columns map[string]string, label string, since time.Time) {
	t.Helper()
	require.Equal(t, legacy.watermark(t), fresh.watermark(t), "%s: watermark", label)
	for _, table := range cmv2DiffTables {
		cols := columns[table]
		want, got := legacy.digest(t, table, cols, since), fresh.digest(t, table, cols, since)
		if want == got {
			continue
		}
		wantRows, gotRows := legacy.rowTexts(t, table, cols, since), fresh.rowTexts(t, table, cols, since)
		t.Fatalf("%s: %s differs (legacy %d rows, fresh %d rows)\n  only in legacy:\n    %s\n  only in fresh:\n    %s",
			label, table, want.rows, got.rows,
			cmv2Head(cmv2SetDiff(wantRows, gotRows), 12), cmv2Head(cmv2SetDiff(gotRows, wantRows), 12))
	}
}

func cmv2Columns(t *testing.T, side *cmv2Side) map[string]string {
	t.Helper()
	columns := make(map[string]string, len(cmv2DiffTables))
	for _, table := range cmv2DiffTables {
		columns[table] = side.tableColumns(t, table)
	}
	return columns
}

// ---- random event stream ----

type cmv2UsageRow struct {
	ID             int64     `json:"id"`
	UserID         int64     `json:"user_id"`
	APIKeyID       int64     `json:"api_key_id"`
	AccountID      int64     `json:"account_id"`
	RequestID      *string   `json:"request_id"`
	Model          string    `json:"model"`
	RequestedModel *string   `json:"requested_model"`
	InputTokens    int       `json:"input_tokens"`
	OutputTokens   int       `json:"output_tokens"`
	CacheCreation  int       `json:"cache_creation_tokens"`
	CacheRead      int       `json:"cache_read_tokens"`
	ActualCost     float64   `json:"actual_cost"`
	Stream         bool      `json:"stream"`
	DurationMS     *int      `json:"duration_ms"`
	FirstTokenMS   *int      `json:"first_token_ms"`
	CreatedAt      time.Time `json:"created_at"`
	GroupID        *int64    `json:"group_id"`
	RequestType    int       `json:"request_type"`
	BillingMode    *string   `json:"billing_mode"`
	ImageCount     *int      `json:"image_count"`
	ImageInput     int       `json:"image_input_tokens"`
	ImageOutput    int       `json:"image_output_tokens"`
}

type cmv2ErrorRow struct {
	ID                   int64           `json:"id"`
	RequestID            *string         `json:"request_id"`
	UserID               *int64          `json:"user_id"`
	AccountID            *int64          `json:"account_id"`
	GroupID              *int64          `json:"group_id"`
	Platform             *string         `json:"platform"`
	Model                *string         `json:"model"`
	RequestedModel       *string         `json:"requested_model"`
	RequestPath          *string         `json:"request_path"`
	InboundEndpoint      *string         `json:"inbound_endpoint"`
	Stream               bool            `json:"stream"`
	IsCountTokens        bool            `json:"is_count_tokens"`
	RequestType          *int            `json:"request_type"`
	ErrorType            string          `json:"error_type"`
	StatusCode           *int            `json:"status_code"`
	UpstreamStatusCode   *int            `json:"upstream_status_code"`
	ErrorMessage         *string         `json:"error_message"`
	ErrorBody            *string         `json:"error_body"`
	ErrorSource          *string         `json:"error_source"`
	ErrorOwner           *string         `json:"error_owner"`
	UpstreamErrorMessage *string         `json:"upstream_error_message"`
	UpstreamErrorDetail  *string         `json:"upstream_error_detail"`
	UpstreamErrors       json.RawMessage `json:"upstream_errors"`
	CreatedAt            time.Time       `json:"created_at"`
}

const cmv2InsertUsageSQL = `
INSERT INTO usage_logs (id, user_id, api_key_id, account_id, request_id, model, requested_model, input_tokens, output_tokens,
  cache_creation_tokens, cache_read_tokens, actual_cost, stream, duration_ms, first_token_ms, created_at, group_id,
  request_type, billing_mode, image_count, image_input_tokens, image_output_tokens)
SELECT x.id, x.user_id, x.api_key_id, x.account_id, x.request_id, x.model, x.requested_model, x.input_tokens, x.output_tokens,
  x.cache_creation_tokens, x.cache_read_tokens, x.actual_cost, x.stream, x.duration_ms, x.first_token_ms, x.created_at, x.group_id,
  x.request_type, x.billing_mode, x.image_count, x.image_input_tokens, x.image_output_tokens
FROM jsonb_to_recordset($1::jsonb) AS x(id bigint, user_id bigint, api_key_id bigint, account_id bigint, request_id text, model text,
  requested_model text, input_tokens int, output_tokens int, cache_creation_tokens int, cache_read_tokens int, actual_cost numeric,
  stream boolean, duration_ms int, first_token_ms int, created_at timestamptz, group_id bigint, request_type smallint,
  billing_mode text, image_count int, image_input_tokens int, image_output_tokens int)`

const cmv2InsertErrorSQL = `
INSERT INTO ops_error_logs (id, request_id, user_id, account_id, group_id, platform, model, requested_model, request_path,
  inbound_endpoint, stream, is_count_tokens, request_type, error_phase, error_type, status_code, upstream_status_code,
  error_message, error_body, error_source, error_owner, upstream_error_message, upstream_error_detail, upstream_errors, created_at)
SELECT x.id, x.request_id, x.user_id, x.account_id, x.group_id, x.platform, x.model, x.requested_model, x.request_path,
  x.inbound_endpoint, x.stream, x.is_count_tokens, x.request_type, 'upstream', x.error_type, x.status_code, x.upstream_status_code,
  x.error_message, x.error_body, x.error_source, x.error_owner, x.upstream_error_message, x.upstream_error_detail, x.upstream_errors, x.created_at
FROM jsonb_to_recordset($1::jsonb) AS x(id bigint, request_id text, user_id bigint, account_id bigint, group_id bigint, platform text,
  model text, requested_model text, request_path text, inbound_endpoint text, stream boolean, is_count_tokens boolean,
  request_type smallint, error_type text, status_code int, upstream_status_code int, error_message text, error_body text,
  error_source text, error_owner text, upstream_error_message text, upstream_error_detail text, upstream_errors jsonb, created_at timestamptz)`

type cmv2Op struct {
	query string
	args  []any
}

type cmv2Live struct {
	id      int64
	created time.Time
}

type cmv2Gen struct {
	rng         *rand.Rand
	nextUsageID int64
	nextErrorID int64
	usage       []cmv2Live
	errs        []cmv2Live
	requestIDs  []string
	errorReqIDs []string
}

func newCMV2Gen(seed int64) *cmv2Gen {
	return &cmv2Gen{rng: rand.New(rand.NewSource(seed)), nextUsageID: 1, nextErrorID: 1}
}

func cmv2Ptr[T any](v T) *T { return &v }

var (
	cmv2Models = []string{"claude-sonnet-4", "claude-opus-4", "gpt-5", "gpt-5-mini", "grok-4", "gemini-2.5-pro", "kimi-k2",
		"deepseek-v3", "o3", "claude-haiku-3", "glm-4.6", "", "  "}
	cmv2RequestedModels = []string{"claude-sonnet-4", " gpt-5 ", "gpt-5-mini", "grok-4", "gemini-2.5-pro", "", "   ", "kimi-k2"}
	// Latencies on and around every histogram bound, plus values outside the table.
	cmv2Latencies = []int{-1, 0, 1, 49, 50, 51, 99, 100, 101, 249, 250, 251, 499, 500, 501, 999, 1000, 1001, 1999, 2000, 2001,
		2999, 3000, 3001, 4999, 5000, 5001, 7999, 8000, 8001, 9999, 10000, 10001, 14999, 15000, 15001, 29999, 30000, 30001,
		59999, 60000, 60001, 119999, 120000, 120001, 299999, 300000, 300001, 599999, 600000, 600001, 2000000}
	cmv2Inputs    = []int{0, 5, 100, 4000, 9999, 10000, 10001, 15000, 30000, 80000}
	cmv2Creations = []int{0, 0, 0, 3000, 5000, 10000, 10001}
	cmv2Reads     = []int{0, 0, 3000, 10000, 20000}
	cmv2Texts     = []string{"content policy violation", "invalid api key", "context length exceeded", "failed to deserialize body",
		"does not support the requested model", "group not allowed", "insufficient balance", "no available accounts",
		"rate limit exceeded", "request timeout", "connection reset by peer", "client canceled", "tls handshake failure",
		"boom", "Overloaded", "missing terminal event"}
	cmv2ErrorTypes   = []string{"api_error", "internal", "cyber_policy", "upstream_error", "rate_limit_error", ""}
	cmv2StatusCodes  = []int{400, 401, 403, 404, 408, 429, 499, 500, 502, 504, 200, 0}
	cmv2ErrPlatforms = []string{"anthropic", "Anthropic", " openai ", "grok", "composite", "", "gemini", "kimi"}
)

// cmv2Users keeps the key space small enough that minutes share keys, so rollup
// rows really are sums of several 1m rows.
const cmv2Users = 16

func (g *cmv2Gen) pick(n int) int { return g.rng.Intn(n) }

func (g *cmv2Gen) chance(percent int) bool { return g.rng.Intn(100) < percent }

func (g *cmv2Gen) timeIn(minute time.Time) time.Time {
	switch g.pick(20) {
	case 0:
		return minute // first instant of the minute
	case 1:
		return minute.Add(time.Minute - time.Microsecond) // last instant of the minute
	default:
		return minute.Add(time.Duration(g.pick(60))*time.Second + time.Duration(g.pick(1000))*time.Millisecond)
	}
}

func (g *cmv2Gen) usageBatch(n int, minute time.Time) []cmv2UsageRow {
	rows := make([]cmv2UsageRow, 0, n)
	for range n {
		id := g.nextUsageID
		g.nextUsageID++
		row := cmv2UsageRow{
			ID: id, UserID: int64(1 + g.pick(cmv2Users)), APIKeyID: 1, AccountID: int64(1 + g.pick(12)),
			Model: cmv2Models[g.pick(len(cmv2Models))], InputTokens: cmv2Inputs[g.pick(len(cmv2Inputs))],
			OutputTokens: g.pick(2000), CacheCreation: cmv2Creations[g.pick(len(cmv2Creations))],
			CacheRead: cmv2Reads[g.pick(len(cmv2Reads))], Stream: g.chance(88), CreatedAt: g.timeIn(minute),
		}
		if g.chance(70) {
			row.RequestedModel = cmv2Ptr(cmv2RequestedModels[g.pick(len(cmv2RequestedModels))])
		}
		if !g.chance(12) {
			row.ActualCost = float64(1+g.pick(900)) / 1e6
		}
		if g.chance(75) {
			row.GroupID = cmv2Ptr(int64(1 + g.pick(8)))
		}
		if !g.chance(15) {
			row.FirstTokenMS = cmv2Ptr(cmv2Latencies[g.pick(len(cmv2Latencies))])
		}
		if !g.chance(10) {
			row.DurationMS = cmv2Ptr(cmv2Latencies[g.pick(len(cmv2Latencies))])
		}
		switch {
		case g.chance(85):
		case g.chance(40):
			row.RequestType = 4
		case g.chance(40):
			row.RequestType = 6
		default:
			row.RequestType = 1 + g.pick(3)
		}
		switch g.pick(20) {
		case 0:
			row.BillingMode = cmv2Ptr("image")
		case 1, 2:
			row.BillingMode = cmv2Ptr("per_request")
		case 3, 4, 5, 6, 7, 8:
			row.BillingMode = cmv2Ptr("token")
		}
		if g.chance(5) {
			row.ImageCount = cmv2Ptr(1 + g.pick(3))
		} else if g.chance(50) {
			row.ImageCount = cmv2Ptr(0)
		}
		if g.chance(3) {
			row.ImageInput = 1 + g.pick(500)
		}
		if g.chance(3) {
			row.ImageOutput = 1 + g.pick(500)
		}
		switch {
		case g.chance(3):
			// NULL request id: counted once per row.
		case g.chance(2):
			row.RequestID = cmv2Ptr("")
		case g.chance(10) && len(g.requestIDs) > 0:
			row.RequestID = cmv2Ptr(g.requestIDs[g.pick(len(g.requestIDs))])
		default:
			row.RequestID = cmv2Ptr(fmt.Sprintf("req-%d", id))
			g.requestIDs = append(g.requestIDs, *row.RequestID)
			if len(g.requestIDs) > 400 {
				g.requestIDs = g.requestIDs[200:]
			}
		}
		g.usage = append(g.usage, cmv2Live{id: id, created: row.CreatedAt})
		rows = append(rows, row)
	}
	return rows
}

func (g *cmv2Gen) errorBatch(n int, minute time.Time) []cmv2ErrorRow {
	rows := make([]cmv2ErrorRow, 0, n)
	for range n {
		id := g.nextErrorID
		g.nextErrorID++
		row := cmv2ErrorRow{
			ID: id, Stream: g.chance(88), IsCountTokens: g.chance(8), ErrorType: cmv2ErrorTypes[g.pick(len(cmv2ErrorTypes))],
			CreatedAt: g.timeIn(minute),
		}
		switch {
		case g.chance(10):
		case g.chance(25) && len(g.errorReqIDs) > 0:
			// A retry of an earlier failure: the latest row of a request id wins, which
			// can move (or remove) the error from an older minute.
			row.RequestID = cmv2Ptr(g.errorReqIDs[g.pick(len(g.errorReqIDs))])
		default:
			row.RequestID = cmv2Ptr(fmt.Sprintf("err-%d", id))
			g.errorReqIDs = append(g.errorReqIDs, *row.RequestID)
			if len(g.errorReqIDs) > 200 {
				g.errorReqIDs = g.errorReqIDs[100:]
			}
		}
		if !g.chance(20) {
			row.UserID = cmv2Ptr(int64(1 + g.pick(cmv2Users)))
		}
		if !g.chance(15) {
			row.AccountID = cmv2Ptr(int64(1 + g.pick(12)))
		}
		if g.chance(80) {
			row.GroupID = cmv2Ptr(int64(1 + g.pick(8)))
		}
		if g.chance(85) {
			row.Platform = cmv2Ptr(cmv2ErrPlatforms[g.pick(len(cmv2ErrPlatforms))])
		}
		if g.chance(80) {
			row.Model = cmv2Ptr(cmv2Models[g.pick(len(cmv2Models))])
		}
		if g.chance(60) {
			row.RequestedModel = cmv2Ptr(cmv2RequestedModels[g.pick(len(cmv2RequestedModels))])
		}
		switch g.pick(8) {
		case 0:
			row.InboundEndpoint = cmv2Ptr("/v1/images/generations")
		case 1:
			row.RequestPath = cmv2Ptr("/v1/images/edits")
		case 2, 3:
			row.InboundEndpoint = cmv2Ptr("/v1/messages")
		}
		if g.chance(15) {
			row.RequestType = cmv2Ptr([]int{0, 4, 6, 1}[g.pick(4)])
		}
		if !g.chance(8) {
			row.StatusCode = cmv2Ptr(cmv2StatusCodes[g.pick(len(cmv2StatusCodes))])
		}
		if g.chance(30) {
			row.UpstreamStatusCode = cmv2Ptr(cmv2StatusCodes[g.pick(len(cmv2StatusCodes))])
		}
		if g.chance(80) {
			row.ErrorMessage = cmv2Ptr(cmv2Texts[g.pick(len(cmv2Texts))])
		}
		if g.chance(30) {
			row.ErrorBody = cmv2Ptr(cmv2Texts[g.pick(len(cmv2Texts))])
		}
		if g.chance(30) {
			row.UpstreamErrorMessage = cmv2Ptr(cmv2Texts[g.pick(len(cmv2Texts))])
		}
		if g.chance(15) {
			row.UpstreamErrorDetail = cmv2Ptr(cmv2Texts[g.pick(len(cmv2Texts))])
		}
		if g.chance(50) {
			row.ErrorSource = cmv2Ptr([]string{"upstream_http", "gateway", "stream_read_error"}[g.pick(3)])
		}
		if g.chance(70) {
			row.ErrorOwner = cmv2Ptr([]string{"provider", "system", "client"}[g.pick(3)])
		}
		switch g.pick(6) {
		case 0:
			row.UpstreamErrors = json.RawMessage(`[]`)
		case 1:
			row.UpstreamErrors = json.RawMessage(`[{"status":502},{"status":503}]`)
		case 2:
			row.UpstreamErrors = json.RawMessage(`{"not":"an array"}`)
		case 3:
			row.UpstreamErrors = json.RawMessage(`[{"status":500}]`)
		}
		g.errs = append(g.errs, cmv2Live{id: id, created: row.CreatedAt})
		rows = append(rows, row)
	}
	return rows
}

func cmv2JSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return string(raw)
}

func (g *cmv2Gen) usageOp(t *testing.T, rows []cmv2UsageRow) []cmv2Op {
	if len(rows) == 0 {
		return nil
	}
	return []cmv2Op{{query: cmv2InsertUsageSQL, args: []any{cmv2JSON(t, rows)}}}
}

func (g *cmv2Gen) errorOp(t *testing.T, rows []cmv2ErrorRow) []cmv2Op {
	if len(rows) == 0 {
		return nil
	}
	return []cmv2Op{{query: cmv2InsertErrorSQL, args: []any{cmv2JSON(t, rows)}}}
}

// recent returns up to n live rows created within the last `within` before now.
func (g *cmv2Gen) recent(rows []cmv2Live, now time.Time, within time.Duration, n int) []int64 {
	var candidates []int64
	for _, row := range rows {
		if !row.created.Before(now.Add(-within)) {
			candidates = append(candidates, row.id)
		}
	}
	g.rng.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	if len(candidates) > n {
		candidates = candidates[:n]
	}
	return candidates
}

func cmv2Drop(rows []cmv2Live, ids []int64) []cmv2Live {
	gone := make(map[int64]bool, len(ids))
	for _, id := range ids {
		gone[id] = true
	}
	kept := rows[:0]
	for _, row := range rows {
		if !gone[row.id] {
			kept = append(kept, row)
		}
	}
	return kept
}

// historyOps fills a minute that no tick has looked at yet.
func (g *cmv2Gen) historyOps(t *testing.T, minute time.Time, usageRows int) []cmv2Op {
	ops := g.usageOp(t, g.usageBatch(usageRows, minute))
	return append(ops, g.errorOp(t, g.errorBatch(g.pick(3), minute))...)
}

// tickOps is what happens in the wall minute before the tick at `now`: fresh
// traffic for the minute that just ended, late arrivals, and edits to recent rows.
func (g *cmv2Gen) tickOps(t *testing.T, now time.Time) []cmv2Op {
	last := now.Add(-time.Minute)
	ops := g.usageOp(t, g.usageBatch(5+g.pick(16), last))
	ops = append(ops, g.errorOp(t, g.errorBatch(g.pick(5), last))...)
	for range g.pick(4) {
		late := now.Add(-time.Duration(2+g.pick(5)) * time.Minute)
		ops = append(ops, g.usageOp(t, g.usageBatch(1+g.pick(3), late))...)
		ops = append(ops, g.errorOp(t, g.errorBatch(g.pick(2), late))...)
	}
	if g.chance(5) { // far outside the live window: only a catch-up window ever sees it
		ops = append(ops, g.usageOp(t, g.usageBatch(2, now.Add(-time.Duration(10+g.pick(20))*time.Minute)))...)
	}
	if g.chance(15) { // belongs to the minute that has not been aggregated yet
		ops = append(ops, g.usageOp(t, []cmv2UsageRow{g.usageBatch(1, now)[0]})...)
	}

	if g.chance(8) {
		ids := g.recent(g.usage, now, 4*time.Minute, 1+g.pick(5))
		g.usage = cmv2Drop(g.usage, ids)
		ops = append(ops, cmv2Op{`DELETE FROM usage_logs WHERE id = ANY($1)`, []any{pq.Array(ids)}})
	}
	if g.chance(8) {
		ids := g.recent(g.usage, now, 4*time.Minute, 1+g.pick(5))
		ops = append(ops, cmv2Op{`UPDATE usage_logs SET actual_cost = 0 WHERE id = ANY($1)`, []any{pq.Array(ids)}})
	}
	if g.chance(6) {
		ids := g.recent(g.usage, now, 4*time.Minute, 1+g.pick(4))
		ops = append(ops, cmv2Op{`UPDATE usage_logs SET group_id = $2 WHERE id = ANY($1)`, []any{pq.Array(ids), int64(1 + g.pick(8))}})
	}
	if g.chance(6) {
		ids := g.recent(g.usage, now, 4*time.Minute, 1+g.pick(4))
		ops = append(ops, cmv2Op{`UPDATE usage_logs SET requested_model = $2 WHERE id = ANY($1)`, []any{pq.Array(ids), cmv2Models[g.pick(len(cmv2Models))]}})
	}
	if g.chance(4) {
		ids := g.recent(g.usage, now, 4*time.Minute, 1+g.pick(4))
		ops = append(ops, cmv2Op{`UPDATE usage_logs SET first_token_ms = $2, duration_ms = $3 WHERE id = ANY($1)`,
			[]any{pq.Array(ids), cmv2Latencies[g.pick(len(cmv2Latencies))], cmv2Latencies[g.pick(len(cmv2Latencies))]}})
	}
	if g.chance(3) { // a whole minute vanishes, so its 1m rows must too
		minute := last.Add(-time.Duration(g.pick(3)) * time.Minute)
		var ids []int64
		for _, row := range g.usage {
			if !row.created.Before(minute) && row.created.Before(minute.Add(time.Minute)) {
				ids = append(ids, row.id)
			}
		}
		g.usage = cmv2Drop(g.usage, ids)
		ops = append(ops, cmv2Op{`DELETE FROM usage_logs WHERE id = ANY($1)`, []any{pq.Array(ids)}})
	}
	if g.chance(5) {
		ids := g.recent(g.errs, now, 6*time.Minute, 1+g.pick(2))
		g.errs = cmv2Drop(g.errs, ids)
		ops = append(ops, cmv2Op{`DELETE FROM ops_error_logs WHERE id = ANY($1)`, []any{pq.Array(ids)}})
	}
	if g.chance(5) {
		ids := g.recent(g.errs, now, 6*time.Minute, 1+g.pick(2))
		ops = append(ops, cmv2Op{`UPDATE ops_error_logs SET status_code = $2 WHERE id = ANY($1)`,
			[]any{pq.Array(ids), []int{200, 429, 500}[g.pick(3)]}})
	}
	return ops
}

func cmv2Apply(t *testing.T, ops []cmv2Op, sides ...*cmv2Side) {
	t.Helper()
	if len(ops) == 0 {
		return
	}
	for _, side := range sides {
		tx, err := side.db.BeginTx(context.Background(), nil)
		require.NoError(t, err)
		for _, op := range ops {
			_, err = tx.ExecContext(context.Background(), op.query, op.args...)
			require.NoError(t, err, "%s: %s", side.name, op.query)
		}
		require.NoError(t, tx.Commit())
	}
}

// ---- scenarios ----

type cmv2Scenario struct {
	name     string
	seed     int64
	timeZone string
	// start is the `now` of the bootstrap pass; live tick i runs at start + (i+1) minutes.
	start   time.Time
	history time.Duration
	ticks   int
	// skip: ticks that never run (the aggregator missed them) but whose traffic still arrives.
	skip map[int]bool
	// restart: process restarts before the tick (in-memory state is lost).
	restart map[int]bool
	// rewind: operator moves data_through back by this much before the tick.
	rewind map[int]time.Duration
}

func cmv2Day(daysAgo int) time.Time {
	return time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -daysAgo)
}

func runCMV2Scenario(t *testing.T, sc cmv2Scenario) {
	t.Helper()
	ctx := context.Background()
	legacy, fresh := newCMV2Pair(t, sc.timeZone)
	columns := cmv2Columns(t, legacy)
	gen := newCMV2Gen(sc.seed)

	for minute := sc.start.Add(-sc.history); minute.Before(sc.start); minute = minute.Add(time.Minute) {
		cmv2Apply(t, gen.historyOps(t, minute, 2+gen.pick(5)), legacy, fresh)
	}
	require.NoError(t, legacy.impl.RecomputeRange(ctx, sc.start.Add(-2*time.Hour), sc.start))
	require.NoError(t, fresh.impl.RecomputeRange(ctx, sc.start.Add(-2*time.Hour), sc.start))
	requireSidesEqual(t, legacy, fresh, columns, sc.name+": bootstrap", time.Time{})

	ran := 0
	for i := 0; i < sc.ticks; i++ {
		now := sc.start.Add(time.Duration(i+1) * time.Minute)
		cmv2Apply(t, gen.tickOps(t, now), legacy, fresh)
		if sc.skip[i] {
			continue
		}
		if sc.restart[i] {
			legacy.restart()
			fresh.restart()
		}
		if back, ok := sc.rewind[i]; ok {
			for _, side := range []*cmv2Side{legacy, fresh} {
				side.exec(t, `UPDATE channel_monitor_v2_watermarks SET data_through = data_through - $1::interval WHERE id = 1`,
					fmt.Sprintf("%d seconds", int(back.Seconds())))
			}
		}
		legacy.liveTick(t, now)
		fresh.liveTick(t, now)
		ran++
		// Per tick, compare what a live tick can touch; every 12th tick, after anything
		// unusual (restart, rewind, catch-up) and at the end, compare every row.
		since := now.Add(-4 * time.Hour)
		if _, rewound := sc.rewind[i]; i%12 == 11 || i == sc.ticks-1 || sc.restart[i] || rewound || (i > 0 && sc.skip[i-1]) {
			since = time.Time{}
		}
		requireSidesEqual(t, legacy, fresh, columns, fmt.Sprintf("%s: tick %d (%s)", sc.name, i, now.Format("01-02 15:04")), since)
	}
	t.Logf("%s: %d live ticks compared (seed %d, session time zone %s)", sc.name, ran, sc.seed, sc.timeZone)
	cmv2LogSizes(t, fresh)
}

func cmv2LogSizes(t *testing.T, side *cmv2Side) {
	t.Helper()
	var parts []string
	for _, table := range cmv2DiffTables {
		var n int
		require.NoError(t, side.db.QueryRowContext(context.Background(), "SELECT count(*) FROM "+table).Scan(&n))
		parts = append(parts, fmt.Sprintf("%s=%d", strings.TrimPrefix(table, "channel_monitor_v2_"), n))
	}
	t.Logf("rows: %s", strings.Join(parts, " "))
}

// Crosses the 5m, 1h, 12h and 1d boundaries (00:00 UTC) in the UTC session.
func TestChannelMonitorV2AggregationMatchesDeleteInsertAcrossBoundaries(t *testing.T) {
	runCMV2Scenario(t, cmv2Scenario{
		name: "utc-midnight", seed: 20261125, timeZone: "UTC",
		start: cmv2Day(2).Add(22 * time.Hour), history: 125 * time.Minute, ticks: 170,
	})
}

// date_bin's origin must stay UTC when the database session is not: Asia/Shanghai
// puts 12:00 UTC at 20:00 local, and the 5m/1h/12h buckets must not move.
func TestChannelMonitorV2AggregationMatchesDeleteInsertInShanghaiSession(t *testing.T) {
	runCMV2Scenario(t, cmv2Scenario{
		name: "shanghai-noon", seed: 7, timeZone: "Asia/Shanghai",
		start: cmv2Day(2).Add(11*time.Hour + 20*time.Minute), history: 125 * time.Minute, ticks: 100,
	})
}

// Missed ticks, a process restart and an operator rewinding the watermark must
// all end up with the same rows as the delete + insert version.
func TestChannelMonitorV2AggregationMatchesDeleteInsertAfterRestartGapAndRewind(t *testing.T) {
	skip := map[int]bool{}
	for i := 40; i < 47; i++ {
		skip[i] = true
	}
	for i := 95; i < 98; i++ {
		skip[i] = true
	}
	runCMV2Scenario(t, cmv2Scenario{
		name: "restart-gap-rewind", seed: 99, timeZone: "UTC",
		start: cmv2Day(2).Add(10*time.Hour + 35*time.Minute), history: 125 * time.Minute, ticks: 130,
		skip:    skip,
		restart: map[int]bool{47: true, 98: true},
		rewind:  map[int]time.Duration{70: 45 * time.Minute, 120: 3 * time.Hour},
	})
}

// RecomputeRange: the 2h bootstrap, then historical chunks of every awkward shape
// (inside one bucket, exactly one bucket, straddling 5m/1h/12h/1d boundaries).
func TestChannelMonitorV2AggregationMatchesDeleteInsertForBackfillRanges(t *testing.T) {
	ctx := context.Background()
	legacy, fresh := newCMV2Pair(t, "UTC")
	columns := cmv2Columns(t, legacy)
	gen := newCMV2Gen(424242)

	day := cmv2Day(2)
	from, to := day.Add(-6*time.Hour), day.Add(31*time.Hour)
	for minute := from; minute.Before(to); minute = minute.Add(time.Minute) {
		cmv2Apply(t, gen.historyOps(t, minute, 1+gen.pick(4)), legacy, fresh)
	}
	now := day.Add(30 * time.Hour)
	for _, rng := range [][2]time.Time{
		{now.Add(-2 * time.Hour), now}, // bootstrap
		{day.Add(27 * time.Hour), day.Add(28 * time.Hour)},
		{day.Add(24 * time.Hour), day.Add(27 * time.Hour)},                              // exactly starts a day, inside one 12h/1d bucket
		{day.Add(12 * time.Hour), day.Add(24 * time.Hour)},                              // exactly one 12h bucket
		{day, day.Add(24 * time.Hour)},                                                  // exactly one day
		{day.Add(23 * time.Hour), day.Add(25 * time.Hour)},                              // straddles midnight
		{day.Add(11*time.Hour + 58*time.Minute), day.Add(12*time.Hour + 2*time.Minute)}, // straddles noon
		{day.Add(3 * time.Hour), day.Add(9 * time.Hour)},                                // inside one bucket
		{day.Add(10*time.Hour + 1*time.Minute), day.Add(10*time.Hour + 2*time.Minute)},  // one minute
		{day.Add(-5 * time.Hour), day.Add(-1 * time.Hour)},
		{day.Add(-2 * time.Hour), day.Add(2 * time.Hour)}, // straddles midnight, bigger
	} {
		require.NoError(t, legacy.impl.RecomputeRange(ctx, rng[0], rng[1]))
		require.NoError(t, fresh.impl.RecomputeRange(ctx, rng[0], rng[1]))
		requireSidesEqual(t, legacy, fresh, columns, fmt.Sprintf("backfill %s..%s", rng[0].Format("01-02 15:04"), rng[1].Format("01-02 15:04")), time.Time{})
	}
	// Then the live path takes over from the freshly built history.
	for i := 1; i <= 30; i++ {
		tick := now.Add(time.Duration(i) * time.Minute)
		cmv2Apply(t, gen.tickOps(t, tick), legacy, fresh)
		legacy.liveTick(t, tick)
		fresh.liveTick(t, tick)
		since := tick.Add(-4 * time.Hour)
		if i%10 == 0 || i == 30 {
			since = time.Time{}
		}
		requireSidesEqual(t, legacy, fresh, columns, fmt.Sprintf("live after backfill, tick %d", i), since)
	}
	cmv2LogSizes(t, fresh)
}

// The sync names its columns explicitly. Compared with the schema the migrations
// really produce (ALTER TABLEs included), a column it does not know would silently
// stop being refreshed.
func TestChannelMonitorV2SyncTablesCoverEveryColumnOfTheRealSchema(t *testing.T) {
	ctx := context.Background()
	for _, table := range allChannelMonitorV2SyncTables() {
		rows, err := integrationDB.QueryContext(ctx, `
			SELECT column_name FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1`, table.name)
		require.NoError(t, err)
		var columns []string
		for rows.Next() {
			var col string
			require.NoError(t, rows.Scan(&col))
			columns = append(columns, col)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		want := append(append([]string{}, table.keys...), table.values...)
		if table.computedAt {
			want = append(want, "computed_at")
		}
		require.ElementsMatch(t, want, columns, table.name)

		pkRows, err := integrationDB.QueryContext(ctx, `
			SELECT a.attname
			FROM pg_index i
			JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, pos) ON TRUE
			JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
			WHERE i.indrelid = ('public.' || $1)::regclass AND i.indisprimary
			ORDER BY k.pos`, table.name)
		require.NoError(t, err)
		var primaryKey []string
		for pkRows.Next() {
			var col string
			require.NoError(t, pkRows.Scan(&col))
			primaryKey = append(primaryKey, col)
		}
		require.NoError(t, pkRows.Err())
		require.NoError(t, pkRows.Close())
		require.Equal(t, table.keys, primaryKey, "%s: keys must be the primary key in index order (ON CONFLICT target)", table.name)
	}
}
