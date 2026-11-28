// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func trafficDeliveryFixture() TrafficEvent {
	now := time.Now().UTC()
	return TrafficEvent{EventID: uuid.NewString(), UserID: 1, APIKeyID: 2, StartedAt: now, FinishedAt: now, UploadBytes: 3, DownloadBytes: 4, MeasurementVersion: 1, Endpoint: "/v1/messages", Method: "POST", StatusCode: 200, Outcome: "completed", RequestComplete: true}
}

func stoppedTrafficDelivery(t *testing.T) *TrafficDelivery {
	t.Helper()
	d, err := NewTrafficDelivery(t.TempDir(), func(context.Context, TrafficEvent) error { return nil })
	require.NoError(t, err)
	d.Stop()
	return d
}

func TestTrafficDeliveryQuarantinesPermanentMetadataAndContinues(t *testing.T) {
	for _, kind := range []string{"invalid_version", "invalid_uuid", "invalid_identity", "negative_bytes", "invalid_time", "null_metadata", "constraint", "data_exception"} {
		t.Run(kind, func(t *testing.T) {
			d := stoppedTrafficDelivery(t)
			bad, good := trafficDeliveryFixture(), trafficDeliveryFixture()
			switch kind {
			case "invalid_version":
				bad.MeasurementVersion = 2
			case "invalid_identity":
				bad.UserID = 0
			case "negative_bytes":
				bad.UploadBytes = -1
			case "invalid_time":
				bad.FinishedAt = bad.StartedAt.Add(-time.Second)
			case "null_metadata":
				bad.RequestID = "bad\x00id"
			}
			require.NoError(t, d.stage(bad))
			if kind == "invalid_uuid" {
				path := filepath.Join(d.dir, bad.EventID+".json")
				raw, err := os.ReadFile(path)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(d.dir, "bad.json"), []byte(strings.ReplaceAll(string(raw), bad.EventID, "bad")), 0600))
				require.NoError(t, os.Remove(path))
			}
			require.NoError(t, d.stage(good))
			delivered := []string{}
			d.write = func(_ context.Context, e TrafficEvent) error {
				if e.EventID == bad.EventID {
					if kind == "constraint" {
						return errors.Join(errors.New("write"), &pq.Error{Code: "23514"})
					}
					if kind == "data_exception" {
						return &pq.Error{Code: "22001"}
					}
					t.Fatal("invalid metadata reached database writer")
				}
				delivered = append(delivered, e.EventID)
				return nil
			}
			require.NoError(t, d.replay(context.Background()))
			require.Equal(t, []string{good.EventID}, delivered)
			files, err := filepath.Glob(filepath.Join(d.dir, "*.json.invalid"))
			require.NoError(t, err)
			require.Len(t, files, 1)
			health, err := d.Health(context.Background())
			require.NoError(t, err)
			require.Equal(t, "degraded", health.Status)
			require.Zero(t, health.PendingEvents)
			require.EqualValues(t, 1, health.QuarantinedEvents)
			require.NotNil(t, health.LastDeliveryAt)
		})
	}
}

func TestTrafficDeliveryRetainsRetryableErrors(t *testing.T) {
	for _, code := range []string{"08006", "40001", "42P01", "42501", ""} {
		t.Run(code, func(t *testing.T) {
			d := stoppedTrafficDelivery(t)
			event := trafficDeliveryFixture()
			require.NoError(t, d.stage(event))
			failure := error(&pq.Error{Code: pq.ErrorCode(code)})
			if code == "" {
				failure = context.DeadlineExceeded
			}
			d.write = func(context.Context, TrafficEvent) error { return failure }
			require.Error(t, d.replay(context.Background()))
			_, err := os.Stat(filepath.Join(d.dir, event.EventID+".json"))
			require.NoError(t, err)
			health, err := d.Health(context.Background())
			require.NoError(t, err)
			require.EqualValues(t, 1, health.PendingEvents)
			require.Zero(t, health.QuarantinedEvents)
			require.EqualValues(t, 1, health.DeliveryFailures)
			d.write = func(context.Context, TrafficEvent) error { return nil }
			require.NoError(t, d.replay(context.Background()))
			health, err = d.Health(context.Background())
			require.NoError(t, err)
			require.Zero(t, health.PendingEvents)
			require.Equal(t, "degraded", health.Status) // Worker is deliberately stopped in this disk unit test.
		})
	}
}

