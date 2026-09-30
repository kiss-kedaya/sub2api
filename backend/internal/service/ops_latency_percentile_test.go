//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type latencyStubOpsRepo struct {
	OpsRepository

	percentiles *OpsLatencyPercentiles
	err         error

	gotStart time.Time
	gotEnd   time.Time
	called   bool
}

func (s *latencyStubOpsRepo) GetLatencyPercentiles(
	_ context.Context, _ *OpsDashboardFilter, start, end time.Time,
) (*OpsLatencyPercentiles, error) {
	s.called = true
	s.gotStart = start
	s.gotEnd = end
	if s.err != nil {
		return nil, s.err
	}
	return s.percentiles, nil
}

// 这两条规则（`p99_latency_ms` / `p95_latency_ms`）此前在 computeRuleMetric 里
// 没有分支，会落到 default 返回 (0,false)，永远不会触发。本测试锁定修复后的行为。
func TestComputeLatencyPercentileMetric(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	ctx := context.Background()

	t.Run("p95 returns the measured percentile", func(t *testing.T) {
		t.Parallel()
		repo := &latencyStubOpsRepo{percentiles: &OpsLatencyPercentiles{P95Ms: 1840.5, P99Ms: 4200, SampleCount: 5000}}
		svc := &OpsAlertEvaluatorService{opsRepo: repo}

		val, ok := svc.computeRuleMetric(ctx, &OpsAlertRule{MetricType: OpsMetricP95LatencyMs},
			nil, now.Add(-5*time.Minute), now, "", nil)

		require.True(t, ok)
		require.InDelta(t, 1840.5, val, 0.001)
	})

	t.Run("p99 returns the measured percentile and can breach 3000ms", func(t *testing.T) {
		t.Parallel()
		repo := &latencyStubOpsRepo{percentiles: &OpsLatencyPercentiles{P95Ms: 1840.5, P99Ms: 4200, SampleCount: 5000}}
		svc := &OpsAlertEvaluatorService{opsRepo: repo}

		val, ok := svc.computeRuleMetric(ctx, &OpsAlertRule{MetricType: OpsMetricP99LatencyMs},
			nil, now.Add(-5*time.Minute), now, "", nil)

		require.True(t, ok)
		require.InDelta(t, 4200, val, 0.001)
		// 规则 3 的阈值是 3000ms：必须能真正判出越界。
		require.True(t, compareMetric(val, ">", 3000))
	})

	// 没有样本时不能报 0ms，否则「流量归零」看起来像「延迟变好了」。
	t.Run("no samples means unavailable, not zero", func(t *testing.T) {
		t.Parallel()
		repo := &latencyStubOpsRepo{percentiles: &OpsLatencyPercentiles{SampleCount: 0}}
		svc := &OpsAlertEvaluatorService{opsRepo: repo}

		_, ok := svc.computeRuleMetric(ctx, &OpsAlertRule{MetricType: OpsMetricP95LatencyMs},
			nil, now.Add(-5*time.Minute), now, "", nil)

		require.False(t, ok)
	})

	t.Run("query failure is unavailable", func(t *testing.T) {
		t.Parallel()
		repo := &latencyStubOpsRepo{err: errors.New("db down")}
		svc := &OpsAlertEvaluatorService{opsRepo: repo}

		_, ok := svc.computeRuleMetric(ctx, &OpsAlertRule{MetricType: OpsMetricP99LatencyMs},
			nil, now.Add(-5*time.Minute), now, "", nil)

		require.False(t, ok)
	})

	// 分位数要对窗口内的行排序，而 usage_logs 有 80+ GB，窗口必须封顶。
	t.Run("oversized window is clamped before it reaches the database", func(t *testing.T) {
		t.Parallel()
		repo := &latencyStubOpsRepo{percentiles: &OpsLatencyPercentiles{P95Ms: 100, SampleCount: 1}}
		svc := &OpsAlertEvaluatorService{opsRepo: repo}

		_, ok := svc.computeRuleMetric(ctx, &OpsAlertRule{MetricType: OpsMetricP95LatencyMs},
			nil, now.Add(-72*time.Hour), now, "", nil)

		require.True(t, ok)
		require.True(t, repo.called)
		require.InDelta(t, opsLatencyPercentileMaxWindow.Seconds(),
			repo.gotEnd.Sub(repo.gotStart).Seconds(), 1)
	})

	t.Run("non-latency metrics do not hit this repo path", func(t *testing.T) {
		t.Parallel()
		repo := &latencyStubOpsRepo{percentiles: &OpsLatencyPercentiles{}}
		svc := &OpsAlertEvaluatorService{opsRepo: repo, opsService: &OpsService{}}

		_, _ = svc.computeRuleMetric(ctx, &OpsAlertRule{MetricType: "memory_usage_percent"},
			nil, now.Add(-5*time.Minute), now, "", nil)

		require.False(t, repo.called)
	})
}

