package repository

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 稳态保留期清理（PruneRetention）的上限和语句形状。语句的实际效果在
// channel_monitor_v2_prune_integration_test.go 里用真实 PostgreSQL 验证。

func TestChannelMonitorV2PruneDefaultLimitsAreBounded(t *testing.T) {
	lim := channelMonitorV2DefaultPruneLimits
	require.Equal(t, 20000, lim.batchSize, "每批最多 2 万行")
	require.LessOrEqual(t, lim.maxDuration, 60*time.Second, "一次运行总时长上限")
	require.LessOrEqual(t, lim.statementTimeout, 20*time.Second, "每条语句的超时要小于集群的 30 秒 statement_timeout")
	require.GreaterOrEqual(t, lim.pause, 100*time.Millisecond)
	require.LessOrEqual(t, lim.pause, 200*time.Millisecond)
	require.Positive(t, lim.maxBatches)
	require.Greater(t, lim.maxBatches, len(channelMonitorV2RetentionRules), "每次运行至少让每条规则各删一批")
	require.Less(t, lim.statementTimeout, lim.maxDuration)
}

func TestChannelMonitorV2PruneBatchSQLShape(t *testing.T) {
	cutoff := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	for _, rule := range channelMonitorV2RetentionRules {
		query := channelMonitorV2PruneBatchSQL(rule)
		args := channelMonitorV2PruneBatchArgs(rule, cutoff, 20000)
		require.True(t, strings.HasPrefix(query, "DELETE FROM "+rule.table+" "), query)
		require.Equal(t, 2, strings.Count(query, rule.table), "子查询也只读这张表")
		require.Contains(t, query, "bucket_start < $", "只删过期的行")
		require.NotContains(t, query, "bucket_start >")
		require.Contains(t, query, "ORDER BY bucket_start LIMIT $", "按时间从最老的读起，每批有上限")
		gotCutoff, ok := args[len(args)-2].(time.Time)
		require.True(t, ok)
		require.True(t, gotCutoff.Equal(cutoff.Add(-rule.retention)), "截止时间 = now - 这张表自己的保留期: %s", rule.table)
		require.Equal(t, 20000, args[len(args)-1])
		if rule.bucketSeconds == 0 {
			require.NotContains(t, query, "bucket_seconds")
			require.Len(t, args, 2)
		} else {
			require.Contains(t, query, "bucket_seconds = $1 AND bucket_start < $2")
			require.Equal(t, rule.bucketSeconds, args[0])
			require.Len(t, args, 3)
		}
	}
}

// 每个 1m 事实表和每个汇总表的每个 bucket_seconds 都要有一条清理规则，保留期与设计一致。
func TestChannelMonitorV2PruneCoversEveryTierWithItsOwnRetention(t *testing.T) {
	type key struct {
		table   string
		seconds int
	}
	want := map[key]time.Duration{
		{"channel_monitor_v2_user_metrics_1m", 0}:       3 * 24 * time.Hour,
		{"channel_monitor_v2_metrics_1m", 0}:            7 * 24 * time.Hour,
		{"channel_monitor_v2_error_metrics_1m", 0}:      7 * 24 * time.Hour,
		{"channel_monitor_v2_latency_histograms_1m", 0}: 7 * 24 * time.Hour,
	}
	for _, table := range []string{
		"channel_monitor_v2_metrics_rollup", "channel_monitor_v2_user_metrics_rollup",
		"channel_monitor_v2_error_metrics_rollup", "channel_monitor_v2_latency_histograms_rollup",
	} {
		want[key{table, 300}] = 7 * 24 * time.Hour
		want[key{table, 3600}] = 30 * 24 * time.Hour
		want[key{table, 43200}] = 45 * 24 * time.Hour
		want[key{table, 86400}] = 90 * 24 * time.Hour
	}
	got := map[key]time.Duration{}
	for _, rule := range channelMonitorV2RetentionRules {
		got[key{rule.table, rule.bucketSeconds}] = rule.retention
	}
	require.Equal(t, want, got)
}
