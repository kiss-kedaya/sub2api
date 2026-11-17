package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCalculateOCRCostUsesOfficialDefault(t *testing.T) {
	svc := &BillingService{}
	cost := svc.CalculateOCRCost(2000, nil, 1)
	// Official list price: 1.5 per 1,000 pages -> 2,000 pages = 3.0
	require.InDelta(t, 3.0, cost.TotalCost, 1e-12)
	require.InDelta(t, 3.0, cost.ActualCost, 1e-12)
	override, zero, negative := 10.0, 0.0, -1.0
	require.InDelta(t, 5.0, svc.CalculateOCRCost(500, &override, 1).ActualCost, 1e-12)
	require.Zero(t, svc.CalculateOCRCost(1000, &zero, 1).ActualCost)
	require.Zero(t, svc.CalculateOCRCost(1000, &negative, 1).ActualCost)
	require.Zero(t, svc.CalculateOCRCost(0, nil, 1).ActualCost)
	cost = svc.CalculateOCRCost(1000, nil, 0.5)
	require.InDelta(t, 1.5, cost.TotalCost, 1e-12)
	require.InDelta(t, 0.75, cost.ActualCost, 1e-12)
}

func TestOfficialSpeechDefaults(t *testing.T) {
	svc := &BillingService{}
	require.Equal(t, 15.0, svc.CalculateAudioCost("tts", 1, nil, 1).ActualCost, "existing xAI TTS must not change")
	require.Equal(t, 0.10, svc.CalculateAudioCost("stt", 1, nil, 1).ActualCost, "existing xAI STT must not change")
	require.Equal(t, 1.5, defaultOCRPricePer1kPages)
	require.Equal(t, 300.0, defaultDeepSeekTTSPricePerMillionChars)
	require.Equal(t, 0.78, defaultDeepSeekASRPricePerHour)
	require.Equal(t, 5.0, defaultSearchPricePer1k)
}
