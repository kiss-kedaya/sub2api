// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// One statement atomically inserts the event and increments its UTC hour only
// when newly inserted. Retrying after an unknown commit outcome is safe.
const insertTrafficSQLTemplate = `WITH inserted AS (
 INSERT INTO user_traffic_events
 (event_id,user_id,api_key_id,group_id,request_id,endpoint,method,started_at,finished_at,
 upload_bytes,download_bytes,status_code,outcome,request_complete,measurement_version)
 VALUES %s
 ON CONFLICT (event_id) DO NOTHING
 RETURNING *
)
INSERT INTO user_traffic_hourly(bucket_start,user_id,api_key_id,group_id,upload_bytes,download_bytes,requests)
SELECT date_trunc('hour',started_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC',
 user_id,api_key_id,group_id,SUM(upload_bytes),SUM(download_bytes),COUNT(*) FROM inserted
GROUP BY 1,2,3,4
ORDER BY 2,3,4,1
ON CONFLICT (user_id,api_key_id,group_id,bucket_start) DO UPDATE SET
 upload_bytes=user_traffic_hourly.upload_bytes+EXCLUDED.upload_bytes,
 download_bytes=user_traffic_hourly.download_bytes+EXCLUDED.download_bytes,
 requests=user_traffic_hourly.requests+EXCLUDED.requests`

func (r *usageLogRepository) RecordTraffic(ctx context.Context, e service.TrafficEvent) error {
	return r.RecordTrafficBatch(ctx, []service.TrafficEvent{e})
}

// RecordTrafficBatch commits an entire replay batch atomically. Grouping the
// inserted rows avoids updating the same hourly row twice in one SQL statement.
func (r *usageLogRepository) RecordTrafficBatch(ctx context.Context, events []service.TrafficEvent) error {
	if len(events) == 0 {
		return nil
	}
	if len(events) > 128 {
		return errors.New("traffic batch too large")
	}
	if r.sql == nil {
		return errors.New("traffic database unavailable")
	}
	values := make([]string, 0, len(events))
	args := make([]any, 0, len(events)*15)
	for _, e := range events {
		if err := service.ValidateTrafficEvent(e); err != nil {
			return err
		}
		placeholders := make([]string, 15)
		for i := range placeholders {
			placeholders[i] = fmt.Sprintf("$%d", len(args)+i+1)
		}
		values = append(values, "("+strings.Join(placeholders, ",")+")")
		args = append(args, e.EventID, e.UserID, e.APIKeyID, e.GroupID, e.RequestID, e.Endpoint, e.Method, e.StartedAt, e.FinishedAt, e.UploadBytes, e.DownloadBytes, e.StatusCode, e.Outcome, e.RequestComplete, e.MeasurementVersion)
	}
	query := fmt.Sprintf(insertTrafficSQLTemplate, strings.Join(values, ","))
	_, err := r.sql.ExecContext(ctx, query, args...)
	return err
}

