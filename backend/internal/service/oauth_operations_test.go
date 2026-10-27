package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestScopedQualityDoesNotStopOtherModelsOrGroups(t *testing.T) {
	a := &Account{Status: StatusActive, Schedulable: true, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{LocalQualityModelPausesKey: map[string]any{"7": map[string]any{"model": "model-a", "group_id": float64(9), "token": "fixture"}}}}
	g9, g10 := int64(9), int64(10)
	require.True(t, a.IsSchedulable())
	require.False(t, a.IsSchedulableForModelWithContext(WithLocalQualityGroup(context.Background(), &g9), "model-a"))
	require.True(t, a.IsSchedulableForModelWithContext(WithLocalQualityGroup(context.Background(), &g10), "model-a"))
	require.True(t, a.IsSchedulableForModelWithContext(WithLocalQualityGroup(context.Background(), &g9), "model-b"))
	a.Extra[OAuthAdmissionPauseKey] = map[string]any{"token": "pending"}
	require.False(t, a.IsSchedulable())
}
func TestOAuthFallbackRequiresFreshQuotaQualityAndCapacity(t *testing.T) {
	s := NewOpsAutomationService(nil, nil, nil, nil)
	cfg := DefaultOpsAutomationConfig()
	cfg.OAuthFallback.Enabled = true
	cfg.OAuthFallback.GroupIDs = []int64{9}
	s.config.Store(cfg)
	quality := 1.0
	s.ranks[automationRankKey(9, "fixture")] = automationRankEntry{expires: time.Now().Add(time.Minute), signals: map[int64]AutomationRankSignal{2: {Quality: &quality, QualitySamples: 2}}}
	api := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Priority: 1}
	oauth := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Priority: 1, Concurrency: 3, Extra: map[string]any{"codex_usage_updated_at": time.Now().UTC().Format(time.RFC3339Nano), "codex_primary_used_percent": float64(10)}}
	loads := map[int64]*AccountLoadInfo{2: {AccountID: 2, CurrentConcurrency: 1, LoadRate: 33}}
	stratum := func(a *Account) string { return oauthFallbackStratum(a, false, false) }
	ordered := s.PreferQualifiedOAuth(9, "fixture", []*Account{api, oauth}, loads, stratum)
	require.Equal(t, int64(2), ordered[0].ID)
	loads[2].LoadRate = 100
	ordered = s.PreferQualifiedOAuth(9, "fixture", []*Account{oauth, api}, loads, stratum)
	require.Equal(t, int64(1), ordered[0].ID)
	loads[2].LoadRate = 0
	delete(oauth.Extra, "codex_primary_used_percent")
	ordered = s.PreferQualifiedOAuth(9, "fixture", []*Account{oauth, api}, loads, stratum)
	require.Equal(t, int64(1), ordered[0].ID)
	cfg.OAuthFallback.Enabled = false
	s.config.Store(cfg)
	require.Equal(t, []*Account{api, oauth}, s.PreferQualifiedOAuth(9, "fixture", []*Account{api, oauth}, loads, stratum))
}
func TestOAuthOperationsOffByDefaultAndThresholdsValidate(t *testing.T) {
	c := DefaultOpsAutomationConfig()
	require.False(t, c.OAuthFallback.Enabled)
	require.False(t, c.Admission.Enabled)
	require.NoError(t, ValidateOpsAutomationConfig(c))
	c.Admission.Enabled = true
	require.Error(t, ValidateOpsAutomationConfig(c))
	c.Admission.Platforms = []string{PlatformOpenAI}
	c.Admission.Model = "fixture"
	require.NoError(t, ValidateOpsAutomationConfig(c))
	c.OAuthFallback.MinimumQuality = 2
	require.Error(t, ValidateOpsAutomationConfig(c))
}
