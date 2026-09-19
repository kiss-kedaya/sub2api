package service

import (
	"context"
	"errors"
	"strings"
	"time"
)

type openAIMessagesKeyRouteContextKey struct{}

// WithOpenAIMessagesKeyRoute enables Messages-only model aliases during key
// routing. Chat Completions shares the capability but must not use these aliases.
func WithOpenAIMessagesKeyRoute(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAIMessagesKeyRouteContextKey{}, true)
}

func openAIMessagesKeyRouteGroupAllowed(ctx context.Context, group *Group) bool {
	if ctx == nil {
		return true
	}
	if enabled, _ := ctx.Value(openAIMessagesKeyRouteContextKey{}).(bool); !enabled {
		return true
	}
	if group == nil {
		return false
	}
	if group.Platform == PlatformGrok || IsCNProvider(group.Platform) {
		return true
	}
	if platform, ok := ResolvedTargetPlatformFromContext(ctx); ok && (platform == PlatformGrok || IsCNProvider(platform)) {
		return true
	}
	// Initial handler dispatch can defer this check to a later smart route.
	// A concrete candidate must enforce its own switch before catalog or sticky use.
	return group.AllowMessagesDispatch
}

func openAIMessagesKeyRouteModel(ctx context.Context, group *Group, model string) string {
	if enabled, _ := ctx.Value(openAIMessagesKeyRouteContextKey{}).(bool); !enabled {
		return model
	}
	normalized := NormalizeOpenAICompatRequestedModel(model)
	if group == nil {
		return normalized
	}
	if group.Platform == PlatformComposite {
		if platform, ok := ResolvedTargetPlatformFromContext(ctx); ok && (platform == PlatformGrok || IsCNProvider(platform)) {
			return normalized
		}
	}
	if mapped := strings.TrimSpace(group.ResolveMessagesDispatchModel(model)); mapped != "" {
		return mapped
	}
	return normalized
}

func (s *OpenAIGatewayService) hydrateAPIKeyGroup(ctx context.Context, apiKey *APIKey, groupID int64) (*APIKey, error) {
	var getGroup func(context.Context, int64) (*Group, error)
	if s != nil && s.schedulerSnapshot != nil {
		getGroup = s.schedulerSnapshot.GetGroupByIDLite
	}
	routed, err := hydrateAPIKeyGroup(ctx, apiKey, groupID, getGroup)
	if err != nil {
		return nil, err
	}
	if !openAIMessagesKeyRouteGroupAllowed(ctx, routed.Group) {
		return nil, ErrNoAvailableAccounts
	}
	return routed, nil
}

// ResolveAPIKeyRouteGroup restores billing from a server-owned async task.
// groupID must come from the authenticated owner's task, never client input.
func (s *OpenAIGatewayService) ResolveAPIKeyRouteGroup(ctx context.Context, apiKey *APIKey, groupID int64) (*APIKey, error) {
	if apiKey == nil || groupID <= 0 {
		return nil, ErrNoAvailableAccounts
	}
	if apiKey.GroupID != nil && *apiKey.GroupID == groupID && apiKey.Group != nil && apiKey.Group.ID == groupID {
		return apiKey, nil
	}
	routed, err := s.hydrateAPIKeyGroup(ctx, apiKey, groupID)
	if err == nil || !errors.Is(err, ErrSchedulerCacheNotReady) || s == nil || s.channelService == nil || s.channelService.groupRepo == nil {
		return routed, err
	}
	// Only async settlement may make this bounded cold-cache lookup. Ordinary
	// account selection keeps its snapshot-only contract.
	lookupCtx, cancel := context.WithTimeout(nonNilContext(ctx), 2*time.Second)
	defer cancel()
	return hydrateAPIKeyGroup(lookupCtx, apiKey, groupID, s.channelService.groupRepo.GetByIDLite)
}

func (s *OpenAIGatewayService) catalogModels(ctx context.Context, groupID *int64, platform string) []string {
	if s == nil {
		return nil
	}
	return s.routeModelsCatalog(ctx, groupID).modelsFor(platform)
}

