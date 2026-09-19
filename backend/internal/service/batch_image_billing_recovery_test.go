//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type recordingBatchImageQueue struct {
	*fakeBatchImageQueue
	enqueued []string
}

func (q *recordingBatchImageQueue) Enqueue(_ context.Context, batchID string) error {
	q.enqueued = append(q.enqueued, batchID)
	return nil
}

func TestBatchImageBillingRecoveryService_ReleasesStaleUnsubmittedHold(t *testing.T) {
	repo := newFakeBatchImageRepository()
	apiKeyID := int64(22)
	holdAmount := 0.5
	stale := &BatchImageJob{
		BatchID:       "imgbatch_stale_created",
		UserID:        11,
		APIKeyID:      &apiKeyID,
		Status:        BatchImageJobStatusCreated,
		EstimatedCost: holdAmount,
		HoldAmount:    &holdAmount,
		CreatedAt:     time.Now().Add(-time.Hour),
		UpdatedAt:     time.Now().Add(-time.Hour),
	}
	activeProviderName := "providers/job"
	active := &BatchImageJob{
		BatchID:         "imgbatch_has_provider",
		UserID:          11,
		APIKeyID:        &apiKeyID,
		Status:          BatchImageJobStatusSubmitted,
		ProviderJobName: &activeProviderName,
		EstimatedCost:   holdAmount,
		HoldAmount:      &holdAmount,
		CreatedAt:       time.Now().Add(-time.Hour),
		UpdatedAt:       time.Now().Add(-time.Hour),
	}
	repo.jobs[stale.BatchID] = stale
	repo.jobs[active.BatchID] = active
	billing := &fakeBatchImageBillingRepo{}
	svc := &BatchImageBillingRecoveryService{Repo: repo, Billing: billing, StaleAfter: time.Minute, Limit: 10}

	released, err := svc.ReleaseStaleUnsubmittedOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, released)
	require.Equal(t, BatchImageJobStatusFailed, repo.jobs[stale.BatchID].Status)
	require.Equal(t, "SUBMIT_STALE_BEFORE_PROVIDER", batchImageDerefString(repo.jobs[stale.BatchID].LastErrorCode))
	require.Len(t, billing.releases, 1)
	require.Equal(t, BatchImageReleaseRequestID(stale.BatchID), billing.releases[0].RequestID)
	require.Equal(t, BatchImageJobStatusSubmitted, repo.jobs[active.BatchID].Status)
}

func TestBatchImageBillingRecoveryService_SkipsJobRefreshedByHeartbeat(t *testing.T) {
	repo := newFakeBatchImageRepository()
	apiKeyID := int64(22)
	holdAmount := 0.5
	// updated_at 在 cutoff 之后（慢提交心跳持续续期）：不得误杀退款。
	fresh := &BatchImageJob{
		BatchID:       "imgbatch_fresh_uploading",
		UserID:        11,
		APIKeyID:      &apiKeyID,
		Status:        BatchImageJobStatusUploading,
		EstimatedCost: holdAmount,
		HoldAmount:    &holdAmount,
		CreatedAt:     time.Now().Add(-time.Hour),
		UpdatedAt:     time.Now(),
	}
	repo.jobs[fresh.BatchID] = fresh
	billing := &fakeBatchImageBillingRepo{}
	svc := &BatchImageBillingRecoveryService{Repo: repo, Billing: billing, StaleAfter: time.Minute, Limit: 10}

	released, err := svc.ReleaseStaleUnsubmittedOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, released)
	require.Equal(t, BatchImageJobStatusUploading, repo.jobs[fresh.BatchID].Status)
	require.Empty(t, billing.releases)
}

