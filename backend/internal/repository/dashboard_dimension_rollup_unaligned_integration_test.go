//go:build integration

package repository

import (
	"testing"
)

// The differential test of dashboard_dimension_rollup_unaligned_pg_test.go on the harness database (migrated
// PostgreSQL in Docker). Smaller than a local run so the job stays short; the zones cover a positive, a zero and a
// negative offset.
func TestRollupUnalignedDifferentialIntegration(t *testing.T) {
	for i, zone := range rollupDiffZones {
		t.Run(zone, func(t *testing.T) {
			runRollupDifferential(t, integrationDB, rollupDiffConfig{zone: zone, windows: 60, rows: 20000, seed: 20261125 + int64(i)})
		})
	}
}
