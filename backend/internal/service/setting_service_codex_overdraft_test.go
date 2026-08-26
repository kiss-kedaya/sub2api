package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type codexRuntimeErrorSettingRepo struct {
	SettingRepository
	err error
}

func (r *codexRuntimeErrorSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return nil, r.err
}

type codexRuntimeValuesSettingRepo struct {
	SettingRepository
	values map[string]string
}

func (r *codexRuntimeValuesSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return r.values, nil
}

func TestParseSettingsCodexQuotaOverdraftDefaults(t *testing.T) {
	svc := &SettingService{cfg: &config.Config{Gateway: config.GatewayConfig{CodexQuotaOverdraftEnabled: true}}}
	settings := svc.parseSettings(map[string]string{})
	require.True(t, settings.CodexQuotaOverdraftEnabled, "missing detector setting keeps the compatibility default")
	require.False(t, settings.CodexQuotaOverdraftBusinessInjectionEnabled, "business injection is fail-closed by default")
}

func TestParseSettingsCodexQuotaOverdraftMissingMasterFollowsDeploymentFallback(t *testing.T) {
	svc := &SettingService{cfg: &config.Config{Gateway: config.GatewayConfig{CodexQuotaOverdraftEnabled: false}}}
	settings := svc.parseSettings(map[string]string{})
	require.False(t, settings.CodexQuotaOverdraftEnabled, "missing DB key must reflect the deployment kill switch")
}

func TestParseSettingsCodexQuotaOverdraftDeploymentMasterClampsPersistedValues(t *testing.T) {
	svc := &SettingService{cfg: &config.Config{Gateway: config.GatewayConfig{CodexQuotaOverdraftEnabled: false}}}
	settings := svc.parseSettings(map[string]string{
		SettingKeyCodexQuotaOverdraftEnabled:                  "true",
		SettingKeyCodexQuotaOverdraftBusinessInjectionEnabled: "true",
	})
	require.False(t, settings.CodexQuotaOverdraftEnabled)
	require.False(t, settings.CodexQuotaOverdraftBusinessInjectionEnabled)
}

func TestParseSettingsCodexQuotaOverdraftExplicitValues(t *testing.T) {
	svc := &SettingService{cfg: &config.Config{Gateway: config.GatewayConfig{CodexQuotaOverdraftEnabled: true}}}
	settings := svc.parseSettings(map[string]string{
		SettingKeyCodexQuotaOverdraftEnabled:                  "false",
		SettingKeyCodexQuotaOverdraftBusinessInjectionEnabled: "true",
	})
	require.False(t, settings.CodexQuotaOverdraftEnabled, "explicit false must not be replaced by the missing-key default")
	require.False(t, settings.CodexQuotaOverdraftBusinessInjectionEnabled, "business injection is ineffective while the deployment master is off")
}

func TestCodexQuotaOverdraftRuntimeFailsClosedWhenDatabaseUnavailable(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{
		CodexQuotaOverdraftEnabled:                  true,
		CodexQuotaOverdraftBusinessInjectionEnabled: true,
	}}
	svc := &SettingService{
		cfg:         cfg,
		settingRepo: &codexRuntimeErrorSettingRepo{err: errors.New("database unavailable")},
	}
	runtime := svc.GetCodexQuotaOverdraftRuntime(context.Background())
	require.False(t, runtime.Enabled)
	require.False(t, runtime.BusinessInjectionEnabled)
}

func TestCodexQuotaOverdraftRuntimeHonorsDeploymentMasterUpperBound(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{CodexQuotaOverdraftEnabled: false}}
	svc := &SettingService{
		cfg: cfg,
		settingRepo: &codexRuntimeValuesSettingRepo{values: map[string]string{
			SettingKeyCodexQuotaOverdraftEnabled:                  "true",
			SettingKeyCodexQuotaOverdraftBusinessInjectionEnabled: "true",
		}},
	}
	runtime := svc.GetCodexQuotaOverdraftRuntime(context.Background())
	require.False(t, runtime.Enabled)
	require.False(t, runtime.BusinessInjectionEnabled)
}
