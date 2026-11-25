package service

import (
	"context"
	"log/slog"
	"math"
	"time"
)

// Smart routing: which of a key's groups is tried first.
//
// A key with several groups used to try them in the order the owner listed them,
// and (2.3.153 on) in the order of the monitor's quality score. Both changed the
// order under a running conversation, and every change moved it onto another
// upstream account whose prompt cache was cold: the user paid for the same prefix
// again and again.
//
// The rules now:
//
//  1. Every candidate group is scored for the requested model from five things:
//     success rate, cache rate, first-token time, price (the user's effective rate
//     multiplier) and the owner's own priority (the group's place in the key's list).
//  2. A session is bound to the group that served it (per API key + model + session).
//     A bound session goes to its group first. It is judged again only when
//     - that group cannot supply an account for this request (an error),
//     - this request has already failed upstream once (the failover retry), or
//     - the monitor shows that group failing most of its requests.
//     A session without a binding (a new one) is judged by the score, and bound to
//     whichever group then serves it.
//  3. Scoring and binding only reorder the candidates. Authorization, allow-lists,
//     platform fit and billing admission are still enforced per candidate by the
//     route iterator, exactly as before.
const (
	routeWeightSuccess  = 0.30
	routeWeightCache    = 0.20
	routeWeightTTFT     = 0.10
	routeWeightCost     = 0.20
	routeWeightPriority = 0.20

	// Scores closer than this are a tie, and a tie goes to the owner's order. It
	// keeps small, noisy differences in the monitor numbers from reshuffling groups.
	routeScoreResolution = 0.02

	// A bound group whose success rate over the monitor window is below this (with
	// enough samples) counts as failing, and its sessions are judged again.
	routeBoundMinSuccessRate = 0.5

	// Redis is consulted on the request path: never wait long, never fail a request
	// because of it.
	routeGroupBindingTimeout = 50 * time.Millisecond
)

// RouteGroupBindingCache remembers which of a key's groups served a session. The
// Redis gateway cache implements it; a cache that does not simply disables the
// binding and groups are judged by score alone.
type RouteGroupBindingCache interface {
	// GetRouteGroup returns the bound group, or ErrStickySessionNotFound.
	GetRouteGroup(ctx context.Context, apiKeyID int64, model, sessionHash string) (int64, error)
	SetRouteGroup(ctx context.Context, apiKeyID int64, model, sessionHash string, groupID int64, ttl time.Duration) error
}

// routeGroupDeps is what planning needs from a gateway service. Every field may
// be nil: the plan then simply knows less and scores those things neutral.
type routeGroupDeps struct {
	quality  map[int64]GroupRouteQualitySnapshot
	bindings RouteGroupBindingCache
	ttl      time.Duration
	// group is a cheap, in-process lookup; nil means unknown.
	group func(id int64) *Group
	// rate resolves the user's effective multiplier from warm local state only.
	rate func(userID, groupID int64, groupDefault float64) float64
}

// routeGroupPlan is the order to try a key's groups in, plus what is needed to
// remember the group that finally served the request.
type routeGroupPlan struct {
	Order []int64
	// Bound is the group the session was bound to and still is (0 when the
	// session is new or is being judged again).
	Bound int64

	bindings RouteGroupBindingCache
	ttl      time.Duration
	apiKeyID int64
	model    string
	session  string
}

// planRouteGroups orders `groupIDs` (the owner's priority order) for one request.
func planRouteGroups(ctx context.Context, deps routeGroupDeps, apiKey *APIKey, groupIDs []int64, model, sessionHash string) routeGroupPlan {
	plan := routeGroupPlan{Order: groupIDs}
	if apiKey == nil || len(groupIDs) < 2 {
		return plan
	}
	model = normalizeRouteModel(model)
	userID := apiKey.UserID
	if userID <= 0 && apiKey.User != nil {
		userID = apiKey.User.ID
	}

	groups := make(map[int64]*Group, len(groupIDs))
	rates := make(map[int64]float64, len(groupIDs))
	for _, id := range groupIDs {
		if deps.group == nil {
			break
		}
		group := deps.group(id)
		if group == nil {
			continue
		}
		groups[id] = group
		if deps.rate != nil {
			if rate := deps.rate(userID, id, group.RateMultiplier); rate >= 0 && !math.IsNaN(rate) && !math.IsInf(rate, 0) {
				rates[id] = rate
			}
		}
	}

	cost := routeCostTerms(groupIDs, groups, rates, model)
	scores := make(map[int64]float64, len(groupIDs))
	last := float64(len(groupIDs) - 1)
	for i, id := range groupIDs {
		quality, known := deps.quality[id].lookup(model)
		success, cache, ttft := routeQualityTerms(quality, known)
		priority := 1 - float64(i)/last
		scores[id] = routeWeightSuccess*success + routeWeightCache*cache + routeWeightTTFT*ttft +
			routeWeightCost*cost[id] + routeWeightPriority*priority
	}
	plan.Order = rankRouteGroups(groupIDs, scores)

	// No session to bind (no client session signal, or no Redis): the score decides.
	if deps.bindings == nil || sessionHash == "" || apiKey.ID <= 0 {
		return plan
	}
	plan.bindings, plan.ttl = deps.bindings, deps.ttl
	plan.apiKeyID, plan.model, plan.session = apiKey.ID, model, sessionHash

	// This request already failed upstream once: that is the "error" that allows a
	// new judgement, so the binding is not consulted.
	if requestHasUpstreamFailure(ctx) {
		return plan
	}
	bound := plan.readBinding(ctx)
	if bound <= 0 || !containsGroupID(groupIDs, bound) || routeBoundGroupFailing(deps.quality, bound, model) {
		return plan
	}
	plan.Bound = bound
	plan.Order = moveGroupToFront(plan.Order, bound)
	return plan
}

