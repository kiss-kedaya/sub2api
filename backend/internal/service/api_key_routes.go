package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

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

// API Key 分组路由模式。管理端列表用它把「单分组」和「智能路由」两种绑定
// 显式区分开，前端不再自行推导（前端只能看到 group_ids，旧服务可能不返回）。
const (
	// APIKeyRouteModeNone 表示这把 Key 没有绑定任何分组。
	APIKeyRouteModeNone = "none"
	// APIKeyRouteModeSingle 表示只有一个候选分组（普通单分组 Key）。
	APIKeyRouteModeSingle = "single"
	// APIKeyRouteModeSmart 表示按顺序回退的智能路由 Key（候选分组 > 1）。
	APIKeyRouteModeSmart = "smart"
)

// RouteGroupCount 返回候选分组数量（去重后）。
// 判定复用 CandidateGroupIDs：智能路由候选列表优先，为空时回退到 GroupID，
// 与网关选路使用完全相同的口径，避免管理端和实际路由出现两套判定。
func (k *APIKey) RouteGroupCount() int {
	return len(k.CandidateGroupIDs())
}

// IsSmartRouting 报告这把 Key 是否为智能路由（候选分组多于一个）。
func (k *APIKey) IsSmartRouting() bool {
	return k.RouteGroupCount() > 1
}

// RouteMode 返回管理端展示用的路由模式：none / single / smart。
func (k *APIKey) RouteMode() string {
	switch count := k.RouteGroupCount(); {
	case count <= 0:
		return APIKeyRouteModeNone
	case count == 1:
		return APIKeyRouteModeSingle
	default:
		return APIKeyRouteModeSmart
	}
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
	return ModelsListAllows(group.ModelAllowlist, model)
}

func IsOpenAICompatibleUpstreamPlatform(platform string) bool {
	return isOpenAICompatibleUpstreamPlatform(platform)
}

