package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// ---- a recording database/sql driver --------------------------------------
//
// The batch writer's contract is about statements and transactions (how many
// there are, what runs inside which one), so the tests use a driver that records
// every statement in order instead of one that has to be told what to expect.

const (
	connTraceFakeDriverName = "sub2api_conn_trace_fake"
	connTraceSetLocalSQL    = "SET LOCAL synchronous_commit = off"
	connTraceInsertPrefix   = "INSERT INTO request_connection_traces"
)

var (
	connTraceFakeRegister sync.Once
	connTraceFakes        sync.Map // dsn -> *connTraceFake
	connTraceFakeSeq      atomic.Int64
)

type connTraceStmt struct {
	Kind  string // begin, exec, commit, rollback
	Query string
	Args  []driver.Value
	Tx    int64 // transaction the statement ran in; 0 outside one
}

type connTraceFake struct {
	mu          sync.Mutex
	stmts       []connTraceStmt
	nextTx      int64
	hook        func(query string, args []driver.Value) error // runs after an Exec is recorded
	execCtxHook func(ctx context.Context, query string, args []driver.Value) error
	queryHook   func(ctx context.Context, query string, args []driver.Value) ([][]driver.Value, error)
}

func newConnTraceDB(t *testing.T) (*sql.DB, *connTraceFake) {
	t.Helper()
	connTraceFakeRegister.Do(func() { sql.Register(connTraceFakeDriverName, connTraceFakeDriver{}) })
	fake := &connTraceFake{}
	dsn := "fake-" + strconv.FormatInt(connTraceFakeSeq.Add(1), 10)
	connTraceFakes.Store(dsn, fake)
	db, err := sql.Open(connTraceFakeDriverName, dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close(); connTraceFakes.Delete(dsn) })
	return db, fake
}

func (f *connTraceFake) record(s connTraceStmt) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stmts = append(f.stmts, s)
}

func (f *connTraceFake) setHook(h func(query string, args []driver.Value) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hook = h
}

func (f *connTraceFake) setExecCtxHook(h func(ctx context.Context, query string, args []driver.Value) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execCtxHook = h
}

func (f *connTraceFake) setQueryHook(h func(ctx context.Context, query string, args []driver.Value) ([][]driver.Value, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queryHook = h
}

func (f *connTraceFake) statements() []connTraceStmt {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]connTraceStmt(nil), f.stmts...)
}

func connTraceKind(s connTraceStmt) string {
	switch {
	case s.Kind != "exec":
		return s.Kind
	case s.Query == connTraceSetLocalSQL:
		return "set"
	case strings.HasPrefix(s.Query, connTraceInsertPrefix):
		return "insert"
	default:
		return "exec"
	}
}

// sequence is the statement log reduced to begin / set / insert / commit / rollback.
func (f *connTraceFake) sequence() []string {
	var out []string
	for _, s := range f.statements() {
		out = append(out, connTraceKind(s))
	}
	return out
}

func (f *connTraceFake) count(kind string) int {
	n := 0
	for _, s := range f.statements() {
		if connTraceKind(s) == kind {
			n++
		}
	}
	return n
}

// connTraceIDs reads the trace_id (first of the 22 columns) of every row of an INSERT.
func connTraceIDs(args []driver.Value) []string {
	var ids []string
	for i := 0; i < len(args); i += connectionTraceColumnCount {
		ids = append(ids, fmt.Sprint(args[i]))
	}
	return ids
}

// committedTraceIDs lists, in order, the trace ids whose INSERT sat in a
// transaction that committed; rolled-back inserts do not count.
func (f *connTraceFake) committedTraceIDs() []string {
	pending := map[int64][]string{}
	var out []string
	for _, s := range f.statements() {
		switch connTraceKind(s) {
		case "insert":
			pending[s.Tx] = append(pending[s.Tx], connTraceIDs(s.Args)...)
		case "commit":
			out = append(out, pending[s.Tx]...)
			delete(pending, s.Tx)
		case "rollback":
			delete(pending, s.Tx)
		}
	}
	return out
}

type connTraceFakeDriver struct{}

func (connTraceFakeDriver) Open(dsn string) (driver.Conn, error) {
	loaded, _ := connTraceFakes.Load(dsn)
	fake, ok := loaded.(*connTraceFake)
	if !ok {
		return nil, errors.New("unknown fake database")
	}
	return &connTraceFakeConn{fake: fake}, nil
}