func TestTrafficDeliveryHealthTracksBacklogAndFailures(t *testing.T) {
	d := stoppedTrafficDelivery(t)
	d.running.Store(true) // Isolate backlog classification from intentionally stopped worker state.
	event := trafficDeliveryFixture()
	require.NoError(t, d.stage(event))
	old := time.Now().Add(-90 * time.Second)
	require.NoError(t, os.Chtimes(filepath.Join(d.dir, event.EventID+".json"), old, old))
	require.NoError(t, os.WriteFile(filepath.Join(d.dir, ".traffic-in-progress"), []byte("ignore"), 0600))
	require.NoError(t, d.scanDisk(context.Background()))
	health, err := d.Health(context.Background())
	require.NoError(t, err)
	require.Equal(t, "delayed", health.Status)
	require.EqualValues(t, 1, health.PendingEvents)
	require.GreaterOrEqual(t, health.OldestPendingAgeSeconds, int64(90))
	require.NotNil(t, health.OldestPendingAt)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = d.Health(ctx)
	require.ErrorIs(t, err, context.Canceled)

}

func TestTrafficDeliveryConcurrentHealthAndRecording(t *testing.T) {
	d, err := NewTrafficDelivery(t.TempDir(), func(context.Context, TrafficEvent) error { return nil })
	require.NoError(t, err)
	defer d.Stop()
	var work sync.WaitGroup
	for i := 0; i < 4; i++ {
		work.Go(func() {
			for j := 0; j < 8; j++ {
				d.RecordTraffic(trafficDeliveryFixture())
				_, err := d.Health(context.Background())
				require.NoError(t, err)
			}
		})
	}
	work.Wait()
	require.Eventually(t, func() bool {
		h, err := d.Health(context.Background())
		return err == nil && h.PendingEvents == 0 && h.DeliveredEvents == 32
	}, 5*time.Second, 10*time.Millisecond)
}

func TestTrafficDeliveryReplayIsBounded(t *testing.T) {
	d := stoppedTrafficDelivery(t)
	for i := 0; i < trafficBatchSize+1; i++ {
		require.NoError(t, d.stage(trafficDeliveryFixture()))
	}
	require.NoError(t, d.replay(context.Background()))
	h, err := d.Health(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, h.PendingEvents)
	require.NoError(t, d.replay(context.Background()))
	h, err = d.Health(context.Background())
	require.NoError(t, err)
	require.Zero(t, h.PendingEvents)
}

func TestTrafficDeliveryDoesNotWaitForBlockedStorageAndDropsOverflow(t *testing.T) {
	entered, releaseCh := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseCh) })
	// Trigger fallback spooling by failing the primary database write, then block inside stage.
	d, err := newTrafficDelivery(func() string { return t.TempDir() }, func(context.Context, TrafficEvent) error { return errors.New("db offline") }, trafficDeliveryOptions{queueCapacity: 2, stopBudget: 25 * time.Millisecond, stage: func(TrafficEvent) error { close(entered); <-releaseCh; return nil }})
	require.NoError(t, err)
	t.Cleanup(func() { release(); d.Stop() })
	d.RecordTraffic(trafficDeliveryFixture())
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not enter blocked storage")
	}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 3; i++ {
			d.RecordTraffic(trafficDeliveryFixture())
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("request waited for background storage")
	}
	h, err := d.Health(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, h.QueuedEvents)
	require.EqualValues(t, 1, h.QueueDroppedEvents)
	before := time.Now()
	d.Stop()
	require.Less(t, time.Since(before), 200*time.Millisecond)
	d.RecordTraffic(trafficDeliveryFixture())
	release()
	select {
	case <-d.done:
	case <-time.After(time.Second):
		t.Fatal("worker failed to exit after storage unblocked")
	}
	h, err = d.Health(context.Background())
	require.NoError(t, err)
	require.Zero(t, h.QueuedEvents)
	require.EqualValues(t, 3, h.ShutdownDroppedEvents)
}

func TestTrafficDeliveryConstructorAndHealthDoNotWaitForDirectoryResolution(t *testing.T) {
	entered, releaseCh := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseCh) })
	dir := t.TempDir()
	started := time.Now()
	d, err := newTrafficDelivery(func() string { close(entered); <-releaseCh; return dir }, func(context.Context, TrafficEvent) error { return nil }, trafficDeliveryOptions{queueCapacity: 2, stopBudget: 25 * time.Millisecond})
	require.NoError(t, err)
	require.Less(t, time.Since(started), 200*time.Millisecond)
	t.Cleanup(func() { release(); d.Stop() })
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("directory resolver did not start")
	}
	d.RecordTraffic(trafficDeliveryFixture())
	h, err := d.Health(context.Background())
	require.NoError(t, err)
	require.False(t, h.StorageAvailable)
	require.Equal(t, 1, h.QueuedEvents)
	started = time.Now()
	d.Stop()
	require.Less(t, time.Since(started), 200*time.Millisecond)
	release()
	select {
	case <-d.done:
	case <-time.After(time.Second):
		t.Fatal("worker failed to stop")
	}
}

