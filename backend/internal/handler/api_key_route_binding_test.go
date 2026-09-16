package handler

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/sjson"
)

type routeBindingGatewayStub struct {
	subscription *service.UserSubscription
	err          error
	mapping      service.ChannelMappingResult
	pricedGroup  int64
	pricedTier   string
	bodyCopies   int
}

func (s *routeBindingGatewayStub) ResolveAPIKeyRouteSubscription(_ context.Context, _ *service.APIKey, _ *service.UserSubscription) (*service.UserSubscription, error) {
	return s.subscription, s.err
}

func (s *routeBindingGatewayStub) ResolveChannelMappingAndRestrict(context.Context, *int64, string) (service.ChannelMappingResult, bool) {
	return s.mapping, false
}

func (s *routeBindingGatewayStub) ReplaceModelInBody(body []byte, model string) []byte {
	s.bodyCopies++
	result, _ := sjson.SetBytes(body, "model", model)
	return result
}

func (s *routeBindingGatewayStub) BalancePreauthorizationCostInput(ctx context.Context, key *service.APIKey, model string, at time.Time, tier string) service.CostInput {
	s.pricedGroup, s.pricedTier = *key.GroupID, tier
	return service.CostInput{Ctx: ctx, Model: model, Group: key.Group, GroupID: key.GroupID, RateMultiplier: key.Group.RateMultiplier, PricingAt: at, ServiceTier: tier}
}

type routeBindingEligibilityStub struct {
	groups []int64
	err    error
}

func (s *routeBindingEligibilityStub) CheckAPIKeyRouteEligibility(ctx context.Context, _ *service.User, key *service.APIKey, _ *service.Group, _ *service.UserSubscription, _ string) error {
	group, ok := ctx.Value(ctxkey.Group).(*service.Group)
	if !ok || group.ID != *key.GroupID {
		return errors.New("billing context still belongs to another group")
	}
	s.groups = append(s.groups, *key.GroupID)
	return s.err
}

func routeBindingFixture() (*gin.Context, *service.APIKey, *service.APIKey) {
	a := &service.Group{ID: 9, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, RateMultiplier: 1}
	b := &service.Group{ID: 17, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, RateMultiplier: 2}
	user := &service.User{ID: 42}
	key := &service.APIKey{ID: 7, UserID: user.ID, User: user, GroupID: &a.ID, Group: a, RouteGroupIDs: []int64{a.ID, b.ID}}
	routed := *key
	routed.Group, routed.GroupID = b, &b.ID
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx := context.WithValue(context.Background(), ctxkey.Group, a)
	ctx = context.WithValue(ctx, ctxkey.RequestID, "route-binding-request")
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(ctx)
	c.Set(string(middleware.ContextKeyAPIKey), key)
	return c, key, &routed
}

func TestKeyRouteBindingUsesSelectedPricingAndContext(t *testing.T) {
	c, key, selected := routeBindingFixture()
	gateway := &routeBindingGatewayStub{mapping: service.ChannelMappingResult{Mapped: true, MappedModel: "gpt-5.1", BillingModelSource: service.BillingModelSourceChannelMapped}}
	eligibility := &routeBindingEligibilityStub{}
	preauthorizer := &preauthorizerStub{requires: true}
	mapping := service.ChannelMappingResult{Mapped: true, MappedModel: "primary-private-model"}
	subscription := &service.UserSubscription{ID: 1, UserID: 42, GroupID: 9}
	var guard *service.BalancePreauthorizationGuard
	at := time.Unix(1000, 0)
	selected.Group.ForceOpenAIFast = true
	err := bindSelectedKeyRoute(c, gateway, eligibility, preauthorizer, keyRouteBinding{
		Previous: key, Selected: selected, Subscription: &subscription, Mapping: &mapping,
		Guard: &guard, Body: []byte(`{"model":"gpt-5.1","input":"hello"}`), Model: "gpt-5.1", PricingAt: at,
	})
	require.NoError(t, err)
	require.Nil(t, subscription)
	require.Equal(t, gateway.mapping, mapping)
	group, ok := c.Request.Context().Value(ctxkey.Group).(*service.Group)
	require.True(t, ok)
	require.Equal(t, selected.Group.ID, group.ID)
	require.Equal(t, "route-binding-request", c.Request.Context().Value(ctxkey.RequestID))
	bound, ok := middleware.GetAPIKeyFromContext(c)
	require.True(t, ok)
	require.Same(t, selected, bound)
	require.NotNil(t, preauthorizer.captured)
	require.Equal(t, selected.Group.ID, gateway.pricedGroup)
	require.Equal(t, "priority", gateway.pricedTier)
	require.Equal(t, at, preauthorizer.captured.CostInput.PricingAt)
	require.Equal(t, 2.0, preauthorizer.captured.CostInput.RateMultiplier)
	require.Equal(t, []int64{17}, eligibility.groups)
	require.Equal(t, int64(9), key.Group.ID, "cached auth key must remain immutable")
}

