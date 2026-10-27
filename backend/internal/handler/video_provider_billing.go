package handler

import (
	"context"
	"time"

	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

func prepareVideoProviderCompletionBilling(ctx context.Context, h *OpenAIGatewayHandler, log *zap.Logger, key *service.APIKey, subject middleware.AuthSubject, taskID string, result *service.OpenAIForwardResult, pending *service.GrokVideoPendingBilling, provider string) *service.OpenAIForwardResult {
	if provider == "" {
		return prepareGrokVideoCompletionBilling(ctx, h, log, key, subject, taskID, result, pending)
	}
	if result == nil || result.VideoCount <= 0 || pending == nil || pending.Provider != provider ||
		key.GroupID == nil || *key.GroupID != pending.GroupID {
		return nil
	}
	if provider == service.VideoProviderSeedance && !result.VideoTokenBilling {
		return nil
	}
	if provider == service.VideoProviderMiniMaxH3 && pending.MiniMaxH3BaseCost == nil {
		return nil
	}
	if !h.gatewayService.HasDurableMediaBilling() {
		claimed, err := h.gatewayService.ClaimGrokVideoBilling(ctx, taskID, subject.UserID, key.ID)
		if err != nil || !claimed {
			return nil
		}
	}
	merged := *result
	merged.Model, merged.BillingModel = pending.Model, firstNonEmptyString(pending.BillingModel, pending.Model)
	merged.UpstreamModel = firstNonEmptyString(pending.UpstreamModel, result.UpstreamModel)
	merged.RequestID, merged.ResponseID = service.StableGrokVideoBillingRequestID(taskID), taskID
	merged.Duration = service.GrokVideoE2EDuration(pending.CreatedAt, time.Now())
	if provider == service.VideoProviderMiniMaxH3 {
		merged.VideoResolution, merged.VideoDurationSeconds = pending.VideoResolution, pending.VideoDurationSeconds
		merged.MiniMaxH3BaseCost = pending.MiniMaxH3BaseCost
	}
	return &merged
}
