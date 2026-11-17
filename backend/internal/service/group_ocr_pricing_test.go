package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupOCRPriceSurvivesSnapshotAndCopy(t *testing.T) {
	svc := &APIKeyService{}
	for _, price := range []*float64{nil, float64Ptr(0), float64Ptr(2.5)} {
		group := &Group{ID: 36, Platform: PlatformDeepseek, OCRPricePer1kPages: price}
		key := &APIKey{Group: group, GroupID: &group.ID, User: &User{ID: 1}}
		snapshot := svc.snapshotFromAPIKey(t.Context(), key)
		encoded, err := json.Marshal(snapshot)
		require.NoError(t, err)
		var decoded APIKeyAuthSnapshot
		require.NoError(t, json.Unmarshal(encoded, &decoded))
		loaded := svc.snapshotToAPIKey("local-auth-fixture", &decoded)
		require.Equal(t, price, loaded.Group.OCRPricePer1kPages)
		for _, clone := range []*Group{cloneSchedulerGroup(group), cloneGroupForDuplicate(group, "local-copy")} {
			require.Equal(t, price, clone.OCRPricePer1kPages)
			if price != nil {
				require.NotSame(t, price, clone.OCRPricePer1kPages)
			}
		}
	}
	var legacy APIKeyAuthGroupSnapshot
	require.NoError(t, json.Unmarshal([]byte(`{"id":36,"platform":"deepseek","search_provider_config_loaded":true}`), &legacy))
	require.Nil(t, legacy.OCRPricePer1kPages, "old snapshots must use the default, not fail decoding")
	loaded, used, err := svc.applyAuthCacheEntry("local-auth-fixture", &APIKeyAuthCacheEntry{Snapshot: &APIKeyAuthSnapshot{Version: apiKeyAuthSnapshotVersion, Group: &legacy}})
	require.NoError(t, err)
	require.True(t, used)
	require.Nil(t, loaded.Group.OCRPricePer1kPages)
	require.False(t, groupMediaPricingLooksIncomplete(&Group{OCRPricePer1kPages: float64Ptr(0)}))
}

func TestDeepSeekMediaInflightReservationUsesFixedUnits(t *testing.T) {
	group := &Group{ID: 36, Platform: PlatformDeepseek, RateMultiplier: .3}
	key := &APIKey{Group: group, GroupID: &group.ID, User: &User{ID: 1}}
	deps := inflightEstimateDeps{billing: &BillingService{}}
	for _, tc := range []struct {
		model, mode string
		units, cost float64
	}{
		{DeepSeekToolsLabelASR, "stt", 120.0 / 3600, 120.0 / 3600 * .78},
		{DeepSeekToolsLabelTTS, "tts", 19.0 / 1_000_000, .0057},
		{DeepSeekToolsLabelOCR, "", 0, 1.5},
	} {
		t.Run(tc.model, func(t *testing.T) {
			cost, priced := deps.estimate(t.Context(), key, InflightEstimateRequest{Model: tc.model, Kind: InflightEstimatePerRequest, AudioMode: tc.mode, AudioUnits: tc.units})
			require.True(t, priced)
			require.InDelta(t, tc.cost, cost, 1e-12)
			zero := 0.0
			group.OCRPricePer1kPages, group.AudioTTSPricePerMillionChars, group.AudioSTTPricePerHour = &zero, &zero, &zero
			cost, priced = deps.estimate(t.Context(), key, InflightEstimateRequest{Model: tc.model, AudioMode: tc.mode, AudioUnits: tc.units})
			require.True(t, priced)
			require.Zero(t, cost)
			group.OCRPricePer1kPages, group.AudioTTSPricePerMillionChars, group.AudioSTTPricePerHour = nil, nil, nil
		})
	}
}
