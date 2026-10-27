package admin

import (
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/serverless"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
)

type ServerlessHandler struct {
	manager *serverless.Manager
	totp    *service.TotpService
	users   *service.UserService
}

func (h *SettingHandler) ServerlessHandler(manager *serverless.Manager) *ServerlessHandler {
	v := NewServerlessHandler(manager)
	v.totp, v.users = h.totpService, h.userService
	return v
}
func (h *ServerlessHandler) RequireStepUp(c *gin.Context) {
	if !h.allowed(c) {
		c.Abort()
		return
	}
	if middleware.EnforceStepUpAlways(c, h.totp, h.users) {
		c.Next()
	}
}

func NewServerlessHandler(manager *serverless.Manager) *ServerlessHandler {
	return &ServerlessHandler{manager: manager}
}
func (h *ServerlessHandler) Engine() *serverless.Manager {
	if h == nil {
		return nil
	}
	return h.manager
}
func (h *ServerlessHandler) Stop() {
	if h != nil && h.manager != nil {
		h.manager.Stop()
	}
}
func (h *ServerlessHandler) allowed(c *gin.Context) bool {
	if !middleware.IsPrimaryAdmin(c) {
		response.Forbidden(c, "Primary administrator access required")
		return false
	}
	if h == nil || h.manager == nil {
		response.Error(c, 503, "Regional routing unavailable")
		return false
	}
	return true
}
func (h *ServerlessHandler) Get(c *gin.Context) {
	if !h.allowed(c) {
		return
	}
	cfg, err := h.manager.Config(c.Request.Context())
	if err != nil {
		response.Error(c, 503, "Regional configuration unavailable")
		return
	}
	pods, err := h.manager.Pods(c.Request.Context())
	if err != nil {
		response.Error(c, 503, "Regional registry unavailable")
		return
	}
	stats, err := h.manager.Stats(c.Request.Context())
	if err != nil {
		response.Error(c, 503, "Regional statistics unavailable")
		return
	}
	response.Success(c, gin.H{"config": cfg, "pods": pods, "stats": stats, "runtime": h.manager.RuntimeStatus()})
}
func (h *ServerlessHandler) Save(c *gin.Context) {
	if !h.allowed(c) {
		return
	}
	var cfg serverless.Config
	if c.ShouldBindJSON(&cfg) != nil {
		response.BadRequest(c, "Invalid regional routing configuration")
		return
	}
	if err := h.manager.Save(c.Request.Context(), cfg); err != nil {
		if errors.Is(err, serverless.ErrConfigConflict) {
			response.Error(c, http.StatusConflict, "Configuration changed; refresh and retry")
		} else {
			response.BadRequest(c, "Invalid regional routing configuration or runtime not configured")
		}
		return
	}
	h.Get(c)
}