func (r *usageLogRepository) TrafficKeyOptions(ctx context.Context, userID int64, query string) ([]service.TrafficKeyOption, error) {
	if r.sql == nil {
		return nil, errors.New("traffic database unavailable")
	}
	// Explicit columns keep credentials out of history filters. Deletion does
	// not revoke the owner's ability to inspect their historical traffic.
	scope := ""
	match := "name ILIKE $1"
	args := []any{"%" + query + "%"}
	if strings.HasPrefix(query, "#") {
		if id, err := strconv.ParseInt(strings.TrimPrefix(query, "#"), 10, 64); err == nil && id > 0 {
			match, args = "id=$1", []any{id}
		}
	}
	if userID > 0 {
		scope = " AND user_id=$2"
		args = append(args, userID)
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT id,name,deleted_at IS NOT NULL FROM api_keys WHERE `+match+scope+` ORDER BY (deleted_at IS NOT NULL),name,id LIMIT 30`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	options := []service.TrafficKeyOption{}
	for rows.Next() {
		var option service.TrafficKeyOption
		if err := rows.Scan(&option.ID, &option.Name, &option.Deleted); err != nil {
			return nil, err
		}
		options = append(options, option)
	}
	return options, rows.Err()
}

func trafficScope(f service.TrafficFilter, args []any) (string, []any) {
	var conditions []string
	for _, v := range []struct {
		name string
		id   int64
	}{{"user_id", f.UserID}, {"api_key_id", f.APIKeyID}} {
		if v.id > 0 {
			args = append(args, v.id)
			conditions = append(conditions, fmt.Sprintf("%s=$%d", v.name, len(args)))
		}
	}
	if len(conditions) == 0 {
		return "", args
	}
	return " AND " + strings.Join(conditions, " AND "), args
}

// Each local bucket independently combines whole UTC hours with raw boundary
// fragments. This preserves both DST hours and fractional-offset day boundaries.
func trafficSource(f service.TrafficFilter) (string, []any) {
	var values []string
	var args []any
	for _, b := range service.TrafficBuckets(f) {
		wholeStart := b.Start.UTC().Truncate(time.Hour)
		if wholeStart.Before(b.Start) {
			wholeStart = wholeStart.Add(time.Hour)
		}
		wholeEnd := b.End.UTC().Truncate(time.Hour)
		n := len(args)
		values = append(values, fmt.Sprintf("($%d::text,$%d::timestamptz,$%d::timestamptz,$%d::timestamptz,$%d::timestamptz)", n+1, n+2, n+3, n+4, n+5))
		args = append(args, b.Label, b.Start, b.End, wholeStart, wholeEnd)
	}
	scope, args := trafficScope(f, args)
	return `WITH buckets(label,start_at,end_at,whole_start,whole_end) AS (VALUES ` + strings.Join(values, ",") + `)
 SELECT b.label,COALESCE(SUM(t.upload_bytes),0),COALESCE(SUM(t.download_bytes),0),COALESCE(SUM(t.requests),0)
 FROM buckets b LEFT JOIN LATERAL (
 SELECT upload_bytes,download_bytes,requests FROM user_traffic_hourly
 WHERE bucket_start >= b.whole_start AND bucket_start < b.whole_end` + scope + `
 UNION ALL
 SELECT upload_bytes,download_bytes,1 AS requests FROM user_traffic_events
 WHERE started_at >= b.start_at AND started_at < b.end_at
 AND (started_at < b.whole_start OR started_at >= b.whole_end)` + scope + `
 ) t ON TRUE GROUP BY b.label,b.start_at ORDER BY b.start_at`, args
}

func (r *usageLogRepository) Traffic(ctx context.Context, f service.TrafficFilter) (*service.TrafficReport, error) {
	if f.Page < 1 || f.Page > 10000 || f.PageSize < 1 || f.PageSize > 100 || !f.End.After(f.Start) {
		return nil, errors.New("invalid traffic filter")
	}
	if r.db == nil {
		return nil, errors.New("traffic database unavailable")
	}
	// Totals, trend and page represent the same database snapshot even while new
	// events arrive. Reads never hold a lock over request processing.
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	out := &service.TrafficReport{Items: []service.TrafficEvent{}, Trend: []service.TrafficPoint{},
		Page: f.Page, PageSize: f.PageSize, Period: f.Period, Start: f.Start, End: f.End,
		Timezone: timezone.Location().String(), MeasurementVersion: 1}
	if f.Location != nil {
		out.Timezone = f.Location.String()
	}
	if f.View != "details" {
		if err := trafficSummary(ctx, tx, f, out); err != nil {
			return nil, err
		}
	}
	if f.View != "summary" {
		if err := trafficDetails(ctx, tx, f, out); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func trafficSummary(ctx context.Context, tx *sql.Tx, f service.TrafficFilter, out *service.TrafficReport) error {
	source, args := trafficSource(f)
	rows, err := tx.QueryContext(ctx, source, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p service.TrafficPoint
		if err := rows.Scan(&p.Bucket, &p.UploadBytes, &p.DownloadBytes, &p.Requests); err != nil {
			_ = rows.Close()
			return err
		}
		p.TotalBytes = p.UploadBytes + p.DownloadBytes
		out.UploadBytes += p.UploadBytes
		out.DownloadBytes += p.DownloadBytes
		out.Requests += p.Requests
		out.Trend = append(out.Trend, p)
	}
	out.TotalBytes = out.UploadBytes + out.DownloadBytes
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	scope, sinceArgs := trafficScope(f, nil)
	var since sql.NullTime
	if err := tx.QueryRowContext(ctx, `SELECT MIN(started_at) FROM user_traffic_events WHERE TRUE`+scope, sinceArgs...).Scan(&since); err != nil {
		return err
	}
	if since.Valid {
		out.RecordedSince = &since.Time
	}
	return nil
}

func trafficDetails(ctx context.Context, tx *sql.Tx, f service.TrafficFilter, out *service.TrafficReport) error {
	scope, detailArgs := trafficScope(f, []any{f.Start, f.End})
	if f.Cursor != nil {
		n := len(detailArgs)
		scope += fmt.Sprintf(" AND (started_at,event_id) < ($%d::timestamptz,$%d::uuid)", n+1, n+2)
		detailArgs = append(detailArgs, f.Cursor.StartedAt, f.Cursor.EventID)
	}
	detailArgs = append(detailArgs, f.PageSize+1)
	pagination := fmt.Sprintf(" LIMIT $%d", len(detailArgs))
	if f.Cursor == nil && f.Page > 1 {
		detailArgs = append(detailArgs, (f.Page-1)*f.PageSize)
		pagination += fmt.Sprintf(" OFFSET $%d", len(detailArgs))
	}
	rows, err := tx.QueryContext(ctx, `SELECT event_id,user_id,api_key_id,group_id,request_id,endpoint,method,
 started_at,finished_at,upload_bytes,download_bytes,status_code,outcome,request_complete,measurement_version
 FROM user_traffic_events WHERE started_at >= $1 AND started_at < $2`+scope+
		` ORDER BY started_at DESC,event_id DESC`+pagination, detailArgs...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var e service.TrafficEvent
		if err := rows.Scan(&e.EventID, &e.UserID, &e.APIKeyID, &e.GroupID, &e.RequestID, &e.Endpoint, &e.Method,
			&e.StartedAt, &e.FinishedAt, &e.UploadBytes, &e.DownloadBytes, &e.StatusCode, &e.Outcome, &e.RequestComplete, &e.MeasurementVersion); err != nil {
			_ = rows.Close()
			return err
		}
		out.Items = append(out.Items, e)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	if len(out.Items) > f.PageSize {
		out.HasMore = true
		out.Items = out.Items[:f.PageSize]
		out.NextCursor = service.EncodeTrafficCursor(out.Items[len(out.Items)-1])
	}
	return nil
}
