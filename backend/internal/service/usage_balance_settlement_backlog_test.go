//go:build unit

package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/stretchr/testify/require"
)

// guardTestRepo is a settlement repository that also reads the backlog; the test
// scripts what it reports and what a flush returns.
type guardTestRepo struct {
	mu       sync.Mutex
	backlog  UsageBalanceSettlementBacklog
	readErr  error
	results  []UsageBalanceSettlementResult
	flushErr error

	backlogCalls atomic.Int64
	lastCap      atomic.Int64
}

func (r *guardTestRepo) setBacklog(b UsageBalanceSettlementBacklog) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backlog = b
	r.readErr = nil
}

func (r *guardTestRepo) setReadError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.readErr = err
}

func (r *guardTestRepo) FlushPendingBalanceSettlements(context.Context, int) ([]UsageBalanceSettlementResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.results, r.flushErr
}

func (r *guardTestRepo) DeleteAppliedBalanceSettlements(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func (r *guardTestRepo) BalanceSettlementBacklog(_ context.Context, depthCap int64) (UsageBalanceSettlementBacklog, error) {
	r.backlogCalls.Add(1)
	r.lastCap.Store(depthCap)
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.backlog, r.readErr
}

// resetSettlementProcessState clears the process-wide flags the guard flips so one
// test never leaks a fallback / notifier state into the next.
func resetSettlementProcessState(t *testing.T) {
	t.Helper()
	reset := func() {
		usageBalanceSettlementConsumerRunning.Store(false)
		usageBalanceSettlementBacklogFallback.Store(false)
		usageBalanceSettlementNotifierActive.Store(false)
		usageBalanceSettlementFallbackCharges.Store(0)
	}
	reset()
	t.Cleanup(reset)
}

// newGuardWorker builds a worker that is never started: tests drive applyBacklog /
// sampleBacklog / logStats synchronously.
func newGuardWorker(repo UsageBalanceSettlementRepository, maxBacklog int64, maxAge time.Duration) *UsageBalanceSettlementWorker {
	return &UsageBalanceSettlementWorker{
		repo: repo,
		opts: UsageBalanceSettlementWorkerOptions{MergeEnabled: true, MaxBacklog: maxBacklog, MaxAge: maxAge},
	}
}

type settlementLogSink struct {
	mu     sync.Mutex
	events []*logger.LogEvent
}

func (s *settlementLogSink) WriteLogEvent(event *logger.LogEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *settlementLogSink) named(message string) []*logger.LogEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*logger.LogEvent
	for _, event := range s.events {
		if event.Message == message {
			out = append(out, event)
		}
	}
	return out
}

var settlementLogCaptureMu sync.Mutex

// captureSettlementLogs routes structured logs into an in-memory sink for the test.
func captureSettlementLogs(t *testing.T) *settlementLogSink {
	t.Helper()
	settlementLogCaptureMu.Lock()
	require.NoError(t, logger.Init(logger.InitOptions{
		Level:       "info",
		Format:      "json",
		ServiceName: "sub2api",
		Environment: "test",
		Output:      logger.OutputOptions{ToStdout: true},
		Sampling:    logger.SamplingOptions{Enabled: false},
	}))
	sink := &settlementLogSink{}
	logger.SetSink(sink)
	t.Cleanup(func() {
		logger.SetSink(nil)
		settlementLogCaptureMu.Unlock()
	})
	return sink
}