type connTraceFakeConn struct {
	fake *connTraceFake
	tx   int64
}

func (c *connTraceFakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are not used on this path")
}
func (c *connTraceFakeConn) Close() error { return nil }
func (c *connTraceFakeConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *connTraceFakeConn) BeginTx(ctx context.Context, _ driver.TxOptions) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.fake.mu.Lock()
	c.fake.nextTx++
	c.tx = c.fake.nextTx
	c.fake.mu.Unlock()
	c.fake.record(connTraceStmt{Kind: "begin", Tx: c.tx})
	return &connTraceFakeTx{conn: c, id: c.tx}, nil
}
func (c *connTraceFakeConn) ExecContext(ctx context.Context, query string, named []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	args := make([]driver.Value, len(named))
	for i, n := range named {
		args[i] = n.Value
	}
	c.fake.record(connTraceStmt{Kind: "exec", Query: query, Args: args, Tx: c.tx})
	c.fake.mu.Lock()
	hook := c.fake.hook
	execCtxHook := c.fake.execCtxHook
	c.fake.mu.Unlock()
	if hook != nil {
		if err := hook(query, args); err != nil {
			return nil, err
		}
	}
	if execCtxHook != nil {
		if err := execCtxHook(ctx, query, args); err != nil {
			return nil, err
		}
	}
	return driver.RowsAffected(1), nil
}

func (c *connTraceFakeConn) QueryContext(ctx context.Context, query string, named []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	args := make([]driver.Value, len(named))
	for i, n := range named {
		args[i] = n.Value
	}
	c.fake.mu.Lock()
	queryHook := c.fake.queryHook
	c.fake.mu.Unlock()
	var rows [][]driver.Value
	if queryHook != nil {
		var err error
		rows, err = queryHook(ctx, query, args)
		if err != nil {
			return nil, err
		}
	}
	return &connTraceFakeRows{rows: rows}, nil
}

type connTraceFakeRows struct {
	rows [][]driver.Value
	idx  int
}

func (r *connTraceFakeRows) Columns() []string {
	if len(r.rows) > 0 {
		cols := make([]string, len(r.rows[0]))
		for i := range cols {
			cols[i] = fmt.Sprintf("col%d", i)
		}
		return cols
	}
	return []string{"col0"}
}
func (r *connTraceFakeRows) Close() error { return nil }
func (r *connTraceFakeRows) Next(dest []driver.Value) error {
	if r.idx >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.idx])
	r.idx++
	return nil
}

type connTraceFakeTx struct {
	conn *connTraceFakeConn
	id   int64
}

func (t *connTraceFakeTx) Commit() error {
	t.conn.fake.record(connTraceStmt{Kind: "commit", Tx: t.id})
	t.conn.tx = 0
	return nil
}
func (t *connTraceFakeTx) Rollback() error {
	t.conn.fake.record(connTraceStmt{Kind: "rollback", Tx: t.id})
	t.conn.tx = 0
	return nil
}

// ---- fixtures ---------------------------------------------------------------

func connTraceFixture(i int) service.ConnectionTrace {
	id := uuid.NewString()
	started := time.Date(2026, 11, 25, 8, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Millisecond)
	return service.ConnectionTrace{
		TraceID:         id,
		RequestID:       "req-" + strconv.Itoa(i),
		ClientRequestID: "client-" + strconv.Itoa(i),
		UserID:          int64(i + 1),
		APIKeyID:        int64(i + 2),
		AccountID:       int64(i + 3),
		GroupID:         int64(i + 4),
		Model:           "test-model",
		Path:            "/v1/responses",
		ClientIP:        "203.0.113.7",
		UserAgent:       "test-agent/1.0",
		Stream:          i%2 == 0,
		StartedAt:       started,
		Source:          "application",
		Host:            "gateway.example.com",
		Scheme:          "https",
		Method:          "POST",
		HTTPVersion:     "HTTP/2.0",
		TLSVersion:      "TLS 1.3",
		UpstreamStatus:  "200",
		Detail:          requesttiming.Snapshot{Version: 1, TraceID: id, StartedAt: started, Status: 200, Events: map[string]float64{"first_byte": 12.5}},
	}
}

