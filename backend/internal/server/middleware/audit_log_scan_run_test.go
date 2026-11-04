package middleware

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCryptoScanRunAuditActionsAreStable(t *testing.T) {
	// Starting a scan, sync or async, is one route and one action: ?async=1 is a
	// query parameter, not a different route key.
	require.Equal(t, "admin.crypto.scan.run", auditActionOverrides["POST /api/v1/admin/crypto/scan/run"])
	require.Equal(t, "admin.crypto.scan.run.cancel", auditActionOverrides["POST /api/v1/admin/crypto/scan/runs/:id/cancel"])
	// Reading run status is not a sensitive read and must stay out of the audit trail.
	require.NotContains(t, auditSensitiveReads, "GET /api/v1/admin/crypto/scan/runs")
	require.NotContains(t, auditSensitiveReads, "GET /api/v1/admin/crypto/scan/runs/:id")
}
