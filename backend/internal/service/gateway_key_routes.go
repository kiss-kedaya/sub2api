package service

import (
	"context"
)

func (s *GatewayService) hydrateAPIKeyGroup(ctx context.Context, apiKey *APIKey, groupID int64) (*APIKey, error) {
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
	platform := ""
	if len(requestPlatform) > 0 {
		platform = requestPlatform[0]
	}
	candidates := apiKey.CandidateGroupIDs()
	if len(candidates) == 0 {
		result, err := s.SelectAccountWithLoadAwareness(ctx, apiKey.GroupID, sessionHash, requestedModel, excludedIDs, metadataUserID, sub2apiUserID)
		return result, apiKey, err
	}
	var lastErr error
	for _, groupID := range candidates {
		gid := groupID
		if !s.groupCatalogUsableForRequest(ctx, gid, platform, requestedModel) {
			continue
		}
		result, err := s.SelectAccountWithLoadAwareness(ctx, &gid, sessionHash, requestedModel, excludedIDs, metadataUserID, sub2apiUserID)
		if err == nil {
			routed, hydErr := s.hydrateAPIKeyGroup(ctx, apiKey, groupID)
			if hydErr != nil {
				releaseAccountSelection(result)
				lastErr = hydErr
				if shouldContinueAlongKeyRoutes(hydErr) {
					continue
				}
				return nil, apiKey, hydErr
			}
			return result, routed, nil
		}
		lastErr = err
		if !shouldContinueAlongKeyRoutes(err) {
			return nil, apiKey, err
		}
	}
	if lastErr == nil {
		lastErr = ErrNoAvailableAccounts
	}
	return nil, apiKey, lastErr
}
