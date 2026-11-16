package service

import (
	"math"
	"testing"
)

// Official DeepSeek pricing, 1 balance unit = 1 CNY (no FX conversion):
//
//	OCR / document parsing: 1.5 per 1,000 pages
//	TTS:                    3   per 10,000 chars  = 300 per million chars
//	ASR:                    0.78 per hour of audio
func TestCalculateOCRCostUsesOfficialDefault(t *testing.T) {
	svc := &BillingService{}

	// 2,000 pages at 1.5/1,000 = 3.0
	cost := svc.CalculateOCRCost(2000, nil, 1)
	if math.Abs(cost.TotalCost-3.0) > 1e-9 {
		t.Fatalf("default 2000 pages = %v, want 3", cost.TotalCost)
	}
	if math.Abs(cost.ActualCost-3.0) > 1e-9 {
		t.Fatalf("actual cost = %v, want 3", cost.ActualCost)
	}

	// Group override wins: 10/1,000 pages.
	override := 10.0
	cost = svc.CalculateOCRCost(500, &override, 1)
	if math.Abs(cost.TotalCost-5.0) > 1e-9 {
		t.Fatalf("override 500 pages = %v, want 5", cost.TotalCost)
	}

	// Explicit zero means free, not "fall back to default".
	zero := 0.0
	if cost = svc.CalculateOCRCost(1000, &zero, 1); cost.TotalCost != 0 {
		t.Fatalf("zero override = %v, want 0", cost.TotalCost)
	}

	// Negative is rejected as free, never billed as a credit.
	negative := -1.0
	if cost = svc.CalculateOCRCost(1000, &negative, 1); cost.TotalCost != 0 {
		t.Fatalf("negative override = %v, want 0", cost.TotalCost)
	}

	// No pages, no charge.
	if cost = svc.CalculateOCRCost(0, nil, 1); cost.TotalCost != 0 {
		t.Fatalf("zero pages = %v, want 0", cost.TotalCost)
	}

	// Rate multiplier applies to the actual cost only.
	cost = svc.CalculateOCRCost(1000, nil, 0.5)
	if math.Abs(cost.TotalCost-1.5) > 1e-9 || math.Abs(cost.ActualCost-0.75) > 1e-9 {
		t.Fatalf("rate 0.5: total=%v actual=%v, want 1.5/0.75", cost.TotalCost, cost.ActualCost)
	}
}

func TestOfficialSpeechDefaults(t *testing.T) {
	if defaultAudioTTSPricePerMillionChars != 15 {
		t.Fatalf("TTS default = %v, want 15 (xAI voice)", defaultAudioTTSPricePerMillionChars)
	}
	if defaultAudioSTTPricePerHour != 0.10 {
		t.Fatalf("STT default = %v, want 0.10 (xAI voice)", defaultAudioSTTPricePerHour)
	}
	if defaultOCRPricePer1kPages != 1.5 {
		t.Fatalf("OCR default = %v, want 1.5 per 1k pages", defaultOCRPricePer1kPages)
	}
	if defaultSearchPricePer1k != 5 {
		t.Fatalf("search default = %v, want 5 per 1k", defaultSearchPricePer1k)
	}
}
