package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// ErrCompositeRouteCacheNotReady indicates that the immutable route projection
// has not been published yet.  Callers should return a retryable readiness
// response rather than silently selecting a detector route (which could ignore
// an explicit administrator mapping).
var ErrCompositeRouteCacheNotReady = errors.New("composite route cache not ready")

// Keep the old package-local name for service tests and compatibility helpers.
var errCompositeRouteCacheNotReady = ErrCompositeRouteCacheNotReady

type CompositeRouteResolver struct {
	repo CompositeModelRouteRepository

	// routesCache is a process-local immutable projection of enabled routes.
	// Composite routing is consulted before account selection, so a repository
	// read here would put a SQL query on every model request. Admin mutations
	// call InvalidateGroup; the TTL is a cross-instance safety bound.
	mu             sync.RWMutex
	routesCache    map[int64]compositeRouteCacheEntry
	generations    map[int64]uint64
	loadSF         singleflight.Group
	refreshStateMu sync.Mutex
	refreshState   map[int64]compositeRouteRefreshState
}

const compositeRouteCacheTTL = 60 * time.Second

type compositeRouteCacheEntry struct {
	routes    []CompositeModelRoute
	expiresAt time.Time
}

type compositeRouteRefreshState struct {
	failures int
	nextTry  time.Time
}

const (
	compositeRouteRefreshRetryBase = 5 * time.Second
	compositeRouteRefreshRetryMax  = time.Minute
)

func NewCompositeRouteResolver(repo CompositeModelRouteRepository) *CompositeRouteResolver {
	return &CompositeRouteResolver{
		repo:         repo,
		routesCache:  make(map[int64]compositeRouteCacheEntry),
		generations:  make(map[int64]uint64),
		refreshState: make(map[int64]compositeRouteRefreshState),
	}
}

func (r *CompositeRouteResolver) Resolve(ctx context.Context, groupID int64, model, endpoint string) (CompositeRouteDecision, error) {
	model = strings.TrimSpace(model)
	endpoint = normalizeCompositeRouteEndpoint(endpoint)
	decision := CompositeRouteDecision{
		GroupID:     groupID,
		PublicModel: model,
		Endpoint:    endpoint,
	}
	if model == "" {
		decision.Reason = "model is required"
		return decision, nil
	}

	if r != nil && groupID > 0 {
		routes, err := r.routesForGroup(ctx, groupID)
		if err != nil {
			return decision, fmt.Errorf("list composite routes: %w", err)
		}
		if route, ok := matchCompositeRoute(routes, model, endpoint); ok {
			upstreamModel := strings.TrimSpace(route.UpstreamModel)
			if upstreamModel == "" {
				upstreamModel = model
			}
			return CompositeRouteDecision{
				Matched:        true,
				Source:         CompositeRouteSourceExplicit,
				GroupID:        groupID,
				PublicModel:    model,
				TargetPlatform: route.TargetPlatform,
				UpstreamModel:  upstreamModel,
				Endpoint:       endpoint,
				Route:          &route,
			}, nil
		}
	}

	if platform, ok := DetectModelPlatform(model); ok {
		return CompositeRouteDecision{
			Matched:        true,
			Source:         CompositeRouteSourceDetector,
			GroupID:        groupID,
			PublicModel:    model,
			TargetPlatform: platform,
			UpstreamModel:  model,
			Endpoint:       endpoint,
		}, nil
	}
	decision.Reason = "no explicit route or built-in detector match"
	return decision, nil
}

