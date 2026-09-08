package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const maxAPIKeyGroupRoutes = 10

// UpstreamPlatformResolver maps a request model to the concrete account
// platform that should handle it. Used by gateway middleware before dispatch.
type UpstreamPlatformResolver interface {
	UpstreamPlatformForModel(ctx context.Context, apiKey *APIKey, model string) (string, bool)
}

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
	if model == "" {
		return !group.CustomModelsListEnabled()
	}
	if !group.CustomModelsListEnabled() {
		return true
	}
	return ModelsListAllows(group.ModelsListConfig, model)
}

func IsOpenAICompatibleUpstreamPlatform(platform string) bool {
	return isOpenAICompatibleUpstreamPlatform(platform)
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

func groupUsableForRequest(group *Group, requestPlatform, model string, schedulablePlatforms ...map[string]struct{}) bool {
	if group == nil {
		return true
	}
	if !groupAllowsRequestedModel(group, model) {
		return false
	}
	if groupPlatformFitsRequest(group.Platform, requestPlatform) {
		return true
	}
	var platforms map[string]struct{}
	if len(schedulablePlatforms) > 0 {
		platforms = schedulablePlatforms[0]
	}
	if len(platforms) == 0 {
		return false
	}
	if requestPlatform != "" {
		if _, ok := platforms[requestPlatform]; ok {
			return true
		}
	}
	for platform := range platforms {
		if groupPlatformFitsRequest(platform, requestPlatform) {
			return true
		}
		// OpenAI-type accounts in a Gemini/Claude-labeled group can serve
		// OpenAI-compatible clients and the Anthropic messages bridge.
		if isOpenAICompatibleUpstreamPlatform(platform) {
			return true
		}
	}
	return false
}

type groupCatalogModelPresence int

const (
	groupCatalogModelUnknown groupCatalogModelPresence = iota
	groupCatalogModelPresent
	groupCatalogModelAbsent
)

func groupCatalogPlatforms() []string {
	return []string{
		PlatformAnthropic, PlatformGemini, PlatformOpenAI, PlatformAntigravity,
		PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek,
	}
}

func modelsAdmitRequestedModel(models []string, requestedModel string) bool {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" || len(models) == 0 {
		return false
	}
	normalized := ""
	for _, id := range models {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if strings.EqualFold(id, requestedModel) {
			return true
		}
		if matchWildcard(id, requestedModel) {
			return true
		}
		if normalized == "" {
			normalized = normalizeRequestedModelForLookup("", requestedModel)
		}
		if normalized != "" && normalized != requestedModel {
			if strings.EqualFold(id, normalized) || matchWildcard(id, normalized) {
				return true
			}
		}
	}
	return false
}

type groupCatalogModelLookup func(ctx context.Context, groupID *int64, platform string) []string

func groupCatalogHasRequestedModelWith(ctx context.Context, groupID int64, requestedModel string, getModels groupCatalogModelLookup) groupCatalogModelPresence {
	if getModels == nil || groupID <= 0 {
		return groupCatalogModelUnknown
	}
	requestedModel = strings.TrimSpace(requestedModel)
	gid := groupID
	sawCatalog := false
	for _, platform := range groupCatalogPlatforms() {
		models := getModels(ctx, &gid, platform)
		if models == nil {
			continue
		}
		sawCatalog = true
		if modelsAdmitRequestedModel(models, requestedModel) {
			return groupCatalogModelPresent
		}
	}
	if !sawCatalog {
		return groupCatalogModelUnknown
	}
	return groupCatalogModelAbsent
}

func skipKeyRouteForCatalog(presence groupCatalogModelPresence, siblingHasPresent bool) bool {
	switch presence {
	case groupCatalogModelAbsent:
		return true
	case groupCatalogModelUnknown:
		return siblingHasPresent
	default:
		return false
	}
}

func keyRouteSiblingHasCatalogedModel(ctx context.Context, groupIDs []int64, requestedModel string, getModels groupCatalogModelLookup) bool {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" || getModels == nil {
		return false
	}
	for _, gid := range groupIDs {
		if groupCatalogHasRequestedModelWith(ctx, gid, requestedModel, getModels) == groupCatalogModelPresent {
			return true
		}
	}
	return false
}

func (s *GatewayService) groupCatalogHasRequestedModel(ctx context.Context, groupID int64, requestedModel string) groupCatalogModelPresence {
	if s == nil {
		return groupCatalogModelUnknown
	}
	return groupCatalogHasRequestedModelWith(ctx, groupID, requestedModel, s.GetAvailableModels)
}

func (s *GatewayService) shouldTryKeyRouteGroup(ctx context.Context, groupID int64, requestPlatform, requestedModel string, siblingHasPresent bool) bool {
	if !s.groupCatalogUsableForRequest(ctx, groupID, requestPlatform, requestedModel) {
		return false
	}
	group := s.GroupPolicyForRequest(ctx, groupID)
	if group != nil && group.CustomModelsListEnabled() {
		return true
	}
	return !skipKeyRouteForCatalog(s.groupCatalogHasRequestedModel(ctx, groupID, requestedModel), siblingHasPresent)
}

func (s *GatewayService) groupCatalogUsableForRequest(ctx context.Context, groupID int64, requestPlatform, requestedModel string) bool {
	if s == nil {
		return true
	}
	gid := groupID
	group := s.GroupPolicyForRequest(ctx, gid)
	if group == nil {
		return s.groupCatalogHasRequestedModel(ctx, groupID, requestedModel) != groupCatalogModelAbsent
	}
	if !groupUsableForRequest(group, requestPlatform, requestedModel, s.GetSchedulablePlatforms(ctx, &gid)) {
		return false
	}
	if group.CustomModelsListEnabled() {
		return true
	}
	return s.groupCatalogHasRequestedModel(ctx, groupID, requestedModel) != groupCatalogModelAbsent
}

// UpstreamPlatformForModel prefers the platform of a schedulable account that
// actually maps the requested model, not the group label and not the model-name
// heuristic. OpenAI-compatible accounts are checked first so a Gemini-labeled
// group holding OpenAI-type accounts (custom Responses URL) resolves to OpenAI.
func (s *GatewayService) UpstreamPlatformForModel(ctx context.Context, apiKey *APIKey, model string) (string, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", false
	}
	if s == nil || apiKey == nil {
		return "", false
	}
	ids := apiKey.CandidateGroupIDs()
	if len(ids) == 0 && apiKey.Group != nil && apiKey.Group.ID > 0 {
		ids = []int64{apiKey.Group.ID}
	}
	prefer := []string{
		PlatformOpenAI, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek,
		PlatformAnthropic, PlatformGemini, PlatformAntigravity,
	}
	sawCatalog := false
	for _, gid := range ids {
		gid := gid
		group := s.GroupPolicyForRequest(ctx, gid)
		if group == nil && apiKey.Group != nil && apiKey.Group.ID == gid {
			group = apiKey.Group
		}
		if !groupAllowsRequestedModel(group, model) {
			continue
		}
		for _, platform := range prefer {
			models := s.GetAvailableModels(ctx, &gid, platform)
			if models == nil {
				continue
			}
			sawCatalog = true
			if modelsAdmitRequestedModel(models, model) {
				return platform, true
			}
		}
	}
	if sawCatalog {
		return "", false
	}
	for _, gid := range ids {
		gid := gid
		for platform := range s.GetSchedulablePlatforms(ctx, &gid) {
			if isOpenAICompatibleUpstreamPlatform(platform) {
				return platform, true
			}
		}
	}
	return "", false
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
