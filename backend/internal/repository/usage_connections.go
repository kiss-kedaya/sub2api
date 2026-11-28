package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Connection traces are written in batches. A single writer used to commit one
// row per transaction, so its throughput was capped by commit latency and a
// 256-slot queue dropped traces as soon as the database slowed down.
const (
	connectionTraceQueueCap       = 4096
	connectionTraceBatchMax       = 256
	connectionTraceBatchWindow    = 20 * time.Millisecond
	connectionTraceWriteTimeout   = 5 * time.Second // per attempt, one context per batch
	connectionTraceWriteAttempts  = 3
	connectionTraceMaxDetailBytes = 128 * 1024
	connectionTraceLogInterval    = 10 * time.Second
	connectionTraceColumnCount    = 22
	connectionTraceDetailColumn   = 13 // zero-based position of the jsonb column
)

// connectionTraceShutdownBudget bounds how long a stop request may spend
// persisting what is still queued. A variable only so tests can shrink it.
var connectionTraceShutdownBudget = 3 * time.Second

var errConnectionTraceTooLarge = errors.New("connection trace exceeds bound")

// connectionDropCounter counts diagnostics rows that were given up on and turns
// the count into at most one log line per interval, so an outage cannot flood
// the log with one line per request.
type connectionDropCounter struct {
	total    atomic.Int64
	reported atomic.Int64
	nextLog  atomic.Int64
}

// add records n drops. When logNow is true the caller should log sinceLastLog.
func (c *connectionDropCounter) add(n int, now time.Time) (sinceLastLog int64, logNow bool) {
	total := c.total.Add(int64(n))
	next := c.nextLog.Load()
	if now.UnixNano() < next || !c.nextLog.CompareAndSwap(next, now.Add(connectionTraceLogInterval).UnixNano()) {
		return 0, false
	}
	return total - c.reported.Swap(total), true
}

// RecordConnectionTrace never blocks the response on database I/O. The queue,
// payload, retries and retention cleanup are bounded and diagnostics-only.
func (r *usageLogRepository) RecordConnectionTrace(record service.ConnectionTrace) {
	if r.db == nil {
		return
	}
	r.startConnectionWriter()
	select {
	case <-r.connectionDone:
		return
	default:
	}
	select {
	case r.connectionQueue <- record:
	default:
		if dropped, ok := r.connectionQueueDrops.add(1, time.Now()); ok {
			logger.LegacyPrintf("connection_diagnostics", "queue full; trace dropped (%d since last report)", dropped)
		}
	}
}

func (r *usageLogRepository) startConnectionWriter() {
	r.connectionOnce.Do(func() {
		r.connectionQueue = make(chan service.ConnectionTrace, connectionTraceQueueCap)
		r.connectionDone = make(chan struct{})
		r.connectionExited = make(chan struct{})
		retentionDone := make(chan struct{})
		go func() {
			defer close(retentionDone)
			r.runConnectionRetention()
		}()
		go r.runConnectionWriter(retentionDone)
	})
}

func (r *usageLogRepository) runConnectionWriter(retentionDone <-chan struct{}) {
	defer close(r.connectionExited)
	batch := make([]service.ConnectionTrace, 0, connectionTraceBatchMax)
	for {
		// A stop request wins over queued work, so shutdown is bounded by the
		// drain budget instead of by however much is still queued.
		select {
		case <-r.connectionDone:
			r.drainConnectionQueue(batch[:0])
			<-retentionDone
			return
		default:
		}
		select {
		case <-r.connectionDone:
			continue
		case first := <-r.connectionQueue:
			batch = r.collectConnectionBatch(append(batch[:0], first))
			r.flushConnectionBatch(context.Background(), batch)
			clear(batch) // do not keep written traces reachable until the next batch
		}
	}
}

// runConnectionRetention runs hourly diagnostic retention on its own goroutine
// so slow deletes can never stall trace ingestion or overflow connectionQueue.
func (r *usageLogRepository) runConnectionRetention() {
	if r.db == nil {
		return
	}
	ticker := time.NewTicker(connectionRetentionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.connectionDone:
			return
		case <-ticker.C:
			r.runDiagnosticRetentionWithStop()
		}
	}
}