func connTraceFixtures(n int) []service.ConnectionTrace {
	out := make([]service.ConnectionTrace, n)
	for i := range out {
		out[i] = connTraceFixture(i)
	}
	return out
}

func connTraceIDsOf(records []service.ConnectionTrace) []string {
	ids := make([]string, len(records))
	for i, r := range records {
		ids[i] = r.TraceID
	}
	return ids
}

func newConnTraceRepo(t *testing.T) (*usageLogRepository, *connTraceFake) {
	t.Helper()
	db, fake := newConnTraceDB(t)
	repo := newUsageLogRepositoryWithSQL(nil, db)
	t.Cleanup(repo.StopConnectionDiagnostics)
	return repo, fake
}

// connTraceBlocker holds the first trace INSERT open so a test can pile traces
// into the queue behind a writer that is busy.
type connTraceBlocker struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	unblock sync.Once
}

func newConnTraceBlocker(t *testing.T, fake *connTraceFake) *connTraceBlocker {
	b := &connTraceBlocker{entered: make(chan struct{}), release: make(chan struct{})}
	fake.setHook(func(query string, _ []driver.Value) error {
		if strings.HasPrefix(query, connTraceInsertPrefix) {
			b.once.Do(func() { close(b.entered); <-b.release })
		}
		return nil
	})
	t.Cleanup(b.Release)
	return b
}

func (b *connTraceBlocker) Release() { b.unblock.Do(func() { close(b.release) }) }

func (b *connTraceBlocker) WaitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-b.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("writer never reached the database")
	}
}

// ---- statement shape --------------------------------------------------------

// The statement the writer used before batching, verbatim. A one-row batch must
// stay byte-identical to it.
const legacyConnectionTraceInsert = `INSERT INTO request_connection_traces
  (trace_id,started_at,request_id,client_request_id,user_id,api_key_id,account_id,group_id,model,path,client_ip,user_agent,stream,detail,source,host,scheme,method,http_version,tls_version,upstream_status,network_error)
  VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb,$15,$16,$17,$18,$19,$20,$21,$22)
  ON CONFLICT (trace_id) DO NOTHING`

func TestConnectionTraceInsertSQL_OneRowIsTheLegacyStatement(t *testing.T) {
	require.Equal(t, legacyConnectionTraceInsert, connectionTraceInsertSQL(1))
}

func TestConnectionTraceInsertSQL_ManyRowsKeepPlaceholderOrder(t *testing.T) {
	for _, rows := range []int{2, 3, connectionTraceBatchMax} {
		query := connectionTraceInsertSQL(rows)
		var got []int
		for _, m := range regexp.MustCompile(`\$(\d+)`).FindAllStringSubmatch(query, -1) {
			n, err := strconv.Atoi(m[1])
			require.NoError(t, err)
			got = append(got, n)
		}
		require.Len(t, got, rows*connectionTraceColumnCount)
		for i, n := range got {
			require.Equal(t, i+1, n, "placeholders must run $1..$N in order (rows=%d)", rows)
		}
		require.Equal(t, rows, strings.Count(query, "::jsonb"), "one jsonb cast per row")
		require.Equal(t, 1, strings.Count(query, "ON CONFLICT (trace_id) DO NOTHING"))
		require.Equal(t, 1, strings.Count(query, connTraceInsertPrefix))
		require.True(t, strings.HasSuffix(query, "ON CONFLICT (trace_id) DO NOTHING"))
	}
}

// ---- one transaction per batch ----------------------------------------------

func TestConnectionTraceBatch_SingleRecordIsOneAsyncCommitTransaction(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	rec := connTraceFixture(7)
	repo.flushConnectionBatch(context.Background(), []service.ConnectionTrace{rec})

	require.Equal(t, []string{"begin", "set", "insert", "commit"}, fake.sequence())
	stmts := fake.statements()
	require.Equal(t, connTraceSetLocalSQL, stmts[1].Query)
	require.Equal(t, legacyConnectionTraceInsert, stmts[2].Query)
	require.Equal(t, stmts[0].Tx, stmts[1].Tx, "SET LOCAL must run inside the batch transaction")
	require.Equal(t, stmts[0].Tx, stmts[2].Tx)

	// Every argument is exactly what the one-row-per-transaction writer passed.
	detail, err := json.Marshal(rec.Detail)
	require.NoError(t, err)
	require.Equal(t, []driver.Value{
		rec.TraceID, rec.StartedAt, rec.RequestID, rec.ClientRequestID, rec.UserID, rec.APIKeyID, rec.AccountID, rec.GroupID,
		rec.Model, rec.Path, rec.ClientIP, rec.UserAgent, rec.Stream, string(detail), rec.Source, rec.Host,
		"", rec.Method, "", "", rec.UpstreamStatus, rec.NetworkError,
	}, stmts[2].Args)
}