func TestKeyRouteBindingDefersOnlySmartKeyHoldUntilSelection(t *testing.T) {
	for _, perRequest := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "images"}[perRequest], func(t *testing.T) {
			c, key, _ := routeBindingFixture()
			gateway := &routeBindingGatewayStub{}
			preauthorizer := &preauthorizerStub{requires: true}
			body := []byte(`{"model":"gpt-5.1","input":"hello"}`)
			at := time.Unix(1000, 0)
			var err error
			if perRequest {
				_, err = preauthorizePerRequestGatewayRequest(c.Request.Context(), preauthorizer, gateway, key, nil, body, "gpt-5.1", at, service.PerRequestPreauthorizationEstimate{RequestCount: 2})
			} else {
				_, err = preauthorizeTextGatewayRequest(c.Request.Context(), preauthorizer, gateway, key, nil, body, "gpt-5.1", at, "")
			}
			require.NoError(t, err)
			require.Nil(t, preauthorizer.captured, "an unselected primary must not reserve another group's price")
			var guard *service.BalancePreauthorizationGuard
			binding := keyRouteBinding{Previous: key, Selected: key, Guard: &guard, Body: body, Model: "gpt-5.1", PricingAt: at}
			if perRequest {
				binding.PerRequest = &service.PerRequestPreauthorizationEstimate{RequestCount: 2}
			}
			require.NoError(t, bindSelectedKeyRoute(c, gateway, nil, preauthorizer, binding))
			require.NotNil(t, preauthorizer.captured, "even primary selection must reserve before forwarding")
			if perRequest {
				require.Equal(t, service.PreauthorizationEstimatePerRequest, preauthorizer.captured.EstimateKind)
				require.Equal(t, 2, preauthorizer.captured.PerRequestEstimate.RequestCount)
			}
			preauthorizer.captured = nil
			require.NoError(t, bindSelectedKeyRoute(c, gateway, nil, preauthorizer, binding))
			require.Nil(t, preauthorizer.captured, "same-group retries must reuse admission")
		})
	}
}

func TestKeyRouteBindingFailureDoesNotPublishSelectedIdentity(t *testing.T) {
	for _, stage := range []string{"subscription", "eligibility", "hold"} {
		t.Run(stage, func(t *testing.T) {
			c, key, selected := routeBindingFixture()
			failure := errors.New("route admission failed")
			gateway := &routeBindingGatewayStub{}
			eligibility := &routeBindingEligibilityStub{}
			preauthorizer := &preauthorizerStub{requires: true}
			switch stage {
			case "subscription":
				gateway.err = failure
			case "eligibility":
				eligibility.err = failure
			case "hold":
				preauthorizer.err = failure
			}
			var guard *service.BalancePreauthorizationGuard
			err := bindSelectedKeyRoute(c, gateway, eligibility, preauthorizer, keyRouteBinding{Previous: key, Selected: selected, Guard: &guard, Model: "gpt-5.1"})
			require.ErrorIs(t, err, failure)
			group, ok := c.Request.Context().Value(ctxkey.Group).(*service.Group)
			require.True(t, ok)
			require.Equal(t, key.Group.ID, group.ID)
			bound, _ := middleware.GetAPIKeyFromContext(c)
			require.Same(t, key, bound)
			require.Nil(t, guard)
		})
	}
}

func TestKeyRouteBindingChecksEachGroupOnlyOnce(t *testing.T) {
	c, primary, backup := routeBindingFixture()
	eligibility := &routeBindingEligibilityStub{}
	gateway := &routeBindingGatewayStub{}
	for _, pair := range [][2]*service.APIKey{{primary, backup}, {backup, primary}, {primary, backup}} {
		require.NoError(t, bindSelectedKeyRoute(c, gateway, eligibility, nil, keyRouteBinding{Previous: pair[0], Selected: pair[1]}))
	}
	require.Equal(t, []int64{17}, eligibility.groups, "global and previously checked group RPM must not be consumed again")
}

func TestKeyRouteBindingDisabledPreauthorizationDoesNotCopyBody(t *testing.T) {
	c, primary, backup := routeBindingFixture()
	gateway := &routeBindingGatewayStub{mapping: service.ChannelMappingResult{Mapped: true, MappedModel: "backup-private"}}
	preauthorizer := &preauthorizerStub{requires: false}
	var mapping service.ChannelMappingResult
	var guard *service.BalancePreauthorizationGuard
	require.NoError(t, bindSelectedKeyRoute(c, gateway, nil, preauthorizer, keyRouteBinding{
		Previous: primary, Selected: backup, Mapping: &mapping, Guard: &guard,
		Body: []byte(`{"model":"gpt-5.1","input":"hello"}`), Model: "gpt-5.1",
	}))
	require.Equal(t, "backup-private", mapping.MappedModel)
	require.Zero(t, gateway.bodyCopies)
	require.Nil(t, preauthorizer.captured)
}
