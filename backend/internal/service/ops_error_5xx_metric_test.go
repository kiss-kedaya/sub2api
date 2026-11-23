//go:build unit

package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newFiveXXBuckets(upstream, gateway int64, count int) []*OpsError5xxBucket {
	buckets := make([]*OpsError5xxBucket, count)
	for i := range buckets {
		buckets[i] = &OpsError5xxBucket{Upstream5xx: upstream, Gateway5xx: gateway}
	}
	return buckets
}

// 拆分核心断言：上游透传的 5xx 绝不能计入 gateway_5xx。
func TestOpsError5xxMetricValue_GatewayExcludesUpstream(t *testing.T) {
	t.Parallel()

	// 线上场景：5 分钟 9506 条 5xx，其中 9480 条是上游透传。
	buckets := newFiveXXBuckets(9480, 26, 1)

	gatewayValue, ok := opsError5xxMetricValue(OpsMetricGateway5xx, buckets)
	require.True(t, ok)
	require.Equal(t, 26.0, gatewayValue, "gateway_5xx 只统计网关自产，不能被上游透传 9480 条带偏")

	upstreamValue, ok := opsError5xxMetricValue(OpsMetricUpstream5xx, buckets)
	require.True(t, ok)
	require.Equal(t, 9480.0, upstreamValue)

	// 极端：全部是上游透传时，网关指标必须是 0。
	allUpstream := newFiveXXBuckets(9506, 0, 1)
	gatewayValue, ok = opsError5xxMetricValue(OpsMetricGateway5xx, allUpstream)
	require.True(t, ok)
	require.Equal(t, 0.0, gatewayValue)
}

func TestOpsError5xxMetricValue_CountsSumWindowBuckets(t *testing.T) {
	t.Parallel()

	buckets := []*OpsError5xxBucket{
		{Upstream5xx: 10, Gateway5xx: 1},
		nil,
		{Upstream5xx: 5, Gateway5xx: 2},
	}

	gatewayValue, ok := opsError5xxMetricValue(OpsMetricGateway5xx, buckets)
	require.True(t, ok)
	require.Equal(t, 3.0, gatewayValue)

	upstreamValue, ok := opsError5xxMetricValue(OpsMetricUpstream5xx, buckets)
	require.True(t, ok)
	require.Equal(t, 15.0, upstreamValue)
}

func TestOpsError5xxMetricValue_SpikeRatio(t *testing.T) {
	t.Parallel()

	makeSeries := func(current int64, baseline ...int64) []*OpsError5xxBucket {
		buckets := make([]*OpsError5xxBucket, 0, len(baseline)+1)
		for _, value := range baseline {
			buckets = append(buckets, &OpsError5xxBucket{Upstream5xx: value})
		}
		buckets = append(buckets, &OpsError5xxBucket{Upstream5xx: current})
		return buckets
	}

	t.Run("明显突增", func(t *testing.T) {
		t.Parallel()
		value, ok := opsError5xxMetricValue(OpsMetricUpstream5xxSpike,
			makeSeries(40, 10, 10, 10, 10, 10, 10))
		require.True(t, ok)
		require.InDelta(t, 4.0, value, 0.0001)
	})

	t.Run("基线为 0 时按 1 条平滑，故障仍可观察", func(t *testing.T) {
		t.Parallel()
		value, ok := opsError5xxMetricValue(OpsMetricUpstream5xxSpike,
			makeSeries(100, 0, 0, 0, 0, 0, 0))
		require.True(t, ok)
		require.InDelta(t, 100.0, value, 0.0001)
	})

	t.Run("平稳不突增", func(t *testing.T) {
		t.Parallel()
		value, ok := opsError5xxMetricValue(OpsMetricUpstream5xxSpike,
			makeSeries(12, 10, 12, 9, 11, 10, 10))
		require.True(t, ok)
		require.InDelta(t, 1.2, value, 0.0001)
	})

	t.Run("只有当前桶时不可用", func(t *testing.T) {
		t.Parallel()
		_, ok := opsError5xxMetricValue(OpsMetricUpstream5xxSpike,
			makeSeries(40))
		require.False(t, ok)
	})
}

