package admin

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type RequestCaptureHandler struct {
	Manager      *requestcapture.Manager
	Settings     *service.SettingService
	adminService service.AdminService
	totp         *service.TotpService
	users        *service.UserService
}

func (h *RequestCaptureHandler) SetStepUpDependencies(totp *service.TotpService, users *service.UserService) {
	h.totp, h.users = totp, users
}

// Body text can contain confidential prompts and tool results even after
// structured credential redaction. Metadata browsing remains available without
// repeatedly challenging the operator, but payload reads always require 2FA.
func (h *RequestCaptureHandler) requireSensitiveRead(c *gin.Context) bool {
	c.Header("Cache-Control", "no-store")
	if !middleware.IsPrimaryAdmin(c) {
		response.Forbidden(c, "Only the primary administrator may read captured content")
		return false
	}
	if c.GetString("auth_method") == service.AuditAuthMethodAdminAPIKey {
		return middleware.EnforceStepUpAlways(c, h.totp, h.users)
	}
	if h.totp == nil || h.users == nil {
		response.Error(c, http.StatusServiceUnavailable, "Step-up verification service unavailable")
		return false
	}
	return middleware.EnforceStepUpAlways(c, h.totp, h.users)
}

// This JSON preflight lets streamed downloads prompt for 2FA before opening the
// stream. Content and Export independently repeat the gate; this is no bypass.
func (h *RequestCaptureHandler) AuthorizeSensitiveRead(c *gin.Context) {
	if h.requireSensitiveRead(c) {
		response.Success(c, gin.H{"authorized": true})
	}
}

func (h *AccountHandler) CaptureHandler(manager *requestcapture.Manager, settings *service.SettingService) *RequestCaptureHandler {
	return &RequestCaptureHandler{Manager: manager, Settings: settings, adminService: h.adminService}
}

func (h *RequestCaptureHandler) GetConfig(c *gin.Context) {
	v, err := h.Settings.GetCaptureSettings(c.Request.Context())
	if err != nil {
		response.Error(c, 503, "Capture settings unavailable")
		return
	}
	response.Success(c, v)
}

func (h *RequestCaptureHandler) UpdateConfig(c *gin.Context) {
	var v service.CaptureSettings
	if err := c.ShouldBindJSON(&v); err != nil {
		response.BadRequest(c, "Invalid capture settings")
		return
	}
	if v.Enabled && h.Manager == nil {
		response.Error(c, 503, "Capture storage unavailable on this instance")
		return
	}
	if err := h.Settings.UpdateCaptureSettings(c.Request.Context(), v); err != nil {
		response.BadRequest(c, "Unable to save capture settings")
		return
	}
	response.Success(c, v)
}

