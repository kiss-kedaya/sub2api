package admin

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AuditLogHandler 操作审计日志管理接口。
// 审计日志仅管理员可读；不提供单条删除，也不提供任何 HTTP 清空能力：
// 人工清理只能由数据库管理员在数据库层执行（见 Clear 的说明）。
type AuditLogHandler struct {
	auditService *service.AuditLogService
}

// NewAuditLogHandler 创建审计日志处理器。
func NewAuditLogHandler(auditService *service.AuditLogService) *AuditLogHandler {
	return &AuditLogHandler{
		auditService: auditService,
	}
}

// AuditService exposes the audit recorder for wiring that lives outside the
// handler layer: the gateway IP risk control records its own auto-disables
// through it so the action is visible even when the process log level hides
// WARN lines.
func (h *AuditLogHandler) AuditService() *service.AuditLogService {
	if h == nil {
		return nil
	}
	return h.auditService
}

// List 分页查询审计日志。
// GET /api/v1/admin/audit-logs
func (h *AuditLogHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	if pageSize > 200 {
		pageSize = 200
	}

	filter := &service.AuditLogFilter{
		Page:       page,
		PageSize:   pageSize,
		ActorEmail: strings.TrimSpace(c.Query("actor_email")),
		AuthMethod: strings.TrimSpace(c.Query("auth_method")),
		Action:     strings.TrimSpace(c.Query("action")),
		Method:     strings.TrimSpace(c.Query("method")),
		ClientIP:   strings.TrimSpace(c.Query("client_ip")),
		Query:      strings.TrimSpace(c.Query("q")),
	}

	if v := strings.TrimSpace(c.Query("actor_user_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid actor_user_id")
			return
		}
		filter.ActorUserID = &id
	}
	if v := strings.TrimSpace(c.Query("start_time")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			response.BadRequest(c, "Invalid start_time, expect RFC3339")
			return
		}
		filter.StartTime = &t
	}
	if v := strings.TrimSpace(c.Query("end_time")); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			response.BadRequest(c, "Invalid end_time, expect RFC3339")
			return
		}
		filter.EndTime = &t
	}
	if v := strings.TrimSpace(c.Query("success")); v != "" {
		success := v == "true"
		filter.Success = &success
	}

	result, err := h.auditService.List(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, result.Logs, int64(result.Total), result.Page, result.PageSize)
}

// Get 查询单条审计日志详情（含脱敏后的请求体）。
// GET /api/v1/admin/audit-logs/:id
func (h *AuditLogHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid audit log id")
		return
	}
	item, err := h.auditService.GetByID(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

// Clear 已停用：后台管理端不得清理审计日志。
// POST /api/v1/admin/audit-logs/clear
//
// 审计日志是追责链路，只允许追加，并按保留期自动滚动清理；
// 人工清理只能由数据库管理员直接在数据库层执行（例如 TRUNCATE audit_logs）。
// 路由保留并固定返回 403，避免旧前端拿到 404 后误判为接口不存在，
// 同时明确告知唯一合规路径。
// 服务内部的 AuditLogService.ClearAll 仍保留给内部/运维 CLI 使用，
// 但不再经由任何 HTTP 接口暴露。
func (h *AuditLogHandler) Clear(c *gin.Context) {
	response.ErrorWithDetails(c, http.StatusForbidden,
		"Audit logs cannot be cleared from the admin console; only a database administrator may purge them directly in the database",
		"AUDIT_LOG_CLEAR_DISABLED", nil)
}

// GroupRateChanges reconstructs real configuration-level rate multiplier changes
// for one group from the admin audit trail.
//
// GET /api/v1/admin/audit-logs/group-rate-changes?group_id=3&days=30
//
// Response: {"group_id":3,"days":30,"source":"audit_log","items":[
//
//	{"group_id":3,"changed_at":"2026-11-02T03:12:00Z",
//	 "previous_rate_multiplier":0.12,"current_rate_multiplier":0.09,
//	 "direction":"down","actor_email":"admin@example.com","source":"audit_log"}]}
//
// items are newest first. The first recorded value only establishes a baseline.
// This is the only endpoint whose timestamp is the real edit time; the
// user-facing /usage/groups/rate-changes endpoint reports observed request
// multipliers instead.
func (h *AuditLogHandler) GroupRateChanges(c *gin.Context) {
	groupID, err := strconv.ParseInt(strings.TrimSpace(c.Query("group_id")), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "Invalid group_id")
		return
	}
	days := 30
	if raw := strings.TrimSpace(c.Query("days")); raw != "" {
		parsed, convErr := strconv.Atoi(raw)
		if convErr != nil || parsed <= 0 || parsed > 365 {
			response.BadRequest(c, "Invalid days, allowed range is 1-365")
			return
		}
		days = parsed
	}
	if h.auditService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Audit log service is unavailable")
		return
	}
	changes, err := h.auditService.GroupRateHistory(c.Request.Context(), groupID, time.Now().AddDate(0, 0, -days), 200)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"group_id": groupID,
		"days":     days,
		"source":   "audit_log",
		"items":    changes,
	})
}