func (s *OpenAIGatewayService) routeModelsCatalog(ctx context.Context, groupID *int64) groupModelsCatalog {
	// Reuse the shared catalog builder. Along-route preparation calls it once
	// per authorized group, without adding a second long-lived cache.
	if s == nil {
		return groupModelsCatalog{}
	}
	return loadGroupModelsCatalogFromStore(ctx, s.accountRepo, s.schedulerSnapshot, groupID)
}

func (s *OpenAIGatewayService) selectAlongKeyRoutes(
	ctx context.Context,
	apiKey *APIKey,
	platformOverride []string,
	requestedModel string,
	selectOne func(ctx context.Context, groupID *int64, groupPlatform []string, model string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error),
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, *APIKey, error) {
	if s == nil || apiKey == nil {
		return nil, OpenAIAccountScheduleDecision{}, apiKey, ErrNoAvailableAccounts
	}
	ctx = withSchedulerRequestMode(ctx, s.accountRepo, s.schedulerSnapshot)
	groupIDs := apiKey.CandidateGroupIDs()
	if len(groupIDs) == 0 {
		selection, decision, err := selectOne(ctx, apiKey.GroupID, append([]string(nil), platformOverride...), requestedModel)
		return selection, decision, apiKey, err
	}
	routes := newAPIKeyRouteIterator(ctx, apiKey, groupIDs, requestedModel, s.hydrateAPIKeyGroup, s.routeModelsCatalog, s.ResolveChannelMappingAndRestrict, openAIMessagesKeyRouteModel)
	var lastErr error
	var lastDecision OpenAIAccountScheduleDecision
	for candidate, ok := routes.next(); ok; candidate, ok = routes.next() {
		routed := candidate.key
		group := routed.Group
		groupPlatform := append([]string(nil), platformOverride...)
		if isOpenAICompatibleUpstreamPlatform(group.Platform) {
			groupPlatform = []string{group.Platform}
		}
		routeCtx := ContextWithAPIKeyRoute(ctx, routed)
		selection, decision, err := selectOne(routeCtx, routed.GroupID, groupPlatform, candidate.model)
		if err == nil {
			return selection, decision, routed, nil
		}
		lastErr = err
		lastDecision = decision
		if !shouldContinueAlongKeyRoutes(err) {
			return nil, decision, apiKey, err
		}
	}
	if lastErr == nil {
		lastErr = routes.err
	}
	if lastErr == nil {
		lastErr = ErrNoAvailableAccounts
	}
	return nil, lastDecision, apiKey, lastErr
}

func (s *OpenAIGatewayService) SelectAccountWithSchedulerForCapabilityAlongKeyRoutes(
	ctx context.Context,
	apiKey *APIKey,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredTransport OpenAIUpstreamTransport,
	requiredCapability OpenAIEndpointCapability,
	requireCompact bool,
	previousResponseCanMove bool,
	useUpstreamTokenCost bool,
	platformOverride ...string,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, *APIKey, error) {
	return s.selectAlongKeyRoutes(ctx, apiKey, platformOverride, requestedModel, func(ctx context.Context, groupID *int64, groupPlatform []string, model string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		return s.SelectAccountWithSchedulerForCapability(
			ctx, groupID, previousResponseID, sessionHash, model, excludedIDs,
			requiredTransport, requiredCapability, requireCompact, previousResponseCanMove, useUpstreamTokenCost, groupPlatform...,
		)
	})
}

func (s *OpenAIGatewayService) SelectAccountWithSchedulerForImagesAlongKeyRoutes(
	ctx context.Context,
	apiKey *APIKey,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredCapability OpenAIImagesCapability,
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, *APIKey, error) {
	return s.selectAlongKeyRoutes(ctx, apiKey, []string{PlatformOpenAI}, requestedModel, func(ctx context.Context, groupID *int64, _ []string, model string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		return s.SelectAccountWithSchedulerForImages(ctx, groupID, sessionHash, model, excludedIDs, requiredCapability)
	})
}
