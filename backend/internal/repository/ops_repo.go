package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type opsRepository struct {
	db *sql.DB
}

const insertOpsErrorLogSQL = `
INSERT INTO ops_error_logs (
  request_id,
  client_request_id,
  user_id,
  api_key_id,
  account_id,
  group_id,
  client_ip,
  platform,
  model,
  request_path,
  stream,
  inbound_endpoint,
  upstream_endpoint,
  requested_model,
  upstream_model,
  request_type,
  user_agent,
  error_phase,
  error_type,
  severity,
  status_code,
  is_business_limited,
  is_count_tokens,
  error_message,
  error_body,
  error_source,
  error_owner,
  upstream_status_code,
  upstream_error_message,
  upstream_error_detail,
  upstream_errors,
  auth_latency_ms,
  routing_latency_ms,
  upstream_latency_ms,
  response_latency_ms,
  time_to_first_token_ms,
  created_at,
  api_key_prefix,
  request_headers,
  upstream_request_headers,
  upstream_response_headers
)`

const opsErrorLogInsertColumnCount = 41
const opsErrorLogInsertChunkSize = 128

func opsErrorLogValuePlaceholders(base int) string {
	values := make([]string, opsErrorLogInsertColumnCount)
	for i := range values {
		values[i] = "$" + strconv.Itoa(base+i+1)
	}
	return "(" + strings.Join(values, ",") + ")"
}

func NewOpsRepository(db *sql.DB) service.OpsRepository {
	return &opsRepository{db: db}
}

