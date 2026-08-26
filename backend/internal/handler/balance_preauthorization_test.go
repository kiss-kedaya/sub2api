package handler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// preauthorizerStub captures the request the handler builds and returns a
// configurable outcome. It optionally reports RequiresPreauthorization=false to
// exercise the subscription/simple-mode skip.
type preauthorizerStub struct {
	captured       *service.BalancePreauthorizationRequest
	guard          *service.BalancePreauthorizationGuard
	err            error
	requires       bool
	requiresCalled bool
}

func (s *preauthorizerStub) Preauthorize(_ context.Context, req service.BalancePreauthorizationRequest) (*service.BalancePreauthorizationGuard, error) {
	captured := req
	s.captured = &captured
	return s.guard, s.err
}

func (s *preauthorizerStub) RequiresPreauthorization(int8) bool {
	s.requiresCalled = true
	return s.requires
}

// pricingProviderStub returns a deterministic CostInput without touching real
// pricing services.
type pricingProviderStub struct {
	lastModel     string
	lastPricingAt time.Time
}

func (s *pricingProviderStub) BalancePreauthorizationCostInput(_ context.Context, _ *service.APIKey, model string, pricingAt time.Time, _ string) service.CostInput {
	s.lastModel = model
	s.lastPricingAt = pricingAt
	return service.CostInput{Model: model, RateMultiplier: 1, PricingAt: pricingAt}
}

func perRequestTestAPIKey() *service.APIKey {
	groupID := int64(9)
	return &service.APIKey{ID: 7, UserID: 42, GroupID: &groupID}
}

// TestPreauthorizePerRequestPassesBillingUnits proves the per-request helper
// forwards the parsed count/size tier and per-request estimate kind so image
// endpoints reserve the exact request price rather than a token upper bound.
func TestPreauthorizePerRequestPassesBillingUnits(t *testing.T) {
	preauthorizer := &preauthorizerStub{requires: true}
	pricing := &pricingProviderStub{}
	pricingAt := time.Unix(1000, 0)

	_, err := preauthorizePerRequestGatewayRequest(
		context.Background(), preauthorizer, pricing,
		perRequestTestAPIKey(), nil, []byte(`{"model":"gpt-image-1","n":3}`),
		"gpt-image-1", pricingAt,
		service.PerRequestPreauthorizationEstimate{RequestCount: 3, SizeTier: "2K"},
	)
	require.NoError(t, err)
	require.NotNil(t, preauthorizer.captured)
	require.Equal(t, service.PreauthorizationEstimatePerRequest, preauthorizer.captured.EstimateKind)
	require.Equal(t, 3, preauthorizer.captured.PerRequestEstimate.RequestCount)
	require.Equal(t, "2K", preauthorizer.captured.PerRequestEstimate.SizeTier)
	require.Equal(t, int64(7), preauthorizer.captured.APIKeyID)
	require.Equal(t, int64(42), preauthorizer.captured.UserID)
	require.Equal(t, service.BillingTypeBalance, preauthorizer.captured.BillingType)
	require.Equal(t, "gpt-image-1", pricing.lastModel)
	require.Equal(t, pricingAt, pricing.lastPricingAt)
}

// TestPreauthorizePerRequestPropagatesWithholdingFailure proves an insufficient
// balance surfaces as the 403 ErrBalanceWithholdingFailed, not a generic error.
func TestPreauthorizePerRequestPropagatesWithholdingFailure(t *testing.T) {
	preauthorizer := &preauthorizerStub{requires: true, err: service.ErrBalanceWithholdingFailed}
	pricing := &pricingProviderStub{}

	_, err := preauthorizePerRequestGatewayRequest(
		context.Background(), preauthorizer, pricing,
		perRequestTestAPIKey(), nil, []byte(`{"model":"gpt-image-1"}`),
		"gpt-image-1", time.Unix(1, 0),
		service.PerRequestPreauthorizationEstimate{RequestCount: 1, SizeTier: "1K"},
	)
	require.ErrorIs(t, err, service.ErrBalanceWithholdingFailed)
	require.True(t, infraerrors.IsForbidden(err), "withholding failure must map to HTTP 403")
}

// TestPreauthorizePerRequestSkipsWhenNotRequired proves subscription/simple
// mode short-circuits before pricing so those requests are never charged a hold.
func TestPreauthorizePerRequestSkipsWhenNotRequired(t *testing.T) {
	preauthorizer := &preauthorizerStub{requires: false}
	pricing := &pricingProviderStub{}

	guard, err := preauthorizePerRequestGatewayRequest(
		context.Background(), preauthorizer, pricing,
		perRequestTestAPIKey(), nil, []byte(`{"model":"gpt-image-1"}`),
		"gpt-image-1", time.Unix(1, 0),
		service.PerRequestPreauthorizationEstimate{RequestCount: 1, SizeTier: "1K"},
	)
	require.NoError(t, err)
	require.Nil(t, guard)
	require.True(t, preauthorizer.requiresCalled)
	require.Nil(t, preauthorizer.captured)
	require.Empty(t, pricing.lastModel)
}
