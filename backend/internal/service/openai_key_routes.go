package service

import (
	"context"
)

func (s *OpenAIGatewayService) hydrateAPIKeyGroup(ctx context.Context, apiKey *APIKey, groupID int64) (*APIKey, error) {
	var getGroup func(context.Context, int64) (*Group, error)
	if s != nil && s.schedulerSnapshot != nil {
		getGroup = s.schedulerSnapshot.GetGroupByIDLite
	}
	return hydrateAPIKeyGroup(ctx, apiKey, groupID, getGroup)
}

func (s *OpenAIGatewayService) catalogModels(ctx context.Context, groupID *int64, platform string) []string {
	if s == nil {
		return nil
	}
	return loadAvailableModelsFromStore(ctx, s.accountRepo, s.schedulerSnapshot, groupID, platform)
}

func (s *OpenAIGatewayService) selectAlongKeyRoutes(
	ctx context.Context,
	apiKey *APIKey,
	platformOverride []string,
	requestedModel string,
	selectOne func(groupID *int64, groupPlatform []string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error),
) (*AccountSelectionResult, OpenAIAccountScheduleDecision, *APIKey, error) {
	candidates := apiKey.CandidateGroupIDs()
	if len(candidates) == 0 {
		selection, decision, err := selectOne(apiKey.GroupID, append([]string(nil), platformOverride...))
		return selection, decision, apiKey, err
	}
	siblingHasPresent := keyRouteSiblingHasCatalogedModel(ctx, candidates, requestedModel, s.catalogModels)
	var lastErr error
	var lastDecision OpenAIAccountScheduleDecision
	for _, groupID := range candidates {
		gid := groupID
		groupPlatform := append([]string(nil), platformOverride...)
		var group *Group
		if s != nil && s.schedulerSnapshot != nil {
			if found, err := s.schedulerSnapshot.GetGroupByIDLite(ctx, gid); err == nil && found != nil {
				group = found
				if !groupAllowsRequestedModel(group, requestedModel) {
					continue
				}
				if isOpenAICompatibleUpstreamPlatform(group.Platform) {
					groupPlatform = []string{group.Platform}
				}
			}
		}
		if group == nil || !group.CustomModelsListEnabled() {
			presence := groupCatalogHasRequestedModelWith(ctx, gid, requestedModel, s.catalogModels)
			if skipKeyRouteForCatalog(presence, siblingHasPresent) {
				continue
			}
		}
		selection, decision, err := selectOne(&gid, groupPlatform)
		if err == nil {
			routed, hydErr := s.hydrateAPIKeyGroup(ctx, apiKey, groupID)
			if hydErr != nil {
				releaseAccountSelection(selection)
				lastErr = hydErr
				lastDecision = decision
				if shouldContinueAlongKeyRoutes(hydErr) {
					continue
				}
				return nil, decision, apiKey, hydErr
			}
			return selection, decision, routed, nil
		}
		lastErr = err
		lastDecision = decision
		if !shouldContinueAlongKeyRoutes(err) {
			return nil, decision, apiKey, err
		}
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
	return s.selectAlongKeyRoutes(ctx, apiKey, platformOverride, requestedModel, func(groupID *int64, groupPlatform []string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		return s.SelectAccountWithSchedulerForCapability(
			ctx, groupID, previousResponseID, sessionHash, requestedModel, excludedIDs,
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
	return s.selectAlongKeyRoutes(ctx, apiKey, []string{PlatformOpenAI}, requestedModel, func(groupID *int64, _ []string) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		return s.SelectAccountWithSchedulerForImages(ctx, groupID, sessionHash, requestedModel, excludedIDs, requiredCapability)
	})
}