func TestTrafficDeliveryBadMountFallsBackOnlyOnWorker(t *testing.T) {
	parent := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(parent, "file"), []byte("not a directory"), 0600))
	entered, releaseCh := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseCh) })
	d, err := NewTrafficDelivery(filepath.Join(parent, "file", "pending"), func(ctx context.Context, _ TrafficEvent) error {
		close(entered)
		select {
		case <-releaseCh:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	require.NoError(t, err)
	t.Cleanup(func() { release(); d.Stop() })
	returned := make(chan struct{})
	go func() { d.RecordTraffic(trafficDeliveryFixture()); close(returned) }()
	select {
	case <-returned:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("request waited for failed mount")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("background fallback did not run")
	}
	h, err := d.Health(context.Background())
	require.NoError(t, err)
	require.False(t, h.StorageAvailable)
	require.True(t, h.WorkerRunning)
	release()
	require.Eventually(t, func() bool { return d.delivered.Load() == 1 }, time.Second, time.Millisecond)
}

func TestTrafficDeliveryFailureAndQuotaDropsAreObservable(t *testing.T) {
	d := stoppedTrafficDelivery(t)
	d.maxSpoolFiles = 1
	require.NoError(t, d.stage(trafficDeliveryFixture()))
	d.write = func(context.Context, TrafficEvent) error {
		t.Error("full spool should not bypass its budget")
		return nil
	}
	d.persist(context.Background(), trafficDeliveryFixture())
	h, err := d.Health(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, h.SpoolLimitDroppedEvents)
	d.maxSpoolFiles = trafficMaxSpoolFiles
	d.maxSpoolBytes = 1
	d.persist(context.Background(), trafficDeliveryFixture())
	h, err = d.Health(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 2, h.SpoolLimitDroppedEvents)
	d.maxSpoolBytes = trafficMaxSpoolBytes
	d.dir = filepath.Join(d.dir, "missing")
	d.write = func(context.Context, TrafficEvent) error { return errors.New("offline") }
	d.persist(context.Background(), trafficDeliveryFixture())
	h, err = d.Health(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, h.StagingFailures)
	require.EqualValues(t, 1, h.PersistenceFailures)
	require.EqualValues(t, 3, h.DroppedEvents)
}

func TestTrafficDeliveryWorkerPanicCannotCrashBusinessProcess(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(context.Context, TrafficEvent) error
		stage func(TrafficEvent) error
	}{
		{
			name:  "primary_write_panic",
			write: func(context.Context, TrafficEvent) error { panic("simulated primary write failure") },
		},
		{
			name:  "fallback_stage_panic",
			write: func(context.Context, TrafficEvent) error { return errors.New("db offline") },
			stage: func(TrafficEvent) error { panic("simulated auxiliary failure") },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := newTrafficDelivery(func() string { return t.TempDir() }, tc.write, trafficDeliveryOptions{queueCapacity: 2, stopBudget: time.Second, stage: tc.stage})
			require.NoError(t, err)
			defer d.Stop()
			d.RecordTraffic(trafficDeliveryFixture())
			select {
			case <-d.done:
			case <-time.After(time.Second):
				t.Fatal("panicking worker did not exit")
			}
			h, err := d.Health(context.Background())
			require.NoError(t, err)
			require.EqualValues(t, 1, h.WorkerPanics)
			require.EqualValues(t, 1, h.DroppedEvents)
			require.False(t, h.WorkerRunning)
			d.RecordTraffic(trafficDeliveryFixture())
			h, err = d.Health(context.Background())
			require.NoError(t, err)
			require.EqualValues(t, 2, h.DroppedEvents)
		})
	}
}

