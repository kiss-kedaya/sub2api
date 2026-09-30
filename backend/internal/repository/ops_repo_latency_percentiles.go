package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// GetLatencyPercentiles 计算窗口内请求耗时的精确分位数。
//
// 只读，且不在请求热路径上（只由 OpsAlertEvaluatorService 周期调用）。
//
// 为什么不用 GetLatencyHistogram：那套桶最大一档是无上界的 `2000ms+`，
// 推不出 p99 是否超过 3000ms。分位数必须精确算，否则规则形同虚设。
//
// 性能：`percentile_cont ... WITHIN GROUP` 需要对窗口内的行排序，所以
// 时间范围必须走 ul.created_at 索引、且窗口由调用方封顶
// （service.opsLatencyPercentileMaxWindow）。
func (r *opsRepository) GetLatencyPercentiles(
	ctx context.Context,
	filter *service.OpsDashboardFilter,
	start, end time.Time,
) (*service.OpsLatencyPercentiles, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	if start.IsZero() || end.IsZero() || !end.After(start) {
		return nil, fmt.Errorf("invalid latency percentile window")
	}

	join, where, args, _ := buildUsageWhere(filter, start.UTC(), end.UTC(), 1)

	// duration_ms 为 NULL 的行（例如上游没回报耗时）不参与统计，
	// 与 GetLatencyHistogram 的口径保持一致。
	//
	// first_token_ms 单独统计：非流式请求没有首字延迟，所以它的样本数会小于总数，
	// 不能和 duration_ms 混在一个 FILTER 里。
	q := `
SELECT
  COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY ul.duration_ms), 0) AS p95_ms,
  COALESCE(PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY ul.duration_ms), 0) AS p99_ms,
  COUNT(ul.duration_ms) AS sample_count,
  COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY ul.first_token_ms), 0) AS ttfb_p95_ms,
  COALESCE(PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY ul.first_token_ms), 0) AS ttfb_p99_ms,
  COUNT(ul.first_token_ms) AS ttfb_sample_count
FROM usage_logs ul
` + join + `
` + where

	out := &service.OpsLatencyPercentiles{}
	if err := r.db.QueryRowContext(ctx, q, args...).Scan(
		&out.P95Ms, &out.P99Ms, &out.SampleCount,
		&out.FirstTokenP95Ms, &out.FirstTokenP99Ms, &out.FirstTokenSampleCount,
	); err != nil {
		return nil, fmt.Errorf("latency percentiles: %w", err)
	}
	return out, nil
}
