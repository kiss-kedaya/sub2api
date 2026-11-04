package cryptopay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/stretchr/testify/require"
)

// fakeScan stands in for the worker: one chain with a durable cursor. step runs
// inside Round without the lock held, so it may block.
type fakeScan struct {
	mu        sync.Mutex
	cursor    uint64
	confirmed uint64
	calls     int
	froms     []uint64
	progErr   error
	step      func(ctx context.Context, f *fakeScan, call int, from uint64) error
}

func newFakeScan(cursor, confirmed uint64) *fakeScan {
	return &fakeScan{cursor: cursor, confirmed: confirmed}
}

func (f *fakeScan) Round(ctx context.Context, from uint64) error {
	f.mu.Lock()
	f.calls++
	call, step := f.calls, f.step
	f.froms = append(f.froms, from)
	f.mu.Unlock()
	if step == nil {
		return nil
	}
	return step(ctx, f, call, from)
}

func (f *fakeScan) Progress(context.Context) ([]scanner.ChainScanProgress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.progErr != nil {
		return nil, f.progErr
	}
	return []scanner.ChainScanProgress{{Chain: "bsc", Cursor: f.cursor, ConfirmedHead: f.confirmed, HeadKnown: true}}, nil
}

// advance moves the cursor forward by at most chunk, never past confirmed.
func (f *fakeScan) advance(chunk uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cursor = min(f.cursor+chunk, f.confirmed)
}

// rewind mimics RunOnceFrom: a start block earlier than the cursor moves the
// cursor back to start-1.
func (f *fakeScan) rewind(from uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if from != 0 && (f.cursor == 0 || from-1 < f.cursor) {
		f.cursor = from - 1
	}
}

func (f *fakeScan) seenFroms() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint64(nil), f.froms...)
}

func (f *fakeScan) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fastScanCfg removes every real wait so loops finish in microseconds.
func fastScanCfg() scanLoopConfig {
	return scanLoopConfig{Deadline: 10 * time.Second, MaxStalled: 5}
}

func newTestRegistry() *scanRegistry { return &scanRegistry{jobs: map[string]*scanJob{}} }

// startAndWait runs one job to its end and also proves the goroutine exited.
func startAndWait(t *testing.T, r *scanRegistry, runner scanRunner, cfg scanLoopConfig, from uint64) *ScanRunState {
	t.Helper()
	started, err := r.start(t.Context(), runner, cfg, from)
	require.NoError(t, err)
	r.mu.Lock()
	job := r.jobs[started.ID]
	r.mu.Unlock()
	select {
	case <-job.done:
	case <-time.After(5 * time.Second):
		t.Fatal("scan job did not finish")
	}
	state := r.get(started.ID)
	require.NotNil(t, state)
	r.mu.Lock()
	require.Nil(t, r.active, "a finished job must release the single-run slot")
	r.mu.Unlock()
	return state
}

func TestScanLoopFirstRoundRewindsThenContinuesFromTheCursor(t *testing.T) {
	f := newFakeScan(900, 1000)
	f.step = func(_ context.Context, f *fakeScan, _ int, from uint64) error {
		f.rewind(from)
		f.advance(150)
		return nil
	}
	state := startAndWait(t, newTestRegistry(), f, fastScanCfg(), 500)

	require.Equal(t, ScanRunCompleted, state.Status)
	require.Equal(t, ScanStopCaughtUp, state.StopReason)
	require.Equal(t, []uint64{500, 0, 0, 0}, f.seenFroms(), "only the first executed round may carry from_block")
	require.Equal(t, 4, state.Rounds)
	require.EqualValues(t, 500, state.FromBlock)
	require.Equal(t, []ScanChainState{{Chain: "bsc", Cursor: 1000, ConfirmedHead: 1000, Remaining: 0, HeadKnown: true}}, state.Chains)
	require.Zero(t, state.StalledRounds)
	require.NotNil(t, state.FinishedAt)
}

