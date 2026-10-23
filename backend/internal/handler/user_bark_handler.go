package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/notify"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// UserBarkHandler exposes the user's Bark push settings. The device key is
// write-only from the client's perspective: reads return only whether a key
// is configured, never the key itself.
type UserBarkHandler struct {
	service  *notify.UserBarkService
	verifier interface {
		VerifyFreshCode(context.Context, int64, string) error
	}
	config *config.Config
}

func NewUserBarkHandler(barkService *notify.UserBarkService, totpService *service.TotpService, cfg *config.Config) *UserBarkHandler {
	return &UserBarkHandler{service: barkService, verifier: totpService, config: cfg}
}

// GetBarkSettings returns the configuration status.
// GET /api/v1/user/bark
func (h *UserBarkHandler) GetBarkSettings(c *gin.Context) {
	cryptoNoStore(c)
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
	cryptoNoStore(c)
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	var req struct {
		DeviceKey *string `json:"device_key"`
		Enabled   *bool   `json:"enabled"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF || req.Enabled == nil {
		response.BadRequest(c, "Invalid request payload")
		return
	}
	if !service.CryptoAdminAllowHTTP() && !cryptoSecureTransport(c.Request, h.config) {
		response.ErrorWithDetails(c, http.StatusForbidden, "HTTPS is required to change notification settings", "BARK_HTTPS_REQUIRED", nil)
		return
	}
	codes := c.Request.Header.Values("X-Bark-TOTP")
	code := ""
	if len(codes) == 1 {
		code = codes[0]
	}
	if h.verifier == nil {
		response.ErrorFrom(c, service.ErrTotpUnavailable)
		return
	}
	if err := h.verifier.VerifyFreshCode(c.Request.Context(), subject.UserID, code); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	err := h.service.UpdateSettings(c.Request.Context(), subject.UserID, req.DeviceKey, *req.Enabled)
	if err != nil {
		if errors.Is(err, notify.ErrBarkKeyRequired) || errors.Is(err, notify.ErrBarkInvalidKey) {
			response.BadRequest(c, err.Error())
			return
		}
		if errors.Is(err, notify.ErrBarkOriginalNotificationFailed) {
			response.ErrorWithDetails(c, http.StatusServiceUnavailable, "原 Bark 设备通知失败，配置未修改，请恢复原通知通道后重试", "BARK_ORIGINAL_NOTIFICATION_FAILED", nil)
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"saved": true}})
}
