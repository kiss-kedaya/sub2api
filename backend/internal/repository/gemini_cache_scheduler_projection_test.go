//go:build unit

package repository

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeminiCacheSchedulerProjectionRetainsCapabilityAndEndpoint(t *testing.T) {
	credentials := map[string]any{
		"api_key": "fixture", "base_url": "https://example.com", "auth_mode": "api_key",
		"explicit_cache_upstream": true, "explicit_cache_upstream_max_ttl_seconds": 3600,
		"unrelated_private_setting": "must-not-be-projected",
	}
	projected := filterSchedulerCredentials(credentials)
	for _, field := range []string{"base_url", "auth_mode", "explicit_cache_upstream", "explicit_cache_upstream_max_ttl_seconds"} {
		require.Equal(t, credentials[field], projected[field], field)
	}
	_, exists := projected["unrelated_private_setting"]
	require.False(t, exists)
}
