package cryptopay

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/wallet"
	"github.com/stretchr/testify/require"
)

var _ sweepPassRunner = (*sweeper.Executor)(nil)

// fakeJob is one durable sweep job. It advances at most one stage per pass, like
// the executor does for a job whose gas confirms between passes.
type fakeJob struct {
	id    int64
	chain string
	stage string // planned, gas_pending, gas_confirmed, sweep_pending, swept
	gas   string
	sweep string
	// failFor is the number of upcoming passes in which the job reports failErr
	// instead of advancing; failEvery makes the failure permanent.
	failFor   int
	failEvery bool
	failErr   error
	failStep  string
}

// fakeQueue stands in for the executor: jobs in id order, a gas payer that is
// serialized per chain (one unconfirmed top-up at a time, exactly like
// gasPayerBusy), job-level failures that can clear, and a ledger of every
// signature and broadcast so a test can prove nothing is sent twice.
type fakeQueue struct {
	mu          sync.Mutex
	jobs        []*fakeJob
	passes      int
	resets      int
	fatalAt     int // pass number (1-based) that returns fatalErr, 0 = never
	fatalErr    error
	hook        func(pass int, q *fakeQueue) // runs at the start of a pass
	resetErr    error
	resetErrAt  int // reset call number that fails
	gasSigned   map[int64]int
	sweepSigned map[int64]int
	broadcasts  map[string]int
}

func newFakeQueue(jobs ...*fakeJob) *fakeQueue {
	for _, j := range jobs {
		if j.stage == "" {
			j.stage = "planned"
		}
		if j.failErr == nil {
			j.failErr = transientRPC()
		}
		if j.failStep == "" {
			j.failStep = sweeper.StepBalanceRead
		}
	}
	return &fakeQueue{jobs: jobs, gasSigned: map[int64]int{}, sweepSigned: map[int64]int{}, broadcasts: map[string]int{}}
}

func transientRPC() error { return rpc.NewTransportFailure(errors.New("fixture transport")) }

func (q *fakeQueue) ResetRetryableFailures(context.Context, int) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.resets++
	if q.resetErr != nil && q.resets == q.resetErrAt {
		return 0, q.resetErr
	}
	return 0, nil
}

// payerBusy mirrors gasPayerBusy: another job on the chain holds an
// unconfirmed top-up.
func (q *fakeQueue) payerBusy(chain string, except int64) bool {
	for _, j := range q.jobs {
		if j.chain == chain && j.id != except && j.stage == "gas_pending" {
			return true
		}
	}
	return false
}

func (q *fakeQueue) sign(counter map[int64]int, id int64, hash string) {
	counter[id]++
	q.broadcasts[hash]++ // the executor broadcasts exactly what it signed
}

func (q *fakeQueue) RunManualAndWait(_ context.Context, dryRun bool) (*sweeper.Summary, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if dryRun {
		return nil, errors.New("the drain never runs dry")
	}
	q.passes++
	if q.hook != nil {
		q.hook(q.passes, q)
	}
	if q.fatalAt == q.passes {
		return nil, q.fatalErr
	}
	summary := &sweeper.Summary{}
	for _, j := range q.jobs {
		if j.stage != "swept" {
			summary.Planned++
		}
	}
	var perr error
	for _, j := range q.jobs {
		if j.stage == "swept" {
			continue
		}
		entry := sweeper.SweepEntry{JobID: j.id, Chain: j.chain, Address: fmt.Sprintf("addr-%d", j.id),
			Token: "usdt", Amount: "1000", Status: "pending"}
		const waiting = "awaiting canonical receipt and required confirmations"
		switch {
		case j.failFor > 0 || j.failEvery:
			if j.failFor > 0 {
				j.failFor--
			}
			entry.Error = rpc.SafeError(j.failErr)
			entry.Step, entry.Category = j.failStep, sweeper.FailureCategory(j.failErr)
			perr = errors.Join(perr, j.failErr)
		case j.stage == "planned" && q.payerBusy(j.chain, j.id):
			entry.Error = "awaiting gas payer transaction confirmation"
		case j.stage == "planned":
			j.gas, j.stage = fmt.Sprintf("gas-%d", j.id), "gas_pending"
			q.sign(q.gasSigned, j.id, j.gas)
			summary.Advanced++
			entry.Error = waiting
		case j.stage == "gas_pending":
			j.stage = "gas_confirmed"
			summary.Advanced++
			entry.Error = waiting
		case j.stage == "gas_confirmed":
			j.sweep, j.stage = fmt.Sprintf("sweep-%d", j.id), "sweep_pending"
			q.sign(q.sweepSigned, j.id, j.sweep)
			summary.Advanced++
			entry.Error = waiting
		case j.stage == "sweep_pending":
			j.stage = "swept"
			summary.Advanced++
			entry.Status = "swept"
		}
		entry.GasTxHash, entry.SweepTxHash = j.gas, j.sweep
		summary.Add(entry)
	}
	return summary, perr
}

