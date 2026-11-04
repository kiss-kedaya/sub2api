package cryptopay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay/mode"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/rpc"
	"github.com/Wei-Shaw/sub2api/internal/cryptopay/scanner"
	"github.com/google/uuid"
)

// ScanRunStatus is the lifecycle state of one asynchronous catch-up scan.
type ScanRunStatus string

const (
	ScanRunRunning   ScanRunStatus = "running"
	ScanRunCompleted ScanRunStatus = "completed"
	ScanRunFailed    ScanRunStatus = "failed"
	ScanRunCancelled ScanRunStatus = "cancelled"
)

// Stop reasons are fixed codes the UI localises. They never carry free text.
const (
	ScanStopCaughtUp  = "caught_up"
	ScanStopStalled   = "stalled"
	ScanStopDeadline  = "deadline"
	ScanStopCancelled = "cancelled"
	ScanStopError     = "error"
	ScanStopInternal  = "internal"
)

var (
	// ErrScanJobRunning is returned when a catch-up scan is already active.
	ErrScanJobRunning = errors.New("a catch-up scan is already running")
	// ErrScanUnavailable means the payment channel or its scanner is off.
	ErrScanUnavailable = errors.New("enable payment channel before scanning")
	// ErrScanRunNotFound and ErrScanRunFinished are the cancel endpoint's outcomes.
	ErrScanRunNotFound = errors.New("scan run not found")
	ErrScanRunFinished = errors.New("scan run already finished")

	errScanCancelled = errors.New("scan cancelled by operator")
)

// ScanChainState is one chain's catch-up position as shown to the operator.
type ScanChainState struct {
	Chain         string `json:"chain"`
	Cursor        uint64 `json:"cursor"`
	ConfirmedHead uint64 `json:"confirmed_head"`
	Remaining     uint64 `json:"remaining"`
	HeadKnown     bool   `json:"head_known"`
}

// ScanRunState is the in-memory record of one asynchronous catch-up scan.
//
// Like SweepRunState it describes one process's current work and is lost on
// restart. The durable record is the scan cursor and the deposits table; a
// lost run is simply started again from the persisted cursor.
type ScanRunState struct {
	ID            string           `json:"id"`
	Status        ScanRunStatus    `json:"status"`
	StartedAt     time.Time        `json:"started_at"`
	FinishedAt    *time.Time       `json:"finished_at,omitempty"`
	FromBlock     uint64           `json:"from_block"`
	Rounds        int              `json:"rounds"`
	BusyRetries   int              `json:"busy_retries"`
	StalledRounds int              `json:"stalled_rounds"`
	Chains        []ScanChainState `json:"chains"`
	LastError     string           `json:"last_error,omitempty"`
	StopReason    string           `json:"stop_reason,omitempty"`

	seq uint64
}

func (s *ScanRunState) clone() *ScanRunState {
	out := *s
	out.Chains = append([]ScanChainState{}, s.Chains...)
	if s.FinishedAt != nil {
		finished := *s.FinishedAt
		out.FinishedAt = &finished
	}
	return &out
}

// scanRunner is the small seam the catch-up loop depends on, so the loop can
// be exercised without a database or RPC node.
type scanRunner interface {
	// Round runs one bounded scan pass. fromBlock is non-zero only for the
	// operator's explicit rewind; every other pass must continue from the
	// persisted cursor.
	Round(ctx context.Context, fromBlock uint64) error
	// Progress reads the durable cursor and last sampled head of each chain.
	Progress(ctx context.Context) ([]scanner.ChainScanProgress, error)
}

// scanLoopConfig holds the loop's timing. Tests shrink it; production uses
// defaultScanLoopConfig.
type scanLoopConfig struct {
	Deadline   time.Duration // total budget for the whole job
	Pause      time.Duration // yield between two executed rounds
	BusyMin    time.Duration // first wait after ErrScanBusy
	BusyMax    time.Duration
	ErrorMin   time.Duration // first wait after a round that failed without progress
	ErrorMax   time.Duration
	MaxStalled int // consecutive rounds without cursor movement before giving up
}

