package handler

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRequestMaxOutputTokens(t *testing.T) {
	require.Equal(t, 1024, requestMaxOutputTokens([]byte(`{"max_tokens":1024}`)))
	require.Equal(t, 2048, requestMaxOutputTokens([]byte(`{"max_completion_tokens":2048}`)))
	require.Equal(t, 4096, requestMaxOutputTokens([]byte(`{"max_output_tokens":4096}`)))
	require.Equal(t, 512, requestMaxOutputTokens([]byte(`{"generationConfig":{"maxOutputTokens":512}}`)))
	require.Equal(t, 0, requestMaxOutputTokens([]byte(`{"max_tokens":"x"}`)))
	require.Equal(t, 0, requestMaxOutputTokens([]byte(`{}`)))
}

type countingEstimator struct{ calls int }

func (e *countingEstimator) EstimateInflightReservation(context.Context, *service.APIKey, string, int, int) float64 {
	e.calls++
	return 1
}

func TestReserveInflightBalance_SkipsWhenDisabledOrSubscription(t *testing.T) {
	cfg := &config.Config{}
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	est := &countingEstimator{}
	apiKey := &service.APIKey{User: &service.User{ID: 1}}

	release, err := reserveInflightBalance(context.Background(), billing, est, apiKey, nil, "m", []byte(`{}`))
	require.NoError(t, err)
	release()
	require.Equal(t, 0, est.calls, "disabled switch must not even estimate")

	cfg.Billing.InflightReservation.Enabled = true
	apiKey.Group = &service.Group{SubscriptionType: service.SubscriptionTypeSubscription}
	release, err = reserveInflightBalance(context.Background(), billing, est, apiKey, &service.UserSubscription{}, "m", []byte(`{}`))
	require.NoError(t, err)
	release()
	require.Equal(t, 0, est.calls, "subscription mode must be unaffected")
}