func (q *fakeQueue) job(id int64) *fakeJob {
	for _, j := range q.jobs {
		if j.id == id {
			return j
		}
	}
	return nil
}

func (q *fakeQueue) assertNothingSentTwice(t *testing.T) {
	t.Helper()
	for id, n := range q.gasSigned {
		require.LessOrEqual(t, n, 1, "gas top-up for job %d was signed %d times", id, n)
	}
	for id, n := range q.sweepSigned {
		require.LessOrEqual(t, n, 1, "sweep for job %d was signed %d times", id, n)
	}
	for hash, n := range q.broadcasts {
		require.Equal(t, 1, n, "transaction %s was broadcast %d times", hash, n)
	}
}

func noPause() sweepDrainConfig {
	return sweepDrainConfig{maxRounds: sweepDrainMaxRounds, stallRounds: sweepDrainStallRounds}
}

func bscJobs(n int, first int64) []*fakeJob {
	out := make([]*fakeJob, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, &fakeJob{id: first + int64(i), chain: "bsc"})
	}
	return out
}

func entryOf(t *testing.T, s *sweeper.Summary, id int64) sweeper.SweepEntry {
	t.Helper()
	for _, e := range s.Entries {
		if e.JobID == id {
			return e
		}
	}
	t.Fatalf("no entry for job %d", id)
	return sweeper.SweepEntry{}
}

// The production incident: one Polygon job errors on the first pass while BSC has
// sixteen jobs that need many rounds. The old loop returned on that first error
// and BSC was never reached.
func TestDrainAFailingChainDoesNotStopTheOtherChain(t *testing.T) {
	polygon := &fakeJob{id: 1335, chain: "polygon", failFor: 2}
	q := newFakeQueue(append([]*fakeJob{polygon}, bscJobs(16, 1400)...)...)
	summary, err := drainManualSweep(t.Context(), q, noPause())

	require.NoError(t, err, "everything advanced, so there is no aggregate error")
	for _, j := range q.jobs {
		require.Equal(t, "swept", j.stage, "job %d (%s) was left behind", j.id, j.chain)
	}
	require.Greater(t, q.passes, 16, "sixteen serialized gas top-ups cannot fit in fewer rounds")
	require.Equal(t, 17, summary.Swept)
	require.Len(t, summary.Entries, 17, "one entry per job, not one per job per round")
	require.Zero(t, summary.Failed)
	require.Zero(t, summary.Pending)
	// The error that happened is reported even though it cleared.
	require.Equal(t, []sweeper.SweepFailure{{JobID: 1335, Chain: "polygon", Step: sweeper.StepBalanceRead, Category: rpc.CategoryTransport, Count: 2}}, summary.Failures)
	// A job that recovered raises no alert.
	require.Empty(t, sweepFailureEvents(summary, err))
	q.assertNothingSentTwice(t)
}

// An error that clears in round two lets that job complete.
func TestDrainRetriesAFailingJobUntilItClears(t *testing.T) {
	q := newFakeQueue(&fakeJob{id: 7, chain: "polygon", failFor: 1})
	summary, err := drainManualSweep(t.Context(), q, noPause())
	require.NoError(t, err)
	require.Equal(t, "swept", q.job(7).stage)
	require.Equal(t, 1, summary.Swept)
	require.Empty(t, sweepFailureEvents(summary, err))
	q.assertNothingSentTwice(t)
}