func (r *usageLogRepository) runDiagnosticRetentionWithStop() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-r.connectionDone:
			cancel()
		case <-ctx.Done():
		}
	}()
	defer func() {
		cancel()
		<-done
	}()
	r.runDiagnosticRetentionCycle(ctx)
}

func (r *usageLogRepository) runDiagnosticRetentionCycle(ctx context.Context) {
	if r.db == nil || ctx.Err() != nil {
		return
	}
	lockCtx, lockCancel := context.WithTimeout(ctx, connectionRetentionLockTimeout)
	lockConn, err := r.db.Conn(lockCtx)
	if err != nil {
		lockCancel()
		if ctx.Err() == nil {
			logger.LegacyPrintf("connection_diagnostics", "retention lock connection failed: %v", err)
		}
		return
	}
	defer func() { _ = lockConn.Close() }()

	var acquired bool
	err = lockConn.QueryRowContext(lockCtx, `SELECT pg_try_advisory_lock($1)`, diagnosticRetentionLockKey).Scan(&acquired)
	lockCancel()
	if err != nil {
		if ctx.Err() == nil {
			logger.LegacyPrintf("connection_diagnostics", "retention advisory lock failed: %v", err)
		}
		// Discard the connection because the server may have already acquired the
		// session-level advisory lock before a client timeout or read failure.
		_ = lockConn.Raw(func(any) error { return driver.ErrBadConn })
		return
	}
	if !acquired {
		return
	}
	defer func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), connectionRetentionLockTimeout)
		defer unlockCancel()
		if _, unlockErr := lockConn.ExecContext(unlockCtx, `SELECT pg_advisory_unlock($1)`, diagnosticRetentionLockKey); unlockErr != nil {
			logger.LegacyPrintf("connection_diagnostics", "retention advisory unlock failed: %v", unlockErr)
			// Discard the connection so a leaked session-level advisory lock never returns to the pool.
			_ = lockConn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()

	r.pruneDiagnosticTable(ctx, lockConn, "request_connection_traces", connectionTracePruneSQL(), connectionTracePruneBatch, connectionTracePrunePasses)
	r.pruneDiagnosticTable(ctx, lockConn, "user_traffic_events", userTrafficEventPruneSQL(), userTrafficEventPruneBatch, userTrafficEventPrunePasses)
}

func (r *usageLogRepository) pruneDiagnosticTable(parent context.Context, conn *sql.Conn, tableName, query string, batchSize, maxPasses int) {
	tableCtx, tableCancel := context.WithTimeout(parent, connectionRetentionTableBudget)
	defer tableCancel()
	for pass := 0; pass < maxPasses; pass++ {
		if tableCtx.Err() != nil {
			if parent.Err() == nil {
				logger.LegacyPrintf("connection_diagnostics", "%s retention budget exhausted (pass=%d): %v", tableName, pass, tableCtx.Err())
			}
			return
		}
		batchCtx, batchCancel := context.WithTimeout(tableCtx, connectionRetentionBatchTimeout)
		res, err := conn.ExecContext(batchCtx, query)
		batchCancel()
		if err != nil {
			if parent.Err() != nil {
				return
			}
			logger.LegacyPrintf("connection_diagnostics", "%s retention failed (pass=%d): %v", tableName, pass+1, err)
			return
		}
		if n, err := res.RowsAffected(); err == nil && n < int64(batchSize) {
			return
		}
	}
}

func connectionTracePruneSQL() string {
	return `DELETE FROM request_connection_traces WHERE ctid IN (SELECT ctid FROM request_connection_traces WHERE started_at < NOW() - INTERVAL '` +
		strconv.Itoa(connectionTraceRetentionDays) + ` days' LIMIT ` + strconv.Itoa(connectionTracePruneBatch) + `)`
}

