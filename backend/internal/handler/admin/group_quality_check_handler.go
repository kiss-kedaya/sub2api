package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// GroupQualityCheckHandler handles per-group degradation (降智) detection config
// and results for the admin channel monitor page.
type GroupQualityCheckHandler struct {
	gqcService *service.GroupQualityCheckService
}

// NewGroupQualityCheckHandler creates a new GroupQualityCheckHandler.
func NewGroupQualityCheckHandler(gqcService *service.GroupQualityCheckService) *GroupQualityCheckHandler {
	return &GroupQualityCheckHandler{gqcService: gqcService}
}

type setGroupQualityCheckRequest struct {
	Enabled bool `json:"enabled"`
}

// SetEnabled PUT /admin/quality-check/groups/:id
func (h *GroupQualityCheckHandler) SetEnabled(c *gin.Context) {
	groupID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid group id")
		return
	}

	var req setGroupQualityCheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	settings, err := h.gqcService.SetGroupEnabled(c.Request.Context(), groupID, req.Enabled)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, settings)
}

// List GET /admin/quality-check/groups
func (h *GroupQualityCheckHandler) List(c *gin.Context) {
	statuses, err := h.gqcService.ListGroupStatuses(c.Request.Context())
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, statuses)
}

// Get GET /admin/quality-check/groups/:id
func (h *GroupQualityCheckHandler) Get(c *gin.Context) {
	groupID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid group id")
		return
	}

	status, err := h.gqcService.GetGroupStatus(c.Request.Context(), groupID)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, status)
}

// ListResults GET /admin/quality-check/groups/:id/results
func (h *GroupQualityCheckHandler) ListResults(c *gin.Context) {
	groupID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid group id")
		return
	}

	limit := 50
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 200 {
		limit = l
	}

	results, err := h.gqcService.ListRecentResults(c.Request.Context(), groupID, limit)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, results)
}