func TestTrafficDeliveryAssignsIndependentIDsAfterHandoff(t *testing.T) {
	ids := make(chan string, 2)
	d, err := NewTrafficDelivery(t.TempDir(), func(_ context.Context, event TrafficEvent) error { ids <- event.EventID; return nil })
	require.NoError(t, err)
	defer d.Stop()
	event := trafficDeliveryFixture()
	event.EventID = ""
	event.RequestID = "same-client-retry"
	d.RecordTraffic(event)
	d.RecordTraffic(event)
	var received []string
	for range 2 {
		select {
		case id := <-ids:
			received = append(received, id)
		case <-time.After(3 * time.Second):
			t.Fatal("events were not delivered")
		}
	}
	require.NotEqual(t, received[0], received[1])
	_, err = uuid.Parse(received[0])
	require.NoError(t, err)
}

func TestTrafficDeliveryConcurrentProducersAndStop(t *testing.T) {
	d, err := newTrafficDelivery(func() string { return t.TempDir() }, func(context.Context, TrafficEvent) error { return nil }, trafficDeliveryOptions{queueCapacity: 2, stopBudget: 50 * time.Millisecond, stage: func(TrafficEvent) error { return nil }})
	require.NoError(t, err)
	var producers sync.WaitGroup
	for range 4 {
		producers.Go(func() {
			for range 1000 {
				d.RecordTraffic(trafficDeliveryFixture())
			}
		})
	}
	d.Stop()
	producers.Wait()
	d.Stop()
	h, err := d.Health(context.Background())
	require.NoError(t, err)
	require.Zero(t, h.QueuedEvents)
	require.Greater(t, h.DroppedEvents, int64(0))
}

func BenchmarkTrafficRecordNonblocking(b *testing.B) {
	for _, full := range []bool{false, true} {
		name := "enqueue"
		if full {
			name = "full"
		}
		b.Run(name, func(b *testing.B) {
			d := &TrafficDelivery{queue: make(chan TrafficEvent, 1)}
			event := trafficDeliveryFixture()
			if full {
				d.queue <- event
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				d.RecordTraffic(event)
				if !full {
					<-d.queue
				}
			}
		})
	}
}

func TestTrafficDeliveryBatchRetryAndPermanentIsolation(t *testing.T) {
	d := stoppedTrafficDelivery(t)
	bad, good := trafficDeliveryFixture(), trafficDeliveryFixture()
	require.NoError(t, d.stage(bad))
	require.NoError(t, d.stage(good))
	calls := 0
	d.writeBatch = func(_ context.Context, events []TrafficEvent) error {
		calls++
		require.Len(t, events, 2)
		return errors.New("temporary database outage")
	}
	d.write = func(context.Context, TrafficEvent) error { t.Fatal("retryable batch must not fan out"); return nil }
	require.Error(t, d.replay(context.Background()))
	require.EqualValues(t, 2, d.pendingFiles)
	require.Equal(t, 1, calls)
	d.writeBatch = func(context.Context, []TrafficEvent) error { return &pq.Error{Code: "23514"} }
	d.write = func(_ context.Context, e TrafficEvent) error {
		if e.EventID == bad.EventID {
			return &pq.Error{Code: "23514"}
		}
		return nil
	}
	require.NoError(t, d.replay(context.Background()))
	require.Zero(t, d.pendingFiles)
	require.EqualValues(t, 1, d.delivered.Load())
	require.FileExists(t, filepath.Join(d.dir, bad.EventID+".json.invalid"))
}

func TestTrafficDeliveryBatchRecoveryRetainsFilesUntilCommit(t *testing.T) {
	d := stoppedTrafficDelivery(t)
	for range 65 {
		require.NoError(t, d.stage(trafficDeliveryFixture()))
	}
	var sizes []int
	d.writeBatch = func(_ context.Context, events []TrafficEvent) error {
		sizes = append(sizes, len(events))
		for _, e := range events {
			require.FileExists(t, filepath.Join(d.dir, e.EventID+".json"))
		}
		return nil
	}
	for range 3 {
		require.NoError(t, d.replay(context.Background()))
	}
	require.Equal(t, []int{32, 32, 1}, sizes)
	require.Zero(t, d.pendingFiles)
	require.EqualValues(t, 65, d.delivered.Load())
}

