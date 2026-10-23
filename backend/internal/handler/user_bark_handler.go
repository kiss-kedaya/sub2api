package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/notify"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

// UserBarkHandler exposes the user's Bark push settings. The device key is
// write-only from the client's perspective: reads return only whether a key
// is configured, never the key itself.
type UserBarkHandler struct {
	service *notify.UserBarkService
}

func NewUserBarkHandler(service *notify.UserBarkService) *UserBarkHandler {
	return &UserBarkHandler{service: service}
}

// GetBarkSettings returns the configuration status.
// GET /api/v1/user/bark
func (h *UserBarkHandler) GetBarkSettings(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	configured, enabled, err := h.service.Status(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"configured": configured,
		"enabled":    enabled,
	}})
}

// UpdateBarkSettings stores or clears the device key.
// PUT /api/v1/user/bark
func (h *UserBarkHandler) UpdateBarkSettings(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var req struct {
		DeviceKey string `json:"device_key"`
		Enabled   bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload")
		return
	}
	if err := h.service.SetDeviceKey(c.Request.Context(), subject.UserID, req.DeviceKey, req.Enabled); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"saved": true}})
}