func userTrafficEventPruneSQL() string {
	return `DELETE FROM user_traffic_events WHERE ctid IN (SELECT ctid FROM user_traffic_events WHERE started_at < NOW() - INTERVAL '` +
		strconv.Itoa(userTrafficEventRetentionDays) + ` days' LIMIT ` + strconv.Itoa(userTrafficEventPruneBatch) + `)`
}

// collectConnectionBatch tops a batch up to the batch limit, waiting at most the
// batch window after its first trace for more to arrive.
func (r *usageLogRepository) collectConnectionBatch(batch []service.ConnectionTrace) []service.ConnectionTrace {
	timer := time.NewTimer(connectionTraceBatchWindow)
	defer timer.Stop()
	for len(batch) < connectionTraceBatchMax {
		select {
		case record := <-r.connectionQueue:
			batch = append(batch, record)
		case <-timer.C:
			return batch
		case <-r.connectionDone:
			return batch
		}
	}
	return batch
}

// takeQueuedConnectionTraces tops a batch up with what is already queued,
// without waiting.
func (r *usageLogRepository) takeQueuedConnectionTraces(batch []service.ConnectionTrace) []service.ConnectionTrace {
	for len(batch) < connectionTraceBatchMax {
		select {
		case record := <-r.connectionQueue:
			batch = append(batch, record)
		default:
			return batch
		}
	}
	return batch
}

// drainConnectionQueue persists what is still queued when the writer is told to
// stop. One deadline bounds the whole drain: once it passes, every remaining
// batch fails immediately and is counted as dropped.
func (r *usageLogRepository) drainConnectionQueue(batch []service.ConnectionTrace) {
	ctx, cancel := context.WithTimeout(context.Background(), connectionTraceShutdownBudget)
	defer cancel()
	for {
		batch = r.takeQueuedConnectionTraces(batch)
		if len(batch) == 0 {
			return
		}
		r.flushConnectionBatch(ctx, batch)
		clear(batch)
		batch = batch[:0]
	}
}

// flushConnectionBatch persists one batch. Every failure ends in a counted,
// rate-limited drop; nothing here can block or panic the writer.
func (r *usageLogRepository) flushConnectionBatch(ctx context.Context, batch []service.ConnectionTrace) {
	args := make([]any, 0, len(batch)*connectionTraceColumnCount)
	for i := range batch {
		raw, err := json.Marshal(batch[i].Detail)
		if err == nil && len(raw) > connectionTraceMaxDetailBytes {
			err = errConnectionTraceTooLarge
		}
		if err != nil {
			// Only this trace is lost; the rest of the batch is unaffected.
			r.dropConnectionTraces(1, err)
			continue
		}
		args = appendConnectionTraceArgs(args, batch[i], string(raw))
	}
	rows := len(args) / connectionTraceColumnCount
	if rows == 0 {
		return
	}
	err := r.writeConnectionTraceRows(ctx, args)
	if err == nil {
		return
	}
	if rows == 1 || !connectionTraceDataError(err) {
		// Database trouble hits every row alike; writing them one by one would
		// only multiply the timeouts, so give the batch up.
		r.dropConnectionTraces(rows, err)
		return
	}
	// A data error does not say which row caused it. Write the rows one by one
	// so a single bad value costs only its own trace.
	for i := 0; i < rows; i++ {
		rowErr := r.writeConnectionTraceRows(ctx, args[i*connectionTraceColumnCount:(i+1)*connectionTraceColumnCount])
		if rowErr == nil {
			continue
		}
		if connectionTraceDataError(rowErr) {
			r.dropConnectionTraces(1, rowErr)
			continue
		}
		// The database went away mid-way: stop instead of retrying every remaining row.
		r.dropConnectionTraces(rows-i, rowErr)
		return
	}
}

func (r *usageLogRepository) dropConnectionTraces(n int, err error) {
	if dropped, ok := r.connectionWriteDrops.add(n, time.Now()); ok {
		logger.LegacyPrintf("connection_diagnostics", "trace persistence failed; %d dropped since last report (%s)", dropped, connectionTraceFailureReason(err))
	}
}