func (h *RequestCaptureHandler) Gate(c *gin.Context) {
	if h.Manager == nil || !h.Manager.Config().Enabled {
		response.Error(c, http.StatusNotFound, "Request capture is disabled")
		c.Abort()
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Next()
}
func captureError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows) || errors.Is(err, requestcapture.ErrNotFound):
		response.NotFound(c, "Capture not found on this instance")
	case errors.Is(err, requestcapture.ErrCapacity), errors.Is(err, requestcapture.ErrFinalizing):
		response.Error(c, http.StatusConflict, err.Error())
	case errors.Is(err, requestcapture.ErrDisabled):
		response.Error(c, http.StatusNotFound, err.Error())
	default:
		response.Error(c, http.StatusInternalServerError, "Request capture operation failed")
	}
}
func capturePage(c *gin.Context) (int, int) {
	page, size := response.ParsePagination(c)
	if size > 100 {
		size = 100
	}
	return size, (page - 1) * size
}
func (h *RequestCaptureHandler) List(c *gin.Context) {
	size, offset := capturePage(c)
	tasks, err := h.Manager.Tasks(c.Request.Context(), size, offset)
	if err != nil {
		captureError(c, err)
		return
	}
	response.Success(c, gin.H{"items": tasks, "stats": h.Manager.Stats(), "has_more": len(tasks) == size})
}
func (h *RequestCaptureHandler) Create(c *gin.Context) {
	var req requestcapture.CreateTask
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid capture task")
		return
	}
	if req.TargetID <= 0 || req.DurationMinutes < 1 || req.DurationMinutes > 1440 {
		response.BadRequest(c, "Target ID must be positive; duration must be 1-1440 minutes")
		return
	}
	var name string
	ctx := c.Request.Context()
	switch req.TargetType {
	case "user":
		v, err := h.adminService.GetUser(ctx, req.TargetID)
		if err != nil {
			response.NotFound(c, "User not found")
			return
		}
		name = v.Email
	case "account":
		v, err := h.adminService.GetAccount(ctx, req.TargetID)
		if err != nil {
			response.NotFound(c, "Account not found")
			return
		}
		name = v.Name
	case "group":
		v, err := h.adminService.GetGroup(ctx, req.TargetID)
		if err != nil {
			response.NotFound(c, "Group not found")
			return
		}
		name = v.Name
	default:
		response.BadRequest(c, "Target type must be user, account or group")
		return
	}
	t, err := h.Manager.Create(ctx, req, name)
	if err != nil {
		captureError(c, err)
		return
	}
	response.Success(c, t)
}
func (h *RequestCaptureHandler) Stop(c *gin.Context) {
	if err := h.Manager.Stop(c.Request.Context(), c.Param("task")); err != nil {
		captureError(c, err)
		return
	}
	response.Success(c, gin.H{"stopped": true})
}
func (h *RequestCaptureHandler) Delete(c *gin.Context) {
	if err := h.Manager.Delete(c.Request.Context(), c.Param("task")); err != nil {
		captureError(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}
func (h *RequestCaptureHandler) Records(c *gin.Context) {
	size, offset := capturePage(c)
	records, err := h.Manager.Records(c.Request.Context(), c.Param("task"), c.Query("request_id"), c.Query("errors_only") == "true", size, offset)
	if err != nil {
		captureError(c, err)
		return
	}
	response.Success(c, gin.H{"items": records, "has_more": len(records) == size})
}
func (h *RequestCaptureHandler) Detail(c *gin.Context) {
	r, err := h.Manager.Record(c.Request.Context(), c.Param("task"), c.Param("record"))
	if err != nil {
		captureError(c, err)
		return
	}
	response.Success(c, r)
}
func (h *RequestCaptureHandler) Content(c *gin.Context) {
	if !h.requireSensitiveRead(c) {
		return
	}
	offset, err := strconv.ParseInt(c.DefaultQuery("offset", "0"), 10, 64)
	if err != nil || offset < 0 {
		response.BadRequest(c, "Invalid content offset")
		return
	}
	text, next, more, err := h.Manager.Preview(c.Request.Context(), c.Param("task"), c.Param("record"), c.Param("part"), offset)
	if err != nil {
		captureError(c, err)
		return
	}
	response.Success(c, gin.H{"text": text, "next_offset": next, "has_more": more})
}
func (h *RequestCaptureHandler) Export(c *gin.Context) {
	if !h.requireSensitiveRead(c) {
		return
	}
	task, err := h.Manager.Task(c.Request.Context(), c.Param("task"))
	if err != nil {
		captureError(c, err)
		return
	}
	if task.Status == "running" {
		response.Error(c, 409, "Stop the capture task before exporting")
		return
	}
	c.Header("Content-Type", "application/gzip")
	c.Header("Content-Disposition", "attachment; filename=request-capture-"+task.ID+".tar.gz")
	if err = h.Manager.Export(c.Request.Context(), c.Writer, task.ID, c.Param("record")); err != nil {
		if !c.Writer.Written() {
			c.Header("Content-Disposition", "")
			captureError(c, err)
		} else {
			// Terminate a failed stream without invoking recovery logging of request headers.
			controller := http.NewResponseController(c.Writer)
			_ = controller.SetWriteDeadline(time.Now().Add(-time.Second))
			_ = controller.Flush()
			c.Abort()
		}
	}
}
