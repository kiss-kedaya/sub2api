package cryptopay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/sweeper"
)

// The manual drain turns one operator click into as many passes as it takes to
// empty the queue. Gas top-ups are serialized by the payer nonce, so a single
// pass only sweeps the addresses whose gas confirmed while it ran.
const (
	sweepDrainMaxRounds = 60
	// sweepDrainStallRounds is how many consecutive rounds may move nothing
	// before the drain gives up. A round that moves nothing and fails nothing but
	// only waits for the chain does not count: confirmations are the normal
	// reason for a quiet round.
	sweepDrainStallRounds = 3
	// Give the previous round's gas top-ups time to mine; without this pause
	// every extra round just sees the same pending gas.
	sweepDrainPause      = 8 * time.Second
	sweepDrainResetLimit = 200
	// sweepDrainMaxFailures bounds the distinct failures one run carries.
	sweepDrainMaxFailures = 50
	// sweepDrainLogFailures bounds the failures named on the one log line.
	sweepDrainLogFailures = 10
	// sweepDrainErrorMax keeps the aggregate message inside what rpc.SafeError
	// keeps verbatim (160 bytes), so the operator reads it as written.
	sweepDrainErrorMax    = 150
	sweepDrainErrorPrefix = "sweep drain stopped: "
)

// sweepPassRunner is what the drain needs from the executor. *sweeper.Executor
// satisfies it; tests substitute a fake, so the loop runs without a chain or a
// database.
type sweepPassRunner interface {
	ResetRetryableFailures(ctx context.Context, limit int) (int, error)
	RunManualAndWait(ctx context.Context, dryRun bool) (*sweeper.Summary, error)
}

type sweepDrainConfig struct {
	maxRounds   int
	stallRounds int
	pause       time.Duration
}

var defaultSweepDrain = sweepDrainConfig{
	maxRounds: sweepDrainMaxRounds, stallRounds: sweepDrainStallRounds, pause: sweepDrainPause,
}

// drainManualSweep runs passes until the queue is empty, nothing has moved for
// stallRounds rounds, a fatal error ends the run, or the context does.
//
// A job-level failure (RPC trouble, one job's step failing) never aborts the
// drain: the pass already carries on with every other job and chain, and the
// next round retries the failing one. Only an error after which no further pass
// could do anything (a locked or disabled signer, a read-only process, another
// instance holding the sweep lock) or the end of the run's own context returns
// at once; the state is then left exactly as the last pass left it.
//
// The returned error is nil whenever anything advanced or nothing failed: the
// failures that remain are in the summary (entries, failures) and produce their
// own alerts. It is an aggregate error only when nothing advanced and
// something is still failing.
func drainManualSweep(ctx context.Context, run sweepPassRunner, cfg sweepDrainConfig) (*sweeper.Summary, error) {
	// First put back failures that never produced a transaction (safe: nothing
	// was broadcast). Broadcast failures stay untouched: retrying those needs an
	// on-chain check, not a guess.
	if _, err := run.ResetRetryableFailures(ctx, sweepDrainResetLimit); err != nil {
		return nil, err
	}
	d := newSweepDrain()
	stalled := 0
	for round := 0; round < cfg.maxRounds; round++ {
		reset := 0
		if round > 0 {
			var err error
			if reset, err = run.ResetRetryableFailures(ctx, sweepDrainResetLimit); err != nil {
				return d.finish("reset_failed", err)
			}
			select {
			case <-time.After(cfg.pause):
			case <-ctx.Done():
				return d.finish("context", ctx.Err())
			}
		}
		pass, perr := run.RunManualAndWait(ctx, false)
		moved, waiting, failing := d.observe(pass, perr)
		// A clean pass that leaves nothing pending or planned ends the drain, even
		// if the run's context ran out a moment later: the work is done. A pass
		// that returned an error may have stopped early, so its counts do not prove
		// the queue is empty.
		if pass != nil && perr == nil && pass.Pending == 0 && pass.Planned == 0 && reset == 0 {
			return d.finish("queue_empty", nil)
		}
		// The run's own context is asked, not the error: an RPC call that timed
		// out wraps context.DeadlineExceeded too, and that is an ordinary job
		// failure. When the context is over, that is the reason the drain ends;
		// what the last pass saw is in the summary.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return d.finish("context", ctxErr)
		}
		if sweeper.IsFatalRunError(perr) {
			return d.finish("fatal", perr)
		}
		// A round stalls when nothing advanced and either something failed or
		// nothing is merely waiting for the chain. A wait does not excuse a
		// failure: a job that fails every round while another one waits would
		// otherwise be retried until the round cap.
		if moved {
			stalled = 0
		} else if failing || !waiting {
			stalled++
		}
		if stalled >= cfg.stallRounds {
			return d.finish("stalled", nil)
		}
	}
	return d.finish("round_cap", nil)
}

