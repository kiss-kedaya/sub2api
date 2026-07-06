package service

import (
	"context"
	"errors"
	"time"
)

const (
	defaultBatchImageBillingRecoveryStaleAfter = 10 * time.Minute
	defaultBatchImageBillingRecoveryLimit      = 100
)

type BatchImageBillingRecoveryService struct {
	Repo       BatchImageRepository
	Billing    UsageBillingRepository
	AuthCache  APIKeyAuthCacheInvalidator
	StaleAfter time.Duration
	Limit      int
}

func (s *BatchImageBillingRecoveryService) ReleaseStaleUnsubmittedOnce(ctx context.Context) (int, error) {
	if s == nil || s.Repo == nil || s.Billing == nil {
		return 0, nil
	}
	staleAfter := s.StaleAfter
	if staleAfter <= 0 {
		staleAfter = defaultBatchImageBillingRecoveryStaleAfter
	}
	limit := s.Limit
	if limit <= 0 {
		limit = defaultBatchImageBillingRecoveryLimit
	}
	jobs, err := s.Repo.ListStaleUnsubmittedBatchImageJobs(ctx, time.Now().Add(-staleAfter), limit)
	if err != nil {
		return 0, err
	}
	released := 0
	for _, job := range jobs {
		if job == nil {
			continue
		}
		msg := "batch image submission did not reach provider before recovery cutoff"
		if err := s.Repo.TransitionBatchImageJobStatus(ctx, job.BatchID, BatchImageJobStatusFailed, BatchImageTransitionOptions{
			EventType:    "billing_hold_recovery_failed_unsubmitted",
			EventPayload: map[string]any{"batch_id": job.BatchID},
			ErrorCode:    batchImageStringPtr("SUBMIT_STALE_BEFORE_PROVIDER"),
			ErrorMessage: batchImageStringPtr(msg),
		}); err != nil && !errors.Is(err, ErrBatchImageInvalidTransition) {
			return released, err
		}
		job.Status = BatchImageJobStatusFailed
		if err := releaseBatchImageBalanceHold(ctx, s.Billing, job, batchImageDerefString(job.RequestHash)); err != nil {
			return released, err
		}
		if s.AuthCache != nil && job.UserID > 0 {
			s.AuthCache.InvalidateAuthCacheByUserID(ctx, job.UserID)
		}
		released++
	}
	return released, nil
}