func defaultScanLoopConfig() scanLoopConfig {
	return scanLoopConfig{
		Deadline: 2 * time.Hour, Pause: time.Second,
		BusyMin: time.Second, BusyMax: 10 * time.Second,
		ErrorMin: 2 * time.Second, ErrorMax: 15 * time.Second,
		MaxStalled: 5,
	}
}

// scanRunRetention bounds how many finished runs stay queryable.
const scanRunRetention = 20

type scanJob struct {
	state  *ScanRunState
	cancel context.CancelCauseFunc
	done   chan struct{} // closed after the goroutine has fully exited
}

// scanRegistry owns every run and enforces "one catch-up scan at a time".
type scanRegistry struct {
	mu     sync.Mutex
	jobs   map[string]*scanJob
	active *scanJob
	seq    uint64
}

// scanJobs is process-wide on purpose: the scan lock it guards is process-wide
// (and cluster-wide through the advisory lock), not per Service value.
var scanJobs = &scanRegistry{jobs: map[string]*scanJob{}}

// start launches the loop in the background and returns a snapshot of the new
// run. parent only supplies values (trace ids); cancellation of the request
// that started the run is deliberately dropped so a closed browser tab cannot
// stop it.
func (r *scanRegistry) start(parent context.Context, runner scanRunner, cfg scanLoopConfig, fromBlock uint64) (*ScanRunState, error) {
	r.mu.Lock()
	if r.active != nil {
		r.mu.Unlock()
		return nil, ErrScanJobRunning
	}
	base, cancelCause := context.WithCancelCause(context.WithoutCancel(parent))
	ctx, cancelTimeout := context.WithTimeout(base, cfg.Deadline)
	r.seq++
	job := &scanJob{
		state: &ScanRunState{
			ID: "scan-" + uuid.NewString(), Status: ScanRunRunning,
			StartedAt: time.Now().UTC(), FromBlock: fromBlock,
			Chains: []ScanChainState{}, seq: r.seq,
		},
		cancel: cancelCause,
		done:   make(chan struct{}),
	}
	r.jobs[job.state.ID] = job
	r.active = job
	r.trimLocked()
	initial := job.state.clone()
	r.mu.Unlock()

	go func() {
		defer close(job.done)
		defer cancelTimeout()
		defer cancelCause(nil)
		r.run(ctx, job, runner, cfg)
	}()
	return initial, nil
}

// trimLocked drops the oldest finished runs beyond the retention bound. The
// active run is never dropped.
func (r *scanRegistry) trimLocked() {
	finished := make([]*scanJob, 0, len(r.jobs))
	for _, job := range r.jobs {
		if job != r.active {
			finished = append(finished, job)
		}
	}
	if len(finished) <= scanRunRetention {
		return
	}
	sort.Slice(finished, func(i, j int) bool { return finished[i].state.seq < finished[j].state.seq })
	for _, job := range finished[:len(finished)-scanRunRetention] {
		delete(r.jobs, job.state.ID)
	}
}

// update mutates a run's state while it is still running.
func (r *scanRegistry) update(job *scanJob, mutate func(*ScanRunState)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if job.state.Status == ScanRunRunning {
		mutate(job.state)
	}
}

// finish moves a running run to its terminal state exactly once.
func (r *scanRegistry) finish(job *scanJob, status ScanRunStatus, reason, errText string) {
	r.mu.Lock()
	if job.state.Status != ScanRunRunning {
		r.mu.Unlock()
		return
	}
	now := time.Now().UTC()
	job.state.Status = status
	job.state.StopReason = reason
	job.state.FinishedAt = &now
	if errText != "" {
		job.state.LastError = errText
	}
	if r.active == job {
		r.active = nil
	}
	id, rounds := job.state.ID, job.state.Rounds
	r.mu.Unlock()
	slog.Info("[cryptopay] catch-up scan finished", "run", id, "status", string(status), "reason", reason, "rounds", rounds)
}

