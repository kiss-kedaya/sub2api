package service

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestPricingService_BaseNameIndexDeterministicConflictResolution(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "pricing.json")
	raw := `{
		"claude-alpha-20250901": {"input_cost_per_token": 0.000009, "output_cost_per_token": 0.000027},
		"claude-alpha": {"input_cost_per_token": 0.000003, "output_cost_per_token": 0.000015},
		"claude-alpha-v1:0": {"input_cost_per_token": 0.000007, "output_cost_per_token": 0.000021},
		"claude-beta-20250201": {"input_cost_per_token": 0.000004, "output_cost_per_token": 0.000016},
		"claude-beta-20250101": {"input_cost_per_token": 0.000002, "output_cost_per_token": 0.000010}
	}`
	require.NoError(t, os.WriteFile(catalogPath, []byte(raw), 0o644))

	svc := NewPricingService(&config.Config{
		Pricing: config.PricingConfig{DataDir: dir},
	}, nil)
	require.NoError(t, svc.loadPricingData(catalogPath))
	require.NotNil(t, svc.baseNameIndex)

	// Canonical unversioned key ("claude-alpha") must win over dated/versioned siblings
	// when a newer dated snapshot misses exact lookup.
	for i := 0; i < 20; i++ {
		got := svc.GetIdentifiedModelPricing("claude-alpha-20261201")
		require.NotNil(t, got)
		require.InDelta(t, 0.000003, got.InputCostPerToken, 1e-12)
	}

	// Without an unversioned canonical key, tie-breaking must be deterministic
	// (lexicographically smallest key).
	for i := 0; i < 20; i++ {
		got := svc.GetIdentifiedModelPricing("claude-beta-20261201")
		require.NotNil(t, got)
		require.InDelta(t, 0.000002, got.InputCostPerToken, 1e-12)
	}

	// Unknown model misses in O(1) without panicking.
	require.Nil(t, svc.GetIdentifiedModelPricing("nonexistent-model-20261201"))
}

func TestPricingService_BaseNameIndexDeterministicCaseOnlyCollision(t *testing.T) {
	svc := NewPricingService(&config.Config{}, nil)
	upper := &LiteLLMModelPricing{InputCostPerToken: 0.000001}
	lower := &LiteLLMModelPricing{InputCostPerToken: 0.000009}
	data := map[string]*LiteLLMModelPricing{
		"Claude-Alpha-20250101": upper,
		"claude-alpha-20250101": lower,
		"CLAUDE-BETA-20250101":  nil,
		"claude-beta-20250101":  lower,
	}

	// Repeat across many iterations so randomized Go map iteration visits keys in
	// different orders; raw-key tie-breaking ("C" < "c") and non-nil preference
	// must pick the same entry every time.
	for i := 0; i < 50; i++ {
		idx := svc.buildBaseNameIndex(data)
		require.Same(t, upper, idx["claude-alpha"])
		require.Same(t, lower, idx["claude-beta"])
	}
}

func TestPricingService_BaseNameIndexFallsBackWhenPricingDataMutatedDirectly(t *testing.T) {
	svc := NewPricingService(&config.Config{}, nil)
	svc.pricingData["custom-direct-20250101"] = &LiteLLMModelPricing{InputCostPerToken: 1.5e-6}

	got := svc.GetIdentifiedModelPricing("custom-direct-20260501")
	require.NotNil(t, got)
	require.InDelta(t, 1.5e-6, got.InputCostPerToken, 1e-12)
}

func BenchmarkPricingService_BaseNameLookupMiss(b *testing.B) {
	dir := b.TempDir()
	catalogPath := filepath.Join(dir, "pricing.json")
	var sb []byte
	sb = append(sb, '{')
	for i := 0; i < 2000; i++ {
		if i > 0 {
			sb = append(sb, ',')
		}
		sb = append(sb, fmt.Sprintf(`"vendor-model-%04d-20250101":{"input_cost_per_token":0.000001,"output_cost_per_token":0.000002}`, i)...)
	}
	sb = append(sb, '}')
	if err := os.WriteFile(catalogPath, sb, 0o644); err != nil {
		b.Fatal(err)
	}

	svc := NewPricingService(&config.Config{Pricing: config.PricingConfig{DataDir: dir}}, nil)
	if err := svc.loadPricingData(catalogPath); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = svc.GetIdentifiedModelPricing("unknown-model-20260101")
	}
}
