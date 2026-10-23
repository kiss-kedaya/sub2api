package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// AdminWalletSecurity exposes only lock metadata, under the group's ID1/JWT
// and HTTPS guard. Private material is never included in this response.
func (h *CryptoHandler) AdminWalletSecurity(c *gin.Context) {
	cryptoNoStore(c)
	if h.service == nil {
		response.Error(c, http.StatusServiceUnavailable, "Wallet unavailable")
		return
	}
	status, err := h.service.WalletSecurity(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Wallet security status unavailable")
		return
	}
	response.Success(c, status)
}

// Recovery also consumes the group's fresh, single-use X-Crypto-TOTP code.
// It never enables the deposit channel; enabling remains a separate action.
func (h *CryptoHandler) AdminRecoverWalletSecurity(c *gin.Context) {
	cryptoNoStore(c)
	var req struct {
		Confirmation string `json:"confirmation"`
	}
	if !decodeCryptoRequest(c, &req, 4<<10, "Invalid recovery request") {
		return
	}
	if req.Confirmation != "RECOVER" {
		response.BadRequest(c, "Explicit recovery confirmation is required")
		return
	}
	if h.service == nil {
		response.Error(c, http.StatusServiceUnavailable, "Wallet unavailable")
		return
	}
	status, err := h.service.RecoverWalletSecurity(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusConflict, "Recovery refused; repair wallet address integrity before retrying")
		return
	}
	response.Success(c, status)
}
