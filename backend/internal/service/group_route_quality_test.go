package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeRouteQualitySource struct {
	snapshot map[int64]GroupRouteQuality
	err      error
	calls    int
}

func (f *fakeRouteQualitySource) Snapshot(context.Context) (map[int64]GroupRouteQuality, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[int64]GroupRouteQuality, len(f.snapshot))
	for id, quality := range f.snapshot {
		out[id] = quality
	}
	return out, nil
}

func TestScoreGroupRouteQuality(t *testing.T) {
	require.Equal(t, groupRouteQualityNeutralScore, scoreGroupRouteQuality(1, 1, 0, 5), "too few samples stay neutral")
	require.InDelta(t, 1.0, scoreGroupRouteQuality(1, 1, 0, 100), 1e-9)
	require.InDelta(t, 0.0, scoreGroupRouteQuality(0, 0, 20_000, 100), 1e-9)
	require.Greater(t,
		scoreGroupRouteQuality(0.99, 0.8, 500, 100),
		scoreGroupRouteQuality(0.80, 0.8, 500, 100),
		"higher success rate must score higher",
	)
	require.Greater(t,
		scoreGroupRouteQuality(0.9, 0.9, 500, 100),
		scoreGroupRouteQuality(0.9, 0.9, 4000, 100),
		"lower TTFT must score higher",
	)
}

func TestGroupRouteQualityCacheScoresAndNeutralDefault(t *testing.T) {
	source := &fakeRouteQualitySource{snapshot: map[int64]GroupRouteQuality{
		1: {SuccessRate: 1, CacheRate: 1, TTFTMs: 0, Samples: 100},
		2: {SuccessRate: 0, CacheRate: 0, TTFTMs: 10_000, Samples: 100},
	}}
	cache := NewGroupRouteQualityCache(source)
	require.NoError(t, cache.Refresh(context.Background()))

	scores := cache.Scores(context.Background(), []int64{1, 2, 3})
	require.InDelta(t, 1.0, scores[1], 1e-9)
	require.InDelta(t, 0.0, scores[2], 1e-9)
	require.InDelta(t, groupRouteQualityNeutralScore, scores[3], 1e-9)
}

func TestGroupRouteQualityCacheKeepsPreviousSnapshotOnError(t *testing.T) {
	source := &fakeRouteQualitySource{snapshot: map[int64]GroupRouteQuality{
		1: {SuccessRate: 1, CacheRate: 1, TTFTMs: 0, Samples: 100},
	}}
	cache := NewGroupRouteQualityCache(source)
	require.NoError(t, cache.Refresh(context.Background()))

	source.err = errors.New("monitor unavailable")
	require.Error(t, cache.Refresh(context.Background()))
	scores := cache.Scores(context.Background(), []int64{1})
	require.InDelta(t, 1.0, scores[1], 1e-9, "the last good snapshot must survive a failed refresh")
}

func TestGroupRouteQualityCacheDoesNotBlockOnStaleSnapshot(t *testing.T) {
	source := &fakeRouteQualitySource{snapshot: map[int64]GroupRouteQuality{1: {Samples: 0}}}
	cache := NewGroupRouteQualityCache(source)
	cache.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	require.NoError(t, cache.Refresh(context.Background()))

	// Advance past the TTL: Scores must return immediately with the previous
	// snapshot while the async refresh runs in the background.
	cache.now = func() time.Time { return time.Unix(1_700_000_000+int64(groupRouteQualityTTL.Seconds())+1, 0) }
	done := make(chan map[int64]float64, 1)
	go func() { done <- cache.Scores(context.Background(), []int64{1}) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Scores blocked on a network refresh")
	}
}

func TestOrderGroupIDsByQualityIsStableAndNeverDropsGroups(t *testing.T) {
	scores := map[int64]float64{1: 0.9, 2: 0.5, 3: 0.7, 4: 0.5}
	require.Equal(t, []int64{1, 3, 2, 4}, orderGroupIDsByQuality(scores, []int64{3, 1, 2, 4}))
	require.Equal(t, []int64{2, 4}, orderGroupIDsByQuality(scores, []int64{2, 4}), "ties keep the configured order")
	require.Equal(t, []int64{9}, orderGroupIDsByQuality(scores, []int64{9}), "a single group is returned as-is")
	require.Equal(t, []int64{5, 6}, orderGroupIDsByQuality(map[int64]float64{}, []int64{5, 6}), "no scores means no reorder")
}

func TestGatewayOrderGroupsByQualityWithoutCacheKeepsOrder(t *testing.T) {
	svc := &GatewayService{}
	groupIDs := []int64{3, 1, 2}
	require.Equal(t, groupIDs, svc.orderGroupsByQuality(context.Background(), groupIDs))
}

func TestGatewayOrderGroupsByQualityUsesInstalledCache(t *testing.T) {
	source := &fakeRouteQualitySource{snapshot: map[int64]GroupRouteQuality{
		1: {SuccessRate: 1, CacheRate: 1, TTFTMs: 0, Samples: 100},
		2: {SuccessRate: 0.1, CacheRate: 0.1, TTFTMs: 9_000, Samples: 100},
	}}
	cache := NewGroupRouteQualityCache(source)
	require.NoError(t, cache.Refresh(context.Background()))
	svc := &GatewayService{}
	svc.SetRouteQualityCache(cache)
	require.Equal(t, []int64{1, 3, 2}, svc.orderGroupsByQuality(context.Background(), []int64{2, 3, 1}),
		"unknown group 3 stays neutral and outranks the low-scoring group 2")
}
