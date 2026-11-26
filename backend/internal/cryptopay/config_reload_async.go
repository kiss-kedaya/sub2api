package cryptopay

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Applying a saved config (rebuild the RPC pools, restart the scan worker) has to
// hold sweepMu, and a manual sweep holds it for up to 30 minutes. An admin
// request must never sit behind that lock: the save is already durable, so the
// request waits a few seconds, answers "not applied yet", and the apply finishes
// in the background as soon as the lock frees up.

const (
	// reloadClosedPoll is how often a reload parked behind a sweep checks whether
	// the service was closed.
	reloadClosedPoll = 50 * time.Millisecond
	// adminSweepLockPoll is how often a waiting signer/vault request retries sweepMu.
	adminSweepLockPoll = 25 * time.Millisecond
)

// reloadApplyTimeout bounds one apply so a hung database cannot pin sweepMu.
// An apply cut short is retried by the config watcher (reloadFailed stays set).
// A var so tests can shorten it.
var reloadApplyTimeout = 2 * time.Minute

// adminSweepLockWait is how long a signer/vault admin request waits for a
// running sweep or scan before answering ErrSweepBusy. Short holders (the config
// watcher, one scan round) are ridden out; a sweep is not. A var so tests can
// shorten it.
var adminSweepLockWait = 5 * time.Second

// ErrSweepBusy means a sweep or scan holds the service for longer than an admin
// request may wait. Nothing was changed; retry after the run finishes.
var ErrSweepBusy = errors.New("a sweep or scan is running; retry after it finishes")

// reloadBatch is one queued reload. Every save that arrives before the reload
// starts reading the stored config joins the same batch, so N quick saves cost
// one reload.
type reloadBatch struct {
	done    chan struct{}
	applied bool // written before done is closed
}

// reloadQueue is the single-flight state behind ReloadWithin. The zero value is
// ready to use.
type reloadQueue struct {
	mu      sync.Mutex
	pending *reloadBatch // waiting for sweepMu; later saves join it
	running bool         // a goroutine is draining the queue
}

// ReloadWithin is Reload for HTTP handlers: it applies the stored config but
// never keeps the caller longer than wait. It reports whether the apply has run:
// true means the new config is live. false means the apply is still queued
// behind a sweep or scan, still running, or was cut off by its timeout or by
// Close. A queued or running apply finishes on its own, even if ctx is cancelled
// — the caller disconnecting must not drop it. Errors inside an apply (bad
// stored config, wallet guard) are logged and retried by the config watcher
// exactly as before and do not change the answer: it reports deferral only.
//
// Saves that arrive while an apply is queued share it, so repeated saves during
// a long sweep produce one reload. The sweepMu -> mu lock order is unchanged.
func (s *Service) ReloadWithin(ctx context.Context, wait time.Duration) (applied bool) {
	batch := s.enqueueReload()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-batch.done:
		return batch.applied
	case <-timer.C:
	case <-ctx.Done():
	}
	return false
}

func (s *Service) enqueueReload() *reloadBatch {
	q := &s.reloadQ
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pending == nil {
		q.pending = &reloadBatch{done: make(chan struct{})}
	}
	batch := q.pending
	if !q.running {
		q.running = true
		go s.drainReloads()
	}
	return batch
}

// drainReloads applies queued batches one at a time. It is the only goroutine
// that runs while q.running is true, and it exits once nothing is pending.
func (s *Service) drainReloads() {
	q := &s.reloadQ
	for {
		locked := s.lockSweepUnlessClosed()
		// Detach the batch only now that sweepMu is ours: a save that lands while
		// we were waiting is covered by the config read below, and one that lands
		// after this point queues another reload.
		q.mu.Lock()
		batch := q.pending
		q.pending = nil
		q.mu.Unlock()
		if locked {
			// Detached from every request: once we hold the lock the apply must
			// happen even if the admin's connection is long gone.
			ctx, cancel := context.WithTimeout(context.Background(), reloadApplyTimeout)
			_ = s.reloadLocked(ctx)    // errors are logged there and retried by the config watcher, as before
			cutOff := ctx.Err() != nil // timed out: the config watcher finishes the job
			cancel()
			s.sweepMu.Unlock()
			batch.applied = !cutOff && !s.isClosed()
		}
		close(batch.done)
		q.mu.Lock()
		if q.pending == nil {
			q.running = false
			q.mu.Unlock()
			return
		}
		q.mu.Unlock()
	}
}

// lockSweepUnlessClosed takes sweepMu, giving up (false) once the service is
// closed. It never holds mu while waiting, so the lock order stays sweepMu -> mu.
//
// When the lock is busy it queues on the mutex like any other Lock caller instead
// of polling it: the apply then runs the moment the sweep ends, and a sweep that
// starts later (the hourly automatic one) queues behind it, so a saved sweep
// destination is not skipped by a run that began after the save.
func (s *Service) lockSweepUnlessClosed() bool {
	if s.isClosed() {
		return false
	}
	if s.sweepMu.TryLock() {
		return true
	}
	locked := make(chan struct{})
	go func() {
		s.sweepMu.Lock()
		close(locked)
	}()
	ticker := time.NewTicker(reloadClosedPoll)
	defer ticker.Stop()
	for {
		select {
		case <-locked:
			return true
		case <-ticker.C:
			if s.isClosed() {
				// Lock cannot be cancelled: hand the lock straight back when it comes.
				go func() {
					<-locked
					s.sweepMu.Unlock()
				}()
				return false
			}
		}
	}
}

func (s *Service) isClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}

// lockSweepWithin takes sweepMu for an admin request that must serialise with
// sweeps (signer and vault changes) but must not hang for the length of one.
// On success the caller owns sweepMu and must Unlock it.
func (s *Service) lockSweepWithin(ctx context.Context) error {
	if s.sweepMu.TryLock() {
		return nil
	}
	timer := time.NewTimer(adminSweepLockWait)
	defer timer.Stop()
	ticker := time.NewTicker(adminSweepLockPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return ErrSweepBusy
		case <-ticker.C:
			if s.sweepMu.TryLock() {
				return nil
			}
		}
	}
}
