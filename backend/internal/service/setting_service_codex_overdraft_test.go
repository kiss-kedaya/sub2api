package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestParseSettingsCodexQuotaOverdraftDefaults(t *testing.T) {
	svc := &SettingService{cfg: &config.Config{}}
	settings := svc.parseSettings(map[string]string{})
	require.True(t, settings.CodexQuotaOverdraftEnabled, "missing detector setting keeps the compatibility default")
	require.False(t, settings.CodexQuotaOverdraftBusinessInjectionEnabled, "business injection is fail-closed by default")
}

func TestParseSettingsCodexQuotaOverdraftExplicitValues(t *testing.T) {
	svc := &SettingService{cfg: &config.Config{}}
	settings := svc.parseSettings(map[string]string{
		SettingKeyCodexQuotaOverdraftEnabled:                  "false",
		SettingKeyCodexQuotaOverdraftBusinessInjectionEnabled: "true",
	})
	require.False(t, settings.CodexQuotaOverdraftEnabled, "explicit false must not be replaced by the missing-key default")
	require.True(t, settings.CodexQuotaOverdraftBusinessInjectionEnabled)
}
