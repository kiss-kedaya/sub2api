package admin

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AnnouncementHandler handles admin announcement management
type AnnouncementHandler struct {
	announcementService *service.AnnouncementService

	// 公告发布属用户可见内容的对外发布动作：发布（active）时要求 step-up 2FA，
	// 保存草稿/归档不打断日常运营。依赖由 wire 在装配后注入。
	totpService    *service.TotpService
	userService    *service.UserService
	settingService *service.SettingService
}

// NewAnnouncementHandler creates a new admin announcement handler
func NewAnnouncementHandler(announcementService *service.AnnouncementService) *AnnouncementHandler {
	return &AnnouncementHandler{
		announcementService: announcementService,
	}
}

// SetStepUpDependencies 注入 step-up 2FA 门控依赖。
func (h *AnnouncementHandler) SetStepUpDependencies(
	totpService *service.TotpService,
	userService *service.UserService,
	settingService *service.SettingService,
) {
	h.totpService = totpService
	h.userService = userService
	h.settingService = settingService
}

// enforcePublishStepUp 对"发布公告"执行 step-up 2FA 门控。
// 未启用 step_up_enabled 时放行（与路由级门控语义一致）；机器凭据一律 403。
// 依赖缺失时 fail-closed 返回 503，避免装配遗漏把门控静默关掉。
func (h *AnnouncementHandler) enforcePublishStepUp(c *gin.Context) bool {
	if h == nil || h.totpService == nil || h.userService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Step-up verification service unavailable")
		return false
	}
	return middleware2.EnforceStepUp(c, h.totpService, h.userService, h.settingService)
}

// announcementStatusNeedsPublishStepUp 判断目标状态是否会把公告发布给用户。
// 只有 active 会立即对用户可见；draft/archived 属日常草稿与下架操作，不要求 2FA。
func announcementStatusNeedsPublishStepUp(status string) bool {
	return strings.TrimSpace(status) == service.AnnouncementStatusActive
}

type CreateAnnouncementRequest struct {
	Title      string                        `json:"title" binding:"required"`
	Content    string                        `json:"content" binding:"required"`
	Status     string                        `json:"status" binding:"omitempty,oneof=draft active archived"`
	NotifyMode string                        `json:"notify_mode" binding:"omitempty,oneof=silent popup"`
	Targeting  service.AnnouncementTargeting `json:"targeting"`
	StartsAt   *int64                        `json:"starts_at"` // Unix seconds, 0/empty = immediate
	EndsAt     *int64                        `json:"ends_at"`   // Unix seconds, 0/empty = never
}

type UpdateAnnouncementRequest struct {
	Title      *string                        `json:"title"`
	Content    *string                        `json:"content"`
	Status     *string                        `json:"status" binding:"omitempty,oneof=draft active archived"`
	NotifyMode *string                        `json:"notify_mode" binding:"omitempty,oneof=silent popup"`
	Targeting  *service.AnnouncementTargeting `json:"targeting"`
	StartsAt   *int64                         `json:"starts_at"` // Unix seconds, 0 = clear
	EndsAt     *int64                         `json:"ends_at"`   // Unix seconds, 0 = clear
}

// List handles listing announcements with filters
// GET /api/v1/admin/announcements
func (h *AnnouncementHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	status := strings.TrimSpace(c.Query("status"))
	search := strings.TrimSpace(c.Query("search"))
	sortBy := c.DefaultQuery("sort_by", "created_at")
	sortOrder := c.DefaultQuery("sort_order", "desc")
	if len(search) > 200 {
		search = search[:200]
	}

	params := pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    sortBy,
		SortOrder: sortOrder,
	}

	items, paginationResult, err := h.announcementService.List(
		c.Request.Context(),
		params,
		service.AnnouncementListFilters{Status: status, Search: search},
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]dto.Announcement, 0, len(items))
	for i := range items {
		out = append(out, *dto.AnnouncementFromService(&items[i]))
	}
	response.Paginated(c, out, paginationResult.Total, page, pageSize)
}

// GetByID handles getting an announcement by ID
// GET /api/v1/admin/announcements/:id
func (h *AnnouncementHandler) GetByID(c *gin.Context) {
	announcementID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || announcementID <= 0 {
		response.BadRequest(c, "Invalid announcement ID")
		return
	}

	item, err := h.announcementService.GetByID(c.Request.Context(), announcementID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.AnnouncementFromService(item))
}