func TestBatchImageBillingRecoveryService_EnqueuesRetryWhenReleaseFails(t *testing.T) {
	repo := newFakeBatchImageRepository()
	apiKeyID := int64(22)
	holdAmount := 0.5
	stale := &BatchImageJob{
		BatchID:       "imgbatch_stale_release_fail",
		UserID:        11,
		APIKeyID:      &apiKeyID,
		Status:        BatchImageJobStatusCreated,
		EstimatedCost: holdAmount,
		HoldAmount:    &holdAmount,
		CreatedAt:     time.Now().Add(-time.Hour),
		UpdatedAt:     time.Now().Add(-time.Hour),
	}
	repo.jobs[stale.BatchID] = stale
	billing := &fakeBatchImageBillingRepo{releaseErr: errors.New("billing db down")}
	queue := &recordingBatchImageQueue{fakeBatchImageQueue: newFakeBatchImageQueue("")}
	svc := &BatchImageBillingRecoveryService{Repo: repo, Billing: billing, Queue: queue, StaleAfter: time.Minute, Limit: 10}

	released, err := svc.ReleaseStaleUnsubmittedOnce(context.Background())
	// job 已转 failed、不会再出现在 stale 列表：释放失败必须入队重试
	//（由 worker 的 releaseTerminalHold 兜底），否则冻结余额永久泄漏。
	require.Error(t, err)
	require.Equal(t, 0, released)
	require.Equal(t, BatchImageJobStatusFailed, repo.jobs[stale.BatchID].Status)
	require.Equal(t, []string{stale.BatchID}, queue.enqueued)
}

func TestBatchImageBillingRecoveryService_RecoversProviderSubmittedQueueFailure(t *testing.T) {
	ctx := context.Background()
	svc, repo, queue, provider, _ := newTestBatchImagePublicService(true)
	queue.err = errors.New("redis unavailable")
	billing := svc.BillingRepo.(*fakeBatchImageBillingRepo)

	_, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "queue-recovery")
	require.ErrorIs(t, err, ErrBatchImageQueueFailed)
	require.Len(t, provider.submits, 1)
	require.Len(t, billing.reserves, 1)
	require.Empty(t, billing.releases)

	queue.err = nil
	recovery := &BatchImageBillingRecoveryService{Repo: repo, Queue: queue, Limit: 10}
	recovered, err := recovery.RecoverSubmittedQueueFailuresOnce(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, recovered)
	require.Len(t, queue.enqueued, 1)
	require.Len(t, provider.submits, 1, "recovery must not submit to provider again")
	require.Len(t, billing.reserves, 1, "recovery must not hold balance again")
	require.Empty(t, billing.releases, "submitted provider job is still running")

	for _, job := range repo.jobs {
		require.Equal(t, BatchImageJobStatusSubmitted, job.Status)
		require.NotEmpty(t, batchImageDerefString(job.ProviderJobName))
		require.Empty(t, batchImageDerefString(job.LastErrorCode))
	}

	recovered, err = recovery.RecoverSubmittedQueueFailuresOnce(ctx)
	require.NoError(t, err)
	require.Zero(t, recovered)
	require.Len(t, queue.enqueued, 1)
	require.Len(t, provider.submits, 1)
	require.Len(t, billing.reserves, 1)
}

func TestBatchImageBillingRecoveryService_TreatsAlreadyQueuedAsRecovered(t *testing.T) {
	providerJobName := "providers/gemini_api/job"
	queueFailure := "QUEUE_FAILED"
	batchID := "imgbatch_already_queued_recovery"
	repo := newFakeBatchImageRepository()
	repo.jobs[batchID] = &BatchImageJob{
		BatchID:         batchID,
		Status:          BatchImageJobStatusSubmitted,
		ProviderJobName: &providerJobName,
		LastErrorCode:   &queueFailure,
	}
	queue := &publicBatchImageQueue{enqueued: []string{batchID}}
	recovery := &BatchImageBillingRecoveryService{Repo: repo, Queue: queue, Limit: 10}

	recovered, err := recovery.RecoverSubmittedQueueFailuresOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, recovered)
	require.Equal(t, []string{batchID}, queue.enqueued)
	require.Empty(t, batchImageDerefString(repo.jobs[batchID].LastErrorCode))
}