func TestScanLoopAlreadyCaughtUpCompletesAfterOnePass(t *testing.T) {
	f := newFakeScan(1000, 1000)
	state := startAndWait(t, newTestRegistry(), f, fastScanCfg(), 0)
	require.Equal(t, ScanRunCompleted, state.Status)
	require.Equal(t, 1, state.Rounds)
	require.Equal(t, []uint64{0}, f.seenFroms())
}

func TestScanLoopStopsAfterFiveRoundsWithoutProgress(t *testing.T) {
	f := newFakeScan(10, 1000)
	state := startAndWait(t, newTestRegistry(), f, fastScanCfg(), 0)
	require.Equal(t, ScanRunFailed, state.Status)
	require.Equal(t, ScanStopStalled, state.StopReason)
	require.Equal(t, 5, f.callCount(), "exactly five stalled rounds, no more")
	require.Equal(t, 5, state.Rounds)
	require.Equal(t, 5, state.StalledRounds)
}

func TestScanLoopStallCounterResetsWhenTheCursorMoves(t *testing.T) {
	f := newFakeScan(10, 1000)
	f.step = func(_ context.Context, f *fakeScan, call int, _ uint64) error {
		switch call {
		case 5:
			f.advance(10)
		case 10:
			f.advance(10_000)
		}
		return nil
	}
	state := startAndWait(t, newTestRegistry(), f, fastScanCfg(), 0)
	require.Equal(t, ScanRunCompleted, state.Status, "four stalled rounds, a move, four more: never five in a row")
	require.Equal(t, 10, state.Rounds)
}

func TestScanLoopBusyRetriesAreNeitherRoundsNorStalls(t *testing.T) {
	f := newFakeScan(0, 1000)
	f.step = func(_ context.Context, f *fakeScan, call int, from uint64) error {
		switch {
		case call <= 6:
			return ErrScanBusy
		case call <= 12:
			return fmt.Errorf("pass: %w", ErrScanBusy)
		}
		f.rewind(from)
		f.advance(5000)
		return nil
	}
	state := startAndWait(t, newTestRegistry(), f, fastScanCfg(), 777)

	require.Equal(t, ScanRunCompleted, state.Status, "12 busy answers exceed the stall limit and must still not fail the run")
	require.Equal(t, 12, state.BusyRetries)
	require.Equal(t, 1, state.Rounds)
	require.Zero(t, state.StalledRounds)
	froms := f.seenFroms()
	require.Len(t, froms, 13)
	for _, from := range froms {
		require.EqualValues(t, 777, from, "a busy answer did not execute, so the rewind is still owed")
	}
}

func TestScanLoopBusyForeverEndsAtTheDeadline(t *testing.T) {
	f := newFakeScan(0, 1000)
	f.step = func(context.Context, *fakeScan, int, uint64) error { return ErrScanBusy }
	cfg := scanLoopConfig{Deadline: 150 * time.Millisecond, BusyMin: time.Millisecond, BusyMax: 5 * time.Millisecond, MaxStalled: 5}
	state := startAndWait(t, newTestRegistry(), f, cfg, 0)
	require.Equal(t, ScanRunFailed, state.Status)
	require.Equal(t, ScanStopDeadline, state.StopReason)
	require.Zero(t, state.Rounds)
	require.Positive(t, state.BusyRetries)
}

func TestScanLoopCancelStopsPromptlyMidRound(t *testing.T) {
	r := newTestRegistry()
	entered := make(chan struct{})
	f := newFakeScan(0, 1000)
	f.step = func(ctx context.Context, _ *fakeScan, _ int, _ uint64) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	started, err := r.start(t.Context(), f, fastScanCfg(), 0)
	require.NoError(t, err)
	<-entered

	snapshot, err := r.cancel(started.ID)
	require.NoError(t, err)
	require.Equal(t, started.ID, snapshot.ID)
	r.mu.Lock()
	job := r.jobs[started.ID]
	r.mu.Unlock()
	select {
	case <-job.done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not stop the run")
	}
	state := r.get(started.ID)
	require.Equal(t, ScanRunCancelled, state.Status)
	require.Equal(t, ScanStopCancelled, state.StopReason)

	_, err = r.cancel(started.ID)
	require.ErrorIs(t, err, ErrScanRunFinished)
	_, err = r.cancel("scan-missing")
	require.ErrorIs(t, err, ErrScanRunNotFound)
}

