package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// GetBillingAnomalySnapshot 采样一次计费异常指标。
//
// 只读：全部是 SELECT 聚合，不写任何表。
//
// 性能约束（线上 usage_logs 约 80+ GB / 四千万行）：
//   - 用一个 created_at 范围扫描同时覆盖「本窗」和「上一个等长窗」，
//     只走一次索引（idx_usage_logs_created_at），避免两次全窗扫描。
//   - 调用方（评估器）负责把窗口限制在 opsBillingMetricMaxWindow 之内。
func (r *opsRepository) GetBillingAnomalySnapshot(
	ctx context.Context,
	start, end time.Time,
) (*service.BillingAnomalySnapshot, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	if !end.After(start) {
		return nil, fmt.Errorf("invalid billing anomaly window: start=%s end=%s", start, end)
	}

	window := end.Sub(start)
	prevStart := start.Add(-window)

	snapshot := &service.BillingAnomalySnapshot{
		WindowStart: start,
		WindowEnd:   end,
	}

	// 一次范围扫描同时算本窗与上一窗。FILTER 让同一批行服务四个聚合。
	const usageQuery = `
SELECT
  COUNT(*) FILTER (
    WHERE created_at >= $1 AND created_at < $2
      AND (input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) > 0
  ) AS metered_requests,
  COUNT(*) FILTER (
    WHERE created_at >= $1 AND created_at < $2
      AND (input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) > 0
      AND actual_cost = 0
  ) AS zero_cost_requests,
  COALESCE(SUM(actual_cost) FILTER (WHERE created_at >= $1 AND created_at < $2), 0) AS window_cost,
  COALESCE(SUM(actual_cost) FILTER (WHERE created_at >= $3 AND created_at < $1), 0) AS previous_window_cost,
  COUNT(*) FILTER (
    WHERE created_at >= $3 AND created_at < $1
      AND (input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens) > 0
      AND actual_cost = 0
  ) AS previous_zero_cost_requests
FROM usage_logs
WHERE created_at >= $3 AND created_at < $2`

	if err := r.db.QueryRowContext(ctx, usageQuery, start, end, prevStart).Scan(
		&snapshot.MeteredRequests,
		&snapshot.ZeroCostRequests,
		&snapshot.WindowCostUSD,
		&snapshot.PreviousWindowCostUSD,
		&snapshot.PreviousWindowZeroCostRequests,
	); err != nil {
		return nil, fmt.Errorf("billing anomaly usage window: %w", err)
	}

	// users 只有万级行，且该查询只用于告警评估，不在请求热路径上。
	const negativeBalanceQuery = `
SELECT COUNT(*)
FROM users
WHERE balance < 0 AND deleted_at IS NULL`

	if err := r.db.QueryRowContext(ctx, negativeBalanceQuery).Scan(&snapshot.NegativeBalanceUsers); err != nil {
		return nil, fmt.Errorf("billing anomaly negative balance users: %w", err)
	}

	// 走 idx_billing_balance_settlements_authorization_recovery (expires_at, id)
	// WHERE status IN (0,1)：过期但从未结算的预扣，说明预扣泄漏。
	const stuckHoldsQuery = `
SELECT COUNT(*)
FROM billing_balance_settlements
WHERE status IN (0, 1)
  AND expires_at IS NOT NULL
  AND expires_at < NOW()`

	if err := r.db.QueryRowContext(ctx, stuckHoldsQuery).Scan(&snapshot.StuckHolds); err != nil {
		return nil, fmt.Errorf("billing anomaly stuck holds: %w", err)
	}

	return snapshot, nil
}