func TestConnectionTraceBatch_FullBatchIsOneStatement(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	records := connTraceFixtures(connectionTraceBatchMax)
	repo.flushConnectionBatch(context.Background(), records)

	require.Equal(t, []string{"begin", "set", "insert", "commit"}, fake.sequence())
	require.Equal(t, connTraceIDsOf(records), fake.committedTraceIDs(), "all rows, in queue order")
	require.Len(t, fake.statements()[2].Args, connectionTraceBatchMax*connectionTraceColumnCount)
	require.Zero(t, repo.connectionWriteDrops.total.Load())
}

func TestConnectionTraceBatch_OversizeRecordIsDroppedAlone(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	records := connTraceFixtures(3)
	records[1].Detail.IdentitySource = strings.Repeat("x", connectionTraceMaxDetailBytes)
	repo.flushConnectionBatch(context.Background(), records)

	require.Equal(t, []string{"begin", "set", "insert", "commit"}, fake.sequence(), "one batch, not a retry storm")
	require.Equal(t, []string{records[0].TraceID, records[2].TraceID}, fake.committedTraceIDs())
	require.EqualValues(t, 1, repo.connectionWriteDrops.total.Load())
}

func TestConnectionTraceBatch_OnlyOversizeRecordsMeansNoStatement(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	records := connTraceFixtures(2)
	for i := range records {
		records[i].Detail.IdentitySource = strings.Repeat("x", connectionTraceMaxDetailBytes)
	}
	repo.flushConnectionBatch(context.Background(), records)

	require.Empty(t, fake.statements())
	require.EqualValues(t, 2, repo.connectionWriteDrops.total.Load())
}

func TestConnectionTraceBatch_DataErrorFallsBackToSingleRows(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	records := connTraceFixtures(4)
	bad := records[2].TraceID
	fake.setHook(func(query string, args []driver.Value) error {
		if !strings.HasPrefix(query, connTraceInsertPrefix) {
			return nil
		}
		ids := connTraceIDs(args)
		if len(ids) > 1 || ids[0] == bad {
			return &pq.Error{Code: "22001", Message: "value too long"}
		}
		return nil
	})
	repo.flushConnectionBatch(context.Background(), records)

	// One failed batch, then one transaction per row; the bad row alone is lost.
	require.Equal(t, 1+len(records), fake.count("insert"), "a data error is not retried as a batch")
	require.Equal(t, []string{records[0].TraceID, records[1].TraceID, records[3].TraceID}, fake.committedTraceIDs())
	require.EqualValues(t, 1, repo.connectionWriteDrops.total.Load())
	require.Equal(t, 1+len(records), fake.count("begin"))
	require.Equal(t, 1+len(records), fake.count("set"), "every write, single rows included, is async-commit")
}

func TestConnectionTraceBatch_TransientErrorRetriesTheBatchThenGivesUp(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	fake.setHook(func(query string, _ []driver.Value) error {
		if strings.HasPrefix(query, connTraceInsertPrefix) {
			return &pq.Error{Code: "40001", Message: "could not serialize access"}
		}
		return nil
	})
	records := connTraceFixtures(3)
	repo.flushConnectionBatch(context.Background(), records)

	require.Equal(t, connectionTraceWriteAttempts, fake.count("insert"), "same attempt budget as before, applied to the batch")
	for _, s := range fake.statements() {
		if connTraceKind(s) == "insert" {
			require.Len(t, s.Args, len(records)*connectionTraceColumnCount, "no row-by-row fallback for a transient failure")
		}
	}
	require.Empty(t, fake.committedTraceIDs())
	require.EqualValues(t, len(records), repo.connectionWriteDrops.total.Load())
}

