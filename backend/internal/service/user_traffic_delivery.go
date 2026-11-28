// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
)

const (
	trafficQueueCapacity       = 1024
	trafficBatchSize           = 32  // bounded disk-spool replay batch size
	trafficMemoryBatchMax      = 128 // matches RecordTrafficBatch's 128-row SQL cap
	trafficFlushWindow         = 50 * time.Millisecond
	trafficRetryBackoff        = 2 * time.Second
	trafficHealthInterval      = 30 * time.Second
	trafficMaxSpoolFiles       = 10000
	trafficMaxSpoolBytes       = 64 << 20
	trafficStopBudget          = time.Second
	trafficWriteBudget         = 250 * time.Millisecond
	trafficDirectWriteBudget   = 500 * time.Millisecond
	trafficShutdownMinDBBudget = 200 * time.Millisecond
)

var errTrafficSpoolFull = errors.New("traffic spool capacity reached")
var errTrafficStorageUnavailable = errors.New("traffic storage unavailable")

type trafficSpoolError struct {
	err         error
	dbSucceeded bool
}

func (e trafficSpoolError) Error() string { return e.err.Error() }
func (e trafficSpoolError) Unwrap() error { return e.err }

// TrafficDelivery is best-effort auxiliary metering. Request goroutines only
// attempt a bounded, nonblocking enqueue; all filesystem/SQL/logging is here.
type TrafficDelivery struct {
	dir                 string // Resolved and used only by the worker, never the request path.
	resolveDir          func() string
	write               func(context.Context, TrafficEvent) error
	writeBatch          func(context.Context, []TrafficEvent) error
	wake                chan struct{}
	queue               chan TrafficEvent
	gate                sync.RWMutex // TryRLock on producers; only final worker drain takes Lock.
	stopping            atomic.Bool
	running             atomic.Bool
	stop                chan struct{}
	cancel              context.CancelFunc
	done                chan struct{}
	once                sync.Once
	stopBudget          time.Duration
	stopDeadline        atomic.Int64
	stageEvent          func(TrafficEvent) error
	removeFile          func(string) error
	disk                atomic.Pointer[trafficDiskHealth]
	processing          atomic.Int64
	queueDrops          atomic.Int64
	shutdownDrops       atomic.Int64
	spoolDrops          atomic.Int64
	invalidDrops        atomic.Int64
	panics              atomic.Int64
	delivered           atomic.Int64
	deliveryFailures    atomic.Int64
	stagingFailures     atomic.Int64
	persistenceFailures atomic.Int64
	lastDelivery        atomic.Int64
	// Worker-owned accounting; reconciled on demand when spool state diverges.
	spoolFiles          int64
	spoolBytes          int64
	pendingFiles        int64
	maxSpoolFiles       int64
	maxSpoolBytes       int64
	spoolNeedsReconcile bool
}

type trafficDiskHealth struct {
	PendingEvents     int64
	QuarantinedEvents int64
	OldestPendingAt   *time.Time
	CheckedAt         time.Time
	Available         bool
}

type trafficDeliveryOptions struct {
	queueCapacity int
	stopBudget    time.Duration
	// A storage seam lets regression tests block real worker I/O deterministically.
	stage      func(TrafficEvent) error
	writeBatch func(context.Context, []TrafficEvent) error
}

func NewTrafficDelivery(dir string, write func(context.Context, TrafficEvent) error) (*TrafficDelivery, error) {
	return NewTrafficDeliveryResolved(func() string { return dir }, write)
}

// Even directory resolution/probing happens on the worker, so a bad mount
// cannot prevent the HTTP service from starting.
func NewTrafficDeliveryResolved(resolveDir func() string, write func(context.Context, TrafficEvent) error) (*TrafficDelivery, error) {
	return newTrafficDelivery(resolveDir, write, trafficDeliveryOptions{queueCapacity: trafficQueueCapacity, stopBudget: trafficStopBudget})
}

