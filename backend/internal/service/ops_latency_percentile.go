package service

import "time"

// 延迟分位数指标。
//
// 背景：`p95_latency_ms` / `p99_latency_ms` 这两条告警规则一直存在，
// 但 `computeRuleMetric` 里**从来没有对应分支**，会直接落到
// `default: return 0, false` —— 永远不会触发（前端 MetricType 里也没有这两个值，
// 但 i18n 里有文案，说明是某轮删前端时漏了后端）。
//
// 为什么不用现成的 `/debug` 直方图：`latencyHistogramBuckets` 只有 6 档，
// 最大一档是**无上界的** `2000ms+`（ops_repo_latency_histogram_buckets.go:13-20）。
// 也就是说从直方图**无法判断** p99 是否超过 3000ms —— 而那正是规则 3 的阈值。
// 所以这里改用精确分位数查询。
const (
	// OpsMetricP95LatencyMs 窗口内请求耗时的 95 分位（毫秒）。
	// 注意这是 duration_ms —— 整段流式响应的总时长，LLM 长回答天然就是几十上百秒。
	OpsMetricP95LatencyMs = "p95_latency_ms"
	// OpsMetricP99LatencyMs 窗口内请求耗时的 99 分位（毫秒）。同上为总时长。
	OpsMetricP99LatencyMs = "p99_latency_ms"
	// OpsMetricP95FirstTokenMs 窗口内首字延迟的 95 分位（毫秒）。
	// 这才是用户真正感知的「网关快不快」——总时长主要取决于模型生成长度。
	OpsMetricP95FirstTokenMs = "p95_first_token_ms"
	// OpsMetricP99FirstTokenMs 窗口内首字延迟的 99 分位（毫秒）。
	OpsMetricP99FirstTokenMs = "p99_first_token_ms"
)

// opsLatencyPercentileMaxWindow 限制分位数查询的时间窗。
//
// 分位数需要对窗口内的行排序，而 usage_logs 线上 80+ GB / 近四千万行。
// 评估器整体只有 45s 预算且按规则串行，所以窗口必须封顶。
const opsLatencyPercentileMaxWindow = 6 * time.Hour

// OpsLatencyPercentiles 是一次只读采样。
type OpsLatencyPercentiles struct {
	// P95Ms / P99Ms 基于 duration_ms（整段流式响应的总时长），单位毫秒。
	P95Ms float64
	P99Ms float64
	// FirstTokenP95Ms / FirstTokenP99Ms 基于 first_token_ms（首字延迟），单位毫秒。
	FirstTokenP95Ms float64
	FirstTokenP99Ms float64
	// SampleCount 是窗口内 duration_ms 非空的样本数。
	// 为 0 时上面的分位数都没有意义，调用方应视为「指标不可用」。
	SampleCount int64
	// FirstTokenSampleCount 是窗口内 first_token_ms 非空的样本数。
	// 非流式请求没有首字延迟，所以它通常小于 SampleCount。
	FirstTokenSampleCount int64
}