func TestMedianInt64(t *testing.T) {
	t.Parallel()

	require.Equal(t, 0.0, medianInt64(nil))
	require.Equal(t, 7.0, medianInt64([]int64{9, 7, 1}))
	require.Equal(t, 5.0, medianInt64([]int64{1, 9, 5, 5}))
	require.Equal(t, 2.5, medianInt64([]int64{1, 2, 3, 4}))
}

func TestComputeUpstream5xxSpikeRatio(t *testing.T) {
	t.Parallel()

	value, ok := computeUpstream5xxSpikeRatio(30, []int64{10, 10, 10, 10, 10, 10})
	require.True(t, ok)
	require.InDelta(t, 3.0, value, 0.0001)

	_, ok = computeUpstream5xxSpikeRatio(30, nil)
	require.False(t, ok)
}

// 评估器接入：metric_type 正确转发到分源聚合，且口径提示落进详情缓存。
func TestComputeRuleMetric_Error5xxSourceSplit(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.November, 23, 10, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)

	var gotStart, gotEnd time.Time
	var gotBucketSeconds int
	repo := &opsRepoMock{
		CountErrorLogs5xxBySourceFn: func(_ context.Context, s, e time.Time, bucketSeconds int, platform string, groupID *int64) ([]*OpsError5xxBucket, error) {
			gotStart, gotEnd, gotBucketSeconds = s, e, bucketSeconds
			require.Equal(t, "openai", platform)
			require.Nil(t, groupID)
			return newFiveXXBuckets(9480, 26, 1), nil
		},
	}
	svc := &OpsAlertEvaluatorService{opsRepo: repo}

	gatewayValue, ok := svc.computeRuleMetric(context.Background(),
		&OpsAlertRule{ID: 7, MetricType: OpsMetricGateway5xx}, nil, start, end, "openai", nil)
	require.True(t, ok)
	require.Equal(t, 26.0, gatewayValue)
	require.Equal(t, start, gotStart, "计数指标整窗作为一个桶，起点就是规则窗口起点")
	require.Equal(t, end, gotEnd)
	require.Equal(t, 300, gotBucketSeconds)

	detail := svc.takeMetricDetail(7)
	require.Contains(t, detail, "【网关自身 5xx】")
	require.Contains(t, detail, "上游透传 9480 条")
	require.Contains(t, detail, "error_source=upstream_http")

	upstreamValue, ok := svc.computeRuleMetric(context.Background(),
		&OpsAlertRule{ID: 8, MetricType: OpsMetricUpstream5xx}, nil, start, end, "openai", nil)
	require.True(t, ok)
	require.Equal(t, 9480.0, upstreamValue)
	upstreamDetail := svc.takeMetricDetail(8)
	require.Contains(t, upstreamDetail, "【上游 503 透传】")
	require.Contains(t, upstreamDetail, "网关自产 26 条")
}

func TestComputeRuleMetric_Error5xxSpikeUsesSevenBuckets(t *testing.T) {
	t.Parallel()

	end := time.Date(2026, time.November, 23, 10, 0, 0, 0, time.UTC)
	start := end.Add(-5 * time.Minute)

	var gotStart, gotEnd time.Time
	var gotBucketSeconds int
	repo := &opsRepoMock{
		CountErrorLogs5xxBySourceFn: func(_ context.Context, s, e time.Time, bucketSeconds int, platform string, groupID *int64) ([]*OpsError5xxBucket, error) {
			gotStart, gotEnd, gotBucketSeconds = s, e, bucketSeconds
			return newFiveXXBuckets(10, 0, 7), nil
		},
	}
	svc := &OpsAlertEvaluatorService{opsRepo: repo}

	value, ok := svc.computeRuleMetric(context.Background(),
		&OpsAlertRule{ID: 9, MetricType: OpsMetricUpstream5xxSpike}, nil, start, end, "", nil)
	require.True(t, ok)
	require.Equal(t, 1.0, value, "平稳基线时突增倍数为 1")
	require.Equal(t, opsError5xxSpikeBucketSeconds, gotBucketSeconds)
	require.Equal(t, end.Add(-7*5*time.Minute), gotStart, "1 个当前桶 + 6 个基线桶")
	require.Equal(t, end, gotEnd)

	detail := svc.takeMetricDetail(9)
	require.Contains(t, detail, "【上游 503 透传突增】")
}

