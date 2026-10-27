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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/google/uuid"
)

const (
	trafficQueueCapacity = 1024
	trafficBatchSize     = 32
	trafficMaxSpoolFiles = 10000
	trafficMaxSpoolBytes = 64 << 20
	trafficStopBudget    = time.Second
	trafficWriteBudget   = 250 * time.Millisecond
)

var errTrafficSpoolFull = errors.New("traffic spool capacity reached")
var errTrafficStorageUnavailable = errors.New("traffic storage unavailable")

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
	stageEvent          func(TrafficEvent) error
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
	// Worker-owned accounting; periodically reconciled with the directory.
	spoolFiles    int64
	spoolBytes    int64
	pendingFiles  int64
	maxSpoolFiles int64
	maxSpoolBytes int64
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
	d.once.Do(func() { d.stopping.Store(true); close(d.stop) })
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
		d.signal()
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

func (d *TrafficDelivery) persist(ctx context.Context, e TrafficEvent) {
	if e.EventID == "" {
		e.EventID = uuid.NewString()
	}
	err := d.stageEvent(e)
	if err == nil {
		return
	}
	if errors.Is(err, errTrafficSpoolFull) {
		d.spoolDrops.Add(1)
		return
	}
	if errors.Is(err, ErrInvalidTrafficEvent) {
		d.invalidDrops.Add(1)
		return
	}
	d.stagingFailures.Add(1)
	d.markStorageUnavailable()
	// This fallback is strictly worker-only, bounded and shutdown-cancellable.
	writeCtx, cancel := context.WithTimeout(ctx, trafficWriteBudget)
	defer cancel()
	if err := d.write(writeCtx, e); err != nil {
		d.deliveryFailures.Add(1)
		d.persistenceFailures.Add(1)
	} else {
		d.noteDelivery()
	}
}

func (d *TrafficDelivery) consume(ctx context.Context) {
	var batch [trafficBatchSize]TrafficEvent
	count := 0
collect:
	for count < len(batch) {
		select {
		case event := <-d.queue:
			batch[count] = event
			count++
		default:
			break collect
		}
	}
	d.processing.Store(int64(count))
	for i := 0; i < count; i++ {
		if ctx.Err() != nil {
			d.shutdownDrops.Add(int64(count - i))
			d.processing.Store(0)
			return
		}
		d.persist(ctx, batch[i])
		d.processing.Add(-1)
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

func (d *TrafficDelivery) drain(ctx context.Context) {
	// Wait only in this worker for producers already past the stopping check.
	// New requests use TryRLock and never wait on this lock or any I/O.
	d.gate.Lock()
	defer d.gate.Unlock()
	for len(d.queue) > 0 && ctx.Err() == nil {
		d.consume(ctx)
	}
	d.abandonQueue()
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
	d.refreshStorage(ctx)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	nextScan, nextReplay, nextAlert := time.Now().Add(2*time.Second), time.Time{}, time.Time{}
	for {
		select {
		case <-ctx.Done():
			d.stopping.Store(true)
			d.drain(ctx)
			return
		case <-d.stop:
			d.drain(ctx)
			return
		case <-ticker.C:
		case <-d.wake:
		}
		now := time.Now()
		if !now.Before(nextScan) {
			d.refreshStorage(ctx)
			nextScan = time.Now().Add(2 * time.Second)
		}
		// A fixed worker and batch cap bound concurrent work and memory use.
		d.consume(ctx)
		if d.pendingFiles > 0 && !time.Now().Before(nextReplay) {
			cycle, cancel := context.WithTimeout(ctx, time.Second)
			// Up to four replay batches for each newly staged batch provide
			// recovery headroom. Budget exhaustion alone is not a database failure.
			deadline, _ := cycle.Deadline()
			for i := 0; i < 4 && d.pendingFiles > 0; i++ {
				if time.Until(deadline) < trafficWriteBudget {
					break
				}
				if err := d.replay(cycle); err != nil {
					if cycle.Err() == nil {
						nextReplay = time.Now().Add(2 * time.Second)
					}
					break
				}
			}
			cancel()
		}
		if len(d.queue) > 0 || (d.pendingFiles > 0 && !time.Now().Before(nextReplay)) {
			d.signal()
		}
		if !time.Now().Before(nextAlert) {
			d.logHealth()
			nextAlert = time.Now().Add(30 * time.Second)
		}
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
	dir, err := os.Open(d.dir)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}

func (d *TrafficDelivery) replay(ctx context.Context) error {
	dir, err := os.Open(d.dir)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	var pending []trafficPendingFile
	// Scan in bounded batches without loading an outage's entire backlog.
	for processed := 0; processed < trafficBatchSize && ctx.Err() == nil; {
		entries, readErr := dir.ReadDir(64)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		for _, entry := range entries {
			if processed >= trafficBatchSize || ctx.Err() != nil {
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
				return err
			}
			raw, err := io.ReadAll(io.LimitReader(f, 8193))
			_ = f.Close()
			if err != nil {
				return err
			}
			var e TrafficEvent
			if len(raw) > 8192 || json.Unmarshal(raw, &e) != nil || e.EventID+".json" != entry.Name() || ValidateTrafficEvent(e) != nil {
				if err := d.quarantine(path); err != nil {
					return err
				}
				continue
			}
			pending = append(pending, trafficPendingFile{event: e, path: path, size: int64(len(raw))})
		}

		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return d.deliverPending(ctx, pending)
}

type trafficPendingFile struct {
	event TrafficEvent
	path  string
	size  int64
}

func (d *TrafficDelivery) acknowledge(file trafficPendingFile) error {
	d.noteDelivery()
	if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	d.pendingFiles--
	d.spoolFiles--
	d.spoolBytes -= file.size
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
					return err
				}
			}
			return nil
		}
		d.deliveryFailures.Add(1)
		if !permanentTrafficError(err) {
			return err
		}
		// A permanent error may concern just one event. Isolate it with bounded
		// single writes; committed batches are safe to retry by their event IDs.
	}
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
				return err
			}
			if err := d.quarantine(file.path); err != nil {
				return err
			}
			continue
		}
		if err := d.acknowledge(file); err != nil {
			return err
		}
	}
	return nil
}

func (d *TrafficDelivery) quarantine(path string) error {
	if err := os.Rename(path, path+".invalid"); err != nil {
		return fmt.Errorf("quarantine traffic event: %w", err)
	}
	d.pendingFiles--
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
	d.delivered.Add(1)
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