// connectionTraceFailureReason names a failure without the database's own
// message, which can echo the offending value.
func connectionTraceFailureReason(err error) string {
	var pgErr *pq.Error
	switch {
	case errors.As(err, &pgErr):
		return "sqlstate " + string(pgErr.Code)
	case errors.Is(err, errConnectionTraceTooLarge):
		return "detail exceeds bound"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "write failed"
	}
}

// connectionTraceDataError reports SQLSTATE classes 22 (data exception) and 23
// (integrity violation): retrying the same values can never succeed.
func connectionTraceDataError(err error) bool {
	var pgErr *pq.Error
	if !errors.As(err, &pgErr) {
		return false
	}
	class := pgErr.Code.Class()
	return class == "22" || class == "23"
}

// writeConnectionTraceRows inserts len(args)/connectionTraceColumnCount rows in
// one asynchronous-commit transaction. Retrying is safe: ON CONFLICT DO NOTHING
// makes a repeated insert harmless even if an earlier commit's outcome is unknown.
func (r *usageLogRepository) writeConnectionTraceRows(parent context.Context, args []any) error {
	query := connectionTraceInsertSQL(len(args) / connectionTraceColumnCount)
	var err error
	for attempt := 0; attempt < connectionTraceWriteAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(parent, connectionTraceWriteTimeout)
		err = execAsyncCommit(ctx, r.db, query, args...)
		cancel()
		// A data error repeats on every retry and a finished parent context cannot
		// recover: only transient failures earn another attempt.
		if err == nil || connectionTraceDataError(err) || parent.Err() != nil {
			return err
		}
	}
	return err
}

// appendConnectionTraceArgs appends one row's values in column order.
func appendConnectionTraceArgs(args []any, v service.ConnectionTrace, detail string) []any {
	return append(args, v.TraceID, v.StartedAt, v.RequestID, v.ClientRequestID, v.UserID, v.APIKeyID, v.AccountID, v.GroupID, v.Model, v.Path, v.ClientIP, v.UserAgent, v.Stream, detail, v.Source, v.Host, connectionTraceProtocolScheme(v.Scheme), v.Method, connectionTraceProtocolVersion(v.HTTPVersion), connectionTraceProtocolVersion(v.TLSVersion), v.UpstreamStatus, v.NetworkError)
}

// connectionTraceInsertSQL builds the multi-row INSERT. The column list,
// placeholder order, jsonb cast and conflict clause are those of the single-row
// statement it replaced, so a one-row batch is the identical statement.
func connectionTraceInsertSQL(rows int) string {
	buf := make([]byte, 0, 512+rows*connectionTraceColumnCount*6)
	buf = append(buf, `INSERT INTO request_connection_traces
  (trace_id,started_at,request_id,client_request_id,user_id,api_key_id,account_id,group_id,model,path,client_ip,user_agent,stream,detail,source,host,scheme,method,http_version,tls_version,upstream_status,network_error)
  VALUES `...)
	n := 0
	for row := 0; row < rows; row++ {
		if row > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, '(')
		for col := 0; col < connectionTraceColumnCount; col++ {
			if col > 0 {
				buf = append(buf, ',')
			}
			n++
			buf = append(buf, '$')
			buf = strconv.AppendInt(buf, int64(n), 10)
			if col == connectionTraceDetailColumn {
				buf = append(buf, "::jsonb"...)
			}
		}
		buf = append(buf, ')')
	}
	buf = append(buf, `
  ON CONFLICT (trace_id) DO NOTHING`...)
	return string(buf)
}

// execAsyncCommit runs one diagnostics write in its own transaction with
// synchronous_commit off for that transaction only. Diagnostics can lose the
// last few hundred milliseconds if the database crashes, and in exchange the
// commit no longer waits behind every other writer for a WAL flush. SET LOCAL
// ends with the transaction: the session and the server setting are untouched.
// Only call it on a pool (never on a caller's transaction, whose durability it
// would change) and never for billing, balance or usage-log writes.
func execAsyncCommit(ctx context.Context, db *sql.DB, query string, args ...any) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "SET LOCAL synchronous_commit = off"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	return tx.Commit()
}

