//go:build unit

package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestBatchImageWorkerRuntime_QueueDisabledDoesNotStart(t *testing.T) {
	queue := &blockingBatchImageRuntimeQueue{}
	runtime := NewBatchImageWorkerRuntime(
		NewBatchImageWorker(queue, &fakeBatchImageProcessor{}, BatchImageWorkerOptions{}),
		&config.Config{BatchImage: config.BatchImageConfig{QueueEnabled: false}},
	)

	runtime.Start()

	require.False(t, runtime.Running())
	require.Zero(t, queue.reserveCalls.Load())
	require.NotPanics(t, runtime.Stop)
}

func TestBatchImageWorkerRuntime_QueueEnabledStartsAndStops(t *testing.T) {
	queue := &blockingBatchImageRuntimeQueue{}
	processor := &fakeBatchImageProcessor{}
	runtime := NewBatchImageWorkerRuntime(
		NewBatchImageWorker(queue, processor, BatchImageWorkerOptions{
			DelayedPollInterval: time.Hour,
			RecoveryInterval:    time.Hour,
		}),
		&config.Config{BatchImage: config.BatchImageConfig{QueueEnabled: true}},
	)

	runtime.Start()

	require.Eventually(t, func() bool {
		return runtime.Running() && queue.reserveCalls.Load() > 0
	}, time.Second, 10*time.Millisecond)
	require.Empty(t, processor.processed)
	require.NotPanics(t, runtime.Stop)
	require.False(t, runtime.Running())
	require.NotPanics(t, runtime.Stop)
}

func TestBatchImageWorkerRuntime_AutomaticallyRecoversSubmittedQueueFailure(t *testing.T) {
	providerJobName := "providers/gemini_api/job"
	queueFailure := "QUEUE_FAILED"
	batchID := "imgbatch_runtime_queue_recovery"
	repo := newFakeBatchImageRepository()
	repo.jobs[batchID] = &BatchImageJob{
		BatchID:         batchID,
		Status:          BatchImageJobStatusSubmitted,
		ProviderJobName: &providerJobName,
		LastErrorCode:   &queueFailure,
	}
	queue := &blockingBatchImageRuntimeQueue{}
	runtime := NewBatchImageWorkerRuntime(
		NewBatchImageWorker(queue, &fakeBatchImageProcessor{}, BatchImageWorkerOptions{
			DelayedPollInterval: time.Hour,
			RecoveryInterval:    time.Hour,
		}),
		&config.Config{BatchImage: config.BatchImageConfig{QueueEnabled: true}},
	)
	runtime.billingRecovery = &BatchImageBillingRecoveryService{Repo: repo, Queue: queue, Limit: 10}

	runtime.Start()
	require.Eventually(t, func() bool {
		return queue.enqueueCalls.Load() == 1
	}, time.Second, 10*time.Millisecond)
	runtime.Stop()

	require.Empty(t, batchImageDerefString(repo.jobs[batchID].LastErrorCode))
	require.Equal(t, int64(1), queue.enqueueCalls.Load())
}

type blockingBatchImageRuntimeQueue struct {
	reserveCalls atomic.Int64
	enqueueCalls atomic.Int64
}

func (q *blockingBatchImageRuntimeQueue) Enqueue(context.Context, string) error {
	q.enqueueCalls.Add(1)
	return nil
}

func (q *blockingBatchImageRuntimeQueue) Reserve(ctx context.Context, _ time.Duration) (ReservedBatchImageJob, error) {
	q.reserveCalls.Add(1)
	<-ctx.Done()
	return ReservedBatchImageJob{}, ctx.Err()
}

func (q *blockingBatchImageRuntimeQueue) RequeueAfter(context.Context, string, time.Duration) error {
	return nil
}

func (q *blockingBatchImageRuntimeQueue) Ack(context.Context, string) error {
	return nil
}

func (q *blockingBatchImageRuntimeQueue) Heartbeat(context.Context, string) error {
	return nil
}

func (q *blockingBatchImageRuntimeQueue) MoveDueDelayedToReady(context.Context, int) (int, error) {
	return 0, nil
}

func (q *blockingBatchImageRuntimeQueue) RecoverStaleActive(context.Context, time.Duration, int) (int, error) {
	return 0, nil
}

func (q *blockingBatchImageRuntimeQueue) TryAcquireJobLock(context.Context, string, time.Duration) (BatchImageJobLock, bool, error) {
	return nil, false, nil
}