func TestScanLoopCancelInterruptsABackoffSleep(t *testing.T) {
	r := newTestRegistry()
	entered := make(chan struct{}, 1)
	f := newFakeScan(0, 1000)
	f.step = func(context.Context, *fakeScan, int, uint64) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		return ErrScanBusy
	}
	cfg := scanLoopConfig{Deadline: time.Hour, BusyMin: time.Hour, BusyMax: time.Hour, MaxStalled: 5}
	started, err := r.start(t.Context(), f, cfg, 0)
	require.NoError(t, err)
	<-entered
	_, err = r.cancel(started.ID)
	require.NoError(t, err)
	r.mu.Lock()
	job := r.jobs[started.ID]
	r.mu.Unlock()
	select {
	case <-job.done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not interrupt the hour-long backoff")
	}
	require.Equal(t, ScanRunCancelled, r.get(started.ID).Status)
}

func TestScanLoopDeadlineMidRoundFailsWithTheDeadlineReason(t *testing.T) {
	f := newFakeScan(0, 1000)
	f.step = func(ctx context.Context, _ *fakeScan, _ int, _ uint64) error {
		<-ctx.Done()
		return ctx.Err()
	}
	cfg := scanLoopConfig{Deadline: 80 * time.Millisecond, MaxStalled: 5}
	state := startAndWait(t, newTestRegistry(), f, cfg, 0)
	require.Equal(t, ScanRunFailed, state.Status)
	require.Equal(t, ScanStopDeadline, state.StopReason)
}

func TestScanRegistryAllowsOneRunAtATime(t *testing.T) {
	r := newTestRegistry()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	f := newFakeScan(0, 10)
	f.step = func(ctx context.Context, f *fakeScan, _ int, _ uint64) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		f.advance(100)
		return nil
	}
	first, err := r.start(t.Context(), f, fastScanCfg(), 0)
	require.NoError(t, err)
	<-entered
	second, err := r.start(t.Context(), newFakeScan(0, 10), fastScanCfg(), 0)
	require.ErrorIs(t, err, ErrScanJobRunning)
	require.Nil(t, second)
	require.Len(t, r.list(), 1, "a refused start must not leave a second run behind")

	close(release)
	r.mu.Lock()
	job := r.jobs[first.ID]
	r.mu.Unlock()
	<-job.done
	third := startAndWait(t, r, newFakeScan(5, 5), fastScanCfg(), 0)
	require.Equal(t, ScanRunCompleted, third.Status)
	require.Equal(t, third.ID, r.list()[0].ID, "newest first")
}

type scanCtxKey struct{}

func TestScanRunSurvivesTheStartingRequestEndingAndKeepsItsValues(t *testing.T) {
	r := newTestRegistry()
	parent, cancelParent := context.WithCancel(context.WithValue(t.Context(), scanCtxKey{}, "trace-1"))
	seen := make(chan any, 1)
	release := make(chan struct{})
	f := newFakeScan(0, 10)
	f.step = func(ctx context.Context, f *fakeScan, _ int, _ uint64) error {
		seen <- ctx.Value(scanCtxKey{})
		<-release
		if err := ctx.Err(); err != nil {
			return err
		}
		f.advance(100)
		return nil
	}
	started, err := r.start(parent, f, fastScanCfg(), 0)
	require.NoError(t, err)
	require.Equal(t, "trace-1", <-seen)
	cancelParent() // the browser leaves

	require.Never(t, func() bool { return r.get(started.ID).Status != ScanRunRunning },
		150*time.Millisecond, 10*time.Millisecond, "the request ending must not stop the run")
	close(release)
	r.mu.Lock()
	job := r.jobs[started.ID]
	r.mu.Unlock()
	<-job.done
	require.Equal(t, ScanRunCompleted, r.get(started.ID).Status)
}