func TestConnectionTraceBatch_TransientErrorRecoversOnRetry(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	var failed atomic.Bool
	fake.setHook(func(query string, _ []driver.Value) error {
		if strings.HasPrefix(query, connTraceInsertPrefix) && failed.CompareAndSwap(false, true) {
			return errors.New("connection reset")
		}
		return nil
	})
	records := connTraceFixtures(3)
	repo.flushConnectionBatch(context.Background(), records)

	require.Equal(t, 2, fake.count("insert"))
	require.Equal(t, connTraceIDsOf(records), fake.committedTraceIDs(), "each trace is stored once")
	require.Zero(t, repo.connectionWriteDrops.total.Load())
}

func TestConnectionTraceBatch_DatabaseLostDuringFallbackStopsTheFallback(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	fake.setHook(func(query string, args []driver.Value) error {
		if !strings.HasPrefix(query, connTraceInsertPrefix) {
			return nil
		}
		if len(connTraceIDs(args)) > 1 {
			return &pq.Error{Code: "22P02", Message: "invalid input syntax"}
		}
		return &pq.Error{Code: "57P01", Message: "terminating connection"}
	})
	records := connTraceFixtures(5)
	repo.flushConnectionBatch(context.Background(), records)

	// The batch, then the first row's attempt budget; the other four are not tried.
	require.Equal(t, 1+connectionTraceWriteAttempts, fake.count("insert"))
	require.Empty(t, fake.committedTraceIDs())
	require.EqualValues(t, len(records), repo.connectionWriteDrops.total.Load())
}

func TestConnectionTraceBatch_FinishedContextGivesUpImmediately(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	repo.flushConnectionBatch(ctx, connTraceFixtures(3))

	require.Empty(t, fake.statements())
	require.EqualValues(t, 3, repo.connectionWriteDrops.total.Load())
}

// ---- the writer loop ---------------------------------------------------------

// 1000 queued traces used to be 1000 INSERT statements in 1000 transactions.
func TestConnectionTraceWriter_ThousandTracesAreAFewStatements(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	blocker := newConnTraceBlocker(t, fake)
	records := connTraceFixtures(1000)

	repo.RecordConnectionTrace(records[0])
	blocker.WaitEntered(t) // the writer is busy; the rest piles up behind it
	for _, rec := range records[1:] {
		repo.RecordConnectionTrace(rec)
	}
	blocker.Release()
	require.Eventually(t, func() bool { return len(fake.committedTraceIDs()) == len(records) }, 10*time.Second, 5*time.Millisecond)
	repo.StopConnectionDiagnostics()

	inserts := fake.count("insert")
	t.Logf("1000 traces -> %d INSERT statements, %d statements in total (BEGIN/SET LOCAL/INSERT/COMMIT)", inserts, len(fake.statements()))
	require.LessOrEqual(t, inserts, 20)
	require.LessOrEqual(t, len(fake.statements()), 20+3*20, "four round trips per batch, never per trace")
	require.Equal(t, inserts, fake.count("begin"))
	require.Equal(t, inserts, fake.count("set"))
	require.Equal(t, inserts, fake.count("commit"))
	require.Equal(t, connTraceIDsOf(records), fake.committedTraceIDs(), "every trace stored once, in order")
	require.Zero(t, repo.connectionQueueDrops.total.Load()+repo.connectionWriteDrops.total.Load())
}

