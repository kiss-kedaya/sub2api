package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

func (s *GatewayService) hydrateAPIKeyGroup(ctx context.Context, apiKey *APIKey, groupID int64) (*APIKey, error) {
	if s != nil && s.schedulerSnapshot != nil {
		return hydrateAPIKeyGroup(ctx, apiKey, groupID, s.schedulerSnapshot.GetGroupByIDLite)
	}
	if s == nil || s.groupRepo == nil {
		return hydrateAPIKeyGroup(ctx, apiKey, groupID, nil)
	}
	return hydrateAPIKeyGroup(ctx, apiKey, groupID, func(ctx context.Context, id int64) (*Group, error) {
		if s == nil {
			return nil, ErrSchedulerCacheNotReady
		}
		group := s.GroupPolicyForRequest(ctx, id)
		if group == nil {
			return nil, ErrSchedulerCacheNotReady
		}
		return group, nil
	})
}

// resolveAPIKeyRouteCandidate applies the same group-level fallback that the
// official scheduler applies inside SelectAccountWithLoadAwareness. The
// returned key must carry the effective group so forwarding and billing use
// the group that actually supplied the account.
func (s *GatewayService) resolveAPIKeyRouteCandidate(ctx context.Context, key *APIKey) (*APIKey, error) {
	if s == nil || key == nil || key.GroupID == nil || key.Group == nil {
		return key, nil
	}
	if forcePlatform, ok := ctx.Value(ctxkey.ForcePlatform).(string); ok && forcePlatform != "" {
		return key, nil
	}
	group, resolvedID, err := s.resolveGatewayGroup(ctx, key.GroupID)
	if err != nil {
		return nil, err
	}
	if group == nil || resolvedID == nil || *resolvedID == *key.GroupID {
		return key, nil
	}
	return cloneAPIKeyWithGroupID(key, group), nil
}

// SelectAccountAlongKeyRoutes tries the key's ordered groups one by one.
// ErrNoAvailableAccounts and other per-group unavailability errors continue to
// the next group. Protocol-incompatible groups are skipped so an OpenAI account
// is never returned to an Anthropic/Gemini forwarder. Billing uses the hydrated
// group returned with a successful selection, not the primary group.
func (s *GatewayService) SelectAccountAlongKeyRoutes(
	ctx context.Context,
	apiKey *APIKey,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	metadataUserID string,
	sub2apiUserID int64,
	requestPlatform ...string,
) (*AccountSelectionResult, *APIKey, error) {
	if s == nil || apiKey == nil {
		return nil, apiKey, ErrNoAvailableAccounts
	}
	ctx = withSchedulerRequestMode(ctx, s.accountRepo, s.schedulerSnapshot)
	platform := ""
	if len(requestPlatform) > 0 {
		platform = requestPlatform[0]
	}
	groupIDs := apiKey.CandidateGroupIDs()
	if len(groupIDs) == 0 {
		result, err := s.SelectAccountWithLoadAwareness(ctx, apiKey.GroupID, sessionHash, requestedModel, excludedIDs, metadataUserID, sub2apiUserID)
		return result, apiKey, err
	}
	routes := newAPIKeyRouteIterator(ctx, apiKey, groupIDs, requestedModel, s.hydrateAPIKeyGroup, s.resolveAPIKeyRouteCandidate, s.ensureGroupModelsCatalog, s.ResolveChannelMappingAndRestrict, nil)
	var lastErr error
	for candidate, ok := routes.next(); ok; candidate, ok = routes.next() {
		routed := candidate.key
		if !groupUsableForRequest(routed.Group, platform, requestedModel, candidate.catalog.platforms) {
			continue
		}
		routeCtx := ContextWithAPIKeyRoute(ctx, routed)
		if _, tokenRequest := gatewayTokenRequestPricingAtFromContext(routeCtx); tokenRequest {
			// Internal composite routing may replace ctxkey.Group later; freeze
			// this candidate's billing parent while preserving the pricing instant.
			routeCtx = context.WithValue(routeCtx, gatewayTokenRequestBillingGroupCtxKey{}, routed.Group)
			routeCtx = s.withGatewayProfitControlGate(routeCtx, routed.GroupID)
		}
		result, err := s.SelectAccountWithLoadAwareness(routeCtx, routed.GroupID, sessionHash, candidate.model, excludedIDs, metadataUserID, sub2apiUserID)
		if err == nil {
			return result, routed, nil
		}
		lastErr = err
		if !shouldContinueAlongKeyRoutes(err) {
			return nil, apiKey, err
		}
	}
	if lastErr == nil {
		lastErr = routes.err
	}
	if lastErr == nil {
		lastErr = ErrNoAvailableAccounts
	}
	return nil, apiKey, lastErr
}
