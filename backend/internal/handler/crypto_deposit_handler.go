package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/cryptopay"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

// CryptoDepositHandler serves the standalone deposit page: per-chain
// addresses, the credit rate and the user's own deposit history.
type CryptoDepositHandler struct {
	service *cryptopay.Service
}

func NewCryptoDepositHandler(service *cryptopay.Service) *CryptoDepositHandler {
	return &CryptoDepositHandler{service: service}
}

// GetDepositInfo returns the addresses, chains, rate and limits the page
// needs in one call.
// GET /api/v1/crypto/deposit-info
func (h *CryptoDepositHandler) GetDepositInfo(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	info, err := h.service.DepositInfo(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": info})
}

// GetDepositHistory lists the caller's own deposits.
// GET /api/v1/crypto/deposit-history
func (h *CryptoDepositHandler) GetDepositHistory(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	records, err := h.service.UserDepositHistory(c.Request.Context(), subject.UserID, 50)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": records})
}
