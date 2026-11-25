package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeRouteQualitySource struct {
	snapshot map[int64]GroupRouteQualitySnapshot
	err      error
	calls    int
}

func (f *fakeRouteQualitySource) Snapshot(context.Context) (map[int64]GroupRouteQualitySnapshot, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[int64]GroupRouteQualitySnapshot, len(f.snapshot))
	for id, snapshot := range f.snapshot {
		out[id] = snapshot
	}
	return out, nil
}

func routeInt64Ptr(v int64) *int64 { return &v }

func monitorRow(groupID int64, model string, requests int64, success, cache float64, ttftMs int64) ChannelMonitorV2MatrixRow {
	row := ChannelMonitorV2MatrixRow{Model: model, Metrics: ChannelMonitorV2Metric{
		RequestCount: requests, SuccessRate: success, CacheRate: cache,
	}}
	if groupID > 0 {
		row.GroupID = routeInt64Ptr(groupID)
	}
	if ttftMs > 0 {
		row.Metrics.TTFT.P50Ms = routeInt64Ptr(ttftMs)
	}
	return row
}

func TestAggregateRouteQualityRows(t *testing.T) {
	out := aggregateRouteQualityRows([]ChannelMonitorV2MatrixRow{
		monitorRow(1, "Claude-Opus-5", 300, 1.0, 0.9, 1000),
		monitorRow(1, "claude-sonnet-5", 100, 0.6, 0.5, 3000),
		monitorRow(2, "claude-opus-5", 50, 0.8, 0.0, 0), // no TTFT sample at all
		monitorRow(0, "claude-opus-5", 999, 0, 0, 1),    // not a group row
		monitorRow(3, "claude-opus-5", 0, 1, 1, 100),    // no requests
	})
	require.Len(t, out, 2, "rows without a group or without requests are ignored")

	g1 := out[1]
	require.Equal(t, int64(400), g1.Overall.Samples)
	require.InDelta(t, (1.0*300+0.6*100)/400, g1.Overall.SuccessRate, 1e-9, "request-weighted success rate")
	require.InDelta(t, (0.9*300+0.5*100)/400, g1.Overall.CacheRate, 1e-9)
	require.InDelta(t, (1000.0*300+3000.0*100)/400, g1.Overall.TTFTMs, 1e-9)
	require.Equal(t, int64(300), g1.ByModel["claude-opus-5"].Samples, "model names are normalized")
	require.Equal(t, int64(100), g1.ByModel["claude-sonnet-5"].Samples)

	g2 := out[2]
	require.Zero(t, g2.Overall.TTFTMs, "a group with no TTFT sample reports none, not zero-is-best")
	require.Equal(t, int64(50), g2.ByModel["claude-opus-5"].Samples)
}

func TestAggregateRouteQualityRowsKeepsTheBusierDuplicateModel(t *testing.T) {
	out := aggregateRouteQualityRows([]ChannelMonitorV2MatrixRow{
		monitorRow(1, "claude-opus-5", 30, 0.2, 0, 0),
		monitorRow(1, "claude-opus-5", 300, 0.9, 0, 0),
		monitorRow(1, "claude-opus-5", 40, 0.1, 0, 0),
	})
	require.InDelta(t, 0.9, out[1].ByModel["claude-opus-5"].SuccessRate, 1e-9)
}

func TestRouteQualitySnapshotLookup(t *testing.T) {
	snapshot := GroupRouteQualitySnapshot{
		Overall: GroupRouteQuality{Samples: 500, SuccessRate: 0.7},
		ByModel: map[string]GroupRouteQuality{
			"claude-opus-5":   {Samples: 100, SuccessRate: 0.95},
			"claude-sonnet-5": {Samples: groupRouteQualityMinSamples - 1, SuccessRate: 0.1},
		},
	}
	q, ok := snapshot.lookup("  Claude-Opus-5 ")
	require.True(t, ok)
	require.InDelta(t, 0.95, q.SuccessRate, 1e-9, "the model's own numbers win when there are enough samples")

	q, ok = snapshot.lookup("claude-sonnet-5")
	require.True(t, ok)
	require.InDelta(t, 0.7, q.SuccessRate, 1e-9, "too few samples for the model fall back to the group as a whole")

	_, ok = (GroupRouteQualitySnapshot{Overall: GroupRouteQuality{Samples: 3}}).lookup("claude-opus-5")
	require.False(t, ok, "not enough samples anywhere means unknown")
	_, ok = (GroupRouteQualitySnapshot{}).lookup("claude-opus-5")
	require.False(t, ok)
}

func TestGroupRouteQualityCachePublishesAndKeepsPreviousSnapshotOnError(t *testing.T) {
	source := &fakeRouteQualitySource{snapshot: map[int64]GroupRouteQualitySnapshot{
		1: {Overall: GroupRouteQuality{GroupID: 1, SuccessRate: 1, Samples: 100}},
	}}
	cache := NewGroupRouteQualityCache(source)
	require.NoError(t, cache.Refresh(context.Background()))
	require.Equal(t, int64(100), cache.Current(context.Background())[1].Overall.Samples)

	source.err = errors.New("monitor unavailable")
	require.Error(t, cache.Refresh(context.Background()))
	require.Equal(t, int64(100), cache.Current(context.Background())[1].Overall.Samples,
		"the last good snapshot must survive a failed refresh")
}

func TestGroupRouteQualityCacheDoesNotBlockOnStaleSnapshot(t *testing.T) {
	source := &fakeRouteQualitySource{snapshot: map[int64]GroupRouteQualitySnapshot{1: {}}}
	cache := NewGroupRouteQualityCache(source)
	cache.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	require.NoError(t, cache.Refresh(context.Background()))

	// Past the TTL, Current must return at once with the previous snapshot while the
	// refresh runs in the background.
	cache.now = func() time.Time { return time.Unix(1_700_000_000+int64(groupRouteQualityTTL.Seconds())+1, 0) }
	done := make(chan map[int64]GroupRouteQualitySnapshot, 1)
	go func() { done <- cache.Current(context.Background()) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Current blocked on a monitor refresh")
	}
}

func TestGroupRouteQualityCacheWithoutSourceHasNoSnapshot(t *testing.T) {
	var nilCache *GroupRouteQualityCache
	require.Nil(t, nilCache.Current(context.Background()))
	require.Nil(t, NewGroupRouteQualityCache(nil).Current(context.Background()))
}

func TestGatewayRouteQualityCachePrefersTheInstalledOne(t *testing.T) {
	previous := DefaultGroupRouteQualityCache()
	t.Cleanup(func() { SetDefaultGroupRouteQualityCache(previous) })
	fallback := NewGroupRouteQualityCache(&fakeRouteQualitySource{})
	SetDefaultGroupRouteQualityCache(fallback)

	svc := &GatewayService{}
	require.Same(t, fallback, svc.groupRouteQualityCache(), "without an installed cache the process default is used")
	installed := NewGroupRouteQualityCache(&fakeRouteQualitySource{})
	svc.SetRouteQualityCache(installed)
	require.Same(t, installed, svc.groupRouteQualityCache())
}
