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
	service *notify.UserBarkService
	// verifier is kept wired so callers/tests do not churn, but updates no
	// longer consult it: see UpdateBarkSettings for the layered protection that
	// replaced the per-save TOTP prompt.
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
	// No TOTP step-up here on purpose. The Bark device key only decides where
	// THIS user's own push notifications go; it grants no balance, credential or
	// admin capability. Forcing a fresh code made the settings page unusable for
	// ordinary users, so protection is layered differently:
	//   - the key is encrypted at rest and never returned to the client;
	//   - a change is announced on the ORIGINAL device first, and the update is
	//     refused when that original notification cannot be delivered
	//     (ErrBarkOriginalNotificationFailed), so a silent redirect is impossible;
	//   - HTTPS is required to submit it.
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