func TestTrafficDeliveryWakesWithoutPollingAndRecoversBacklog(t *testing.T) {
	dir := t.TempDir()
	seed := stoppedTrafficDelivery(t)
	seed.dir = dir
	for range 128 {
		require.NoError(t, seed.stage(trafficDeliveryFixture()))
	}
	var delivered atomic.Int64
	d, err := NewTrafficDeliveryBatchedResolved(func() string { return dir }, func(context.Context, TrafficEvent) error { t.Error("unexpected single write"); return nil }, func(_ context.Context, events []TrafficEvent) error { delivered.Add(int64(len(events))); return nil })
	require.NoError(t, err)
	defer d.Stop()
	for range 64 {
		d.RecordTraffic(trafficDeliveryFixture())
	}
	require.Eventually(t, func() bool { return delivered.Load() == 192 }, 5*time.Second, 10*time.Millisecond)
	h, err := d.Health(context.Background())
	require.NoError(t, err)
	require.Zero(t, h.DroppedEvents)
	require.Eventually(t, func() bool { h, _ := d.Health(context.Background()); return h.PendingEvents == 0 }, time.Second, time.Millisecond)
}

func TestTrafficDeliverySteadyStateBatchesInMemoryWithZeroDiskWrites(t *testing.T) {
	dir := t.TempDir()
	var (
		mu         sync.Mutex
		batchSizes []int
		delivered  int64
		stageCalls atomic.Int64
	)
	d, err := newTrafficDelivery(
		func() string { return dir },
		func(context.Context, TrafficEvent) error {
			t.Error("unexpected single write in healthy batched steady state")
			return nil
		},
		trafficDeliveryOptions{
			queueCapacity: trafficQueueCapacity,
			stopBudget:    time.Second,
			stage: func(TrafficEvent) error {
				stageCalls.Add(1)
				return errors.New("unexpected disk spool during healthy steady state")
			},
			writeBatch: func(_ context.Context, events []TrafficEvent) error {
				mu.Lock()
				batchSizes = append(batchSizes, len(events))
				delivered += int64(len(events))
				mu.Unlock()
				return nil
			},
		},
	)
	require.NoError(t, err)
	defer d.Stop()

	// Send 300 events: must split into <=128-row batches and flush the tail via 50ms window.
	const total = 300
	for range total {
		d.RecordTraffic(trafficDeliveryFixture())
	}
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return delivered == total
	}, 3*time.Second, 10*time.Millisecond)

	d.Stop()

	require.Zero(t, stageCalls.Load(), "steady-state path must not touch disk spool")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "spool directory must remain completely empty in steady state")

	mu.Lock()
	require.LessOrEqual(t, len(batchSizes), 20, "events must be batched rather than written one by one")
	for _, size := range batchSizes {
		require.LessOrEqual(t, size, trafficMemoryBatchMax, "each batch must respect RecordTrafficBatch's 128-row cap")
	}
	mu.Unlock()

	h, err := d.Health(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, total, h.DeliveredEvents)
	require.Zero(t, h.PendingEvents)
	require.Zero(t, h.DroppedEvents)
	require.Zero(t, h.DeliveryFailures)
}