func TestComputeRuleMetric_Error5xxClampsLargeWindow(t *testing.T) {
	t.Parallel()

	end := time.Date(2026, time.November, 23, 10, 0, 0, 0, time.UTC)
	start := end.Add(-12 * time.Hour)

	var gotWindow time.Duration
	repo := &opsRepoMock{
		CountErrorLogs5xxBySourceFn: func(_ context.Context, s, e time.Time, bucketSeconds int, platform string, groupID *int64) ([]*OpsError5xxBucket, error) {
			gotWindow = e.Sub(s)
			return newFiveXXBuckets(1, 0, 1), nil
		},
	}
	svc := &OpsAlertEvaluatorService{opsRepo: repo}

	_, ok := svc.computeRuleMetric(context.Background(),
		&OpsAlertRule{ID: 10, MetricType: OpsMetricGateway5xx}, nil, start, end, "", nil)
	require.True(t, ok)
	require.Equal(t, opsError5xxMaxWindow, gotWindow)
}

func TestComputeRuleMetric_Error5xxRepoErrorIsUnavailable(t *testing.T) {
	t.Parallel()

	repo := &opsRepoMock{
		CountErrorLogs5xxBySourceFn: func(context.Context, time.Time, time.Time, int, string, *int64) ([]*OpsError5xxBucket, error) {
			return nil, errors.New("db down")
		},
	}
	svc := &OpsAlertEvaluatorService{opsRepo: repo}

	_, ok := svc.computeRuleMetric(context.Background(),
		&OpsAlertRule{ID: 11, MetricType: OpsMetricGateway5xx},
		nil, time.Now().Add(-5*time.Minute), time.Now(), "", nil)
	require.False(t, ok)
	require.Empty(t, svc.takeMetricDetail(11))
}