// A completed terminal never proves client receipt. Cancellations after a
// completed terminal can be normal cleanup; recovered attempt failures do not
// turn a completed request into a failure. Unknown/legacy data stays unknown.
const connectionStateSQL = `CASE
 WHEN source IN ('nginx','caddy') AND (network_error<>'' OR COALESCE((detail->>'status')::int,0)>=400) THEN 'edge_failed'
 WHEN source IN ('nginx','caddy') THEN 'edge_completed'
 WHEN detail->>'downstream_error'='true' THEN 'downstream_write_failed'
 WHEN detail->>'client_disconnect'='true' OR detail->>'outcome'='client_disconnected' THEN 'client_disconnected'
 WHEN detail->>'outcome'='failed' OR detail->>'terminal'='failed' OR COALESCE((detail->>'status')::int,0)>=400 THEN 'failed'
 WHEN detail->>'terminal'='incomplete' THEN 'incomplete'
 WHEN detail->>'terminal'='completed' THEN 'completed'
 WHEN detail->>'canceled'='true' THEN 'canceled'
 ELSE 'unknown' END`

func connectionTraceQuery(f service.ConnectionTraceFilter) (string, []any) {
	args := []any{f.Start, f.End}
	// Apply indexed time ranges in both arms. Legacy traces are not backfilled;
	// each real trace is returned once even if billing retries share a usage row.
	source := `WITH source AS (
 SELECT trace_id,started_at,request_id,client_request_id,user_id,api_key_id,account_id,group_id,model,path,client_ip,user_agent,stream,detail,source,host,scheme,method,http_version,tls_version,upstream_status,network_error,false AS legacy
 FROM request_connection_traces WHERE started_at >= $1 AND started_at < $2
 UNION ALL
 (SELECT DISTINCT ON (e.request_id) md5('ops-error:' || e.id::text)::uuid,e.created_at,e.request_id,COALESCE(e.client_request_id,''),
 COALESCE(e.user_id,0),COALESCE(e.api_key_id,0),COALESCE(e.account_id,0),COALESCE(e.group_id,0),COALESCE(e.model,''),
 e.request_path,COALESCE(host(e.client_ip),''),COALESCE(e.user_agent,''),COALESCE(e.stream,false),
 jsonb_build_object('version',0,'trace_id',md5('ops-error:'||e.id::text)::uuid,'started_at',e.created_at-COALESCE(e.duration_ms,0)*INTERVAL '1 millisecond',
 'status',e.status_code,'total_ms',e.duration_ms,'outcome',CASE WHEN e.status_code=499 THEN 'client_disconnected' ELSE 'failed' END,
 'terminal','', 'events',jsonb_build_object(),'spans',jsonb_build_array(),'attempts',jsonb_build_array()),
 'application'::text,''::text,''::text,'POST'::text,''::text,''::text,''::text,''::text,true
 FROM ops_error_logs e
 WHERE e.created_at >= $1 AND e.created_at < $2 AND e.status_code>=400
 AND (e.request_path LIKE '%/responses' OR e.request_path LIKE '%/responses/compact' OR e.request_path LIKE '%/messages' OR e.request_path LIKE '%/chat/completions')
 AND NOT EXISTS (SELECT 1 FROM request_connection_traces n WHERE n.request_id=e.request_id)

 ORDER BY e.request_id,e.created_at DESC)
 ), classified AS (SELECT *, ` + connectionStateSQL + ` AS state FROM source)
 SELECT c.trace_id,c.started_at,c.request_id,c.client_request_id,c.user_id,c.api_key_id,c.account_id,c.group_id,c.model,c.path,c.client_ip,c.user_agent,c.stream,c.detail,c.legacy,c.state,c.source,c.host,c.scheme,c.method,c.http_version,c.tls_version,c.upstream_status,c.network_error,
 COALESCE(u.email,''),COALESCE(k.name,''),COALESCE(a.name,''),COALESCE(g.name,''),COALESCE(u.username,'')
 FROM classified c LEFT JOIN users u ON u.id=c.user_id LEFT JOIN api_keys k ON k.id=c.api_key_id
 LEFT JOIN accounts a ON a.id=c.account_id LEFT JOIN groups g ON g.id=c.group_id WHERE 1=1`
	add := func(expr string, v any) { args = append(args, v); source += fmt.Sprintf(expr, len(args)) }
	for _, p := range []struct {
		column string
		value  int64
	}{{"user_id", f.UserID}, {"api_key_id", f.APIKeyID}, {"account_id", f.AccountID}, {"group_id", f.GroupID}} {
		if p.value > 0 {
			add(" AND c."+p.column+"=$%d", p.value)
		}
	}
	if f.Host != "" {
		add(" AND c.host=$%d", f.Host)
	}
	if f.Source == "edge" {
		source += " AND c.source IN ('nginx','caddy')"
	} else if f.Source != "" && f.Source != "all" {
		add(" AND c.source=$%d", f.Source)
	}
	if f.Model != "" {
		add(" AND c.model=$%d", f.Model)
	}
	if f.ClientIP != "" {
		add(" AND c.client_ip=$%d", f.ClientIP)
	}
	if f.RequestID != "" {
		args = append(args, f.RequestID)
		n := len(args)
		source += fmt.Sprintf(" AND (c.request_id=$%d OR c.client_request_id=$%d OR c.trace_id::text=$%d)", n, n, n)
	}
	if f.State == "abnormal" {
		source += " AND c.state NOT IN ('completed','edge_completed','unknown')"
	} else if f.State != "" && f.State != "all" {
		add(" AND c.state=$%d", f.State)
	}
	args = append(args, f.PageSize+1, (f.Page-1)*f.PageSize)
	source += fmt.Sprintf(" ORDER BY c.started_at DESC,c.trace_id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	return source, args
}

func (r *usageLogRepository) ListConnectionTraces(ctx context.Context, f service.ConnectionTraceFilter) (*service.ConnectionTraceList, error) {
	query, args := connectionTraceQuery(f)
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := &service.ConnectionTraceList{Items: []service.ConnectionTrace{}, Page: f.Page, PageSize: f.PageSize, RetentionDays: connectionTraceRetentionDays}
	for rows.Next() {
		var v service.ConnectionTrace
		var raw []byte
		if err := rows.Scan(&v.TraceID, &v.StartedAt, &v.RequestID, &v.ClientRequestID, &v.UserID, &v.APIKeyID, &v.AccountID, &v.GroupID, &v.Model, &v.Path, &v.ClientIP, &v.UserAgent, &v.Stream, &raw, &v.Legacy, &v.State, &v.Source, &v.Host, &v.Scheme, &v.Method, &v.HTTPVersion, &v.TLSVersion, &v.UpstreamStatus, &v.NetworkError, &v.UserEmail, &v.APIKeyName, &v.AccountName, &v.GroupName, &v.UserName); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &v.Detail); err != nil {
			return nil, fmt.Errorf("decode connection trace: %w", err)
		}
		if v.Path != "" {
			v.Path = service.ConnectionPath(v.Path)
		}
		if !v.Detail.StartedAt.IsZero() {
			v.StartedAt = v.Detail.StartedAt
		}
		out.Items = append(out.Items, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.HasMore = len(out.Items) > f.PageSize
	if out.HasMore {
		out.Items = out.Items[:f.PageSize]
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// Enrich only the bounded page with existing structured error identities;
	// raw upstream messages stay in the established sanitized Ops detail API.
	if len(out.Items) > 0 {
		ids := make([]string, 0, len(out.Items))
		for _, item := range out.Items {
			if item.RequestID != "" {
				ids = append(ids, item.RequestID)
			}
		}
		if len(ids) > 0 {
			errors, err := r.sql.QueryContext(ctx, `SELECT DISTINCT ON (request_id) request_id,id,error_type,status_code FROM ops_error_logs WHERE request_id=ANY($1) AND status_code>=400 ORDER BY request_id,created_at DESC`, pq.Array(ids))
			if err != nil {
				return nil, err
			}
			defer func() { _ = errors.Close() }()
			for errors.Next() {
				var rid, kind string
				var id int64
				var status int
				if err := errors.Scan(&rid, &id, &kind, &status); err != nil {
					return nil, err
				}
				for i := range out.Items {
					if out.Items[i].RequestID == rid {
						out.Items[i].ErrorLogID = id
						out.Items[i].ErrorType = kind
						out.Items[i].ErrorStatus = status
					}
				}
			}
			if err := errors.Err(); err != nil {
				return nil, err
			}
		}
	}
	for i := range out.Items {
		if out.Items[i].Detail.AuthRejection != "" {
			out.Items[i].ErrorType = out.Items[i].Detail.AuthRejection
			out.Items[i].ErrorStatus = out.Items[i].Detail.Status
		}
	}
	return out, nil
}

// ImportEdgeConnections acknowledges durable writes, not queue acceptance. A
// collector can safely resend a batch after a lost acknowledgement.
func (r *usageLogRepository) ImportEdgeConnections(ctx context.Context, records []service.ConnectionTrace) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, v := range records {
		raw, e := json.Marshal(v.Detail)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO request_connection_traces(trace_id,started_at,request_id,client_request_id,user_id,api_key_id,account_id,group_id,model,path,client_ip,user_agent,stream,detail,source,host,scheme,method,http_version,tls_version,upstream_status,network_error) VALUES($1,$2,$3,'',0,0,0,0,'',$4,$5,'',false,$6::jsonb,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT(trace_id) DO NOTHING`, v.TraceID, v.StartedAt, v.RequestID, v.Path, v.ClientIP, string(raw), v.Source, v.Host, connectionTraceProtocolScheme(v.Scheme), v.Method, connectionTraceProtocolVersion(v.HTTPVersion), connectionTraceProtocolVersion(v.TLSVersion), v.UpstreamStatus, v.NetworkError)
		if e != nil {
			return e
		}
	}
	return tx.Commit()
}

// StopConnectionDiagnostics stops the writer and gives it a bounded moment to
// persist what is already queued; the process closes the database right after.
func (r *usageLogRepository) StopConnectionDiagnostics() {
	r.startConnectionWriter()
	r.connectionStop.Do(func() { close(r.connectionDone) })
	timer := time.NewTimer(connectionTraceShutdownBudget + time.Second)
	defer timer.Stop()
	select {
	case <-r.connectionExited:
	case <-timer.C:
	}
}

// Retention windows and pruning budgets for the two high-volume diagnostic
// tables. Traces run ~10 GB/day and traffic events ~3 GB/day, so both windows
// stay at 7 days; small ctid batches keep lock and WAL spikes bounded while
// enough passes per hour still outpace ingestion (~612k rows/hour at 170/s).
// Delivered live_balance_adjustment_outbox cleanup lives exclusively in
// LiveBalanceAdjustmentOutboxWorker (every 5 minutes) to avoid duplicate locks.
const (
	diagnosticRetentionLockKey     int64 = 0x5355423244524554 // "SUB2DRET"
	connectionRetentionLockTimeout       = 5 * time.Second

	connectionTraceRetentionDays = 7
	connectionTracePruneBatch    = 10000
	connectionTracePrunePasses   = 400

	userTrafficEventRetentionDays = 7
	userTrafficEventPruneBatch    = 10000
	userTrafficEventPrunePasses   = 400
)

// Variables only so tests can shrink intervals and budgets without waiting.
var (
	connectionRetentionInterval     = time.Hour
	connectionRetentionBatchTimeout = 20 * time.Second
	connectionRetentionTableBudget  = 10 * time.Minute
)

// Protocol metadata carries no diagnostic value per row and repeats the same
// two or three values millions of times, so it is not stored: user_agent,
// method, host, upstream_status and network_error stay because they are what
// an operator actually reads when a request is slow or failing.
func connectionTraceProtocolScheme(string) string  { return "" }
func connectionTraceProtocolVersion(string) string { return "" }
