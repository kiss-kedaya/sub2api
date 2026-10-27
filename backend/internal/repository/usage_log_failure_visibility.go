package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const usageFailureBatchSize = 100

// HydrateUsageLogFailures adds conservatively correlated terminal error metadata to an
// already-authorized page. This is not used by the gateway or billing writes.
// The separate index probes avoid an OR across unrelated request-ID indexes.
func (r *usageLogRepository) HydrateUsageLogFailures(ctx context.Context, logs []service.UsageLog) error {
	for start := 0; start < len(logs); start += usageFailureBatchSize {
		end := min(start+usageFailureBatchSize, len(logs))
		if err := r.hydrateUsageFailureBatch(ctx, logs[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (r *usageLogRepository) hydrateUsageFailureBatch(ctx context.Context, logs []service.UsageLog) error {
	values := make([]string, 0, len(logs))
	args := make([]any, 0, len(logs)*8)
	byID := make(map[int64]int, len(logs))
	for i := range logs {
		log := &logs[i]
		log.FailureStatusCode = nil
		requestID := strings.TrimSpace(log.RequestID)
		if log.ID <= 0 || log.UserID <= 0 || log.APIKeyID <= 0 || requestID == "" || log.CreatedAt.IsZero() {
			continue
		}
		clientRequestID := service.NormalizeUsageRequestIDKey(requestID)
		n := len(args)
		values = append(values, fmt.Sprintf("($%d::bigint,$%d::bigint,$%d::bigint,$%d::bigint,$%d::text,$%d::text,$%d::timestamptz,$%d::integer)", n+1, n+2, n+3, n+4, n+5, n+6, n+7, n+8))
		duration := sql.NullInt64{}
		if log.DurationMs != nil && *log.DurationMs >= 0 {
			duration = sql.NullInt64{Int64: int64(*log.DurationMs), Valid: true}
		}
		args = append(args, log.ID, log.UserID, log.APIKeyID, log.AccountID, requestID, clientRequestID, log.CreatedAt, duration)
		byID[log.ID] = i
	}
	if len(values) == 0 {
		return nil
	}

	// User/key/account scope, a one-second completion window and elapsed-time
	// agreement when both sides recorded it avoid borrowing a previous request's
	// failure. This is best-effort correlation, not a durable success boolean.
	// Nearby usage with the same caller-controlled ID makes the result ambiguous.
	// Recovered upstream attempts retain wire 2xx and must never mark paid usage
	// as failed. Error text, body, URLs, and account metadata are not selected.
	scope := `e.user_id = wanted.user_id
      AND e.api_key_id = wanted.api_key_id
      AND e.account_id = wanted.account_id
      AND e.status_code BETWEEN 400 AND 599
      AND e.created_at >= wanted.ended_at - INTERVAL '1 second'
      AND e.created_at <= wanted.ended_at + INTERVAL '1 second'
      AND (e.duration_ms IS NULL OR wanted.duration_ms IS NULL
           OR ABS(e.duration_ms::bigint - wanted.duration_ms::bigint) <= 1000)`
	query := `WITH wanted(id,user_id,api_key_id,account_id,request_id,client_request_id,ended_at,duration_ms) AS (VALUES ` + strings.Join(values, ",") + `)
SELECT wanted.id, failure.status_code
FROM wanted
CROSS JOIN LATERAL (
  SELECT matched.status_code
  FROM (
    SELECT e.status_code,e.created_at FROM ops_error_logs e
    WHERE e.request_id IN (wanted.request_id, wanted.client_request_id) AND ` + scope + `
    UNION ALL
    SELECT e.status_code,e.created_at FROM ops_error_logs e
    WHERE e.client_request_id = wanted.client_request_id AND ` + scope + `
  ) matched
  ORDER BY ABS(EXTRACT(EPOCH FROM (matched.created_at - wanted.ended_at))), matched.created_at DESC
  LIMIT 1
) failure
WHERE NOT EXISTS (
  SELECT 1 FROM usage_logs sibling
  WHERE sibling.request_id IN (wanted.request_id, wanted.client_request_id,
        'client:' || wanted.client_request_id, 'local:' || wanted.client_request_id)
    AND sibling.api_key_id = wanted.api_key_id
    AND sibling.user_id = wanted.user_id
    AND sibling.account_id = wanted.account_id
    AND sibling.id <> wanted.id
    AND sibling.created_at >= wanted.ended_at - INTERVAL '1 second'
    AND sibling.created_at <= wanted.ended_at + INTERVAL '1 second'
)`
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var status int
		if err := rows.Scan(&id, &status); err != nil {
			return err
		}
		if i, ok := byID[id]; ok && status >= 400 && status <= 599 {
			logs[i].FailureStatusCode = &status
		}
	}
	return rows.Err()
}
