//go:build integration

package repository

import (
	"testing"
)

// The differential test of usage_key_daily_split_pg_test.go on the harness database (migrated PostgreSQL in
// Docker). Smaller than a local run so the job stays short; the zones are the same ones, DST days and half hour
// zones included.
func TestKeyDailyTrendSplitDifferentialIntegration(t *testing.T) {
	runKeyDailyDifferential(t, integrationDB, keyDailyDiffConfig{windows: 15, rows: 20000, seed: 20261126})
}