// finishContext maps a finished job context to its terminal state.
func (r *scanRegistry) finishContext(ctx context.Context, job *scanJob) {
	if errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
		r.finish(job, ScanRunFailed, ScanStopDeadline, "")
		return
	}
	r.finish(job, ScanRunCancelled, ScanStopCancelled, "")
}

func (r *scanRegistry) get(id string) *ScanRunState {
	r.mu.Lock()
	defer r.mu.Unlock()
	if job, ok := r.jobs[id]; ok {
		return job.state.clone()
	}
	return nil
}

// list returns every known run, newest first.
func (r *scanRegistry) list() []*ScanRunState {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*ScanRunState, 0, len(r.jobs))
	for _, job := range r.jobs {
		out = append(out, job.state.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq > out[j].seq })
	return out
}

// cancel asks a running run to stop. The run reaches "cancelled" when its
// goroutine notices the context, so the returned snapshot may still say
// "running"; callers keep polling.
func (r *scanRegistry) cancel(id string) (*ScanRunState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	job, ok := r.jobs[id]
	if !ok {
		return nil, ErrScanRunNotFound
	}
	if job.state.Status != ScanRunRunning {
		return nil, ErrScanRunFinished
	}
	job.cancel(errScanCancelled)
	return job.state.clone(), nil
}

// scanLoop is the catch-up loop of one run. It is the only writer of the run's
// terminal state, so every exit ends in exactly one registry.finish.
type scanLoop struct {
	reg    *scanRegistry
	job    *scanJob
	runner scanRunner
	cfg    scanLoopConfig
	id     string
	// nextFrom is the rewind for the next executed round. Only the first
	// executed round may carry the operator's from_block; passing it again
	// would rewind the cursor every round and the scan could never finish.
	nextFrom uint64
	rounds   int
	stalled  int
	busy     *backoff
	failing  *backoff
}

func (r *scanRegistry) run(ctx context.Context, job *scanJob, runner scanRunner, cfg scanLoopConfig) {
	l := &scanLoop{
		reg: r, job: job, runner: runner, cfg: cfg, id: job.state.ID,
		nextFrom: job.state.FromBlock,
		busy:     newBackoff(cfg.BusyMin, cfg.BusyMax),
		failing:  newBackoff(cfg.ErrorMin, cfg.ErrorMax),
	}
	defer func() {
		if p := recover(); p != nil {
			// Type only: a panic value can embed an RPC URL or a driver DSN.
			slog.Error("[cryptopay] catch-up scan panicked", "run", l.id, "panic_type", fmt.Sprintf("%T", p))
			r.finish(job, ScanRunFailed, ScanStopInternal, "Payment scan failed unexpectedly; check deposit records before retrying")
		}
	}()
	for !l.step(ctx) {
	}
}

// stop finishes the run from its already-done context.
func (l *scanLoop) stop(ctx context.Context) bool {
	l.reg.finishContext(ctx, l.job)
	return true
}

// step runs one iteration and reports whether the run has ended.
func (l *scanLoop) step(ctx context.Context) bool {
	if ctx.Err() != nil {
		return l.stop(ctx)
	}
	// Read the cursors immediately before the round so the comparison below is
	// attributable to this round and not to a pass that ran while we waited.
	before, beforeErr := l.runner.Progress(ctx)
	if beforeErr == nil {
		l.reg.update(l.job, func(s *ScanRunState) { s.Chains = scanChainStates(before) })
	}
	roundErr := l.runner.Round(ctx, l.nextFrom)
	if ctx.Err() != nil {
		return l.stop(ctx)
	}
	if errors.Is(roundErr, ErrScanBusy) {
		// The background scanner (or a sweep/reload) holds the lock. That is
		// contention, not failure: it does not count as a round or a stall.
		l.reg.update(l.job, func(s *ScanRunState) { s.BusyRetries++ })
		if !sleepCtx(ctx, l.busy.next()) {
			return l.stop(ctx)
		}
		return false
	}
	l.busy.reset()
	l.rounds++
	if isTerminalScanError(roundErr) {
		l.reg.update(l.job, func(s *ScanRunState) { s.Rounds = l.rounds })
		l.reg.finish(l.job, ScanRunFailed, ScanStopError, scanSafeError(roundErr))
		return true
	}
	after, afterErr := l.runner.Progress(ctx)
	if ctx.Err() != nil {
		return l.stop(ctx)
	}
	moved := beforeErr == nil && afterErr == nil && cursorsMoved(before, after)
	return l.settle(ctx, after, afterErr, roundErr, moved)
}

