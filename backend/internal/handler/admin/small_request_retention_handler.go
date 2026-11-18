package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

// Small retention is shared PostgreSQL storage, not an instance-local capture
// task. It must not inherit the file manager's instance pinning requirement.
func (h *RequestCaptureHandler) SmallGate(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	if !middleware.IsPrimaryAdmin(c) {
		response.Forbidden(c, "Only the primary administrator may manage captures")
		c.Abort()
		return
	}
	r := h.Settings.SmallRequestRecorder()
	if r == nil || r.Store == nil {
		response.Error(c, http.StatusServiceUnavailable, "Small request retention unavailable")
		c.Abort()
		return
	}
	if !r.Ready() {
		response.Error(c, http.StatusServiceUnavailable, "Small request retention tables are not ready")
		c.Abort()
		return
	}
	c.Next()
}
func (h *RequestCaptureHandler) SmallUpdateConfig(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !middleware.IsPrimaryAdmin(c) {
		response.Forbidden(c, "Only the primary administrator may manage captures")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid retention settings")
		return
	}
	r := h.Settings.SmallRequestRecorder()
	if input.Enabled && !r.Ready() {
		response.Error(c, 503, "Small request retention tables are not ready")
		return
	}
	config, err := h.Settings.GetCaptureSettings(c.Request.Context())
	if err != nil {
		response.Error(c, 503, "Capture settings unavailable")
		return
	}
	config.SmallRequestEnabled = input.Enabled
	if err = h.Settings.UpdateCaptureSettings(c.Request.Context(), config); err != nil {
		response.Error(c, 503, "Unable to save retention settings")
		return
	}
	response.Success(c, gin.H{"enabled": input.Enabled, "ready": r.Ready()})
}

func (h *RequestCaptureHandler) SmallStats(c *gin.Context) {
	stats, err := h.Settings.SmallRequestRecorder().Store.Stats(c.Request.Context())
	if err != nil {
		captureError(c, err)
		return
	}
	stats.QuotaFull = h.Settings.SmallRequestRecorder().QuotaFull()
	response.Success(c, stats)
}
func (h *RequestCaptureHandler) SmallList(c *gin.Context) {
	size, offset := capturePage(c)
	f := requestcapture.SmallFilter{Model: c.Query("model"), RequestID: c.Query("request_id"), Limit: size, Offset: offset}
	for _, field := range []struct {
		name   string
		target *int64
	}{{"user_id", &f.UserID}, {"api_key_id", &f.APIKeyID}, {"min_repeats", &f.MinRepeats}} {
		if value := c.Query(field.name); value != "" {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < 0 {
				response.BadRequest(c, "Invalid retention filter")
				return
			}
			*field.target = n
		}
	}
	if value := c.Query("status"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 100 || n > 599 {
			response.BadRequest(c, "Invalid status filter")
			return
		}
		f.Status = n
	}
	for _, field := range []struct {
		name   string
		target *time.Time
	}{{"from", &f.From}, {"to", &f.To}} {
		if value := c.Query(field.name); value != "" {
			v, err := time.Parse(time.RFC3339, value)
			if err != nil {
				response.BadRequest(c, "Invalid retention time")
				return
			}
			*field.target = v
		}
	}
	if len(f.Model) > 256 || len(f.RequestID) > 256 || (!f.From.IsZero() && !f.To.IsZero() && f.From.After(f.To)) {
		response.BadRequest(c, "Invalid retention filter")
		return
	}
	items, err := h.Settings.SmallRequestRecorder().Store.List(c.Request.Context(), f)
	if err != nil {
		captureError(c, err)
		return
	}
	response.Success(c, gin.H{"items": items, "has_more": len(items) == size})
}
func (h *RequestCaptureHandler) SmallDetail(c *gin.Context) {
	if !h.requireSensitiveRead(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("record"), 10, 64)
	if err != nil || id < 1 {
		response.BadRequest(c, "Invalid retention record")
		return
	}
	detail, err := h.Settings.SmallRequestRecorder().Store.Detail(c.Request.Context(), id)
	if err != nil {
		captureError(c, err)
		return
	}
	response.Success(c, detail)
}
func (h *RequestCaptureHandler) SmallClear(c *gin.Context) {
	if !h.requireSensitiveRead(c) {
		return
	}
	r := h.Settings.SmallRequestRecorder()
	r.Invalidate()
	if err := r.Store.Clear(c.Request.Context()); err != nil {
		captureError(c, err)
		return
	}
	response.Success(c, gin.H{"cleared": true})
}