func (p *routeGroupPlan) readBinding(ctx context.Context) int64 {
	readCtx, cancel := context.WithTimeout(ctx, routeGroupBindingTimeout)
	defer cancel()
	id, err := p.bindings.GetRouteGroup(readCtx, p.apiKeyID, p.model, p.session)
	if err != nil {
		// A miss is the normal new-session case; anything else is Redis trouble, and
		// the request must not depend on it.
		return 0
	}
	return id
}

// remember binds the session to the group that served this request. `groupID` is
// the group's place in the key's own list, not a Claude fallback group it may have
// been redirected to, so the same list entry comes first next time.
func (p routeGroupPlan) remember(ctx context.Context, groupID int64) {
	if p.bindings == nil || p.session == "" || groupID <= 0 {
		return
	}
	// The request may already be finished or cancelled; the binding is still worth writing.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*routeGroupBindingTimeout)
	defer cancel()
	if err := p.bindings.SetRouteGroup(writeCtx, p.apiKeyID, p.model, p.session, groupID, p.ttl); err != nil {
		slog.Debug("route_group.bind_failed", "api_key_id", p.apiKeyID, "group_id", groupID, "error", err)
		return
	}
	if p.Bound != 0 && p.Bound != groupID {
		slog.Debug("route_group.rebound", "api_key_id", p.apiKeyID, "from", p.Bound, "to", groupID)
	}
}

// routeQualityTerms turns monitor numbers into 0..1 terms; unknown is neutral.
func routeQualityTerms(q GroupRouteQuality, known bool) (success, cache, ttft float64) {
	if !known {
		return groupRouteQualityNeutralScore, groupRouteQualityNeutralScore, groupRouteQualityNeutralScore
	}
	success, cache = clampUnit(q.SuccessRate), clampUnit(q.CacheRate)
	ttft = groupRouteQualityNeutralScore
	if q.TTFTMs > 0 {
		ttft = clampUnit(1 - q.TTFTMs/groupRouteQualityTTFTTargetMs)
	}
	return success, cache, ttft
}

// routeGroupComparableByRate reports whether a group's multiplier says something
// about what this request would cost: a complete, active, token-priced group that
// is allowed to serve the model. Subscription groups are paid for up front and an
// incomplete group has no trustworthy multiplier, so both stay neutral.
func routeGroupComparableByRate(group *Group, model string) bool {
	return group != nil && group.Hydrated && group.IsActive() && !group.IsSubscriptionType() &&
		groupAllowsRequestedModel(group, model)
}

// routeCostTerms scores price: the cheapest comparable group gets 1 and a group that
// costs k times as much gets 1/k; a free group gets 1. With fewer than two
// comparable groups there is nothing to compare and everyone stays neutral.
func routeCostTerms(groupIDs []int64, groups map[int64]*Group, rates map[int64]float64, model string) map[int64]float64 {
	terms := make(map[int64]float64, len(groupIDs))
	comparable := 0
	minRate := math.Inf(1)
	for _, id := range groupIDs {
		terms[id] = groupRouteQualityNeutralScore
		rate, ok := rates[id]
		if !ok || !routeGroupComparableByRate(groups[id], model) {
			continue
		}
		comparable++
		if rate > 0 && rate < minRate {
			minRate = rate
		}
	}
	if comparable < 2 {
		return terms
	}
	for _, id := range groupIDs {
		rate, ok := rates[id]
		if !ok || !routeGroupComparableByRate(groups[id], model) {
			continue
		}
		if rate <= 0 || math.IsInf(minRate, 1) {
			terms[id] = 1
			continue
		}
		terms[id] = clampUnit(minRate / rate)
	}
	return terms
}