// 首字延迟（TTFB）才是用户感知的「网关快不快」：总时长主要取决于模型生成多长
// （生产实测 p50 就有 12.5 秒、p95 114 秒），拿它当延迟阈值没有意义。
func TestComputeLatencyPercentileMetric_FirstToken(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	ctx := context.Background()

	t.Run("p95 first token returns the ttfb percentile", func(t *testing.T) {
		t.Parallel()
		repo := &latencyStubOpsRepo{percentiles: &OpsLatencyPercentiles{
			P95Ms: 114000, P99Ms: 226000, SampleCount: 9000,
			FirstTokenP95Ms: 56900, FirstTokenP99Ms: 131000, FirstTokenSampleCount: 7430,
		}}
		svc := &OpsAlertEvaluatorService{opsRepo: repo}

		val, ok := svc.computeRuleMetric(ctx, &OpsAlertRule{MetricType: OpsMetricP95FirstTokenMs},
			nil, now.Add(-5*time.Minute), now, "", nil)

		require.True(t, ok)
		require.InDelta(t, 56900, val, 0.001, "must report TTFB, not the 114s total duration")
	})

	t.Run("p99 first token returns the ttfb percentile", func(t *testing.T) {
		t.Parallel()
		repo := &latencyStubOpsRepo{percentiles: &OpsLatencyPercentiles{
			SampleCount: 9000, FirstTokenP95Ms: 56900, FirstTokenP99Ms: 131000, FirstTokenSampleCount: 7430,
		}}
		svc := &OpsAlertEvaluatorService{opsRepo: repo}

		val, ok := svc.computeRuleMetric(ctx, &OpsAlertRule{MetricType: OpsMetricP99FirstTokenMs},
			nil, now.Add(-5*time.Minute), now, "", nil)

		require.True(t, ok)
		require.InDelta(t, 131000, val, 0.001)
	})

	// 关键边界：非流式请求没有首字延迟。整窗都是非流式时 TTFB 样本为 0，
	// 但 duration 样本不为 0 —— 不能用 SampleCount 放行，否则会把 0ms 当真实延迟。
	t.Run("no ttfb samples means unavailable even when duration samples exist", func(t *testing.T) {
		t.Parallel()
		repo := &latencyStubOpsRepo{percentiles: &OpsLatencyPercentiles{
			P95Ms: 99551, P99Ms: 200000, SampleCount: 1600,
			FirstTokenP95Ms: 0, FirstTokenP99Ms: 0, FirstTokenSampleCount: 0,
		}}
		svc := &OpsAlertEvaluatorService{opsRepo: repo}

		_, ok := svc.computeRuleMetric(ctx, &OpsAlertRule{MetricType: OpsMetricP95FirstTokenMs},
			nil, now.Add(-5*time.Minute), now, "", nil)

		require.False(t, ok)
	})
}
