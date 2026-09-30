//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// billingStubOpsRepo 只实现计费快照，其余方法由嵌入的接口提供（不会被调用）。
type billingStubOpsRepo struct {
	OpsRepository

	snapshot *BillingAnomalySnapshot
	err      error

	called   bool
	gotStart time.Time
	gotEnd   time.Time
}

func (s *billingStubOpsRepo) GetBillingAnomalySnapshot(
	_ context.Context, start, end time.Time,
) (*BillingAnomalySnapshot, error) {
	s.called = true
	s.gotStart = start
	s.gotEnd = end
	if s.err != nil {
		return nil, s.err
	}
	return s.snapshot, nil
}

// GetDashboardOverview 让非计费指标能走完自己的分支（那些分支会调用它）。
func (s *billingStubOpsRepo) GetDashboardOverview(
	context.Context, *OpsDashboardFilter,
) (*OpsDashboardOverview, error) {
	return &OpsDashboardOverview{}, nil
}

// GetLatencyPercentiles 让延迟分位数分支能跑（本文件的用例不会走到它）。
func (s *billingStubOpsRepo) GetLatencyPercentiles(
	context.Context, *OpsDashboardFilter, time.Time, time.Time,
) (*OpsLatencyPercentiles, error) {
	return &OpsLatencyPercentiles{}, nil
}

func TestComputeBillingAnomalyMetric_ZeroCostRequests(t *testing.T) {
	t.Parallel()

	repo := &billingStubOpsRepo{snapshot: &BillingAnomalySnapshot{
		MeteredRequests:  1000,
		ZeroCostRequests: 7,
	}}
	svc := &OpsAlertEvaluatorService{opsRepo: repo}

	now := time.Now().UTC()
	val, ok := svc.computeRuleMetric(context.Background(),
		&OpsAlertRule{MetricType: OpsMetricBillingZeroCostRequests},
		nil, now.Add(-5*time.Minute), now, "", nil)

	require.True(t, ok)
	require.InDelta(t, 7, val, 0.0001)
	require.True(t, repo.called)
}

func TestComputeBillingAnomalyMetric_ZeroCostRatio(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	ctx := context.Background()

	t.Run("25 of 1000 metered requests is 2.5%", func(t *testing.T) {
		t.Parallel()

		repo := &billingStubOpsRepo{snapshot: &BillingAnomalySnapshot{
			MeteredRequests:  1000,
			ZeroCostRequests: 25,
		}}
		svc := &OpsAlertEvaluatorService{opsRepo: repo}

		val, ok := svc.computeRuleMetric(ctx,
			&OpsAlertRule{MetricType: OpsMetricBillingZeroCostRatio},
			nil, now.Add(-5*time.Minute), now, "", nil)

		require.True(t, ok)
		require.InDelta(t, 2.5, val, 0.0001)
	})

	t.Run("no metered traffic leaves the ratio unavailable", func(t *testing.T) {
		t.Parallel()

		repo := &billingStubOpsRepo{snapshot: &BillingAnomalySnapshot{
			MeteredRequests:  0,
			ZeroCostRequests: 0,
		}}
		svc := &OpsAlertEvaluatorService{opsRepo: repo}

		_, ok := svc.computeRuleMetric(ctx,
			&OpsAlertRule{MetricType: OpsMetricBillingZeroCostRatio},
			nil, now.Add(-5*time.Minute), now, "", nil)

		require.False(t, ok, "a ratio without a denominator must not fire an alert")
	})
}

func TestComputeBillingAnomalyMetric_CostSpikeRatio(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	ctx := context.Background()

	t.Run("doubling the spend reports 200", func(t *testing.T) {
		t.Parallel()

		repo := &billingStubOpsRepo{snapshot: &BillingAnomalySnapshot{
			WindowCostUSD:         40,
			PreviousWindowCostUSD: 20,
		}}
		svc := &OpsAlertEvaluatorService{opsRepo: repo}

		val, ok := svc.computeRuleMetric(ctx,
			&OpsAlertRule{MetricType: OpsMetricBillingCostSpikeRatio},
			nil, now.Add(-5*time.Minute), now, "", nil)

		require.True(t, ok)
		require.InDelta(t, 200, val, 0.0001)
	})

	t.Run("no previous baseline must not fire", func(t *testing.T) {
		t.Parallel()

		repo := &billingStubOpsRepo{snapshot: &BillingAnomalySnapshot{
			WindowCostUSD:         40,
			PreviousWindowCostUSD: 0,
		}}
		svc := &OpsAlertEvaluatorService{opsRepo: repo}

		_, ok := svc.computeRuleMetric(ctx,
			&OpsAlertRule{MetricType: OpsMetricBillingCostSpikeRatio},
			nil, now.Add(-5*time.Minute), now, "", nil)

		require.False(t, ok, "an empty previous window must not look like a cost spike")
	})
}

func TestComputeBillingAnomalyMetric_NegativeBalanceAndStuckHolds(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	ctx := context.Background()

	repo := &billingStubOpsRepo{snapshot: &BillingAnomalySnapshot{
		NegativeBalanceUsers: 3,
		StuckHolds:           11,
	}}
	svc := &OpsAlertEvaluatorService{opsRepo: repo}

	neg, ok := svc.computeRuleMetric(ctx,
		&OpsAlertRule{MetricType: OpsMetricBillingNegativeBalanceUsers},
		nil, now.Add(-5*time.Minute), now, "", nil)
	require.True(t, ok)
	require.InDelta(t, 3, neg, 0.0001)

	stuck, ok := svc.computeRuleMetric(ctx,
		&OpsAlertRule{MetricType: OpsMetricBillingStuckHolds},
		nil, now.Add(-5*time.Minute), now, "", nil)
	require.True(t, ok)
	require.InDelta(t, 11, stuck, 0.0001)
}

