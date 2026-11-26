//go:build integration

package repository

import (
	"testing"
)

// The differential test of usage_key_window_split_pg_test.go on the harness database (migrated PostgreSQL in
// Docker). Smaller than a local run so the job stays short (about a minute); the zones and the hand-picked windows are
// the same ones, DST days and half hour zones included.
func TestKeyWindowSplitDifferentialIntegration(t *testing.T) {
	runKeyWindowDifferential(t, integrationDB, keyWindowDiffConfig{windows: 4, rows: 10000, seed: 20261126})
}
