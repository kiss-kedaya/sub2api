// Handler shapes adapted from ranxi2001/sub2api @ 9620d315, LGPL-3.0-or-later.
package securityaudit

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func ProvidePenaltyAdminHandler(prompt PromptAdminService, penalties *service.SecurityPenaltyService) *PromptAdminHandler {
	h := NewPromptAdminHandler(prompt)
	h.penalties = penalties
	return h
}

func (h *PromptAdminHandler) StopSecurityPenalties() {
	if h != nil && h.penalties != nil {
		h.penalties.Stop()
	}
}

func (h *PromptAdminHandler) penaltyAdmin(c *gin.Context) bool {
	c.Header("Cache-Control", "no-store")
	if !middleware.IsPrimaryAdmin(c) {
		response.Forbidden(c, "Primary administrator access required")
		return false
	}
	if h == nil || h.penalties == nil {
		response.Error(c, 503, "Security records unavailable")
		return false
	}
	return true
}

func (h *PromptAdminHandler) GetPenaltyConfig(c *gin.Context) {
	if !h.penaltyAdmin(c) {
		return
	}
	v, err := h.penalties.GetConfig(c.Request.Context())
	if !response.ErrorFrom(c, err) {
		response.Success(c, v)
	}
}
func (h *PromptAdminHandler) SavePenaltyConfig(c *gin.Context) {
	if !h.penaltyAdmin(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var cfg service.SecurityPenaltyConfig
	if c.ShouldBindJSON(&cfg) != nil {
		response.BadRequest(c, "Invalid security penalty configuration")
		return
	}
	v, err := h.penalties.SaveConfig(c.Request.Context(), cfg)
	if !response.ErrorFrom(c, err) {
		middleware.SetAuditAction(c, "admin.security_penalty.config.update")
		response.Success(c, v)
	}
}
func (h *PromptAdminHandler) ReleasePenalty(c *gin.Context) {
	if !h.penaltyAdmin(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var req struct {
		Reason string `json:"reason"`
	}
	if err != nil || id <= 0 || c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid release request")
		return
	}
	if err = h.penalties.Release(c.Request.Context(), id, adminID(c), req.Reason); !response.ErrorFrom(c, err) {
		middleware.SetAuditAction(c, "admin.security_penalty.release")
		response.Success(c, gin.H{"released": true})
	}
}
func (h *PromptAdminHandler) penaltyOwner(c *gin.Context) (int64, bool) {
	c.Header("Cache-Control", "no-store")
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Authorization required")
		return 0, false
	}
	if h == nil || h.penalties == nil {
		response.Error(c, 503, "Security records unavailable")
		return 0, false
	}
	return subject.UserID, true
}
func (h *PromptAdminHandler) MyPenaltySummary(c *gin.Context) {
	id, ok := h.penaltyOwner(c)
	if !ok {
		return
	}
	v, err := h.penalties.Summary(c.Request.Context(), id)
	if !response.ErrorFrom(c, err) {
		response.Success(c, v)
	}
}
func (h *PromptAdminHandler) MyPenaltyEvents(c *gin.Context) {
	id, ok := h.penaltyOwner(c)
	if !ok {
		return
	}
	h.listPenaltyEvents(c, id)
}
func (h *PromptAdminHandler) AdminPenaltyEvents(c *gin.Context) {
	if !h.penaltyAdmin(c) {
		return
	}
	id := int64(0)
	if raw := strings.TrimSpace(c.Query("user_id")); raw != "" {
		var err error
		id, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid user ID")
			return
		}
	}
	h.listPenaltyEvents(c, id)
}
func (h *PromptAdminHandler) listPenaltyEvents(c *gin.Context, id int64) {
	page, size := response.ParsePagination(c)
	v, err := h.penalties.ListEvents(c.Request.Context(), id, page, size)
	if !response.ErrorFrom(c, err) {
		response.Success(c, v)
	}
}