// The spool format remains one event per file, so pending records survive
// upgrades and rollback. Only the database handoff is batched.
func NewTrafficDeliveryBatchedResolved(resolveDir func() string, write func(context.Context, TrafficEvent) error, batch func(context.Context, []TrafficEvent) error) (*TrafficDelivery, error) {
	if batch == nil {
		return nil, errors.New("missing traffic batch writer")
	}
	return newTrafficDelivery(resolveDir, write, trafficDeliveryOptions{queueCapacity: trafficQueueCapacity, stopBudget: trafficStopBudget, writeBatch: batch})
}

func newTrafficDelivery(resolveDir func() string, write func(context.Context, TrafficEvent) error, options trafficDeliveryOptions) (*TrafficDelivery, error) {
	if resolveDir == nil || write == nil || options.queueCapacity <= 0 || options.stopBudget <= 0 {
		return nil, errors.New("invalid traffic delivery configuration")
	}
	d := &TrafficDelivery{resolveDir: resolveDir, write: write, queue: make(chan TrafficEvent, options.queueCapacity), stop: make(chan struct{}), done: make(chan struct{}), stopBudget: options.stopBudget, maxSpoolFiles: trafficMaxSpoolFiles, maxSpoolBytes: trafficMaxSpoolBytes}
	d.writeBatch = options.writeBatch
	d.wake = make(chan struct{}, 1)
	d.stageEvent = options.stage
	if d.stageEvent == nil {
		d.stageEvent = d.stage
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.disk.Store(&trafficDiskHealth{CheckedAt: time.Now().UTC()})
	d.running.Store(true)
	go d.run(ctx)
	return d, nil
}

func (d *TrafficDelivery) Stop() {
	d.once.Do(func() {
		d.stopping.Store(true)
		d.stopDeadline.Store(time.Now().Add(d.stopBudget).UnixNano())
		close(d.stop)
	})
	timer := time.NewTimer(d.stopBudget)
	defer timer.Stop()
	select {
	case <-d.done:
	case <-timer.C:
		d.cancel()
	}
}

func (d *TrafficDelivery) RecordTraffic(e TrafficEvent) {
	// Never close queue: producers cannot panic during concurrent shutdown.
	if d.stopping.Load() || !d.gate.TryRLock() {
		d.shutdownDrops.Add(1)
		return
	}
	defer d.gate.RUnlock()
	if d.stopping.Load() {
		d.shutdownDrops.Add(1)
		return
	}
	select {
	case d.queue <- e:
	default:
		d.queueDrops.Add(1)
	}
}

func (d *TrafficDelivery) signal() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// persist spools an event to local disk as fallback when direct PostgreSQL
// delivery fails (or during shutdown when the database cannot accept it).
func (d *TrafficDelivery) persist(_ context.Context, e TrafficEvent) {
	if e.EventID == "" {
		e.EventID = uuid.NewString()
	}
	err := d.stageEvent(e)
	if err == nil {
		return
	}
	if errors.Is(err, errTrafficSpoolFull) {
		d.spoolNeedsReconcile = true
		d.spoolDrops.Add(1)
		return
	}
	if errors.Is(err, ErrInvalidTrafficEvent) {
		d.invalidDrops.Add(1)
		return
	}
	d.stagingFailures.Add(1)
	d.markStorageUnavailable()
	d.persistenceFailures.Add(1)
}

func (d *TrafficDelivery) collectMemoryBatch(batch []TrafficEvent) []TrafficEvent {
	for len(batch) < trafficMemoryBatchMax {
		select {
		case event := <-d.queue:
			batch = append(batch, event)
		default:
			return batch
		}
	}
	return batch
}

func (d *TrafficDelivery) spoolBatch(ctx context.Context, events []TrafficEvent) {
	for i, e := range events {
		if ctx.Err() != nil {
			remaining := int64(len(events) - i)
			d.shutdownDrops.Add(remaining)
			d.processing.Add(-remaining)
			return
		}
		d.persist(ctx, e)
		d.processing.Add(-1)
	}
}

// flushMemoryBatch delivers an in-memory batch directly to PostgreSQL without
// touching disk in steady state. Only retryable PostgreSQL failures (or tight
// shutdown headroom where writeBudget <= 0) spill unpersisted events to spool.
func (d *TrafficDelivery) flushMemoryBatch(ctx context.Context, batch []TrafficEvent, nextDBRetry *time.Time, writeBudget time.Duration) {
	if len(batch) == 0 {
		return
	}
	valid := make([]TrafficEvent, 0, len(batch))
	for _, e := range batch {
		if e.EventID == "" {
			e.EventID = uuid.NewString()
		}
		if ValidateTrafficEvent(e) != nil {
			d.invalidDrops.Add(1)
			d.processing.Add(-1)
			continue
		}
		valid = append(valid, e)
	}
	if len(valid) == 0 {
		return
	}

	// While backing off after a retryable DB failure (or when shutdown headroom
	// is too tight to risk a hanging DB write), spill directly to a healthy spool.
	if d.disk.Load().Available && (writeBudget <= 0 || (!nextDBRetry.IsZero() && time.Now().Before(*nextDBRetry))) {
		d.spoolBatch(ctx, valid)
		return
	}

	if d.writeBatch != nil {
		if ctx.Err() != nil {
			remaining := int64(len(valid))
			d.shutdownDrops.Add(remaining)
			d.processing.Add(-remaining)
			return
		}
		writeCtx, cancel := context.WithTimeout(ctx, writeBudget)
		err := d.writeBatch(writeCtx, valid)
		cancel()
		if err == nil {
			d.noteDeliveryCount(len(valid))
			d.processing.Add(-int64(len(valid)))
			*nextDBRetry = time.Time{}
			return
		}
		if ctx.Err() != nil {
			remaining := int64(len(valid))
			d.shutdownDrops.Add(remaining)
			d.processing.Add(-remaining)
			return
		}
		d.deliveryFailures.Add(1)
		if !permanentTrafficError(err) {
			*nextDBRetry = time.Now().Add(trafficRetryBackoff)
			d.spoolBatch(ctx, valid)
			return
		}
		// A permanent error may belong to just one event; isolate row-by-row.
	}

	for i, e := range valid {
		if ctx.Err() != nil {
			remaining := int64(len(valid) - i)
			d.shutdownDrops.Add(remaining)
			d.processing.Add(-remaining)
			return
		}
		writeCtx, cancel := context.WithTimeout(ctx, writeBudget)
		err := d.write(writeCtx, e)
		cancel()
		if err == nil {
			d.noteDelivery()
			d.processing.Add(-1)
			*nextDBRetry = time.Time{}
			continue
		}
		if ctx.Err() != nil {
			remaining := int64(len(valid) - i)
			d.shutdownDrops.Add(remaining)
			d.processing.Add(-remaining)
			return
		}
		d.deliveryFailures.Add(1)
		if permanentTrafficError(err) {
			d.invalidDrops.Add(1)
			d.processing.Add(-1)
			continue
		}
		*nextDBRetry = time.Now().Add(trafficRetryBackoff)
		d.spoolBatch(ctx, valid[i:])
		return
	}
}

func (d *TrafficDelivery) replayPendingCycle(ctx context.Context, nextDBRetry, nextReplayRetry *time.Time) {
	if d.pendingFiles <= 0 {
		return
	}
	now := time.Now()
	if (!nextDBRetry.IsZero() && now.Before(*nextDBRetry)) || (!nextReplayRetry.IsZero() && now.Before(*nextReplayRetry)) {
		return
	}
	cycle, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if !d.disk.Load().Available {
		d.refreshStorage(cycle)
		if !d.disk.Load().Available {
			if cycle.Err() == nil {
				*nextReplayRetry = time.Now().Add(trafficRetryBackoff)
			}
			return
		}
	}
	// Up to four replay batches per cycle provide recovery headroom.
	// Budget exhaustion alone is not a database failure.
	deadline, _ := cycle.Deadline()
	for i := 0; i < 4 && d.pendingFiles > 0; i++ {
		if time.Until(deadline) < trafficWriteBudget {
			break
		}
		if err := d.replay(cycle); err != nil {
			var spoolErr trafficSpoolError
			if errors.As(err, &spoolErr) {
				// Filesystem errors must not poison healthy PostgreSQL delivery.
				if spoolErr.dbSucceeded {
					*nextDBRetry = time.Time{}
				}
				d.markStorageUnavailable()
				if cycle.Err() == nil {
					*nextReplayRetry = time.Now().Add(trafficRetryBackoff)
				}
			} else if cycle.Err() == nil {
				*nextDBRetry = time.Now().Add(trafficRetryBackoff)
			}
			break
		}
		*nextDBRetry = time.Time{}
		*nextReplayRetry = time.Time{}
	}
}

func (d *TrafficDelivery) abandonQueue() {
	d.shutdownDrops.Add(d.processing.Swap(0))
	for {
		select {
		case <-d.queue:
			d.shutdownDrops.Add(1)
		default:
			return
		}
	}
}

func (d *TrafficDelivery) shutdownWriteBudget() time.Duration {
	deadlineNS := d.stopDeadline.Load()
	if deadlineNS == 0 {
		return trafficWriteBudget
	}
	rem := time.Until(time.Unix(0, deadlineNS))
	if d.disk.Load().Available {
		// Reserve at least 3/4 of remaining stopBudget for fallback spooling so an
		// unresponsive database cannot exhaust stopBudget and drop buffered rows.
		if rem < trafficShutdownMinDBBudget {
			return 0
		}
		return min(rem/4, trafficWriteBudget)
	}
	if rem <= 0 {
		return 0
	}
	return min(rem, trafficWriteBudget)
}

func (d *TrafficDelivery) drain(ctx context.Context, memBatch []TrafficEvent, nextDBRetry *time.Time) {
	// Wait only in this worker for producers already past the stopping check.
	// New requests use TryRLock and never wait on this lock or any I/O.
	d.gate.Lock()
	defer d.gate.Unlock()
	for ctx.Err() == nil {
		memBatch = d.collectMemoryBatch(memBatch)
		if len(memBatch) == 0 {
			break
		}
		d.processing.Store(int64(len(memBatch)))
		d.flushMemoryBatch(ctx, memBatch, nextDBRetry, d.shutdownWriteBudget())
		clear(memBatch)
		memBatch = memBatch[:0]
	}
	if len(memBatch) > 0 {
		d.shutdownDrops.Add(int64(len(memBatch)))
		d.processing.Store(0)
		clear(memBatch)
	}
	d.abandonQueue()
}

func (d *TrafficDelivery) reconcileHealth(ctx context.Context) {
	disk := d.disk.Load()
	// Stay zero-I/O in healthy empty steady state; reconcile only when storage
	// was unavailable, hit its spool quota, or holds quarantined files awaiting cleanup.
	if !disk.Available || d.spoolNeedsReconcile || disk.QuarantinedEvents > 0 {
		d.refreshStorage(ctx)
		if d.disk.Load().Available {
			d.spoolNeedsReconcile = false
		}
	}
}

func (d *TrafficDelivery) run(ctx context.Context) {
	defer close(d.done)
	defer d.cancel()
	defer d.running.Store(false)
	defer func() {
		if recovered := recover(); recovered != nil {
			d.panics.Add(1)
			d.stopping.Store(true)
			d.gate.Lock()
			d.abandonQueue()
			d.gate.Unlock()
			logger.LegacyPrintf("user_traffic", "DELIVERY ALERT background worker stopped after panic; dropped=%d", d.droppedEvents())
		}
	}()
	d.dir = d.resolveDir()
	// Scan disk once at startup to discover any spooled/quarantined backlog from
	// a previous run; steady-state operation avoids periodic directory Stat scans.
	d.refreshStorage(ctx)

	memBatch := make([]TrafficEvent, 0, trafficMemoryBatchMax)
	var nextDBRetry, nextReplayRetry time.Time

	flushTimer := time.NewTimer(trafficFlushWindow)
	if !flushTimer.Stop() {
		select {
		case <-flushTimer.C:
		default:
		}
	}
	defer flushTimer.Stop()
	var flushCh <-chan time.Time

	replayTimer := time.NewTimer(trafficRetryBackoff)
	if !replayTimer.Stop() {
		select {
		case <-replayTimer.C:
		default:
		}
	}
	defer replayTimer.Stop()
	var replayCh <-chan time.Time

	healthTicker := time.NewTicker(trafficHealthInterval)
	defer healthTicker.Stop()

	armFlush := func() {
		if flushCh != nil {
			return
		}
		flushTimer.Reset(trafficFlushWindow)
		flushCh = flushTimer.C
	}
	stopFlush := func() {
		if flushCh == nil {
			return
		}
		if !flushTimer.Stop() {
			select {
			case <-flushTimer.C:
			default:
			}
		}
		flushCh = nil
	}
	syncReplayTimer := func() {
		if replayCh != nil {
			if !replayTimer.Stop() {
				select {
				case <-replayTimer.C:
				default:
				}
			}
			replayCh = nil
		}
		if d.pendingFiles <= 0 {
			return
		}
		retryAt := nextDBRetry
		if nextReplayRetry.After(retryAt) {
			retryAt = nextReplayRetry
		}
		delay := time.Until(retryAt)
		if delay <= 0 {
			d.signal()
			return
		}
		replayTimer.Reset(delay)
		replayCh = replayTimer.C
	}

	if d.pendingFiles > 0 {
		d.signal()
	}

	for {
		select {
		case <-ctx.Done():
			d.stopping.Store(true)
			d.drain(ctx, memBatch, &nextDBRetry)
			return
		case <-d.stop:
			d.drain(ctx, memBatch, &nextDBRetry)
			return
		default:
		}

		select {
		case <-ctx.Done():
			d.stopping.Store(true)
			d.drain(ctx, memBatch, &nextDBRetry)
			return
		case <-d.stop:
			d.drain(ctx, memBatch, &nextDBRetry)
			return
		case event := <-d.queue:
			memBatch = d.collectMemoryBatch(append(memBatch, event))
			d.processing.Store(int64(len(memBatch)))
			if len(memBatch) >= trafficMemoryBatchMax {
				stopFlush()
				d.flushMemoryBatch(ctx, memBatch, &nextDBRetry, trafficDirectWriteBudget)
				clear(memBatch)
				memBatch = memBatch[:0]
			} else {
				armFlush()
			}
		case <-flushCh:
			flushCh = nil
			if len(memBatch) > 0 {
				memBatch = d.collectMemoryBatch(memBatch)
				d.processing.Store(int64(len(memBatch)))
				d.flushMemoryBatch(ctx, memBatch, &nextDBRetry, trafficDirectWriteBudget)
				clear(memBatch)
				memBatch = memBatch[:0]
			}
		case <-replayCh:
			replayCh = nil
			d.replayPendingCycle(ctx, &nextDBRetry, &nextReplayRetry)
		case <-d.wake:
			d.replayPendingCycle(ctx, &nextDBRetry, &nextReplayRetry)
		case <-healthTicker.C:
			d.reconcileHealth(ctx)
			d.logHealth()
		}
		syncReplayTimer()
	}
}

func (d *TrafficDelivery) refreshStorage(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if err := os.MkdirAll(d.dir, 0700); err != nil {
		d.markStorageUnavailable()
	} else if err := d.scanDisk(ctx); err != nil {
		d.markStorageUnavailable()
	}
}

func (d *TrafficDelivery) markStorageUnavailable() {
	snapshot := *d.disk.Load()
	snapshot.Available = false
	snapshot.CheckedAt = time.Now().UTC()
	d.disk.Store(&snapshot)
}

func (d *TrafficDelivery) droppedEvents() int64 {
	return d.queueDrops.Load() + d.shutdownDrops.Load() + d.spoolDrops.Load() + d.invalidDrops.Load() + d.persistenceFailures.Load()
}

func (d *TrafficDelivery) logHealth() {
	health, _ := d.Health(context.Background())
	if health.Status != "ok" {
		logger.LegacyPrintf("user_traffic", "DELIVERY ALERT status=%s queued=%d pending=%d storage_available=%t quarantined=%d dropped=%d", health.Status, health.QueuedEvents, health.PendingEvents, health.StorageAvailable, health.QuarantinedEvents, health.DroppedEvents)
	}
}

func (d *TrafficDelivery) stage(e TrafficEvent) error {
	if _, err := uuid.Parse(e.EventID); err != nil {
		return ErrInvalidTrafficEvent
	}
	// Do not grow a spool whose existing footprint could not be measured.
	if !d.disk.Load().Available {
		return errTrafficStorageUnavailable
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if len(raw) > 8192 {
		return ErrInvalidTrafficEvent
	}
	if d.spoolFiles >= d.maxSpoolFiles || d.spoolBytes+int64(len(raw)) > d.maxSpoolBytes {
		return errTrafficSpoolFull
	}
	f, err := os.CreateTemp(d.dir, ".traffic-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() { _ = f.Close(); _ = os.Remove(name) }()
	if _, err = f.Write(raw); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, filepath.Join(d.dir, e.EventID+".json")); err != nil {
		return err
	}
	d.spoolFiles++
	d.spoolBytes += int64(len(raw))
	d.pendingFiles++
	d.updatePendingSnapshot()
	return syncTrafficDir(d.dir)
}

func syncTrafficDir(dirPath string) error {
	// Windows rejects FlushFileBuffers on directory handles with ERROR_ACCESS_DENIED.
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(dirPath)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}

func (d *TrafficDelivery) replay(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := os.Open(d.dir)
	if err != nil {
		return trafficSpoolError{err: err}
	}
	var pending []trafficPendingFile
	processed := 0
	reachedEOF := false
	// Scan in bounded batches without loading an outage's entire backlog.
	for processed < trafficBatchSize && ctx.Err() == nil {
		entries, readErr := dir.ReadDir(64)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			_ = dir.Close()
			return trafficSpoolError{err: readErr}
		}
		hitLimit := false
		for _, entry := range entries {
			if processed >= trafficBatchSize || ctx.Err() != nil {
				hitLimit = true
				break
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			processed++
			path := filepath.Join(d.dir, entry.Name())
			f, err := os.Open(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				_ = dir.Close()
				return trafficSpoolError{err: err}
			}
			raw, err := io.ReadAll(io.LimitReader(f, 8193))
			_ = f.Close()
			if err != nil {
				_ = dir.Close()
				return trafficSpoolError{err: err}
			}
			var e TrafficEvent
			if len(raw) > 8192 || json.Unmarshal(raw, &e) != nil || e.EventID+".json" != entry.Name() || ValidateTrafficEvent(e) != nil {
				if err := d.quarantine(path); err != nil {
					_ = dir.Close()
					return trafficSpoolError{err: err}
				}
				continue
			}
			pending = append(pending, trafficPendingFile{event: e, path: path, size: int64(len(raw))})
		}

		if errors.Is(readErr, io.EOF) {
			if !hitLimit {
				reachedEOF = true
			}
			break
		}
	}
	_ = dir.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := d.deliverPending(ctx, pending); err != nil {
		return err
	}
	if (d.pendingFiles != 0 && reachedEOF) || (d.pendingFiles > 0 && processed == 0) {
		// Reconcile drifted accounting (e.g. duplicate EventID overwrite or external
		// file removal) once the directory scan reaches EOF so replay never spins.
		if err := d.scanDisk(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return trafficSpoolError{err: err, dbSucceeded: len(pending) > 0}
		}
	}
	return nil
}

type trafficPendingFile struct {
	event TrafficEvent
	path  string
	size  int64
}

func (d *TrafficDelivery) acknowledge(file trafficPendingFile) error {
	d.noteDelivery()
	remove := d.removeFile
	if remove == nil {
		remove = os.Remove
	}
	if err := remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if d.pendingFiles > 0 {
		d.pendingFiles--
	}
	if d.spoolFiles > 0 {
		d.spoolFiles--
	}
	d.spoolBytes -= file.size
	if d.spoolBytes < 0 {
		d.spoolBytes = 0
	}
	d.updatePendingSnapshot()
	return nil
}

func (d *TrafficDelivery) deliverPending(ctx context.Context, files []trafficPendingFile) error {
	if len(files) == 0 {
		return nil
	}
	if d.writeBatch != nil {
		events := make([]TrafficEvent, len(files))
		for i := range files {
			events[i] = files[i].event
		}
		writeCtx, cancel := context.WithTimeout(ctx, trafficWriteBudget)
		err := d.writeBatch(writeCtx, events)
		cancel()
		if err == nil {
			for _, file := range files {
				if err := d.acknowledge(file); err != nil {
					return trafficSpoolError{err: err, dbSucceeded: true}
				}
			}
			return nil
		}
		d.deliveryFailures.Add(1)
		if !permanentTrafficError(err) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		// A permanent error may concern just one event. Isolate it with bounded
		// single writes; committed batches are safe to retry by their event IDs.
	}
	dbSucceeded := false
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		writeCtx, cancel := context.WithTimeout(ctx, trafficWriteBudget)
		err := d.write(writeCtx, file.event)
		cancel()
		if err != nil {
			d.deliveryFailures.Add(1)
			if !permanentTrafficError(err) {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return err
			}
			if err := d.quarantine(file.path); err != nil {
				return trafficSpoolError{err: err, dbSucceeded: dbSucceeded}
			}
			continue
		}
		dbSucceeded = true
		if err := d.acknowledge(file); err != nil {
			return trafficSpoolError{err: err, dbSucceeded: true}
		}
	}
	return nil
}

func (d *TrafficDelivery) quarantine(path string) error {
	if err := os.Rename(path, path+".invalid"); err != nil {
		return fmt.Errorf("quarantine traffic event: %w", err)
	}
	if d.pendingFiles > 0 {
		d.pendingFiles--
	}
	snapshot := *d.disk.Load()
	snapshot.QuarantinedEvents++
	d.disk.Store(&snapshot)
	d.updatePendingSnapshot()
	logger.LegacyPrintf("user_traffic", "DELIVERY ALERT invalid pending event quarantined; manual repair required: %s", filepath.Base(path))
	return nil
}

func permanentTrafficError(err error) bool {
	if errors.Is(err, ErrInvalidTrafficEvent) {
		return true
	}
	var state interface{ SQLState() string }
	// Data exceptions and integrity violations cannot be fixed by retrying the
	// same metadata. Network, transaction, schema and permission errors retry.
	if errors.As(err, &state) {
		return strings.HasPrefix(state.SQLState(), "22") || strings.HasPrefix(state.SQLState(), "23")
	}
	return false
}

func (d *TrafficDelivery) noteDelivery() {
	d.noteDeliveryCount(1)
}

func (d *TrafficDelivery) noteDeliveryCount(n int) {
	if n <= 0 {
		return
	}
	d.delivered.Add(int64(n))
	d.lastDelivery.Store(time.Now().Unix())
}

// TrafficDeliveryHealth is a cached node-local view. Reading it never touches disk.
type TrafficDeliveryHealth struct {
	Status                   string     `json:"status"`
	QueuedEvents             int        `json:"queued_events"`
	QueueCapacity            int        `json:"queue_capacity"`
	ProcessingEvents         int64      `json:"processing_events"`
	DroppedEvents            int64      `json:"dropped_events"`
	QueueDroppedEvents       int64      `json:"queue_dropped_events"`
	ShutdownDroppedEvents    int64      `json:"shutdown_dropped_events"`
	SpoolLimitDroppedEvents  int64      `json:"spool_limit_dropped_events"`
	InvalidDroppedEvents     int64      `json:"invalid_dropped_events"`
	UnavailableDroppedEvents int64      `json:"unavailable_dropped_events"`
	WorkerPanics             int64      `json:"worker_panics"`
	WorkerRunning            bool       `json:"worker_running"`
	StorageAvailable         bool       `json:"storage_available"`
	PendingEvents            int64      `json:"pending_events"`
	QuarantinedEvents        int64      `json:"quarantined_events"`
	OldestPendingAt          *time.Time `json:"oldest_pending_at"`
	OldestPendingAgeSeconds  int64      `json:"oldest_pending_age_seconds"`
	DeliveredEvents          int64      `json:"delivered_events"`
	DeliveryFailures         int64      `json:"delivery_failures"`
	StagingFailures          int64      `json:"staging_failures"`
	PersistenceFailures      int64      `json:"persistence_failures"`
	LastDeliveryAt           *time.Time `json:"last_delivery_at"`
	CheckedAt                time.Time  `json:"checked_at"`
}

func (d *TrafficDelivery) updatePendingSnapshot() {
	snapshot := *d.disk.Load()
	snapshot.PendingEvents = d.pendingFiles
	if d.pendingFiles == 0 {
		snapshot.OldestPendingAt = nil
	} else if snapshot.OldestPendingAt == nil {
		now := time.Now().UTC()
		snapshot.OldestPendingAt = &now
	}
	d.disk.Store(&snapshot)
}

func (d *TrafficDelivery) scanDisk(ctx context.Context) error {
	snapshot := trafficDiskHealth{Available: true, CheckedAt: time.Now().UTC()}
	dir, err := os.Open(d.dir)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	var files, bytes int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := dir.ReadDir(64)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			info, err := entry.Info()
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			files++
			bytes += info.Size()
			if strings.HasSuffix(entry.Name(), ".json.invalid") {
				snapshot.QuarantinedEvents++
				continue
			}
			if !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			snapshot.PendingEvents++
			modified := info.ModTime().UTC()
			if snapshot.OldestPendingAt == nil || modified.Before(*snapshot.OldestPendingAt) {
				snapshot.OldestPendingAt = &modified
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	d.spoolFiles, d.spoolBytes, d.pendingFiles = files, bytes, snapshot.PendingEvents
	d.disk.Store(&snapshot)
	return nil
}

func (d *TrafficDelivery) Health(ctx context.Context) (TrafficDeliveryHealth, error) {
	if err := ctx.Err(); err != nil {
		return TrafficDeliveryHealth{}, err
	}
	disk := d.disk.Load()
	h := TrafficDeliveryHealth{Status: "ok", QueuedEvents: len(d.queue), QueueCapacity: cap(d.queue), ProcessingEvents: d.processing.Load(), DroppedEvents: d.droppedEvents(), QueueDroppedEvents: d.queueDrops.Load(), ShutdownDroppedEvents: d.shutdownDrops.Load(), SpoolLimitDroppedEvents: d.spoolDrops.Load(), InvalidDroppedEvents: d.invalidDrops.Load(), WorkerPanics: d.panics.Load(), WorkerRunning: d.running.Load(), StorageAvailable: disk.Available, PendingEvents: disk.PendingEvents, QuarantinedEvents: disk.QuarantinedEvents, OldestPendingAt: disk.OldestPendingAt, CheckedAt: disk.CheckedAt, DeliveredEvents: d.delivered.Load(), DeliveryFailures: d.deliveryFailures.Load(), StagingFailures: d.stagingFailures.Load(), PersistenceFailures: d.persistenceFailures.Load()}
	if last := d.lastDelivery.Load(); last > 0 {
		at := time.Unix(last, 0).UTC()
		h.LastDeliveryAt = &at
	}
	if h.OldestPendingAt != nil {
		h.OldestPendingAgeSeconds = int64(time.Since(*h.OldestPendingAt).Seconds())
		if h.OldestPendingAgeSeconds < 0 {
			h.OldestPendingAgeSeconds = 0
		}
	}
	if h.QueuedEvents >= h.QueueCapacity/2 || h.PendingEvents >= 256 || h.OldestPendingAgeSeconds >= 60 {
		h.Status = "delayed"
	}
	if !h.StorageAvailable || !h.WorkerRunning || h.DroppedEvents > 0 || h.QuarantinedEvents > 0 || h.WorkerPanics > 0 {
		h.Status = "degraded"
	}
	return h, nil
}
