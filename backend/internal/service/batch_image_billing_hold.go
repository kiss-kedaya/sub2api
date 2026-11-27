package service

import (
	"context"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const (
	batchImageHoldRequestPrefix    = "batch_image_hold:"
	batchImageCaptureRequestPrefix = "batch_image_capture:"
	batchImageReleaseRequestPrefix = "batch_image_release:"
)

func BatchImageHoldRequestID(batchID string) string {
	return batchImageHoldRequestPrefix + strings.TrimSpace(batchID)
}

func BatchImageCaptureRequestID(batchID string) string {
	return batchImageCaptureRequestPrefix + strings.TrimSpace(batchID)
}

func BatchImageReleaseRequestID(batchID string) string {
	return batchImageReleaseRequestPrefix + strings.TrimSpace(batchID)
}

func buildBatchImageHoldCommand(job *BatchImageJob, requestID string, actualAmount float64, payloadHash string) (*BatchImageBalanceHoldCommand, error) {
	if job == nil {
		return nil, ErrBatchImageBillingHoldFailed
	}
	if job.APIKeyID == nil || *job.APIKeyID <= 0 {
		return nil, ErrBatchImageSettlementMissingAPIKeyID
	}
	holdAmount := job.EstimatedCost
	if job.HoldAmount != nil {
		holdAmount = *job.HoldAmount
	}
	if holdAmount < 0 {
		holdAmount = 0
	}
	if actualAmount < 0 {
		actualAmount = 0
	}
	return &BatchImageBalanceHoldCommand{
		RequestID:          requestID,
		APIKeyID:           *job.APIKeyID,
		UserID:             job.UserID,
		BatchID:            job.BatchID,
		HoldAmount:         holdAmount,
		ActualAmount:       actualAmount,
		RequestPayloadHash: strings.TrimSpace(payloadHash),
	}, nil
}

func reserveBatchImageBalanceHold(ctx context.Context, repo UsageBillingRepository, job *BatchImageJob, payloadHash string) error {
	if repo == nil {
		return ErrBatchImageBillingHoldFailed.WithCause(errors.New("batch image billing repository is not configured"))
	}
	cmd, err := buildBatchImageHoldCommand(job, BatchImageHoldRequestID(job.BatchID), 0, payloadHash)
	if err != nil {
		return err
	}
	if cmd.HoldAmount <= 0 {
		return nil
	}
	if _, err := repo.ReserveBatchImageBalance(ctx, cmd); err != nil {
		if errors.Is(err, ErrBatchImageInsufficientBalance) {
			return ErrBatchImageInsufficientBalance
		}
		return ErrBatchImageBillingHoldFailed.WithCause(err)
	}
	return nil
}

func captureBatchImageBalanceHold(ctx context.Context, repo UsageBillingRepository, job *BatchImageJob, actualAmount float64, payloadHash string) (bool, error) {
	if repo == nil {
		return false, ErrBatchImageSettlementBillingFailed.WithCause(errors.New("batch image billing repository is not configured"))
	}
	cmd, err := buildBatchImageHoldCommand(job, BatchImageCaptureRequestID(job.BatchID), actualAmount, payloadHash)
	if err != nil {
		return false, err
	}
	res, err := repo.CaptureBatchImageBalance(ctx, cmd)
	if err != nil {
		return false, ErrBatchImageSettlementBillingFailed.WithCause(err)
	}
	return res != nil && res.Applied, nil
}

// BatchImageAPIKeyQuotaUpdater abstracts API key quota and rate-limit usage
// persistence when a batch image hold is captured during settlement.
type BatchImageAPIKeyQuotaUpdater interface {
	UpdateQuotaUsed(ctx context.Context, apiKeyID int64, cost float64) error
	UpdateRateLimitUsage(ctx context.Context, apiKeyID int64, cost float64) error
}

type batchImageBillingCacheProvider interface {
	BillingCacheService() *BillingCacheService
}

func resolveBatchImageBillingCache(explicit *BillingCacheService, authCache APIKeyAuthCacheInvalidator) *BillingCacheService {
	if explicit != nil {
		return explicit
	}
	if provider, ok := authCache.(batchImageBillingCacheProvider); ok && provider != nil {
		return provider.BillingCacheService()
	}
	return nil
}

func resolveBatchImageAPIKeyQuotaUpdater(explicit BatchImageAPIKeyQuotaUpdater, authCache APIKeyAuthCacheInvalidator) BatchImageAPIKeyQuotaUpdater {
	if explicit != nil {
		return explicit
	}
	if updater, ok := authCache.(BatchImageAPIKeyQuotaUpdater); ok && updater != nil {
		return updater
	}
	return nil
}

// invalidateBatchImageUserBalanceAndAuthCache evicts both the 5-minute Redis
// billing:balance:{uid} cache and the API key auth cache after any PG
// users.balance / frozen_balance mutation in batch image lifecycle paths.
func invalidateBatchImageUserBalanceAndAuthCache(ctx context.Context, billingCache *BillingCacheService, authCache APIKeyAuthCacheInvalidator, userID int64) {
	if userID <= 0 {
		return
	}
	if bc := resolveBatchImageBillingCache(billingCache, authCache); bc != nil {
		baseCtx := context.Background()
		if ctx != nil {
			baseCtx = context.WithoutCancel(ctx)
		}
		invCtx, cancel := context.WithTimeout(baseCtx, cacheWriteTimeout)
		_ = bc.InvalidateUserBalance(invCtx, userID)
		cancel()
	}
	if authCache != nil {
		authCache.InvalidateAuthCacheByUserID(ctx, userID)
	}
}

func releaseBatchImageBalanceHold(ctx context.Context, repo UsageBillingRepository, job *BatchImageJob, payloadHash string) error {
	if repo == nil || job == nil {
		return nil
	}
	cmd, err := buildBatchImageHoldCommand(job, BatchImageReleaseRequestID(job.BatchID), 0, payloadHash)
	if err != nil {
		return err
	}
	if cmd.HoldAmount <= 0 {
		return nil
	}
	if _, err := repo.ReleaseBatchImageBalance(ctx, cmd); err != nil {
		// 同一 release request id 出现指纹冲突，说明此前已有一次携带不同
		// payloadHash 的释放成功提交（资金已归还）。视为幂等成功，
		// 避免历史指纹不一致的 job 永远卡在释放失败的毒消息循环里。
		if errors.Is(err, ErrUsageBillingRequestConflict) {
			logger.L().Warn("batch_image.release_fingerprint_conflict_treated_as_released",
				zap.String("batch_id", job.BatchID),
			)
			return nil
		}
		return ErrBatchImageBillingHoldFailed.WithCause(err)
	}
	return nil
}
