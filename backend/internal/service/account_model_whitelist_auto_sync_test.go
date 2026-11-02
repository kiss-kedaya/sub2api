package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateModelWhitelistAutoSyncExtraAcceptsUIConfig(t *testing.T) {
	extra := map[string]any{
		ModelWhitelistAutoSyncExtraKey: map[string]any{
			"enabled":        true,
			"interval_hours": float64(24), // JSON numbers decode to float64
			"mode":           "whitelist",
		},
	}

	require.NoError(t, ValidateModelWhitelistAutoSyncExtra(extra))

	config, ok := extra[ModelWhitelistAutoSyncExtraKey].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, config["enabled"])
	require.Equal(t, 24, config["interval_hours"], "hours must be stored as an integer")
	require.Equal(t, "whitelist", config["mode"])
}

func TestValidateModelWhitelistAutoSyncExtraAppliesDefaults(t *testing.T) {
	extra := map[string]any{
		ModelWhitelistAutoSyncExtraKey: map[string]any{"enabled": true},
	}

	require.NoError(t, ValidateModelWhitelistAutoSyncExtra(extra))

	config, ok := extra[ModelWhitelistAutoSyncExtraKey].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 24, config["interval_hours"])
	require.Equal(t, "whitelist", config["mode"])
}

func TestValidateModelWhitelistAutoSyncExtraNilClearsKey(t *testing.T) {
	extra := map[string]any{ModelWhitelistAutoSyncExtraKey: nil}

	require.NoError(t, ValidateModelWhitelistAutoSyncExtra(extra))
	require.NotContains(t, extra, ModelWhitelistAutoSyncExtraKey)
}

func TestValidateModelWhitelistAutoSyncExtraIgnoresAbsentKey(t *testing.T) {
	extra := map[string]any{"base_rpm": 10}

	require.NoError(t, ValidateModelWhitelistAutoSyncExtra(extra))
	require.Len(t, extra, 1)
	require.NoError(t, ValidateModelWhitelistAutoSyncExtra(nil))
}

func TestValidateModelWhitelistAutoSyncExtraRejectsBadShapes(t *testing.T) {
	cases := map[string]any{
		"not an object":       "enabled",
		"enabled not bool":    map[string]any{"enabled": "yes"},
		"mode not whitelist":  map[string]any{"mode": "all"},
		"interval zero":       map[string]any{"interval_hours": 0},
		"interval too big":    map[string]any{"interval_hours": 721},
		"interval fractional": map[string]any{"interval_hours": 1.5},
		"interval string":     map[string]any{"interval_hours": "24"},
	}

	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			extra := map[string]any{ModelWhitelistAutoSyncExtraKey: value}
			require.Error(t, ValidateModelWhitelistAutoSyncExtra(extra))
		})
	}
}

func TestValidateModelWhitelistAutoSyncExtraKeepsUnknownSiblingKeys(t *testing.T) {
	extra := map[string]any{
		ModelWhitelistAutoSyncExtraKey: map[string]any{
			"enabled":        true,
			"interval_hours": 6,
			"mode":           "whitelist",
			"last_run_at":    "2026-11-02T00:00:00Z",
		},
	}

	require.NoError(t, ValidateModelWhitelistAutoSyncExtra(extra))
	config, ok := extra[ModelWhitelistAutoSyncExtraKey].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "2026-11-02T00:00:00Z", config["last_run_at"])
}

// A full single-account PUT replaces the whole extra map. The auto-sync config
// must survive such edits, otherwise the UI setting silently disappears.
func TestPreserveManagedAccountExtraKeepsAutoSyncConfig(t *testing.T) {
	account := &Account{Extra: map[string]any{
		ModelWhitelistAutoSyncExtraKey: map[string]any{
			"enabled":        true,
			"interval_hours": 12,
			"mode":           "whitelist",
		},
		"base_rpm": 10,
	}}
	replacement := map[string]any{"another_key": "value"}

	preserveManagedAccountExtra(account, replacement)

	require.Contains(t, replacement, ModelWhitelistAutoSyncExtraKey)
	require.NotContains(t, replacement, "base_rpm", "only managed keys are preserved")
	require.Equal(t, "value", replacement["another_key"])
}

func TestManagedAccountExtraKeysIncludeAutoSync(t *testing.T) {
	require.Contains(t, managedAccountExtraKeysPreservedOnUpdate(), ModelWhitelistAutoSyncExtraKey)
}
