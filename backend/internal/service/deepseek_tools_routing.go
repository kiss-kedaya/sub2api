package service

import (
	"context"
	"sort"
	"strings"
)

// PrepareDeepSeekToolsRouting narrows the authenticated key's own route groups
// before the shared scheduler can infer a different platform from a model name.
// Standalone Search/Fetch have no model by default; use the published account
// catalog rather than a model ID that may no longer exist in the group's pool.
func (s *OpenAIGatewayService) PrepareDeepSeekToolsRouting(ctx context.Context, key *APIKey, requestedModel string) (*APIKey, []string, error) {
	if s == nil || key == nil || s.schedulerSnapshot == nil {
		return nil, nil, ErrNoAvailableAccounts
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// Even when the emergency freshness mode is enabled, preparation is a
	// read-only cache operation. A cold catalog never authorizes a DB scan.
	// Set the marker directly: withSchedulerSnapshotOnly intentionally keeps
	// an existing emergency freshness scope, which is inappropriate here.
	ctx = context.WithValue(ctx, schedulerSnapshotOnlyContextKey{}, true)
	groupIDs := key.CandidateGroupIDs()
	allowedIDs := make([]int64, 0, len(groupIDs))
	groups := make([]*Group, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		routed, err := s.hydrateAPIKeyGroup(ctx, key, groupID)
		if err != nil || !apiKeyRouteGroupAllowed(routed) || routed.Group.Platform != PlatformDeepseek {
			continue
		}
		allowedIDs = append(allowedIDs, groupID)
		groups = append(groups, routed.Group)
	}
	if len(allowedIDs) == 0 {
		return nil, nil, ErrNoAvailableAccounts
	}
	prepared := cloneAPIKeyWithGroupID(key, groups[0])
	prepared.RouteGroupIDs = allowedIDs
	if requestedModel = strings.TrimSpace(requestedModel); requestedModel != "" {
		// The scheduler still enforces each selected group's model allowlist.
		return prepared, []string{requestedModel}, nil
	}
	seen := make(map[string]struct{})
	var models []string
	for i, group := range groups {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		candidates := append([]string(nil), s.catalogModels(ctx, &allowedIDs[i], PlatformDeepseek)...)
		sort.SliceStable(candidates, func(i, j int) bool {
			a, b := strings.TrimSpace(candidates[i]), strings.TrimSpace(candidates[j])
			aFlash, bFlash := strings.Contains(strings.ToLower(a), "flash"), strings.Contains(strings.ToLower(b), "flash")
			if aFlash != bFlash {
				return aFlash
			}
			return a < b
		})
		for _, model := range candidates {
			model = strings.TrimSpace(model)
			if model == "" || !groupAllowsRequestedModel(group, model) {
				continue
			}
			if _, exists := seen[model]; exists {
				continue
			}
			seen[model] = struct{}{}
			models = append(models, model)
		}
	}
	if len(models) == 0 {
		return nil, nil, ErrNoAvailableAccounts
	}
	return prepared, models, nil
}