// settle records a finished round and decides whether the run ends.
func (l *scanLoop) settle(ctx context.Context, after []scanner.ChainScanProgress, afterErr, roundErr error, moved bool) bool {
	rewound := l.nextFrom != 0
	l.nextFrom = 0 // every later round continues from the persisted cursor
	l.record(after, afterErr, roundErr)
	if rewound && roundErr != nil && !moved {
		// The rewind may never have been persisted. Carrying on from the old
		// cursor would finish "successfully" without scanning the range the
		// operator asked for, so fail and let them start it again.
		l.reg.finish(l.job, ScanRunFailed, ScanStopError, "")
		return true
	}
	caughtUp := roundErr == nil && afterErr == nil && allCaughtUp(after)
	if moved || caughtUp {
		l.stalled = 0
	} else {
		l.stalled++
	}
	l.reg.update(l.job, func(s *ScanRunState) { s.StalledRounds = l.stalled })
	if caughtUp {
		l.reg.finish(l.job, ScanRunCompleted, ScanStopCaughtUp, "")
		return true
	}
	if l.stalled >= l.cfg.MaxStalled {
		cause := roundErr
		if cause == nil {
			cause = afterErr
		}
		slog.Warn("[cryptopay] catch-up scan stopped: cursor did not advance",
			"run", l.id, "rounds", l.rounds, "stalled_rounds", l.stalled,
			"cause", scanSafeError(cause), "chains", summarizeScan(after))
		l.reg.finish(l.job, ScanRunFailed, ScanStopStalled, "")
		return true
	}
	wait := l.cfg.Pause
	if roundErr != nil && !moved {
		wait = max(wait, l.failing.next())
	} else {
		l.failing.reset()
	}
	if !sleepCtx(ctx, wait) {
		return l.stop(ctx)
	}
	return false
}

func (l *scanLoop) record(after []scanner.ChainScanProgress, afterErr, roundErr error) {
	l.reg.update(l.job, func(s *ScanRunState) {
		s.Rounds = l.rounds
		if afterErr == nil {
			s.Chains = scanChainStates(after)
		}
		switch {
		case roundErr != nil:
			s.LastError = scanSafeError(roundErr)
		case afterErr != nil:
			s.LastError = scanSafeError(afterErr)
		}
	})
}

// backoff doubles from lo up to hi. It is only touched by the run goroutine.
type backoff struct{ lo, hi, cur time.Duration }

func newBackoff(lo, hi time.Duration) *backoff { return &backoff{lo: lo, hi: hi, cur: lo} }

func (b *backoff) next() time.Duration {
	wait := b.cur
	b.cur = min(b.cur*2, b.hi)
	return wait
}

func (b *backoff) reset() { b.cur = b.lo }

// sleepCtx waits for d and reports false when ctx ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// cursorsMoved reports whether any chain's durable cursor differs from before.
// A chain that was not measured before cannot show movement (fail closed).
func cursorsMoved(before, after []scanner.ChainScanProgress) bool {
	prior := make(map[string]uint64, len(before))
	for _, p := range before {
		prior[p.Chain] = p.Cursor
	}
	for _, p := range after {
		if old, ok := prior[p.Chain]; ok && old != p.Cursor {
			return true
		}
	}
	return false
}

// allCaughtUp is true when every scanned chain's cursor reached its confirmed
// head. No scanned chain at all is vacuously caught up, as in the sync scan.
func allCaughtUp(progress []scanner.ChainScanProgress) bool {
	for _, p := range progress {
		if !p.CaughtUp() {
			return false
		}
	}
	return true
}