func TestSettlementGuard_NextStateTable(t *testing.T) {
	const (
		maxBacklog = int64(100)
		maxAge     = 10 * time.Second
	)
	tests := []struct {
		name       string
		active     bool
		backlog    UsageBalanceSettlementBacklog
		maxBacklog int64
		maxAge     time.Duration
		want       bool
	}{
		{"calm queue stays on the queue", false, UsageBalanceSettlementBacklog{Pending: 10, OldestAge: time.Second}, maxBacklog, maxAge, false},
		{"depth equal to the limit is not over it", false, UsageBalanceSettlementBacklog{Pending: 100}, maxBacklog, maxAge, false},
		{"depth over the limit falls back", false, UsageBalanceSettlementBacklog{Pending: 101}, maxBacklog, maxAge, true},
		{"age equal to the limit is not over it", false, UsageBalanceSettlementBacklog{OldestAge: 10 * time.Second}, maxBacklog, maxAge, false},
		{"age over the limit falls back even with an empty-ish queue", false, UsageBalanceSettlementBacklog{Pending: 1, OldestAge: 10*time.Second + time.Millisecond}, maxBacklog, maxAge, true},
		{"capped depth reading falls back", false, UsageBalanceSettlementBacklog{Pending: 20000, PendingCapped: true}, maxBacklog, maxAge, true},

		{"hysteresis: between half and the limit stays in fallback", true, UsageBalanceSettlementBacklog{Pending: 99}, maxBacklog, maxAge, true},
		{"hysteresis: exactly half stays in fallback", true, UsageBalanceSettlementBacklog{Pending: 50}, maxBacklog, maxAge, true},
		{"just below half recovers", true, UsageBalanceSettlementBacklog{Pending: 49}, maxBacklog, maxAge, false},
		{"age keeps fallback active while depth is calm", true, UsageBalanceSettlementBacklog{Pending: 0, OldestAge: 5 * time.Second}, maxBacklog, maxAge, true},
		{"age just below half recovers", true, UsageBalanceSettlementBacklog{Pending: 0, OldestAge: 5*time.Second - time.Millisecond}, maxBacklog, maxAge, false},
		{"depth keeps fallback active while age is calm", true, UsageBalanceSettlementBacklog{Pending: 50, OldestAge: 0}, maxBacklog, maxAge, true},

		{"depth trigger disabled ignores depth", false, UsageBalanceSettlementBacklog{Pending: 1_000_000_000}, 0, maxAge, false},
		{"age trigger disabled ignores age", false, UsageBalanceSettlementBacklog{OldestAge: time.Hour}, maxBacklog, 0, false},
		{"both triggers disabled never fall back", false, UsageBalanceSettlementBacklog{Pending: 1_000_000_000, OldestAge: time.Hour}, 0, 0, false},
		{"both triggers disabled recover immediately", true, UsageBalanceSettlementBacklog{Pending: 1_000_000_000, OldestAge: time.Hour}, 0, 0, false},
		{"disabled depth trigger does not block recovery", true, UsageBalanceSettlementBacklog{Pending: 1_000_000_000, OldestAge: time.Second}, 0, maxAge, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, nextBacklogFallbackState(tt.active, tt.backlog, tt.maxBacklog, tt.maxAge))
		})
	}
}

func TestSettlementGuard_DepthCapAlwaysAllowsTheHalfThresholdComparison(t *testing.T) {
	require.Equal(t, int64(20000), usageBalanceSettlementDepthCap(0))
	require.Equal(t, int64(20000), usageBalanceSettlementDepthCap(5000))
	require.Equal(t, int64(40000), usageBalanceSettlementDepthCap(20000))
	require.Equal(t, int64(2_000_000), usageBalanceSettlementDepthCap(1_000_000))
	for _, limit := range []int64{1, 100, 20000, 750000} {
		require.Greater(t, usageBalanceSettlementDepthCap(limit), limit, "a capped reading must still be able to exceed the limit")
	}
}