func TestTrafficDeliveryFallsBackToSpoolOnOutageAndRecovers(t *testing.T) {
	dir := t.TempDir()
	var (
		outage    atomic.Bool
		mu        sync.Mutex
		delivered = map[string]int{}
	)
	d, err := NewTrafficDeliveryBatchedResolved(
		func() string { return dir },
		func(context.Context, TrafficEvent) error {
			t.Error("unexpected single write")
			return nil
		},
		func(_ context.Context, events []TrafficEvent) error {
			if outage.Load() {
				return errors.New("postgres unavailable")
			}
			mu.Lock()
			for _, e := range events {
				delivered[e.EventID]++
			}
			mu.Unlock()
			return nil
		},
	)
	require.NoError(t, err)
	defer d.Stop()

	// 1. Healthy steady state: delivered directly with zero spool files.
	healthy := trafficDeliveryFixture()
	d.RecordTraffic(healthy)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return delivered[healthy.EventID] == 1
	}, 2*time.Second, 10*time.Millisecond)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)

	// 2. Database outage: events spill to local spool files.
	outage.Store(true)
	var failedIDs []string
	for range 5 {
		e := trafficDeliveryFixture()
		failedIDs = append(failedIDs, e.EventID)
		d.RecordTraffic(e)
	}
	require.Eventually(t, func() bool {
		h, err := d.Health(context.Background())
		return err == nil && h.PendingEvents == 5
	}, 2*time.Second, 10*time.Millisecond)
	for _, id := range failedIDs {
		require.FileExists(t, filepath.Join(dir, id+".json"))
	}

	// 3. Database recovers: spooled backlog replays and spool empties; new events skip disk again.
	outage.Store(false)
	require.Eventually(t, func() bool {
		h, err := d.Health(context.Background())
		if err != nil || h.PendingEvents != 0 {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		for _, id := range failedIDs {
			if delivered[id] != 1 {
				return false
			}
		}
		return true
	}, 5*time.Second, 25*time.Millisecond)

	entries, err = os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)

	after := trafficDeliveryFixture()
	d.RecordTraffic(after)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return delivered[after.EventID] == 1
	}, 2*time.Second, 10*time.Millisecond)
	entries, err = os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestTrafficDeliveryShutdownSpoolsUnpersistedEventsAndReplaysOnRestart(t *testing.T) {
	dir := t.TempDir()
	entered, releaseGate := make(chan struct{}), make(chan struct{})
	var (
		attemptMu        sync.Mutex
		shutdownAttempts []string
	)
	d, err := NewTrafficDeliveryBatchedResolved(
		func() string {
			close(entered)
			<-releaseGate
			return dir
		},
		func(context.Context, TrafficEvent) error { return errors.New("offline") },
		func(_ context.Context, batch []TrafficEvent) error {
			attemptMu.Lock()
			for _, e := range batch {
				shutdownAttempts = append(shutdownAttempts, e.EventID)
			}
			attemptMu.Unlock()
			return errors.New("offline")
		},
	)
	require.NoError(t, err)
	<-entered

	events := []TrafficEvent{trafficDeliveryFixture(), trafficDeliveryFixture(), trafficDeliveryFixture()}
	wantIDs := make([]string, len(events))
	for i, e := range events {
		wantIDs[i] = e.EventID
		d.RecordTraffic(e)
	}
	// Hold the worker until Stop has signaled so drain deterministically handles all events.
	stopped := make(chan struct{})
	go func() {
		d.Stop()
		close(stopped)
	}()
	require.Eventually(t, func() bool { return d.stopping.Load() }, time.Second, time.Millisecond)
	close(releaseGate)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return promptly")
	}

	attemptMu.Lock()
	require.ElementsMatch(t, wantIDs, shutdownAttempts, "drain must attempt PostgreSQL delivery before spooling")
	attemptMu.Unlock()
	for _, e := range events {
		require.FileExists(t, filepath.Join(dir, e.EventID+".json"))
	}

	var (
		mu       sync.Mutex
		replayed []string
	)
	restarted, err := NewTrafficDeliveryBatchedResolved(
		func() string { return dir },
		func(context.Context, TrafficEvent) error { t.Error("unexpected single write"); return nil },
		func(_ context.Context, batch []TrafficEvent) error {
			mu.Lock()
			for _, e := range batch {
				replayed = append(replayed, e.EventID)
			}
			mu.Unlock()
			return nil
		},
	)
	require.NoError(t, err)
	defer restarted.Stop()

	require.Eventually(t, func() bool {
		h, err := restarted.Health(context.Background())
		return err == nil && h.PendingEvents == 0 && h.DeliveredEvents == int64(len(events))
	}, 3*time.Second, 10*time.Millisecond)

	restarted.Stop()

	mu.Lock()
	require.ElementsMatch(t, wantIDs, replayed)
	mu.Unlock()
	jsonFiles, err := filepath.Glob(filepath.Join(dir, "*.json"))
	require.NoError(t, err)
	require.Empty(t, jsonFiles)
}