// routesForGroup returns the immutable route projection. Snapshot-only model
// requests never synchronously fall back to PostgreSQL: a cold group uses the
// built-in model detector for this request while a single background refresh
// populates the route cache for subsequent requests. Explicit/admin callers
// retain the historical synchronous load semantics.
func (r *CompositeRouteResolver) routesForGroup(ctx context.Context, groupID int64) ([]CompositeModelRoute, error) {
	if r == nil || groupID <= 0 || r.repo == nil {
		return nil, nil
	}
	now := time.Now()
	r.mu.RLock()
	entry, cached := r.routesCache[groupID]
	r.mu.RUnlock()
	if cached {
		if now.Before(entry.expiresAt) {
			return cloneCompositeRoutes(entry.routes), nil
		}
		if schedulerSnapshotOnlyFromContext(ctx) {
			r.refreshGroupAsync(groupID)
			return cloneCompositeRoutes(entry.routes), nil
		}
	}
	if schedulerSnapshotOnlyFromContext(ctx) {
		r.refreshGroupAsync(groupID)
		// Do not silently fall back to the built-in detector: an explicit route
		// can intentionally override the detected provider/model. Returning a
		// readiness error lets the handler retry after the background projection
		// is published instead of misrouting the first request.
		return nil, errCompositeRouteCacheNotReady
	}
	value, err, _ := r.loadSF.Do(fmt.Sprintf("%d", groupID), func() (any, error) {
		// Recheck after joining the flight; another caller may have published the
		// route projection while this request was waiting for the lock.
		r.mu.RLock()
		current, ok := r.routesCache[groupID]
		r.mu.RUnlock()
		if ok && time.Now().Before(current.expiresAt) {
			return cloneCompositeRoutes(current.routes), nil
		}
		r.mu.RLock()
		generation := r.generations[groupID]
		r.mu.RUnlock()
		loadCtx := context.Background()
		if ctx != nil {
			loadCtx = context.WithoutCancel(ctx)
		}
		loadCtx, cancel := context.WithTimeout(loadCtx, 5*time.Second)
		defer cancel()
		routes, loadErr := r.repo.ListByGroup(loadCtx, groupID, false)
		if loadErr != nil {
			return nil, loadErr
		}
		r.storeRoutesIfGeneration(groupID, generation, routes)
		return cloneCompositeRoutes(routes), nil
	})
	if err != nil {
		return nil, err
	}
	routes, _ := value.([]CompositeModelRoute)
	return routes, nil
}

func (r *CompositeRouteResolver) storeRoutes(groupID int64, routes []CompositeModelRoute) {
	if r == nil {
		return
	}
	r.mu.RLock()
	generation := r.generations[groupID]
	r.mu.RUnlock()
	r.storeRoutesIfGeneration(groupID, generation, routes)
}

func (r *CompositeRouteResolver) storeRoutesIfGeneration(groupID int64, generation uint64, routes []CompositeModelRoute) {
	if r == nil || groupID <= 0 {
		return
	}
	cloned := cloneCompositeRoutes(routes)
	r.mu.Lock()
	if r.generations[groupID] != generation {
		r.mu.Unlock()
		return
	}
	if r.routesCache == nil {
		r.routesCache = make(map[int64]compositeRouteCacheEntry)
	}
	r.routesCache[groupID] = compositeRouteCacheEntry{routes: cloned, expiresAt: time.Now().Add(compositeRouteCacheTTL)}
	r.mu.Unlock()
}

func (r *CompositeRouteResolver) refreshGroupAsync(groupID int64) {
	if r == nil || r.repo == nil || groupID <= 0 || !r.refreshRetryAllowed(groupID, time.Now()) {
		return
	}
	key := fmt.Sprintf("refresh:%d", groupID)
	r.loadSF.DoChan(key, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r.mu.RLock()
		generation := r.generations[groupID]
		r.mu.RUnlock()
		routes, err := r.repo.ListByGroup(ctx, groupID, false)
		if err != nil {
			r.recordRefreshFailure(groupID)
			return nil, err
		}
		r.storeRoutesIfGeneration(groupID, generation, routes)
		r.clearRefreshFailure(groupID)
		return nil, nil
	})
}

func (r *CompositeRouteResolver) refreshRetryAllowed(groupID int64, now time.Time) bool {
	if r == nil || groupID <= 0 {
		return false
	}
	r.refreshStateMu.Lock()
	defer r.refreshStateMu.Unlock()
	if r.refreshState == nil {
		r.refreshState = make(map[int64]compositeRouteRefreshState)
	}
	state, ok := r.refreshState[groupID]
	return !ok || state.nextTry.IsZero() || !now.Before(state.nextTry)
}

