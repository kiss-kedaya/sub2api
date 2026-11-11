package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Managed proxies persist "enc:managed:v1:" + base64(nonce+ciphertext+tag), and a
// generated 64-character password becomes 139 bytes at rest. The previous
// VARCHAR(100) column rejected every managed static-node import.
func TestProxiesPasswordWideningMigration(t *testing.T) {
	content, err := FS.ReadFile("313_widen_proxies_password.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE proxies ALTER COLUMN password TYPE VARCHAR(255)")

	// Plain, transactional, forward-only schema change: no data is rewritten and
	// no non-transactional migration suffix is needed.
	lower := strings.ToLower(string(content))
	require.NotContains(t, lower, "concurrent")
	require.NotContains(t, lower, "drop column")
	require.NotContains(t, lower, "not null")
}
