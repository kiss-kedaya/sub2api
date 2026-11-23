package service

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Ops 5xx 告警指标：把「上游透传 5xx」与「网关自产 5xx」拆成两个计数指标。
//
// 背景：旧口径把 op_error_logs 里所有 status_code>=500 都算成网关故障，上游返回并
// 透传的 503/502/504 会把「渠道抖动」误报成「网关自身故障」。
//
// 数据源：ops_error_logs（只读聚合，不写任何表）。
//
//	upstream_5xx            = status_code>=500 AND error_source = 'upstream_http'
//	gateway_5xx             = status_code>=500 AND error_source <> 'upstream_http'
//	upstream_5xx_spike_ratio = 当前 5 分钟桶 upstream_5xx / 前 30 分钟（6 个桶）中位数
//
// 口径边界（与迁移 337 注释、PR 描述保持一致）：
//   - ops_error_logs 只记录走到应用层、且未被 ops 忽略设置过滤的错误；
//     nginx/CDN 层直接 502、客户端断连、被过滤的 count_tokens 等不在统计内。
//   - error_source=upstream_http 表示「上游侧错误」（上游返回或上游网络失败后被透传），
//     其余（gateway/client_request/空值）都算「我方产出」，客户端最终看到的是 5xx。
//   - is_count_tokens=true 的请求不计入，与 ops 其它错误统计口径一致。
const (
	// OpsMetricGateway5xx 网关/平台自产 5xx 条数（真故障指标）。
	OpsMetricGateway5xx = "gateway_5xx"
	// OpsMetricUpstream5xx 上游返回并透传的 5xx 条数（渠道健康指标）。
	OpsMetricUpstream5xx = "upstream_5xx"
	// OpsMetricUpstream5xxSpike 上游 5xx 相对 30 分钟中位数的突增倍数（观察类）。
	OpsMetricUpstream5xxSpike = "upstream_5xx_spike_ratio"

	// opsError5xxUpstreamSource 是 ops_error_logs.error_source 中「上游透传」的标准值。
	opsError5xxUpstreamSource = "upstream_http"

	// 突增观察固定用 5 分钟桶 × 前 6 个桶（30 分钟）做基线。
	opsError5xxSpikeBucketSeconds  = 300
	opsError5xxSpikeBaselineBucket = 6

	// 兜底上限：规则窗口过大时不至于把评估器 45s 预算吃光。
	opsError5xxMaxBuckets      = 288 // 24h / 5m 桶
	opsError5xxMaxWindow       = 6 * time.Hour
	opsError5xxMinBucketSecond = 1
)

// OpsError5xxBucket 是 ops_error_logs 的 5xx 分源计数桶。
type OpsError5xxBucket struct {
	BucketStart time.Time
	// Upstream5xx 是 error_source='upstream_http' 的 5xx 条数。
	Upstream5xx int64
	// Gateway5xx 是 error_source<>'upstream_http'（含空值）的 5xx 条数。
	Gateway5xx int64
}

// Total 返回该桶 5xx 总条数。
func (b *OpsError5xxBucket) Total() int64 {
	if b == nil {
		return 0
	}
	return b.Upstream5xx + b.Gateway5xx
}

// UpstreamSharePercent 返回上游透传 5xx 占该桶 5xx 的百分比；无 5xx 时返回 0。
func (b *OpsError5xxBucket) UpstreamSharePercent() float64 {
	total := b.Total()
	if total <= 0 {
		return 0
	}
	return float64(b.Upstream5xx) / float64(total) * 100
}

// isOpsError5xxMetricType 判断是否为 5xx 分源计数/突增指标。
func isOpsError5xxMetricType(metricType string) bool {
	switch strings.TrimSpace(metricType) {
	case OpsMetricGateway5xx, OpsMetricUpstream5xx, OpsMetricUpstream5xxSpike:
		return true
	default:
		return false
	}
}

