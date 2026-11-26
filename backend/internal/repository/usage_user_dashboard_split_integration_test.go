//go:build integration

package repository

import (
	"testing"
)

// The differential test of usage_user_dashboard_split_pg_test.go on the harness database (migrated PostgreSQL in
// Docker). Smaller than a local run so the job stays short; the zones are the same ones, DST days and half hour
// zones included.
func TestUserDashboardSplitDifferentialIntegration(t *testing.T) {
	runUserDashboardDifferential(t, integrationDB, userDashboardDiffConfig{cases: 6, rows: 20000, seed: 20261126})
}