func TestScanLoopFirstRoundFailureWithoutMovementFailsInsteadOfSkippingTheRewind(t *testing.T) {
	f := newFakeScan(900, 1000)
	f.step = func(context.Context, *fakeScan, int, uint64) error {
		return errors.New("historical scan must start within the last 100000 confirmed blocks https://rpc.example/key123")
	}
	state := startAndWait(t, newTestRegistry(), f, fastScanCfg(), 500)
	require.Equal(t, ScanRunFailed, state.Status)
	require.Equal(t, ScanStopError, state.StopReason)
	require.Equal(t, []uint64{500}, f.seenFroms(), "no retry that would silently drop the rewind")
	require.Equal(t, 1, state.Rounds)
	require.NotEmpty(t, state.LastError)
	require.NotContains(t, state.LastError, "key123")
	require.NotContains(t, state.LastError, "rpc.example")
}

func TestScanLoopFirstRoundErrorAfterTheRewindStillContinues(t *testing.T) {
	f := newFakeScan(900, 1000)
	f.step = func(_ context.Context, f *fakeScan, call int, from uint64) error {
		f.rewind(from)
		f.advance(300)
		if call == 1 {
			return fmt.Errorf("scan bsc: %w", context.DeadlineExceeded)
		}
		return nil
	}
	state := startAndWait(t, newTestRegistry(), f, fastScanCfg(), 500)
	require.Equal(t, ScanRunCompleted, state.Status, "the rewind landed, so a per-chain timeout is not fatal")
	require.Equal(t, []uint64{500, 0}, f.seenFroms())
	require.Equal(t, 2, state.Rounds)
	require.Equal(t, "Chain scan budget exceeded; progress so far is kept", state.LastError)
}

func TestScanLoopErrorsAreSanitisedBeforeTheyReachState(t *testing.T) {
	f := newFakeScan(10, 1000)
	f.step = func(context.Context, *fakeScan, int, uint64) error {
		return errors.New("dial tcp: lookup https://user:hunter2@node.example/v3/key123: no such host")
	}
	state := startAndWait(t, newTestRegistry(), f, fastScanCfg(), 0)
	require.Equal(t, ScanRunFailed, state.Status)
	require.Equal(t, ScanStopStalled, state.StopReason)
	require.NotEmpty(t, state.LastError)
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	for _, secret := range []string{"hunter2", "node.example", "key123", "user:"} {
		require.NotContains(t, string(raw), secret)
	}
}

func TestScanLoopReportsAnRPCFailureByCategoryOnly(t *testing.T) {
	f := newFakeScan(10, 1000)
	f.step = func(context.Context, *fakeScan, int, uint64) error {
		return fmt.Errorf("scan bsc: %w", rpc.NewTransportFailure(errors.New("Post https://rpc.example/key123: dial tcp")))
	}
	state := startAndWait(t, newTestRegistry(), f, fastScanCfg(), 0)
	require.Equal(t, "RPC connection failed", state.LastError)
}

func TestScanLoopProgressReadFailuresCountAsStalls(t *testing.T) {
	f := newFakeScan(10, 1000)
	f.progErr = errors.New("pq: password authentication failed for user admin at 10.0.0.5")
	state := startAndWait(t, newTestRegistry(), f, fastScanCfg(), 0)
	require.Equal(t, ScanRunFailed, state.Status)
	require.Equal(t, ScanStopStalled, state.StopReason)
	require.NotEmpty(t, state.LastError)
	require.NotContains(t, state.LastError, "10.0.0.5")
	require.NotNil(t, state.Chains)
	require.Empty(t, state.Chains)
}

