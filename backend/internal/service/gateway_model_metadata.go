package service

import (
	"context"
	"sync"
)

// gatewayModelMetadata bundles the lazily created model metadata catalogue so
// that lightweight GatewayService constructions (tests, tooling) never start a
// registry fetch, while production reuses one process-wide snapshot.
type gatewayModelMetadata struct {
	once    sync.Once
	catalog *ModelMetadataCatalog
}

func (g *gatewayModelMetadata) get() *ModelMetadataCatalog {
	if g == nil {
		return nil
	}
	g.once.Do(func() {
		g.catalog = NewModelMetadataCatalog(nil)
	})
	return g.catalog
}

// ModelMetadataCatalog returns the gateway's model metadata catalogue. It is
// created on first use; the registry itself is fetched asynchronously.
func (s *GatewayService) ModelMetadataCatalog() *ModelMetadataCatalog {
	if s == nil {
		return nil
	}
	return s.modelMetadata.get()
}

// EnrichModelsListBody merges catalogue metadata into a model-list body. The
// original body is returned unchanged when the catalogue is unavailable or the
// body cannot be parsed, so /v1/models never fails because of metadata.
func (s *GatewayService) EnrichModelsListBody(ctx context.Context, body []byte, platform string, resolvePricing ModelPricingResolverFunc) ([]byte, error) {
	catalog := s.ModelMetadataCatalog()
	if catalog == nil {
		return body, nil
	}
	return catalog.EnrichModelsListBody(ctx, body, platform, resolvePricing)
}

// ChannelModelPricing returns the channel price card for a group+model pair.
// The lookup is served from the channel cache used by billing, so a model list
// never queries PostgreSQL per entry.
func (s *GatewayService) ChannelModelPricing(ctx context.Context, groupID int64, modelID string) *ChannelModelPricing {
	if s == nil || s.channelService == nil || groupID <= 0 {
		return nil
	}
	return s.channelService.GetChannelModelPricing(ctx, groupID, modelID)
}

// SyncModelMetadata refreshes the models.dev registry. It is read-only: no
// pricing or billing configuration is read or written.
func (s *GatewayService) SyncModelMetadata(ctx context.Context) (*ModelMetadataSyncResult, error) {
	catalog := s.ModelMetadataCatalog()
	if catalog == nil {
		return nil, errModelMetadataCatalogUnavailable
	}
	return catalog.Sync(ctx)
}
