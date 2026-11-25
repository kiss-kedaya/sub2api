package service

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Smart routing picks among the groups bound to an API key. This file keeps the
// channel monitor's numbers (success rate, cache rate, first-token time) per group
// and per model in a cached snapshot, so choosing a group never waits on the
// monitor query. How the numbers, the group's price and the user's own order are
// combined is in group_route_plan.go.
const (
	groupRouteQualityNeutralScore = 0.5
	groupRouteQualityTTL          = 2 * time.Minute
	groupRouteQualityWindow       = time.Hour
	groupRouteQualityMinSamples   = int64(20)
	groupRouteQualityTTFTTargetMs = 5000.0
)

// GroupRouteQuality is the monitor's aggregate for one group, either for one
// model (Model set) or across all of the group's models (Model empty).
type GroupRouteQuality struct {
	GroupID     int64
	Model       string
	SuccessRate float64
	CacheRate   float64
	TTFTMs      float64
	Samples     int64
}

// GroupRouteQualitySnapshot is everything the monitor knows about one group.
type GroupRouteQualitySnapshot struct {
	Overall GroupRouteQuality
	// ByModel is keyed by normalizeRouteModel(model).
	ByModel map[string]GroupRouteQuality
}

// lookup returns the numbers to judge `model` by: the group's own numbers for
// that model when there are enough samples, else the group-wide ones, else
// nothing (the caller treats the group as unknown, which scores neutral).
func (s GroupRouteQualitySnapshot) lookup(model string) (GroupRouteQuality, bool) {
	if q, ok := s.ByModel[normalizeRouteModel(model)]; ok && q.Samples >= groupRouteQualityMinSamples {
		return q, true
	}
	if s.Overall.Samples >= groupRouteQualityMinSamples {
		return s.Overall, true
	}
	return GroupRouteQuality{}, false
}

func normalizeRouteModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}

// GroupRouteQualitySource loads the latest quality snapshot for all groups.
type GroupRouteQualitySource interface {
	Snapshot(ctx context.Context) (map[int64]GroupRouteQualitySnapshot, error)
}

// GroupRouteQualityCache keeps the last successful snapshot and refreshes it in
// the background. Callers never block on the monitor query.
type GroupRouteQualityCache struct {
	source GroupRouteQualitySource
	ttl    time.Duration
	now    func() time.Time

	mu          sync.Mutex
	snapshot    map[int64]GroupRouteQualitySnapshot
	fetchedAt   time.Time
	lastAttempt time.Time
	refreshing  bool
	lastErr     error
}

func NewGroupRouteQualityCache(source GroupRouteQualitySource) *GroupRouteQualityCache {
	return &GroupRouteQualityCache{
		source:   source,
		ttl:      groupRouteQualityTTL,
		now:      time.Now,
		snapshot: map[int64]GroupRouteQualitySnapshot{},
	}
}

func (c *GroupRouteQualityCache) clock() time.Time {
	if c == nil || c.now == nil {
		return time.Now()
	}
	return c.now()
}

// Current returns the latest snapshot (nil when there is no monitor) and starts a
// background refresh when it is stale. The returned map is never modified after it
// is published, so callers may read it without holding any lock.
func (c *GroupRouteQualityCache) Current(ctx context.Context) map[int64]GroupRouteQualitySnapshot {
	if c == nil || c.source == nil {
		return nil
	}
	c.ensureAsync(ctx)
	c.mu.Lock()
	snapshot := c.snapshot
	c.mu.Unlock()
	return snapshot
}

// Refresh loads a new snapshot; a failed load keeps the previous one.
func (c *GroupRouteQualityCache) Refresh(ctx context.Context) error {
	if c == nil || c.source == nil {
		return nil
	}
	c.mu.Lock()
	if c.refreshing {
		c.mu.Unlock()
		return nil
	}
	c.refreshing = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.refreshing = false
		c.mu.Unlock()
	}()

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	snapshot, err := c.source.Snapshot(ctx)
	if err != nil {
		c.mu.Lock()
		c.lastErr = err
		c.mu.Unlock()
		return err
	}
	c.mu.Lock()
	c.snapshot = snapshot
	c.fetchedAt = c.clock()
	c.lastErr = nil
	c.mu.Unlock()
	return nil
}

func (c *GroupRouteQualityCache) ensureAsync(ctx context.Context) {
	if c == nil || c.source == nil {
		return
	}
	now := c.clock()
	c.mu.Lock()
	fresh := !c.fetchedAt.IsZero() && now.Sub(c.fetchedAt) < c.ttl
	busy := c.refreshing
	retryBlocked := !c.lastAttempt.IsZero() && now.Sub(c.lastAttempt) < 30*time.Second
	if fresh || busy || retryBlocked {
		c.mu.Unlock()
		return
	}
	// Record the attempt before spawning so a failing source cannot start one
	// refresh per request when the monitor is disabled or unreachable.
	c.lastAttempt = now
	c.mu.Unlock()
	fetchCtx := context.Background()
	if ctx != nil {
		fetchCtx = context.WithoutCancel(ctx)
	}
	go func() { _ = c.Refresh(fetchCtx) }()
}