func TestSettlementGuard_ApplyBacklogEntersAndLeavesWithHysteresis(t *testing.T) {
	resetSettlementProcessState(t)
	w := newGuardWorker(&guardTestRepo{}, 100, 10*time.Second)

	steps := []struct {
		name         string
		backlog      UsageBalanceSettlementBacklog
		wantFallback bool
		transitions  uint64
	}{
		{"at the limit", UsageBalanceSettlementBacklog{Pending: 100}, false, 0},
		{"over the limit", UsageBalanceSettlementBacklog{Pending: 101}, true, 1},
		{"draining but above half", UsageBalanceSettlementBacklog{Pending: 99}, true, 1},
		{"exactly half", UsageBalanceSettlementBacklog{Pending: 50}, true, 1},
		{"below half", UsageBalanceSettlementBacklog{Pending: 49}, false, 2},
		{"wobbling just under the limit does not re-enter", UsageBalanceSettlementBacklog{Pending: 60}, false, 2},
		{"age over the limit", UsageBalanceSettlementBacklog{Pending: 3, OldestAge: 11 * time.Second}, true, 3},
		{"age exactly half", UsageBalanceSettlementBacklog{Pending: 3, OldestAge: 5 * time.Second}, true, 3},
		{"everything calm", UsageBalanceSettlementBacklog{Pending: 3, OldestAge: 4 * time.Second}, false, 4},
	}
	for _, step := range steps {
		w.applyBacklog(step.backlog)
		require.Equal(t, step.wantFallback, UsageBalanceSettlementBacklogFallbackActive(), step.name)
		require.Equal(t, step.transitions, w.fallbackTransitions.Load(), step.name)
		if step.wantFallback {
			require.NotZero(t, w.fallbackSince.Load(), "%s: entry time must be recorded", step.name)
		} else {
			require.Zero(t, w.fallbackSince.Load(), "%s: entry time must be cleared on recovery", step.name)
		}
	}
}

func TestSettlementGuard_ReadErrorKeepsThePreviousState(t *testing.T) {
	resetSettlementProcessState(t)
	repo := &guardTestRepo{}
	w := newGuardWorker(repo, 100, 10*time.Second)
	w.backlogReader = repo
	ctx := context.Background()

	// Not in fallback: an unreadable queue must not flip the process into it.
	repo.setReadError(context.DeadlineExceeded)
	w.sampleBacklog(ctx)
	require.False(t, UsageBalanceSettlementBacklogFallbackActive())
	require.Equal(t, uint64(1), w.sampleFailures.Load())

	// In fallback: it stays there until a reading says the queue calmed down.
	repo.setBacklog(UsageBalanceSettlementBacklog{Pending: 500})
	w.sampleBacklog(ctx)
	require.True(t, UsageBalanceSettlementBacklogFallbackActive())
	repo.setReadError(context.DeadlineExceeded)
	w.sampleBacklog(ctx)
	require.True(t, UsageBalanceSettlementBacklogFallbackActive())
	require.Equal(t, uint64(2), w.sampleFailures.Load())

	repo.setBacklog(UsageBalanceSettlementBacklog{Pending: 0})
	w.sampleBacklog(ctx)
	require.False(t, UsageBalanceSettlementBacklogFallbackActive())
}

func TestSettlementGuard_SamplingUsesADepthCapAboveTheLimit(t *testing.T) {
	resetSettlementProcessState(t)
	repo := &guardTestRepo{}
	w := newGuardWorker(repo, 30000, 0)
	w.backlogReader = repo
	w.sampleBacklog(context.Background())
	require.Equal(t, int64(60000), repo.lastCap.Load())
}

func TestSettlementGuard_MonitorDoesNotRunWhenSwitchIsOff(t *testing.T) {
	resetSettlementProcessState(t)
	repo := &guardTestRepo{}
	repo.setBacklog(UsageBalanceSettlementBacklog{Pending: 1_000_000})
	worker := NewUsageBalanceSettlementWorkerWithOptions(repo, UsageBalanceSettlementWorkerOptions{
		MergeEnabled:          false,
		MaxBacklog:            1,
		MaxAge:                time.Second,
		BacklogSampleInterval: time.Millisecond,
	})
	t.Cleanup(worker.Stop)
	require.True(t, UsageBalanceSettlementConsumerRunning(), "the settlement worker itself still runs with the switch off")

	time.Sleep(60 * time.Millisecond)
	require.Zero(t, repo.backlogCalls.Load(), "switch off: no sampling query may be issued")
	require.False(t, UsageBalanceSettlementBacklogFallbackActive())

	// The legacy constructor is the same thing.
	legacy := NewUsageBalanceSettlementWorker(repo)
	t.Cleanup(legacy.Stop)
	time.Sleep(30 * time.Millisecond)
	require.Zero(t, repo.backlogCalls.Load())
}