// Create handles creating a new announcement
// POST /api/v1/admin/announcements
func (h *AnnouncementHandler) Create(c *gin.Context) {
	var req CreateAnnouncementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	// 直接创建为 active 等于对外发布公告（用户可见内容，可用于钓鱼/欺诈文案）：
	// 要求 step-up 2FA；status 缺省为 draft，保存草稿不打断。
	if announcementStatusNeedsPublishStepUp(req.Status) {
		if !h.enforcePublishStepUp(c) {
			return
		}
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}

	input := &service.CreateAnnouncementInput{
		Title:      req.Title,
		Content:    req.Content,
		Status:     req.Status,
		NotifyMode: req.NotifyMode,
		Targeting:  req.Targeting,
		ActorID:    &subject.UserID,
	}

	if req.StartsAt != nil && *req.StartsAt > 0 {
		t := time.Unix(*req.StartsAt, 0)
		input.StartsAt = &t
	}
	if req.EndsAt != nil && *req.EndsAt > 0 {
		t := time.Unix(*req.EndsAt, 0)
		input.EndsAt = &t
	}

	created, err := h.announcementService.Create(c.Request.Context(), input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.AnnouncementFromService(created))
}

// Update handles updating an announcement
// PUT /api/v1/admin/announcements/:id
func (h *AnnouncementHandler) Update(c *gin.Context) {
	announcementID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || announcementID <= 0 {
		response.BadRequest(c, "Invalid announcement ID")
		return
	}

	var req UpdateAnnouncementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	// 判定本次更新是否面向"已发布/将发布"的公告：显式把状态改为 active，
	// 或未改状态但当前公告已是 active（改正文/定向/弹窗方式等同对在线公告二次发布）。
	// 这两类需要 step-up 2FA；草稿编辑与下架（archived）不打断。
	publishes := false
	if req.Status != nil {
		publishes = announcementStatusNeedsPublishStepUp(*req.Status)
	} else {
		existing, getErr := h.announcementService.GetByID(c.Request.Context(), announcementID)
		if getErr != nil {
			response.ErrorFrom(c, getErr)
			return
		}
		publishes = existing != nil && announcementStatusNeedsPublishStepUp(existing.Status)
	}
	if publishes && !h.enforcePublishStepUp(c) {
		return
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}

	input := &service.UpdateAnnouncementInput{
		Title:      req.Title,
		Content:    req.Content,
		Status:     req.Status,
		NotifyMode: req.NotifyMode,
		Targeting:  req.Targeting,
		ActorID:    &subject.UserID,
	}

	if req.StartsAt != nil {
		if *req.StartsAt == 0 {
			var cleared *time.Time = nil
			input.StartsAt = &cleared
		} else {
			t := time.Unix(*req.StartsAt, 0)
			ptr := &t
			input.StartsAt = &ptr
		}
	}

	if req.EndsAt != nil {
		if *req.EndsAt == 0 {
			var cleared *time.Time = nil
			input.EndsAt = &cleared
		} else {
			t := time.Unix(*req.EndsAt, 0)
			ptr := &t
			input.EndsAt = &ptr
		}
	}

	updated, err := h.announcementService.Update(c.Request.Context(), announcementID, input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.AnnouncementFromService(updated))
}

// Delete handles deleting an announcement
// DELETE /api/v1/admin/announcements/:id
func (h *AnnouncementHandler) Delete(c *gin.Context) {
	announcementID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || announcementID <= 0 {
		response.BadRequest(c, "Invalid announcement ID")
		return
	}

	if err := h.announcementService.Delete(c.Request.Context(), announcementID); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "Announcement deleted successfully"})
}

// ListReadStatus handles listing users read status for an announcement
// GET /api/v1/admin/announcements/:id/read-status
func (h *AnnouncementHandler) ListReadStatus(c *gin.Context) {
	announcementID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || announcementID <= 0 {
		response.BadRequest(c, "Invalid announcement ID")
		return
	}

	page, pageSize := response.ParsePagination(c)
	params := pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    c.DefaultQuery("sort_by", "email"),
		SortOrder: c.DefaultQuery("sort_order", "asc"),
	}
	search := strings.TrimSpace(c.Query("search"))
	if len(search) > 200 {
		search = search[:200]
	}

	items, paginationResult, err := h.announcementService.ListUserReadStatus(
		c.Request.Context(),
		announcementID,
		params,
		search,
	)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Paginated(c, adminAnnouncementReadResponse(c, items), paginationResult.Total, page, pageSize)
}
