package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestModelWhitelistAutoSyncConfigReadsUIObject(t *testing.T) {
	extra := map[string]any{
		ModelWhitelistAutoSyncExtraKey: map[string]any{
			"enabled":        true,
			"interval_hours": float64(6),
			"mode":           "whitelist",
		},
	}

	enabled, interval, ok := modelWhitelistAutoSyncConfig(extra)
	require.True(t, ok)
	require.True(t, enabled)
	require.Equal(t, 6*time.Hour, interval)

	view, hasConfig := ModelWhitelistAutoSyncConfigFromExtra(extra)
	require.True(t, hasConfig)
	require.True(t, view.Enabled)
	require.Equal(t, 6, view.IntervalHours)
	require.Equal(t, ModelWhitelistAutoSyncModeWhitelist, view.Mode)
}

func TestModelWhitelistAutoSyncConfigMissingOrDisabled(t *testing.T) {
	enabled, _, ok := modelWhitelistAutoSyncConfig(nil)
	require.False(t, ok)
	require.False(t, enabled)

	enabled, interval, ok := modelWhitelistAutoSyncConfig(map[string]any{
		ModelWhitelistAutoSyncExtraKey: map[string]any{"enabled": false},
	})
	require.True(t, ok)
	require.False(t, enabled)
	require.Equal(t, 24*time.Hour, interval, "default interval applies when omitted")
}

// A non-identity mapping is an administrator's routing and must block the job.
func TestModelWhitelistBaselineMappingDetectsCustomMapping(t *testing.T) {
	identity, custom, err := modelWhitelistBaselineMapping(&Account{Credentials: map[string]any{
		"model_mapping": map[string]any{"gpt-5": "gpt-5", "gpt-5-mini": "gpt-5-mini"},
	}})
	require.NoError(t, err)
	require.False(t, custom)
	require.Equal(t, map[string]string{"gpt-5": "gpt-5", "gpt-5-mini": "gpt-5-mini"}, identity)

	_, custom, err = modelWhitelistBaselineMapping(&Account{Credentials: map[string]any{
		"model_mapping": map[string]any{"claude-sonnet-4": "upstream-sonnet"},
	}})
	require.NoError(t, err)
	require.True(t, custom)

	_, custom, err = modelWhitelistBaselineMapping(&Account{Credentials: map[string]any{
		"model_mapping": []string{"a", "b"},
	}})
	require.NoError(t, err)
	require.True(t, custom, "non-object mapping is treated as custom")
}

func TestModelWhitelistBaselineMappingEmpty(t *testing.T) {
	baseline, custom, err := modelWhitelistBaselineMapping(&Account{})
	require.NoError(t, err)
	require.False(t, custom)
	require.Empty(t, baseline)
}

func TestNormalizeModelWhitelistModelsDedupesAndSorts(t *testing.T) {
	models := normalizeModelWhitelistModels(&UpstreamModelCatalog{
		Models: []string{" gpt-5", "gpt-5", "", "claude-sonnet-4", "gemini-2.5-pro"},
	})
	require.Equal(t, []string{"claude-sonnet-4", "gemini-2.5-pro", "gpt-5"}, models)
	require.Empty(t, normalizeModelWhitelistModels(nil))
}

func TestModelWhitelistBackoffGrowsAndCaps(t *testing.T) {
	interval := 6 * time.Hour

	require.Equal(t, interval, effectiveModelWhitelistInterval(interval, 0))
	require.Equal(t, 6*time.Hour, modelWhitelistBackoff(interval, 1), "first failure keeps the cadence")
	require.Equal(t, 10*time.Minute, modelWhitelistBackoff(time.Minute, 2))
	require.Equal(t, modelWhitelistAutoSyncBackoffMax, modelWhitelistBackoff(time.Minute, 30))
}

func TestModelWhitelistAutoSyncStateRoundTrip(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	state := &ModelWhitelistAutoSyncState{
		LastRunAt:           modelWhitelistTimePtr(now),
		LastSuccessAt:       modelWhitelistTimePtr(now.Add(-time.Hour)),
		NextRunAt:           modelWhitelistTimePtr(now.Add(6 * time.Hour)),
		LastError:           "boom",
		LastErrorAt:         modelWhitelistTimePtr(now),
		ConsecutiveFailures: 2,
		LastAppliedModels:   41,
	}

	extra := map[string]any{ModelWhitelistAutoSyncStateExtraKey: state.toMap()}
	decoded := decodeModelWhitelistAutoSyncState(extra)
	require.NotNil(t, decoded)
	require.Equal(t, now, decoded.LastRunAt.UTC())
	require.Equal(t, 2, decoded.ConsecutiveFailures)
	require.Equal(t, 41, decoded.LastAppliedModels)
	require.Equal(t, "boom", decoded.LastError)

	require.Nil(t, decodeModelWhitelistAutoSyncState(map[string]any{}))
}