func TestSettlementGuard_MonitorFollowsTheQueueEndToEnd(t *testing.T) {
	resetSettlementProcessState(t)
	repo := &guardTestRepo{}
	repo.setBacklog(UsageBalanceSettlementBacklog{Pending: 500})
	worker := NewUsageBalanceSettlementWorkerWithOptions(repo, UsageBalanceSettlementWorkerOptions{
		MergeEnabled:          true,
		MaxBacklog:            100,
		MaxAge:                10 * time.Second,
		BacklogSampleInterval: 5 * time.Millisecond,
	})
	t.Cleanup(worker.Stop)

	require.Eventually(t, UsageBalanceSettlementBacklogFallbackActive, 500*time.Millisecond, 2*time.Millisecond,
		"the first sample is taken at start, before any request is allowed to queue")

	// 5ms sampling interval: well inside 500ms. The default (1s) would not be.
	repo.setBacklog(UsageBalanceSettlementBacklog{Pending: 10})
	require.Eventually(t, func() bool { return !UsageBalanceSettlementBacklogFallbackActive() }, 500*time.Millisecond, 2*time.Millisecond)

	repo.setBacklog(UsageBalanceSettlementBacklog{Pending: 0, OldestAge: 30 * time.Second})
	require.Eventually(t, UsageBalanceSettlementBacklogFallbackActive, 500*time.Millisecond, 2*time.Millisecond)

	worker.Stop()
	require.False(t, UsageBalanceSettlementBacklogFallbackActive(), "no worker, no backlog reading: the state is cleared")
	require.False(t, UsageBalanceSettlementConsumerRunning())
	callsAtStop := repo.backlogCalls.Load()
	time.Sleep(40 * time.Millisecond)
	require.Equal(t, callsAtStop, repo.backlogCalls.Load(), "no sampling after Stop")
}

func TestSettlementGuard_StopClearsProcessFlags(t *testing.T) {
	resetSettlementProcessState(t)
	worker := NewUsageBalanceSettlementWorkerWithOptions(&guardTestRepo{}, UsageBalanceSettlementWorkerOptions{
		MergeEnabled: true,
		Observer:     &settlementObserverSpy{},
	})
	require.True(t, UsageBalanceSettlementNotifierActive())
	usageBalanceSettlementBacklogFallback.Store(true)
	worker.Stop()
	require.False(t, UsageBalanceSettlementNotifierActive())
	require.False(t, UsageBalanceSettlementBacklogFallbackActive())
	require.False(t, UsageBalanceSettlementConsumerRunning())
}

func TestSettlementGuard_LogsAreRateLimited(t *testing.T) {
	resetSettlementProcessState(t)
	sink := captureSettlementLogs(t)
	w := newGuardWorker(&guardTestRepo{}, 100, 10*time.Second)

	over := UsageBalanceSettlementBacklog{Pending: 150, OldestAge: 2 * time.Second}
	for i := 0; i < 50; i++ {
		w.applyBacklog(over)
	}
	require.Len(t, sink.named("billing.balance_settlement_backlog_high"), 1, "a stuck queue must not flood the log")
	require.Len(t, sink.named("billing.balance_settlement_fallback_enter"), 1, "entering fallback is logged once, not per sample")

	high := sink.named("billing.balance_settlement_backlog_high")[0]
	require.Equal(t, int64(150), high.Fields["backlog_depth"])
	require.Equal(t, int64(2000), high.Fields["oldest_age_ms"])
	require.Equal(t, int64(100), high.Fields["max_backlog"])
	require.Equal(t, int64(10), high.Fields["max_age_seconds"])
	require.Equal(t, true, high.Fields["sync_fallback"])
	require.Equal(t, "warn", high.Level)

	// After the quiet period the next over-limit reading logs again.
	w.lastBacklogLog.Store(time.Now().Add(-2 * usageBalanceSettlementBacklogLogEvery).UnixNano())
	w.applyBacklog(over)
	require.Len(t, sink.named("billing.balance_settlement_backlog_high"), 2)

	w.applyBacklog(UsageBalanceSettlementBacklog{Pending: 1})
	require.Len(t, sink.named("billing.balance_settlement_fallback_exit"), 1)
	w.lastBacklogLog.Store(0)
	w.applyBacklog(UsageBalanceSettlementBacklog{Pending: 1})
	require.Len(t, sink.named("billing.balance_settlement_backlog_high"), 2, "a calm queue never warns")
}

