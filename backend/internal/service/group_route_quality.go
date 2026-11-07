package service

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Smart routing picks among the groups bound to an API key. Without quality
// signals the order is the key's configured order, so a group whose upstream is
// slow or erroring keeps winning. This file adds a lightweight, cached score
// built from the existing channel-monitor aggregates (cache rate, success
// rate, TTFT) and only reorders candidates: no group is ever dropped, and a
// group without samples gets the neutral score, which keeps the original order.
const (
	groupRouteQualityNeutralScore = 0.5
	groupRouteQualityTTL          = 2 * time.Minute
	groupRouteQualityWindow       = time.Hour
	groupRouteQualityMinSamples   = int64(20)
	groupRouteQualityTTFTTargetMs = 5000.0
	groupRouteQualityWeightSum    = 0.5 + 0.3 + 0.2 // success + cache + ttft
)

// GroupRouteQuality is the cached quality signal for one group.
type GroupRouteQuality struct {
	GroupID     int64
	SuccessRate float64
	CacheRate   float64
	TTFTMs      float64
	Samples     int64
	Score       float64
}

// GroupRouteQualitySource loads the latest quality snapshot for all groups.
type GroupRouteQualitySource interface {
	Snapshot(ctx context.Context) (map[int64]GroupRouteQuality, error)
}

// GroupRouteQualityCache keeps the last successful snapshot and refreshes it in
// the background. Callers never block on the monitor query.
type GroupRouteQualityCache struct {
	source GroupRouteQualitySource
	ttl    time.Duration
	now    func() time.Time

	mu          sync.Mutex
	snapshot    map[int64]GroupRouteQuality
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
		snapshot: map[int64]GroupRouteQuality{},
	}
}

func (c *GroupRouteQualityCache) clock() time.Time {
	if c == nil || c.now == nil {
		return time.Now()
	}
	return c.now()
}

// Scores returns one score per requested group. Groups without a fresh
// snapshot get the neutral score so callers preserve their configured order.
func (c *GroupRouteQualityCache) Scores(ctx context.Context, groupIDs []int64) map[int64]float64 {
	scores := make(map[int64]float64, len(groupIDs))
	for _, id := range groupIDs {
		scores[id] = groupRouteQualityNeutralScore
	}
	if c == nil || c.source == nil || len(groupIDs) == 0 {
		return scores
	}
	c.ensureAsync(ctx)
	c.mu.Lock()
	snapshot := c.snapshot
	c.mu.Unlock()
	for _, id := range groupIDs {
		if quality, ok := snapshot[id]; ok {
			scores[id] = quality.Score
		}
	}
	return scores
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
	for id, quality := range snapshot {
		quality.GroupID = id
		quality.Score = scoreGroupRouteQuality(quality.SuccessRate, quality.CacheRate, quality.TTFTMs, quality.Samples)
		snapshot[id] = quality
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

// scoreGroupRouteQuality blends the three monitor signals into 0..1. The TTFT
// term is 1 when the sample is instantaneous and 0 at (or above) the target.
func scoreGroupRouteQuality(successRate, cacheRate, ttftMs float64, samples int64) float64 {
	if samples < groupRouteQualityMinSamples {
		return groupRouteQualityNeutralScore
	}
	success := clampUnit(successRate)
	cache := clampUnit(cacheRate)
	ttft := 1.0
	if ttftMs > 0 {
		ttft = clampUnit(1 - ttftMs/groupRouteQualityTTFTTargetMs)
	}
	return clampUnit((0.5*success + 0.3*cache + 0.2*ttft) / groupRouteQualityWeightSum)
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

// channelMonitorRouteQualitySource adapts the channel-monitor matrix into the
// cache's snapshot type.
type channelMonitorRouteQualitySource struct {
	monitor *ChannelMonitorV2Service
}

func NewChannelMonitorRouteQualitySource(monitor *ChannelMonitorV2Service) GroupRouteQualitySource {
	return &channelMonitorRouteQualitySource{monitor: monitor}
}

func (s *channelMonitorRouteQualitySource) Snapshot(ctx context.Context) (map[int64]GroupRouteQuality, error) {
	out := map[int64]GroupRouteQuality{}
	if s == nil || s.monitor == nil {
		return out, nil
	}
	now := time.Now()
	filter := ChannelMonitorV2Filter{
		Start:  now.Add(-groupRouteQualityWindow),
		End:    now,
		Bucket: 5 * time.Minute,
	}
	matrix, err := s.monitor.Matrix(ctx, filter, ChannelMonitorV2GroupByPlatformGroup, true)
	if err != nil {
		return nil, err
	}
	if matrix == nil {
		return out, nil
	}
	for _, row := range matrix.Items {
		if row.GroupID == nil || *row.GroupID <= 0 {
			continue
		}
		quality := GroupRouteQuality{
			GroupID:     *row.GroupID,
			SuccessRate: row.Metrics.SuccessRate,
			CacheRate:   row.Metrics.CacheRate,
			Samples:     row.Metrics.RequestCount,
		}
		if row.Metrics.TTFT.P50Ms != nil {
			quality.TTFTMs = float64(*row.Metrics.TTFT.P50Ms)
		}
		out[quality.GroupID] = quality
	}
	return out, nil
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

// orderGroupIDsByQuality sorts candidates by score, highest first, keeping the
// original order for ties and unknown groups. It never removes a group.
func orderGroupIDsByQuality(scores map[int64]float64, groupIDs []int64) []int64 {
	if len(groupIDs) < 2 || len(scores) == 0 {
		return groupIDs
	}
	out := append([]int64(nil), groupIDs...)
	sort.SliceStable(out, func(i, j int) bool {
		return scores[out[i]] > scores[out[j]]
	})
	return out
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

// orderGroupsByQuality reorders smart-routing candidates by monitor quality.
// Unknown groups stay neutral, so the configured order is preserved until
// samples exist.
func (s *GatewayService) orderGroupsByQuality(ctx context.Context, groupIDs []int64) []int64 {
	return orderGroupIDsByQualityWithCache(s.groupRouteQualityCache(), ctx, groupIDs)
}

// OpenAIGatewayService routes OpenAI-compatible requests; it shares the same
// process-default quality cache.
func (s *OpenAIGatewayService) orderGroupsByQuality(ctx context.Context, groupIDs []int64) []int64 {
	return orderGroupIDsByQualityWithCache(DefaultGroupRouteQualityCache(), ctx, groupIDs)
}

func orderGroupIDsByQualityWithCache(cache *GroupRouteQualityCache, ctx context.Context, groupIDs []int64) []int64 {
	if cache == nil || len(groupIDs) < 2 {
		return groupIDs
	}
	return orderGroupIDsByQuality(cache.Scores(ctx, groupIDs), groupIDs)
}