// isOpenAICompatibleUpstreamPlatform 委托给 openai_compatible_upstream_platforms.go
// 的唯一名单，避免路由/调度/准入各维护一份平台 switch 而漂移。
func isOpenAICompatibleUpstreamPlatform(p string) bool {
	return openAICompatibleUpstreamPlatform(p)
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
			// Claude 模型例外：grok/CN 供应商等非 Anthropic 上游账号默认不能
			// 服务 claude-*（否则 /v1/messages 会把 claude 请求转成 grok 上游，
			// 与 /v1/chat/completions 的 404 判定不一致）。仅当分组显式开启
			// 跨协议转换时才继续可用，逐账号准入由调度层的 Claude 闸门负责。
			if !upstreamPlatformServesClaudeFamily(platform) &&
				!crossProtocolAllowsClaudeOnUpstream(group) &&
				isClaudeModel(model) {
				continue
			}
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

func GroupCatalogPlatforms() []string {
	return groupCatalogPlatforms()
}

func groupCatalogPlatforms() []string {
	return []string{
		PlatformAnthropic, PlatformGemini, PlatformOpenAI, PlatformAntigravity,
		PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax,
		PlatformCline, PlatformCommandCode, PlatformTypeSafe,
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

// groupPassthroughsRequestedModel reports whether a group may still serve a
// model that its account-mapping catalog does not list. Official routing
// passthroughs unmapped IDs onto accounts of the matching platform; the catalog
// is not a closed allowlist unless the group enabled one.
func groupPassthroughsRequestedModel(group *Group, requestedModel string, schedulable map[string]struct{}) bool {
	if group != nil && group.CustomModelsListEnabled() {
		return false
	}
	detected, ok := DetectModelPlatform(requestedModel)
	if !ok {
		return false
	}
	if group != nil && (group.Platform == detected || group.Platform == PlatformComposite) {
		return true
	}
	if isOpenAICompatibleUpstreamPlatform(detected) &&
		!upstreamPlatformServesClaudeFamily(detected) &&
		!crossProtocolAllowsClaudeOnUpstream(group) &&
		isClaudeModel(requestedModel) {
		// 同上：该模型族只能由 Anthropic 系上游服务，而唯一可调度平台是
		// grok/CN 供应商且分组未开跨协议转换 → 不能当作“透传可用”。
		return false
	}
	_, ok = schedulable[detected]
	return ok
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
	if s.modelsListCache != nil {
		return catalogHasRequestedModel(s.ensureGroupModelsCatalog(ctx, &groupID), requestedModel)
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
	presence := s.groupCatalogHasRequestedModel(ctx, groupID, requestedModel)
	if !skipKeyRouteForCatalog(presence, siblingHasPresent) {
		return true
	}
	if siblingHasPresent {
		return false
	}
	gid := groupID
	return groupPassthroughsRequestedModel(group, requestedModel, s.GetSchedulablePlatforms(ctx, &gid))
}

func (s *GatewayService) groupCatalogUsableForRequest(ctx context.Context, groupID int64, requestPlatform, requestedModel string) bool {
	if s == nil {
		return true
	}
	gid := groupID
	group := s.GroupPolicyForRequest(ctx, gid)
	platforms := s.GetSchedulablePlatforms(ctx, &gid)
	if group == nil {
		if s.groupCatalogHasRequestedModel(ctx, groupID, requestedModel) != groupCatalogModelAbsent {
			return true
		}
		return groupPassthroughsRequestedModel(nil, requestedModel, platforms)
	}
	if !groupUsableForRequest(group, requestPlatform, requestedModel, platforms) {
		return false
	}
	if group.CustomModelsListEnabled() {
		return true
	}
	if s.groupCatalogHasRequestedModel(ctx, groupID, requestedModel) != groupCatalogModelAbsent {
		return true
	}
	return groupPassthroughsRequestedModel(group, requestedModel, platforms)
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
	detected, detectedOK := DetectModelPlatform(model)
	prefer := []string{
		PlatformOpenAI, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformCline, PlatformCommandCode,
		PlatformAnthropic, PlatformGemini, PlatformAntigravity,
	}
	if detectedOK && detected == PlatformGrok {
		prefer = []string{
			PlatformGrok, PlatformOpenAI, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformCline, PlatformCommandCode,
			PlatformAnthropic, PlatformGemini, PlatformAntigravity,
		}
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
	// Catalog miss is not a closed world. A grok-* (or gpt-*, claude-*) ID that
	// no mapping lists yet still belongs on a matching-platform candidate group.
	if detectedOK {
		for _, gid := range ids {
			gid := gid
			group := s.GroupPolicyForRequest(ctx, gid)
			if group == nil && apiKey.Group != nil && apiKey.Group.ID == gid {
				group = apiKey.Group
			}
			if !groupAllowsRequestedModel(group, model) {
				continue
			}
			if groupPassthroughsRequestedModel(group, model, s.GetSchedulablePlatforms(ctx, &gid)) {
				return detected, true
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

// ContextWithAPIKeyRoute carries the selected billing group into scheduling
// and forwarding. For composite routes the API key still owns the parent group.
func ContextWithAPIKeyRoute(ctx context.Context, key *APIKey) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if key == nil || key.GroupID == nil || key.Group == nil || key.Group.ID != *key.GroupID {
		return ctx
	}
	return context.WithValue(ctx, ctxkey.Group, key.Group)
}

func apiKeyRouteGroupAllowed(key *APIKey) bool {
	if key == nil || key.User == nil || key.GroupID == nil || key.Group == nil ||
		key.Group.ID != *key.GroupID || !IsGroupContextValid(key.Group) || !key.Group.IsActive() {
		return false
	}
	// Subscription admission and limits are checked by the request's subscription
	// resolver. They do not use the standard/exclusive AllowedGroups policy.
	return key.Group.IsSubscriptionType() || key.User.CanBindGroup(key.Group.ID, key.Group.IsExclusive)
}

type apiKeyRouteCandidate struct {
	key      *APIKey
	catalog  groupModelsCatalog
	presence groupCatalogModelPresence
	model    string
}

type apiKeyRouteIterator struct {
	ctx               context.Context
	apiKey            *APIKey
	groupIDs          []int64
	requestedModel    string
	hydrate           func(context.Context, *APIKey, int64) (*APIKey, error)
	resolveCandidate  func(context.Context, *APIKey) (*APIKey, error)
	catalog           func(context.Context, *int64) groupModelsCatalog
	resolveMapping    func(context.Context, *int64, string) (ChannelMappingResult, bool)
	fallbackModel     func(context.Context, *Group, string) string
	candidates        []apiKeyRouteCandidate
	nextGroup         int
	nextCandidate     int
	siblingHasPresent bool
	err               error
	// skipReasons counts why candidate groups were dropped before account
	// selection. It turns the bare "no available accounts" routing error into a
	// diagnosable one ("filtered: catalog_model_absent=1 platform_mismatch=1").
	skipReasons map[string]int
}

// keyRouteSkipReasons 是路由层候选被过滤原因的分类常量。
const (
	keyRouteSkipUnauthorized      = "unauthorized"
	keyRouteSkipModelNotAllowed   = "model_not_in_group_allowlist"
	keyRouteSkipChannelRestricted = "channel_mapping_restricted"
	keyRouteSkipCatalogAbsent     = "catalog_model_absent"
	keyRouteSkipCatalogUnknown    = "catalog_unknown_with_sibling"
	keyRouteSkipPlatformMismatch  = "platform_mismatch"
	keyRouteSkipHydrateFailed     = "group_hydrate_failed"
)

func (r *apiKeyRouteIterator) recordSkip(reason string) {
	if r == nil || reason == "" {
		return
	}
	if r.skipReasons == nil {
		r.skipReasons = make(map[string]int)
	}
	r.skipReasons[reason]++
}

// skipSummary renders the collected skip counts in a stable order.
func (r *apiKeyRouteIterator) skipSummary() string {
	if r == nil || len(r.skipReasons) == 0 {
		return ""
	}
	keys := make([]string, 0, len(r.skipReasons))
	total := 0
	for reason, count := range r.skipReasons {
		keys = append(keys, reason)
		total += count
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, reason := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", reason, r.skipReasons[reason]))
	}
	return fmt.Sprintf("routes_skipped=%d filtered: %s", total, strings.Join(parts, " "))
}

func newAPIKeyRouteIterator(ctx context.Context, apiKey *APIKey, groupIDs []int64, requestedModel string,
	hydrate func(context.Context, *APIKey, int64) (*APIKey, error),
	resolveCandidate func(context.Context, *APIKey) (*APIKey, error),
	catalog func(context.Context, *int64) groupModelsCatalog,
	resolveMapping func(context.Context, *int64, string) (ChannelMappingResult, bool),
	fallbackModel func(context.Context, *Group, string) string,
) apiKeyRouteIterator {
	groupIDs, constraintErr := securityPenaltyRouteCandidateIDs(ctx, apiKey, groupIDs)
	return apiKeyRouteIterator{
		ctx: ctx, apiKey: apiKey, groupIDs: groupIDs, requestedModel: requestedModel,
		hydrate: hydrate, resolveCandidate: resolveCandidate, catalog: catalog,
		resolveMapping: resolveMapping, fallbackModel: fallbackModel,
		err: constraintErr,
	}
}

// Look ahead only for unknown catalogs: a later authorized group that explicitly
// supports the model must still take precedence over unknown earlier groups.
func (r *apiKeyRouteIterator) next() (apiKeyRouteCandidate, bool) {
	for {
		if r.nextCandidate == len(r.candidates) {
			r.candidates = r.candidates[:0]
			r.nextCandidate = 0
			if !r.loadNext() {
				return apiKeyRouteCandidate{}, false
			}
		}
		candidate := r.candidates[r.nextCandidate]
		r.nextCandidate++
		if !candidate.key.Group.CustomModelsListEnabled() {
			// 透传账号（OpenAI / Anthropic auto-passthrough）只替换认证、可服务任意
			// 模型，它的账号映射目录天然不完整：不能按「目录没有该模型」把整组跳过。
			if !candidate.catalog.passthrough && candidate.presence == groupCatalogModelUnknown {
				for !r.siblingHasPresent && r.loadNext() {
				}
			}
			if !candidate.catalog.passthrough &&
				skipKeyRouteForCatalog(candidate.presence, r.siblingHasPresent) &&
				(r.siblingHasPresent ||
					!groupPassthroughsRequestedModel(candidate.key.Group, r.requestedModel, candidate.catalog.platforms)) {
				if candidate.presence == groupCatalogModelUnknown {
					r.recordSkip(keyRouteSkipCatalogUnknown)
				} else {
					r.recordSkip(keyRouteSkipCatalogAbsent)
				}
				continue
			}
		}
		return candidate, true
	}
}

// Authorization and mapping remain candidate-scoped and precede catalog reads.
func (r *apiKeyRouteIterator) loadNext() bool {
	for r.nextGroup < len(r.groupIDs) {
		groupID := r.groupIDs[r.nextGroup]
		r.nextGroup++
		routed, err := r.hydrate(r.ctx, r.apiKey, groupID)
		if err != nil {
			r.recordSkip(keyRouteSkipHydrateFailed)
			r.err = err
			continue
		}
		// Route authorization applies to the API-key-bound candidate. A Claude
		// fallback is an official group-level redirect and must not be rejected
		// again as if it were directly bound to the key.
		if !apiKeyRouteGroupAllowed(routed) {
			r.recordSkip(keyRouteSkipUnauthorized)
			if securityPenaltyConstraint(r.ctx) != nil {
				r.err = ErrSecurityPenaltyGroupRestricted
			}
			continue
		}
		if !groupAllowsRequestedModel(routed.Group, r.requestedModel) {
			r.recordSkip(keyRouteSkipModelNotAllowed)
			continue
		}
		if r.resolveCandidate != nil {
			routed, err = r.resolveCandidate(ContextWithAPIKeyRoute(r.ctx, routed), routed)
			if err != nil {
				r.err = err
				continue
			}
		}
		if err := CheckSecurityPenaltySelectedGroup(r.ctx, routed); err != nil {
			r.err = err
			continue
		}
		if routed == nil || routed.Group == nil || !groupAllowsRequestedModel(routed.Group, r.requestedModel) {
			continue
		}
		if OpenAIImageGenerationIntentFromContext(r.ctx) && !GroupAllowsImageGeneration(routed.Group) {
			continue
		}
		routeCtx := ContextWithAPIKeyRoute(r.ctx, routed)
		mapping, restricted := r.resolveMapping(routeCtx, routed.GroupID, r.requestedModel)
		if restricted {
			r.recordSkip(keyRouteSkipChannelRestricted)
			continue
		}
		model := mapping.MappedModel
		if !mapping.Mapped && r.fallbackModel != nil {
			model = r.fallbackModel(routeCtx, routed.Group, r.requestedModel)
		}
		cat := r.catalog(routeCtx, routed.GroupID)
		presence := catalogHasRequestedModel(cat, r.requestedModel)
		if presence != groupCatalogModelPresent && model != r.requestedModel &&
			catalogHasRequestedModel(cat, model) == groupCatalogModelPresent {
			presence = groupCatalogModelPresent
		}
		if presence == groupCatalogModelPresent {
			r.siblingHasPresent = true
		}
		r.candidates = append(r.candidates, apiKeyRouteCandidate{key: routed, catalog: cat, presence: presence, model: model})
		return true
	}
	return false
}

func hydrateAPIKeyGroup(ctx context.Context, apiKey *APIKey, groupID int64, getGroup func(context.Context, int64) (*Group, error)) (*APIKey, error) {
	if apiKey == nil {
		return nil, ErrNoAvailableAccounts
	}
	if getGroup != nil {
		group, err := getGroup(ctx, groupID)
		if err != nil {
			return nil, err
		}
		if group != nil && group.ID == groupID {
			return cloneAPIKeyWithGroupID(apiKey, group), nil
		}
		return nil, ErrSchedulerCacheNotReady
	}
	if apiKey.GroupID != nil && *apiKey.GroupID == groupID && apiKey.Group != nil && apiKey.Group.ID == groupID {
		return apiKey, nil
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

// validateAPIKeyGroupRoutes 校验请求里的分组绑定，并回传主分组对象。
// 主分组对象用于让创建/更新回执带上完整 group：列表接口用 WithGroup 预加载，
// 回执缺了 group 时前端乐观插入的行在「立即使用密钥」时会被误判成无分组。
func (s *APIKeyService) validateAPIKeyGroupRoutes(ctx context.Context, user *User, groupID *int64, groupIDs []int64) ([]int64, *int64, *Group, error) {
	routeIDs, primary, err := normalizeAPIKeyGroupIDs(groupID, groupIDs)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(routeIDs) == 0 {
		return nil, primary, nil, nil
	}
	var primaryGroup *Group
	for _, id := range routeIDs {
		group, err := s.groupRepo.GetByID(ctx, id)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("get group: %w", err)
		}
		if !s.canUserBindGroup(ctx, user, group) {
			return nil, nil, nil, ErrGroupNotAllowed
		}
		if primary != nil && *primary == id {
			primaryGroup = group
		}
	}
	return routeIDs, primary, primaryGroup, nil
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
