package handler

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

func (h *UserHandler) SetCFAllowlistService(svc *service.CFAllowlistService) {
	h.cfAllowlist = svc
}

func (h *UserHandler) GetCFAllowlist(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if h.cfAllowlist == nil {
		response.Error(c, http.StatusServiceUnavailable, "Cloudflare allowlist is not configured")
		return
	}
	st, err := h.cfAllowlist.Status(c.Request.Context(), subject.UserID, ip.GetClientIP(c))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, st)
}

type cfAllowlistAddRequest struct {
	IP string `json:"ip"`
}

func (h *UserHandler) AddCFAllowlist(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if h.cfAllowlist == nil {
		response.Error(c, http.StatusServiceUnavailable, "Cloudflare allowlist is not configured")
		return
	}
	var req cfAllowlistAddRequest
	_ = c.ShouldBindJSON(&req)
	raw := req.IP
	if raw == "" {
		raw = ip.GetClientIP(c)
	}
	item, err := h.cfAllowlist.Add(c.Request.Context(), subject.UserID, raw)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, item)
}

func (h *UserHandler) DeleteCFAllowlist(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if h.cfAllowlist == nil {
		response.Error(c, http.StatusServiceUnavailable, "Cloudflare allowlist is not configured")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid id")
		return
	}
	if err := h.cfAllowlist.Delete(c.Request.Context(), subject.UserID, id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"ok": true})
}