func (r *CompositeRouteResolver) recordRefreshFailure(groupID int64) {
	if r == nil || groupID <= 0 {
		return
	}
	r.refreshStateMu.Lock()
	defer r.refreshStateMu.Unlock()
	if r.refreshState == nil {
		r.refreshState = make(map[int64]compositeRouteRefreshState)
	}
	state := r.refreshState[groupID]
	state.failures++
	delay := compositeRouteRefreshRetryBase
	for i := 1; i < state.failures && delay < compositeRouteRefreshRetryMax; i++ {
		delay *= 2
		if delay >= compositeRouteRefreshRetryMax {
			delay = compositeRouteRefreshRetryMax
			break
		}
	}
	state.nextTry = time.Now().Add(delay)
	r.refreshState[groupID] = state
}

func (r *CompositeRouteResolver) clearRefreshFailure(groupID int64) {
	if r == nil || groupID <= 0 {
		return
	}
	r.refreshStateMu.Lock()
	if r.refreshState != nil {
		delete(r.refreshState, groupID)
	}
	r.refreshStateMu.Unlock()
}

// InvalidateGroup drops a route projection after an admin mutation. It is
// safe to call even when the resolver was constructed with a nil repository.
func (r *CompositeRouteResolver) InvalidateGroup(groupID int64) {
	if r == nil || groupID <= 0 {
		return
	}
	r.mu.Lock()
	delete(r.routesCache, groupID)
	if r.generations == nil {
		r.generations = make(map[int64]uint64)
	}
	r.generations[groupID]++
	r.mu.Unlock()
	r.clearRefreshFailure(groupID)
}

func cloneCompositeRoutes(routes []CompositeModelRoute) []CompositeModelRoute {
	if len(routes) == 0 {
		return nil
	}
	return append([]CompositeModelRoute(nil), routes...)
}

func matchCompositeRoute(routes []CompositeModelRoute, model, endpoint string) (CompositeModelRoute, bool) {
	if len(routes) == 0 {
		return CompositeModelRoute{}, false
	}

	type candidate struct {
		route          CompositeModelRoute
		matchStrength  int
		endpointWeight int
		prefixLen      int
	}
	candidates := make([]candidate, 0, len(routes))
	for _, route := range routes {
		route.Endpoint = normalizeCompositeRouteEndpoint(route.Endpoint)
		if route.Endpoint != endpoint && route.Endpoint != CompositeRouteEndpointAny {
			continue
		}
		route.MatchType = normalizeCompositeRouteMatchType(route.MatchType)
		publicModel := strings.TrimSpace(route.PublicModel)
		if publicModel == "" {
			continue
		}

		matchStrength := 0
		prefixLen := len(publicModel)
		switch route.MatchType {
		case CompositeRouteMatchExact:
			if publicModel != model {
				continue
			}
			matchStrength = 2
		case CompositeRouteMatchPrefix:
			if !strings.HasPrefix(model, publicModel) {
				continue
			}
			matchStrength = 1
		default:
			continue
		}
		endpointWeight := 0
		if route.Endpoint == endpoint {
			endpointWeight = 1
		}
		candidates = append(candidates, candidate{
			route:          route,
			matchStrength:  matchStrength,
			endpointWeight: endpointWeight,
			prefixLen:      prefixLen,
		})
	}
	if len(candidates) == 0 {
		return CompositeModelRoute{}, false
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.matchStrength != b.matchStrength {
			return a.matchStrength > b.matchStrength
		}
		if a.endpointWeight != b.endpointWeight {
			return a.endpointWeight > b.endpointWeight
		}
		if a.prefixLen != b.prefixLen {
			return a.prefixLen > b.prefixLen
		}
		if a.route.Priority != b.route.Priority {
			return a.route.Priority < b.route.Priority
		}
		return a.route.ID < b.route.ID
	})
	return candidates[0].route, true
}
