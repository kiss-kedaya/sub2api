package admin

import (
	"context"
	"encoding/csv"
	"net/http"
	"sort"
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
	minPct := 0.0
	if raw := strings.TrimSpace(c.Query("min_pct")); raw != "" {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil && parsed > 0 && parsed <= 100 {
			minPct = parsed
		}
	}
	items, total, err := h.svc.RateChanges(c.Request.Context(), service.UpstreamRateFilter{
		AccountID: accountID,
		From:      time.Now().UTC().AddDate(0, 0, -days),
		MinPct:    minPct,
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

// GET /api/v1/admin/upstream-monitor/trend?days=7|14|30
func (h *UpstreamMonitorHandler) Trend(c *gin.Context) {
	if !h.available(c) {
		return
	}
	days := 7
	if raw := strings.TrimSpace(c.Query("days")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 30 {
			days = parsed
		}
	}
	points, err := h.svc.Trend(c.Request.Context(), days)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"days": days, "points": points})
}

// GET /api/v1/admin/upstream-monitor/alerts?days=7&status=open|acked&kind=&page=1
func (h *UpstreamMonitorHandler) Alerts(c *gin.Context) {
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
	items, total, err := h.svc.Alerts(c.Request.Context(), service.UpstreamAlertFilter{
		AccountID: accountID,
		Kind:      c.Query("kind"),
		Acked:     c.DefaultQuery("status", "all"),
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

// POST /api/v1/admin/upstream-monitor/alerts/:id/ack
func (h *UpstreamMonitorHandler) AckAlert(c *gin.Context) {
	if !h.available(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid alert id")
		return
	}
	if err := h.svc.AckAlert(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"id": id, "acked": true})
}

// GET /api/v1/admin/upstream-monitor/alert-rules
func (h *UpstreamMonitorHandler) ListAlertRules(c *gin.Context) {
	if !h.available(c) {
		return
	}
	rules, err := h.svc.AlertRules(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items := make([]service.UpstreamAlertRule, 0, len(rules))
	for _, rule := range rules {
		items = append(items, rule)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].AccountID < items[j].AccountID })
	response.Success(c, gin.H{"items": items})
}

type upstreamAlertRuleRequest struct {
	AccountID  int64   `json:"account_id"`
	Threshold  float64 `json:"threshold"`
	Unit       string  `json:"unit"`
	RateMinPct float64 `json:"rate_min_pct"`
	Enabled    bool    `json:"enabled"`
}

// PUT /api/v1/admin/upstream-monitor/alert-rules
func (h *UpstreamMonitorHandler) SaveAlertRule(c *gin.Context) {
	if !h.available(c) {
		return
	}
	var req upstreamAlertRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid alert rule payload")
		return
	}
	if req.AccountID <= 0 {
		response.BadRequest(c, "invalid account id")
		return
	}
	rule, err := h.svc.SaveAlertRule(c.Request.Context(), service.UpstreamAlertRule{
		AccountID:  req.AccountID,
		Threshold:  req.Threshold,
		Unit:       req.Unit,
		RateMinPct: req.RateMinPct,
		Enabled:    req.Enabled,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, rule)
}

// DELETE /api/v1/admin/upstream-monitor/alert-rules/:id
func (h *UpstreamMonitorHandler) DeleteAlertRule(c *gin.Context) {
	if !h.available(c) {
		return
	}
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "invalid account id")
		return
	}
	if err := h.svc.DeleteAlertRule(c.Request.Context(), accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"account_id": accountID, "deleted": true})
}

// GET /api/v1/admin/upstream-monitor/export
// Streams the filtered list as CSV so operations can reconcile upstream
// balances offline. It reads the same bounded query as the list endpoint.
func (h *UpstreamMonitorHandler) Export(c *gin.Context) {
	if !h.available(c) {
		return
	}
	filter := service.UpstreamMonitorFilter{
		Platform:      c.Query("platform"),
		AccountType:   c.Query("type"),
		AccountStatus: c.Query("status"),
		Search:        c.Query("search"),
		BalanceStatus: c.DefaultQuery("balance_status", "all"),
		Sort:          c.DefaultQuery("sort", "name"),
	}
	rows, err := h.svc.ExportAccounts(c.Request.Context(), filter, 5000)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="upstream-monitor.csv"`)
	c.Status(http.StatusOK)
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	writer := csv.NewWriter(c.Writer)
	_ = writer.Write([]string{
		"account_id", "name", "notes", "platform", "type", "status", "balance", "unit", "unlimited",
		"balance_status", "balance_read_at", "rate", "rate_synced", "rate_sync_enabled",
		"alert_threshold", "alert_enabled", "last_sampled_at", "balance_error",
	})
	for _, item := range rows {
		balance := ""
		if item.Unlimited {
			balance = "unlimited"
		} else if item.Balance != nil {
			balance = strconv.FormatFloat(*item.Balance, 'f', -1, 64)
		}
		rate := ""
		if item.Rate != nil {
			rate = strconv.FormatFloat(*item.Rate, 'f', 4, 64)
		}
		synced := ""
		if item.RateSynced != nil {
			synced = strconv.FormatFloat(*item.RateSynced, 'f', 4, 64)
		}
		threshold := ""
		if item.AlertThreshold != nil {
			threshold = strconv.FormatFloat(*item.AlertThreshold, 'f', -1, 64)
		}
		readAt := ""
		if item.BalanceReadAt != nil {
			readAt = item.BalanceReadAt.Format(time.RFC3339)
		}
		sampledAt := ""
		if item.LastSampledAt != nil {
			sampledAt = item.LastSampledAt.Format(time.RFC3339)
		}
		_ = writer.Write([]string{
			strconv.FormatInt(item.AccountID, 10), csvSafe(item.Name), csvSafe(item.Notes), csvSafe(item.Platform), csvSafe(item.Type), csvSafe(item.Status),
			balance, csvSafe(item.Unit), strconv.FormatBool(item.Unlimited),
			csvSafe(item.BalanceStatus), readAt, rate, synced, strconv.FormatBool(item.RateSyncEnabled),
			threshold, strconv.FormatBool(item.AlertEnabled), sampledAt, csvSafe(item.BalanceError),
		})
	}
	writer.Flush()
}

// csvSafe neutralizes spreadsheet formula injection without changing the value
// for anything that is not a comma, quote or formula trigger.
func csvSafe(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	switch trimmed[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	}
	return value
}
