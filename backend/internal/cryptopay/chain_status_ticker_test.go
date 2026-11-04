package cryptopay

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func waitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("the sampling loop did not run its tick")
	}
}

func isRunning(m *chainMonitor) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

func TestTickerStartsOnceStopsWhenNobodyIsWatching(t *testing.T) {
	m, clock, tick := newTestMonitor(t, nil)
	fired := make(chan struct{}, 8)
	body := func(context.Context) { fired <- struct{}{} }

	for i := 0; i < 5; i++ {
		m.touch(body)
	}
	require.Equal(t, 1, tick.Created(), "five page polls share one sampling loop")

	tick.Fire(0)
	waitSignal(t, fired)

	clock.Advance(chainWatchWindow) // exactly 10 minutes: still being watched
	tick.Fire(0)
	waitSignal(t, fired)
	require.True(t, isRunning(m))

	clock.Advance(time.Nanosecond) // one tick past the window: nobody is looking
	tick.Fire(0)
	require.Eventually(t, func() bool { return !isRunning(m) }, 3*time.Second, 5*time.Millisecond)
	require.Eventually(t, func() bool { return tick.Stopped() == 1 }, 3*time.Second, 5*time.Millisecond,
		"the idle loop releases its ticker")
	require.Empty(t, fired, "an idle tick samples nothing and spends no RPC quota")

	m.touch(body) // somebody opens the page again
	require.Equal(t, 2, tick.Created())
	tick.Fire(1)
	waitSignal(t, fired)
}

func TestTouchRefreshesTheIdleClock(t *testing.T) {
	m, clock, tick := newTestMonitor(t, nil)
	fired := make(chan struct{}, 8)
	body := func(context.Context) { fired <- struct{}{} }
	m.touch(body)
	clock.Advance(9 * time.Minute)
	m.touch(body)
	clock.Advance(9 * time.Minute) // 18 minutes after the first touch, 9 after the last
	tick.Fire(0)
	waitSignal(t, fired)
	require.True(t, isRunning(m))
	require.Equal(t, 1, tick.Created())
}

// A loop that is already shutting down must never clear the state of the loop
// that replaced it, or the next page poll would start a duplicate.
func TestStaleLoopCannotClearItsSuccessor(t *testing.T) {
	m, clock, tick := newTestMonitor(t, nil)
	body := func(context.Context) {}
	m.touch(body) // generation 1
	m.stop()
	m.touch(body) // generation 2
	require.Equal(t, 2, tick.Created())

	clock.Advance(chainWatchWindow + time.Hour) // far past idle for the stale loop
	stale := make(chan time.Time, 1)
	stale <- clock.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.tickLoop(context.Background(), 1, stale, func() {}, body)
	}()
	waitSignal(t, done)

	require.True(t, isRunning(m), "generation 2 is still the live loop")
	m.mu.Lock()
	gen, hasCancel := m.loopGen, m.cancel != nil
	m.mu.Unlock()
	require.Equal(t, uint64(2), gen)
	require.True(t, hasCancel)
}

func TestBackgroundTickSamplesWithoutCountingAsPageActivity(t *testing.T) {
	a := "https://rpc.ankr.com/bsc/SECRETKEY"
	cfg, pools := bscOnly(a)
	node := newFakeNode(t, fakeNodeOpts{chainID: 56, head: 10_003})
	h := newStatusHarness(t, cfg, pools, map[string]*fakeNode{a: node})
	h.expectCursors(cursorRow{ChainBSC, 9_000, h.clock.Now()})
	h.expectPending(nil)
	_, err := h.svc.ChainStatus(context.Background())
	require.NoError(t, err)
	m := h.svc.monitor()
	m.mu.Lock()
	seenAtPoll := m.lastSeen
	m.mu.Unlock()
	require.Len(t, m.window(ChainBSC), 1)

	h.clock.Advance(15 * time.Second)
	node.head.Store(10_023)
	h.expectCursors(cursorRow{ChainBSC, 9_500, h.clock.Now()})
	h.expectPending(nil)
	h.tick.Fire(0) // the 15 s ticker, with no page open

	require.Eventually(t, func() bool { return len(m.window(ChainBSC)) == 2 }, 3*time.Second, 5*time.Millisecond,
		"the tick probed the chain and recorded a sample")
	m.mu.Lock()
	seenAfterTick := m.lastSeen
	m.mu.Unlock()
	require.True(t, seenAtPoll.Equal(seenAfterTick), "only a page poll keeps the loop alive")
	require.NoError(t, h.mock.ExpectationsWereMet())
}
