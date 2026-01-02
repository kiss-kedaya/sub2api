package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// ListErrorLogs queries ops_error_logs with optional filters and pagination.
// It returns the list items and the total count of matching rows.
func (r *OpsRepository) ListErrorLogs(ctx context.Context, filter *service.ErrorLogFilter) ([]*service.ErrorLog, int64, error) {
	page := 1
	pageSize := 20
	if filter != nil {
		if filter.Page > 0 {
			page = filter.Page
		}
		if filter.PageSize > 0 {
			pageSize = filter.PageSize
		}
	}
	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	conditions := make([]string, 0)
	args := make([]any, 0)

	addCondition := func(condition string, values ...any) {
		conditions = append(conditions, condition)
		args = append(args, values...)
	}

	if filter != nil {
		// 默认查询最近 24 小时
		if filter.StartTime == nil && filter.EndTime == nil {
			defaultStart := time.Now().Add(-24 * time.Hour)
			filter.StartTime = &defaultStart
		}

		if filter.StartTime != nil {
			addCondition(fmt.Sprintf("created_at >= $%d", len(args)+1), *filter.StartTime)
		}
		if filter.EndTime != nil {
			addCondition(fmt.Sprintf("created_at <= $%d", len(args)+1), *filter.EndTime)
		}
		if filter.ErrorCode != nil {
			addCondition(fmt.Sprintf("status_code = $%d", len(args)+1), *filter.ErrorCode)
		}
		if provider := strings.TrimSpace(filter.Provider); provider != "" {
			addCondition(fmt.Sprintf("platform = $%d", len(args)+1), provider)
		}
		if filter.AccountID != nil {
			addCondition(fmt.Sprintf("account_id = $%d", len(args)+1), *filter.AccountID)
		}
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	countQuery := fmt.Sprintf(`SELECT COUNT(1) FROM ops_error_logs %s`, where)
	var total int64
	if err := scanSingleRow(ctx, r.sql, countQuery, args, &total); err != nil {
		if err == sql.ErrNoRows {
			total = 0
		} else {
			return nil, 0, err
		}
	}

	listQuery := fmt.Sprintf(`
		SELECT
			id,
			created_at,
			severity,
			request_id,
			account_id,
			request_path,
			platform,
			model,
			status_code,
			error_message,
			duration_ms,
			retry_count,
			stream
		FROM ops_error_logs
		%s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, where, len(args)+1, len(args)+2)

	listArgs := append(append([]any{}, args...), pageSize, offset)
	rows, err := r.sql.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	results := make([]*service.ErrorLog, 0)
	for rows.Next() {
		var (
			id         int64
			createdAt  time.Time
			severity   sql.NullString
			requestID  sql.NullString
			accountID  sql.NullInt64
			requestURI sql.NullString
			platform   sql.NullString
			model      sql.NullString
			statusCode sql.NullInt64
			message    sql.NullString
			durationMs sql.NullInt64
			retryCount sql.NullInt64
			stream     sql.NullBool
		)

		if err := rows.Scan(
			&id,
			&createdAt,
			&severity,
			&requestID,
			&accountID,
			&requestURI,
			&platform,
			&model,
			&statusCode,
			&message,
			&durationMs,
			&retryCount,
			&stream,
		); err != nil {
			return nil, 0, err
		}

		entry := &service.ErrorLog{
			ID:        id,
			Timestamp: createdAt,
			Level:     levelFromSeverity(severity.String),
			RequestID: requestID.String,
			APIPath:   requestURI.String,
			Provider:  platform.String,
			Model:     model.String,
			HTTPCode:  int(statusCode.Int64),
			Stream:    stream.Bool,
		}
		if accountID.Valid {
			entry.AccountID = strconv.FormatInt(accountID.Int64, 10)
		}
		if message.Valid {
			entry.ErrorMessage = message.String
		}
		if durationMs.Valid {
			v := int(durationMs.Int64)
			entry.DurationMs = &v
		}
		if retryCount.Valid {
			v := int(retryCount.Int64)
			entry.RetryCount = &v
		}

		results = append(results, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return results, total, nil
}

func levelFromSeverity(severity string) string {
	sev := strings.ToUpper(strings.TrimSpace(severity))
	switch sev {
	case "P0", "P1":
		return "CRITICAL"
	case "P2":
		return "ERROR"
	case "P3":
		return "WARN"
	default:
		return "ERROR"
	}
}