// With nothing else moving, a persistent error stops the drain after the stall
// limit and says why, in text the run status keeps verbatim.
func TestDrainStopsAfterTheStallLimitWithAClearAggregateError(t *testing.T) {
	q := newFakeQueue(&fakeJob{id: 1338, chain: "polygon", failEvery: true})
	summary, err := drainManualSweep(t.Context(), q, noPause())

	require.Error(t, err)
	require.Equal(t, sweepDrainStallRounds, q.passes)
	require.Equal(t, "sweep drain stopped: no progress after 3 rounds; failing: polygon#1338 balance_read/transport", err.Error())
	require.Equal(t, err.Error(), rpc.SafeError(err), "the operator must read this text, not the generic sentence")
	require.False(t, sweeper.IsFatalRunError(err))
	require.Len(t, summary.Entries, 1)
	require.Equal(t, "pending", summary.Entries[0].Status)
	require.Equal(t, 1, summary.Failed, "a job that still fails is a failure, so the console does not call the run clean")
	require.Zero(t, summary.Pending)
	require.Equal(t, sweepDrainStallRounds, summary.Failures[0].Count)
	// The failing job still produces its alert, once.
	events := sweepFailureEvents(summary, err)
	require.Len(t, events, 1)
	require.Contains(t, events[0].body, "环节：balance_read/transport")
}

// Progress elsewhere keeps the drain going; once only the failing job is left,
// the stall limit ends it. The error is nil because something advanced, and the
// failing job still alerts exactly once.
func TestDrainReturnsNilWhenOtherChainsAdvancedButStillAlertsTheFailingJob(t *testing.T) {
	q := newFakeQueue(append([]*fakeJob{{id: 1, chain: "polygon", failEvery: true}}, bscJobs(5, 10)...)...)
	summary, err := drainManualSweep(t.Context(), q, noPause())

	require.NoError(t, err)
	require.Equal(t, 5, summary.Swept)
	require.Equal(t, "pending", entryOf(t, summary, 1).Status)
	require.Equal(t, 1, summary.Failed, "the blocked job is counted as failed (and not as pending)")
	require.Zero(t, summary.Pending)
	events := sweepFailureEvents(summary, err)
	require.Len(t, events, 1, "one alert for the one job that still fails")
	// And sweep.executed fires, which the old abort never allowed.
	service := &Service{}
	capture := &capturingNotifier{}
	service.SetEventNotifier(capture)
	service.reportSweepExecuted(t.Context(), summary, err)
	require.Len(t, capture.sent(), 1)
	// The failing job was retried while the others drained; nothing was signed twice.
	require.Greater(t, summary.Failures[0].Count, sweepDrainStallRounds)
	q.assertNothingSentTwice(t)
}

func TestDrainStopsAtOnceOnAFatalError(t *testing.T) {
	for _, target := range []error{
		wallet.ErrSignerShareRequired, wallet.ErrSecurityLocked, wallet.ErrSigningDisabled,
		wallet.ErrSigningAuthorizationRequired, wallet.ErrWalletNotConfigured, wallet.ErrSignerRateLimited,
		mode.ErrReadOnly, sweeper.ErrSweepLockHeld,
		// Joined with a job error, the way Run returns it.
		errors.Join(transientRPC(), fmt.Errorf("sign: %w", wallet.ErrSignerShareRequired)),
	} {
		t.Run(strings.SplitN(target.Error(), "\n", 2)[0], func(t *testing.T) {
			q := newFakeQueue(bscJobs(4, 1)...)
			q.fatalAt, q.fatalErr = 3, target
			summary, err := drainManualSweep(t.Context(), q, noPause())

			require.Same(t, target, err, "the original error is returned unchanged so handlers can still map it")
			require.Equal(t, 3, q.passes, "no pass after a fatal error")
			require.NotNil(t, summary)
			require.Positive(t, summary.Advanced, "the progress before the stop is kept")
			q.assertNothingSentTwice(t)
		})
	}
}

// A locked signer stops the drain on the very first pass too, and nothing runs again.
func TestDrainStopsOnFirstPassWhenTheSignerIsLocked(t *testing.T) {
	q := newFakeQueue(bscJobs(3, 1)...)
	q.fatalAt, q.fatalErr = 1, wallet.ErrSignerShareRequired
	_, err := drainManualSweep(t.Context(), q, noPause())
	require.ErrorIs(t, err, wallet.ErrSignerShareRequired)
	require.Equal(t, 1, q.passes)
	require.Equal(t, 1, q.resets, "only the initial reset ran")
}

