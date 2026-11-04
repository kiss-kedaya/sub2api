package cryptopay

import (
	"context"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"golang.org/x/sync/singleflight"
)

type chainProbeEntry struct {
	signature string
	at        time.Time
	probes    []nodeProbe
}

// chainMonitor holds the in-process state behind the admin node-status page:
// the probe cache, the sample windows and the on-demand ticker. The zero value
// is usable; the first call initialises it. Everything here is read-only with
// respect to the database and the scanner.
type chainMonitor struct {
	once sync.Once

	mu      sync.Mutex
	rings   map[string]*sampleRing
	missing map[string]time.Time
	cache   map[string]chainProbeEntry
	flights singleflight.Group

	// Ticker state, guarded by mu. loopGen identifies the live loop so a loop
	// that is already shutting down can never clear the state of its successor.
	lastSeen time.Time
	running  bool
	loopGen  uint64
	cancel   context.CancelFunc

	// Seams. Tests set them before first use; init only fills the gaps.
	now          func() time.Time
	newPool      func(endpoint string) *rpc.Pool
	newTicker    func(time.Duration) (<-chan time.Time, func())
	probeTimeout time.Duration
	slowAfter    time.Duration
	cacheTTL     time.Duration
	tickEvery    time.Duration
	watchFor     time.Duration
}

func (m *chainMonitor) init() {
	m.once.Do(func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.rings = map[string]*sampleRing{}
		m.missing = map[string]time.Time{}
		m.cache = map[string]chainProbeEntry{}
		if m.now == nil {
			m.now = time.Now
		}
		if m.newPool == nil {
			// rpc.NewPool keeps the public-address checks and the per-endpoint
			// throttle that scanner traffic already goes through.
			m.newPool = rpc.NewPool
		}
		if m.newTicker == nil {
			m.newTicker = realTicker
		}
		if m.probeTimeout <= 0 {
			m.probeTimeout = nodeProbeTimeout
		}
		if m.slowAfter <= 0 {
			m.slowAfter = nodeSlowAfter
		}
		if m.cacheTTL <= 0 {
			m.cacheTTL = chainProbeCacheTTL
		}
		if m.tickEvery <= 0 {
			m.tickEvery = chainTickInterval
		}
		if m.watchFor <= 0 {
			m.watchFor = chainWatchWindow
		}
	})
}

func realTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// touch records that someone is looking at the page and makes sure the sampling
// loop is running. With nobody looking for watchFor the loop ends by itself, so
// a page nobody opens costs no RPC quota.
func (m *chainMonitor) touch(tick func(context.Context)) {
	m.init()
	now := m.now()
	m.mu.Lock()
	m.lastSeen = now
	if m.running {
		m.mu.Unlock()
		return
	}
	m.running = true
	m.loopGen++
	gen := m.loopGen
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.mu.Unlock()
	ticks, stopTicker := m.newTicker(m.tickEvery)
	go m.tickLoop(ctx, gen, ticks, stopTicker, tick)
}

func (m *chainMonitor) tickLoop(ctx context.Context, gen uint64, ticks <-chan time.Time, stopTicker func(), tick func(context.Context)) {
	defer stopTicker()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		if m.now().Sub(m.lastSeen) > m.watchFor {
			if m.loopGen == gen {
				m.running = false
				if m.cancel != nil {
					m.cancel()
					m.cancel = nil
				}
			}
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
		tickCtx, cancel := context.WithTimeout(ctx, chainTickBudget)
		tick(tickCtx)
		cancel()
	}
}

// stop ends the sampling loop. Production never needs it (the loop ends on its
// own when idle); tests use it so no goroutine outlives them.
func (m *chainMonitor) stop() {
	m.init()
	m.mu.Lock()
	cancel := m.cancel
	m.cancel = nil
	m.running = false
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
