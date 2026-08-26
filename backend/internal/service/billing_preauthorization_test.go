package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEstimateBillingPreauthorizationUsesConservativeInputRate(t *testing.T) {
	estimate, err := EstimateBillingPreauthorization(BillingPreauthorizationEstimateInput{
		BillableInputBytes:        1000,
		InputPricePerToken:        2e-6,
		CacheReadPricePerToken:    3e-6,
		OutputPricePerToken:       10e-6,
		RateMultiplier:            1.25,
		InitialOutputWindowTokens: 512,
	})

	require.NoError(t, err)
	require.Equal(t, 1000, estimate.InputTokenUpperBound)
	require.Equal(t, 512, estimate.ReservedOutputTokens)
	require.Equal(t, 0.01015, estimate.HoldAmount)
}

func TestEstimateBillingPreauthorizationUsesHigherCacheCreationPrice(t *testing.T) {
	estimate, err := EstimateBillingPreauthorization(BillingPreauthorizationEstimateInput{
		BillableInputBytes:         100,
		InputPricePerToken:         0.001,
		CacheReadPricePerToken:     0.002,
		CacheCreationPricePerToken: 0.003,
		OutputPricePerToken:        0.004,
		RateMultiplier:             1,
		InitialOutputWindowTokens:  10,
	})
	require.NoError(t, err)
	require.InDelta(t, 0.34, estimate.HoldAmount, 1e-12)
}

func TestEstimateBillingPreauthorizationRoundsHoldUp(t *testing.T) {
	estimate, err := EstimateBillingPreauthorization(BillingPreauthorizationEstimateInput{
		BillableInputBytes: 1,
		InputPricePerToken: 0.000000001,
		RateMultiplier:     1,
	})

	require.NoError(t, err)
	require.Equal(t, 0.00000001, estimate.HoldAmount)
}

func TestPlanBillingOutputHoldTopUpUsesWholeWindows(t *testing.T) {
	topUp, err := PlanBillingOutputHoldTopUp(1024, 900, 512, 10e-6, 1)

	require.NoError(t, err)
	require.Equal(t, 512, topUp.AdditionalTokens)
	require.Equal(t, 0.00512, topUp.AdditionalAmount)

	unchanged, err := PlanBillingOutputHoldTopUp(2048, 1000, 512, 10e-6, 1)
	require.NoError(t, err)
	require.Zero(t, unchanged)
}

func TestBillingPreauthorizationEstimateRejectsInvalidValues(t *testing.T) {
	_, err := EstimateBillingPreauthorization(BillingPreauthorizationEstimateInput{
		BillableInputBytes: 1,
		InputPricePerToken: math.NaN(),
		RateMultiplier:     1,
	})
	require.ErrorIs(t, err, ErrInvalidBillingPreauthorizationEstimate)

	_, err = PlanBillingOutputHoldTopUp(1, 1, 0, 1, 1)
	require.ErrorIs(t, err, ErrInvalidBillingPreauthorizationEstimate)
}
