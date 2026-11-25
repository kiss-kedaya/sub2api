//go:build integration

package repository

import "testing"

// Same checks as TestConnectionTracePostgresBatchInsert, against the migrated
// schema of the integration database.
func TestConnectionTraceIntegration_BatchInsert(t *testing.T) {
	runConnectionTracePostgresChecks(t, integrationDB)
}
