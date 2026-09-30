package service

import "time"

// 计费看门狗用的只读指标。
//
// 设计约束（重要）：
//   - 只读。这些指标只做 SELECT 聚合，**绝不修改余额、预扣、用量行**。
//   - 不进请求热路径。它们只由 OpsAlertEvaluatorService 周期性调用。
//   - 查询必须走索引。usage_logs 有 idx_usage_logs_created_at，所有窗口查询
//     都用 created_at 做范围限制；billing_balance_settlements 有
//     idx_billing_balance_settlements_authorization_recovery (expires_at,id)
//     专门覆盖「status IN (0,1) 且已过期」。
const (
	// OpsMetricBillingZeroCostRequests 窗内「有用量但实际扣费为 0」的请求数。
	// 这是「用了不扣费」的直接信号。
	OpsMetricBillingZeroCostRequests = "billing_zero_cost_requests"
	// OpsMetricBillingZeroCostRatio 同上，按「有用量的请求」为分母的百分比。
	// 绝对量随流量波动，比例更适合设阈值告警。
	OpsMetricBillingZeroCostRatio = "billing_zero_cost_ratio"
	// OpsMetricBillingNegativeBalanceUsers 余额为负的未删除用户数。
	OpsMetricBillingNegativeBalanceUsers = "billing_negative_balance_users"
	// OpsMetricBillingCostSpikeRatio 本窗总扣费 / 上一等长窗总扣费 × 100。
	// 上一窗为 0（无数据）时该指标不可用，规则不会触发。
	OpsMetricBillingCostSpikeRatio = "billing_cost_spike_ratio"
	// OpsMetricBillingStuckHolds 已过期但从未结算的预扣笔数。
	// 持续增长说明预扣泄漏（用户被占住额度或漏扣）。
	OpsMetricBillingStuckHolds = "billing_stuck_holds"

	// OpsMetricBillingZeroCostRequestsDelta 本窗零扣费请求数 − 上一等长窗。
	//
	// 为什么要有它：`billing_zero_cost_requests` 是绝对值，而生产上它**长期非零**
	// （某个未定价的模型持续产生零扣费请求，约 600 次/小时）。用「> 0」当阈值
	// 等于每小时发一封 P0，直接变成告警疲劳。增量指标在稳态下约为 0，
	// 只有出现真正的突增才告警。
	OpsMetricBillingZeroCostRequestsDelta = "billing_zero_cost_requests_delta"

	// OpsMetricBillingNegativeBalanceUsersDelta 负余额用户数相对基线的增量。
	//
	// 负余额用户数是**存量指标**，没有「上一个窗口」可言，所以基线存 Redis
	// （每小时重置一次，见 negativeBalanceBaselineTTL）。这样稳态的缓慢累积
	// 不会报警，只有明显的新增才报。
	OpsMetricBillingNegativeBalanceUsersDelta = "billing_negative_balance_users_delta"
)

// opsBillingMetricMaxWindow 限制计费指标实际扫描的时间窗。
//
// usage_logs 线上约 80+ GB / 四千万行，评估器整体只有 45s 预算且按规则串行执行，
// 一个超大窗口会把其它规则一起拖死。超过该上限时按上限截断并在日志里留痕。
const opsBillingMetricMaxWindow = 6 * time.Hour

// BillingAnomalySnapshot 是一次只读采样。
type BillingAnomalySnapshot struct {
	// 本窗（[WindowStart, WindowEnd)）
	WindowStart time.Time
	WindowEnd   time.Time

	// MeteredRequests：本窗内四类 token 之和 > 0 的请求数。
	MeteredRequests int64
	// ZeroCostRequests：本窗内有用量但 actual_cost = 0 的请求数。
	ZeroCostRequests int64
	// WindowCostUSD：本窗 sum(actual_cost)。
	WindowCostUSD float64
	// PreviousWindowCostUSD：紧邻的上一个等长窗口 sum(actual_cost)。
	PreviousWindowCostUSD float64

	// PreviousWindowZeroCostRequests：上一等长窗内「有用量但 actual_cost = 0」的请求数。
	PreviousWindowZeroCostRequests int64

	// NegativeBalanceUsers：balance < 0 且未软删的用户数。
	NegativeBalanceUsers int64

	// StuckHolds：状态为 prepared/authorized 且 expires_at 已过期的预扣笔数。
	StuckHolds int64
}

// ZeroCostRatio 返回「有用量但不扣费」的百分比；无样本时返回 0。
func (s *BillingAnomalySnapshot) ZeroCostRatio() float64 {
	if s == nil || s.MeteredRequests <= 0 {
		return 0
	}
	return float64(s.ZeroCostRequests) / float64(s.MeteredRequests) * 100
}

// CostSpikeRatio 返回本窗相对上一窗的费用百分比。
// 第二个返回值为 false 表示上一窗没有可用的费用基线（此时不应对该指标告警，
// 否则会把「上一窗恰好没流量」误报成「费用暴涨」）。
func (s *BillingAnomalySnapshot) CostSpikeRatio() (float64, bool) {
	if s == nil || s.PreviousWindowCostUSD <= 0 {
		return 0, false
	}
	return s.WindowCostUSD / s.PreviousWindowCostUSD * 100, true
}

// ZeroCostRequestDelta 返回本窗相对上一窗的零扣费请求数增量。
// 稳态下接近 0；明显为正说明零扣费在突增。
func (s *BillingAnomalySnapshot) ZeroCostRequestDelta() float64 {
	if s == nil {
		return 0
	}
	return float64(s.ZeroCostRequests - s.PreviousWindowZeroCostRequests)
}