// sweepDrain accumulates the rounds of one drain into a single summary: the
// latest state of every job (not one row per job per round), the counters
// rebuilt from those states, and the distinct failures that happened on the way.
type sweepDrain struct {
	entries  []sweeper.SweepEntry
	index    map[string]int
	planned  int
	advanced int
	rounds   int
	// progressed is true once any round advanced a job; blocked describes the
	// last round only, so a failure that cleared does not count.
	progressed bool
	blocked    bool
	failures   []drainFailure
	failureAt  map[string]int
	omitted    map[string]struct{}
}

type drainFailure struct {
	sweeper.SweepFailure
	last int // the round it was last seen in
}

func newSweepDrain() *sweepDrain {
	return &sweepDrain{index: map[string]int{}, failureAt: map[string]int{}, omitted: map[string]struct{}{}}
}

// entryKey identifies a job; an entry that never became a job is identified by
// its address and asset instead.
func entryKey(e sweeper.SweepEntry) string {
	if e.JobID > 0 {
		return "job:" + strconv.FormatInt(e.JobID, 10)
	}
	return addressEntryKey(e)
}

func addressEntryKey(e sweeper.SweepEntry) string {
	return "addr:" + strings.ToLower(e.Chain+"|"+e.Address+"|"+e.Token)
}

// put keeps the latest entry per job. Jobs are keyed by id, never by address:
// an address that is swept and then receives new deposits gets a second job, and
// the first sweep must stay in the summary.
func (d *sweepDrain) put(e sweeper.SweepEntry) {
	key := entryKey(e)
	if i, ok := d.index[key]; ok {
		d.entries[i] = e
		return
	}
	if e.JobID > 0 {
		// The address failed before it had a job, and has one now: replace the
		// placeholder, or it would be reported as failing for ever.
		if i, ok := d.index[addressEntryKey(e)]; ok {
			d.entries[i] = e
			delete(d.index, addressEntryKey(e))
			d.index[key] = i
			return
		}
	}
	d.index[key] = len(d.entries)
	d.entries = append(d.entries, e)
}

// observe merges one pass. moved reports that some job advanced (the executor
// counts those); waiting that some job only waits for the chain; failing that a
// job (or the run) failed an attempt in this pass.
func (d *sweepDrain) observe(pass *sweeper.Summary, perr error) (moved, waiting, failing bool) {
	d.rounds++
	failing = perr != nil
	explained := false
	if pass != nil {
		d.planned = max(d.planned, pass.Planned)
		d.advanced += pass.Advanced
		moved = pass.Advanced > 0 || pass.Swept > 0
		for _, e := range pass.Entries {
			d.put(e)
			switch {
			case e.NeedsAttention() && e.Step != "":
				// A failed attempt of this run. A parked row (a legacy record, a
				// released failure) has no step and is not one.
				failing, explained = true, true
				d.fail(entryKey(e), sweeper.SweepFailure{JobID: e.JobID, Chain: e.Chain, Step: e.Step, Category: e.Category})
			case e.Waiting():
				waiting = true
			}
		}
	}
	if perr != nil && !explained {
		// An error no job explains (the mined-sweep recovery, the planner).
		category := sweeper.FailureCategory(perr)
		d.fail("run|"+category, sweeper.SweepFailure{Step: "run", Category: category})
	}
	d.blocked = failing
	d.progressed = d.progressed || moved
	return moved, waiting, failing
}