func TestConnectionTraceWriter_QueueFullDropsWithoutBlocking(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	blocker := newConnTraceBlocker(t, fake)
	const extra = 50
	records := connTraceFixtures(1 + connectionTraceQueueCap + extra)

	repo.RecordConnectionTrace(records[0])
	blocker.WaitEntered(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, rec := range records[1:] {
			repo.RecordConnectionTrace(rec)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RecordConnectionTrace blocked on a full queue")
	}

	require.EqualValues(t, extra, repo.connectionQueueDrops.total.Load(), "only the overflow is dropped")
	require.Equal(t, connectionTraceQueueCap, len(repo.connectionQueue), "the queue kept everything that fit")

	blocker.Release()
	repo.StopConnectionDiagnostics()
	require.Equal(t, connTraceIDsOf(records[:1+connectionTraceQueueCap]), fake.committedTraceIDs(), "queued traces are written, overflow is not")
}

func TestConnectionTraceDropCounter_LogsOncePerInterval(t *testing.T) {
	var c connectionDropCounter
	t0 := time.Unix(1_000_000, 0)

	n, ok := c.add(1, t0)
	require.True(t, ok, "the first drop is reported at once")
	require.EqualValues(t, 1, n)

	for i := 1; i < 1000; i++ {
		_, ok = c.add(1, t0.Add(time.Duration(i)*time.Millisecond))
		require.False(t, ok, "drops inside the interval are only counted")
	}

	n, ok = c.add(1, t0.Add(connectionTraceLogInterval))
	require.True(t, ok)
	require.EqualValues(t, 1000, n, "the next line carries everything dropped since the last one")
	require.EqualValues(t, 1001, c.total.Load())

	_, ok = c.add(3, t0.Add(connectionTraceLogInterval+time.Second))
	require.False(t, ok)
	n, ok = c.add(1, t0.Add(2*connectionTraceLogInterval))
	require.True(t, ok)
	require.EqualValues(t, 4, n)
}

func TestConnectionTraceDropCounter_ConcurrentLoggersAreRateLimited(t *testing.T) {
	var c connectionDropCounter
	now := time.Unix(1_000_000, 0)
	var logged, reported atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				if n, ok := c.add(1, now); ok {
					logged.Add(1)
					reported.Add(n)
				}
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, logged.Load(), "one line for 16000 drops inside one interval")
	require.EqualValues(t, 16000, c.total.Load())
	// The winning goroutine reports every drop counted by the time it reads the
	// total, and other goroutines add before that, so the first line can carry
	// more than one drop (a real CI run saw 2). What must hold is that nothing is
	// lost: the next line carries whatever the first one did not.
	first := reported.Load()
	require.GreaterOrEqual(t, first, int64(1))
	rest, ok := c.add(0, now.Add(connectionTraceLogInterval))
	require.True(t, ok)
	require.EqualValues(t, 16000, first+rest, "the two lines together account for every drop")
}

func TestConnectionTraceWriter_StopFlushesWhatIsQueued(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	blocker := newConnTraceBlocker(t, fake)
	records := connTraceFixtures(301)

	repo.RecordConnectionTrace(records[0])
	blocker.WaitEntered(t)
	for _, rec := range records[1:] {
		repo.RecordConnectionTrace(rec)
	}

	stopped := make(chan struct{})
	go func() { repo.StopConnectionDiagnostics(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned while queued traces were still unwritten")
	case <-time.After(100 * time.Millisecond):
	}
	blocker.Release()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop never returned")
	}
	require.Equal(t, connTraceIDsOf(records), fake.committedTraceIDs(), "the queue was flushed before Stop returned")

	// Nothing is accepted once stopped.
	before := len(fake.statements())
	repo.RecordConnectionTrace(connTraceFixture(999))
	require.Zero(t, len(repo.connectionQueue))
	time.Sleep(3 * connectionTraceBatchWindow)
	require.Len(t, fake.statements(), before)
}

func TestConnectionTraceWriter_StopWithEmptyQueueReturnsPromptly(t *testing.T) {
	repo, _ := newConnTraceRepo(t)
	repo.RecordConnectionTrace(connTraceFixture(1))
	start := time.Now()
	repo.StopConnectionDiagnostics()
	require.Less(t, time.Since(start), time.Second)
	repo.StopConnectionDiagnostics() // a second stop is a no-op
}

func TestConnectionTraceWriter_StopIsBounded(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	previous := connectionTraceShutdownBudget
	connectionTraceShutdownBudget = 100 * time.Millisecond
	t.Cleanup(func() { connectionTraceShutdownBudget = previous })
	blocker := newConnTraceBlocker(t, fake)
	// Cleanups run last-in first: release the database, wait for the writer to
	// leave, and only then put the shutdown budget back.
	t.Cleanup(func() { blocker.Release(); <-repo.connectionExited })

	repo.RecordConnectionTrace(connTraceFixture(1))
	blocker.WaitEntered(t)
	start := time.Now()
	repo.StopConnectionDiagnostics() // the database never answers
	require.Less(t, time.Since(start), 3*time.Second, "Stop must not wait on a stuck database")
}

