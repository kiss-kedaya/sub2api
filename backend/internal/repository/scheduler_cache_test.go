package repository

import (
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestFilterSchedulerCredentialsKeepsSubscriptionPlanType(t *testing.T) {
	filtered := filterSchedulerCredentials(map[string]any{
		"plan_type":     "plus",
		"access_token":  "secret-access-token",
		"refresh_token": "secret-refresh-token",
	})

	require.Equal(t, "plus", filtered["plan_type"])
	require.NotContains(t, filtered, "access_token")
	require.NotContains(t, filtered, "refresh_token")
}

func TestSchedulerMetadataAccountKeepsOpenAISubscriptionIdentity(t *testing.T) {
	account := service.Account{
		ID:       24,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeOAuth,
		Credentials: map[string]any{
			"plan_type":    "plus",
			"access_token": "secret-access-token",
		},
	}

	metadata := buildSchedulerMetadataAccount(account)

	require.True(t, metadata.IsOpenAIChatGPTSubscription())
	require.Empty(t, metadata.GetCredential("access_token"))
}

func TestSchedulerMetadataAccountProjectsUpstreamBillingProbe(t *testing.T) {
	lastError := strings.Repeat("upstream diagnostic ", 512)
	probe := map[string]any{
		"status": "ok",
		"data": map[string]any{
			"billing_scope":             "token",
			"resolved_rate_multiplier":  0.03,
			"peak_rate_enabled":         true,
			"peak_start":                "09:00",
			"peak_end":                  "18:00",
			"peak_rate_multiplier":      2.0,
			"timezone":                  "Asia/Shanghai",
			"effective_rate_multiplier": 0.03,
			"remote_diagnostic":         lastError,
		},
		"received_at":   "2026-07-13T10:00:00Z",
		"fresh_until":   "2026-07-13T11:00:00Z",
		"next_probe_at": "2026-07-13T10:30:00Z",
		"http_status":   502,
		"last_error":    lastError,
	}
	account := service.Account{
		ID: 42,
		Extra: map[string]any{
			"upstream_billing_probe": probe,
			"unused_large_field":     "drop-me",
		},
	}

	metadata := buildSchedulerMetadataAccount(account)
	fullPayload, metaPayload, err := marshalSchedulerCacheAccount(account)
	require.NoError(t, err)

	filtered, ok := metadata.Extra["upstream_billing_probe"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ok", filtered["status"])
	require.Equal(t, "2026-07-13T10:00:00Z", filtered["received_at"])
	require.Equal(t, "2026-07-13T11:00:00Z", filtered["fresh_until"])
	require.Equal(t, "2026-07-13T10:30:00Z", filtered["next_probe_at"])
	require.NotContains(t, filtered, "http_status")
	require.NotContains(t, filtered, "last_error")
	filteredData, ok := filtered["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "token", filteredData["billing_scope"])
	require.Equal(t, 0.03, filteredData["resolved_rate_multiplier"])
	require.Equal(t, true, filteredData["peak_rate_enabled"])
	require.Equal(t, "09:00", filteredData["peak_start"])
	require.Equal(t, "18:00", filteredData["peak_end"])
	require.Equal(t, 2.0, filteredData["peak_rate_multiplier"])
	require.Equal(t, "Asia/Shanghai", filteredData["timezone"])
	require.NotContains(t, filteredData, "effective_rate_multiplier")
	require.NotContains(t, filteredData, "remote_diagnostic")
	require.NotContains(t, metadata.Extra, "unused_large_field")
	require.Contains(t, string(fullPayload), lastError)
	require.NotContains(t, string(metaPayload), "last_error")
	require.Less(t, len(metaPayload)*4, len(fullPayload))
}

func TestSchedulerMetadataAccountDropsInvalidUpstreamBillingProbe(t *testing.T) {
	for _, probe := range []any{
		"invalid",
		map[string]any{},
		map[string]any{"status": ""},
	} {
		metadata := buildSchedulerMetadataAccount(service.Account{
			Extra: map[string]any{service.UpstreamBillingProbeExtraKey: probe},
		})

		require.NotContains(t, metadata.Extra, service.UpstreamBillingProbeExtraKey)
	}
}

// #436: the scheduler reads account metadata from the sched:meta projection, not
// from the full credentials payload. openai_capabilities must survive that
// projection or every snapshot-only scheduling decision rejects the account.
func TestFilterSchedulerCredentialsKeepsOpenAICapabilities(t *testing.T) {
	capabilities := map[string]any{"seedance": true}
	filtered := filterSchedulerCredentials(map[string]any{
		"openai_capabilities": capabilities,
		"access_token":        "secret-access-token",
	})

	require.Equal(t, capabilities, filtered["openai_capabilities"])
	require.NotContains(t, filtered, "access_token")
}

func TestSeedanceSchedulerMetadataKeepsCapability(t *testing.T) {
	account := service.Account{
		ID:       1106,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":             "ark-secret",
			"base_url":            "https://ark.cn-beijing.volces.com/api/v3",
			"openai_capabilities": map[string]any{"seedance": true},
		},
	}
	require.True(t, account.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilitySeedance))

	metadata := buildSchedulerMetadataAccount(account)

	require.True(t, metadata.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilitySeedance),
		"snapshot-only scheduling must keep the operator capability opt-in")
	require.NotContains(t, metadata.Credentials, "access_token")
}

// GetSnapshot serves sched:meta, not the full account payload. Decode the exact
// metadata payload a cache hit yields and require the capability gate to agree
// with the DB fallback path (which hydrates full credentials).
func TestSeedanceSchedulerSnapshotMetadataKeepsCapability(t *testing.T) {
	account := service.Account{
		ID:       1107,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":             "ark-secret",
			"base_url":            "https://ark.cn-beijing.volces.com/api/v3",
			"openai_capabilities": map[string]any{"seedance": true},
		},
	}
	fullPayload, metaPayload, err := marshalSchedulerCacheAccount(account)
	require.NoError(t, err)

	fromDBFallback := account
	require.True(t, fromDBFallback.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilitySeedance))

	cached, err := decodeCachedAccount(metaPayload)
	require.NoError(t, err)
	require.True(t, cached.SupportsOpenAIEndpointCapability(service.OpenAIEndpointCapabilitySeedance),
		"snapshot hit and DB fallback must agree on capability eligibility")
	require.NotContains(t, string(metaPayload), "access_token")
	require.Contains(t, string(fullPayload), "ark-secret")
}
