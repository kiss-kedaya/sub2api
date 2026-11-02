package service

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestSelectGroupRateChangesEmptyInput(t *testing.T) {
	require.Empty(t, SelectGroupRateChanges(nil, time.Now(), RateChangeFlapWindow))
	require.Empty(t, SelectGroupRateChanges([]usagestats.GroupRateBucket{}, time.Now(), RateChangeFlapWindow))
}

func TestSelectGroupRateChangesNeedsTwoDistinctValues(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	buckets := []usagestats.GroupRateBucket{{
		GroupID:        3,
		RateMultiplier: 0.09,
		Requests:       40,
		FirstSeenAt:    now.Add(-20 * time.Hour),
		LastSeenAt:     now.Add(-10 * time.Minute),
	}}
	require.Empty(t, SelectGroupRateChanges(buckets, now, RateChangeFlapWindow))
}

func TestSelectGroupRateChangesReportsDrop(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	buckets := []usagestats.GroupRateBucket{
		{GroupID: 3, RateMultiplier: 0.12, Requests: 8, FirstSeenAt: now.Add(-30 * time.Hour), LastSeenAt: now.Add(-3 * time.Hour)},
		{GroupID: 3, RateMultiplier: 0.09, Requests: 42, FirstSeenAt: now.Add(-2 * time.Hour), LastSeenAt: now.Add(-5 * time.Minute)},
	}

	changes := SelectGroupRateChanges(buckets, now, RateChangeFlapWindow)
	require.Len(t, changes, 1)
	require.Equal(t, int64(3), changes[0].GroupID)
	require.Equal(t, 0.09, changes[0].CurrentRateMultiplier)
	require.Equal(t, 0.12, changes[0].PreviousRateMultiplier)
	require.Equal(t, usagestats.RateDirectionDown, changes[0].Direction)
	require.Equal(t, now.Add(-2*time.Hour), changes[0].ChangedAt)
	require.Equal(t, int64(42), changes[0].Requests)
}

func TestSelectGroupRateChangesReportsRise(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	buckets := []usagestats.GroupRateBucket{
		{GroupID: 9, RateMultiplier: 0.05, Requests: 5, FirstSeenAt: now.Add(-40 * time.Hour), LastSeenAt: now.Add(-4 * time.Hour)},
		{GroupID: 9, RateMultiplier: 0.08, Requests: 7, FirstSeenAt: now.Add(-3 * time.Hour), LastSeenAt: now.Add(-5 * time.Minute)},
	}

	changes := SelectGroupRateChanges(buckets, now, RateChangeFlapWindow)
	require.Len(t, changes, 1)
	require.Equal(t, usagestats.RateDirectionUp, changes[0].Direction)
	require.Equal(t, 0.08, changes[0].CurrentRateMultiplier)
	require.Equal(t, 0.05, changes[0].PreviousRateMultiplier)
}

// Mixed billing modes (image vs token) can alternate between two multipliers.
// A recently observed older value means the data is oscillating, not a price move.
func TestSelectGroupRateChangesSuppressesFlapping(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	buckets := []usagestats.GroupRateBucket{
		{GroupID: 3, RateMultiplier: 0.12, Requests: 8, FirstSeenAt: now.Add(-2 * time.Hour), LastSeenAt: now.Add(-10 * time.Minute)},
		{GroupID: 3, RateMultiplier: 0.09, Requests: 42, FirstSeenAt: now.Add(-90 * time.Minute), LastSeenAt: now.Add(-5 * time.Minute)},
	}
	require.Empty(t, SelectGroupRateChanges(buckets, now, RateChangeFlapWindow))
}

// If the older value was still seen after the newer one started we cannot
// order them, so the group must be skipped instead of guessing.
func TestSelectGroupRateChangesSkipsOverlappingBuckets(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	buckets := []usagestats.GroupRateBucket{
		{GroupID: 3, RateMultiplier: 0.12, Requests: 8, FirstSeenAt: now.Add(-5 * time.Hour), LastSeenAt: now.Add(-70 * time.Minute)},
		{GroupID: 3, RateMultiplier: 0.09, Requests: 42, FirstSeenAt: now.Add(-90 * time.Minute), LastSeenAt: now.Add(-5 * time.Minute)},
	}
	require.Empty(t, SelectGroupRateChanges(buckets, now, RateChangeFlapWindow))
}

func TestSelectGroupRateChangesIsDeterministicallyOrdered(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	buckets := []usagestats.GroupRateBucket{
		{GroupID: 9, RateMultiplier: 0.5, Requests: 1, FirstSeenAt: now.Add(-20 * time.Hour), LastSeenAt: now.Add(-4 * time.Hour)},
		{GroupID: 9, RateMultiplier: 0.2, Requests: 2, FirstSeenAt: now.Add(-3 * time.Hour), LastSeenAt: now.Add(-time.Minute)},
		{GroupID: 3, RateMultiplier: 0.5, Requests: 1, FirstSeenAt: now.Add(-20 * time.Hour), LastSeenAt: now.Add(-4 * time.Hour)},
		{GroupID: 3, RateMultiplier: 0.7, Requests: 2, FirstSeenAt: now.Add(-3 * time.Hour), LastSeenAt: now.Add(-time.Minute)},
	}

	changes := SelectGroupRateChanges(buckets, now, RateChangeFlapWindow)
	require.Len(t, changes, 2)
	require.Equal(t, int64(3), changes[0].GroupID)
	require.Equal(t, int64(9), changes[1].GroupID)
}