// An RPC call that timed out wraps context.DeadlineExceeded too. That is an
// ordinary job failure, not the end of the run.
func TestDrainTreatsAnRPCTimeoutAsAJobFailureNotAContextEnd(t *testing.T) {
	timeout := rpc.NewTransportFailure(context.DeadlineExceeded)
	require.ErrorIs(t, timeout, context.DeadlineExceeded, "the fixture must look like the real thing")
	q := newFakeQueue(&fakeJob{id: 1, chain: "polygon", failFor: 2, failErr: timeout}, &fakeJob{id: 2, chain: "bsc"})
	summary, err := drainManualSweep(t.Context(), q, noPause())
	require.NoError(t, err)
	require.Equal(t, 2, summary.Swept)
	require.Greater(t, q.passes, 3)
	require.Equal(t, rpc.CategoryTimeout, summary.Failures[0].Category)
}

func TestDrainStopsWhenTheRunContextEnds(t *testing.T) {
	t.Run("during a pass", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		q := newFakeQueue(bscJobs(4, 1)...)
		q.hook = func(pass int, _ *fakeQueue) {
			if pass == 2 {
				cancel()
			}
		}
		summary, err := drainManualSweep(ctx, q, noPause())
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, 2, q.passes)
		require.NotNil(t, summary)
	})
	t.Run("on the last round there is no pause to notice it", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		q := newFakeQueue(bscJobs(4, 1)...)
		q.hook = func(int, *fakeQueue) { cancel() }
		cfg := noPause()
		cfg.maxRounds = 1
		_, err := drainManualSweep(ctx, q, cfg)
		require.ErrorIs(t, err, context.Canceled, "running out of rounds must not hide that the run's context ended")
	})
	t.Run("a drain that finished its work keeps its success", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		q := newFakeQueue() // nothing to do
		q.hook = func(int, *fakeQueue) { cancel() }
		_, err := drainManualSweep(ctx, q, noPause())
		require.NoError(t, err, "the queue was empty: a context that ends a moment later changes nothing")
	})
	t.Run("a pass that also failed reports the context, and keeps what it saw", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		q := newFakeQueue(&fakeJob{id: 1, chain: "polygon", failEvery: true})
		q.hook = func(int, *fakeQueue) { cancel() }
		summary, err := drainManualSweep(ctx, q, noPause())
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, 1, q.passes)
		require.Equal(t, "pending", entryOf(t, summary, 1).Status, "the failing job is still in the summary and alerts")
	})
	t.Run("during the pause", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancel()
		q := newFakeQueue(bscJobs(4, 1)...)
		cfg := noPause()
		cfg.pause = time.Hour
		summary, err := drainManualSweep(ctx, q, cfg)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Equal(t, 1, q.passes, "the pause is cut short, no second pass")
		require.NotNil(t, summary)
	})
}

// scriptedRunner returns canned passes for the cases a job state machine cannot
// express.
type scriptedRunner struct {
	calls  int
	reset  func(call int) int // rows put back by the call-th reset (1 = the initial one)
	resets int
	script func(call int) (*sweeper.Summary, error)
}

func (r *scriptedRunner) ResetRetryableFailures(context.Context, int) (int, error) {
	r.resets++
	if r.reset != nil {
		return r.reset(r.resets), nil
	}
	return 0, nil
}

func (r *scriptedRunner) RunManualAndWait(context.Context, bool) (*sweeper.Summary, error) {
	r.calls++
	return r.script(r.calls)
}

func single(e sweeper.SweepEntry, set func(*sweeper.Summary)) *sweeper.Summary {
	s := &sweeper.Summary{}
	s.Add(e)
	if set != nil {
		set(s)
	}
	return s
}

