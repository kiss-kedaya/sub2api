package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// AdminRunScan requires fresh payment 2FA through the entire admin route group.
func (h *CryptoHandler) AdminRunScan(c *gin.Context) {
	cryptoVaultHeaders(c)
	var request struct{}
	if !decodeCryptoRequest(c, &request, 4<<10, "Invalid scan request") {
		return
	}
	if h.service == nil {
		response.Error(c, http.StatusServiceUnavailable, "Payment scanner unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	if err := h.service.RunScan(ctx); err != nil {
		if errors.Is(err, cryptopay.ErrScanBusy) {
			response.Error(c, http.StatusConflict, "Another payment operation is running; retry later")
			return
		}
		slog.WarnContext(ctx, "[cryptopay] manual scan failed", "error", err)
		response.Error(c, http.StatusServiceUnavailable, "Payment scan did not complete; check deposit records before retrying")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"completed": true}})
}
