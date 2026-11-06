package handler

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// modelsListMetadataProvider enriches public model discovery with catalogue
// metadata (description, limits, modalities, capabilities, prices) and can
// force a registry refresh for admins. *service.GatewayService implements it.
type modelsListMetadataProvider interface {
	EnrichModelsListBody(ctx context.Context, body []byte, platform string, resolvePricing service.ModelPricingResolverFunc) ([]byte, error)
	SyncModelMetadata(ctx context.Context) (*service.ModelMetadataSyncResult, error)
}

// enrichModelsListBody merges catalogue metadata into a model-list body.
// Failures are logged and the original body is returned: metadata is an
// enhancement and must never turn /v1/models into an error.
func (h *GatewayHandler) enrichModelsListBody(c *gin.Context, platform string, body []byte) []byte {
	if h == nil || h.modelMetadata == nil || len(body) == 0 || c == nil || c.Request == nil {
		return body
	}
	enriched, err := h.modelMetadata.EnrichModelsListBody(c.Request.Context(), body, platform, h.modelsListPricingResolver(c, platform))
	if err != nil {
		slog.Warn("model list metadata enrichment failed", "error", err, "platform", platform)
		return body
	}
	return enriched
}

// modelsListPricingResolver returns the billed price card lookup for the
// requesting API key: the group price card first, then the channel price card,
// matching the billing resolver's precedence. Both lookups are cache-backed, so
// a model list does not query PostgreSQL per entry.
//
// It returns nil when no price source is available; the catalogue then omits the
// pricing fields instead of inventing a price.
func (h *GatewayHandler) modelsListPricingResolver(c *gin.Context, platform string) service.ModelPricingResolverFunc {
	if c == nil || c.Request == nil {
		return nil
	}
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || apiKey == nil {
		return nil
	}
	group := apiKey.Group
	var groupID int64
	if group != nil {
		groupID = group.ID
	}
	hasGroupPricing := group != nil && len(group.ModelPricing) > 0
	hasChannelLookup := h != nil && h.gatewayService != nil && groupID > 0
	if !hasGroupPricing && !hasChannelLookup {
		return nil
	}
	ctx := c.Request.Context()
	return func(modelID string) *service.ChannelModelPricing {
		if hasGroupPricing {
			if pricing := service.FindConfiguredModelPricing(group.ModelPricing, platform, modelID); pricing != nil {
				return pricing
			}
		}
		if hasChannelLookup {
			return h.gatewayService.ChannelModelPricing(ctx, groupID, modelID)
		}
		return nil
	}
}

// SyncModelMetadata reloads the models.dev catalogue and reports what was
// fetched. It is read-only: no price, multiplier or billing configuration is
// read or written by this handler.
// POST /api/v1/admin/models/metadata/sync
func (h *GatewayHandler) SyncModelMetadata(c *gin.Context) {
	if h == nil || h.modelMetadata == nil {
		response.Error(c, http.StatusServiceUnavailable, "Model metadata catalogue is not configured")
		return
	}
	result, err := h.modelMetadata.SyncModelMetadata(c.Request.Context())
	if err != nil {
		slog.Warn("model metadata sync failed", "error", err)
		response.Error(c, http.StatusBadGateway, "Failed to refresh the model metadata registry")
		return
	}
	response.Success(c, result)
}