// rankRouteGroups orders groups by score, highest first. Scores within
// routeScoreResolution of the best remaining one are a tie, and the tie goes to the
// group the owner listed first. It never drops a group.
func rankRouteGroups(groupIDs []int64, scores map[int64]float64) []int64 {
	remaining := append([]int64(nil), groupIDs...)
	out := make([]int64, 0, len(groupIDs))
	for len(remaining) > 0 {
		best := math.Inf(-1)
		for _, id := range remaining {
			if scores[id] > best {
				best = scores[id]
			}
		}
		pick := 0
		for i, id := range remaining {
			if scores[id] >= best-routeScoreResolution {
				pick = i
				break
			}
		}
		out = append(out, remaining[pick])
		remaining = append(remaining[:pick], remaining[pick+1:]...)
	}
	return out
}

func routeBoundGroupFailing(quality map[int64]GroupRouteQualitySnapshot, groupID int64, model string) bool {
	q, known := quality[groupID].lookup(model)
	return known && q.SuccessRate < routeBoundMinSuccessRate
}

func containsGroupID(ids []int64, id int64) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

// moveGroupToFront returns a copy of ids with id first and the rest in order.
func moveGroupToFront(ids []int64, id int64) []int64 {
	out := make([]int64, 0, len(ids))
	out = append(out, id)
	for _, candidate := range ids {
		if candidate != id {
			out = append(out, candidate)
		}
	}
	return out
}

type routeSessionContextKey struct{}

// withRouteSessionHash carries the client's session hash down to group selection;
// the OpenAI-compatible entry points derive it before selection starts.
func withRouteSessionHash(ctx context.Context, sessionHash string) context.Context {
	if sessionHash == "" {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, routeSessionContextKey{}, sessionHash)
}

func routeSessionHashFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	hash, _ := ctx.Value(routeSessionContextKey{}).(string)
	return hash
}

// requestHasUpstreamFailure reports whether an upstream attempt of this request has
// already failed. The state is installed once per request by
// PrepareSchedulerRequestContext and shared by every retry.
func requestHasUpstreamFailure(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	state, _ := ctx.Value(upstreamAttemptContextKey{}).(*upstreamAttempts)
	if state == nil {
		return false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return len(state.failed) > 0
}

// routeGroupLookup is the cheap group lookup planning uses: the key's own group when
// it matches, else the scheduler snapshot's in-process copy. It never touches the
// database; a cold snapshot just means unknown.
func routeGroupLookup(ctx context.Context, apiKey *APIKey, snapshot *SchedulerSnapshotService) func(int64) *Group {
	return func(id int64) *Group {
		if apiKey != nil && apiKey.Group != nil && apiKey.Group.ID == id {
			return apiKey.Group
		}
		if snapshot == nil {
			return nil
		}
		group, err := snapshot.GetGroupByIDLite(ctx, id)
		if err != nil {
			return nil
		}
		return group
	}
}

// routeGroupDeps wires planning to the Claude/Gemini gateway service.
func (s *GatewayService) routeGroupDeps(ctx context.Context, apiKey *APIKey) routeGroupDeps {
	deps := routeGroupDeps{
		quality: s.groupRouteQualityCache().Current(ctx),
		ttl:     stickySessionTTL,
		group:   routeGroupLookup(ctx, apiKey, s.schedulerSnapshot),
		rate:    s.getUserGroupRateMultiplierSnapshotOnly,
	}
	if bindings, ok := s.cache.(RouteGroupBindingCache); ok {
		deps.bindings = bindings
	}
	return deps
}

// routeGroupDeps wires planning to the OpenAI-compatible gateway service, which
// shares the process-default quality cache.
func (s *OpenAIGatewayService) routeGroupDeps(ctx context.Context, apiKey *APIKey) routeGroupDeps {
	ttl := openaiStickySessionTTL
	if s.cfg != nil && s.cfg.Gateway.OpenAIWS.StickySessionTTLSeconds > 0 {
		ttl = time.Duration(s.cfg.Gateway.OpenAIWS.StickySessionTTLSeconds) * time.Second
	}
	deps := routeGroupDeps{
		quality: DefaultGroupRouteQualityCache().Current(ctx),
		ttl:     ttl,
		group:   routeGroupLookup(ctx, apiKey, s.schedulerSnapshot),
		rate:    s.resolveUserGroupRateMultiplierSnapshotOnly,
	}
	if bindings, ok := s.cache.(RouteGroupBindingCache); ok {
		deps.bindings = bindings
	}
	return deps
}
