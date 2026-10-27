package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
)

type ExcelGrantHandler struct {
	svc   *service.ExcelGrantService
	totp  *service.TotpService
	users *service.UserService
}

func NewExcelGrantHandler(svc *service.ExcelGrantService) *ExcelGrantHandler {
	return &ExcelGrantHandler{svc: svc}
}

func (h *ExcelGrantHandler) Stop() {
	if h != nil && h.svc != nil {
		h.svc.Stop()
	}
}
func (h *ExcelGrantHandler) SetStepUpDependencies(totp *service.TotpService, users *service.UserService) {
	h.totp, h.users = totp, users
}
func (h *ExcelGrantHandler) guard(c *gin.Context) bool {
	if !middleware.IsPrimaryAdmin(c) {
		response.Forbidden(c, "Primary administrator access required")
		return false
	}
	return true
}
func (h *ExcelGrantHandler) id(c *gin.Context) (int64, bool) {
	if !h.guard(c) {
		return 0, false
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account id")
		return 0, false
	}
	return id, true
}
func (h *ExcelGrantHandler) List(c *gin.Context) {
	if !h.guard(c) {
		return
	}
	rows, err := h.svc.List(c.Request.Context())
	if err != nil {
		response.Error(c, 503, "Excel grants unavailable")
		return
	}
	response.Success(c, gin.H{"items": rows})
}
func (h *ExcelGrantHandler) Save(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	var req struct {
		service.ExcelGrantConfig
		Revision string `json:"revision"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid BPS settings")
		return
	}
	row, err := h.svc.SaveConfig(c.Request.Context(), id, req.ExcelGrantConfig, req.Revision)
	if err != nil {
		response.Error(c, 409, err.Error())
		return
	}
	middleware.SetAuditAction(c, "admin.excel_bps.config.update")
	response.Success(c, row)
}
func (h *ExcelGrantHandler) Authorize(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	if !middleware.EnforceStepUpAlways(c, h.totp, h.users) {
		return
	}
	result, err := h.svc.BeginAuthorization(c.Request.Context(), id)
	if err != nil {
		response.Error(c, 409, err.Error())
		return
	}
	c.Header("Cache-Control", "no-store")
	middleware.SetAuditAction(c, "admin.excel_bps.authorize.start")
	response.Success(c, result)
}
func (h *ExcelGrantHandler) Callback(c *gin.Context) {
	id, ok := h.id(c)
	if !ok {
		return
	}
	if !middleware.EnforceStepUpAlways(c, h.totp, h.users) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var req struct {
		SessionID   string `json:"session_id"`
		CallbackURL string `json:"callback_url"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid OAuth callback")
		return
	}
	row, err := h.svc.CompleteAuthorization(c.Request.Context(), id, req.SessionID, req.CallbackURL)
	if err != nil {
		response.Error(c, 409, err.Error())
		return
	}
	middleware.SetAuditAction(c, "admin.excel_bps.authorize.complete")
	response.Success(c, row)
}
