package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type OpsAutomationHandler struct{ svc *service.OpsAutomationService }

func NewOpsAutomationHandler(svc *service.OpsAutomationService) *OpsAutomationHandler {
	return &OpsAutomationHandler{svc: svc}
}

func (h *OpsAutomationHandler) Stop() {
	if h != nil && h.svc != nil {
		h.svc.Stop()
	}
}

func (h *OpsAutomationHandler) Engine() *service.OpsAutomationService {
	if h == nil {
		return nil
	}
	return h.svc
}
func (h *OpsAutomationHandler) allow(c *gin.Context) bool {
	if !middleware.IsPrimaryAdmin(c) {
		response.Forbidden(c, "Primary administrator access required")
		return false
	}
	return true
}
func (h *OpsAutomationHandler) Get(c *gin.Context) {
	if !h.allow(c) {
		return
	}
	v, err := h.svc.GetConfig(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Automation settings unavailable")
		return
	}
	response.Success(c, v)
}
func (h *OpsAutomationHandler) Save(c *gin.Context) {
	if !h.allow(c) {
		return
	}
	var v service.OpsAutomationConfig
	if c.ShouldBindJSON(&v) != nil {
		response.BadRequest(c, "Invalid automation settings")
		return
	}
	out, err := h.svc.SaveConfig(c.Request.Context(), v)
	if err != nil {
		if errors.Is(err, service.ErrChannelOperationsConflict) {
			response.ErrorFrom(c, err)
		} else {
			response.BadRequest(c, "Invalid or unavailable automation configuration")
		}
		return
	}
	response.Success(c, out)
}
func (h *OpsAutomationHandler) Plans(c *gin.Context) {
	if !h.allow(c) {
		return
	}
	v, err := h.svc.ListLocalPlans(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Local reasoning plans unavailable")
		return
	}
	response.Success(c, v)
}
func (h *OpsAutomationHandler) SavePlan(c *gin.Context) {
	if !h.allow(c) {
		return
	}
	var v service.LocalReasoningPlan
	if c.ShouldBindJSON(&v) != nil {
		response.BadRequest(c, "Invalid local reasoning plan")
		return
	}
	out, err := h.svc.SaveLocalPlan(c.Request.Context(), v)
	if err != nil {
		if errors.Is(err, service.ErrChannelOperationsConflict) {
			response.ErrorFrom(c, err)
		} else {
			response.BadRequest(c, "Invalid local reasoning plan or restore its owned pause before changing scope")
		}
		return
	}
	response.Success(c, out)
}
func (h *OpsAutomationHandler) RunPlan(c *gin.Context) {
	if !h.allow(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid plan ID")
		return
	}
	if err = h.svc.RunLocalPlan(c.Request.Context(), id); err != nil {
		response.BadRequest(c, "Local reasoning run failed or plan is busy")
		return
	}
	response.Success(c, gin.H{"ok": true})
}
func (h *OpsAutomationHandler) Ownership(c *gin.Context) {
	if !h.allow(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid plan ID")
		return
	}
	var v struct {
		Fence   string `json:"fence"`
		Confirm bool   `json:"confirm"`
		Restore bool   `json:"restore"`
		Delete  bool   `json:"delete"`
	}
	if c.Request.Method != "GET" {
		if c.ShouldBindJSON(&v) != nil || !v.Confirm || (!v.Restore && !v.Delete) {
			response.BadRequest(c, "Preview and confirm ownership first")
			return
		}
	}
	out, err := h.svc.LocalOwnership(c.Request.Context(), id, v.Fence, v.Restore, v.Delete)
	if err != nil {
		if errors.Is(err, service.ErrChannelOperationsConflict) {
			response.ErrorFrom(c, err)
		} else {
			response.BadRequest(c, "Ownership cannot be changed; refresh the preview and inspect manual changes")
		}
		return
	}
	response.Success(c, out)
}
