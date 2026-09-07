package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const maxAPIKeyGroupRoutes = 10

func (k *APIKey) CandidateGroupIDs() []int64 {
	if k == nil {
		return nil
	}
	if len(k.RouteGroupIDs) > 0 {
		out := make([]int64, 0, len(k.RouteGroupIDs))
		seen := make(map[int64]struct{}, len(k.RouteGroupIDs))
		for _, id := range k.RouteGroupIDs {
			if id <= 0 {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
		if len(out) > 0 {
			return out
		}
	}
	if k.GroupID != nil && *k.GroupID > 0 {
		return []int64{*k.GroupID}
	}
	return nil
}

// UsesRequestTargetPlatform reports whether this key should dispatch and
// schedule from the requested model rather than the primary group's platform.
// Composite groups already do this; smart-routing keys with more than one
// bound group need the same treatment so a later OpenAI group is reachable
// when the primary group is Claude (and the reverse).
func (k *APIKey) UsesRequestTargetPlatform() bool {
	if k == nil {
		return false
	}
	if k.Group != nil && k.Group.Platform == PlatformComposite {
		return true
	}
	return len(k.CandidateGroupIDs()) > 1
}

func groupAllowsRequestedModel(group *Group, model string) bool {
	if group == nil {
		return true
	}
	model = strings.TrimSpace(model)
	if model == "" || !group.CustomModelsListEnabled() {
		return true
	}
	for _, allowed := range group.ModelsListConfig.Models {
		if strings.EqualFold(strings.TrimSpace(allowed), model) {
			return true
		}
	}
	return false
}

func isOpenAICompatibleUpstreamPlatform(platform string) bool {
	switch strings.TrimSpace(platform) {
	case PlatformOpenAI, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek:
		return true
	default:
		return false
	}
}

func groupPlatformFitsRequest(groupPlatform, requestPlatform string) bool {
	groupPlatform = strings.TrimSpace(groupPlatform)
	requestPlatform = strings.TrimSpace(requestPlatform)
	if groupPlatform == "" || requestPlatform == "" {
		return true
	}
	if groupPlatform == requestPlatform || groupPlatform == PlatformComposite {
		return true
	}
	// Antigravity groups can serve Claude-compatible and Gemini traffic.
	if groupPlatform == PlatformAntigravity && (requestPlatform == PlatformAnthropic || requestPlatform == PlatformGemini) {
		return true
	}
	return false
}

func groupUsableForRequest(group *Group, requestPlatform, model string) bool {
	if group == nil {
		return true
	}
	if !groupPlatformFitsRequest(group.Platform, requestPlatform) {
		return false
	}
	return groupAllowsRequestedModel(group, model)
}

func shouldContinueAlongKeyRoutes(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrNoAvailableAccounts) ||
		errors.Is(err, ErrSchedulerCacheNotReady) ||
		errors.Is(err, ErrClaudeCodeOnly) ||
		errors.Is(err, ErrNoAvailableCompactAccounts) ||
		errors.Is(err, ErrGroupNotFound)
}

func releaseAccountSelection(result *AccountSelectionResult) {
	if result != nil && result.ReleaseFunc != nil {
		result.ReleaseFunc()
	}
}

func hydrateAPIKeyGroup(ctx context.Context, apiKey *APIKey, groupID int64, getGroup func(context.Context, int64) (*Group, error)) (*APIKey, error) {
	if apiKey == nil {
		return nil, ErrNoAvailableAccounts
	}
	if apiKey.GroupID != nil && *apiKey.GroupID == groupID && apiKey.Group != nil && apiKey.Group.ID == groupID {
		return apiKey, nil
	}
	if getGroup != nil {
		group, err := getGroup(ctx, groupID)
		if err != nil {
			return nil, err
		}
		if group != nil {
			return cloneAPIKeyWithGroupID(apiKey, group), nil
		}
	}
	return nil, ErrSchedulerCacheNotReady
}

func normalizeAPIKeyGroupIDs(groupID *int64, groupIDs []int64) ([]int64, *int64, error) {
	cleaned := make([]int64, 0, len(groupIDs))
	seen := make(map[int64]struct{}, len(groupIDs))
	for _, id := range groupIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			return nil, nil, infraerrors.BadRequest("API_KEY_GROUP_IDS_DUPLICATE", "group_ids must not contain duplicates")
		}
		seen[id] = struct{}{}
		cleaned = append(cleaned, id)
	}
	if len(cleaned) == 0 {
		if groupID != nil && *groupID > 0 {
			return []int64{*groupID}, groupID, nil
		}
		return nil, groupID, nil
	}
	if len(cleaned) > maxAPIKeyGroupRoutes {
		return nil, nil, infraerrors.BadRequest("API_KEY_GROUP_IDS_TOO_MANY", fmt.Sprintf("group_ids supports at most %d groups", maxAPIKeyGroupRoutes))
	}
	if groupID != nil && *groupID > 0 && *groupID != cleaned[0] {
		return nil, nil, infraerrors.BadRequest("API_KEY_GROUP_ID_MISMATCH", "group_id must match the first group_ids entry")
	}
	first := cleaned[0]
	return cleaned, &first, nil
}

func cloneAPIKeyWithGroupID(apiKey *APIKey, group *Group) *APIKey {
	if apiKey == nil {
		return nil
	}
	cloned := *apiKey
	if group != nil {
		id := group.ID
		cloned.GroupID = &id
		cloned.Group = group
	}
	return &cloned
}

func persistedRouteGroupIDs(routeIDs []int64) []int64 {
	if len(routeIDs) <= 1 {
		return nil
	}
	out := make([]int64, len(routeIDs))
	copy(out, routeIDs)
	return out
}

func (s *APIKeyService) validateAPIKeyGroupRoutes(ctx context.Context, user *User, groupID *int64, groupIDs []int64) ([]int64, *int64, error) {
	routeIDs, primary, err := normalizeAPIKeyGroupIDs(groupID, groupIDs)
	if err != nil {
		return nil, nil, err
	}
	if len(routeIDs) == 0 {
		return nil, primary, nil
	}
	for _, id := range routeIDs {
		group, err := s.groupRepo.GetByID(ctx, id)
		if err != nil {
			return nil, nil, fmt.Errorf("get group: %w", err)
		}
		if !s.canUserBindGroup(ctx, user, group) {
			return nil, nil, ErrGroupNotAllowed
		}
	}
	return routeIDs, primary, nil
}

func (s *APIKeyService) replaceAPIKeyGroupRoutes(ctx context.Context, apiKeyID int64, routeIDs []int64) error {
	store, ok := s.apiKeyRepo.(apiKeyGroupRouteStore)
	if !ok {
		return nil
	}
	persist := routeIDs
	if len(persist) <= 1 {
		persist = nil
	}
	if err := store.ReplaceGroupRoutes(ctx, apiKeyID, persist); err != nil {
		return fmt.Errorf("replace api key group routes: %w", err)
	}
	return nil
}

func (s *APIKeyService) attachAPIKeyGroupRoutes(ctx context.Context, keys []APIKey) error {
	store, ok := s.apiKeyRepo.(apiKeyGroupRouteStore)
	if !ok || len(keys) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(keys))
	for i := range keys {
		ids = append(ids, keys[i].ID)
	}
	routes, err := store.ListGroupRoutes(ctx, ids)
	if err != nil {
		return err
	}
	for i := range keys {
		keys[i].RouteGroupIDs = routes[keys[i].ID]
	}
	return nil
}
