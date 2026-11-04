package cryptopay

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSampleRingKeepsTheNewestAndResets(t *testing.T) {
	var r sampleRing
	base := time.Unix(1_700_000_000, 0)
	for i := 0; i < chainSampleCapacity+10; i++ {
		r.push(chainSample{At: base.Add(time.Duration(i) * time.Second), Cursor: uint64(i)})
	}
	got := r.since(time.Time{})
	require.Len(t, got, chainSampleCapacity, "the ring is bounded")
	require.Equal(t, uint64(10), got[0].Cursor, "the oldest ten were overwritten")
	require.Equal(t, uint64(chainSampleCapacity+9), got[len(got)-1].Cursor)
	for i := 1; i < len(got); i++ {
		require.Less(t, got[i-1].At, got[i].At, "oldest first")
	}
	last, ok := r.last()
	require.True(t, ok)
	require.Equal(t, uint64(chainSampleCapacity+9), last.Cursor)

	r.reset()
	_, ok = r.last()
	require.False(t, ok)
	require.Empty(t, r.since(time.Time{}))
}

func TestSampleRingSinceIncludesTheCutoffInstant(t *testing.T) {
	var r sampleRing
	base := time.Unix(1_700_000_000, 0)
	for i := 0; i < 4; i++ {
		r.push(chainSample{At: base.Add(time.Duration(i) * time.Minute), Cursor: uint64(i)})
	}
	got := r.since(base.Add(time.Minute))
	require.Len(t, got, 3)
	require.Equal(t, uint64(1), got[0].Cursor, "a sample exactly at the cutoff is kept")
}

func TestRecordSpacingRewindAndRetention(t *testing.T) {
	m, clock, _ := newTestMonitor(t, nil)
	m.record("bsc", 100, 1000)
	clock.Advance(4 * time.Second)
	m.record("bsc", 150, 1010)
	require.Len(t, m.window("bsc"), 1, "closer than the minimum spacing is dropped")

	clock.Advance(time.Second) // exactly chainSampleMinSpacing after the first
	m.record("bsc", 150, 1010)
	require.Len(t, m.window("bsc"), 2)

	clock.Advance(chainSampleMinSpacing)
	m.record("bsc", 150, 1020) // an unchanged cursor is not a rewind
	require.Len(t, m.window("bsc"), 3)

	m.record("ethereum", 5, 50)
	require.Len(t, m.window("ethereum"), 1, "chains do not share a window")

	clock.Advance(chainSampleMinSpacing)
	m.record("bsc", 90, 1030) // an operator rewound the cursor for a rescan
	got := m.window("bsc")
	require.Len(t, got, 1, "the old baseline is meaningless after a rewind")
	require.Equal(t, uint64(90), got[0].Cursor)

	clock.Advance(-10 * time.Second) // the clock went backwards
	m.record("bsc", 95, 1040)
	got = m.window("bsc")
	require.Len(t, got, 1)
	require.Equal(t, uint64(95), got[0].Cursor)
}

func TestWindowRetentionBoundary(t *testing.T) {
	m, clock, _ := newTestMonitor(t, nil)
	m.record("bsc", 1, 1)
	clock.Advance(chainSampleRetention)
	require.Len(t, m.window("bsc"), 1, "a sample exactly 15 minutes old is still inside the window")
	clock.Advance(time.Nanosecond)
	require.Empty(t, m.window("bsc"))
	require.Empty(t, m.window("never-sampled"))
}

func TestCursorMissingForRestartsWhenTheRowAppears(t *testing.T) {
	m, clock, _ := newTestMonitor(t, nil)
	require.Zero(t, m.cursorMissingFor("bsc", false), "the first sighting starts the clock")
	clock.Advance(90 * time.Second)
	require.Equal(t, 90*time.Second, m.cursorMissingFor("bsc", false))
	require.Zero(t, m.cursorMissingFor("bsc", true), "a present row clears it")
	clock.Advance(10 * time.Second)
	require.Zero(t, m.cursorMissingFor("bsc", false), "and the clock restarts from the next missing sighting")
	clock.Advance(5 * time.Second)
	require.Equal(t, 5*time.Second, m.cursorMissingFor("bsc", false))
	require.Zero(t, m.cursorMissingFor("ethereum", false), "chains are tracked separately")
}