// 触发矩阵：上游透传大流量不能触发网关告警；网关自产超阈值才触发。
func TestOps5xxAlertTriggerMatrix(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		metricType string
		operator   string
		threshold  float64
		upstream   int64
		gateway    int64
		baseline   int64
		wantValue  float64
		wantFire   bool
	}

	cases := []testCase{
		{
			name:       "线上误报场景：9506 条 5xx 里 9480 条上游透传，网关规则不该触发",
			metricType: OpsMetricGateway5xx,
			operator:   ">",
			threshold:  500,
			upstream:   9480,
			gateway:    26,
			wantValue:  26,
			wantFire:   false,
		},
		{
			name:       "网关自产 500 条整不触发（严格大于）",
			metricType: OpsMetricGateway5xx,
			operator:   ">",
			threshold:  500,
			upstream:   0,
			gateway:    500,
			wantValue:  500,
			wantFire:   false,
		},
		{
			name:       "网关自产 501 条触发",
			metricType: OpsMetricGateway5xx,
			operator:   ">",
			threshold:  500,
			upstream:   0,
			gateway:    501,
			wantValue:  501,
			wantFire:   true,
		},
		{
			name:       "上游透传 30000 条整不触发硬顶",
			metricType: OpsMetricUpstream5xx,
			operator:   ">",
			threshold:  30000,
			upstream:   30000,
			wantValue:  30000,
			wantFire:   false,
		},
		{
			name:       "上游透传 30001 条触发硬顶观察",
			metricType: OpsMetricUpstream5xx,
			operator:   ">",
			threshold:  30000,
			upstream:   30001,
			wantValue:  30001,
			wantFire:   true,
		},
		{
			name:       "上游透传 3 倍整不触发突增观察（严格大于）",
			metricType: OpsMetricUpstream5xxSpike,
			operator:   ">",
			threshold:  3,
			upstream:   30,
			baseline:   10,
			wantValue:  3,
			wantFire:   false,
		},
		{
			name:       "上游透传超过 3 倍触发突增观察",
			metricType: OpsMetricUpstream5xxSpike,
			operator:   ">",
			threshold:  3,
			upstream:   31,
			baseline:   10,
			wantValue:  3.1,
			wantFire:   true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buckets []*OpsError5xxBucket
			switch tc.metricType {
			case OpsMetricUpstream5xxSpike:
				baseline := make([]int64, opsError5xxSpikeBaselineBucket)
				for i := range baseline {
					baseline[i] = tc.baseline
				}
				buckets = make([]*OpsError5xxBucket, 0, len(baseline)+1)
				for _, value := range baseline {
					buckets = append(buckets, &OpsError5xxBucket{Upstream5xx: value})
				}
				buckets = append(buckets, &OpsError5xxBucket{Upstream5xx: tc.upstream})
			default:
				buckets = []*OpsError5xxBucket{{Upstream5xx: tc.upstream, Gateway5xx: tc.gateway}}
			}

			value, ok := opsError5xxMetricValue(tc.metricType, buckets)
			require.True(t, ok)
			require.InDelta(t, tc.wantValue, value, 0.0001)

			rule := &OpsAlertRule{MetricType: tc.metricType, Operator: tc.operator, Threshold: tc.threshold}
			require.Equal(t, tc.wantFire, compareMetric(value, rule.Operator, rule.Threshold))
		})
	}
}

func TestBuildOpsAlertDescriptionWithDetail(t *testing.T) {
	t.Parallel()

	rule := &OpsAlertRule{
		Name:       "网关自身 5xx 激增",
		MetricType: OpsMetricGateway5xx,
		Operator:   ">",
		Threshold:  500,
	}

	description := buildOpsAlertDescriptionWithDetail(rule, 501, 5, "", nil,
		"【网关自身 5xx】窗口内 5xx 共 9506 条，上游透传 9480 条不计入。")
	require.True(t, strings.HasPrefix(description, "【网关自身 5xx】"),
		"口径归属必须排在数值前面，值班先看到是网关故障还是上游透传")
	require.Contains(t, description, "gateway_5xx > 500.00")
	require.Contains(t, description, "current 501.00")

	// 没有详情时保持原描述格式不变。
	plain := buildOpsAlertDescriptionWithDetail(rule, 501, 5, "openai", nil, "")
	require.NotContains(t, plain, "【")
	require.Contains(t, plain, "platform=openai")
}

func TestOpsError5xxMetricDetail_SourceDistribution(t *testing.T) {
	t.Parallel()

	buckets := []*OpsError5xxBucket{{Upstream5xx: 9, Gateway5xx: 1}}

	gatewayDetail := opsError5xxMetricDetail(OpsMetricGateway5xx, buckets)
	require.Contains(t, gatewayDetail, "5xx 共 10 条")
	require.Contains(t, gatewayDetail, "上游透传 9 条")
	require.Contains(t, gatewayDetail, "网关自产 1 条")
	require.Contains(t, gatewayDetail, "占比 10.0%")

	upstreamDetail := opsError5xxMetricDetail(OpsMetricUpstream5xx, buckets)
	require.Contains(t, upstreamDetail, "【上游 503 透传】")
	require.Contains(t, upstreamDetail, "占比 90.0%")
	require.Contains(t, upstreamDetail, "网关自产 1 条不计入本指标")
}
