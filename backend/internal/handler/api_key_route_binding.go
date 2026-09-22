package handler

import (
	"context"
	"time"

	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type keyRouteBillingProvider interface {
	balancePreauthorizationPricingProvider
	ResolveAPIKeyRouteSubscription(context.Context, *service.APIKey, *service.UserSubscription) (*service.UserSubscription, error)
	ResolveChannelMappingAndRestrict(context.Context, *int64, string) (service.ChannelMappingResult, bool)
	ReplaceModelInBody([]byte, string) []byte
	RebindKeyRouteProfitControl(context.Context, *int64) context.Context
}

type keyRouteEligibilityChecker interface {
	CheckAPIKeyRouteEligibility(context.Context, *service.User, *service.APIKey, *service.Group, *service.UserSubscription, string) error
}

type keyRouteBinding struct {
	Previous     *service.APIKey
	Selected     *service.APIKey
	Subscription **service.UserSubscription
	Mapping      *service.ChannelMappingResult
	Guard        **service.BalancePreauthorizationGuard
	Body         []byte
	Model        string
	PricingAt    time.Time
	PerRequest   *service.PerRequestPreauthorizationEstimate
}

const checkedKeyRouteGroupsKey = "gateway.checked_key_route_groups"

type selectedKeyRouteContextKey struct{}

func deferKeyRoutePreauthorization(ctx context.Context, key *service.APIKey) bool {
	if len(key.CandidateGroupIDs()) <= 1 {
		return false
	}
	if ctx == nil {
		return true
	}
	groupID, _ := ctx.Value(selectedKeyRouteContextKey{}).(int64)
	return key.GroupID == nil || groupID != *key.GroupID
}

func keyRouteGroupChanged(previous, selected *service.APIKey) bool {
	return previous != nil && selected != nil && previous.GroupID != nil && selected.GroupID != nil && *previous.GroupID != *selected.GroupID
}

func keyRouteRequiresPreauthorization(preauthorizer balancePreauthorizer, key *service.APIKey, subscription *service.UserSubscription) bool {
	if preauthorizer == nil {
		return false
	}
	if requirement, ok := preauthorizer.(balancePreauthorizationRequirement); ok {
		return requirement.RequiresPreauthorization(service.BalancePreauthorizationBillingType(key, subscription))
	}
	return true
}

func (h *OpenAIGatewayHandler) bindSelectedKeyRoute(c *gin.Context, binding keyRouteBinding) error {
	return bindSelectedKeyRoute(c, h.gatewayService, h.billingCacheService, h.balancePreauthorizer, binding)
}

func (h *GatewayHandler) bindSelectedKeyRoute(c *gin.Context, binding keyRouteBinding) error {
	return bindSelectedKeyRoute(c, h.gatewayService, h.billingCacheService, h.balancePreauthorizer, binding)
}

// Keep the selected billing identity, forwarding policy and hold in the same
// request context. Account retries inside a group do not repeat admission.
func bindSelectedKeyRoute(c *gin.Context, gateway keyRouteBillingProvider, billing keyRouteEligibilityChecker, preauthorizer balancePreauthorizer, binding keyRouteBinding) error {
	previous, selected := binding.Previous, binding.Selected
	if selected == nil || previous == nil || selected.GroupID == nil || previous.GroupID == nil {
		return nil
	}
	changed := keyRouteGroupChanged(previous, selected)
	if !changed && !deferKeyRoutePreauthorization(c.Request.Context(), selected) {
		return nil
	}
	ctx := service.ContextWithAPIKeyRoute(c.Request.Context(), selected)
	ctx = context.WithValue(ctx, selectedKeyRouteContextKey{}, *selected.GroupID)
	if changed {
		ctx = gateway.RebindKeyRouteProfitControl(ctx, selected.GroupID)
	}
	var currentSubscription *service.UserSubscription
	if binding.Subscription != nil {
		currentSubscription = *binding.Subscription
	}
	subscription, err := gateway.ResolveAPIKeyRouteSubscription(ctx, selected, currentSubscription)
	if err != nil {
		return err
	}
	checked, _ := c.Get(checkedKeyRouteGroupsKey)
	groups, _ := checked.(map[int64]struct{})
	if groups == nil {
		groups = map[int64]struct{}{*previous.GroupID: {}}
		c.Set(checkedKeyRouteGroupsKey, groups)
	}
	if _, ok := groups[*selected.GroupID]; !ok && billing != nil {
		if err := billing.CheckAPIKeyRouteEligibility(ctx, selected.User, selected, selected.Group, subscription, service.QuotaPlatform(ctx, selected)); err != nil {
			return err
		}
		groups[*selected.GroupID] = struct{}{}
	}
	var mapping service.ChannelMappingResult
	if binding.Mapping != nil {
		mapping, _ = gateway.ResolveChannelMappingAndRestrict(ctx, selected.GroupID, binding.Model)
	}
	if binding.Guard != nil && (*binding.Guard != nil || keyRouteRequiresPreauthorization(preauthorizer, selected, subscription)) {
		body := binding.Body
		if mapping.Mapped {
			body = gateway.ReplaceModelInBody(body, mapping.MappedModel)
		}
		model := service.BalancePreauthorizationBillingModel(binding.Model, mapping)
		tier := gjson.GetBytes(body, "service_tier").String()
		if selected.Group != nil && selected.Group.ForceOpenAIFast && selected.Group.Platform == service.PlatformOpenAI {
			tier = "priority"
		}
		if *binding.Guard == nil {
			var guard *service.BalancePreauthorizationGuard
			if binding.PerRequest == nil {
				guard, err = preauthorizeTextGatewayRequest(ctx, preauthorizer, gateway, selected, subscription, body, model, binding.PricingAt, tier)
			} else {
				guard, err = preauthorizePerRequestGatewayRequest(ctx, preauthorizer, gateway, selected, subscription, body, model, binding.PricingAt, *binding.PerRequest)
			}
			if err != nil {
				return err
			}
			*binding.Guard = guard
		} else {
			estimate := service.EstimateBalancePreauthorizationTokens(body)
			request := service.BalancePreauthorizationRequest{
				APIKeyID: selected.ID, UserID: selected.UserID,
				BillingType:        service.BalancePreauthorizationBillingType(selected, subscription),
				BillableInputBytes: len(body), EstimatedInputTokens: estimate.InputTokens,
				InitialOutputWindowTokens: estimate.OutputTokens,
				CostInput:                 gateway.BalancePreauthorizationCostInput(ctx, selected, model, binding.PricingAt, tier),
			}
			if request.UserID <= 0 && selected.User != nil {
				request.UserID = selected.User.ID
			}
			if binding.PerRequest != nil {
				request.EstimateKind = service.PreauthorizationEstimatePerRequest
				request.PerRequestEstimate = *binding.PerRequest
			}
			if err := (*binding.Guard).RepriceForRoute(ctx, request); err != nil {
				return err
			}
		}
		if *binding.Guard != nil {
			ctx = service.ContextWithBalancePreauthorizationGuard(ctx, *binding.Guard)
		}
	}
	if binding.Mapping != nil {
		*binding.Mapping = mapping
	}
	if binding.Subscription != nil {
		*binding.Subscription = subscription
	}
	c.Set(string(middleware.ContextKeyAPIKey), selected)
	c.Set(string(middleware.ContextKeySubscription), subscription)
	c.Request = c.Request.WithContext(ctx)
	return nil
}

func releaseRejectedKeyRouteSelection(selection *service.AccountSelectionResult) {
	if selection != nil && selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}