// opsError5xxMetricValue 把分桶结果折算成规则指标值。
//
// 计数指标对窗口内所有桶求和（正常只有 1 个桶）；突增指标取最后一个桶与
// 前 N 个桶中位数的比值。
func opsError5xxMetricValue(metricType string, buckets []*OpsError5xxBucket) (float64, bool) {
	metricType = strings.TrimSpace(metricType)
	if len(buckets) == 0 {
		return 0, false
	}

	var upstream, gateway int64
	for _, bucket := range buckets {
		if bucket == nil {
			continue
		}
		upstream += bucket.Upstream5xx
		gateway += bucket.Gateway5xx
	}

	switch metricType {
	case OpsMetricUpstream5xx:
		return float64(upstream), true
	case OpsMetricGateway5xx:
		return float64(gateway), true
	case OpsMetricUpstream5xxSpike:
		if len(buckets) < 2 {
			return 0, false
		}
		current := buckets[len(buckets)-1]
		if current == nil {
			return 0, false
		}
		baseline := make([]int64, 0, len(buckets)-1)
		for _, bucket := range buckets[:len(buckets)-1] {
			if bucket == nil {
				continue
			}
			baseline = append(baseline, bucket.Upstream5xx)
		}
		if len(baseline) == 0 {
			return 0, false
		}
		return computeUpstream5xxSpikeRatio(current.Upstream5xx, baseline)
	default:
		return 0, false
	}
}

// opsError5xxMetricDetail 生成告警描述里的口径提示。
//
// 文案前缀区分【网关自身 5xx】/【上游 503 透传】，并给出 error_source 分布，
// 让值班人员一眼看出这条告警是「我们故障」还是「上游渠道抖动」。
func opsError5xxMetricDetail(metricType string, buckets []*OpsError5xxBucket) string {
	var upstream, gateway int64
	for _, bucket := range buckets {
		if bucket == nil {
			continue
		}
		upstream += bucket.Upstream5xx
		gateway += bucket.Gateway5xx
	}
	total := upstream + gateway
	share := func(n int64) float64 {
		if total <= 0 {
			return 0
		}
		return float64(n) / float64(total) * 100
	}

	switch strings.TrimSpace(metricType) {
	case OpsMetricGateway5xx:
		return fmt.Sprintf(
			"【网关自身 5xx】窗口内 5xx 共 %d 条，其中上游透传 %d 条（error_source=upstream_http，不计入本指标），网关自产 %d 条（error_source=gateway/client_request/空值，占比 %.1f%%）",
			total, upstream, gateway, share(gateway),
		)
	case OpsMetricUpstream5xx:
		return fmt.Sprintf(
			"【上游 503 透传】窗口内上游返回并透传的 5xx 共 %d 条（error_source=upstream_http，占比 %.1f%%）；网关自产 %d 条不计入本指标（见 gateway_5xx）。这是渠道健康趋势指标，不代表网关自身故障",
			upstream, share(upstream), gateway,
		)
	case OpsMetricUpstream5xxSpike:
		baseline := make([]int64, 0, len(buckets))
		for _, bucket := range buckets[:maxInt(0, len(buckets)-1)] {
			if bucket == nil {
				continue
			}
			baseline = append(baseline, bucket.Upstream5xx)
		}
		current := int64(0)
		if len(buckets) > 0 && buckets[len(buckets)-1] != nil {
			current = buckets[len(buckets)-1].Upstream5xx
		}
		median := medianInt64(baseline)
		smoothing := ""
		if median < 1 {
			smoothing = "（基线为 0，按 1 条平滑，避免基线稀疏时无法观察）"
		}
		return fmt.Sprintf(
			"【上游 503 透传突增】当前 5 分钟上游透传 5xx %d 条，前 %d 个 5 分钟桶中位数 %.1f 条%s；口径 error_source=upstream_http，用于渠道健康观察，不代表网关自身故障",
			current, len(baseline), median, smoothing,
		)
	default:
		return ""
	}
}

// computeUpstream5xxSpikeRatio 计算「当前值 / 基线中位数」的倍数。
//
// 中位数为 0（基线稀疏）时按 1 做平滑分母，这样「平时 0 条、故障时 100 条」
// 仍能被观察类规则看到，而不是因为除零永远返回不可用。
func computeUpstream5xxSpikeRatio(current int64, baseline []int64) (float64, bool) {
	if current < 0 || len(baseline) == 0 {
		return 0, false
	}
	denominator := medianInt64(baseline)
	if denominator < 1 {
		denominator = 1
	}
	return float64(current) / denominator, true
}

// medianInt64 返回整型样本的中位数（偶数个取中间两个均值）。
func medianInt64(values []int64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]int64, len(values))
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return float64(sorted[middle])
	}
	return float64(sorted[middle-1]+sorted[middle]) / 2
}
