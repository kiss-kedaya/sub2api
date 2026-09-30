//go:build unit

package service

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeDeployStatus(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

func deployStatusPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "deploy-check.status")
}

func TestReadDeployCheckStatus(t *testing.T) {
	t.Parallel()

	t.Run("parses a healthy status", func(t *testing.T) {
		t.Parallel()
		path := deployStatusPath(t)
		now := time.Now().UTC().Truncate(time.Second)
		writeDeployStatus(t, path, "version_mismatch=0\nchecked_at_unix="+
			strconv.FormatInt(now.Unix(), 10)+"\ndetail=pid 123 exe /opt/sub2api/sub2api-2.0.31\n")

		status, err := ReadDeployCheckStatus(path)
		require.NoError(t, err)
		require.False(t, status.VersionMismatch)
		require.True(t, status.CheckedAt.Equal(now))
		require.Contains(t, status.Detail, "sub2api-2.0.31")
		require.False(t, status.Stale(now))
	})

	t.Run("parses a mismatch and accepts boolean spellings", func(t *testing.T) {
		t.Parallel()
		path := deployStatusPath(t)
		now := time.Now().UTC()
		writeDeployStatus(t, path, "version_mismatch=true\nchecked_at_unix="+
			strconv.FormatInt(now.Unix(), 10)+"\n")

		status, err := ReadDeployCheckStatus(path)
		require.NoError(t, err)
		require.True(t, status.VersionMismatch)
	})

	t.Run("missing file is unavailable", func(t *testing.T) {
		t.Parallel()
		_, err := ReadDeployCheckStatus(deployStatusPath(t))
		require.ErrorIs(t, err, errDeployCheckUnavailable)
	})

	t.Run("incomplete file is unavailable", func(t *testing.T) {
		t.Parallel()
		path := deployStatusPath(t)
		writeDeployStatus(t, path, "version_mismatch=0\n") // 缺 checked_at_unix
		_, err := ReadDeployCheckStatus(path)
		require.ErrorIs(t, err, errDeployCheckUnavailable)
	})

	t.Run("garbage is unavailable, never a silent healthy", func(t *testing.T) {
		t.Parallel()
		path := deployStatusPath(t)
		writeDeployStatus(t, path, "version_mismatch=maybe\nchecked_at_unix=abc\n")
		_, err := ReadDeployCheckStatus(path)
		require.ErrorIs(t, err, errDeployCheckUnavailable)
	})
}

func TestDeployCheckStale(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	require.True(t, (&DeployCheckStatus{}).Stale(now), "a zero timestamp must count as stale")
	require.False(t, (&DeployCheckStatus{CheckedAt: now.Add(-time.Minute)}).Stale(now))
	require.True(t, (&DeployCheckStatus{CheckedAt: now.Add(-deployCheckStaleAfter - time.Minute)}).Stale(now))
}

func TestDeployCheckStatusPathDefault(t *testing.T) {
	// 不使用 t.Parallel()：本用例会改环境变量。
	t.Setenv(deployCheckStatusPathEnv, "")
	require.Equal(t, defaultDeployCheckStatusPath, deployCheckStatusPath())

	t.Setenv(deployCheckStatusPathEnv, "  /tmp/custom.status  ")
	require.Equal(t, "/tmp/custom.status", deployCheckStatusPath())
}

func TestComputeDeployCheckMetric(t *testing.T) {
	t.Parallel()

	t.Run("mismatch reports 1", func(t *testing.T) {
		t.Parallel()
		path := deployStatusPath(t)
		writeDeployStatus(t, path, "version_mismatch=1\nchecked_at_unix="+
			strconv.FormatInt(time.Now().Unix(), 10)+"\n")
		svc := &OpsAlertEvaluatorService{deployStatusPathOverride: path}

		val, ok := svc.computeRuleMetric(nil, &OpsAlertRule{MetricType: OpsMetricDeployVersionMismatch},
			nil, time.Time{}, time.Time{}, "", nil)
		require.True(t, ok)
		require.InDelta(t, 1, val, 0.0001)
	})

	t.Run("healthy reports 0", func(t *testing.T) {
		t.Parallel()
		path := deployStatusPath(t)
		writeDeployStatus(t, path, "version_mismatch=0\nchecked_at_unix="+
			strconv.FormatInt(time.Now().Unix(), 10)+"\n")
		svc := &OpsAlertEvaluatorService{deployStatusPathOverride: path}

		val, ok := svc.computeRuleMetric(nil, &OpsAlertRule{MetricType: OpsMetricDeployVersionMismatch},
			nil, time.Time{}, time.Time{}, "", nil)
		require.True(t, ok)
		require.InDelta(t, 0, val, 0.0001)
	})

	// 关键安全属性：看门狗自己死掉时，「版本不一致」绝不能报 0（那等于显示健康）。
	t.Run("a dead watchdog must not look healthy", func(t *testing.T) {
		t.Parallel()
		svc := &OpsAlertEvaluatorService{deployStatusPathOverride: deployStatusPath(t)}

		_, ok := svc.computeRuleMetric(nil, &OpsAlertRule{MetricType: OpsMetricDeployVersionMismatch},
			nil, time.Time{}, time.Time{}, "", nil)
		require.False(t, ok, "no status file means the metric is unavailable, not zero")
	})

	t.Run("a stale status is not trusted for mismatch", func(t *testing.T) {
		t.Parallel()
		path := deployStatusPath(t)
		stale := time.Now().Add(-2 * deployCheckStaleAfter).Unix()
		writeDeployStatus(t, path, "version_mismatch=0\nchecked_at_unix="+
			strconv.FormatInt(stale, 10)+"\n")
		svc := &OpsAlertEvaluatorService{deployStatusPathOverride: path}

		_, ok := svc.computeRuleMetric(nil, &OpsAlertRule{MetricType: OpsMetricDeployVersionMismatch},
			nil, time.Time{}, time.Time{}, "", nil)
		require.False(t, ok)
	})

	// age 指标即使状态过旧也必须可用——它正是用来发现「看门狗不再跑了」的。
	t.Run("age keeps working when the status goes stale", func(t *testing.T) {
		t.Parallel()
		path := deployStatusPath(t)
		old := time.Now().Add(-time.Hour).Unix()
		writeDeployStatus(t, path, "version_mismatch=0\nchecked_at_unix="+
			strconv.FormatInt(old, 10)+"\n")
		svc := &OpsAlertEvaluatorService{deployStatusPathOverride: path}

		val, ok := svc.computeRuleMetric(nil, &OpsAlertRule{MetricType: OpsMetricDeployCheckAgeSeconds},
			nil, time.Time{}, time.Time{}, "", nil)
		require.True(t, ok)
		require.Greater(t, val, 3000.0)
	})
}
