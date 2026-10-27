package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
)

type CredentialGuardHandler struct {
	svc   *service.CredentialGuardService
	totp  *service.TotpService
	users *service.UserService
}

func NewCredentialGuardHandler(svc *service.CredentialGuardService) *CredentialGuardHandler {
	return &CredentialGuardHandler{svc: svc}
}

func (h *CredentialGuardHandler) Stop() {
	if h != nil && h.svc != nil {
		h.svc.Stop()
	}
}
func (h *CredentialGuardHandler) SetStepUpDependencies(totp *service.TotpService, users *service.UserService) {
	h.totp, h.users = totp, users
}
func (h *CredentialGuardHandler) guard(c *gin.Context) bool {
	if !middleware.IsPrimaryAdmin(c) {
		response.Forbidden(c, "Primary administrator access required")
		return false
	}
	return true
}
func (h *CredentialGuardHandler) List(c *gin.Context) {
	if !h.guard(c) {
		return
	}
	rows, err := h.svc.List(c.Request.Context())
	if err != nil {
		response.Error(c, 503, "Credential inspection records unavailable")
		return
	}
	response.Success(c, gin.H{"items": rows})
}
func (h *CredentialGuardHandler) Save(c *gin.Context) {
	if !h.guard(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account id")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	var req service.CredentialGuardInput
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "Invalid credential inspection settings")
		return
	}
	previous, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		response.Error(c, 503, "Credential settings unavailable")
		return
	}
	sensitive := req.Password != "" || req.TOTPSecret != "" || req.APIToken != "" || req.ClearSecrets || req.ResetFailures || (req.AutoRelogin && (previous == nil || !previous.Config.AutoRelogin))
	if previous != nil && previous.SecretsConfigured && (previous.Config.ReloginEndpoint != req.ReloginEndpoint || previous.Config.LoginEmail != req.LoginEmail) {
		sensitive = true
	}
	if sensitive && !middleware.EnforceStepUpAlways(c, h.totp, h.users) {
		return
	}
	row, err := h.svc.Save(c.Request.Context(), id, req)
	if err != nil {
		response.Error(c, 409, err.Error())
		return
	}
	middleware.SetAuditAction(c, "admin.credential_guard.settings.update")
	response.Success(c, row)
}
func (h *CredentialGuardHandler) Probe(c *gin.Context)   { h.run(c, false) }
func (h *CredentialGuardHandler) Relogin(c *gin.Context) { h.run(c, true) }
func (h *CredentialGuardHandler) run(c *gin.Context, relogin bool) {
	if !h.guard(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account id")
		return
	}
	if relogin && !middleware.EnforceStepUpAlways(c, h.totp, h.users) {
		return
	}
	row, err := h.svc.Run(c.Request.Context(), id, relogin)
	if err != nil {
		response.Error(c, 409, err.Error())
		return
	}
	middleware.SetAuditAction(c, "admin.credential_guard.inspect")
	response.Success(c, row)
}
