// Adapted from ranxi2001/sub2api @ 9620d315, licensed under LGPL-3.0-or-later.
package admin

import (
	"context"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/trafficquery"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *UsageHandler) TrafficKeys(c *gin.Context) {
	if middleware.IsAdminObserver(c) {
		response.Forbidden(c, "Traffic identities are unavailable for observers")
		return
	}
	userID, query, ok := trafficquery.ParseKeys(c, 0, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 4*time.Second)
	defer cancel()
	options, err := h.usageService.TrafficKeyOptions(ctx, userID, query)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, options)
}

func (h *UsageHandler) TrafficHealth(c *gin.Context) {
	if middleware.IsAdminObserver(c) {
		response.Forbidden(c, "Traffic identities are unavailable for observers")
		return
	}
	if h.trafficDelivery == nil {
		dropped := h.trafficUninitializedDrops.Load()
		response.Success(c, service.TrafficDeliveryHealth{Status: "degraded", DroppedEvents: dropped, UnavailableDroppedEvents: dropped, CheckedAt: time.Now().UTC()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 4*time.Second)
	defer cancel()
	health, err := h.trafficDelivery.Health(ctx)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Traffic delivery health unavailable")
		return
	}
	response.Success(c, health)
}

func (h *UsageHandler) ConfigureTrafficDelivery(resolveDir func() string) (func(), error) {
	d, err := service.NewTrafficDeliveryBatchedResolved(resolveDir, h.usageService.RecordTraffic, h.usageService.RecordTrafficBatch)
	if err != nil {
		return nil, err
	}
	h.trafficDelivery = d
	return d.Stop, nil
}

func (h *UsageHandler) RecordTraffic(e service.TrafficEvent) {
	if h.trafficDelivery != nil {
		h.trafficDelivery.RecordTraffic(e)
		return
	}
	// Statistics are optional: never replace an unavailable worker with a
	// synchronous database call or logging on the business request goroutine.
	h.trafficUninitializedDrops.Add(1)
}

func (h *UsageHandler) Traffic(c *gin.Context) {
	if middleware.IsAdminObserver(c) {
		response.Forbidden(c, "Traffic identities are unavailable for observers")
		return
	}
	f, ok := trafficquery.Parse(c, 0, true)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	report, err := h.usageService.Traffic(ctx, f)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, report)
}
