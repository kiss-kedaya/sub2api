package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Proxy passwords are now stored as "enc:v1:" + base64(nonce || AES-256-GCM
// ciphertext || tag). The legacy managed-proxy format fit VARCHAR(255), but a
// long upstream password expands past that limit after base64 encoding, so the
// column must be TEXT before the lazy backfill rewrites rows.
func TestEncryptSecretsAtRestWidensProxyPassword(t *testing.T) {
	content, err := FS.ReadFile("328_encrypt_secrets_at_rest.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "ALTER TABLE proxies ALTER COLUMN password TYPE TEXT")

	// Plain, transactional, forward-only schema change: no data is rewritten
	// inside the migration (the Go backfill owns encryption) and no
	// non-transactional suffix is needed.
	lower := strings.ToLower(string(content))
	require.NotContains(t, lower, "concurrent")
	require.NotContains(t, lower, "drop column")
	require.NotContains(t, lower, "not null")
}
