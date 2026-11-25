package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// usageStatsGateWarnEvery spaces the "shedding load" log line: during a pile-up every queued
// aggregate times out, and one line per request would bury the log.
const usageStatsGateWarnEvery = 10 * time.Second

// ErrUsageStatsBusy is what an aggregate gets when every database slot stayed taken for the whole
// queue wait. It is a plain 503: asking the caller to retry in a few seconds is cheaper for
// everyone than starting one more heavy scan on a database that is already saturated.
var ErrUsageStatsBusy = infraerrors.ServiceUnavailable("USAGE_STATS_BUSY", "Usage statistics are busy, please try again in a few seconds")

// usageStatsGate caps how many cache-miss aggregates one process runs on the database at once.
// The scans behind the usage page are IO-bound: past a handful of them the extra ones only make
// every other query (and the usage_logs writers) slower, so the surplus waits for a slot, and
// gives up with ErrUsageStatsBusy after queueTimeout instead of piling more connections on.
// A nil gate means "no cap".
type usageStatsGate struct {
	slots        chan struct{}
	queueTimeout time.Duration
	waiting      atomic.Int64
	lastWarn     atomic.Int64 // Unix nanos of the last log line
}

// newUsageStatsGate returns nil (no cap) when slots <= 0. A queueTimeout <= 0 means "never wait":
// a miss that finds every slot taken is turned away at once.
func newUsageStatsGate(slots int, queueTimeout time.Duration) *usageStatsGate {
	if slots <= 0 {
		return nil
	}
	return &usageStatsGate{slots: make(chan struct{}, slots), queueTimeout: queueTimeout}
}

// acquire takes a slot and returns the function that gives it back (call it exactly once; extra
// calls are ignored). The wait is bounded by queueTimeout and by ctx.
func (g *usageStatsGate) acquire(ctx context.Context) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	select {
	case g.slots <- struct{}{}:
		return g.releaseOnce(), nil
	default:
	}
	if g.queueTimeout <= 0 {
		return nil, g.shed()
	}
	g.waiting.Add(1)
	defer g.waiting.Add(-1)
	timer := time.NewTimer(g.queueTimeout)
	defer timer.Stop()
	select {
	case g.slots <- struct{}{}:
		return g.releaseOnce(), nil
	case <-timer.C:
		return nil, g.shed()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (g *usageStatsGate) releaseOnce() func() {
	var once sync.Once
	return func() { once.Do(func() { <-g.slots }) }
}

func (g *usageStatsGate) shed() error {
	now := time.Now().UnixNano()
	if last := g.lastWarn.Load(); now-last >= int64(usageStatsGateWarnEvery) && g.lastWarn.CompareAndSwap(last, now) {
		logger.L().Warn("usage stats busy, turning aggregate away",
			zap.Int("in_flight", len(g.slots)), zap.Int64("waiting", g.waiting.Load()), zap.Duration("queue_timeout", g.queueTimeout))
	}
	return ErrUsageStatsBusy
}
