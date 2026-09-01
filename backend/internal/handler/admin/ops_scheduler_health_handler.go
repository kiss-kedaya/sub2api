package admin

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GetSchedulerFreshnessHealth exposes process-local scheduler canary counters.
// It intentionally contains only monotonic totals and no account/request data.
// GET /api/v1/admin/ops/scheduler-freshness/health
func (h *OpsHandler) GetSchedulerFreshnessHealth(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}
	if err := h.opsService.RequireMonitoringEnabled(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, service.SnapshotSchedulerFreshnessMetrics())
}
