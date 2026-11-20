package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadKeyRotationGateDefaults(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)

	require.True(t, cfg.Security.KeyRotationGateEnabled, "key_rotation_gate_enabled must default to true")
	require.Equal(t, DefaultKeyRotationCutoff, cfg.Security.KeyRotationCutoff)
	require.Equal(t, "2026-11-21T00:00:00+08:00", cfg.Security.KeyRotationCutoff)

	enabled, cutoff := cfg.KeyRotationGateSettings()
	require.True(t, enabled)
	require.True(t, cutoff.Equal(time.Date(2026, 11, 21, 0, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))),
		"cutoff must resolve to 2026-11-21T00:00:00+08:00, got %s", cutoff)
}

func TestLoadKeyRotationGateEnvOverrides(t *testing.T) {
	resetViperWithJWTSecret(t)
	t.Setenv("SECURITY_KEY_ROTATION_GATE_ENABLED", "false")
	t.Setenv("SECURITY_KEY_ROTATION_CUTOFF", "2027-01-01T00:00:00Z")

	cfg, err := Load()
	require.NoError(t, err)
	require.False(t, cfg.Security.KeyRotationGateEnabled)
	require.Equal(t, "2027-01-01T00:00:00Z", cfg.Security.KeyRotationCutoff)
}

func TestValidateRejectsInvalidKeyRotationCutoff(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)

	cfg.Security.KeyRotationCutoff = "not-a-timestamp"
	err = cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "security.key_rotation_cutoff")
}

func TestValidateDefaultsEmptyKeyRotationCutoff(t *testing.T) {
	resetViperWithJWTSecret(t)
	cfg, err := Load()
	require.NoError(t, err)

	cfg.Security.KeyRotationCutoff = "   "
	require.NoError(t, cfg.Validate())
	require.Equal(t, DefaultKeyRotationCutoff, cfg.Security.KeyRotationCutoff)
}

func TestKeyRotationGateSettingsFallbacks(t *testing.T) {
	// Programmatically constructed configs (tests, embedders) bypass viper
	// defaults: an empty cutoff must fall back instead of disabling the gate.
	cfg := &Config{}
	enabled, cutoff := cfg.KeyRotationGateSettings()
	require.False(t, enabled)
	require.Equal(t, DefaultKeyRotationCutoffTime(), cutoff)

	var nilCfg *Config
	enabled, cutoff = nilCfg.KeyRotationGateSettings()
	require.True(t, enabled)
	require.Equal(t, DefaultKeyRotationCutoffTime(), cutoff)
}