// channelMonitorRouteQualitySource adapts the channel-monitor matrix into the
// cache's snapshot type.
type channelMonitorRouteQualitySource struct {
	monitor *ChannelMonitorV2Service
}

func NewChannelMonitorRouteQualitySource(monitor *ChannelMonitorV2Service) GroupRouteQualitySource {
	return &channelMonitorRouteQualitySource{monitor: monitor}
}

func (s *channelMonitorRouteQualitySource) Snapshot(ctx context.Context) (map[int64]GroupRouteQualitySnapshot, error) {
	if s == nil || s.monitor == nil {
		return map[int64]GroupRouteQualitySnapshot{}, nil
	}
	now := time.Now()
	filter := ChannelMonitorV2Filter{
		Start:  now.Add(-groupRouteQualityWindow),
		End:    now,
		Bucket: 5 * time.Minute,
	}
	// One query at group x model granularity; the group-wide numbers are derived
	// from the same rows, so this costs the same as the group-only query did.
	matrix, err := s.monitor.Matrix(ctx, filter, ChannelMonitorV2GroupByPlatformGroupModel, true)
	if err != nil {
		return nil, err
	}
	if matrix == nil {
		return map[int64]GroupRouteQualitySnapshot{}, nil
	}
	return aggregateRouteQualityRows(matrix.Items), nil
}

// aggregateRouteQualityRows turns group x model monitor rows into per-group
// snapshots. The group-wide rates are request-count weighted averages of the
// model rows; the group-wide first-token time is the same weighted average of the
// model medians (an approximation, which is all a ranking signal needs).
func aggregateRouteQualityRows(rows []ChannelMonitorV2MatrixRow) map[int64]GroupRouteQualitySnapshot {
	type sums struct {
		requests             int64
		success, cache, ttft float64
		ttftRequests         int64
		models               map[string]GroupRouteQuality
	}
	acc := map[int64]*sums{}
	for _, row := range rows {
		if row.GroupID == nil || *row.GroupID <= 0 {
			continue
		}
		n := row.Metrics.RequestCount
		if n <= 0 {
			continue
		}
		id := *row.GroupID
		s := acc[id]
		if s == nil {
			s = &sums{models: map[string]GroupRouteQuality{}}
			acc[id] = s
		}
		quality := GroupRouteQuality{
			GroupID:     id,
			Model:       normalizeRouteModel(row.Model),
			SuccessRate: row.Metrics.SuccessRate,
			CacheRate:   row.Metrics.CacheRate,
			Samples:     n,
		}
		if row.Metrics.TTFT.P50Ms != nil {
			quality.TTFTMs = float64(*row.Metrics.TTFT.P50Ms)
		}
		s.requests += n
		s.success += quality.SuccessRate * float64(n)
		s.cache += quality.CacheRate * float64(n)
		if quality.TTFTMs > 0 {
			s.ttft += quality.TTFTMs * float64(n)
			s.ttftRequests += n
		}
		if quality.Model != "" {
			// The same model can appear on several platforms of one group row set;
			// keep the busier one rather than overwrite it with a quieter one.
			if prev, ok := s.models[quality.Model]; !ok || quality.Samples > prev.Samples {
				s.models[quality.Model] = quality
			}
		}
	}
	out := make(map[int64]GroupRouteQualitySnapshot, len(acc))
	for id, s := range acc {
		overall := GroupRouteQuality{
			GroupID:     id,
			SuccessRate: s.success / float64(s.requests),
			CacheRate:   s.cache / float64(s.requests),
			Samples:     s.requests,
		}
		if s.ttftRequests > 0 {
			overall.TTFTMs = s.ttft / float64(s.ttftRequests)
		}
		out[id] = GroupRouteQualitySnapshot{Overall: overall, ByModel: s.models}
	}
	return out
}

var (
	defaultRouteQualityMu  sync.RWMutex
	defaultRouteQualitySvc *GroupRouteQualityCache
)

// SetDefaultGroupRouteQualityCache lets the application wiring publish the
// monitor-backed cache without threading it through every constructor.
func SetDefaultGroupRouteQualityCache(cache *GroupRouteQualityCache) {
	defaultRouteQualityMu.Lock()
	defaultRouteQualitySvc = cache
	defaultRouteQualityMu.Unlock()
}

func DefaultGroupRouteQualityCache() *GroupRouteQualityCache {
	defaultRouteQualityMu.RLock()
	defer defaultRouteQualityMu.RUnlock()
	return defaultRouteQualitySvc
}

// SetRouteQualityCache installs an explicit cache on the gateway (tests and
// alternative wiring). When nil, the process default is used.
func (s *GatewayService) SetRouteQualityCache(cache *GroupRouteQualityCache) {
	if s == nil {
		return
	}
	s.routeQuality = cache
}

func (s *GatewayService) groupRouteQualityCache() *GroupRouteQualityCache {
	if s != nil && s.routeQuality != nil {
		return s.routeQuality
	}
	return DefaultGroupRouteQualityCache()
}

func clampUnit(value float64) float64 {
	switch {
	case value < 0:
		return 0
	case value > 1:
		return 1
	default:
		return value
	}
}
