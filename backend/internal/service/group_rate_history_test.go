package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func auditRow(at time.Time, body string) GroupRateAuditRow {
	return GroupRateAuditRow{CreatedAt: at, ActorEmail: "admin@example.com", RequestBody: body}
}

func TestBuildGroupRateConfigChangesUsesAuditBodies(t *testing.T) {
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	rows := []GroupRateAuditRow{
		auditRow(base, `{"name":"g","rate_multiplier":0.12}`),                  // baseline
		auditRow(base.Add(time.Hour), `{"name":"g","rate_multiplier":0.12}`),   // no change
		auditRow(base.Add(2*time.Hour), `{"name":"g","rate_multiplier":0.09}`), // down
		auditRow(base.Add(3*time.Hour), `{"name":"g","description":"x"}`),      // no rate field
		auditRow(base.Add(4*time.Hour), `{"name":"g","rate_multiplier":0.15}`), // up
	}

	changes := buildGroupRateConfigChanges(3, rows)
	require.Len(t, changes, 2)

	// Newest first.
	require.Equal(t, base.Add(4*time.Hour), changes[0].ChangedAt)
	require.Equal(t, "up", changes[0].Direction)
	require.Equal(t, 0.15, changes[0].CurrentRate)
	require.Equal(t, 0.09, *changes[0].PreviousRate)
	require.Equal(t, "audit_log", changes[0].Source)

	require.Equal(t, base.Add(2*time.Hour), changes[1].ChangedAt)
	require.Equal(t, "down", changes[1].Direction)
	require.Equal(t, 0.09, changes[1].CurrentRate)
	require.Equal(t, 0.12, *changes[1].PreviousRate)
}

func TestBuildGroupRateConfigChangesIgnoresGarbage(t *testing.T) {
	rows := []GroupRateAuditRow{
		auditRow(time.Now(), `not-json`),
		auditRow(time.Now(), ``),
		auditRow(time.Now(), `{"rate_multiplier":"0.2"}`),
		auditRow(time.Now(), `{"rate_multiplier":0}`),
	}
	require.Empty(t, buildGroupRateConfigChanges(9, rows))
}

func TestParseGroupRateFromAuditBody(t *testing.T) {
	value, ok := parseGroupRateFromAuditBody(`{"rate_multiplier":0.075}`)
	require.True(t, ok)
	require.Equal(t, 0.075, value)

	_, ok = parseGroupRateFromAuditBody(`{"rate_multiplier":null}`)
	require.False(t, ok)
}