// Progress detection edge cases.
func TestDrainProgressDetection(t *testing.T) {
	t.Run("waiting for the chain is not a stall", func(t *testing.T) {
		// Nine quiet rounds (a job waiting on confirmations), then it lands.
		r := &scriptedRunner{script: func(call int) (*sweeper.Summary, error) {
			if call <= 9 {
				return single(sweeper.SweepEntry{JobID: 1, Chain: "bsc", Status: "pending",
					Error: "awaiting canonical receipt and required confirmations"}, func(s *sweeper.Summary) { s.Planned = 1 }), nil
			}
			return single(sweeper.SweepEntry{JobID: 1, Chain: "bsc", Status: "swept", Amount: "5"},
				func(s *sweeper.Summary) { s.Advanced = 1 }), nil
		}}
		_, err := drainManualSweep(t.Context(), r, noPause())
		require.NoError(t, err)
		require.Equal(t, 10, r.calls, "a quiet wait must not trip the stall limit")
	})
	t.Run("a wait does not excuse a failure that never clears", func(t *testing.T) {
		// One job waits on confirmations that never arrive while another fails every
		// round and nothing advances: without this rule the failing job is retried
		// until the round cap.
		r := &scriptedRunner{script: func(int) (*sweeper.Summary, error) {
			s := &sweeper.Summary{Planned: 2}
			s.Add(sweeper.SweepEntry{JobID: 1, Chain: "bsc", Status: "pending", Error: "awaiting canonical receipt and required confirmations"})
			s.Add(sweeper.SweepEntry{JobID: 2, Chain: "polygon", Status: "pending", Error: "RPC connection failed",
				Step: sweeper.StepNonce, Category: rpc.CategoryTransport})
			return s, transientRPC()
		}}
		_, err := drainManualSweep(t.Context(), r, noPause())
		require.Equal(t, sweepDrainStallRounds, r.calls)
		require.EqualError(t, err, "sweep drain stopped: no progress after 3 rounds; failing: polygon#2 nonce/transport")
	})
	t.Run("a pending job with no reason is not a wait", func(t *testing.T) {
		// Back-pressure entries carry no reason. They must not keep a drain alive.
		r := &scriptedRunner{script: func(int) (*sweeper.Summary, error) {
			return single(sweeper.SweepEntry{JobID: 1, Chain: "bsc", Status: "pending"}, func(s *sweeper.Summary) { s.Planned = 1 }), nil
		}}
		_, err := drainManualSweep(t.Context(), r, noPause())
		require.NoError(t, err, "nothing failed")
		require.Equal(t, sweepDrainStallRounds, r.calls)
	})
	t.Run("a stall of exactly N rounds stops, N-1 does not", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			stallFor  int
			wantStops bool
		}{{"N-1", sweepDrainStallRounds - 1, false}, {"N", sweepDrainStallRounds, true}} {
			t.Run(tc.name, func(t *testing.T) {
				// Fails for stallFor rounds, then clears and sweeps.
				q := newFakeQueue(&fakeJob{id: 1, chain: "polygon", failFor: tc.stallFor})
				summary, err := drainManualSweep(t.Context(), q, noPause())
				if tc.wantStops {
					require.Error(t, err)
					require.Equal(t, "pending", summary.Entries[0].Status)
					return
				}
				require.NoError(t, err)
				require.Equal(t, "swept", q.job(1).stage)
			})
		}
	})
	t.Run("progress resets the stall counter", func(t *testing.T) {
		// Job 2 only advances on every third pass: two stalled rounds, one that
		// advances, again and again. Never N in a row, so the drain must go on
		// until job 2 is swept. Job 1 fails throughout.
		q := newFakeQueue(&fakeJob{id: 1, chain: "polygon", failEvery: true}, &fakeJob{id: 2, chain: "bsc"})
		q.hook = func(pass int, q *fakeQueue) { q.job(2).failEvery = pass%3 != 0 }
		_, err := drainManualSweep(t.Context(), q, noPause())
		require.NoError(t, err)
		require.Equal(t, "swept", q.job(2).stage)
		require.Greater(t, q.passes, 9)
	})
	t.Run("nothing to do ends the drain in one pass without an error", func(t *testing.T) {
		q := newFakeQueue()
		summary, err := drainManualSweep(t.Context(), q, noPause())
		require.NoError(t, err)
		require.Equal(t, 1, q.passes)
		require.Empty(t, summary.Entries)
	})
	t.Run("a parked row is not a failure of the run", func(t *testing.T) {
		// A legacy row that needs a human, an unswept balance behind it, nothing else.
		r := &scriptedRunner{script: func(int) (*sweeper.Summary, error) {
			return single(sweeper.SweepEntry{JobID: 9, Chain: "bsc", Status: "manual_review",
				Error: "legacy or ambiguous transaction; reconcile hashes manually before releasing address"},
				func(s *sweeper.Summary) { s.Planned = 1 }), nil
		}}
		summary, err := drainManualSweep(t.Context(), r, noPause())
		require.NoError(t, err, "a drain that had nothing to do is not an error just because a row is parked")
		require.Equal(t, sweepDrainStallRounds, r.calls, "but it must not spin until the round cap")
		require.Equal(t, 1, summary.Failed)
		require.Empty(t, summary.Failures, "a parked row has no step and is not a failed attempt of this run")
	})
	t.Run("rows put back keep the queue open", func(t *testing.T) {
		// Round 1 leaves a job pending; the reset before round 2 puts rows back, so
		// the empty-looking pass 2 must not end the drain.
		r := &scriptedRunner{
			reset: func(call int) int {
				if call == 2 {
					return 2
				}
				return 0
			},
			script: func(call int) (*sweeper.Summary, error) {
				if call == 1 {
					return single(sweeper.SweepEntry{JobID: 1, Chain: "bsc", Status: "pending"}, func(s *sweeper.Summary) { s.Planned = 1 }), nil
				}
				return &sweeper.Summary{}, nil
			},
		}
		_, err := drainManualSweep(t.Context(), r, noPause())
		require.NoError(t, err)
		require.Equal(t, 3, r.calls, "pass 2 followed a reset, so only pass 3 proves the queue empty")
	})
	t.Run("a sweep with no advance count is still progress", func(t *testing.T) {
		r := &scriptedRunner{script: func(call int) (*sweeper.Summary, error) {
			if call <= 5 {
				return single(sweeper.SweepEntry{JobID: int64(call), Chain: "bsc", Status: "swept", Amount: "1"},
					func(s *sweeper.Summary) { s.Planned = 1 }), nil
			}
			return &sweeper.Summary{}, nil
		}}
		_, err := drainManualSweep(t.Context(), r, noPause())
		require.NoError(t, err)
		require.Equal(t, 6, r.calls)
	})
	t.Run("the round cap ends a drain that keeps advancing", func(t *testing.T) {
		r := &scriptedRunner{script: func(int) (*sweeper.Summary, error) {
			return &sweeper.Summary{Planned: 1, Pending: 1, Advanced: 1}, nil
		}}
		_, err := drainManualSweep(t.Context(), r, noPause())
		require.NoError(t, err)
		require.Equal(t, sweepDrainMaxRounds, r.calls)
	})
	t.Run("a failed pass with empty counts does not prove the queue empty", func(t *testing.T) {
		// The pass could not even load the unfinished jobs, so every counter is zero.
		r := &scriptedRunner{script: func(int) (*sweeper.Summary, error) {
			return &sweeper.Summary{}, errors.New("fixture: could not load unfinished jobs")
		}}
		_, err := drainManualSweep(t.Context(), r, noPause())
		require.Error(t, err)
		require.Equal(t, sweepDrainStallRounds, r.calls, "retried until the stall limit, not declared empty")
	})
	t.Run("a pass that returns no summary is a failure, not an empty queue", func(t *testing.T) {
		r := &scriptedRunner{script: func(int) (*sweeper.Summary, error) { return nil, errors.New("fixture: planner exploded") }}
		summary, err := drainManualSweep(t.Context(), r, noPause())
		require.Error(t, err)
		require.Equal(t, sweepDrainStallRounds, r.calls)
		require.Equal(t, "sweep drain stopped: no progress after 3 rounds; failing: run/other", err.Error())
		require.Len(t, summary.Failures, 1, "an error no job explains is named, not lost")
		require.Equal(t, "run", summary.Failures[0].Step)
	})
}