func (r *opsRepository) InsertErrorLog(ctx context.Context, input *service.OpsInsertErrorLogInput) (int64, error) {
	if r == nil || r.db == nil {
		return 0, fmt.Errorf("nil ops repository")
	}
	if input == nil {
		return 0, fmt.Errorf("nil input")
	}

	var id int64
	err := r.db.QueryRowContext(
		ctx,
		insertOpsErrorLogSQL+" VALUES "+opsErrorLogValuePlaceholders(0)+" RETURNING id",
		opsInsertErrorLogArgs(input)...,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (r *opsRepository) BatchInsertErrorLogs(ctx context.Context, inputs []*service.OpsInsertErrorLogInput) (int64, error) {
	if r == nil || r.db == nil {
		return 0, fmt.Errorf("nil ops repository")
	}
	if len(inputs) == 0 {
		return 0, nil
	}

	valid := make([]*service.OpsInsertErrorLogInput, 0, len(inputs))
	for _, input := range inputs {
		if input != nil {
			valid = append(valid, input)
		}
	}
	if len(valid) == 0 {
		return 0, nil
	}

	// Error logs are diagnostic, so each chunk runs through execAsyncCommit to
	// avoid stalling primary WAL flushes. Committing per chunk also ensures the
	// returned inserted count matches persisted rows if a later chunk fails.
	for start := 0; start < len(valid); start += opsErrorLogInsertChunkSize {
		end := start + opsErrorLogInsertChunkSize
		if end > len(valid) {
			end = len(valid)
		}
		values := make([]string, 0, end-start)
		args := make([]any, 0, (end-start)*opsErrorLogInsertColumnCount)
		for i, input := range valid[start:end] {
			values = append(values, opsErrorLogValuePlaceholders(i*opsErrorLogInsertColumnCount))
			args = append(args, opsInsertErrorLogArgs(input)...)
		}
		if err := execAsyncCommit(ctx, r.db, insertOpsErrorLogSQL+" VALUES "+strings.Join(values, ","), args...); err != nil {
			return int64(start), err
		}
	}

	return int64(len(valid)), nil
}

const (
	opsErrorLogMaxRequestIDLen    = 64
	opsErrorLogMaxPlatformLen     = 32
	opsErrorLogMaxModelLen        = 100
	opsErrorLogMaxPathLen         = 256
	opsErrorLogMaxErrorPhaseLen   = 32
	opsErrorLogMaxErrorTypeLen    = 64
	opsErrorLogMaxSeverityLen     = 8
	opsErrorLogDefaultSeverity    = "P2"
	opsErrorLogMaxErrorSourceLen  = 64
	opsErrorLogMaxErrorOwnerLen   = 32
	opsErrorLogMaxAPIKeyPrefixLen = 32

	opsSystemLogMaxHostLen      = 255
	opsSystemLogMaxLevelLen     = 16
	opsSystemLogMaxComponentLen = 128
	opsSystemLogMaxRequestIDLen = 128
	opsSystemLogMaxPlatformLen  = 32
	opsSystemLogMaxModelLen     = 128
)

func opsInsertErrorLogArgs(input *service.OpsInsertErrorLogInput) []any {
	return []any{
		opsNullStringMax(input.RequestID, opsErrorLogMaxRequestIDLen),
		opsNullStringMax(input.ClientRequestID, opsErrorLogMaxRequestIDLen),
		opsNullInt64(input.UserID),
		opsNullInt64(input.APIKeyID),
		opsNullInt64(input.AccountID),
		opsNullInt64(input.GroupID),
		opsNullString(input.ClientIP),
		opsNullStringMax(input.Platform, opsErrorLogMaxPlatformLen),
		opsNullStringMax(input.Model, opsErrorLogMaxModelLen),
		opsNullStringMax(input.RequestPath, opsErrorLogMaxPathLen),
		input.Stream,
		opsNullStringMax(input.InboundEndpoint, opsErrorLogMaxPathLen),
		opsNullStringMax(input.UpstreamEndpoint, opsErrorLogMaxPathLen),
		opsNullStringMax(input.RequestedModel, opsErrorLogMaxModelLen),
		opsNullStringMax(input.UpstreamModel, opsErrorLogMaxModelLen),
		opsNullInt16(input.RequestType),
		opsNullString(input.UserAgent),
		opsRequiredStringMax(input.ErrorPhase, opsErrorLogMaxErrorPhaseLen),
		opsRequiredStringMax(input.ErrorType, opsErrorLogMaxErrorTypeLen),
		opsRequiredStringMaxOrDefault(input.Severity, opsErrorLogMaxSeverityLen, opsErrorLogDefaultSeverity),
		opsNullInt(input.StatusCode),
		input.IsBusinessLimited,
		input.IsCountTokens,
		opsNullString(input.ErrorMessage),
		opsNullString(input.ErrorBody),
		opsNullStringMax(input.ErrorSource, opsErrorLogMaxErrorSourceLen),
		opsNullStringMax(input.ErrorOwner, opsErrorLogMaxErrorOwnerLen),
		opsNullableIntPointer(input.UpstreamStatusCode),
		opsNullString(input.UpstreamErrorMessage),
		opsNullString(input.UpstreamErrorDetail),
		opsNullJSONBString(input.UpstreamErrorsJSON),
		opsNullInt64(input.AuthLatencyMs),
		opsNullInt64(input.RoutingLatencyMs),
		opsNullInt64(input.UpstreamLatencyMs),
		opsNullInt64(input.ResponseLatencyMs),
		opsNullInt64(input.TimeToFirstTokenMs),
		input.CreatedAt,
		opsNullStringMax(input.APIKeyPrefix, opsErrorLogMaxAPIKeyPrefixLen),
		opsNullJSONBHeaderSnapshot(input.RequestHeaders),
		opsNullJSONBHeaderSnapshot(input.UpstreamRequestHeaders),
		opsNullJSONBHeaderSnapshot(input.UpstreamResponseHeaders),
	}
}

// decodeOpsHeaderSnapshot 把详情查询里取回的 JSONB 文本解析回头快照。
// 空串（NULL 列）或解析失败都返回 nil，前端按「无记录」展示。
func decodeOpsHeaderSnapshot(raw string) service.OpsHeaderSnapshot {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}
	out := service.OpsHeaderSnapshot{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// opsNullJSONBHeaderSnapshot 把脱敏后的头快照序列化成 JSONB 参数。
// 空快照写 NULL，避免为「没有头可记」的请求产生无意义的空对象。
// 写入前清洗 NUL 字节和非法 UTF-8，避免 Postgres JSONB 报 0x00 / \u0000 错误。
func opsNullJSONBHeaderSnapshot(snapshot service.OpsHeaderSnapshot) any {
	if len(snapshot) == 0 {
		return nil
	}
	clean := snapshot
	needsClean := false
	for k, v := range snapshot {
		if strings.IndexByte(k, 0) >= 0 || strings.IndexByte(v, 0) >= 0 || !utf8.ValidString(k) || !utf8.ValidString(v) {
			needsClean = true
			break
		}
	}
	if needsClean {
		clean = make(service.OpsHeaderSnapshot, len(snapshot))
		for k, v := range snapshot {
			ck := opsSanitizeText(k)
			if ck == "" {
				continue
			}
			clean[ck] = opsSanitizeText(v)
		}
		if len(clean) == 0 {
			return nil
		}
	}
	encoded, err := json.Marshal(clean)
	if err != nil {
		// 序列化失败不能影响错误记录本身；丢弃这一份快照。
		return nil
	}
	return string(encoded)
}

// opsErrorLogsOrderBy builds the ORDER BY clause from a whitelist, mirroring
// usageLogOrderBy semantics. Unknown SortBy falls back to created_at; e.id is
// always appended as tiebreaker for stable pagination.
func opsErrorLogsOrderBy(filter *service.OpsErrorLogFilter) string {
	sortBy := ""
	sortOrder := ""
	if filter != nil {
		sortBy = strings.ToLower(strings.TrimSpace(filter.SortBy))
		sortOrder = strings.ToLower(strings.TrimSpace(filter.SortOrder))
	}

	var column string
	switch sortBy {
	case "model":
		column = "COALESCE(NULLIF(TRIM(e.requested_model), ''), e.model)"
	case "status_code":
		// 与展示列/过滤保持同义:列表展示 COALESCE(upstream_status_code, status_code, 0),
		// status_code 过滤也用同一表达式,故排序必须一致——否则 recovered upstream 行
		//（status_code<400 但展示上游 5xx）排序键与显示值/分页切分不符。
		column = "COALESCE(e.upstream_status_code, e.status_code, 0)"
	default:
		column = "e.created_at"
	}

	dir := "DESC"
	if sortOrder == "asc" {
		dir = "ASC"
	}
	return fmt.Sprintf("%s %s, e.id %s", column, dir, dir)
}

func (r *opsRepository) ListErrorLogs(ctx context.Context, filter *service.OpsErrorLogFilter) (*service.OpsErrorLogList, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	if filter == nil {
		filter = &service.OpsErrorLogFilter{}
	}

	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 500 {
		pageSize = 500
	}

	where, args := buildOpsErrorLogsWhere(filter)
	orderBy := opsErrorLogsOrderBy(filter)
	countSQL := "SELECT COUNT(*) FROM ops_error_logs e " + where
	offset := (page - 1) * pageSize
	argsWithLimit := append(append([]any{}, args...), pageSize, offset)

	type countResult struct {
		total int
		err   error
	}
	countCh := make(chan countResult, 1)
	go func() {
		var total int
		err := r.db.QueryRowContext(ctx, countSQL, args...).Scan(&total)
		countCh <- countResult{total: total, err: err}
	}()

	selectSQL := `
SELECT
  e.id,
  e.created_at,
  e.error_phase,
  e.error_type,
  COALESCE(e.error_owner, ''),
  COALESCE(e.error_source, ''),
  e.severity,
  COALESCE(e.upstream_status_code, e.status_code, 0),
  COALESCE(e.platform, ''),
  COALESCE(e.model, ''),
  COALESCE(e.resolved, false),
  e.resolved_at,
  e.resolved_by_user_id,
  COALESCE(u2.email, ''),
  COALESCE(e.client_request_id, ''),
  COALESCE(e.request_id, ''),
  COALESCE(
    NULLIF(e.upstream_response_headers->>'x-request-id', ''),
    NULLIF(e.upstream_response_headers->>'x-oneapi-request-id', ''),
    NULLIF(e.upstream_request_headers->>'x-request-id', ''),
    ''
  ),
  COALESCE(e.error_message, ''),
  e.user_id,
  COALESCE(u.email, ''),
  e.api_key_id,
  e.account_id,
  COALESCE(a.name, ''),
  e.group_id,
  COALESCE(g.name, ''),
  CASE WHEN e.client_ip IS NULL THEN NULL ELSE host(e.client_ip) END,
  COALESCE(e.request_path, ''),
  e.stream,
  COALESCE(e.inbound_endpoint, ''),
  COALESCE(e.upstream_endpoint, ''),
  COALESCE(e.requested_model, ''),
  COALESCE(e.upstream_model, ''),
  COALESCE(e.user_agent, ''),
  e.request_type,
  COALESCE(ak.name, ''),
  ak.deleted_at
FROM (
  SELECT e.id
  FROM ops_error_logs e
  ` + where + `
  ORDER BY ` + orderBy + `
  LIMIT $` + itoa(len(args)+1) + ` OFFSET $` + itoa(len(args)+2) + `
) page
JOIN ops_error_logs e ON e.id = page.id
LEFT JOIN accounts a ON e.account_id = a.id
LEFT JOIN groups g ON e.group_id = g.id
LEFT JOIN users u ON e.user_id = u.id
LEFT JOIN users u2 ON e.resolved_by_user_id = u2.id
LEFT JOIN api_keys ak ON ak.id = e.api_key_id
ORDER BY ` + orderBy

	rows, err := r.db.QueryContext(ctx, selectSQL, argsWithLimit...)
	if err != nil {
		<-countCh
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make([]*service.OpsErrorLog, 0, pageSize)
	for rows.Next() {
		var item service.OpsErrorLog
		var statusCode sql.NullInt64
		var clientIP sql.NullString
		var userID sql.NullInt64
		var apiKeyID sql.NullInt64
		var accountID sql.NullInt64
		var accountName string
		var groupID sql.NullInt64
		var groupName string
		var userEmail string
		var resolvedAt sql.NullTime
		var resolvedBy sql.NullInt64
		var resolvedByName string
		var requestType sql.NullInt64
		var apiKeyName string
		var apiKeyDeletedAt sql.NullTime
		if err := rows.Scan(
			&item.ID,
			&item.CreatedAt,
			&item.Phase,
			&item.Type,
			&item.Owner,
			&item.Source,
			&item.Severity,
			&statusCode,
			&item.Platform,
			&item.Model,
			&item.Resolved,
			&resolvedAt,
			&resolvedBy,
			&resolvedByName,
			&item.ClientRequestID,
			&item.RequestID,
			&item.UpstreamRequestID,
			&item.Message,
			&userID,
			&userEmail,
			&apiKeyID,
			&accountID,
			&accountName,
			&groupID,
			&groupName,
			&clientIP,
			&item.RequestPath,
			&item.Stream,
			&item.InboundEndpoint,
			&item.UpstreamEndpoint,
			&item.RequestedModel,
			&item.UpstreamModel,
			&item.UserAgent,
			&requestType,
			&apiKeyName,
			&apiKeyDeletedAt,
		); err != nil {
			return nil, err
		}
		if resolvedAt.Valid {
			t := resolvedAt.Time
			item.ResolvedAt = &t
		}
		if resolvedBy.Valid {
			v := resolvedBy.Int64
			item.ResolvedByUserID = &v
		}
		item.ResolvedByUserName = resolvedByName
		item.StatusCode = int(statusCode.Int64)
		if clientIP.Valid {
			s := clientIP.String
			item.ClientIP = &s
		}
		if userID.Valid {
			v := userID.Int64
			item.UserID = &v
		}
		item.UserEmail = userEmail
		if apiKeyID.Valid {
			v := apiKeyID.Int64
			item.APIKeyID = &v
		}
		if accountID.Valid {
			v := accountID.Int64
			item.AccountID = &v
		}
		item.AccountName = accountName
		if groupID.Valid {
			v := groupID.Int64
			item.GroupID = &v
		}
		item.GroupName = groupName
		if requestType.Valid {
			v := int16(requestType.Int64)
			item.RequestType = &v
		}
		item.APIKeyName = apiKeyName
		item.APIKeyDeleted = apiKeyDeletedAt.Valid
		out = append(out, &item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	countRes := <-countCh
	if countRes.err != nil {
		return nil, countRes.err
	}
	total := countRes.total

	return &service.OpsErrorLogList{
		Errors:   out,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func (r *opsRepository) GetErrorLogByID(ctx context.Context, id int64) (*service.OpsErrorLogDetail, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	if id <= 0 {
		return nil, fmt.Errorf("invalid id")
	}

	q := `
SELECT
  e.id,
  e.created_at,
  e.error_phase,
  e.error_type,
  COALESCE(e.error_owner, ''),
  COALESCE(e.error_source, ''),
  e.severity,
  COALESCE(e.upstream_status_code, e.status_code, 0),
  COALESCE(e.platform, ''),
  COALESCE(e.model, ''),
  COALESCE(e.resolved, false),
  e.resolved_at,
  e.resolved_by_user_id,
  COALESCE(e.client_request_id, ''),
  COALESCE(e.request_id, ''),
  COALESCE(e.error_message, ''),
  COALESCE(e.error_body, ''),
  e.upstream_status_code,
  COALESCE(e.upstream_error_message, ''),
  COALESCE(e.upstream_error_detail, ''),
  COALESCE(e.upstream_errors::text, ''),
  e.is_business_limited,
  e.user_id,
  COALESCE(u.email, ''),
  e.api_key_id,
  e.account_id,
  COALESCE(a.name, ''),
  e.group_id,
  COALESCE(g.name, ''),
  CASE WHEN e.client_ip IS NULL THEN NULL ELSE host(e.client_ip) END,
  COALESCE(e.request_path, ''),
  e.stream,
  COALESCE(e.inbound_endpoint, ''),
  COALESCE(e.upstream_endpoint, ''),
  COALESCE(e.requested_model, ''),
  COALESCE(e.upstream_model, ''),
  e.request_type,
  COALESCE(e.user_agent, ''),
  e.auth_latency_ms,
  e.routing_latency_ms,
  e.upstream_latency_ms,
  e.response_latency_ms,
  e.time_to_first_token_ms,
  COALESCE(e.api_key_prefix, ''),
  COALESCE(ak.name, ''),
  ak.deleted_at,
  COALESCE(e.request_headers::text, ''),
  COALESCE(e.upstream_request_headers::text, ''),
  COALESCE(e.upstream_response_headers::text, '')
FROM ops_error_logs e
LEFT JOIN users u ON e.user_id = u.id
LEFT JOIN accounts a ON e.account_id = a.id
LEFT JOIN groups g ON e.group_id = g.id
LEFT JOIN api_keys ak ON ak.id = e.api_key_id
WHERE e.id = $1
LIMIT 1`

	var out service.OpsErrorLogDetail
	var statusCode sql.NullInt64
	var upstreamStatusCode sql.NullInt64
	var resolvedAt sql.NullTime
	var resolvedBy sql.NullInt64
	var clientIP sql.NullString
	var userID sql.NullInt64
	var apiKeyID sql.NullInt64
	var accountID sql.NullInt64
	var groupID sql.NullInt64
	var authLatency sql.NullInt64
	var routingLatency sql.NullInt64
	var upstreamLatency sql.NullInt64
	var responseLatency sql.NullInt64
	var ttft sql.NullInt64
	var requestType sql.NullInt64
	var detailAPIKeyName string
	var detailAPIKeyDeletedAt sql.NullTime
	var requestHeadersJSON string
	var upstreamRequestHeadersJSON string
	var upstreamResponseHeadersJSON string

	err := r.db.QueryRowContext(ctx, q, id).Scan(
		&out.ID,
		&out.CreatedAt,
		&out.Phase,
		&out.Type,
		&out.Owner,
		&out.Source,
		&out.Severity,
		&statusCode,
		&out.Platform,
		&out.Model,
		&out.Resolved,
		&resolvedAt,
		&resolvedBy,
		&out.ClientRequestID,
		&out.RequestID,
		&out.Message,
		&out.ErrorBody,
		&upstreamStatusCode,
		&out.UpstreamErrorMessage,
		&out.UpstreamErrorDetail,
		&out.UpstreamErrors,
		&out.IsBusinessLimited,
		&userID,
		&out.UserEmail,
		&apiKeyID,
		&accountID,
		&out.AccountName,
		&groupID,
		&out.GroupName,
		&clientIP,
		&out.RequestPath,
		&out.Stream,
		&out.InboundEndpoint,
		&out.UpstreamEndpoint,
		&out.RequestedModel,
		&out.UpstreamModel,
		&requestType,
		&out.UserAgent,
		&authLatency,
		&routingLatency,
		&upstreamLatency,
		&responseLatency,
		&ttft,
		&out.APIKeyPrefix,
		&detailAPIKeyName,
		&detailAPIKeyDeletedAt,
		&requestHeadersJSON,
		&upstreamRequestHeadersJSON,
		&upstreamResponseHeadersJSON,
	)
	if err != nil {
		return nil, err
	}
	out.RequestHeaders = decodeOpsHeaderSnapshot(requestHeadersJSON)
	out.UpstreamRequestHeaders = decodeOpsHeaderSnapshot(upstreamRequestHeadersJSON)
	out.UpstreamResponseHeaders = decodeOpsHeaderSnapshot(upstreamResponseHeadersJSON)

	out.StatusCode = int(statusCode.Int64)
	if resolvedAt.Valid {
		t := resolvedAt.Time
		out.ResolvedAt = &t
	}
	if resolvedBy.Valid {
		v := resolvedBy.Int64
		out.ResolvedByUserID = &v
	}
	if clientIP.Valid {
		s := clientIP.String
		out.ClientIP = &s
	}
	if upstreamStatusCode.Valid {
		v := int(upstreamStatusCode.Int64)
		out.UpstreamStatusCode = &v
	}
	if userID.Valid {
		v := userID.Int64
		out.UserID = &v
	}
	if apiKeyID.Valid {
		v := apiKeyID.Int64
		out.APIKeyID = &v
	}
	if accountID.Valid {
		v := accountID.Int64
		out.AccountID = &v
	}
	if groupID.Valid {
		v := groupID.Int64
		out.GroupID = &v
	}
	if authLatency.Valid {
		v := authLatency.Int64
		out.AuthLatencyMs = &v
	}
	if routingLatency.Valid {
		v := routingLatency.Int64
		out.RoutingLatencyMs = &v
	}
	if upstreamLatency.Valid {
		v := upstreamLatency.Int64
		out.UpstreamLatencyMs = &v
	}
	if responseLatency.Valid {
		v := responseLatency.Int64
		out.ResponseLatencyMs = &v
	}
	if ttft.Valid {
		v := ttft.Int64
		out.TimeToFirstTokenMs = &v
	}
	if requestType.Valid {
		v := int16(requestType.Int64)
		out.RequestType = &v
	}
	out.APIKeyName = detailAPIKeyName
	out.APIKeyDeleted = detailAPIKeyDeletedAt.Valid

	// Normalize upstream_errors to empty string when stored as JSON null.
	out.UpstreamErrors = strings.TrimSpace(out.UpstreamErrors)
	if out.UpstreamErrors == "null" {
		out.UpstreamErrors = ""
	}

	return &out, nil
}

func (r *opsRepository) UpdateErrorResolution(ctx context.Context, errorID int64, resolved bool, resolvedByUserID *int64, resolvedAt *time.Time) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("nil ops repository")
	}
	if errorID <= 0 {
		return fmt.Errorf("invalid error id")
	}

	q := `
UPDATE ops_error_logs
SET
  resolved = $2,
  resolved_at = $3,
  resolved_by_user_id = $4
WHERE id = $1`

	at := sql.NullTime{}
	if resolvedAt != nil && !resolvedAt.IsZero() {
		at = sql.NullTime{Time: resolvedAt.UTC(), Valid: true}
	} else if resolved {
		now := time.Now().UTC()
		at = sql.NullTime{Time: now, Valid: true}
	}

	_, err := r.db.ExecContext(
		ctx,
		q,
		errorID,
		resolved,
		at,
		nullInt64(resolvedByUserID),
	)
	return err
}

func (r *opsRepository) BatchInsertSystemLogs(ctx context.Context, inputs []*service.OpsInsertSystemLogInput) (int64, error) {
	if r == nil || r.db == nil {
		return 0, fmt.Errorf("nil ops repository")
	}
	if len(inputs) == 0 {
		return 0, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	stmt, err := tx.PrepareContext(ctx, pq.CopyIn(
		"ops_system_logs",
		"created_at",
		"host",
		"level",
		"component",
		"message",
		"request_id",
		"client_request_id",
		"user_id",
		"api_key_id",
		"account_id",
		"platform",
		"model",
		"extra",
	))
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}

	var inserted int64
	for _, input := range inputs {
		if input == nil {
			continue
		}
		createdAt := input.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		component := strings.TrimSpace(opsTruncateRunes(strings.TrimSpace(opsSanitizeText(input.Component)), opsSystemLogMaxComponentLen))
		level := strings.TrimSpace(opsTruncateRunes(strings.ToLower(strings.TrimSpace(opsSanitizeText(input.Level))), opsSystemLogMaxLevelLen))
		message := strings.TrimSpace(opsSanitizeText(input.Message))
		if level == "" || message == "" {
			continue
		}
		if component == "" {
			component = "app"
		}
		extra := strings.TrimSpace(opsSanitizeJSONB(input.ExtraJSON))
		if extra == "" || !json.Valid([]byte(extra)) {
			extra = "{}"
		}
		if _, err := stmt.ExecContext(
			ctx,
			createdAt.UTC(),
			opsNullStringMax(input.Host, opsSystemLogMaxHostLen),
			level,
			component,
			message,
			opsNullStringMax(input.RequestID, opsSystemLogMaxRequestIDLen),
			opsNullStringMax(input.ClientRequestID, opsSystemLogMaxRequestIDLen),
			opsNullInt64(input.UserID),
			opsNullInt64(input.APIKeyID),
			opsNullInt64(input.AccountID),
			opsNullStringMax(input.Platform, opsSystemLogMaxPlatformLen),
			opsNullStringMax(input.Model, opsSystemLogMaxModelLen),
			extra,
		); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return inserted, err
		}
		inserted++
	}

	if _, err := stmt.ExecContext(ctx); err != nil {
		_ = stmt.Close()
		_ = tx.Rollback()
		return inserted, err
	}
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		return inserted, err
	}
	if err := tx.Commit(); err != nil {
		return inserted, err
	}
	return inserted, nil
}

func (r *opsRepository) ListSystemLogs(ctx context.Context, filter *service.OpsSystemLogFilter) (*service.OpsSystemLogList, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	if filter == nil {
		filter = &service.OpsSystemLogFilter{}
	}

	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}

	where, args, _ := buildOpsSystemLogsWhere(filter)
	countSQL := "SELECT COUNT(*) FROM ops_system_logs l " + where
	var total int
	if err := r.db.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, err
	}

	offset := (page - 1) * pageSize
	argsWithLimit := append(args, pageSize, offset)
	query := `
SELECT
  l.id,
  l.created_at,
  COALESCE(l.host, ''),
  l.level,
  COALESCE(l.component, ''),
  COALESCE(l.message, ''),
  COALESCE(l.request_id, ''),
  COALESCE(l.client_request_id, ''),
  l.user_id,
  l.api_key_id,
  l.account_id,
  COALESCE(l.platform, ''),
  COALESCE(l.model, ''),
  COALESCE(l.extra::text, '{}')
FROM ops_system_logs l
` + where + `
ORDER BY l.created_at DESC, l.id DESC
LIMIT $` + itoa(len(args)+1) + ` OFFSET $` + itoa(len(args)+2)

	rows, err := r.db.QueryContext(ctx, query, argsWithLimit...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	logs := make([]*service.OpsSystemLog, 0, pageSize)
	for rows.Next() {
		item := &service.OpsSystemLog{}
		var userID sql.NullInt64
		var apiKeyID sql.NullInt64
		var accountID sql.NullInt64
		var extraRaw string
		if err := rows.Scan(
			&item.ID,
			&item.CreatedAt,
			&item.Host,
			&item.Level,
			&item.Component,
			&item.Message,
			&item.RequestID,
			&item.ClientRequestID,
			&userID,
			&apiKeyID,
			&accountID,
			&item.Platform,
			&item.Model,
			&extraRaw,
		); err != nil {
			return nil, err
		}
		if userID.Valid {
			v := userID.Int64
			item.UserID = &v
		}
		if apiKeyID.Valid {
			v := apiKeyID.Int64
			item.APIKeyID = &v
		}
		if accountID.Valid {
			v := accountID.Int64
			item.AccountID = &v
		}
		extraRaw = strings.TrimSpace(extraRaw)
		if extraRaw != "" && extraRaw != "null" && extraRaw != "{}" {
			extra := make(map[string]any)
			if err := json.Unmarshal([]byte(extraRaw), &extra); err == nil {
				item.Extra = extra
			}
		}
		logs = append(logs, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &service.OpsSystemLogList{
		Logs:     logs,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func (r *opsRepository) DeleteSystemLogs(ctx context.Context, filter *service.OpsSystemLogCleanupFilter) (int64, error) {
	if r == nil || r.db == nil {
		return 0, fmt.Errorf("nil ops repository")
	}
	if filter == nil {
		filter = &service.OpsSystemLogCleanupFilter{}
	}

	where, args, hasConstraint := buildOpsSystemLogsCleanupWhere(filter)
	if !hasConstraint {
		return 0, fmt.Errorf("cleanup requires at least one filter condition")
	}

	query := "DELETE FROM ops_system_logs l " + where
	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *opsRepository) InsertSystemLogCleanupAudit(ctx context.Context, input *service.OpsSystemLogCleanupAudit) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("nil ops repository")
	}
	if input == nil {
		return fmt.Errorf("nil input")
	}
	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	conditions := strings.TrimSpace(opsSanitizeJSONB(input.Conditions))
	if conditions == "" || !json.Valid([]byte(conditions)) {
		conditions = "{}"
	}
	_, err := r.db.ExecContext(ctx, `
INSERT INTO ops_system_log_cleanup_audits (
  created_at,
  operator_id,
  conditions,
  deleted_rows
) VALUES ($1,$2,$3,$4)
`, createdAt.UTC(), input.OperatorID, conditions, input.DeletedRows)
	return err
}

var likePatternReplacer = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// escapeLikePattern 转义 LIKE/ILIKE 通配符（\ % _），避免用户输入被当作通配符。
// Postgres 默认以反斜杠为转义符，无需额外 ESCAPE 子句。
func escapeLikePattern(s string) string {
	return likePatternReplacer.Replace(s)
}

func buildOpsErrorLogsWhere(filter *service.OpsErrorLogFilter) (string, []any) {
	clauses := make([]string, 0, 12)
	args := make([]any, 0, 12)
	clauses = append(clauses, "1=1")

	phaseFilter := ""
	if filter != nil {
		phaseFilter = strings.TrimSpace(strings.ToLower(filter.Phase))
	}
	// ops_error_logs stores client-visible error requests (status>=400),
	// but we also persist "recovered" upstream errors (status<400) for upstream health visibility.
	// If Resolved is not specified, do not filter by resolved state (backward-compatible).
	resolvedFilter := (*bool)(nil)
	if filter != nil {
		resolvedFilter = filter.Resolved
	}
	// Keep list endpoints scoped to client errors unless the caller explicitly opts
	// into recovered provider-health rows (upstream/account_auth). Request-error
	// endpoints never set the opt-in and retain this guard.
	// cyber_policy is exempt from the status >= 400 guard: streaming cyber hits arrive with
	// status 200 (the SSE stream opened successfully before upstream returned response.failed),
	// but they are always client-visible blocked requests that belong in admin + user error
	// lists.  Without the exemption the entire streaming-path cyber sink would be invisible.
	if !opsFilterIncludesRecoveredProviderRows(filter, phaseFilter) {
		clauses = append(clauses, "(COALESCE(e.status_code, 0) >= 400 OR e.error_type = 'cyber_policy')")
	}

	if filter.StartTime != nil && !filter.StartTime.IsZero() {
		args = append(args, filter.StartTime.UTC())
		clauses = append(clauses, "e.created_at >= $"+itoa(len(args)))
	}
	if filter.EndTime != nil && !filter.EndTime.IsZero() {
		args = append(args, filter.EndTime.UTC())
		// Keep time-window semantics consistent with other ops queries: [start, end)
		clauses = append(clauses, "e.created_at < $"+itoa(len(args)))
	}
	if p := strings.TrimSpace(filter.Platform); p != "" {
		args = append(args, p)
		clauses = append(clauses, "e.platform = $"+itoa(len(args)))
	}
	if filter.GroupID != nil && *filter.GroupID > 0 {
		args = append(args, *filter.GroupID)
		clauses = append(clauses, "e.group_id = $"+itoa(len(args)))
	}
	if filter.AccountID != nil && *filter.AccountID > 0 {
		args = append(args, *filter.AccountID)
		clauses = append(clauses, "e.account_id = $"+itoa(len(args)))
	}
	if phase := phaseFilter; phase != "" {
		args = append(args, phase)
		clauses = append(clauses, "e.error_phase = $"+itoa(len(args)))
	}
	if filter != nil {
		if owner := strings.TrimSpace(strings.ToLower(filter.Owner)); owner != "" {
			args = append(args, owner)
			clauses = append(clauses, "LOWER(COALESCE(e.error_owner,'')) = $"+itoa(len(args)))
		}
		if source := strings.TrimSpace(strings.ToLower(filter.Source)); source != "" {
			args = append(args, source)
			clauses = append(clauses, "LOWER(COALESCE(e.error_source,'')) = $"+itoa(len(args)))
		}
	}
	if resolvedFilter != nil {
		args = append(args, *resolvedFilter)
		clauses = append(clauses, "COALESCE(e.resolved,false) = $"+itoa(len(args)))
	}

	// View filter: errors vs excluded vs all.
	// Excluded = business-limited errors (quota/concurrency/billing).
	// Upstream 429/529 are included in errors view to match SLA calculation.
	view := ""
	if filter != nil {
		view = strings.ToLower(strings.TrimSpace(filter.View))
	}
	switch view {
	case "", "errors":
		clauses = append(clauses, "COALESCE(e.is_business_limited,false) = false")
	case "excluded":
		clauses = append(clauses, "COALESCE(e.is_business_limited,false) = true")
	case "all":
		// no-op
	default:
		// treat unknown as default 'errors'
		clauses = append(clauses, "COALESCE(e.is_business_limited,false) = false")
	}
	if len(filter.StatusCodes) > 0 {
		args = append(args, pq.Array(filter.StatusCodes))
		clauses = append(clauses, "COALESCE(e.upstream_status_code, e.status_code, 0) = ANY($"+itoa(len(args))+")")
	} else if filter.StatusCodesOther {
		// "Other" means: status codes not in the common list.
		known := []int{400, 401, 403, 404, 409, 422, 429, 500, 502, 503, 504, 529}
		args = append(args, pq.Array(known))
		clauses = append(clauses, "NOT (COALESCE(e.upstream_status_code, e.status_code, 0) = ANY($"+itoa(len(args))+"))")
	}
	// 使用记录搜索框只传 request_id。库里可能是 client: 前缀，错误表拆成 request_id / client_request_id。
	// 默认与 "=id" 走精确匹配（=），命中 btree 索引；"^id" 走区分大小写前缀 LIKE '...%'，
	// "*id" 显式回退包含匹配 ILIKE '%...%'，避免后续删除 pg_trgm 索引后默认查询触发全表扫描。
	if rid := strings.TrimSpace(filter.RequestID); rid != "" {
		var operator, pattern string
		switch {
		case strings.HasPrefix(rid, "*"):
			if raw := strings.TrimSpace(rid[1:]); raw != "" {
				operator = "ILIKE"
				pattern = "%" + escapeLikePattern(raw) + "%"
			}
		case strings.HasPrefix(rid, "^"):
			if normalized := service.NormalizeUsageRequestIDKey(strings.TrimSpace(rid[1:])); normalized != "" {
				operator = "LIKE"
				pattern = escapeLikePattern(normalized) + "%"
			}
		default:
			raw := rid
			if strings.HasPrefix(rid, "=") {
				raw = strings.TrimSpace(rid[1:])
			}
			if normalized := service.NormalizeUsageRequestIDKey(raw); normalized != "" {
				operator = "="
				pattern = normalized
			}
		}
		if operator != "" {
			args = append(args, pattern)
			n := itoa(len(args))
			clauses = append(clauses, "(e.request_id "+operator+" $"+n+" OR e.client_request_id "+operator+" $"+n+")")
		} else {
			// Non-empty request_id filter with empty target after sigil/prefix stripping
			// must fail closed rather than dropping the predicate and returning all rows.
			clauses = append(clauses, "FALSE")
		}
	}
	if crid := strings.TrimSpace(filter.ClientRequestID); crid != "" {
		args = append(args, crid)
		clauses = append(clauses, "COALESCE(e.client_request_id,'') = $"+itoa(len(args)))
	}

	if q := strings.TrimSpace(filter.Query); q != "" {
		like := "%" + q + "%"
		args = append(args, like)
		n := itoa(len(args))
		clauses = append(clauses, "(e.request_id ILIKE $"+n+" OR e.client_request_id ILIKE $"+n+" OR e.error_message ILIKE $"+n+")")
	}

	if userQuery := strings.TrimSpace(filter.UserQuery); userQuery != "" {
		like := "%" + userQuery + "%"
		args = append(args, like)
		n := itoa(len(args))
		clauses = append(clauses, "EXISTS (SELECT 1 FROM users u WHERE u.id = e.user_id AND u.email ILIKE $"+n+")")
	}

	if filter.UserID != nil && *filter.UserID > 0 {
		args = append(args, *filter.UserID)
		n := itoa(len(args))
		clauses = append(clauses, "e.user_id = $"+n)
	}
	if filter.APIKeyID != nil && *filter.APIKeyID > 0 {
		args = append(args, *filter.APIKeyID)
		clauses = append(clauses, "e.api_key_id = $"+itoa(len(args)))
	}
	if m := strings.TrimSpace(filter.Model); m != "" {
		if filter.ModelFuzzy {
			args = append(args, "%"+escapeLikePattern(m)+"%")
			clauses = append(clauses, "COALESCE(e.requested_model, e.model, '') ILIKE $"+itoa(len(args)))
		} else {
			args = append(args, m)
			clauses = append(clauses, "COALESCE(e.requested_model, e.model, '') = $"+itoa(len(args)))
		}
	}
	if filter.ExcludeCountTokens {
		clauses = append(clauses, "COALESCE(e.is_count_tokens, false) = false")
	}
	if len(filter.ErrorPhasesAny) > 0 {
		args = append(args, pq.Array(filter.ErrorPhasesAny))
		clauses = append(clauses, "e.error_phase = ANY($"+itoa(len(args))+")")
	}
	if len(filter.ErrorTypesAny) > 0 {
		args = append(args, pq.Array(filter.ErrorTypesAny))
		clauses = append(clauses, "e.error_type = ANY($"+itoa(len(args))+")")
	}

	return "WHERE " + strings.Join(clauses, " AND "), args
}

func opsFilterIncludesRecoveredProviderRows(filter *service.OpsErrorLogFilter, phaseFilter string) bool {
	if filter == nil || !filter.IncludeRecoveredUpstream {
		return false
	}
	if phaseFilter != "" {
		return phaseFilter == "upstream" || phaseFilter == "account_auth"
	}
	if len(filter.ErrorPhasesAny) == 0 {
		return false
	}
	sawProviderPhase := false
	for _, rawPhase := range filter.ErrorPhasesAny {
		switch strings.TrimSpace(strings.ToLower(rawPhase)) {
		case "upstream", "account_auth":
			sawProviderPhase = true
		default:
			return false
		}
	}
	return sawProviderPhase
}

func buildOpsSystemLogsWhere(filter *service.OpsSystemLogFilter) (string, []any, bool) {
	clauses := make([]string, 0, 10)
	args := make([]any, 0, 10)
	clauses = append(clauses, "1=1")
	hasConstraint := false

	if filter != nil && filter.StartTime != nil && !filter.StartTime.IsZero() {
		args = append(args, filter.StartTime.UTC())
		clauses = append(clauses, "l.created_at >= $"+itoa(len(args)))
		hasConstraint = true
	}
	if filter != nil && filter.EndTime != nil && !filter.EndTime.IsZero() {
		args = append(args, filter.EndTime.UTC())
		clauses = append(clauses, "l.created_at < $"+itoa(len(args)))
		hasConstraint = true
	}
	if filter != nil {
		if v := strings.TrimSpace(filter.Host); v != "" {
			args = append(args, v)
			clauses = append(clauses, "l.host = $"+itoa(len(args)))
			hasConstraint = true
		}
		if v := strings.ToLower(strings.TrimSpace(filter.Level)); v != "" {
			args = append(args, v)
			clauses = append(clauses, "LOWER(COALESCE(l.level,'')) = $"+itoa(len(args)))
			hasConstraint = true
		}
		if v := strings.TrimSpace(filter.Component); v != "" {
			args = append(args, v)
			clauses = append(clauses, "COALESCE(l.component,'') = $"+itoa(len(args)))
			hasConstraint = true
		}
		if v := strings.TrimSpace(filter.RequestID); v != "" {
			args = append(args, v)
			clauses = append(clauses, "COALESCE(l.request_id,'') = $"+itoa(len(args)))
			hasConstraint = true
		}
		if v := strings.TrimSpace(filter.ClientRequestID); v != "" {
			args = append(args, v)
			clauses = append(clauses, "COALESCE(l.client_request_id,'') = $"+itoa(len(args)))
			hasConstraint = true
		}
		if filter.UserID != nil && *filter.UserID > 0 {
			args = append(args, *filter.UserID)
			clauses = append(clauses, "l.user_id = $"+itoa(len(args)))
			hasConstraint = true
		}
		if filter.APIKeyID != nil && *filter.APIKeyID > 0 {
			args = append(args, *filter.APIKeyID)
			clauses = append(clauses, "l.api_key_id = $"+itoa(len(args)))
			hasConstraint = true
		}
		if filter.AccountID != nil && *filter.AccountID > 0 {
			args = append(args, *filter.AccountID)
			clauses = append(clauses, "l.account_id = $"+itoa(len(args)))
			hasConstraint = true
		}
		if v := strings.TrimSpace(filter.Platform); v != "" {
			args = append(args, v)
			clauses = append(clauses, "COALESCE(l.platform,'') = $"+itoa(len(args)))
			hasConstraint = true
		}
		if v := strings.TrimSpace(filter.Model); v != "" {
			args = append(args, v)
			clauses = append(clauses, "COALESCE(l.model,'') = $"+itoa(len(args)))
			hasConstraint = true
		}
		if v := strings.TrimSpace(filter.Query); v != "" {
			like := "%" + v + "%"
			args = append(args, like)
			n := itoa(len(args))
			clauses = append(clauses, "(l.message ILIKE $"+n+" OR COALESCE(l.request_id,'') ILIKE $"+n+" OR COALESCE(l.client_request_id,'') ILIKE $"+n+" OR COALESCE(l.extra::text,'') ILIKE $"+n+")")
			hasConstraint = true
		}
	}

	return "WHERE " + strings.Join(clauses, " AND "), args, hasConstraint
}

func buildOpsSystemLogsCleanupWhere(filter *service.OpsSystemLogCleanupFilter) (string, []any, bool) {
	if filter == nil {
		filter = &service.OpsSystemLogCleanupFilter{}
	}
	listFilter := &service.OpsSystemLogFilter{
		StartTime:       filter.StartTime,
		EndTime:         filter.EndTime,
		Host:            filter.Host,
		Level:           filter.Level,
		Component:       filter.Component,
		RequestID:       filter.RequestID,
		ClientRequestID: filter.ClientRequestID,
		UserID:          filter.UserID,
		APIKeyID:        filter.APIKeyID,
		AccountID:       filter.AccountID,
		Platform:        filter.Platform,
		Model:           filter.Model,
		Query:           filter.Query,
	}
	return buildOpsSystemLogsWhere(listFilter)
}

// opsSanitizeText strips NUL bytes and invalid UTF-8 sequences so PostgreSQL
// TEXT/VARCHAR inserts never fail with "invalid byte sequence for encoding UTF8: 0x00".
func opsSanitizeText(s string) string {
	if strings.IndexByte(s, 0) >= 0 {
		s = strings.ReplaceAll(s, "\x00", "")
	}
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	return s
}

func opsParseHex4(s string) (uint16, bool) {
	if len(s) < 4 {
		return 0, false
	}
	var v uint16
	for i := 0; i < 4; i++ {
		c := s[i]
		var d uint16
		switch {
		case c >= '0' && c <= '9':
			d = uint16(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint16(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = uint16(c-'A') + 10
		default:
			return 0, false
		}
		v = (v << 4) | d
	}
	return v, true
}

// opsSanitizeJSONB cleans raw NUL bytes, invalid UTF-8, unescaped JSON \u0000
// (SQLSTATE 22P05), and lone UTF-16 surrogates (SQLSTATE 22P02) while preserving
// valid surrogate pairs and escaped literal sequences such as \\u0000.
func opsSanitizeJSONB(s string) string {
	s = opsSanitizeText(s)
	if !strings.Contains(s, `\u`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if i+6 <= len(s) && s[i] == '\\' && s[i+1] == 'u' {
			bs := 0
			for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
				bs++
			}
			if bs%2 == 0 {
				if code, ok := opsParseHex4(s[i+2 : i+6]); ok {
					if code == 0 {
						i += 6
						continue
					}
					if code >= 0xD800 && code <= 0xDBFF {
						if i+12 <= len(s) && s[i+6] == '\\' && s[i+7] == 'u' {
							if low, ok2 := opsParseHex4(s[i+8 : i+12]); ok2 && low >= 0xDC00 && low <= 0xDFFF {
								_, _ = b.WriteString(s[i : i+12])
								i += 12
								continue
							}
						}
						_, _ = b.WriteString(`\ufffd`)
						i += 6
						continue
					}
					if code >= 0xDC00 && code <= 0xDFFF {
						_, _ = b.WriteString(`\ufffd`)
						i += 6
						continue
					}
				}
			}
		}
		_ = b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// opsTruncateRunes truncates s to at most maxRunes Unicode code points (matching
// PostgreSQL VARCHAR(N) character-count semantics) without splitting UTF-8 sequences.
func opsTruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || len(s) <= maxRunes {
		return s
	}
	count := 0
	for i := range s {
		if count == maxRunes {
			return s[:i]
		}
		count++
	}
	return s
}

// Helpers for nullable args
func opsNullString(v any) any {
	return opsNullStringMax(v, 0)
}

func opsNullStringMax(v any, maxRunes int) any {
	var raw string
	switch s := v.(type) {
	case nil:
		return sql.NullString{}
	case *string:
		if s == nil {
			return sql.NullString{}
		}
		raw = *s
	case string:
		raw = s
	default:
		return sql.NullString{}
	}
	cleaned := strings.TrimSpace(opsSanitizeText(raw))
	if cleaned == "" {
		return sql.NullString{}
	}
	if maxRunes > 0 {
		cleaned = strings.TrimSpace(opsTruncateRunes(cleaned, maxRunes))
		if cleaned == "" {
			return sql.NullString{}
		}
	}
	return sql.NullString{String: cleaned, Valid: true}
}

func opsRequiredStringMax(s string, maxRunes int) string {
	cleaned := strings.TrimSpace(opsSanitizeText(s))
	if maxRunes > 0 {
		cleaned = strings.TrimSpace(opsTruncateRunes(cleaned, maxRunes))
	}
	return cleaned
}

func opsRequiredStringMaxOrDefault(s string, maxRunes int, fallback string) string {
	cleaned := opsRequiredStringMax(s, maxRunes)
	if cleaned == "" {
		return fallback
	}
	return cleaned
}

func opsNullJSONBString(v *string) any {
	if v == nil {
		return sql.NullString{}
	}
	cleaned := strings.TrimSpace(opsSanitizeJSONB(*v))
	if cleaned == "" || !json.Valid([]byte(cleaned)) {
		return sql.NullString{}
	}
	return sql.NullString{String: cleaned, Valid: true}
}

func opsNullInt64(v *int64) any {
	if v == nil || *v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

func opsNullInt(v any) any {
	switch n := v.(type) {
	case nil:
		return sql.NullInt64{}
	case *int:
		if n == nil || *n == 0 {
			return sql.NullInt64{}
		}
		return sql.NullInt64{Int64: int64(*n), Valid: true}
	case *int64:
		if n == nil || *n == 0 {
			return sql.NullInt64{}
		}
		return sql.NullInt64{Int64: *n, Valid: true}
	case int:
		if n == 0 {
			return sql.NullInt64{}
		}
		return sql.NullInt64{Int64: int64(n), Valid: true}
	default:
		return sql.NullInt64{}
	}
}

// opsNullableIntPointer distinguishes an absent value from an explicitly
// observed zero. Credential-stage failures intentionally persist upstream
// status 0 because no inference request was sent.
func opsNullableIntPointer(v *int) any {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}

func opsNullInt16(v *int16) any {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}