func TestTrafficDeliveryIsolatesPermanentBatchErrorInMemoryWithoutDisk(t *testing.T) {
	dir := t.TempDir()
	good1, bad, good2 := trafficDeliveryFixture(), trafficDeliveryFixture(), trafficDeliveryFixture()
	var (
		mu        sync.Mutex
		delivered []string
	)
	d, err := NewTrafficDeliveryBatchedResolved(
		func() string { return dir },
		func(_ context.Context, e TrafficEvent) error {
			if e.EventID == bad.EventID {
				return &pq.Error{Code: "23514"}
			}
			mu.Lock()
			delivered = append(delivered, e.EventID)
			mu.Unlock()
			return nil
		},
		func(context.Context, []TrafficEvent) error {
			return &pq.Error{Code: "23514"}
		},
	)
	require.NoError(t, err)
	defer d.Stop()

	d.RecordTraffic(good1)
	d.RecordTraffic(bad)
	d.RecordTraffic(good2)
	require.Eventually(t, func() bool {
		h, err := d.Health(context.Background())
		return err == nil && h.DeliveredEvents == 2 && h.InvalidDroppedEvents == 1
	}, 2*time.Second, 10*time.Millisecond)

	mu.Lock()
	require.ElementsMatch(t, []string{good1.EventID, good2.EventID}, delivered)
	mu.Unlock()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestTrafficDeliveryReconcilesStalePendingCountWithoutSpinning(t *testing.T) {
	dir := t.TempDir()
	var (
		outage    atomic.Bool
		delivered atomic.Int64
	)
	outage.Store(true)
	d, err := NewTrafficDeliveryBatchedResolved(
		func() string { return dir },
		func(context.Context, TrafficEvent) error { return errors.New("unexpected single write") },
		func(_ context.Context, events []TrafficEvent) error {
			if outage.Load() {
				return errors.New("postgres unavailable")
			}
			delivered.Add(int64(len(events)))
			return nil
		},
	)
	require.NoError(t, err)
	defer d.Stop()

	// Enqueue the exact same EventID twice while PostgreSQL is offline: both stage
	// to <eventID>.json, overwriting the single spool file while pendingFiles becomes 2.
	dup := trafficDeliveryFixture()
	d.RecordTraffic(dup)
	d.RecordTraffic(dup)

	require.Eventually(t, func() bool {
		_, statErr := os.Stat(filepath.Join(dir, dup.EventID+".json"))
		h, healthErr := d.Health(context.Background())
		return statErr == nil && healthErr == nil && h.PendingEvents >= 1
	}, 2*time.Second, 10*time.Millisecond)

	outage.Store(false)
	require.Eventually(t, func() bool {
		h, err := d.Health(context.Background())
		return err == nil && h.PendingEvents == 0 && delivered.Load() >= 1
	}, 4*time.Second, 20*time.Millisecond, "stale pendingFiles after duplicate EventID overwrite must reconcile to 0")
}

func TestTrafficDeliveryShutdownSpoolsBeforeStopBudgetExpiresOnHangingDB(t *testing.T) {
	for _, tc := range []struct {
		name            string
		stopBudget      time.Duration
		expectDBAttempt bool
	}{
		{name: "tight_budget_skips_hanging_db", stopBudget: 180 * time.Millisecond, expectDBAttempt: false},
		{name: "bounded_db_attempt_then_spools", stopBudget: 600 * time.Millisecond, expectDBAttempt: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			gate := make(chan struct{})
			entered := make(chan struct{})
			var writeCalls atomic.Int64
			d, err := newTrafficDelivery(
				func() string {
					close(entered)
					<-gate
					return dir
				},
				func(ctx context.Context, _ TrafficEvent) error {
					writeCalls.Add(1)
					<-ctx.Done()
					return ctx.Err()
				},
				trafficDeliveryOptions{
					queueCapacity: trafficQueueCapacity,
					stopBudget:    tc.stopBudget,
					writeBatch: func(ctx context.Context, _ []TrafficEvent) error {
						writeCalls.Add(1)
						<-ctx.Done()
						return ctx.Err()
					},
				},
			)
			require.NoError(t, err)
			<-entered

			events := []TrafficEvent{trafficDeliveryFixture(), trafficDeliveryFixture(), trafficDeliveryFixture()}
			for _, e := range events {
				d.RecordTraffic(e)
			}

			stopped := make(chan struct{})
			go func() {
				d.Stop()
				close(stopped)
			}()
			require.Eventually(t, func() bool { return d.stopping.Load() }, time.Second, time.Millisecond)
			close(gate)

			select {
			case <-stopped:
			case <-time.After(2 * time.Second):
				t.Fatal("Stop did not return within bound")
			}

			if tc.expectDBAttempt {
				require.EqualValues(t, 1, writeCalls.Load())
			} else {
				require.Zero(t, writeCalls.Load())
			}
			for _, e := range events {
				require.FileExists(t, filepath.Join(dir, e.EventID+".json"), "hanging DB during shutdown must still spool unpersisted events")
			}
			h, err := d.Health(context.Background())
			require.NoError(t, err)
			require.Zero(t, h.ShutdownDroppedEvents)
		})
	}
}