func TestConnectionTraceWriter_ConcurrentProducersLoseNothing(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	const producers, each = 8, 500 // 4000 traces fit the queue even if the writer never ran
	records := connTraceFixtures(producers * each)

	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(batch []service.ConnectionTrace) {
			defer wg.Done()
			for _, rec := range batch {
				repo.RecordConnectionTrace(rec)
			}
		}(records[p*each : (p+1)*each])
	}
	wg.Wait()
	repo.StopConnectionDiagnostics()

	got := fake.committedTraceIDs()
	require.ElementsMatch(t, connTraceIDsOf(records), got)
	require.Len(t, got, len(records), "no trace is written twice")
	require.Zero(t, repo.connectionQueueDrops.total.Load()+repo.connectionWriteDrops.total.Load())
	require.Less(t, fake.count("insert"), len(records)/10, "batching, not one statement per trace")
}

func TestConnectionTraceWriter_RepositoryWithoutPoolIgnoresTraces(t *testing.T) {
	db, fake := newConnTraceDB(t)
	repo := &usageLogRepository{sql: db} // transaction-scoped repositories have no pool
	repo.RecordConnectionTrace(connTraceFixture(1))
	require.Nil(t, repo.connectionQueue, "no writer is started")
	require.Empty(t, fake.statements())
}

func TestConnectionTraceWriter_RetentionRunsConcurrentlyWithoutBlockingTraceWrites(t *testing.T) {
	prevInterval := connectionRetentionInterval
	connectionRetentionInterval = 10 * time.Millisecond
	t.Cleanup(func() { connectionRetentionInterval = prevInterval })

	entered := make(chan struct{})
	canceled := make(chan struct{})
	var enterOnce, cancelOnce sync.Once

	db, fake := newConnTraceDB(t)
	fake.setQueryHook(func(_ context.Context, query string, _ []driver.Value) ([][]driver.Value, error) {
		if strings.Contains(query, "pg_try_advisory_lock") {
			return [][]driver.Value{{true}}, nil
		}
		return nil, nil
	})
	fake.setExecCtxHook(func(ctx context.Context, query string, _ []driver.Value) error {
		if strings.HasPrefix(query, "DELETE FROM request_connection_traces") {
			enterOnce.Do(func() { close(entered) })
			<-ctx.Done()
			cancelOnce.Do(func() { close(canceled) })
			return ctx.Err()
		}
		return nil
	})

	repo := newUsageLogRepositoryWithSQL(nil, db)
	t.Cleanup(repo.StopConnectionDiagnostics)

	first := connTraceFixture(0)
	repo.RecordConnectionTrace(first)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("retention goroutine never started")
	}

	extra := connTraceFixtures(64)
	for _, rec := range extra {
		repo.RecordConnectionTrace(rec)
	}
	wantTotal := 1 + len(extra)
	require.Eventually(t, func() bool {
		return len(fake.committedTraceIDs()) == wantTotal
	}, 3*time.Second, 5*time.Millisecond, "trace writer must keep flushing while retention is slow")
	require.Zero(t, repo.connectionQueueDrops.total.Load()+repo.connectionWriteDrops.total.Load())

	stopped := make(chan struct{})
	go func() {
		repo.StopConnectionDiagnostics()
		close(stopped)
	}()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("in-flight retention query was not canceled on stop")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("StopConnectionDiagnostics did not return promptly")
	}
}

// ---- other write paths stay synchronous --------------------------------------

func TestConnectionTraceBatch_EdgeImportStaysSynchronous(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	records := connTraceFixtures(2)
	require.NoError(t, repo.ImportEdgeConnections(context.Background(), records))

	// The edge collector forgets its copy once this returns, so the import must
	// not use asynchronous commit.
	require.Equal(t, []string{"begin", "insert", "insert", "commit"}, fake.sequence())
	for _, s := range fake.statements() {
		require.NotContains(t, strings.ToLower(s.Query), "synchronous_commit")
	}
}

func TestConnectionTraceBatch_UsageLogWritesStaySynchronous(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	prepared := prepareUsageLogInsert(&service.UsageLog{
		UserID: 1, APIKeyID: 2, RequestID: "client:sync-commit", Model: "test-model", CreatedAt: time.Now().UTC(),
	})
	repo.flushBestEffortBatch(repo.db, []usageLogBestEffortRequest{{prepared: prepared, apiKeyID: 2}})

	stmts := fake.statements()
	require.Len(t, stmts, 1, "usage logs are a plain statement, no transaction wrapper")
	require.Equal(t, "exec", stmts[0].Kind)
	require.NotContains(t, strings.ToLower(stmts[0].Query), "synchronous_commit")
}