func TestScanLoopTerminalErrorsFailOnTheFirstRound(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"read only":   {fmt.Errorf("pass: %w", mode.ErrReadOnly), "Payment scanning is read-only on this instance"},
		"unavailable": {ErrScanUnavailable, ErrScanUnavailable.Error()},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeScan(10, 1000)
			f.step = func(context.Context, *fakeScan, int, uint64) error { return tc.err }
			state := startAndWait(t, newTestRegistry(), f, fastScanCfg(), 0)
			require.Equal(t, ScanRunFailed, state.Status)
			require.Equal(t, ScanStopError, state.StopReason)
			require.Equal(t, 1, f.callCount(), "retrying cannot fix this")
			require.Equal(t, 1, state.Rounds)
			require.Equal(t, tc.want, state.LastError)
		})
	}
}

func TestScanLoopPanicIsContainedAndReleasesTheSlot(t *testing.T) {
	r := newTestRegistry()
	f := newFakeScan(10, 1000)
	f.step = func(context.Context, *fakeScan, int, uint64) error { panic("boom https://rpc.example/key123") }
	state := startAndWait(t, r, f, fastScanCfg(), 0)
	require.Equal(t, ScanRunFailed, state.Status)
	require.Equal(t, ScanStopInternal, state.StopReason)
	require.NotEmpty(t, state.LastError)
	require.NotContains(t, state.LastError, "key123")
	next := startAndWait(t, r, newFakeScan(5, 5), fastScanCfg(), 0)
	require.Equal(t, ScanRunCompleted, next.Status, "a panic must not wedge the single-run slot")
}

