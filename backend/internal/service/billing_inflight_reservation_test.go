//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestEstimateInflightReservationCost(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	pricing, err := billing.GetModelPricing("claude-sonnet-4-5")
	require.NoError(t, err)

	cfg := config.InflightReservationConfig{DefaultMaxTokens: 8192, MaxOutputTokens: 64000, MaxInputTokens: 1000}

	// explicit max_tokens; body 400 bytes → 100 input tokens
	cost, ok := EstimateInflightReservationCost(billing, cfg, "claude-sonnet-4-5", 400, 1000, 1)
	require.True(t, ok)
	require.InDelta(t, 100*pricing.InputPricePerToken+1000*pricing.OutputPricePerToken, cost, 1e-12)

	// missing max_tokens → default; rate multiplier applied
	cost, ok = EstimateInflightReservationCost(billing, cfg, "claude-sonnet-4-5", 0, 0, 2)
	require.True(t, ok)
	require.InDelta(t, 2*8192*pricing.OutputPricePerToken, cost, 1e-12)

	// caps: input and output clamped
	cost, ok = EstimateInflightReservationCost(billing, cfg, "claude-sonnet-4-5", 1_000_000, 1_000_000, 1)
	require.True(t, ok)
	require.InDelta(t, 1000*pricing.InputPricePerToken+64000*pricing.OutputPricePerToken, cost, 1e-12)

	// free group / missing billing → fail open
	_, ok = EstimateInflightReservationCost(billing, cfg, "claude-sonnet-4-5", 10, 10, 0)
	require.False(t, ok)
	_, ok = EstimateInflightReservationCost(nil, cfg, "claude-sonnet-4-5", 10, 10, 1)
	require.False(t, ok)
	_, ok = EstimateInflightReservationCost(billing, cfg, "", 10, 10, 1)
	require.False(t, ok)
}

func TestReserveInflightBalance_NoReservationCacheFailsOpen(t *testing.T) {
	cfg := &config.Config{}
	cfg.Billing.InflightReservation.Enabled = true
	svc := NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)
	release, err := svc.ReserveInflightBalance(context.Background(), &User{ID: 1}, nil, nil, 100)
	require.NoError(t, err)
	release()
}

func TestReserveInflightBalance_SimpleModeDisabled(t *testing.T) {
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Billing.InflightReservation.Enabled = true
	svc := NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)
	require.False(t, svc.InflightReservationEnabled())
}
