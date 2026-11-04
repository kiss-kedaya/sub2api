package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// UpstreamMonitorHandler exposes the operations-center read model. Every GET is
// cheap: it reads accounts.extra plus bounded history tables and never probes an
// upstream or touches usage_logs.
type UpstreamMonitorHandler struct {
	svc *service.UpstreamMonitorService
}

func NewUpstreamMonitorHandler(svc *service.UpstreamMonitorService) *UpstreamMonitorHandler {
	return &UpstreamMonitorHandler{svc: svc}
}

func (h *UpstreamMonitorHandler) available(c *gin.Context) bool {
	if h == nil || h.svc == nil {
		response.Error(c, http.StatusServiceUnavailable, "upstream monitor unavailable")
		return false
	}
	return true
}

// GET /api/v1/admin/upstream-monitor/overview
func (h *UpstreamMonitorHandler) Overview(c *gin.Context) {
	if !h.available(c) {
		return
	}
	overview, err := h.svc.Overview(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, overview)
}

// GET /api/v1/admin/upstream-monitor/accounts
func (h *UpstreamMonitorHandler) Accounts(c *gin.Context) {
	if !h.available(c) {
		return
	}
	page, pageSize := response.ParsePagination(c)
	if pageSize > 200 {
		pageSize = 200
	}
	filter := service.UpstreamMonitorFilter{
		Platform:      c.Query("platform"),
		AccountType:   c.Query("type"),
		AccountStatus: c.Query("status"),
		Search:        c.Query("search"),
		BalanceStatus: c.DefaultQuery("balance_status", "all"),
		Sort:          c.DefaultQuery("sort", "name"),
		Page:          page,
		PageSize:      pageSize,
	}
	items, total, err := h.svc.List(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	pages := 0
	if pageSize > 0 {
		pages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}
	response.Success(c, response.PaginatedData{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Pages:    pages,
	})
}

// GET /api/v1/admin/upstream-monitor/accounts/:id/history?days=7
func (h *UpstreamMonitorHandler) AccountHistory(c *gin.Context) {
	if !h.available(c) {
		return
	}
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "invalid account id")
		return
	}
	days := 7
	if raw := strings.TrimSpace(c.Query("days")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 30 {
			days = parsed
		}
	}
	points, err := h.svc.History(c.Request.Context(), accountID, time.Now().UTC().AddDate(0, 0, -days))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"account_id": accountID,
		"days":       days,
		"points":     points,
	})
}

// GET /api/v1/admin/upstream-monitor/rate-changes?days=7&account_id=&page=1
func (h *UpstreamMonitorHandler) RateChanges(c *gin.Context) {
	if !h.available(c) {
		return
	}
	page, pageSize := response.ParsePagination(c)
	if pageSize > 200 {
		pageSize = 200
	}
	days := 7
	if raw := strings.TrimSpace(c.Query("days")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 90 {
			days = parsed
		}
	}
	var accountID int64
	if raw := strings.TrimSpace(c.Query("account_id")); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			accountID = parsed
		}
	}
	items, total, err := h.svc.RateChanges(c.Request.Context(), service.UpstreamRateFilter{
		AccountID: accountID,
		From:      time.Now().UTC().AddDate(0, 0, -days),
		Page:      page,
		PageSize:  pageSize,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	pages := 0
	if pageSize > 0 {
		pages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}
	response.Success(c, response.PaginatedData{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Pages:    pages,
	})
}

// POST /api/v1/admin/upstream-monitor/sample
// Re-reads the stored snapshots immediately, so a manual probe is reflected in
// the history before the next five-minute cycle. This never calls an upstream.
func (h *UpstreamMonitorHandler) Sample(c *gin.Context) {
	if !h.available(c) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	result, err := h.svc.SampleNow(ctx)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
