package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// countErrorLogs5xxBySourceQuery 是 5xx 分源计数查询。
//
// 谓词 COALESCE(status_code, 0) >= 500 与 COALESCE(is_count_tokens, false) = false
// 必须与 338 迁移的部分索引谓词保持一致，规划器才能命中
// idx_ops_error_logs_5xx_time_source。
const countErrorLogs5xxBySourceQuery = `
SELECT
  FLOOR(EXTRACT(EPOCH FROM (created_at - $1)) / ($2)::numeric)::bigint AS bucket_idx,
  COUNT(*) FILTER (WHERE COALESCE(error_source, '') = 'upstream_http') AS upstream_5xx,
  COUNT(*) FILTER (WHERE COALESCE(error_source, '') <> 'upstream_http') AS gateway_5xx
FROM ops_error_logs
WHERE created_at >= $1
  AND created_at < $3
  AND COALESCE(status_code, 0) >= 500
  AND COALESCE(is_count_tokens, false) = false
  AND ($4 = '' OR platform = $4)
  AND ($5 = 0 OR group_id = $5)
GROUP BY bucket_idx
ORDER BY bucket_idx`

// CountErrorLogs5xxBySource 只读聚合 ops_error_logs 的 5xx 分源计数。
//
// 一次 created_at 范围扫描同时算出两个口径，供 ops 告警拆分指标使用：
//   - upstream_5xx：error_source='upstream_http'（上游返回并透传）；
//   - gateway_5xx：error_source<>'upstream_http'（网关/平台自产，含历史空值）。
//
// 走 338 迁移的 5xx 部分索引 idx_ops_error_logs_5xx_time_source；is_count_tokens
// 与 ops 其它错误统计一致排除。platform/group_id 过滤为可选维度（空值/0 不启用）。
func (r *opsRepository) CountErrorLogs5xxBySource(
	ctx context.Context,
	start, end time.Time,
	bucketSeconds int,
	platform string,
	groupID *int64,
) ([]*service.OpsError5xxBucket, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	if !end.After(start) {
		return nil, fmt.Errorf("invalid 5xx metric window: start=%s end=%s", start, end)
	}
	if bucketSeconds <= 0 {
		return nil, fmt.Errorf("invalid 5xx metric bucket seconds: %d", bucketSeconds)
	}

	totalSeconds := int(end.Sub(start).Seconds())
	bucketCount := (totalSeconds + bucketSeconds - 1) / bucketSeconds
	if bucketCount <= 0 {
		bucketCount = 1
	}

	platformArg := strings.TrimSpace(platform)
	var groupArg int64
	if groupID != nil && *groupID > 0 {
		groupArg = *groupID
	}

	rows, err := r.db.QueryContext(ctx, countErrorLogs5xxBySourceQuery, start, bucketSeconds, end, platformArg, groupArg)
	if err != nil {
		return nil, fmt.Errorf("query ops error logs 5xx source counts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	// 先补零桶，保证突增指标永远拿到完整的基线序列。
	buckets := make([]*service.OpsError5xxBucket, bucketCount)
	for i := range buckets {
		buckets[i] = &service.OpsError5xxBucket{
			BucketStart: start.Add(time.Duration(i*bucketSeconds) * time.Second),
		}
	}

	for rows.Next() {
		var index int64
		var upstream, gateway int64
		if err := rows.Scan(&index, &upstream, &gateway); err != nil {
			return nil, fmt.Errorf("scan ops error logs 5xx source counts: %w", err)
		}
		if index < 0 || index >= int64(bucketCount) {
			continue
		}
		buckets[int(index)].Upstream5xx = upstream
		buckets[int(index)].Gateway5xx = gateway
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ops error logs 5xx source counts: %w", err)
	}
	return buckets, nil
}