func TestTrafficDeliveryReconcilesSpoolCapacityAfterManualCleanup(t *testing.T) {
	d := stoppedTrafficDelivery(t)
	d.maxSpoolFiles = 1

	bad := trafficDeliveryFixture()
	require.NoError(t, d.stage(bad))
	require.NoError(t, d.quarantine(filepath.Join(d.dir, bad.EventID+".json")))

	// Spool is full due to the quarantined file; fallback persistence hits quota.
	d.persist(context.Background(), trafficDeliveryFixture())
	require.EqualValues(t, 1, d.spoolDrops.Load())
	require.True(t, d.spoolNeedsReconcile)

	// Operator manually removes the quarantined file.
	require.NoError(t, os.Remove(filepath.Join(d.dir, bad.EventID+".json.invalid")))

	// Bounded health reconciliation detects the freed quota and clears quarantined count.
	d.reconcileHealth(context.Background())
	require.False(t, d.spoolNeedsReconcile)
	require.Zero(t, d.spoolFiles)
	require.Zero(t, d.spoolBytes)
	h, err := d.Health(context.Background())
	require.NoError(t, err)
	require.Zero(t, h.QuarantinedEvents)

	// Subsequent fallback spooling succeeds again.
	next := trafficDeliveryFixture()
	d.persist(context.Background(), next)
	require.EqualValues(t, 1, d.spoolDrops.Load())
	require.FileExists(t, filepath.Join(d.dir, next.EventID+".json"))

	// Healthy empty steady state skips disk reconciliation completely.
	require.NoError(t, os.Remove(filepath.Join(d.dir, next.EventID+".json")))
	require.NoError(t, d.scanDisk(context.Background()))
	checkedBefore := d.disk.Load().CheckedAt
	d.dir = filepath.Join(d.dir, "missing-must-not-be-scanned")
	d.reconcileHealth(context.Background())
	require.True(t, d.disk.Load().Available, "healthy empty steady state must not scan disk")
	require.Equal(t, checkedBefore, d.disk.Load().CheckedAt)
}

func TestTrafficDeliveryReplayFilesystemErrorDoesNotPoisonHealthyDBDelivery(t *testing.T) {
	t.Run("acknowledge_remove_failure_after_db_commit", func(t *testing.T) {
		d := stoppedTrafficDelivery(t)
		var delivered []string
		d.writeBatch = func(_ context.Context, events []TrafficEvent) error {
			for _, e := range events {
				delivered = append(delivered, e.EventID)
			}
			return nil
		}
		d.removeFile = func(string) error {
			return os.ErrPermission
		}

		spooled := trafficDeliveryFixture()
		require.NoError(t, d.stage(spooled))

		// Simulate an expired previous DB backoff; replay commits to PG, then fails os.Remove.
		nextDBRetry := time.Now().Add(-time.Second)
		var nextReplayRetry time.Time
		d.replayPendingCycle(context.Background(), &nextDBRetry, &nextReplayRetry)

		require.True(t, nextDBRetry.IsZero(), "successful PG commit must clear DB backoff even when spool unlink fails")
		require.True(t, nextReplayRetry.After(time.Now()), "spool unlink failure must use replay backoff")
		require.False(t, d.disk.Load().Available, "spool unlink failure must mark storage unavailable")

		// Fresh incoming traffic must still deliver directly to healthy PG without dropping.
		fresh := trafficDeliveryFixture()
		d.processing.Store(1)
		d.flushMemoryBatch(context.Background(), []TrafficEvent{fresh}, &nextDBRetry, trafficDirectWriteBudget)

		require.Contains(t, delivered, spooled.EventID)
		require.Contains(t, delivered, fresh.EventID)
		require.Zero(t, d.persistenceFailures.Load())
		require.Zero(t, d.droppedEvents())
	})

	t.Run("unreadable_spool_dir_before_db_write", func(t *testing.T) {
		d := stoppedTrafficDelivery(t)
		var delivered []string
		d.writeBatch = func(_ context.Context, events []TrafficEvent) error {
			for _, e := range events {
				delivered = append(delivered, e.EventID)
			}
			return nil
		}

		spooled := trafficDeliveryFixture()
		require.NoError(t, d.stage(spooled))
		// Point dir to a non-existent path inside a regular file so os.Open/MkdirAll fails.
		blocker := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(blocker, []byte("x"), 0600))
		d.dir = filepath.Join(blocker, "spool")

		var nextDBRetry, nextReplayRetry time.Time
		d.replayPendingCycle(context.Background(), &nextDBRetry, &nextReplayRetry)

		require.True(t, nextDBRetry.IsZero(), "spool open error must not trigger DB backoff")
		require.True(t, nextReplayRetry.After(time.Now()))
		require.False(t, d.disk.Load().Available)

		fresh := trafficDeliveryFixture()
		d.processing.Store(1)
		d.flushMemoryBatch(context.Background(), []TrafficEvent{fresh}, &nextDBRetry, trafficDirectWriteBudget)

		require.Equal(t, []string{fresh.EventID}, delivered)
		require.Zero(t, d.persistenceFailures.Load())
		require.Zero(t, d.droppedEvents())
	})
}
