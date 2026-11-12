package ipnotice

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDefaultSettingsKeepsKeysAlone pins the product default: a fresh
// deployment detects shared egress and appends the IP, but never disables a key
// until an operator opts in.
func TestDefaultSettingsKeepsKeysAlone(t *testing.T) {
	settings := DefaultSettings()
	require.NotNil(t, settings.KeyDisableEnabled, "the default must be persisted as an explicit boolean")
	require.False(t, settings.KeyDisable())
}

// TestKeyDisableTreatsUnsetAsFalse covers policies stored before this switch
// existed: a missing field means observe-only, never "protect the keys".
func TestKeyDisableTreatsUnsetAsFalse(t *testing.T) {
	require.False(t, Settings{}.KeyDisable())

	normalized, err := (Settings{IPs: []string{}, RPM: DefaultRPM}).Normalize()
	require.NoError(t, err)
	require.NotNil(t, normalized.KeyDisableEnabled)
	require.False(t, normalized.KeyDisable())

	raw, err := json.Marshal(normalized)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"key_disable_enabled":false`,
		"the stored policy must say observe-only explicitly")
}

// TestKeyDisableRoundTripsTheOptIn proves the switch survives Normalize and a
// JSON round trip, so the admin form and the gateway agree on the value.
func TestKeyDisableRoundTripsTheOptIn(t *testing.T) {
	enabled := true
	normalized, err := (Settings{IPs: []string{}, RPM: DefaultRPM, KeyDisableEnabled: &enabled}).Normalize()
	require.NoError(t, err)
	require.True(t, normalized.KeyDisable())

	raw, err := json.Marshal(normalized)
	require.NoError(t, err)
	var decoded Settings
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.True(t, decoded.KeyDisable())
}
