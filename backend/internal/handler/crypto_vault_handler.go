package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func cryptoVaultHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Expires", "0")
	c.Header("X-Content-Type-Options", "nosniff")
}

// Import is protected by the mandatory fresh Crypto 2FA middleware.
func (h *CryptoHandler) AdminImportHotMnemonic(c *gin.Context) {
	cryptoVaultHeaders(c)
	var req struct {
		HotMnemonic string `json:"hot_mnemonic"`
	}
	if !decodeCryptoVaultRequest(c, &req) {
		return
	}
	if strings.TrimSpace(req.HotMnemonic) == "" || len(req.HotMnemonic) > 1024 {
		response.BadRequest(c, "A valid hot-wallet mnemonic is required")
		return
	}
	if h.service == nil {
		response.Error(c, http.StatusServiceUnavailable, "Wallet unavailable")
		return
	}
	err := h.service.ImportHotMnemonic(c.Request.Context(), req.HotMnemonic)
	req.HotMnemonic = ""
	if err != nil {
		response.Error(c, http.StatusConflict, "Hot-wallet import refused; check the wallet configuration and unresolved sweeps")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"saved": true}})
}

func decodeCryptoVaultRequest(c *gin.Context, target any) bool {
	return decodeCryptoRequest(c, target, 4<<10, "Invalid vault request")
}

// Reject trailing JSON and unknown fields before invoking any payment action.
// Keep body limits local to these endpoints so unrelated gateway traffic is
// unaffected. The larger config limit accommodates multiple RPC endpoints.
func decodeCryptoRequest(c *gin.Context, target any, limit int64, message string) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		response.BadRequest(c, message)
		return false
	}
	return true
}
