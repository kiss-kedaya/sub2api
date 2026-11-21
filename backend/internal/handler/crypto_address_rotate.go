package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// AdminRotateAddress keeps the administrative route but prevents rotation until
// the wallet integrity guard and deposit attribution both support retired addresses.
func (h *CryptoHandler) AdminRotateAddress(c *gin.Context) {
	cryptoVaultHeaders(c)
	response.ErrorWithDetails(c, http.StatusConflict, "Deposit address rotation is temporarily unavailable", "DEPOSIT_ADDRESS_ROTATION_UNAVAILABLE", nil)
}