func TestSettlementGuard_SampleFailureLogIsRateLimited(t *testing.T) {
	resetSettlementProcessState(t)
	sink := captureSettlementLogs(t)
	repo := &guardTestRepo{}
	repo.setReadError(context.DeadlineExceeded)
	w := newGuardWorker(repo, 100, 0)
	w.backlogReader = repo
	for i := 0; i < 20; i++ {
		w.sampleBacklog(context.Background())
	}
	require.Len(t, sink.named("billing.balance_settlement_backlog_sample_failed"), 1)
	require.Equal(t, uint64(20), w.sampleFailures.Load())
}

func TestSettlementGuard_StatsLogCarriesBacklogReading(t *testing.T) {
	resetSettlementProcessState(t)
	sink := captureSettlementLogs(t)
	w := newGuardWorker(&guardTestRepo{}, 100, 10*time.Second)

	w.applyBacklog(UsageBalanceSettlementBacklog{Pending: 7, OldestAge: 1500 * time.Millisecond})
	w.flushes.Store(3)
	w.events.Store(30)
	w.logStats()

	events := sink.named("billing.balance_settlement_flushed")
	require.Len(t, events, 1)
	fields := events[0].Fields
	require.Equal(t, int64(7), fields["backlog_depth"])
	require.Equal(t, int64(1500), fields["oldest_age_ms"])
	require.Equal(t, false, fields["sync_fallback"])
	require.Equal(t, false, fields["backlog_depth_capped"])
	require.Equal(t, uint64(3), fields["flushes"], "the existing fields are still there")
	require.Equal(t, uint64(30), fields["total_events"])

	// No new flushes but a non-empty queue: still logged. A stalled worker is
	// exactly the case where the flush counters stop moving.
	w.logStats()
	require.Len(t, sink.named("billing.balance_settlement_flushed"), 2)

	// Quiet: nothing flushed, nothing queued, not in fallback.
	w.applyBacklog(UsageBalanceSettlementBacklog{Pending: 0})
	w.logStats()
	require.Len(t, sink.named("billing.balance_settlement_flushed"), 2)

	// In fallback with an empty reading is still worth a line.
	usageBalanceSettlementBacklogFallback.Store(true)
	w.logStats()
	require.Len(t, sink.named("billing.balance_settlement_flushed"), 3)
}

func TestSettlementGuard_StatsLogIsUnchangedWithoutMonitoring(t *testing.T) {
	resetSettlementProcessState(t)
	sink := captureSettlementLogs(t)
	w := &UsageBalanceSettlementWorker{repo: &guardTestRepo{}}

	w.logStats()
	require.Empty(t, sink.named("billing.balance_settlement_flushed"), "no activity, no line (as before)")

	w.flushes.Store(2)
	w.events.Store(5)
	w.logStats()
	events := sink.named("billing.balance_settlement_flushed")
	require.Len(t, events, 1)
	for _, key := range []string{"backlog_depth", "backlog_depth_capped", "oldest_age_ms", "sync_fallback"} {
		require.NotContains(t, events[0].Fields, key, "switch off: the log line keeps its old shape")
	}
}

