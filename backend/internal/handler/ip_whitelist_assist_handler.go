package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// ipWhitelistAssistService is the subset of
// service.IPWhitelistAssistService used by the handler (replaceable in tests).
type ipWhitelistAssistService interface {
	Get(ctx context.Context, userID int64, currentIP string) (service.IPWhitelistAssistResult, error)
}

// IPWhitelistAssistHandler serves the "fill in my current IP" helper used by
// both the single-key and bulk-edit dialogs.
type IPWhitelistAssistHandler struct {
	service ipWhitelistAssistService
}

// NewIPWhitelistAssistHandler creates the handler.
func NewIPWhitelistAssistHandler(svc *service.IPWhitelistAssistService) *IPWhitelistAssistHandler {
	return &IPWhitelistAssistHandler{service: svc}
}

// Get returns the caller's trusted client IP, its location and the addresses
// the caller recently connected from.
func (h *IPWhitelistAssistHandler) Get(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if h == nil || h.service == nil {
		response.Error(c, http.StatusServiceUnavailable, "IP whitelist assist is not configured")
		return
	}
	// Same resolution as the API key IP ACL middleware (session binding is set
	// by the global middleware, honoring api_key_acl_trust_forwarded_ip and
	// server.trusted_proxies), so what the user confirms is what will be checked.
	currentIP := middleware2.SecurityClientIP(c)
	result, err := h.service.Get(c.Request.Context(), subject.UserID, currentIP)
	if err != nil {
		if errors.Is(err, service.ErrIPWhitelistAssistRateLimited) {
			response.Error(c, http.StatusTooManyRequests, "Too many requests, please retry shortly")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