func scanChainStates(progress []scanner.ChainScanProgress) []ScanChainState {
	out := make([]ScanChainState, 0, len(progress))
	for _, p := range progress {
		out = append(out, ScanChainState{
			Chain: p.Chain, Cursor: p.Cursor, ConfirmedHead: p.ConfirmedHead,
			Remaining: p.Remaining(), HeadKnown: p.HeadKnown,
		})
	}
	return out
}

// summarizeScan renders chain names and block numbers only, safe for logs.
func summarizeScan(progress []scanner.ChainScanProgress) string {
	parts := make([]string, 0, len(progress))
	for _, p := range progress {
		parts = append(parts, fmt.Sprintf("%s cursor=%d confirmed=%d remaining=%d known=%t",
			p.Chain, p.Cursor, p.ConfirmedHead, p.Remaining(), p.HeadKnown))
	}
	return strings.Join(parts, "; ")
}

// isTerminalScanError marks failures a retry cannot fix inside this run.
func isTerminalScanError(err error) bool {
	return errors.Is(err, mode.ErrReadOnly) || errors.Is(err, ErrScanUnavailable)
}

// scanSafeError is the only error-string boundary for a run's last_error. It
// never forwards err.Error() directly: scan errors can wrap RPC URLs or
// database driver text, so unknown causes collapse to rpc.SafeError's fixed
// wording.
func scanSafeError(err error) string {
	var failure *rpc.Failure
	switch {
	case err == nil:
		return ""
	case errors.Is(err, mode.ErrReadOnly):
		return "Payment scanning is read-only on this instance"
	case errors.Is(err, ErrScanUnavailable):
		return ErrScanUnavailable.Error()
	case errors.Is(err, context.DeadlineExceeded) && !errors.As(err, &failure):
		return "Chain scan budget exceeded; progress so far is kept"
	}
	return rpc.SafeError(err)
}

// scanWorker returns the live scanner worker, or ErrScanUnavailable.
func (s *Service) scanWorker() (*scanner.Worker, error) {
	if s == nil {
		return nil, ErrScanUnavailable
	}
	s.mu.RLock()
	enabled, worker := s.config.Enabled, s.worker
	s.mu.RUnlock()
	if !enabled || worker == nil {
		return nil, ErrScanUnavailable
	}
	return worker, nil
}

// serviceScanRunner adapts Service to the loop. It re-resolves the worker on
// every call because a config save replaces the worker mid-run.
type serviceScanRunner struct{ s *Service }

func (r serviceScanRunner) Round(ctx context.Context, fromBlock uint64) error {
	if err := mode.CheckWritable(); err != nil {
		return err
	}
	if _, err := r.s.scanWorker(); err != nil {
		return err
	}
	// RunScan takes sweepMu for this one round only, so sweeps and config
	// reloads are never blocked for the whole job.
	return r.s.RunScan(ctx, fromBlock)
}

func (r serviceScanRunner) Progress(ctx context.Context) ([]scanner.ChainScanProgress, error) {
	worker, err := r.s.scanWorker()
	if err != nil {
		return nil, err
	}
	return worker.ScanProgress(ctx)
}

// StartScanAsync launches a catch-up scan in the background and returns a
// snapshot of it. The run repeats bounded scan passes until every scanned
// chain's cursor reaches its confirmed head, so one click is enough to follow
// a long backlog. fromBlock follows RunOnceFrom: non-zero is an explicit
// rewind and only the first executed pass uses it.
func (s *Service) StartScanAsync(ctx context.Context, fromBlock uint64) (*ScanRunState, error) {
	if err := mode.CheckWritable(); err != nil {
		return nil, err
	}
	if _, err := s.scanWorker(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return scanJobs.start(ctx, serviceScanRunner{s: s}, defaultScanLoopConfig(), fromBlock)
}

// ScanRun returns the current state of an asynchronous scan, or nil.
func (s *Service) ScanRun(id string) *ScanRunState { return scanJobs.get(id) }

// ScanRuns lists known scans, newest first.
func (s *Service) ScanRuns() []*ScanRunState { return scanJobs.list() }

// CancelScanRun asks a running scan to stop after its current pass step.
func (s *Service) CancelScanRun(id string) (*ScanRunState, error) { return scanJobs.cancel(id) }