func TestDrainResetFailureEndsTheDrain(t *testing.T) {
	t.Run("before the first pass nothing runs", func(t *testing.T) {
		q := newFakeQueue(bscJobs(2, 1)...)
		q.resetErr, q.resetErrAt = errors.New("fixture: reset failed"), 1
		summary, err := drainManualSweep(t.Context(), q, noPause())
		require.EqualError(t, err, "fixture: reset failed")
		require.Nil(t, summary, "as before: no summary when the first reset fails")
		require.Zero(t, q.passes)
	})
	t.Run("mid-run the progress so far is returned", func(t *testing.T) {
		q := newFakeQueue(bscJobs(3, 1)...)
		q.resetErr, q.resetErrAt = errors.New("fixture: reset failed"), 3
		summary, err := drainManualSweep(t.Context(), q, noPause())
		require.EqualError(t, err, "fixture: reset failed")
		require.Equal(t, 2, q.passes)
		require.Positive(t, summary.Advanced)
	})
}

// The labels in the aggregate error and the log line name where a failure is.
func TestFailureLabel(t *testing.T) {
	for _, tc := range []struct {
		f    sweeper.SweepFailure
		want string
	}{
		{sweeper.SweepFailure{JobID: 1338, Chain: "polygon", Step: "balance_read", Category: "transport"}, "polygon#1338 balance_read/transport"},
		{sweeper.SweepFailure{Chain: "bsc", Step: "validate", Category: "other"}, "bsc validate/other"},
		{sweeper.SweepFailure{Step: "run", Category: "storage"}, "run/storage"},
	} {
		require.Equal(t, tc.want, failureLabel(tc.f))
	}
}

