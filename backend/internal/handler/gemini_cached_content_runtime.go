package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (h *GatewayHandler) ConfigureGeminiExplicitCache(repo service.GeminiCachedContentRepository, cfg *config.Config) {
	h.geminiCachedContentService = service.NewGeminiCachedContentService(repo, h.geminiCompatService, cfg)
	h.geminiCachedContentService.SetBillingGateway(h.gatewayService)
	h.geminiCachedContentService.Start()
}
func (h *GatewayHandler) StopGeminiExplicitCache() {
	if h != nil && h.geminiCachedContentService != nil {
		h.geminiCachedContentService.Stop()
	}
}