// fail counts one failure in the current round, once per distinct key.
func (d *sweepDrain) fail(key string, f sweeper.SweepFailure) {
	key += "|" + f.Step + "/" + f.Category
	if i, ok := d.failureAt[key]; ok {
		if d.failures[i].last != d.rounds {
			d.failures[i].Count++
			d.failures[i].last = d.rounds
		}
		return
	}
	if len(d.failures) >= sweepDrainMaxFailures {
		d.omitted[key] = struct{}{}
		return
	}
	f.Count = 1
	d.failureAt[key] = len(d.failures)
	d.failures = append(d.failures, drainFailure{SweepFailure: f, last: d.rounds})
}

// summary rebuilds the counters from the final state of every job, so a job
// that failed in five rounds is one failure, not five. A pending job that still
// carries a failure is counted as failed, not pending: the operator's summary
// (and the console's "partial" status, which reads the failed count) must not
// call a run clean while a job is still blocked.
func (d *sweepDrain) summary() *sweeper.Summary {
	out := &sweeper.Summary{Planned: d.planned, Advanced: d.advanced}
	for _, e := range d.entries {
		out.Add(e)
		if e.Status == "pending" && e.NeedsAttention() {
			out.Pending--
			out.Failed++
		}
	}
	for _, f := range d.failures {
		out.Failures = append(out.Failures, f.SweepFailure)
	}
	out.FailuresOmitted = len(d.omitted)
	return out
}

// unresolved lists the failures of the last round.
func (d *sweepDrain) unresolved() []sweeper.SweepFailure {
	var out []sweeper.SweepFailure
	for _, f := range d.failures {
		if f.last == d.rounds {
			out = append(out, f.SweepFailure)
		}
	}
	return out
}

func failureLabel(f sweeper.SweepFailure) string {
	code := f.Step + "/" + f.Category
	switch {
	case f.JobID > 0:
		return f.Chain + "#" + strconv.FormatInt(f.JobID, 10) + " " + code
	case f.Chain != "":
		return f.Chain + " " + code
	}
	return code
}

// stalledError is the aggregate error of a drain that moved nothing while
// something still fails. Its text is built from chain names, job ids and the
// sweeper's fixed step/category words only, and fits what rpc.SafeError keeps
// verbatim, so the operator reads exactly this instead of the generic sentence.
func (d *sweepDrain) stalledError() error {
	rounds := strconv.Itoa(d.rounds) + " rounds"
	if d.rounds == 1 {
		rounds = "1 round"
	}
	text := sweepDrainErrorPrefix + "no progress after " + rounds
	list := d.unresolved()
	if len(list) == 0 {
		return errors.New(text + "; see the run summary")
	}
	text += "; failing:"
	shown := 0
	for _, f := range list {
		sep := " "
		if shown > 0 {
			sep = ", "
		}
		next := text + sep + failureLabel(f)
		tail := ""
		if left := len(list) - shown - 1; left > 0 {
			tail = fmt.Sprintf(" (+%d more)", left)
		}
		if shown > 0 && len(next)+len(tail) > sweepDrainErrorMax {
			break
		}
		text = next
		shown++
	}
	if shown < len(list) {
		text += fmt.Sprintf(" (+%d more)", len(list)-shown)
	}
	return errors.New(text)
}

// finish returns the summary and the error, and writes the one line per click
// that says how the drain ended. It is a single line on purpose: the process
// samples repeated log lines, and per-job detail is in sweep_records.error.
func (d *sweepDrain) finish(reason string, err error) (*sweeper.Summary, error) {
	summary := d.summary()
	if err == nil && !d.progressed && d.blocked {
		err = d.stalledError()
	}
	level := slog.LevelInfo
	if err != nil || d.blocked {
		level = slog.LevelError
	}
	labels := make([]string, 0, sweepDrainLogFailures)
	for _, f := range d.unresolved() {
		if len(labels) == sweepDrainLogFailures {
			break
		}
		labels = append(labels, failureLabel(f))
	}
	slog.Log(context.Background(), level, "cryptopay.sweep.drain_finished",
		"reason", reason, "rounds", d.rounds, "swept", summary.Swept, "advanced", d.advanced,
		"failed", summary.Failed, "pending", summary.Pending,
		"failing", strings.Join(labels, "; "), "error", rpc.SafeError(err))
	return summary, err
}
