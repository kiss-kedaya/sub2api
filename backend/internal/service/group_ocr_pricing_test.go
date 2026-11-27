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
	_, used, err := svc.applyAuthCacheEntry("local-auth-fixture", &APIKeyAuthCacheEntry{Snapshot: &APIKeyAuthSnapshot{Version: apiKeyAuthSnapshotVersion, Group: &legacy}})
	require.NoError(t, err)
	require.False(t, used, "legacy DeepSeek snapshots without tool_pricing_config_loaded must reload from DB")

	var current APIKeyAuthGroupSnapshot
	require.NoError(t, json.Unmarshal([]byte(`{"id":36,"platform":"deepseek","search_provider_config_loaded":true,"tool_pricing_config_loaded":true}`), &current))
	loaded, used, err := svc.applyAuthCacheEntry("local-auth-fixture", &APIKeyAuthCacheEntry{Snapshot: &APIKeyAuthSnapshot{Version: apiKeyAuthSnapshotVersion, Group: &current}})
	require.NoError(t, err)
	require.True(t, used)
	require.Nil(t, loaded.Group.OCRPricePer1kPages)
	require.False(t, groupMediaPricingLooksIncomplete(&Group{OCRPricePer1kPages: float64Ptr(0)}))
}

func TestDeepSeekMediaInflightReservationUsesToolMultiplierAndUnits(t *testing.T) {
	group := &Group{ID: 36, Platform: PlatformDeepseek, RateMultiplier: .3}
	key := &APIKey{Group: group, GroupID: &group.ID, User: &User{ID: 1}}
	deps := inflightEstimateDeps{billing: &BillingService{}}
	for _, tc := range []struct {
		model, mode string
		units       int
		audioUnits  float64
		baseCost    float64
	}{
		{DeepSeekToolsLabelSearch, "", 1, 0, .005},
		{DeepSeekToolsLabelFetch, "", 1, 0, .005},
		{DeepSeekToolsLabelASR, "stt", 0, 120.0 / 3600, 120.0 / 3600 * .78},
		{DeepSeekToolsLabelTTS, "tts", 0, 19.0 / 1_000_000, .0057},
		{DeepSeekToolsLabelOCR, "", 10, 0, 10.0 / 1000 * 1.5},
		{DeepSeekToolsLabelOCR, "", 0, 0, 1.0 / 1000 * 1.5},
	} {
		t.Run(tc.model, func(t *testing.T) {
			group.ToolRateIndependent, group.ToolRateMultiplier = false, 1.0
			cost, priced := deps.estimate(t.Context(), key, InflightEstimateRequest{Model: tc.model, Kind: InflightEstimatePerRequest, Units: tc.units, AudioMode: tc.mode, AudioUnits: tc.audioUnits})
			require.True(t, priced)
			require.InDelta(t, tc.baseCost*0.3, cost, 1e-12)

			group.ToolRateIndependent, group.ToolRateMultiplier = true, 1.0
			cost, priced = deps.estimate(t.Context(), key, InflightEstimateRequest{Model: tc.model, Kind: InflightEstimatePerRequest, Units: tc.units, AudioMode: tc.mode, AudioUnits: tc.audioUnits})
			require.True(t, priced)
			require.InDelta(t, tc.baseCost, cost, 1e-12)

			zero := 0.0
			group.SearchPricePer1k, group.FetchPricePer1k = &zero, &zero
			group.OCRPricePer1kPages, group.AudioTTSPricePerMillionChars, group.AudioSTTPricePerHour = &zero, &zero, &zero
			cost, priced = deps.estimate(t.Context(), key, InflightEstimateRequest{Model: tc.model, Units: tc.units, AudioMode: tc.mode, AudioUnits: tc.audioUnits})
			require.True(t, priced)
			require.Zero(t, cost)
			group.SearchPricePer1k, group.FetchPricePer1k = nil, nil
			group.OCRPricePer1kPages, group.AudioTTSPricePerMillionChars, group.AudioSTTPricePerHour = nil, nil, nil
		})
	}
}
