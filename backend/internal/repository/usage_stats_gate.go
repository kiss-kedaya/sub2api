package repository

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const (
	// usageStatsGateLogEvery spaces the "shedding load" log line: during a pile-up every queued
	// aggregate times out, and one line per request would bury the log.
	usageStatsGateLogEvery = 10 * time.Second

	// usageStatsBusyRetryAfterSeconds is what a turned-away client is told to wait before asking again
	// (Retry-After). It is longer than one healthy aggregate and short enough for a page to feel alive.
	usageStatsBusyRetryAfterSeconds = 2

	// One owner (a user, or an API key when the aggregate is key-scoped) may hold at most
	// max(usageStatsOwnerMinSlots, slots/usageStatsOwnerShare) database slots at once, so a single
	// client re-asking with a different date range every time can never take all of them.
	usageStatsOwnerMinSlots = 2
	usageStatsOwnerShare    = 4
	// usageStatsOwnerBacklog is how many times its slot share one owner may have inside the gate (running, or
	// waiting for one of its own turns) before further misses are turned away at once. The usage page itself
	// fires about six aggregates on a cold load; this leaves room for that, and for a second tab.
	usageStatsOwnerBacklog = 4
)

// ErrUsageStatsBusy is what an aggregate gets when it could not get a database slot in time. It is a 429
// with Retry-After, not a 503: nothing is broken, the caller is simply asked to come back in a moment, and
// that must neither look like a gateway failure to the 5xx counters nor be logged as an error.
var ErrUsageStatsBusy = infraerrors.TooManyRequests("USAGE_STATS_BUSY", "Usage statistics are busy, please try again in a few seconds").
	WithMetadata(map[string]string{"retry_after": strconv.Itoa(usageStatsBusyRetryAfterSeconds)})

// usageStatsGate caps how many cache-miss aggregates one process runs on the database at once.
// The scans behind the usage page are IO-bound: past a handful of them the extra ones only make
// every other query (and the usage_logs writers) slower, so the surplus waits for a slot, and
// gives up with ErrUsageStatsBusy after queueTimeout instead of piling more connections on.
//
// On top of the shared slots every owner has a share of its own (see usageStatsOwnerMinSlots): the
// aggregates of one owner beyond that share wait for the owner's own earlier ones, in front of the shared
// queue, so however hard one client pushes, the other clients keep their slots and their place in line.
// A nil gate means "no cap".
type usageStatsGate struct {
	slots        chan struct{}
	queueTimeout time.Duration
	ownerSlots   int
	waiting      atomic.Int64
	lastLog      atomic.Int64 // Unix nanos of the last log line

	mu     sync.Mutex
	owners map[string]*usageStatsOwner // only owners that have an aggregate inside the gate
}

// usageStatsOwner is one owner's share of the gate.
type usageStatsOwner struct {
	turns   chan struct{} // capacity = the owner's share: holding one entitles to compete for a shared slot
	pending int           // aggregates of this owner inside the gate; guarded by usageStatsGate.mu
}

// newUsageStatsGate returns nil (no cap) when slots <= 0. A queueTimeout <= 0 means "never wait":
// a miss that finds every slot taken is turned away at once.
func newUsageStatsGate(slots int, queueTimeout time.Duration) *usageStatsGate {
	if slots <= 0 {
		return nil
	}
	return &usageStatsGate{
		slots:        make(chan struct{}, slots),
		queueTimeout: queueTimeout,
		ownerSlots:   max(usageStatsOwnerMinSlots, slots/usageStatsOwnerShare),
		owners:       make(map[string]*usageStatsOwner),
	}
}

// acquire takes a slot for one aggregate of owner and returns the function that gives it back (call it
// exactly once; extra calls are ignored). An empty owner has no share of its own and only competes for
// the shared slots. The whole wait, for the owner's turn and for a shared slot together, is bounded by
// queueTimeout and by ctx.
func (g *usageStatsGate) acquire(ctx context.Context, owner string) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	deadline := time.Now().Add(g.queueTimeout)
	leaveOwner, err := g.enter(ctx, owner, deadline)
	if err != nil {
		return nil, err
	}
	releaseSlot, err := g.acquireSlot(ctx, time.Until(deadline), owner)
	if err != nil {
		leaveOwner()
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			releaseSlot()
			leaveOwner()
		})
	}, nil
}

// enter admits one aggregate of owner and returns the function that undoes it. It is turned away at once
// when the owner already has usageStatsOwnerBacklog times its share inside the gate (a flood, not a page),
// and otherwise waits, at most until deadline, for one of the owner's own turns.
func (g *usageStatsGate) enter(ctx context.Context, owner string, deadline time.Time) (func(), error) {
	if owner == "" || g.ownerSlots <= 0 {
		return func() {}, nil
	}
	g.mu.Lock()
	o := g.owners[owner]
	if o == nil {
		o = &usageStatsOwner{turns: make(chan struct{}, g.ownerSlots)}
		if g.owners == nil {
			g.owners = make(map[string]*usageStatsOwner)
		}
		g.owners[owner] = o
	}
	if o.pending >= g.ownerSlots*usageStatsOwnerBacklog {
		g.mu.Unlock()
		return nil, g.shed(owner)
	}
	o.pending++
	g.mu.Unlock()

	// Hands the turn (when held) and the backlog count back. The entry is dropped with its last aggregate,
	// so the map only ever holds owners that are in the gate right now.
	leave := func(holdsTurn bool) {
		if holdsTurn {
			<-o.turns
		}
		g.mu.Lock()
		o.pending--
		if o.pending == 0 {
			delete(g.owners, owner)
		}
		g.mu.Unlock()
	}

	select {
	case o.turns <- struct{}{}:
		return func() { leave(true) }, nil
	default:
	}
	wait := time.Until(deadline)
	if wait <= 0 {
		leave(false)
		return nil, g.shed(owner)
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case o.turns <- struct{}{}:
		return func() { leave(true) }, nil
	case <-timer.C:
		leave(false)
		return nil, g.shed(owner)
	case <-ctx.Done():
		leave(false)
		return nil, ctx.Err()
	}
}

// acquireSlot takes one shared slot, waiting at most wait for it (wait <= 0 means "never wait").
func (g *usageStatsGate) acquireSlot(ctx context.Context, wait time.Duration, owner string) (func(), error) {
	select {
	case g.slots <- struct{}{}:
		return g.releaseOnce(), nil
	default:
	}
	if wait <= 0 {
		return nil, g.shed(owner)
	}
	g.waiting.Add(1)
	defer g.waiting.Add(-1)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case g.slots <- struct{}{}:
		return g.releaseOnce(), nil
	case <-timer.C:
		return nil, g.shed(owner)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (g *usageStatsGate) releaseOnce() func() {
	var once sync.Once
	return func() { once.Do(func() { <-g.slots }) }
}

// shed turns one aggregate away. Being busy is expected under load, so this is an Info line at most once
// per usageStatsGateLogEvery, never an error.
func (g *usageStatsGate) shed(owner string) error {
	now := time.Now().UnixNano()
	if last := g.lastLog.Load(); now-last >= int64(usageStatsGateLogEvery) && g.lastLog.CompareAndSwap(last, now) {
		logger.L().Info("usage stats busy, turning aggregate away",
			zap.String("owner", owner), zap.Int("in_flight", len(g.slots)), zap.Int64("waiting", g.waiting.Load()),
			zap.Duration("queue_timeout", g.queueTimeout))
	}
	return ErrUsageStatsBusy
}
