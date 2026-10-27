package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

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
	case r.connectionQueue <- record:
	default:
		logger.LegacyPrintf("connection_diagnostics", "queue full; trace dropped")
	}
}

func (r *usageLogRepository) startConnectionWriter() {
	r.connectionOnce.Do(func() {
		r.connectionQueue = make(chan service.ConnectionTrace, 256)
		r.connectionDone = make(chan struct{})
		go r.runConnectionWriter()
	})
}

func (r *usageLogRepository) runConnectionWriter() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-r.connectionDone:
			return
		case record := <-r.connectionQueue:
			for attempt := 0; attempt < 3; attempt++ {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				err := r.writeConnectionTrace(ctx, record)
				cancel()
				if err == nil {
					break
				}
				if attempt == 2 {
					logger.LegacyPrintf("connection_diagnostics", "trace persistence failed")
				}
			}
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err := r.db.ExecContext(ctx, `DELETE FROM request_connection_traces WHERE trace_id IN
    (SELECT trace_id FROM request_connection_traces WHERE started_at < NOW()-INTERVAL '30 days' LIMIT 10000)`)
			cancel()
			if err != nil {
				logger.LegacyPrintf("connection_diagnostics", "retention failed")
			}
		}
	}
}

func (r *usageLogRepository) writeConnectionTrace(ctx context.Context, v service.ConnectionTrace) error {
	raw, err := json.Marshal(v.Detail)
	if err != nil {
		return err
	}
	if len(raw) > 128*1024 {
		return fmt.Errorf("connection trace exceeds bound")
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO request_connection_traces
  (trace_id,started_at,request_id,client_request_id,user_id,api_key_id,account_id,group_id,model,path,client_ip,user_agent,stream,detail,source,host,scheme,method,http_version,tls_version,upstream_status,network_error)
  VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb,$15,$16,$17,$18,$19,$20,$21,$22)
  ON CONFLICT (trace_id) DO NOTHING`, v.TraceID, v.StartedAt, v.RequestID, v.ClientRequestID, v.UserID, v.APIKeyID, v.AccountID, v.GroupID, v.Model, v.Path, v.ClientIP, v.UserAgent, v.Stream, string(raw), v.Source, v.Host, v.Scheme, v.Method, v.HTTPVersion, v.TLSVersion, v.UpstreamStatus, v.NetworkError)
	return err
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
	out := &service.ConnectionTraceList{Items: []service.ConnectionTrace{}, Page: f.Page, PageSize: f.PageSize, RetentionDays: 30}
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
		_, e = tx.ExecContext(ctx, `INSERT INTO request_connection_traces(trace_id,started_at,request_id,client_request_id,user_id,api_key_id,account_id,group_id,model,path,client_ip,user_agent,stream,detail,source,host,scheme,method,http_version,tls_version,upstream_status,network_error) VALUES($1,$2,$3,'',0,0,0,0,'',$4,$5,'',false,$6::jsonb,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT(trace_id) DO NOTHING`, v.TraceID, v.StartedAt, v.RequestID, v.Path, v.ClientIP, string(raw), v.Source, v.Host, v.Scheme, v.Method, v.HTTPVersion, v.TLSVersion, v.UpstreamStatus, v.NetworkError)
		if e != nil {
			return e
		}
	}
	return tx.Commit()
}

func (r *usageLogRepository) StopConnectionDiagnostics() {
	r.startConnectionWriter()
	r.connectionStop.Do(func() { close(r.connectionDone) })
}