func TestScanRunSnapshotsAreIsolatedFromTheStoredState(t *testing.T) {
	r := newTestRegistry()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	f := newFakeScan(5, 10)
	f.step = func(ctx context.Context, _ *fakeScan, _ int, _ uint64) error {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}
	started, err := r.start(t.Context(), f, fastScanCfg(), 0)
	require.NoError(t, err)
	raw, err := json.Marshal(started)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"chains":[]`, "an empty chain list must encode as [] not null")
	<-entered

	snapshot := r.get(started.ID)
	require.Len(t, snapshot.Chains, 1)
	snapshot.Chains[0].Cursor = 999
	snapshot.Status = ScanRunFailed
	listed := r.list()
	listed[0].Chains[0].Cursor = 888
	again := r.get(started.ID)
	require.EqualValues(t, 5, again.Chains[0].Cursor)
	require.Equal(t, ScanRunRunning, again.Status)
	close(release)
}

func TestScanRegistryKeepsOnlyTheNewestFinishedRuns(t *testing.T) {
	r := newTestRegistry()
	var ids []string
	for range scanRunRetention + 5 {
		state := startAndWait(t, r, newFakeScan(1, 1), fastScanCfg(), 0)
		ids = append(ids, state.ID)
	}
	require.Len(t, r.list(), scanRunRetention+1)
	require.Nil(t, r.get(ids[0]), "the oldest run is dropped")
	require.NotNil(t, r.get(ids[len(ids)-1]))
	require.Equal(t, ids[len(ids)-1], r.list()[0].ID, "newest first")
}

func TestScanRegistryIsSafeUnderConcurrentReadersAndCancel(t *testing.T) {
	r := newTestRegistry()
	f := newFakeScan(0, 1<<40)
	f.step = func(_ context.Context, f *fakeScan, _ int, _ uint64) error {
		f.advance(1)
		return nil
	}
	started, err := r.start(t.Context(), f, fastScanCfg(), 0)
	require.NoError(t, err)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if s := r.get(started.ID); s != nil && len(s.Chains) > 0 {
					s.Chains[0].Cursor++ // a private copy: must not race with the writer
				}
				for _, s := range r.list() {
					s.Rounds++
				}
			}
		}()
	}
	require.Eventually(t, func() bool { return r.get(started.ID).Rounds >= 300 }, 10*time.Second, time.Millisecond)
	_, err = r.cancel(started.ID)
	require.NoError(t, err)
	r.mu.Lock()
	job := r.jobs[started.ID]
	r.mu.Unlock()
	<-job.done
	close(stop)
	wg.Wait()
	require.Equal(t, ScanRunCancelled, r.get(started.ID).Status)
}

func TestStartScanAsyncRefusesWhenTheScannerIsNotRunning(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	before := len(scanJobs.list())
	for name, s := range map[string]*Service{"nil service": nil, "zero service": {}} {
		t.Run(name, func(t *testing.T) {
			state, err := s.StartScanAsync(t.Context(), 0)
			require.ErrorIs(t, err, ErrScanUnavailable)
			require.Nil(t, state)
		})
	}
	require.Len(t, scanJobs.list(), before, "a refused start must not register a run")
	scanJobs.mu.Lock()
	require.Nil(t, scanJobs.active)
	scanJobs.mu.Unlock()
}

func TestStartScanAsyncHonoursReadOnlyMode(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "true")
	t.Setenv("CRYPTO_ADMIN_TEST_WRITE_ENABLED", "false")
	state, err := (&Service{}).StartScanAsync(t.Context(), 0)
	require.ErrorIs(t, err, mode.ErrReadOnly)
	require.Nil(t, state)
}

func TestServiceScanRunnerFailsClosedWithoutAWorker(t *testing.T) {
	t.Setenv("CRYPTO_ADMIN_TEST_MODE", "false")
	for _, s := range []*Service{nil, {}} {
		runner := serviceScanRunner{s: s}
		require.ErrorIs(t, runner.Round(t.Context(), 0), ErrScanUnavailable)
		progress, err := runner.Progress(t.Context())
		require.ErrorIs(t, err, ErrScanUnavailable)
		require.Nil(t, progress)
	}
}

func TestScanRunLookupsAreSafeOnAnEmptyService(t *testing.T) {
	for _, s := range []*Service{nil, {}} {
		require.Nil(t, s.ScanRun("scan-missing"))
		_, err := s.CancelScanRun("scan-missing")
		require.ErrorIs(t, err, ErrScanRunNotFound)
		require.NotNil(t, s.ScanRuns(), "must encode as [] not null")
	}
}

func TestScanBackoffDoublesToItsCapAndResets(t *testing.T) {
	b := newBackoff(time.Second, 5*time.Second)
	got := []time.Duration{b.next(), b.next(), b.next(), b.next()}
	require.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second}, got)
	b.reset()
	require.Equal(t, time.Second, b.next())
}

func TestSleepCtxHonoursCancellationAndZero(t *testing.T) {
	require.True(t, sleepCtx(t.Context(), 0))
	require.True(t, sleepCtx(t.Context(), time.Millisecond))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.False(t, sleepCtx(ctx, 0))
	require.False(t, sleepCtx(ctx, time.Hour))
}

func TestCursorsMovedFailsClosedOnUnmeasuredChains(t *testing.T) {
	at := func(chain string, cursor uint64) scanner.ChainScanProgress {
		return scanner.ChainScanProgress{Chain: chain, Cursor: cursor}
	}
	require.True(t, cursorsMoved([]scanner.ChainScanProgress{at("bsc", 1)}, []scanner.ChainScanProgress{at("bsc", 2)}))
	require.True(t, cursorsMoved([]scanner.ChainScanProgress{at("bsc", 5)}, []scanner.ChainScanProgress{at("bsc", 2)}), "a rewind is movement too")
	require.False(t, cursorsMoved([]scanner.ChainScanProgress{at("bsc", 1)}, []scanner.ChainScanProgress{at("bsc", 1)}))
	require.False(t, cursorsMoved(nil, []scanner.ChainScanProgress{at("bsc", 2)}), "no baseline, no claim")
	require.False(t, cursorsMoved([]scanner.ChainScanProgress{at("eth", 1)}, []scanner.ChainScanProgress{at("bsc", 2)}))
}

func TestAllCaughtUpNeedsEveryMeasuredChainAtItsHead(t *testing.T) {
	done := scanner.ChainScanProgress{Chain: "bsc", Cursor: 10, ConfirmedHead: 10, HeadKnown: true}
	behind := scanner.ChainScanProgress{Chain: "eth", Cursor: 5, ConfirmedHead: 10, HeadKnown: true}
	unknown := scanner.ChainScanProgress{Chain: "polygon", Cursor: 10}
	require.True(t, allCaughtUp([]scanner.ChainScanProgress{done}))
	require.False(t, allCaughtUp([]scanner.ChainScanProgress{done, behind}))
	require.False(t, allCaughtUp([]scanner.ChainScanProgress{done, unknown}))
	require.True(t, allCaughtUp(nil), "no scanned chain is vacuously caught up, as in the sync scan")
}

func TestScanSafeErrorNeverForwardsRawText(t *testing.T) {
	secret := "https://user:hunter2@node.example/v3/key123"
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"nil":             {nil, ""},
		"read only":       {mode.ErrReadOnly, "Payment scanning is read-only on this instance"},
		"unavailable":     {ErrScanUnavailable, "enable payment channel before scanning"},
		"bare deadline":   {fmt.Errorf("scan bsc: %w", context.DeadlineExceeded), "Chain scan budget exceeded; progress so far is kept"},
		"rpc transport":   {fmt.Errorf("scan bsc: %w", rpc.NewTransportFailure(context.DeadlineExceeded)), "RPC connection failed"},
		"our own wording": {errors.New("cryptopay is disabled"), "cryptopay is disabled"},
		"raw leak":        {errors.New("Post " + secret + ": dial tcp 10.0.0.5:443: refused"), "Payment operation failed; reconcile transaction status before retrying"},
	} {
		t.Run(name, func(t *testing.T) {
			got := scanSafeError(tc.err)
			require.Equal(t, tc.want, got)
			for _, leak := range []string{"hunter2", "key123", "node.example", "10.0.0.5"} {
				require.NotContains(t, got, leak)
			}
		})
	}
}

func TestDefaultScanLoopConfigMatchesTheContract(t *testing.T) {
	cfg := defaultScanLoopConfig()
	require.Equal(t, 5, cfg.MaxStalled)
	require.Equal(t, 2*time.Hour, cfg.Deadline)
	require.Positive(t, cfg.Pause)
	require.Positive(t, cfg.BusyMin)
	require.GreaterOrEqual(t, cfg.BusyMax, cfg.BusyMin)
	require.Positive(t, cfg.ErrorMin)
	require.GreaterOrEqual(t, cfg.ErrorMax, cfg.ErrorMin)
}

func TestScanRunStateJSONShape(t *testing.T) {
	r := newTestRegistry()
	state := startAndWait(t, r, newFakeScan(1, 1), fastScanCfg(), 42)
	raw, err := json.Marshal(state)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	for _, key := range []string{"id", "status", "started_at", "finished_at", "from_block", "rounds", "busy_retries", "stalled_rounds", "chains", "stop_reason"} {
		require.Contains(t, decoded, key)
	}
	require.NotContains(t, decoded, "seq", "the ordering counter is internal")
	require.NotContains(t, decoded, "last_error", "omitted while empty")
	chain := decoded["chains"].([]any)[0].(map[string]any)
	for _, key := range []string{"chain", "cursor", "confirmed_head", "remaining", "head_known"} {
		require.Contains(t, chain, key)
	}
	require.Equal(t, "completed", decoded["status"])
	require.EqualValues(t, 42, decoded["from_block"])
}