func TestSettlementGuard_HealthReportsReadingStateAndCounters(t *testing.T) {
	resetSettlementProcessState(t)
	repo := &guardTestRepo{}
	repo.setBacklog(UsageBalanceSettlementBacklog{Pending: 42, OldestAge: 1234 * time.Millisecond})
	w := newGuardWorker(repo, 100, 10*time.Second)
	w.flushes.Store(5)
	w.events.Store(50)
	w.failures.Store(1)
	w.sampleFailures.Store(2)
	w.observerDropped.Store(3)
	usageBalanceSettlementConsumerRunning.Store(true)
	w.applyBacklog(UsageBalanceSettlementBacklog{Pending: 500})
	RecordUsageBalanceSettlementFallbackCharge()
	RecordUsageBalanceSettlementFallbackCharge()

	health := w.Health(context.Background())
	require.True(t, health.MergeEnabled)
	require.True(t, health.ConsumerRunning)
	require.Equal(t, int64(42), health.Pending, "Health takes its own live reading")
	require.Equal(t, int64(1234), health.OldestAgeMs)
	require.False(t, health.PendingCapped)
	require.Equal(t, int64(100), health.MaxBacklog)
	require.Equal(t, int64(10), health.MaxAgeSeconds)
	require.True(t, health.FallbackActive)
	require.NotNil(t, health.FallbackSince)
	require.NotNil(t, health.LastSampleAt, "the monitor's latest successful reading time is reported so a stalled monitor is visible")
	require.WithinDuration(t, time.Now(), *health.LastSampleAt, 5*time.Second)
	require.Equal(t, uint64(1), health.FallbackTransitions)
	require.Equal(t, uint64(2), health.FallbackCharges)
	require.Equal(t, uint64(5), health.Flushes)
	require.Equal(t, uint64(50), health.Events)
	require.Equal(t, uint64(1), health.Failures)
	require.Equal(t, uint64(2), health.SampleFailures)
	require.Equal(t, uint64(3), health.ObserverDropped)
	require.Empty(t, health.StatsError)

	// A failing read is reported, not hidden, and does not wipe the counters.
	repo.setReadError(context.DeadlineExceeded)
	health = w.Health(context.Background())
	require.Equal(t, context.DeadlineExceeded.Error(), health.StatsError)
	require.Equal(t, uint64(5), health.Flushes)

	// Recovery clears the "since" marker.
	repo.setBacklog(UsageBalanceSettlementBacklog{Pending: 1})
	w.applyBacklog(UsageBalanceSettlementBacklog{Pending: 1})
	health = w.Health(context.Background())
	require.False(t, health.FallbackActive)
	require.Nil(t, health.FallbackSince)
}

func TestSettlementGuard_HealthWorksWithTheSwitchOffAndWithoutAReader(t *testing.T) {
	resetSettlementProcessState(t)
	repo := &guardTestRepo{}
	repo.setBacklog(UsageBalanceSettlementBacklog{Pending: 9})
	off := &UsageBalanceSettlementWorker{repo: repo}
	health := off.Health(context.Background())
	require.False(t, health.MergeEnabled)
	require.Equal(t, int64(9), health.Pending, "operators can read the queue even when merge settlement is off")
	require.Nil(t, health.LastSampleAt, "no monitor, no sample time")

	noReader := &UsageBalanceSettlementWorker{repo: &usageBalanceSettlementStatsSpy{}}
	require.Equal(t, UsageBalanceSettlementHealth{}, noReader.Health(context.Background()))

	var nilWorker *UsageBalanceSettlementWorker
	require.Equal(t, UsageBalanceSettlementHealth{}, nilWorker.Health(context.Background()))
}

func TestSettlementGuard_ProcessCountersAndFlags(t *testing.T) {
	resetSettlementProcessState(t)
	require.False(t, UsageBalanceSettlementBacklogFallbackActive())
	require.False(t, UsageBalanceSettlementNotifierActive())
	RecordUsageBalanceSettlementFallbackCharge()
	require.Equal(t, uint64(1), usageBalanceSettlementFallbackCharges.Load())
}
