// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestTrafficCalendarPeriods(t *testing.T) {
	// Keep global timezone untouched: other service tests can run concurrently.
	loc := timezone.Location()
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, loc) // Sunday
	for _, tc := range []struct{ period, date, start, end string }{
		{"day", "2024-02-29", "2024-02-29", "2024-03-01"},
		{"week", "", "2026-10-19", "2026-10-26"},
		{"month", "2024-02-29", "2024-02-01", "2024-03-01"},
		{"year", "2024-12-31", "2024-01-01", "2025-01-01"},
	} {
		f, err := NewTrafficFilter(tc.period, tc.date, now)
		require.NoError(t, err)
		require.Equal(t, tc.start, f.Start.Format("2006-01-02"))
		require.Equal(t, tc.end, f.End.Format("2006-01-02"))
	}
	_, err := NewTrafficFilter("invalid", "", now)
	require.Error(t, err)
	_, err = NewTrafficFilter("day", "2026-02-30", now)
	require.Error(t, err)
}

func TestTrafficCalendarDSTAndRepeatedHours(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	for _, tc := range []struct {
		date  string
		hours int
	}{{"2026-03-08", 23}, {"2026-11-01", 25}} {
		f, err := newTrafficFilter("day", tc.date, time.Now(), loc)
		require.NoError(t, err)
		require.Equal(t, time.Duration(tc.hours)*time.Hour, f.End.Sub(f.Start))
		buckets := TrafficBuckets(f)
		require.Len(t, buckets, tc.hours)
		seen := map[string]bool{}
		for _, b := range buckets {
			require.False(t, seen[b.Label])
			seen[b.Label] = true
		}
	}
}

func TestTrafficCalendarMidnightTransitionPreservesRequestedDate(t *testing.T) {
	for _, tc := range []struct{ zone, date, start, end string }{
		{"America/Santiago", "2026-09-06", "2026-09-06T01:00:00-03:00", "2026-09-07T00:00:00-03:00"},
		{"America/Havana", "2026-03-08", "2026-03-08T01:00:00-04:00", "2026-03-09T00:00:00-04:00"},
		{"Pacific/Apia", "2011-12-29", "2011-12-29T00:00:00-10:00", "2011-12-31T00:00:00+14:00"},
	} {
		t.Run(tc.zone, func(t *testing.T) {
			loc, err := time.LoadLocation(tc.zone)
			require.NoError(t, err)
			f, err := newTrafficFilter("day", tc.date, time.Now(), loc)
			require.NoError(t, err)
			require.Equal(t, tc.start, f.Start.Format(time.RFC3339))
			require.Equal(t, tc.end, f.End.Format(time.RFC3339))
		})
	}
}

func TestTrafficCalendarRejectsNonexistentDay(t *testing.T) {
	loc, err := time.LoadLocation("Pacific/Apia")
	require.NoError(t, err)
	_, err = newTrafficFilter("day", "2011-12-30", time.Now(), loc)
	require.Error(t, err)
}

func TestTrafficCalendarBucketsCrossSkippedDaysAndMidnights(t *testing.T) {
	for _, tc := range []struct {
		zone, date string
		days       int
	}{
		{"Pacific/Apia", "2011-12-01", 30},
		{"America/Santiago", "2026-09-01", 30},
		{"America/Havana", "2026-03-01", 31},
	} {
		t.Run(tc.zone, func(t *testing.T) {
			loc, err := time.LoadLocation(tc.zone)
			require.NoError(t, err)
			f, err := newTrafficFilter("month", tc.date, time.Now(), loc)
			require.NoError(t, err)
			buckets := TrafficBuckets(f)
			require.Len(t, buckets, tc.days)
			cursor := f.Start
			seen := map[string]bool{}
			for _, b := range buckets {
				require.Equal(t, cursor, b.Start)
				require.True(t, b.End.After(b.Start))
				require.Equal(t, b.Start.In(loc).Format("2006-01-02"), b.Label)
				require.False(t, seen[b.Label])
				seen[b.Label] = true
				cursor = b.End
			}
			require.Equal(t, f.End, cursor)
			if tc.zone == "Pacific/Apia" {
				require.False(t, seen["2011-12-30"])
			}
		})
	}
}

func TestTrafficCalendarUsesFirstMidnightWhenRepeated(t *testing.T) {
	loc, err := time.LoadLocation("America/Havana")
	require.NoError(t, err)
	f, err := newTrafficFilter("day", "2026-11-01", time.Now(), loc)
	require.NoError(t, err)
	require.Equal(t, "2026-11-01T00:00:00-04:00", f.Start.Format(time.RFC3339))
	require.Equal(t, 25*time.Hour, f.End.Sub(f.Start))
	require.Len(t, TrafficBuckets(f), 25)
}

func TestTrafficDeliverySurvivesDatabaseOutageAndRestart(t *testing.T) {
	dir := t.TempDir()
	d, err := NewTrafficDelivery(dir, func(context.Context, TrafficEvent) error { return errors.New("offline") })
	require.NoError(t, err)
	e := trafficDeliveryFixture()
	d.RecordTraffic(e)
	d.Stop()
	path := filepath.Join(dir, e.EventID+".json")
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	var mu sync.Mutex
	var received []TrafficEvent
	d, err = NewTrafficDelivery(dir, func(_ context.Context, e TrafficEvent) error {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, e)
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(d.Stop)
	require.Eventually(t, func() bool { _, err := os.Stat(path); return errors.Is(err, os.ErrNotExist) }, 3*time.Second, 10*time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, received, 1)
	require.Equal(t, e, received[0])
}

func TestTrafficDeliveryCorruptionRemainsIsolated(t *testing.T) {
	d := stoppedTrafficDelivery(t)
	bad := filepath.Join(d.dir, uuid.NewString()+".json")
	require.NoError(t, os.WriteFile(bad, []byte("truncated"), 0600))
	require.NoError(t, d.stage(trafficDeliveryFixture()))
	require.NoError(t, d.scanDisk(context.Background()))
	count := 0
	d.write = func(context.Context, TrafficEvent) error { count++; return nil }
	require.NoError(t, d.replay(context.Background()))
	require.Equal(t, 1, count)
	_, err := os.Stat(bad + ".invalid")
	require.NoError(t, err)
}
