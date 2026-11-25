//go:build integration

package repository

import "testing"

// 集成测试任务在它自己的 PostgreSQL 上跑 dashboard_aggregation_selfheal_pg_test.go 里同一份测试。

func TestDashboardSelfHealLateRowsIntegration(t *testing.T) {
	runDashboardSelfHealLateRows(t, integrationDB)
}

func TestDashboardSelfHealKeepsDailyActiveUsersIntegration(t *testing.T) {
	runDashboardSelfHealKeepsDailyActiveUsers(t, integrationDB)
}