// 评估器整体只有 45s 预算且按规则串行执行，线上 usage_logs 有 80+ GB，
// 所以计费指标必须把窗口截断到 opsBillingMetricMaxWindow。
func TestComputeBillingAnomalyMetric_ClampsOversizedWindow(t *testing.T) {
	t.Parallel()

	repo := &billingStubOpsRepo{snapshot: &BillingAnomalySnapshot{MeteredRequests: 1}}
	svc := &OpsAlertEvaluatorService{opsRepo: repo}

	now := time.Now().UTC()
	_, ok := svc.computeRuleMetric(context.Background(),
		&OpsAlertRule{MetricType: OpsMetricBillingZeroCostRequests},
		nil, now.Add(-48*time.Hour), now, "", nil)

	require.True(t, ok)
	require.True(t, repo.called)
	require.InDelta(t, opsBillingMetricMaxWindow.Seconds(),
		repo.gotEnd.Sub(repo.gotStart).Seconds(), 1,
		"an oversized window must be clamped before it reaches the database")
}

func TestComputeBillingAnomalyMetric_RepoErrorIsNotAnAlert(t *testing.T) {
	t.Parallel()

	repo := &billingStubOpsRepo{err: errors.New("db down")}
	svc := &OpsAlertEvaluatorService{opsRepo: repo}

	now := time.Now().UTC()
	_, ok := svc.computeRuleMetric(context.Background(),
		&OpsAlertRule{MetricType: OpsMetricBillingZeroCostRequests},
		nil, now.Add(-5*time.Minute), now, "", nil)

	require.False(t, ok, "a query failure must not be reported as a billing anomaly")
}

// 绝对值指标在生产上长期非零（未定价模型持续产生零扣费请求），所以告警改用增量。
func TestComputeBillingAnomalyMetric_ZeroCostDelta(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	ctx := context.Background()

	t.Run("steady state reports about zero", func(t *testing.T) {
		t.Parallel()
		// 本窗 52 笔、上窗 50 笔 —— 稳态，不该告警。
		repo := &billingStubOpsRepo{snapshot: &BillingAnomalySnapshot{
			MeteredRequests: 9000, ZeroCostRequests: 52, PreviousWindowZeroCostRequests: 50,
		}}
		svc := &OpsAlertEvaluatorService{opsRepo: repo}

		val, ok := svc.computeRuleMetric(ctx,
			&OpsAlertRule{MetricType: OpsMetricBillingZeroCostRequestsDelta},
			nil, now.Add(-5*time.Minute), now, "", nil)

		require.True(t, ok)
		require.InDelta(t, 2, val, 0.0001)
		require.False(t, compareMetric(val, ">", 300), "steady state must not breach")
	})

	t.Run("a real surge is reported", func(t *testing.T) {
		t.Parallel()
		repo := &billingStubOpsRepo{snapshot: &BillingAnomalySnapshot{
			MeteredRequests: 9000, ZeroCostRequests: 5000, PreviousWindowZeroCostRequests: 50,
		}}
		svc := &OpsAlertEvaluatorService{opsRepo: repo}

		val, ok := svc.computeRuleMetric(ctx,
			&OpsAlertRule{MetricType: OpsMetricBillingZeroCostRequestsDelta},
			nil, now.Add(-5*time.Minute), now, "", nil)

		require.True(t, ok)
		require.InDelta(t, 4950, val, 0.0001)
		require.True(t, compareMetric(val, ">", 300))
	})
}

// 负余额是存量指标，基线在 Redis 上。Redis 不可用时必须报「不可用」而不是
// 拿 0 当增量——否则基数 1000+ 会每次 Redis 抖动都误报一次。
func TestComputeBillingAnomalyMetric_NegativeBalanceDeltaFailsSafe(t *testing.T) {
	t.Parallel()

	repo := &billingStubOpsRepo{snapshot: &BillingAnomalySnapshot{NegativeBalanceUsers: 1101}}
	svc := &OpsAlertEvaluatorService{opsRepo: repo} // 没有 redisClient

	now := time.Now().UTC()
	_, ok := svc.computeRuleMetric(context.Background(),
		&OpsAlertRule{MetricType: OpsMetricBillingNegativeBalanceUsersDelta},
		nil, now.Add(-5*time.Minute), now, "", nil)

	require.False(t, ok, "without a baseline the delta is unavailable, never zero")
}

func TestComputeBillingAnomalyMetric_NonBillingMetricNotIntercepted(t *testing.T) {
	t.Parallel()

	repo := &billingStubOpsRepo{snapshot: &BillingAnomalySnapshot{}}
	svc := &OpsAlertEvaluatorService{
		opsRepo:    repo,
		opsService: &OpsService{},
	}

	now := time.Now().UTC()
	// success_rate 走 dashboard overview 分支；仓储不应被调用。
	_, _ = svc.computeRuleMetric(context.Background(),
		&OpsAlertRule{MetricType: "success_rate"},
		nil, now.Add(-5*time.Minute), now, "", nil)

	require.False(t, repo.called, "non-billing metrics must not hit the billing repo")
}