// The merged summary is bounded and de-duplicated however long the drain runs.
func TestDrainSummaryIsBoundedAndDeduplicated(t *testing.T) {
	failing := func(id int64) sweeper.SweepEntry {
		return sweeper.SweepEntry{JobID: id, Chain: "bsc", Status: "pending", Error: "RPC connection failed",
			Step: sweeper.StepGasPrice, Category: rpc.CategoryTransport}
	}
	t.Run("a job failing in every round is one failure with a count", func(t *testing.T) {
		d := newSweepDrain()
		for round := 0; round < 40; round++ {
			d.observe(single(failing(5), func(s *sweeper.Summary) { s.Planned = 1 }), errors.New("fixture"))
		}
		summary := d.summary()
		require.Len(t, summary.Entries, 1)
		require.Len(t, summary.Failures, 1)
		require.Equal(t, 40, summary.Failures[0].Count)
	})
	t.Run("the failure list is capped and the overflow is counted", func(t *testing.T) {
		d := newSweepDrain()
		s := &sweeper.Summary{}
		for id := int64(1); id <= 200; id++ {
			s.Add(failing(id))
		}
		for round := 0; round < 3; round++ {
			d.observe(s, errors.New("fixture"))
		}
		summary := d.summary()
		require.Len(t, summary.Failures, sweepDrainMaxFailures)
		require.Equal(t, 200-sweepDrainMaxFailures, summary.FailuresOmitted, "each omitted job is counted once, not once per round")
		require.Len(t, summary.Entries, 200, "entries are the jobs themselves and stay complete")
		// The aggregate error stays inside what the run status keeps verbatim.
		err := d.stalledError()
		require.LessOrEqual(t, len(err.Error()), 160)
		require.Equal(t, err.Error(), rpc.SafeError(err))
		require.Contains(t, err.Error(), "(+")
	})
	t.Run("a swept job and a later job on the same address both stay", func(t *testing.T) {
		d := newSweepDrain()
		d.observe(single(sweeper.SweepEntry{JobID: 1, Chain: "bsc", Address: "0xA", Token: "usdt", Status: "swept", Amount: "100"}, nil), nil)
		d.observe(single(sweeper.SweepEntry{JobID: 2, Chain: "bsc", Address: "0xA", Token: "usdt", Status: "swept", Amount: "40"}, nil), nil)
		summary := d.summary()
		require.Equal(t, 2, summary.Swept)
		require.Len(t, summary.Entries, 2, "the first sweep must not be overwritten: its amount is reported")
	})
	t.Run("a job that now exists replaces its placeholder", func(t *testing.T) {
		d := newSweepDrain()
		d.observe(single(sweeper.SweepEntry{Chain: "bsc", Address: "0xA", Token: "usdt", Status: "failed", Error: "invalid sweep plan",
			Step: sweeper.StepValidate, Category: sweeper.CategoryOther}, nil), errors.New("fixture"))
		d.observe(single(sweeper.SweepEntry{JobID: 3, Chain: "bsc", Address: "0xA", Token: "usdt", Status: "swept", Amount: "7"}, nil), nil)
		summary := d.summary()
		require.Len(t, summary.Entries, 1)
		require.Equal(t, int64(3), summary.Entries[0].JobID)
		require.Empty(t, sweepFailureEvents(summary, nil), "the stale placeholder must not alert for ever")
	})
	t.Run("no node text reaches the aggregate error or the failures", func(t *testing.T) {
		d := newSweepDrain()
		d.observe(single(sweeper.SweepEntry{JobID: 4, Chain: "bsc", Status: "pending",
			Error: "https://node.example/fixture-secret-path?api_key=fixture-secret-key 0x" + strings.Repeat("a", 64),
			Step:  sweeper.StepReceipt, Category: rpc.CategoryTransport}, nil), errors.New("fixture-secret-driver-message"))
		text := d.stalledError().Error()
		for _, f := range d.summary().Failures {
			text += fmt.Sprintf("%+v", f)
		}
		require.NotContains(t, text, "fixture-")
		require.NotContains(t, text, "https://")
		require.NotContains(t, text, "0x")
	})
}