func TestConnectionTraceBatch_AsyncCommitStaysInTheDiagnosticsHelper(t *testing.T) {
	// Billing, balance and usage-log writes live in this package. Keeping the
	// setting in one helper (and out of every other file) is what guarantees it
	// can only ever reach the diagnostics callers.
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	var users []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		require.NoError(t, err)
		if strings.Contains(strings.ToLower(string(raw)), "synchronous_commit") {
			users = append(users, name)
		}
	}
	require.Equal(t, []string{"usage_connections.go"}, users)
}

// ---- traffic events ----------------------------------------------------------

func connTraceTrafficEvent(i int) service.TrafficEvent {
	at := time.Date(2026, 11, 25, 8, 0, i, 0, time.UTC)
	return service.TrafficEvent{
		EventID: uuid.NewString(), UserID: 1, APIKeyID: 2, GroupID: 3, RequestID: "req-" + strconv.Itoa(i),
		Endpoint: "/v1/responses", Method: "POST", StartedAt: at, FinishedAt: at.Add(time.Second),
		UploadBytes: 10, DownloadBytes: 20, StatusCode: 200, Outcome: "completed", RequestComplete: true, MeasurementVersion: 1,
	}
}

func TestTrafficEventBatch_RunsInAsyncCommitTransaction(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	events := []service.TrafficEvent{connTraceTrafficEvent(1), connTraceTrafficEvent(2), connTraceTrafficEvent(3)}
	require.NoError(t, repo.RecordTrafficBatch(context.Background(), events))

	require.Equal(t, []string{"begin", "set", "exec", "commit"}, fake.sequence())
	stmts := fake.statements()
	require.Equal(t, connTraceSetLocalSQL, stmts[1].Query)
	require.Equal(t, stmts[0].Tx, stmts[2].Tx)
	// The dedupe-and-aggregate statement is unchanged: one statement for the batch.
	require.Contains(t, stmts[2].Query, "INSERT INTO user_traffic_events")
	require.Contains(t, stmts[2].Query, "ON CONFLICT (event_id) DO NOTHING")
	require.Contains(t, stmts[2].Query, "INSERT INTO user_traffic_hourly")
	require.Len(t, stmts[2].Args, len(events)*15)
}

func TestTrafficEventBatch_SingleEventUsesTheSamePath(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	require.NoError(t, repo.RecordTraffic(context.Background(), connTraceTrafficEvent(1)))
	require.Equal(t, []string{"begin", "set", "exec", "commit"}, fake.sequence())
}

func TestTrafficEventBatch_InvalidEventNeverReachesTheDatabase(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	bad := connTraceTrafficEvent(1)
	bad.UserID = 0
	require.ErrorIs(t, repo.RecordTrafficBatch(context.Background(), []service.TrafficEvent{connTraceTrafficEvent(2), bad}), service.ErrInvalidTrafficEvent)
	require.Empty(t, fake.statements())
}

func TestTrafficEventBatch_FailureRollsBack(t *testing.T) {
	repo, fake := newConnTraceRepo(t)
	fake.setHook(func(query string, _ []driver.Value) error {
		if strings.Contains(query, "user_traffic_events") {
			return &pq.Error{Code: "22P02", Message: "invalid input syntax"}
		}
		return nil
	})
	err := repo.RecordTrafficBatch(context.Background(), []service.TrafficEvent{connTraceTrafficEvent(1)})
	var pgErr *pq.Error
	require.ErrorAs(t, err, &pgErr, "the delivery worker still sees the SQLSTATE and quarantines permanent errors")
	require.Equal(t, []string{"begin", "set", "exec", "rollback"}, fake.sequence())
}

// A repository bound to someone else's transaction must not change how that
// transaction commits.
func TestTrafficEventBatch_RepositoryWithoutPoolKeepsThePlainStatement(t *testing.T) {
	db, fake := newConnTraceDB(t)
	repo := &usageLogRepository{sql: db}
	require.NoError(t, repo.RecordTrafficBatch(context.Background(), []service.TrafficEvent{connTraceTrafficEvent(1)}))
	require.Equal(t, []string{"exec"}, fake.sequence())
	require.NotContains(t, fake.statements()[0].Query, "synchronous_commit")
}
